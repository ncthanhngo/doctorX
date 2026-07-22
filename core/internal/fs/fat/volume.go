// Package fat cài đặt fs.Volume cho FAT12/FAT16/FAT32: chỉ đọc metadata
// (boot sector, bảng FAT, directory entry) và sinh Patch để gỡ attribute.
// Không đọc nội dung file — xem giải thích phạm vi ở package fs.
package fat

import (
	"context"
	"fmt"
	"path"
	"strings"
	"sync"

	"github.com/soi/doctorx/core/internal/blockdev"
	"github.com/soi/doctorx/core/internal/fs"
	"github.com/soi/doctorx/core/internal/guard"
)

// dirLoc xác định vị trí một thư mục để đọc entry: hoặc là root cố định của
// FAT12/16 (fixedRoot), hoặc một chuỗi cluster (mọi thư mục khác, kể cả root
// của FAT32).
type dirLoc struct {
	fixedRoot bool
	cluster   uint32
}

// fatPrivate là dữ liệu riêng của driver gắn vào fs.Entry.FSPrivate.
type fatPrivate struct {
	Cluster uint32
	IsDir   bool
}

// Volume cài đặt fs.Volume cho FAT12/16/32.
type Volume struct {
	rd   *blockdev.Reader
	kind fs.Kind

	bytesPerSector    uint32
	sectorsPerCluster uint32
	clusterSize       uint32
	totalClusters     uint32
	rootCluster       uint32 // 0 nếu FAT12/16

	fatOffset       int64 // byte offset của FAT#0
	rootDirOffset   int64 // byte offset root cố định (FAT12/16)
	rootDirSectors  uint32
	dataStartOffset int64

	label      string
	totalBytes int64

	mu     sync.Mutex
	ranges *guard.RangeSet
}

// Open parse boot sector từ rd và dựng Volume sẵn sàng Walk/Stat.
func Open(rd *blockdev.Reader) (*Volume, error) {
	buf := make([]byte, bootSectorSize)
	if _, err := rd.ReadAt(buf, 0); err != nil {
		return nil, fmt.Errorf("đọc boot sector: %w: %w", fs.ErrCorrupt, err)
	}
	bs, err := parseBootSector(buf)
	if err != nil {
		return nil, err
	}

	v := &Volume{
		rd:                rd,
		kind:              bs.kind,
		bytesPerSector:    bs.bytesPerSector,
		sectorsPerCluster: bs.sectorsPerCluster,
		clusterSize:       bs.bytesPerSector * bs.sectorsPerCluster,
		totalClusters:     bs.totalClusters,
		rootCluster:       bs.rootCluster,
		rootDirSectors:    bs.rootDirSectors,
		label:             bs.label,
		totalBytes:        int64(bs.totalSectors) * int64(bs.bytesPerSector),
		ranges:            guard.NewRangeSet(),
	}
	v.fatOffset = int64(bs.reservedSectors) * int64(bs.bytesPerSector)
	v.rootDirOffset = int64(bs.reservedSectors+bs.numFATs*bs.fatSize) * int64(bs.bytesPerSector)
	v.dataStartOffset = int64(bs.firstDataSector) * int64(bs.bytesPerSector)

	return v, nil
}

func (v *Volume) Info() fs.VolumeInfo {
	return fs.VolumeInfo{
		Kind:        v.kind,
		Label:       v.label,
		BytesPerSec: v.bytesPerSector,
		ClusterSize: v.clusterSize,
		TotalBytes:  v.totalBytes,
	}
}

func (v *Volume) Writable() (bool, string) { return true, "" }

func (v *Volume) MetadataRanges() *guard.RangeSet { return v.ranges }

func (v *Volume) addRange(start, end int64) {
	v.mu.Lock()
	v.ranges.Add(start, end, "fat-dir")
	v.mu.Unlock()
}

func (v *Volume) rootLoc() dirLoc {
	if v.kind == fs.KindFAT32 {
		return dirLoc{cluster: v.rootCluster}
	}
	return dirLoc{fixedRoot: true}
}

func (v *Volume) clusterOffset(c uint32) int64 {
	return v.dataStartOffset + int64(c-2)*int64(v.clusterSize)
}

// normalizePath quy chuẩn về dạng bắt đầu "/", không có "." hay ".." dư thừa.
func normalizePath(p string) string {
	if p == "" {
		return "/"
	}
	return path.Clean("/" + p)
}

func (v *Volume) rootEntry() *fs.Entry {
	return &fs.Entry{
		Path:      "/",
		Size:      0,
		Attrs:     fs.AttrDir,
		Locs:      nil, // root không có entry mô tả nó trong thư mục cha nào cả
		FSPrivate: fatPrivate{Cluster: v.rootCluster, IsDir: true},
	}
}

func (v *Volume) buildEntry(p string, r dirRecord) *fs.Entry {
	return &fs.Entry{
		Path:     p,
		Size:     int64(r.Size),
		Attrs:    fs.Attr(r.Attr),
		Modified: r.MTime,
		Locs: []fs.EntryLoc{{
			Offset: r.AttrOffset,
			Width:  1,
			Region: "fat-dir",
		}},
		FSPrivate: fatPrivate{Cluster: r.Cluster, IsDir: r.IsDir},
	}
}

// readDir đọc toàn bộ entry của một thư mục (root cố định hoặc chuỗi
// cluster), đăng ký vùng byte đã đọc vào MetadataRanges. Streaming ở cấp
// cluster/sector — buffer tạm chỉ giữ một cluster/root region tại một thời
// điểm, danh sách record trả về mới là thứ gom lại.
func (v *Volume) readDir(ctx context.Context, loc dirLoc) ([]dirRecord, error) {
	dec := newDirDecoder()
	var recs []dirRecord

	feedBuf := func(buf []byte, base int64) bool {
		for i := 0; i+32 <= len(buf); i += 32 {
			rec, stop := dec.feed(buf[i:i+32], base+int64(i))
			if stop {
				return true
			}
			if rec != nil {
				recs = append(recs, *rec)
			}
		}
		return false
	}

	if loc.fixedRoot {
		n := int64(v.rootDirSectors) * int64(v.bytesPerSector)
		if n == 0 {
			return recs, nil
		}
		buf := make([]byte, n)
		if _, err := v.rd.ReadAt(buf, v.rootDirOffset); err != nil {
			return nil, fmt.Errorf("đọc root directory: %w: %w", fs.ErrCorrupt, err)
		}
		v.addRange(v.rootDirOffset, v.rootDirOffset+n)
		feedBuf(buf, v.rootDirOffset)
		return recs, nil
	}

	if loc.cluster < 2 {
		return recs, nil // thư mục rỗng / chưa cấp phát cluster
	}
	chain, err := v.clusterChain(ctx, loc.cluster)
	if err != nil {
		return nil, fmt.Errorf("đọc chuỗi cluster thư mục: %w", err)
	}

	clusterBytes := int64(v.clusterSize)
	for _, c := range chain {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		off := v.clusterOffset(c)
		buf := make([]byte, clusterBytes)
		if _, err := v.rd.ReadAt(buf, off); err != nil {
			return nil, fmt.Errorf("đọc cluster thư mục %d: %w: %w", c, fs.ErrCorrupt, err)
		}
		v.addRange(off, off+clusterBytes)
		if feedBuf(buf, off) {
			break
		}
	}
	return recs, nil
}

// resolveDirLoc coi p là đường dẫn của CHÍNH thư mục cần định vị (không phải
// cha của nó) và trả về vị trí để đọc entry bên trong.
func (v *Volume) resolveDirLoc(ctx context.Context, p string) (dirLoc, error) {
	p = normalizePath(p)
	loc := v.rootLoc()
	if p == "/" {
		return loc, nil
	}

	comps := strings.Split(strings.Trim(p, "/"), "/")
	for depth, comp := range comps {
		if depth+1 > fs.MaxPathDepth {
			return dirLoc{}, fmt.Errorf("%s: %w", p, fs.ErrDepthLimit)
		}
		if err := ctx.Err(); err != nil {
			return dirLoc{}, err
		}
		recs, err := v.readDir(ctx, loc)
		if err != nil {
			return dirLoc{}, err
		}
		found := false
		for _, r := range recs {
			if r.IsDir && strings.EqualFold(r.Name, comp) {
				if r.Cluster < 2 {
					return dirLoc{}, fmt.Errorf("thư mục %q có cluster bắt đầu không hợp lệ (%d): %w", comp, r.Cluster, fs.ErrCorrupt)
				}
				loc = dirLoc{cluster: r.Cluster}
				found = true
				break
			}
		}
		if !found {
			return dirLoc{}, fmt.Errorf("%w: %s", fs.ErrNotFound, p)
		}
	}
	return loc, nil
}

func (v *Volume) Stat(ctx context.Context, p string) (*fs.Entry, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	np := normalizePath(p)
	if np == "/" {
		return v.rootEntry(), nil
	}

	dir, name := path.Split(np)
	loc, err := v.resolveDirLoc(ctx, dir)
	if err != nil {
		return nil, err
	}
	recs, err := v.readDir(ctx, loc)
	if err != nil {
		return nil, err
	}
	for _, r := range recs {
		if strings.EqualFold(r.Name, name) {
			return v.buildEntry(np, r), nil
		}
	}
	return nil, fmt.Errorf("%w: %s", fs.ErrNotFound, np)
}

func (v *Volume) Walk(ctx context.Context, opt fs.WalkOpt, fn func(*fs.Entry) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	rootPath := normalizePath(opt.Root)
	loc, err := v.resolveDirLoc(ctx, rootPath)
	if err != nil {
		return err
	}
	base := rootPath
	if base == "/" {
		base = ""
	}
	return v.walkDir(ctx, loc, base, 0, opt, fn)
}

func (v *Volume) walkDir(ctx context.Context, loc dirLoc, base string, depth int, opt fs.WalkOpt, fn func(*fs.Entry) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	recs, err := v.readDir(ctx, loc)
	if err != nil {
		return err
	}

	for _, r := range recs {
		if err := ctx.Err(); err != nil {
			return err
		}
		childPath := base + "/" + r.Name
		nd := depth + 1
		if nd > fs.MaxPathDepth {
			return fmt.Errorf("%s: %w", childPath, fs.ErrDepthLimit)
		}
		// Con ở độ sâu vượt MaxDepth thì không xuất ra (và không cần đọc tiếp
		// bên trong nó — dừng đệ quy ngay tại đây).
		inRange := opt.MaxDepth == 0 || nd <= opt.MaxDepth
		if inRange && !(opt.DirsOnly && !r.IsDir) {
			entry := v.buildEntry(childPath, r)
			if err := fn(entry); err != nil {
				return err
			}
		}

		if r.IsDir && (opt.MaxDepth == 0 || nd < opt.MaxDepth) {
			if r.Cluster < 2 {
				return fmt.Errorf("thư mục %q có cluster bắt đầu không hợp lệ (%d): %w", childPath, r.Cluster, fs.ErrCorrupt)
			}
			if err := v.walkDir(ctx, dirLoc{cluster: r.Cluster}, childPath, nd, opt, fn); err != nil {
				return err
			}
		}
	}
	return nil
}

// ClearAttrs đọc lại byte attribute thật từ device (không tin Attrs trong e,
// có thể đã cũ) để dựng Patch gỡ các bit trong mask.
func (v *Volume) ClearAttrs(e *fs.Entry, mask fs.Attr) ([]fs.Patch, error) {
	if len(e.Locs) == 0 {
		return []fs.Patch{}, nil
	}
	loc := e.Locs[0]

	buf := make([]byte, 1)
	if _, err := v.rd.ReadAt(buf, loc.Offset); err != nil {
		return nil, fmt.Errorf("đọc byte attribute tại offset %d: %w: %w", loc.Offset, fs.ErrCorrupt, err)
	}
	old := buf[0]
	newVal := old &^ byte(mask)
	if newVal == old {
		return []fs.Patch{}, nil
	}

	return []fs.Patch{{
		Offset: loc.Offset,
		Old:    []byte{old},
		New:    []byte{newVal},
		Region: loc.Region,
	}}, nil
}
