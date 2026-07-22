package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"strconv"
	"time"

	"github.com/soi/doctorx/core/internal/blockdev"
	dfs "github.com/soi/doctorx/core/internal/fs"
	"github.com/soi/doctorx/core/internal/fsprobe"
	"github.com/soi/doctorx/core/internal/imaging"
	"github.com/soi/doctorx/core/internal/ipc"
	"github.com/soi/doctorx/core/internal/rescue"
	"github.com/soi/doctorx/core/internal/scan"
)

const defaultSocket = "/var/run/doctorx.sock"

func cmdServe(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	socket := fs.String("socket", defaultSocket, "đường dẫn Unix socket")
	owner := fs.String("owner-uid", "", "UID được phép kết nối (mặc định: người chạy sudo)")
	fs.Parse(args)

	uid := -1
	switch {
	case *owner != "":
		v, err := strconv.Atoi(*owner)
		if err != nil {
			return fmt.Errorf("owner-uid không hợp lệ: %w", err)
		}
		uid = v
	case os.Getenv("SUDO_UID") != "":
		uid, _ = strconv.Atoi(os.Getenv("SUDO_UID"))
	}

	srv := ipc.NewServer(*socket)
	registerHandlers(srv)
	if err := srv.Listen(uid); err != nil {
		return err
	}
	defer srv.Close()

	log.Printf("doctorx-core đang phục vụ tại %s (uid được phép: %d)", *socket, uid)
	// Dọn journal cũ mỗi lần khởi động; giữ 30 ngày là đủ để người dùng nhận ra
	// mình muốn hoàn tác.
	if dir, err := journalDir(); err == nil {
		blockdev.PruneJournals(dir, 30*24*time.Hour)
	}
	return srv.Serve(ctx)
}

func registerHandlers(srv *ipc.Server) {
	srv.Handle("ping", func(ctx context.Context, _ json.RawMessage, _ func(string, any)) (any, error) {
		return map[string]any{"ok": true, "euid": os.Geteuid()}, nil
	})

	srv.Handle("list_devices", func(ctx context.Context, _ json.RawMessage, _ func(string, any)) (any, error) {
		disks, err := blockdev.ListExternalDisks(ctx)
		if err != nil {
			return nil, ipc.Errf(ipc.CodeIO, "%v", err)
		}
		return map[string]any{"disks": disks}, nil
	})

	srv.Handle("scan_volume", handleScanVolume)
	srv.Handle("list_hidden_dirs", handleListHiddenDirs)
	srv.Handle("rescue_copy_out", handleCopyOut)
	srv.Handle("rescue_unhide", handleUnhide)
	srv.Handle("flash_preflight", handleFlashPreflight)
	srv.Handle("flash_image", handleFlashImage)
	srv.Handle("format_disk", handleFormatDisk)
	srv.Handle("check_bad_blocks", handleCheckBadBlocks)
}

func handleFlashPreflight(ctx context.Context, raw json.RawMessage, _ func(string, any)) (any, error) {
	var p struct {
		BSD string `json:"bsd"`
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, ipc.Errf(ipc.CodeBadRequest, "tham số không hợp lệ: %v", err)
	}
	target, err := imaging.Preflight(ctx, p.BSD)
	if err != nil {
		return nil, ipc.Errf(ipc.CodeProtected, "%v", err)
	}
	return target, nil
}

func handleFlashImage(ctx context.Context, raw json.RawMessage, emit func(string, any)) (any, error) {
	var req imaging.FlashRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return nil, ipc.Errf(ipc.CodeBadRequest, "tham số không hợp lệ: %v", err)
	}
	res, err := imaging.Flash(ctx, req, func(done, total int64) {
		emit("progress", map[string]any{"doneBytes": done, "totalBytes": total})
	})
	if err != nil {
		// Trả cả res (nếu có) để UI biết đã ghi tới đâu khi verify thất bại.
		return res, ipc.Errf(ipc.CodeIO, "%v", err)
	}
	return res, nil
}

func handleFormatDisk(ctx context.Context, raw json.RawMessage, _ func(string, any)) (any, error) {
	var req imaging.FormatRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return nil, ipc.Errf(ipc.CodeBadRequest, "tham số không hợp lệ: %v", err)
	}
	res, err := imaging.Format(ctx, req)
	if err != nil {
		return nil, ipc.Errf(ipc.CodeIO, "%v", err)
	}
	return res, nil
}

func handleCheckBadBlocks(ctx context.Context, raw json.RawMessage, emit func(string, any)) (any, error) {
	var req imaging.BadBlocksRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return nil, ipc.Errf(ipc.CodeBadRequest, "tham số không hợp lệ: %v", err)
	}
	res, err := imaging.CheckBadBlocks(ctx, req, func(done, total int64) {
		emit("progress", map[string]any{"doneBytes": done, "totalBytes": total})
	})
	if err != nil {
		return res, ipc.Errf(ipc.CodeIO, "%v", err)
	}
	return res, nil
}

type volumeParams struct {
	BSD string `json:"bsd"`
}

func handleScanVolume(ctx context.Context, raw json.RawMessage, emit func(string, any)) (any, error) {
	var p volumeParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, ipc.Errf(ipc.CodeBadRequest, "tham số không hợp lệ: %v", err)
	}
	part, err := findPartition(ctx, p.BSD)
	if err != nil {
		return nil, ipc.Errf(ipc.CodeNotFound, "%v", err)
	}

	var concealed []scan.Concealed
	sess, openErr := fsprobe.Open(p.BSD, fsprobe.OpenOpts{})
	if openErr == nil {
		defer sess.Close()
		concealed, err = scan.FindConcealedRaw(ctx, sess.Volume, dfs.WalkOpt{})
	} else if part.MountPoint != "" {
		// Filesystem chưa có driver raw: vẫn quét được qua mount, đánh đổi là
		// không thấy được bit System.
		concealed, err = scan.FindConcealedMounted(ctx, part.MountPoint, 0)
	} else {
		return nil, ipc.Errf(ipc.CodeUnsupported, "%v", openErr)
	}
	if err != nil {
		return nil, ipc.Errf(ipc.CodeIO, "%v", err)
	}

	var worm *scan.WormReport
	if part.MountPoint != "" {
		worm, _ = scan.ScanWorm(ctx, part.MountPoint, concealed)
	}
	return map[string]any{
		"partition":     part,
		"concealed":     concealed,
		"worm":          worm,
		"rawDriver":     openErr == nil,
		"systemBitSeen": openErr == nil,
	}, nil
}

func handleListHiddenDirs(ctx context.Context, raw json.RawMessage, _ func(string, any)) (any, error) {
	var p volumeParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, ipc.Errf(ipc.CodeBadRequest, "tham số không hợp lệ: %v", err)
	}
	sess, err := fsprobe.Open(p.BSD, fsprobe.OpenOpts{})
	if err != nil {
		return nil, ipc.Errf(ipc.CodeUnsupported, "%v", err)
	}
	defer sess.Close()

	entries, err := rescue.ListRootHiddenDirs(ctx, sess.Volume)
	if err != nil {
		return nil, ipc.Errf(ipc.CodeIO, "%v", err)
	}
	type dirOut struct {
		Path      string   `json:"path"`
		Attrs     []string `json:"attrs"`
		Protected bool     `json:"protected"`
	}
	out := make([]dirOut, 0, len(entries))
	for _, e := range entries {
		out = append(out, dirOut{e.Path, e.Attrs.Names(), rescue.PathIsProtected(e.Path)})
	}
	return map[string]any{"dirs": out}, nil
}

func handleCopyOut(ctx context.Context, raw json.RawMessage, emit func(string, any)) (any, error) {
	var p struct {
		BSD   string   `json:"bsd"`
		Paths []string `json:"paths"`
		Dest  string   `json:"dest"`
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, ipc.Errf(ipc.CodeBadRequest, "tham số không hợp lệ: %v", err)
	}
	part, err := findPartition(ctx, p.BSD)
	if err != nil {
		return nil, ipc.Errf(ipc.CodeNotFound, "%v", err)
	}
	if part.MountPoint == "" {
		return nil, ipc.Errf(ipc.CodeBadRequest, "phân vùng %s chưa được gắn", part.BSD)
	}
	if p.Dest == "" {
		home, _ := os.UserHomeDir()
		p.Dest = rescue.DefaultDest(home, part.Label, time.Now())
	}

	res, err := rescue.CopyOut(ctx, rescue.CopyRequest{
		SourceMount: part.MountPoint, Paths: p.Paths, Dest: p.Dest,
	}, func(file string, done, total int64, filesDone int) {
		emit("progress", map[string]any{
			"file": file, "doneBytes": done, "totalBytes": total, "filesDone": filesDone,
		})
	})
	if err != nil {
		return res, ipc.Errf(ipc.CodeIO, "%v", err)
	}
	res.RenameMap = p.Dest
	return res, nil
}

func handleUnhide(ctx context.Context, raw json.RawMessage, emit func(string, any)) (any, error) {
	var p struct {
		BSD       string `json:"bsd"`
		Path      string `json:"path"`
		Recursive bool   `json:"recursive"`
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, ipc.Errf(ipc.CodeBadRequest, "tham số không hợp lệ: %v", err)
	}
	target := rescue.TrimVolumePath(p.Path)
	if rescue.PathIsProtected(target) {
		return nil, ipc.Errf(ipc.CodeProtected, "%q là thư mục hệ thống, DoctorX không sửa vào đó", target)
	}
	part, err := findPartition(ctx, p.BSD)
	if err != nil {
		return nil, ipc.Errf(ipc.CodeNotFound, "%v", err)
	}

	wasMounted := part.MountPoint != ""
	if wasMounted {
		emit("status", map[string]any{"phase": "unmount"})
		if err := blockdev.Unmount(ctx, part.BSD); err != nil {
			return nil, ipc.Errf(ipc.CodeIO, "%v", err)
		}
		defer func() {
			emit("status", map[string]any{"phase": "remount"})
			if err := blockdev.Mount(ctx, part.BSD); err != nil {
				log.Printf("gắn lại %s thất bại: %v", part.BSD, err)
			}
		}()
	}

	sess, err := fsprobe.Open(part.BSD, fsprobe.OpenOpts{Write: true})
	if err != nil {
		return nil, ipc.Errf(ipc.CodeUnsupported, "%v", err)
	}
	defer sess.Close()

	dir, err := journalDir()
	if err != nil {
		return nil, ipc.Errf(ipc.CodeInternal, "%v", err)
	}
	res, err := rescue.Unhide(ctx, sess.Volume, sess.File, sess.Reader, rescue.UnhideRequest{
		Path:        target,
		Recursive:   p.Recursive,
		JournalDir:  dir,
		JournalID:   newJournalID(),
		DeviceBSD:   part.BSD,
		VolumeLabel: part.Label,
	}, func(scanned int, current string) {
		emit("progress", map[string]any{"scanned": scanned, "current": current})
	})
	if err != nil {
		return nil, ipc.Errf(ipc.CodeIO, "%v", err)
	}
	return res, nil
}
