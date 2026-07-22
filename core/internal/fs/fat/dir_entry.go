package fat

import (
	"encoding/binary"
	"strings"
	"time"
	"unicode/utf16"
)

// dirRecord là một entry đã được giải mã (đã ghép Long File Name nếu có,
// đã bỏ qua entry rác/đã xoá/"."/"..").
type dirRecord struct {
	Name       string
	Attr       byte
	Size       uint32
	Cluster    uint32
	MTime      time.Time
	AttrOffset int64 // vị trí byte tuyệt đối của trường attribute trên volume
	IsDir      bool
}

// attrDir và attrVolumeID trùng giá trị bit với fs.AttrDir / fs.AttrVolumeID,
// khai báo riêng ở đây để dir_entry.go không phụ thuộc ngược vào volume.go.
const (
	attrVolumeID    = 0x08
	attrDir         = 0x10
	attrLongName    = 0x0F
	entryFree       = 0xE5
	entryEndOfDir   = 0x00
	dotEntryMarker  = 0x2E // '.'
	lfnLastFlagBit  = 0x40
	lfnSeqNumberBit = 0x3F
)

// dirDecoder ghép các entry LFN 32-byte liên tiếp thành tên dài, giữ trạng
// thái xuyên suốt một lượt đọc thư mục (có thể trải nhiều cluster).
type dirDecoder struct {
	lfnParts    map[byte][]uint16
	lfnChecksum byte
}

func newDirDecoder() *dirDecoder {
	return &dirDecoder{lfnParts: make(map[byte][]uint16)}
}

func (d *dirDecoder) reset() {
	for k := range d.lfnParts {
		delete(d.lfnParts, k)
	}
}

// feed xử lý một entry 32-byte thô. raw phải đúng 32 byte (caller đảm bảo).
// Trả rec != nil khi entry này mô tả một file/thư mục thật; stop=true khi gặp
// dấu hết thư mục (0x00), caller phải dừng đọc toàn bộ directory.
func (d *dirDecoder) feed(raw []byte, absOffset int64) (rec *dirRecord, stop bool) {
	b0 := raw[0]
	if b0 == entryEndOfDir {
		return nil, true
	}
	if b0 == entryFree {
		d.reset()
		return nil, false
	}

	attr := raw[11]
	if attr == attrLongName {
		seq := b0 & lfnSeqNumberBit
		if seq == 0 {
			// Số thứ tự 0 vô nghĩa — entry hỏng, bỏ chuỗi LFN đang ghép dở.
			d.reset()
			return nil, false
		}
		if b0&lfnLastFlagBit != 0 {
			// Entry "cuối" (theo thứ tự vật lý là đầu tiên) luôn mở ra một tên
			// mới — xoá phần còn sót của set trước để tránh ghép nhầm.
			d.reset()
		}
		d.lfnChecksum = raw[13]
		d.lfnParts[seq] = extractLFNChars(raw)
		return nil, false
	}

	if attr&attrVolumeID != 0 {
		// Entry nhãn volume, không phải file/thư mục thật.
		d.reset()
		return nil, false
	}
	if b0 == dotEntryMarker {
		// "." hoặc ".." — không phải entry thật, tránh đệ quy vô hạn khi Walk.
		d.reset()
		return nil, false
	}

	name := d.resolveName(raw)
	d.reset()

	cluster := uint32(binary.LittleEndian.Uint16(raw[20:22]))<<16 | uint32(binary.LittleEndian.Uint16(raw[26:28]))
	size := binary.LittleEndian.Uint32(raw[28:32])
	wrtTime := binary.LittleEndian.Uint16(raw[22:24])
	wrtDate := binary.LittleEndian.Uint16(raw[24:26])

	return &dirRecord{
		Name:       name,
		Attr:       attr,
		Size:       size,
		Cluster:    cluster,
		MTime:      dosDateTimeToTime(wrtDate, wrtTime),
		AttrOffset: absOffset + 11,
		IsDir:      attr&attrDir != 0,
	}, false
}

// resolveName ghép tên dài từ các phần LFN đã tích luỹ, verify checksum khớp
// short name; lệch hoặc thiếu phần thì fallback về short name 8.3.
func (d *dirDecoder) resolveName(raw []byte) string {
	short := shortNameString(raw)
	if len(d.lfnParts) == 0 {
		return short
	}

	var maxSeq byte
	for seq := range d.lfnParts {
		if seq > maxSeq {
			maxSeq = seq
		}
	}

	chars := make([]uint16, 0, int(maxSeq)*13)
	for seq := byte(1); seq <= maxSeq; seq++ {
		part, ok := d.lfnParts[seq]
		if !ok {
			return short // thiếu một mảnh giữa chừng — không tin được, fallback
		}
		chars = append(chars, part...)
	}

	if lfnChecksum(raw[0:11]) != d.lfnChecksum {
		return short
	}

	end := len(chars)
	for end > 0 && (chars[end-1] == 0x0000 || chars[end-1] == 0xFFFF) {
		end--
	}
	chars = chars[:end]
	if len(chars) == 0 {
		return short
	}
	return string(utf16.Decode(chars))
}

// extractLFNChars lấy 13 ký tự UTF-16 của một entry LFN, từ 3 vùng byte
// 1-10, 14-25, 28-31.
func extractLFNChars(raw []byte) []uint16 {
	chars := make([]uint16, 0, 13)
	for _, span := range [3][2]int{{1, 11}, {14, 26}, {28, 32}} {
		for i := span[0]; i < span[1]; i += 2 {
			chars = append(chars, binary.LittleEndian.Uint16(raw[i:i+2]))
		}
	}
	return chars
}

// lfnChecksum tính checksum theo thuật toán chuẩn FAT LFN trên 11 byte short
// name thô (chưa hạ chữ thường, chưa dịch 0x05).
func lfnChecksum(shortName []byte) byte {
	var sum byte
	for _, c := range shortName {
		sum = (sum>>1 | sum<<7) + c
	}
	return sum
}

// shortNameString dựng chuỗi "TEN.EXT" từ 11 byte short name (raw[0:11]) và
// áp bit hạ chữ thường NT (raw[12]) nếu có.
func shortNameString(raw []byte) string {
	var nb [11]byte
	copy(nb[:], raw[0:11])
	if nb[0] == 0x05 {
		// Quy ước FAT: byte đầu 0x05 nghĩa là ký tự thật là 0xE5 (0xE5 vốn là
		// dấu hiệu "entry đã xoá" nên không thể dùng trực tiếp).
		nb[0] = 0xE5
	}
	base := strings.TrimRight(string(nb[0:8]), " ")
	ext := strings.TrimRight(string(nb[8:11]), " ")

	ntres := raw[12]
	if ntres&0x08 != 0 {
		base = strings.ToLower(base)
	}
	if ntres&0x10 != 0 {
		ext = strings.ToLower(ext)
	}
	if ext == "" {
		return base
	}
	return base + "." + ext
}

// dosDateTimeToTime giải mã cặp DOS date/time ở offset 22-25 của entry.
// Giá trị vô lý (tháng/ngày/giờ ngoài phạm vi) trả về time.Time zero thay vì
// lỗi — mtime sai lệch không đáng để chặn toàn bộ việc duyệt thư mục.
func dosDateTimeToTime(d, t uint16) time.Time {
	year := int(d>>9) + 1980
	month := int((d >> 5) & 0x0F)
	day := int(d & 0x1F)
	hour := int(t >> 11)
	min := int((t >> 5) & 0x3F)
	sec := int(t&0x1F) * 2

	if month < 1 || month > 12 || day < 1 || day > 31 || hour > 23 || min > 59 || sec > 59 {
		return time.Time{}
	}
	return time.Date(year, time.Month(month), day, hour, min, sec, 0, time.UTC)
}
