// Package ntfs cài đặt fs.Volume cho NTFS: chỉ đọc metadata (boot sector,
// MFT record, chỉ mục $I30) và sinh Patch để gỡ attribute Hidden/System.
// Không đọc nội dung file — xem package fs để biết lý do.
package ntfs

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

// rootDirRecord là số MFT record cố định của thư mục gốc trên mọi volume
// NTFS.
const rootDirRecord = 5

// Volume là cài đặt fs.Volume cho NTFS.
type Volume struct {
	rd         *blockdev.Reader
	boot       *bootSector
	label      string
	recordSize int

	// mftBootOffset là offset byte tuyệt đối của MFT record 0, tính trực
	// tiếp từ boot sector — dùng để bootstrap đọc $DATA runlist của $MFT.
	mftBootOffset int64
	// mftRuns/mftStartVCN mô tả stream $DATA của $MFT, dùng để định vị mọi
	// record khác ngoài record 0.
	mftRuns     []dataRun
	mftStartVCN uint64

	mu         sync.Mutex
	ranges     *guard.RangeSet
	seenRanges map[int64]bool // offset bắt đầu vùng đã khai báo vào ranges
}

// errStopScan dùng nội bộ để dừng sớm khi duyệt chỉ mục tìm một tên cụ thể.
var errStopScan = errors.New("đã tìm thấy, dừng quét")

// Open đọc boot sector, bootstrap $MFT và trả về Volume sẵn sàng duyệt
// metadata.
func Open(rd *blockdev.Reader) (*Volume, error) {
	buf := make([]byte, bootSectorSize)
	if _, err := rd.ReadAt(buf, 0); err != nil {
		return nil, fmt.Errorf("%w: đọc boot sector: %v", fs.ErrCorrupt, err)
	}
	boot, err := parseBootSector(buf)
	if err != nil {
		return nil, err
	}

	clusterSize := int64(boot.ClusterSize())
	mftOff := int64(boot.MFTStartCluster) * clusterSize
	if size := rd.Size(); size > 0 {
		volBytes := int64(boot.TotalSectors) * int64(boot.BytesPerSector)
		if volBytes > size {
			return nil, fmt.Errorf("%w: boot sector khai báo %d byte, vượt quá dung lượng ảnh %d byte", fs.ErrCorrupt, volBytes, size)
		}
		if mftOff < 0 || mftOff+int64(boot.RecordSize) > size {
			return nil, fmt.Errorf("%w: vị trí $MFT (offset %d) vượt quá dung lượng ảnh %d byte", fs.ErrCorrupt, mftOff, size)
		}
	}

	v := &Volume{
		rd: rd, boot: boot, recordSize: int(boot.RecordSize),
		mftBootOffset: mftOff,
		ranges:        guard.NewRangeSet(),
		seenRanges:    make(map[int64]bool),
	}

	mftRec, err := v.readRecord(0)
	if err != nil {
		return nil, fmt.Errorf("%w: đọc MFT record 0 ($MFT): %v", fs.ErrCorrupt, err)
	}
	dataAttr, ok := findAttr(mftRec, attrTypeData)
	if !ok || !dataAttr.NonResident {
		return nil, fmt.Errorf("%w: $MFT thiếu $DATA non-resident", fs.ErrCorrupt)
	}
	runs, err := decodeRunList(dataAttr.RunListBytes(mftRec.Raw))
	if err != nil {
		return nil, fmt.Errorf("%w: giải mã runlist $MFT: %v", fs.ErrCorrupt, err)
	}
	v.mftRuns = runs
	v.mftStartVCN = dataAttr.StartVCN

	label, err := v.readLabel()
	if err != nil {
		return nil, err
	}
	v.label = label

	return v, nil
}

// mftRecordOffset ánh xạ số MFT record (khác 0) sang offset byte tuyệt đối
// trên volume, qua runlist $DATA của $MFT.
func (v *Volume) mftRecordOffset(num uint64) (int64, error) {
	streamOff := int64(num) * int64(v.recordSize)
	return mapStreamOffset(v.mftRuns, v.mftStartVCN, int64(v.boot.ClusterSize()), streamOff)
}

// declareRange khai báo một vùng byte vào MetadataRanges, mỗi vùng (theo
// offset bắt đầu) chỉ khai báo một lần dù được đọc lại nhiều lần khi Walk đi
// qua cùng record/INDX block ở các lần gọi khác nhau.
func (v *Volume) declareRange(start, end int64, name string) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.seenRanges[start] {
		return
	}
	v.seenRanges[start] = true
	v.ranges.Add(start, end, name)
}

// readLabel tìm $VOLUME_NAME (0x60) trong record 3 ($Volume). Không tìm thấy
// nghĩa là volume không đặt nhãn — hợp lệ, trả chuỗi rỗng.
func (v *Volume) readLabel() (string, error) {
	rec, err := v.readRecord(3)
	if err != nil {
		return "", fmt.Errorf("%w: đọc record $Volume: %v", fs.ErrCorrupt, err)
	}
	attr, ok := findAttr(rec, attrTypeVolumeName)
	if !ok || attr.NonResident {
		return "", nil
	}
	content := attr.Content(rec.Raw)
	n := len(content) / 2
	units := make([]uint16, n)
	for i := 0; i < n; i++ {
		units[i] = binary.LittleEndian.Uint16(content[i*2 : i*2+2])
	}
	return string(utf16.Decode(units)), nil
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

// findChildInDir tìm entry tên `name` (so khớp không phân biệt hoa/thường)
// trực tiếp trong thư mục dirNum, dừng quét ngay khi tìm thấy.
func (v *Volume) findChildInDir(ctx context.Context, dirNum uint64, name string) (mftNum uint64, attrKeyOffset int64, ok bool, err error) {
	scanErr := v.iterateIndexForDir(ctx, dirNum, func(num uint64, entryName string, off int64) error {
		if strings.EqualFold(entryName, name) {
			mftNum, attrKeyOffset, ok = num, off, true
			return errStopScan
		}
		return nil
	})
	if scanErr != nil && !errors.Is(scanErr, errStopScan) {
		return 0, 0, false, scanErr
	}
	return mftNum, attrKeyOffset, ok, nil
}

// resolveDirParts đi từ root theo từng thành phần path, trả về số MFT record
// của thư mục đích cùng đường dẫn chuẩn hoá của nó.
func (v *Volume) resolveDirParts(ctx context.Context, parts []string) (uint64, string, error) {
	cur := uint64(rootDirRecord)
	curPath := "/"
	for _, part := range parts {
		select {
		case <-ctx.Done():
			return 0, "", ctx.Err()
		default:
		}
		num, _, ok, err := v.findChildInDir(ctx, cur, part)
		if err != nil {
			return 0, "", err
		}
		next := path.Join(curPath, part)
		if !ok {
			return 0, "", fmt.Errorf("%w: %s", fs.ErrNotFound, next)
		}
		rec, err := v.readRecord(num)
		if err != nil {
			return 0, "", err
		}
		if !rec.isDir() {
			return 0, "", fmt.Errorf("%w: %s không phải thư mục", fs.ErrNotFound, next)
		}
		cur, curPath = num, next
	}
	return cur, curPath, nil
}

// buildEntry dựng fs.Entry cho MFT record childNum, đã biết trước path tuyệt
// đối và offset của bản copy $FILE_NAME trong chỉ mục của thư mục cha
// (parentIndexKeyAttrOffset — chính là EntryLoc thứ 3, "ntfs-index").
func (v *Volume) buildEntry(childNum uint64, entryPath string, parentIndexKeyAttrOffset int64) (*fs.Entry, error) {
	rec, err := v.readRecord(childNum)
	if err != nil {
		return nil, err
	}
	if !rec.inUse() {
		// Chỉ mục $I30 của cha vẫn còn tham chiếu tới một record đã được
		// đánh dấu rảnh (đã xoá) — cây thư mục và MFT lệch nhau, coi là hỏng
		// thay vì trả về entry của dữ liệu rác đã bị tái sử dụng một phần.
		return nil, fmt.Errorf("%w: record %d (%s) không còn in-use nhưng vẫn được chỉ mục tham chiếu", fs.ErrCorrupt, childNum, entryPath)
	}

	stdAttr, stdRec, ok, err := v.findAttrDeep(rec, attrTypeStandardInfo)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, fmt.Errorf("%w: record %d (%s) thiếu $STANDARD_INFORMATION", fs.ErrCorrupt, childNum, entryPath)
	}
	if stdAttr.NonResident {
		return nil, fmt.Errorf("%w: $STANDARD_INFORMATION non-resident bất thường tại record %d", fs.ErrCorrupt, childNum)
	}
	attrsVal, err := parseStandardInfoAttrs(stdAttr.Content(stdRec.Raw))
	if err != nil {
		return nil, fmt.Errorf("%w: %s: %v", fs.ErrCorrupt, entryPath, err)
	}
	stdInfoAbsOffset := stdRec.Offset + int64(stdAttr.recOff) + int64(stdAttr.ContentOffset) + stdInfoFileAttrOffset

	fnAttr, fnContent, ok := selectPreferredFileName(rec)
	if !ok {
		return nil, fmt.Errorf("%w: record %d (%s) thiếu $FILE_NAME", fs.ErrCorrupt, childNum, entryPath)
	}
	fnAttrAbsOffset := rec.Offset + int64(fnAttr.recOff) + int64(fnAttr.ContentOffset) + fnAttrOffset

	isDir := rec.isDir()
	const knownAttrs = fs.AttrReadOnly | fs.AttrHidden | fs.AttrSystem | fs.AttrVolumeID | fs.AttrArchive
	attrs := fs.Attr(attrsVal) & knownAttrs
	if isDir {
		attrs |= fs.AttrDir
	}

	var size int64
	if !isDir {
		size = int64(fnContent.RealSize)
	}

	return &fs.Entry{
		Path: entryPath, Size: size, Attrs: attrs, Modified: fnContent.Modified,
		Locs: []fs.EntryLoc{
			{Offset: stdInfoAbsOffset, Width: 4, Region: "ntfs-stdinfo"},
			{Offset: fnAttrAbsOffset, Width: 4, Region: "ntfs-filename"},
			{Offset: parentIndexKeyAttrOffset, Width: 4, Region: "ntfs-index"},
		},
	}, nil
}

// walkDir duyệt DFS thư mục dirNum, gọi fn cho từng entry theo opt.
func (v *Volume) walkDir(ctx context.Context, dirNum uint64, dirPath string, depth int, opt fs.WalkOpt, fn func(*fs.Entry) error) error {
	if depth > fs.MaxPathDepth {
		return fs.ErrDepthLimit
	}
	return v.iterateIndexForDir(ctx, dirNum, func(num uint64, name string, attrKeyOffset int64) error {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		// 16 record đầu là metafile của chính NTFS ($MFT, $LogFile, $Boot,
		// $Extend...), đều mang cờ Hidden+System nhưng KHÔNG phải dữ liệu người
		// dùng. Bỏ qua hẳn — không emit, không đi vào — đúng như mọi công cụ
		// NTFS: enumeration của người dùng bắt đầu từ record 16. Nhờ vậy tầng
		// trên không bao giờ thấy chúng, và $Extend (record 11) bị chặn nên các
		// con $ObjId/$Quota/$Reparse cũng không lộ ra.
		if num < firstUserRecord {
			return nil
		}

		entryPath := path.Join(dirPath, name)
		entry, err := v.buildEntry(num, entryPath, attrKeyOffset)
		if err != nil {
			return err
		}

		isDir := entry.Attrs.IsDir()
		if isDir || !opt.DirsOnly {
			if err := fn(entry); err != nil {
				return err
			}
		}
		if isDir && (opt.MaxDepth == 0 || depth < opt.MaxDepth) {
			return v.walkDir(ctx, num, entryPath, depth+1, opt, fn)
		}
		return nil
	})
}

// Info trả về metadata tổng quan của volume.
func (v *Volume) Info() fs.VolumeInfo {
	return fs.VolumeInfo{
		Kind:        fs.KindNTFS,
		Label:       v.label,
		BytesPerSec: v.boot.BytesPerSector,
		ClusterSize: v.boot.ClusterSize(),
		TotalBytes:  int64(v.boot.TotalSectors) * int64(v.boot.BytesPerSector),
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
	dirNum, dirPath, err := v.resolveDirParts(ctx, parts[:len(parts)-1])
	if err != nil {
		return nil, err
	}
	num, attrKeyOffset, ok, err := v.findChildInDir(ctx, dirNum, parts[len(parts)-1])
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, fmt.Errorf("%w: %s", fs.ErrNotFound, p)
	}
	return v.buildEntry(num, path.Join(dirPath, parts[len(parts)-1]), attrKeyOffset)
}

// Writable cho phép ghi attribute tại chỗ khi volume ở trạng thái an toàn:
// không hibernate/Fast Startup, dirty bit tắt, phiên bản NTFS được hỗ trợ.
// Chi tiết mô hình an toàn xem checkWritable trong volume_flags.go.
func (v *Volume) Writable() (bool, string) {
	w := v.checkWritable()
	return w.ok, w.reason
}

// MetadataRanges trả về các vùng byte đã tích luỹ qua các lần Walk/Stat —
// chỉ gồm MFT record và INDX block thực sự đã đọc.
func (v *Volume) MetadataRanges() *guard.RangeSet {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.ranges
}

// ClearAttrs dựng patch gỡ các bit trong mask khỏi FileAttributes tại cả ba
// vị trí của entry: $STANDARD_INFORMATION, $FILE_NAME trong record của chính
// file, và bản copy trong chỉ mục $I30 của thư mục cha. Đọc lại giá trị thật
// từ device cho từng Loc — tránh ghi đè nhầm nếu device đã đổi từ lúc Walk.
func (v *Volume) ClearAttrs(e *fs.Entry, mask fs.Attr) ([]fs.Patch, error) {
	if len(e.Locs) == 0 {
		return nil, fmt.Errorf("%w: entry không có vị trí NTFS hợp lệ để sửa", fs.ErrCorrupt)
	}
	var patches []fs.Patch
	for _, loc := range e.Locs {
		if loc.Width != 4 {
			return nil, fmt.Errorf("%w: EntryLoc NTFS phải rộng 4 byte, nhận %d tại offset %d", fs.ErrCorrupt, loc.Width, loc.Offset)
		}
		// Mọi vị trí attribute của NTFS đều nằm trong một cấu trúc có Update
		// Sequence Array (MFT record hoặc INDX block, đều căn theo bội số 512).
		// USA đặt số tuần tự ở 2 byte CUỐI mỗi sector; ghi đè trúng đó sẽ phá
		// fixup và block hỏng khi đọc lại — mà verify-after-write KHÔNG bắt được
		// vì ta đọc lại đúng thứ vừa ghi. Từ chối file này thay vì ghi mù.
		if secRel := loc.Offset % 512; secRel+int64(loc.Width) > 510 {
			return nil, fmt.Errorf("%w: attribute tại offset %d rơi vào vùng Update Sequence Array của sector — không thể sửa an toàn",
				fs.ErrCorrupt, loc.Offset)
		}
		buf := make([]byte, 4)
		if _, err := v.rd.ReadAt(buf, loc.Offset); err != nil {
			return nil, fmt.Errorf("%w: đọc lại giá trị attribute tại offset %d: %v", fs.ErrCorrupt, loc.Offset, err)
		}
		old := binary.LittleEndian.Uint32(buf)
		newVal := old &^ uint32(mask)
		if newVal == old {
			continue
		}
		newBuf := make([]byte, 4)
		binary.LittleEndian.PutUint32(newBuf, newVal)
		patches = append(patches, fs.Patch{
			Offset: loc.Offset,
			Old:    append([]byte(nil), buf...),
			New:    newBuf,
			Region: loc.Region,
		})
	}
	return patches, nil
}
