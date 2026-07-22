package rescue

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/soi/doctorx/core/internal/blockdev"
	dfs "github.com/soi/doctorx/core/internal/fs"
	"github.com/soi/doctorx/core/internal/guard"
)

// UnhideRequest mô tả một lần Quick Restore.
type UnhideRequest struct {
	// Path là mục cần khôi phục, tính từ gốc volume.
	Path string
	// Recursive gỡ cờ ẩn cho toàn bộ cây con — tương đương `attrib -h -s /s /d`.
	Recursive bool
	// JournalDir là nơi lưu bản gốc để hoàn tác.
	JournalDir string
	// JournalID do tầng gọi sinh (thường là timestamp), dùng làm tên file.
	JournalID string
	// DeviceBSD và VolumeLabel chỉ để ghi vào header journal.
	DeviceBSD   string
	VolumeLabel string
}

// UnhideResult tổng kết một lần Quick Restore.
type UnhideResult struct {
	EntriesChanged int       `json:"entriesChanged"`
	EntriesScanned int       `json:"entriesScanned"`
	BlocksWritten  int       `json:"blocksWritten"`
	JournalID      string    `json:"journalId"`
	Skipped        []Skipped `json:"skipped"`
}

// UnhideProgress báo tiến trình duyệt cây.
type UnhideProgress func(scanned int, current string)

// Unhide gỡ bit Hidden và System khỏi một mục và (tuỳ chọn) toàn bộ cây con.
//
// Đây là bản chuyển của `attrib -h -s /s /d` từ script .bat gốc, khác ở ba
// điểm quan trọng:
//   - Không cần takeown/icacls: DoctorX ghi thẳng byte với quyền root, không đi
//     qua tầng ACL của Windows.
//   - Danh sách thư mục cấm được enforce trong code cho MỌI mục trong cây, thay
//     vì in cảnh báo rồi để người dùng tự cân nhắc.
//   - Mọi thay đổi được ghi journal trước, hoàn tác được nguyên khối.
//
// Volume phải đã được tháo mount trước khi gọi.
func Unhide(ctx context.Context, vol dfs.Volume, dev blockdev.ReadWriterAt, rd *blockdev.Reader,
	req UnhideRequest, prog UnhideProgress) (*UnhideResult, error) {

	if ok, why := vol.Writable(); !ok {
		return nil, fmt.Errorf("%w: %s", dfs.ErrNotWritable, why)
	}
	// Chặn ngay từ đầu, trước cả khi mở đường ghi.
	if err := guard.AllowWrite(req.Path); err != nil {
		return nil, err
	}

	res := &UnhideResult{}
	var patches []dfs.Patch

	collect := func(e *dfs.Entry) error {
		res.EntriesScanned++
		if prog != nil && res.EntriesScanned%256 == 0 {
			prog(res.EntriesScanned, e.Path)
		}
		if !e.Attrs.IsConcealed() {
			return nil
		}
		// File phụ trợ của macOS (._*) không phải dữ liệu người dùng; gỡ cờ ẩn
		// chỉ tạo ra một đống file rác lộ thiên khi cắm sang Windows.
		if guard.IsMacOSMetadata(e.Path) {
			return nil
		}
		// Kiểm tra lại cho từng mục trong cây, không chỉ mục gốc được chọn.
		if err := guard.AllowWrite(e.Path); err != nil {
			res.Skipped = append(res.Skipped, Skipped{Path: e.Path, Reason: err.Error()})
			return nil
		}
		ps, err := vol.ClearAttrs(e, dfs.AttrHiddenSystem)
		if err != nil {
			res.Skipped = append(res.Skipped, Skipped{Path: e.Path, Reason: err.Error()})
			return nil
		}
		if len(ps) == 0 {
			return nil
		}
		patches = append(patches, ps...)
		res.EntriesChanged++
		return nil
	}

	// Mục gốc được chọn phải được xử lý dù nó là file hay thư mục.
	root, err := vol.Stat(ctx, req.Path)
	if err != nil {
		return nil, err
	}
	if err := collect(root); err != nil {
		return nil, err
	}

	if req.Recursive && root.Attrs.IsDir() {
		opt := dfs.WalkOpt{Root: req.Path}
		if err := vol.Walk(ctx, opt, collect); err != nil {
			return nil, fmt.Errorf("duyệt cây %q: %w", req.Path, err)
		}
	}

	if len(patches) == 0 {
		res.JournalID = ""
		return res, nil
	}

	jnl, err := blockdev.NewJournal(req.JournalDir, blockdev.JournalMeta{
		ID:          req.JournalID,
		CreatedAt:   nowFunc(),
		DeviceBSD:   req.DeviceBSD,
		VolumeLabel: req.VolumeLabel,
		Operation:   describeOp(req),
		BlockSize:   rd.BlockSize(),
	})
	if err != nil {
		return nil, err
	}
	defer jnl.Close()

	w := blockdev.NewWriter(dev, rd, vol.MetadataRanges(), jnl)
	if err := w.Apply(patches); err != nil {
		return nil, fmt.Errorf("ghi thay đổi: %w", err)
	}

	res.JournalID = jnl.ID()
	res.BlocksWritten = countBlocks(patches, rd.BlockSize())
	return res, nil
}

func describeOp(req UnhideRequest) string {
	mode := "một mục"
	if req.Recursive {
		mode = "đệ quy"
	}
	return fmt.Sprintf("gỡ cờ ẩn (%s): %s", mode, req.Path)
}

func countBlocks(patches []dfs.Patch, blockSize int64) int {
	seen := map[int64]bool{}
	for _, p := range patches {
		seen[p.Offset/blockSize] = true
	}
	return len(seen)
}

// ListRootHiddenDirs liệt kê các thư mục bị giấu ở cấp gốc volume — đúng bước
// `dir /b /a:hd` của script .bat, nhưng luôn tức thì vì chỉ duyệt một cấp bất
// kể ổ lớn cỡ nào.
func ListRootHiddenDirs(ctx context.Context, vol dfs.Volume) ([]dfs.Entry, error) {
	var out []dfs.Entry
	opt := dfs.WalkOpt{Root: "/", MaxDepth: 1, DirsOnly: true}
	err := vol.Walk(ctx, opt, func(e *dfs.Entry) error {
		if e.Attrs.IsConcealed() {
			cp := *e
			out = append(out, cp)
		}
		return nil
	})
	if err != nil && !errors.Is(err, context.Canceled) {
		return out, err
	}
	return out, nil
}

// PathIsProtected cho tầng IPC hỏi trước khi hiện nút cho người dùng bấm.
func PathIsProtected(path string) bool { return guard.IsProtected(path) }

// TrimVolumePath chuẩn hoá đường dẫn về dạng "/a/b" mà driver mong đợi.
func TrimVolumePath(p string) string {
	p = strings.TrimSpace(p)
	if p == "" {
		return "/"
	}
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	return p
}
