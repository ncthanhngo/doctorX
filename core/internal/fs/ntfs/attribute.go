package ntfs

import (
	"encoding/binary"
	"fmt"
	"unicode/utf16"

	"github.com/soi/doctorx/core/internal/fs"
)

// Các loại attribute NTFS cần đọc trong phạm vi package này.
const (
	attrTypeStandardInfo = 0x10
	attrTypeAttrList     = 0x20
	attrTypeFileName     = 0x30
	attrTypeData         = 0x80
	attrTypeIndexRoot    = 0x90
	attrTypeIndexAlloc   = 0xA0
	attrTypeVolumeName   = 0x60
	attrTypeEnd          = 0xFFFFFFFF
)

// maxAttrsPerRecord chặn vòng lặp giải mã attribute vô hạn nếu record hỏng.
const maxAttrsPerRecord = 4096

// attrHeader là attribute header đã giải mã cùng vị trí tương đối trong
// record để tính lại offset tuyệt đối trên volume khi cần.
type attrHeader struct {
	Type        uint32
	Length      uint32
	NonResident bool
	NameLen     uint8
	NameOffset  uint16

	// recOff là offset của attribute header này tính từ đầu record buffer.
	recOff int

	// Resident.
	ContentOffset uint16
	ContentLength uint32

	// Non-resident.
	StartVCN      uint64
	LastVCN       uint64
	RunListOffset uint16
}

// parseAttributes duyệt tuần tự các attribute trong record buf bắt đầu từ
// firstOffset, dừng ở marker kết thúc 0xFFFFFFFF hoặc hết buffer.
func parseAttributes(buf []byte, firstOffset uint16) ([]attrHeader, error) {
	var out []attrHeader
	off := int(firstOffset)
	for len(out) < maxAttrsPerRecord {
		if off < 0 || off+4 > len(buf) {
			return nil, fmt.Errorf("%w: attribute header vượt quá bản ghi tại offset %d", fs.ErrCorrupt, off)
		}
		typ := binary.LittleEndian.Uint32(buf[off : off+4])
		if typ == attrTypeEnd {
			break
		}
		if off+8 > len(buf) {
			return nil, fmt.Errorf("%w: attribute header cụt tại offset %d", fs.ErrCorrupt, off)
		}
		length := binary.LittleEndian.Uint32(buf[off+4 : off+8])
		if length < 16 || off+int(length) > len(buf) {
			return nil, fmt.Errorf("%w: attribute length bất thường (%d) tại offset %d", fs.ErrCorrupt, length, off)
		}
		nonResByte := buf[off+8]
		nameLen := buf[off+9]
		nameOffset := binary.LittleEndian.Uint16(buf[off+10 : off+12])

		a := attrHeader{
			Type: typ, Length: length, NonResident: nonResByte != 0,
			NameLen: nameLen, NameOffset: nameOffset, recOff: off,
		}
		if a.NonResident {
			if off+0x22 > len(buf) {
				return nil, fmt.Errorf("%w: header non-resident cụt tại offset %d", fs.ErrCorrupt, off)
			}
			a.StartVCN = binary.LittleEndian.Uint64(buf[off+0x10 : off+0x18])
			a.LastVCN = binary.LittleEndian.Uint64(buf[off+0x18 : off+0x20])
			a.RunListOffset = binary.LittleEndian.Uint16(buf[off+0x20 : off+0x22])
			if int(a.RunListOffset) > int(length) {
				return nil, fmt.Errorf("%w: RunListOffset (%d) vượt quá attribute length (%d) tại offset %d", fs.ErrCorrupt, a.RunListOffset, length, off)
			}
		} else {
			if off+0x16 > len(buf) {
				return nil, fmt.Errorf("%w: header resident cụt tại offset %d", fs.ErrCorrupt, off)
			}
			a.ContentLength = binary.LittleEndian.Uint32(buf[off+0x10 : off+0x14])
			a.ContentOffset = binary.LittleEndian.Uint16(buf[off+0x14 : off+0x16])
			if int(a.ContentOffset)+int(a.ContentLength) > int(length) {
				return nil, fmt.Errorf("%w: content attribute [offset %d, %d byte) vượt quá attribute length %d tại record offset %d",
					fs.ErrCorrupt, a.ContentOffset, a.ContentLength, length, off)
			}
		}
		out = append(out, a)
		off += int(length)
	}
	return out, nil
}

// Content trả về nội dung resident của attribute trong record buffer rec.
// Chỉ gọi khi !NonResident.
func (a attrHeader) Content(rec []byte) []byte {
	start := a.recOff + int(a.ContentOffset)
	return rec[start : start+int(a.ContentLength)]
}

// RunListBytes trả về vùng byte chứa data run list của attribute non-resident
// trong record buffer rec.
func (a attrHeader) RunListBytes(rec []byte) []byte {
	start := a.recOff + int(a.RunListOffset)
	end := a.recOff + int(a.Length)
	return rec[start:end]
}

// attrName giải mã tên attribute (nếu có, vd stream "$I30" hay Alternate Data
// Stream) trong record buffer rec.
func (a attrHeader) attrName(rec []byte) string {
	if a.NameLen == 0 {
		return ""
	}
	start := a.recOff + int(a.NameOffset)
	units := make([]uint16, a.NameLen)
	for i := range units {
		units[i] = binary.LittleEndian.Uint16(rec[start+i*2 : start+i*2+2])
	}
	return string(utf16.Decode(units))
}

// findAttr trả về attribute đầu tiên khớp typ trong record, chỉ tìm trong
// chính record này (không theo $ATTRIBUTE_LIST sang record khác).
func findAttr(rec *mftRecord, typ uint32) (attrHeader, bool) {
	for _, a := range rec.Attrs {
		if a.Type == typ {
			return a, true
		}
	}
	return attrHeader{}, false
}
