package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/soi/doctorx/core/internal/blockdev"
	"github.com/soi/doctorx/core/internal/rescue"
)

func cmdCopy(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("copy", flag.ExitOnError)
	dest := fs.String("dest", "", "thư mục đích (mặc định: ~/DoctorX Rescued/<nhãn ổ>-<thời điểm>)")
	flags, positional := splitArgs(args)
	fs.Parse(flags)
	if len(positional) < 2 {
		return fmt.Errorf("cú pháp: doctorx-core copy <bsd> <đường/dẫn> [...] [-dest DIR]")
	}

	part, err := findPartition(ctx, positional[0])
	if err != nil {
		return err
	}
	if part.MountPoint == "" {
		return fmt.Errorf("phân vùng %s chưa được gắn — hãy gắn ổ rồi thử lại", part.BSD)
	}
	if *dest == "" {
		home, _ := os.UserHomeDir()
		*dest = rescue.DefaultDest(home, part.Label, time.Now())
	}

	fmt.Printf("Sao chép sang: %s\n\n", *dest)
	last := ""
	res, err := rescue.CopyOut(ctx, rescue.CopyRequest{
		SourceMount: part.MountPoint,
		Paths:       positional[1:],
		Dest:        *dest,
	}, func(file string, done, total int64, filesDone int) {
		if file != last {
			last = file
			fmt.Printf("\r  [%d] %s%s", filesDone+1, filepath.Base(file), strings.Repeat(" ", 20))
		}
	})
	fmt.Println()
	if err != nil {
		return err
	}
	fmt.Printf("\nXong: %d file, %s\n", res.FilesCopied, humanSize(res.BytesCopied))
	if len(res.Skipped) > 0 {
		fmt.Printf("%d mục không sao chép được trọn vẹn:\n", len(res.Skipped))
		for _, s := range res.Skipped {
			fmt.Printf("  %s — %s\n", s.Path, s.Reason)
		}
	}
	return nil
}

func cmdUnhide(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("unhide", flag.ExitOnError)
	recursive := fs.Bool("recursive", false, "gỡ cờ ẩn cho toàn bộ cây con")
	yes := fs.Bool("y", false, "không hỏi xác nhận")
	flags, positional := splitArgs(args)
	fs.Parse(flags)
	if len(positional) < 2 {
		return fmt.Errorf("cú pháp: sudo doctorx-core unhide <bsd> <đường/dẫn> [-recursive]")
	}
	bsd, target := positional[0], rescue.TrimVolumePath(positional[1])

	// File ảnh ghi được bằng quyền người dùng; chỉ thiết bị thật mới cần root.
	if !isImageFile(bsd) && os.Geteuid() != 0 {
		return fmt.Errorf("thao tác này ghi trực tiếp xuống thiết bị nên cần quyền quản trị — chạy lại với sudo")
	}
	if rescue.PathIsProtected(target) {
		return fmt.Errorf("%q là thư mục hệ thống, DoctorX không sửa vào đó", target)
	}

	part, err := findPartition(ctx, bsd)
	if err != nil {
		return err
	}
	if !*yes {
		fmt.Printf("Sắp gỡ cờ ẩn cho %q trên %s (%s).\n", target, part.BSD, part.FSType)
		if *recursive {
			fmt.Println("Chế độ đệ quy: áp dụng cho toàn bộ cây con.")
		}
		fmt.Print("Ổ sẽ được tháo tạm rồi gắn lại. Tiếp tục? [y/N] ")
		var ans string
		fmt.Scanln(&ans)
		if !strings.EqualFold(strings.TrimSpace(ans), "y") {
			fmt.Println("Đã huỷ.")
			return nil
		}
	}

	// macOS chặn ghi xuống thiết bị khi volume còn mount.
	wasMounted := part.MountPoint != ""
	if wasMounted {
		fmt.Printf("Tháo %s...\n", part.BSD)
		if err := blockdev.Unmount(ctx, part.BSD); err != nil {
			return err
		}
	}
	remount := func() {
		if wasMounted {
			if err := blockdev.Mount(ctx, part.BSD); err != nil {
				fmt.Fprintf(os.Stderr, "Cảnh báo: không gắn lại được ổ: %v\n", err)
			}
		}
	}
	defer remount()

	sess, err := openTarget(bsd, true)
	if err != nil {
		return err
	}
	defer sess.Close()

	jnlDir, err := journalDir()
	if err != nil {
		return err
	}
	res, err := rescue.Unhide(ctx, sess.Volume, sess.File, sess.Reader, rescue.UnhideRequest{
		Path:        target,
		Recursive:   *recursive,
		JournalDir:  jnlDir,
		JournalID:   newJournalID(),
		DeviceBSD:   part.BSD,
		VolumeLabel: part.Label,
	}, func(scanned int, current string) {
		fmt.Printf("\r  đã duyệt %d mục...", scanned)
	})
	fmt.Println()
	if err != nil {
		return err
	}

	fmt.Printf("\nXong: %d/%d mục được gỡ cờ ẩn, ghi %d block.\n",
		res.EntriesChanged, res.EntriesScanned, res.BlocksWritten)
	if res.JournalID != "" {
		fmt.Printf("Muốn hoàn tác: sudo doctorx-core rollback %s %s\n", res.JournalID, part.BSD)
	}
	for _, s := range res.Skipped {
		fmt.Printf("  bỏ qua %s — %s\n", s.Path, s.Reason)
	}
	return nil
}

func cmdRollback(ctx context.Context, args []string) error {
	if len(args) < 2 {
		return fmt.Errorf("cú pháp: sudo doctorx-core rollback <journal-id> <bsd>")
	}
	if !isImageFile(args[1]) && os.Geteuid() != 0 {
		return fmt.Errorf("hoàn tác ghi xuống thiết bị nên cần quyền quản trị — chạy lại với sudo")
	}
	dir, err := journalDir()
	if err != nil {
		return err
	}
	path := filepath.Join(dir, args[0]+".jnl")
	if _, err := os.Stat(path); err != nil {
		return fmt.Errorf("không thấy journal %s: %w", path, err)
	}

	part, err := findPartition(ctx, args[1])
	if err != nil {
		return err
	}
	wasMounted := part.MountPoint != ""
	if wasMounted {
		if err := blockdev.Unmount(ctx, part.BSD); err != nil {
			return err
		}
		defer blockdev.Mount(ctx, part.BSD)
	}

	sess, err := openTarget(part.BSD, true)
	if err != nil {
		return err
	}
	defer sess.Close()

	n, err := blockdev.Rollback(sess.File, path)
	if err != nil {
		return err
	}
	fmt.Printf("Đã hoàn tác %d block về trạng thái trước khi sửa.\n", n)
	return nil
}
