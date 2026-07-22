package ntfs

import (
	"encoding/binary"
	"fmt"
	"time"
	"unicode/utf16"

	"github.com/soi/doctorx/core/internal/fs"
)

// Offset các trường trong nội dung $FILE_NAME.
const (
	fnParentOffset   = 0x00
	fnModTimeOffset  = 0x10
	fnRealSizeOffset = 0x30
	fnAttrOffset     = 0x38
	fnNameLenOffset  = 0x40
	fnNamespaceOff   = 0x41
	fnNameOffset     = 0x42
)

// Namespace của $FILE_NAME — quyết định ưu tiên khi một file có nhiều tên.
const (
	nsPOSIX       = 0
	nsWin32       = 1
	nsDOS         = 2
	nsWin32AndDOS = 3
)

// mftRefRecordMask lấy 48 bit thấp (số MFT record) từ một MFT reference
// 64-bit; 16 bit cao là số sequence dùng để phát hiện record bị tái sử dụng.
const mftRefRecordMask = 0x0000FFFFFFFFFFFF

// fileNameContent là nội dung $FILE_NAME đã giải mã.
type fileNameContent struct {
	Parent    uint64
	Attrs     uint32
	RealSize  uint64
	Modified  time.Time
	Namespace uint8
	Name      string
}

// parseFileName giải mã nội dung resident của một attribute $FILE_NAME.
func parseFileName(content []byte) (fileNameContent, error) {
	if len(content) < fnNameOffset+1 {
		return fileNameContent{}, fmt.Errorf("%w: nội dung $FILE_NAME quá ngắn (%d byte)", fs.ErrCorrupt, len(content))
	}
	nameLen := int(content[fnNameLenOffset])
	ns := content[fnNamespaceOff]
	nameEnd := fnNameOffset + nameLen*2
	if nameEnd > len(content) {
		return fileNameContent{}, fmt.Errorf("%w: tên trong $FILE_NAME (dài %d ký tự) vượt quá nội dung (%d byte)", fs.ErrCorrupt, nameLen, len(content))
	}
	units := make([]uint16, nameLen)
	for i := 0; i < nameLen; i++ {
		units[i] = binary.LittleEndian.Uint16(content[fnNameOffset+i*2 : fnNameOffset+i*2+2])
	}

	fc := fileNameContent{
		Parent:    binary.LittleEndian.Uint64(content[fnParentOffset : fnParentOffset+8]),
		Attrs:     binary.LittleEndian.Uint32(content[fnAttrOffset : fnAttrOffset+4]),
		Namespace: ns,
		Name:      string(utf16.Decode(units)),
	}
	if len(content) >= fnRealSizeOffset+8 {
		fc.RealSize = binary.LittleEndian.Uint64(content[fnRealSizeOffset : fnRealSizeOffset+8])
	}
	if len(content) >= fnModTimeOffset+8 {
		fc.Modified = ntfsTimeToTime(binary.LittleEndian.Uint64(content[fnModTimeOffset : fnModTimeOffset+8]))
	}
	return fc, nil
}

// ntfsTimeToTime quy đổi FILETIME NTFS (100ns kể từ 1601-01-01 UTC) sang
// time.Time. Giá trị 0 trả về time.Time zero thay vì báo lỗi — timestamp hỏng
// không nên chặn việc đọc entry.
func ntfsTimeToTime(v uint64) time.Time {
	if v == 0 {
		return time.Time{}
	}
	const epochDiff100ns = 116444736000000000 // khoảng cách 1601-01-01 tới 1970-01-01, tính bằng 100ns
	d := int64(v) - epochDiff100ns
	return time.Unix(0, d*100).UTC()
}

// selectPreferredFileName chọn attribute $FILE_NAME "đại diện" của record khi
// có nhiều bản (Win32, DOS 8.3, POSIX...). Ưu tiên Win32/Win32+DOS trước, kế
// đến POSIX, cuối cùng mới đến DOS 8.3 thuần — vì DOS 8.3 chỉ là tên rút gọn
// dùng cho tương thích, Explorer/Windows hiển thị tên Win32.
func selectPreferredFileName(rec *mftRecord) (attrHeader, fileNameContent, bool) {
	var best attrHeader
	var bestContent fileNameContent
	found := false
	bestRank := 99
	for _, a := range rec.Attrs {
		if a.Type != attrTypeFileName || a.NonResident {
			continue
		}
		fc, err := parseFileName(a.Content(rec.Raw))
		if err != nil {
			continue // bản này hỏng, thử bản khác thay vì chặn cả entry
		}
		rank := 2
		switch fc.Namespace {
		case nsWin32, nsWin32AndDOS:
			rank = 0
		case nsPOSIX:
			rank = 1
		case nsDOS:
			rank = 2
		}
		if rank < bestRank {
			bestRank = rank
			best = a
			bestContent = fc
			found = true
		}
	}
	return best, bestContent, found
}
