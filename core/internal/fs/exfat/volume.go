// Package exfat cài đặt fs.Volume cho exFAT: chỉ đọc metadata (boot sector,
// FAT chain, directory entry) và sinh Patch để gỡ attribute Hidden/System.
// Không đọc nội dung file — xem package fs để biết lý do.
package exfat

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"path"
	"strings"
	"sync"
	"unicode/utf16"

	"github.com/soi/doctorx/core/internal/blockdev"
	"github.com/soi/doctorx/core/internal/fs"
	"github.com/soi/doctorx/core/internal/guard"
)

// Volume là cài đặt fs.Volume cho exFAT.
type Volume struct {
	rd    *blockdev.Reader
	boot  *bootSector
	label string

	mu      sync.Mutex
	ranges  *guard.RangeSet
	seenDir map[uint32]bool // cluster thư mục đã khai báo vào ranges
}

// entryPrivate là dữ liệu riêng gắn vào fs.Entry.FSPrivate — đủ để ClearAttrs
// định vị lại entry set trên device mà không cần Walk lại toàn bộ cây.
type entryPrivate struct {
	start          int64
	secondaryCount int
}

// dirRef định danh một thư mục (root hoặc thư mục con) để mở lại chain
// cluster của nó.
type dirRef struct {
	first      uint32
	noFatChain bool
	dataLength uint64
}

// errStopScan dùng nội bộ để dừng sớm khi quét thư mục tìm một tên cụ thể.
var errStopScan = errors.New("đã tìm thấy, dừng quét")

// Open đọc boot sector và xác thực hình học volume, trả về Volume sẵn sàng
// duyệt metadata.
func Open(rd *blockdev.Reader) (*Volume, error) {
	buf := make([]byte, bootSectorSize)
	if _, err := rd.ReadAt(buf, 0); err != nil {
		return nil, fmt.Errorf("%w: đọc boot sector: %v", fs.ErrCorrupt, err)
	}
	boot, err := parseBootSector(buf)
	if err != nil {
		return nil, err
	}
	if err := validateGeometry(rd, boot); err != nil {
		return nil, err
	}

	v := &Volume{
		rd:      rd,
		boot:    boot,
		ranges:  guard.NewRangeSet(),
		seenDir: make(map[uint32]bool),
	}

	label, err := v.readLabel()
	if err != nil {
		return nil, err
	}
	v.label = label

	return v, nil
}

// validateGeometry chặn boot sector khai báo vùng nằm ngoài dung lượng thật
// của device — dấu hiệu image bị truncate hoặc hỏng.
func validateGeometry(rd *blockdev.Reader, b *bootSector) error {
	size := rd.Size()
	if size <= 0 {
		return nil // kích thước không xác định (vd. stream) — bỏ qua kiểm tra
	}
	bps := int64(b.BytesPerSector())
	fatEnd := int64(b.FatOffset)*bps + int64(b.FatLength)*bps
	if fatEnd > size {
		return fmt.Errorf("%w: bảng FAT [offset %d) vượt quá dung lượng ảnh %d byte", fs.ErrCorrupt, fatEnd, size)
	}
	heapEnd := int64(b.ClusterHeapOffset)*bps + int64(b.ClusterCount)*int64(b.BytesPerCluster())
	if heapEnd > size {
		return fmt.Errorf("%w: cluster heap [offset %d) vượt quá dung lượng ảnh %d byte", fs.ErrCorrupt, heapEnd, size)
	}
	return nil
}

// dirClusters trả về danh sách cluster của một thư mục. Root directory (và
// bất cứ khi nào không biết trước độ dài) luôn đi theo FAT chain tới EOF, vì
// root không có Stream Extension khai báo NoFatChain/DataLength. Thư mục con
// dùng NoFatChain + DataLength lấy từ entry set của chính nó.
func (v *Volume) dirClusters(ref dirRef) ([]uint32, error) {
	if !ref.noFatChain {
		return followFATChain(v.rd, v.boot, ref.first)
	}
	clusterSize := uint64(v.boot.BytesPerCluster())
	count := (ref.dataLength + clusterSize - 1) / clusterSize
	if count == 0 {
		return nil, nil
	}
	if !v.boot.validCluster(ref.first) || uint64(ref.first)+count-firstValidCluster > uint64(v.boot.ClusterCount) {
		return nil, fmt.Errorf("%w: chuỗi cluster liên tiếp vượt phạm vi (first=%d, count=%d)", fs.ErrCorrupt, ref.first, count)
	}
	return contiguousChain(ref.first, uint32(count)), nil
}

// declareClusters khai báo các cluster đã đọc vào MetadataRanges, mỗi cluster
// chỉ khai báo một lần.
func (v *Volume) declareClusters(clusters []uint32) {
	v.mu.Lock()
	defer v.mu.Unlock()
	for _, c := range clusters {
		if v.seenDir[c] {
			continue
		}
		v.seenDir[c] = true
		off := clusterOffset(v.boot, c)
		v.ranges.Add(off, off+int64(v.boot.BytesPerCluster()), "exfat-dir")
	}
}

// scanDir đọc toàn bộ entry set InUse trong thư mục ref, khai báo cluster đã
// đọc vào MetadataRanges, và gọi fn cho từng entry set.
func (v *Volume) scanDir(ref dirRef, fn func(dirEntrySet) error) error {
	clusters, err := v.dirClusters(ref)
	if err != nil {
		return err
	}
	v.declareClusters(clusters)
	dr := newDirEntryReader(v.rd, v.boot, clusters)
	return decodeEntrySets(dr, fn)
}

// findChild tìm entry set tên `name` (so khớp không phân biệt hoa/thường)
// trực tiếp trong thư mục ref, dừng quét ngay khi tìm thấy.
func (v *Volume) findChild(ref dirRef, name string) (*dirEntrySet, error) {
	var found *dirEntrySet
	err := v.scanDir(ref, func(set dirEntrySet) error {
		if strings.EqualFold(set.Name, name) {
			s := set
			found = &s
			return errStopScan
		}
		return nil
	})
	if err != nil && !errors.Is(err, errStopScan) {
		return nil, err
	}
	return found, nil
}

// cleanPathParts chuẩn hoá path kiểu "/" thành các thành phần không rỗng.
// "" và "/" trả về nil (gốc volume).
func cleanPathParts(p string) []string {
	clean := strings.Trim(path.Clean("/"+p), "/")
	if clean == "" || clean == "." {
		return nil
	}
	return strings.Split(clean, "/")
}

// resolveDirParts đi từ root theo từng thành phần path, trả về dirRef của thư
// mục đích cùng đường dẫn chuẩn hoá của nó.
func (v *Volume) resolveDirParts(ctx context.Context, parts []string) (dirRef, string, error) {
	cur := dirRef{first: v.boot.FirstClusterOfRootDirectory}
	curPath := "/"
	for _, part := range parts {
		select {
		case <-ctx.Done():
			return dirRef{}, "", ctx.Err()
		default:
		}
		found, err := v.findChild(cur, part)
		if err != nil {
			return dirRef{}, "", err
		}
		next := path.Join(curPath, part)
		if found == nil {
			return dirRef{}, "", fmt.Errorf("%w: %s", fs.ErrNotFound, next)
		}
		if !fs.Attr(found.Attrs).IsDir() {
			return dirRef{}, "", fmt.Errorf("%w: %s không phải thư mục", fs.ErrNotFound, next)
		}
		cur = dirRef{first: found.FirstCluster, noFatChain: found.NoFatChain, dataLength: found.DataLength}
		curPath = next
	}
	return cur, curPath, nil
}

// readLabel tìm Volume Label Directory Entry (0x83) trong root directory.
// Không tìm thấy nghĩa là volume không đặt nhãn — hợp lệ, trả chuỗi rỗng.
func (v *Volume) readLabel() (string, error) {
	root := dirRef{first: v.boot.FirstClusterOfRootDirectory}
	clusters, err := v.dirClusters(root)
	if err != nil {
		return "", err
	}
	v.declareClusters(clusters)
	dr := newDirEntryReader(v.rd, v.boot, clusters)
	for {
		raw, _, ok, err := dr.next()
		if err != nil {
			return "", err
		}
		if !ok {
			return "", nil
		}
		if raw[0] != entryTypeVolumeLabel {
			continue
		}
		n := int(raw[1])
		if n > 11 {
			return "", fmt.Errorf("%w: độ dài nhãn volume bất thường: %d", fs.ErrCorrupt, n)
		}
		units := make([]uint16, n)
		for i := 0; i < n; i++ {
			units[i] = binary.LittleEndian.Uint16(raw[2+i*2 : 4+i*2])
		}
		return string(utf16.Decode(units)), nil
	}
}

// buildEntry dựng fs.Entry từ một entry set đã giải mã và path tuyệt đối của
// nó. Thư mục báo Size = 0 — DataLength của thư mục là dung lượng cấp phát
// cho bảng entry con, không phải "kích thước" theo nghĩa người dùng hiểu.
func (v *Volume) buildEntry(set dirEntrySet, p string) *fs.Entry {
	attrs := fs.Attr(set.Attrs)
	size := int64(set.DataLength)
	if attrs.IsDir() {
		size = 0
	}
	return &fs.Entry{
		Path:     p,
		Size:     size,
		Attrs:    attrs,
		Modified: set.Modified,
		Locs: []fs.EntryLoc{
			{Offset: set.Start + 4, Width: 2, Region: "exfat-dir-attr"},
			{Offset: set.Start + 2, Width: 2, Region: "exfat-dir-checksum"},
		},
		FSPrivate: entryPrivate{start: set.Start, secondaryCount: set.SecondaryCount},
	}
}

// walkDir duyệt DFS thư mục ref, gọi fn cho từng entry theo opt.
func (v *Volume) walkDir(ctx context.Context, ref dirRef, dirPath string, depth int, opt fs.WalkOpt, fn func(*fs.Entry) error) error {
	if depth > fs.MaxPathDepth {
		return fs.ErrDepthLimit
	}
	return v.scanDir(ref, func(set dirEntrySet) error {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		isDir := fs.Attr(set.Attrs).IsDir()
		entryPath := path.Join(dirPath, set.Name)

		if isDir || !opt.DirsOnly {
			if err := fn(v.buildEntry(set, entryPath)); err != nil {
				return err
			}
		}

		if isDir && (opt.MaxDepth == 0 || depth < opt.MaxDepth) {
			child := dirRef{first: set.FirstCluster, noFatChain: set.NoFatChain, dataLength: set.DataLength}
			return v.walkDir(ctx, child, entryPath, depth+1, opt, fn)
		}
		return nil
	})
}

// Info trả về metadata tổng quan của volume.
func (v *Volume) Info() fs.VolumeInfo {
	return fs.VolumeInfo{
		Kind:        fs.KindExFAT,
		Label:       v.label,
		BytesPerSec: v.boot.BytesPerSector(),
		ClusterSize: v.boot.BytesPerCluster(),
		TotalBytes:  int64(v.boot.VolumeLength) * int64(v.boot.BytesPerSector()),
	}
}

// Walk duyệt cây thư mục theo DFS, streaming — không gom vào slice.
func (v *Volume) Walk(ctx context.Context, opt fs.WalkOpt, fn func(*fs.Entry) error) error {
	parts := cleanPathParts(opt.Root)
	root, rootPath, err := v.resolveDirParts(ctx, parts)
	if err != nil {
		return err
	}
	return v.walkDir(ctx, root, rootPath, 1, opt, fn)
}

// Stat trả về đúng một entry theo đường dẫn tuyệt đối kiểu "/".
func (v *Volume) Stat(ctx context.Context, p string) (*fs.Entry, error) {
	parts := cleanPathParts(p)
	if len(parts) == 0 {
		return nil, fmt.Errorf("%w: root directory không có entry riêng để Stat", fs.ErrNotFound)
	}
	parent, parentPath, err := v.resolveDirParts(ctx, parts[:len(parts)-1])
	if err != nil {
		return nil, err
	}
	found, err := v.findChild(parent, parts[len(parts)-1])
	if err != nil {
		return nil, err
	}
	if found == nil {
		return nil, fmt.Errorf("%w: %s", fs.ErrNotFound, p)
	}
	return v.buildEntry(*found, path.Join(parentPath, found.Name)), nil
}

// Writable exFAT do package này ghi luôn cho phép — không có khái niệm dirty
// bit/journal như NTFS.
func (v *Volume) Writable() (bool, string) { return true, "" }

// MetadataRanges trả về các vùng byte đã tích luỹ qua các lần Walk/Stat —
// chỉ gồm cluster thư mục thực sự đã đọc, không khai báo cả cluster heap.
func (v *Volume) MetadataRanges() *guard.RangeSet {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.ranges
}

// ClearAttrs dựng patch gỡ các bit trong mask khỏi FileAttributes của entry.
// Đổi attribute làm SetChecksum của cả entry set thay đổi theo, nên luôn trả
// về hai patch: attribute và checksum. Đọc lại toàn bộ entry set từ device để
// Old là giá trị thật hiện có, tránh ghi đè nhầm nếu device đã đổi từ lúc
// Walk.
func (v *Volume) ClearAttrs(e *fs.Entry, mask fs.Attr) ([]fs.Patch, error) {
	priv, ok := e.FSPrivate.(entryPrivate)
	if !ok {
		return nil, fmt.Errorf("%w: entry không có vị trí exFAT hợp lệ để sửa", fs.ErrCorrupt)
	}

	total := (priv.secondaryCount + 1) * dirEntrySize
	buf := make([]byte, total)
	if _, err := v.rd.ReadAt(buf, priv.start); err != nil {
		return nil, fmt.Errorf("%w: đọc lại entry set tại offset %d: %v", fs.ErrCorrupt, priv.start, err)
	}
	if buf[0] != entryTypeFile {
		return nil, fmt.Errorf("%w: entry tại offset %d không còn là File Directory Entry (có thể đã bị ghi đè)", fs.ErrCorrupt, priv.start)
	}

	oldAttrBytes := append([]byte(nil), buf[4:6]...)
	oldChecksumBytes := append([]byte(nil), buf[2:4]...)

	curAttr := binary.LittleEndian.Uint16(buf[4:6])
	newAttr := curAttr &^ uint16(mask)
	binary.LittleEndian.PutUint16(buf[4:6], newAttr)

	newChecksum := SetChecksum(buf)
	newAttrBytes := make([]byte, 2)
	binary.LittleEndian.PutUint16(newAttrBytes, newAttr)
	newChecksumBytes := make([]byte, 2)
	binary.LittleEndian.PutUint16(newChecksumBytes, newChecksum)

	return []fs.Patch{
		{Offset: priv.start + 4, Old: oldAttrBytes, New: newAttrBytes, Region: "exfat-dir-attr"},
		{Offset: priv.start + 2, Old: oldChecksumBytes, New: newChecksumBytes, Region: "exfat-dir-checksum"},
	}, nil
}
