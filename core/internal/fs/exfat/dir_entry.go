package exfat

import (
	"encoding/binary"
	"fmt"
	"time"
	"unicode/utf16"

	"github.com/soi/doctorx/core/internal/blockdev"
	"github.com/soi/doctorx/core/internal/fs"
)

// dirEntrySize là kích thước cố định của một directory entry exFAT.
const dirEntrySize = 32

// Các loại entry cần nhận diện. Giá trị đã bao gồm bit 0x80 (InUse) — một
// entry đã bị xoá chỉ tắt bit này nên byte type sẽ khác các hằng số dưới đây
// và tự động bị bỏ qua khi so khớp chính xác, không cần mask riêng.
const (
	entryTypeVolumeLabel = 0x83
	entryTypeFile        = 0x85
	entryTypeStreamExt   = 0xC0
	entryTypeFileName    = 0xC1
)

// noFatChainFlag là bit 1 của GeneralSecondaryFlags trong Stream Extension:
// bật khi cluster được cấp phát liền khối, không cần tra bảng FAT.
const noFatChainFlag = 0x02

// maxSecondaryCount chặn SecondaryCount rác — đặc tả giới hạn tối đa 255.
const maxSecondaryCount = 255

// dirEntrySet là dữ liệu đã giải mã của một File Directory Entry (0x85) cùng
// Stream Extension (0xC0) và các File Name entry (0xC1) đi kèm — đơn vị mô tả
// một file/thư mục trong exFAT.
type dirEntrySet struct {
	// Start là offset byte tuyệt đối trên volume của entry chính (0x85).
	Start          int64
	SecondaryCount int
	Attrs          uint16
	Modified       time.Time
	NoFatChain     bool
	FirstCluster   uint32
	DataLength     uint64
	Name           string
}

// decodeTimestamp quy đổi timestamp 32-bit kiểu DOS của exFAT sang time.Time.
// Trả về giá trị zero nếu thành phần ngày/tháng vô lý — không nên chặn cả
// việc đọc entry chỉ vì timestamp hỏng.
func decodeTimestamp(v uint32) time.Time {
	year := int(v>>25) + 1980
	month := int((v >> 21) & 0x0F)
	day := int((v >> 16) & 0x1F)
	hour := int((v >> 11) & 0x1F)
	minute := int((v >> 5) & 0x3F)
	second := int(v&0x1F) * 2
	if month < 1 || month > 12 || day < 1 || day > 31 {
		return time.Time{}
	}
	return time.Date(year, time.Month(month), day, hour, minute, second, 0, time.UTC)
}

// dirEntryReader duyệt tuần tự các entry 32 byte của một thư mục theo danh
// sách cluster đã cho, chỉ giữ trong bộ nhớ nội dung của một cluster tại một
// thời điểm — không tải toàn bộ thư mục cùng lúc.
type dirEntryReader struct {
	rd       *blockdev.Reader
	boot     *bootSector
	clusters []uint32

	ci  int    // chỉ số cluster hiện tại trong clusters, -1 = chưa đọc cluster nào
	buf []byte // nội dung cluster hiện tại
	pos int    // vị trí byte tiếp theo trong buf
}

func newDirEntryReader(rd *blockdev.Reader, b *bootSector, clusters []uint32) *dirEntryReader {
	return &dirEntryReader{rd: rd, boot: b, clusters: clusters, ci: -1}
}

// next trả entry 32 byte tiếp theo cùng offset tuyệt đối trên volume của nó,
// hoặc ok=false khi đã hết thư mục.
func (r *dirEntryReader) next() (raw []byte, offset int64, ok bool, err error) {
	for r.buf == nil || r.pos >= len(r.buf) {
		r.ci++
		if r.ci >= len(r.clusters) {
			return nil, 0, false, nil
		}
		buf := make([]byte, r.boot.BytesPerCluster())
		off := clusterOffset(r.boot, r.clusters[r.ci])
		if _, err := r.rd.ReadAt(buf, off); err != nil {
			return nil, 0, false, fmt.Errorf("%w: đọc cluster thư mục %d: %v", fs.ErrCorrupt, r.clusters[r.ci], err)
		}
		r.buf = buf
		r.pos = 0
	}
	off := clusterOffset(r.boot, r.clusters[r.ci]) + int64(r.pos)
	raw = r.buf[r.pos : r.pos+dirEntrySize]
	r.pos += dirEntrySize
	return raw, off, true, nil
}

// decodeEntrySets duyệt các entry set InUse của một thư mục qua dr, gọi fn
// cho từng entry set File (0x85) hợp lệ. Volume Label và entry đã xoá bị bỏ
// qua ngay ở vòng lặp ngoài.
func decodeEntrySets(dr *dirEntryReader, fn func(dirEntrySet) error) error {
	for {
		raw, off, ok, err := dr.next()
		if err != nil {
			return err
		}
		if !ok {
			return nil
		}
		if raw[0] != entryTypeFile {
			continue
		}
		set, err := readEntrySet(dr, raw, off)
		if err != nil {
			return err
		}
		if err := fn(set); err != nil {
			return err
		}
	}
}

// readEntrySet giải mã một entry set bắt đầu từ entry chính `primary` (đã đọc
// sẵn) tại offset tuyệt đối `start`, đọc tiếp các secondary entry từ dr.
//
// Xác thực SetChecksum trên toàn bộ entry set trước khi trả về — đây là cách
// chính bắt được directory hỏng/entry bị cắt cụt do image truncate.
func readEntrySet(dr *dirEntryReader, primary []byte, start int64) (dirEntrySet, error) {
	secCount := int(primary[1])
	if secCount < 1 || secCount > maxSecondaryCount {
		return dirEntrySet{}, fmt.Errorf("%w: SecondaryCount bất thường (%d) tại offset %d", fs.ErrCorrupt, secCount, start)
	}
	declaredChecksum := binary.LittleEndian.Uint16(primary[2:4])

	full := make([]byte, 0, (secCount+1)*dirEntrySize)
	full = append(full, primary...)

	set := dirEntrySet{
		Start:          start,
		SecondaryCount: secCount,
		Attrs:          binary.LittleEndian.Uint16(primary[4:6]),
		Modified:       decodeTimestamp(binary.LittleEndian.Uint32(primary[12:16])),
	}

	var nameUnits []uint16
	nameLen := -1
	for i := 0; i < secCount; i++ {
		raw, _, ok, err := dr.next()
		if err != nil {
			return dirEntrySet{}, err
		}
		if !ok {
			return dirEntrySet{}, fmt.Errorf("%w: entry set tại offset %d thiếu %d secondary entry", fs.ErrCorrupt, start, secCount-i)
		}
		full = append(full, raw...)

		switch raw[0] {
		case entryTypeStreamExt:
			set.NoFatChain = raw[1]&noFatChainFlag != 0
			nameLen = int(raw[3])
			set.FirstCluster = binary.LittleEndian.Uint32(raw[20:24])
			set.DataLength = binary.LittleEndian.Uint64(raw[24:32])
		case entryTypeFileName:
			for j := 0; j < 15; j++ {
				nameUnits = append(nameUnits, binary.LittleEndian.Uint16(raw[2+j*2:4+j*2]))
			}
		}
	}
	if nameLen < 0 {
		return dirEntrySet{}, fmt.Errorf("%w: entry set tại offset %d thiếu Stream Extension", fs.ErrCorrupt, start)
	}
	if nameLen > len(nameUnits) {
		return dirEntrySet{}, fmt.Errorf("%w: NameLength (%d) vượt số ký tự có trong Name entry tại offset %d", fs.ErrCorrupt, nameLen, start)
	}
	set.Name = string(utf16.Decode(nameUnits[:nameLen]))

	if got := SetChecksum(full); got != declaredChecksum {
		return dirEntrySet{}, fmt.Errorf("%w: SetChecksum sai tại offset %d (lưu 0x%04x, tính được 0x%04x)", fs.ErrCorrupt, start, declaredChecksum, got)
	}
	return set, nil
}
