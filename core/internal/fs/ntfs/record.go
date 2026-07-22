package ntfs

import (
	"bytes"
	"encoding/binary"
	"fmt"

	"github.com/soi/doctorx/core/internal/fs"
)

// fileMagic/badMagic là 4 byte đầu của một MFT file record.
var (
	fileMagic = []byte("FILE")
	badMagic  = []byte("BAAD")
)

// recordHeaderMinLen là số byte tối thiểu để đọc các trường cố định của FILE
// record header (đến hết BaseRecordReference @0x20, dài 8 byte).
const recordHeaderMinLen = 0x28

// mftRecord là một MFT file record đã áp Update Sequence Array fixup và giải
// mã danh sách attribute.
type mftRecord struct {
	Num             uint64
	Offset          int64 // vị trí byte tuyệt đối của record trên volume
	Raw             []byte
	FirstAttrOffset uint16
	Flags           uint16 // bit 0x01 = in use, bit 0x02 = directory
	BaseRecordRef   uint64
	Attrs           []attrHeader
}

func (r *mftRecord) inUse() bool { return r.Flags&0x01 != 0 }
func (r *mftRecord) isDir() bool { return r.Flags&0x02 != 0 }

// applyFixup khôi phục 2 byte cuối mỗi sector trong buf từ Update Sequence
// Array, đồng thời xác thực USN lưu ở cuối mỗi sector khớp giá trị khai báo.
// Không áp bước này thì mọi record dài hơn 1 sector sẽ bị parse sai vì 2 byte
// cuối mỗi sector đã bị thay bằng USN khi ghi xuống đĩa.
func applyFixup(buf []byte, sectorSize uint32) error {
	if len(buf) < 8 {
		return fmt.Errorf("%w: bản ghi quá ngắn để đọc Update Sequence Array", fs.ErrCorrupt)
	}
	if sectorSize == 0 {
		return fmt.Errorf("%w: kích thước sector = 0, không áp được fixup", fs.ErrCorrupt)
	}
	usaOff := binary.LittleEndian.Uint16(buf[0x04:0x06])
	usaCnt := binary.LittleEndian.Uint16(buf[0x06:0x08])
	if usaCnt == 0 {
		return fmt.Errorf("%w: Update Sequence Array rỗng", fs.ErrCorrupt)
	}
	usaEnd := int(usaOff) + int(usaCnt)*2
	if usaEnd > len(buf) {
		return fmt.Errorf("%w: Update Sequence Array [offset %d, %d byte) vượt quá kích thước bản ghi %d", fs.ErrCorrupt, usaOff, usaCnt*2, len(buf))
	}
	usn := binary.LittleEndian.Uint16(buf[usaOff : usaOff+2])

	numSectors := int(usaCnt) - 1
	for i := 0; i < numSectors; i++ {
		secEnd := (i+1)*int(sectorSize) - 2
		if secEnd < 0 || secEnd+2 > len(buf) {
			return fmt.Errorf("%w: sector %d vượt quá bản ghi khi áp fixup", fs.ErrCorrupt, i)
		}
		stored := binary.LittleEndian.Uint16(buf[secEnd : secEnd+2])
		if stored != usn {
			return fmt.Errorf("%w: USN không khớp tại sector %d (lưu %#04x, muốn %#04x) — bản ghi hỏng", fs.ErrCorrupt, i, stored, usn)
		}
		valOff := int(usaOff) + 2 + i*2
		copy(buf[secEnd:secEnd+2], buf[valOff:valOff+2])
	}
	return nil
}

// readRecord đọc và giải mã MFT record số num. Record 0 ($MFT) được định vị
// trực tiếp qua boot sector; các record khác định vị qua runlist $DATA của
// $MFT (v.mftRuns) đã dựng lúc Open.
func (v *Volume) readRecord(num uint64) (*mftRecord, error) {
	var off int64
	if num == 0 {
		off = v.mftBootOffset
	} else {
		mapped, err := v.mftRecordOffset(num)
		if err != nil {
			return nil, fmt.Errorf("%w: định vị MFT record %d: %v", fs.ErrCorrupt, num, err)
		}
		off = mapped
	}

	buf := make([]byte, v.recordSize)
	if _, err := v.rd.ReadAt(buf, off); err != nil {
		return nil, fmt.Errorf("%w: đọc MFT record %d tại offset %d: %v", fs.ErrCorrupt, num, off, err)
	}
	if bytes.Equal(buf[0:4], badMagic) {
		return nil, fmt.Errorf("%w: MFT record %d hỏng (đánh dấu BAAD)", fs.ErrCorrupt, num)
	}
	if !bytes.Equal(buf[0:4], fileMagic) {
		return nil, fmt.Errorf("%w: MFT record %d thiếu chữ ký FILE", fs.ErrCorrupt, num)
	}
	if err := applyFixup(buf, v.boot.BytesPerSector); err != nil {
		return nil, fmt.Errorf("%w: fixup MFT record %d: %v", fs.ErrCorrupt, num, err)
	}
	if len(buf) < recordHeaderMinLen {
		return nil, fmt.Errorf("%w: MFT record %d quá ngắn (%d byte)", fs.ErrCorrupt, num, len(buf))
	}

	firstAttr := binary.LittleEndian.Uint16(buf[0x14:0x16])
	flags := binary.LittleEndian.Uint16(buf[0x16:0x18])
	baseRef := binary.LittleEndian.Uint64(buf[0x20:0x28])

	attrs, err := parseAttributes(buf, firstAttr)
	if err != nil {
		return nil, fmt.Errorf("%w: parse attribute của record %d: %v", fs.ErrCorrupt, num, err)
	}

	v.declareRange(off, off+int64(len(buf)), "ntfs-mft-record")

	return &mftRecord{
		Num: num, Offset: off, Raw: buf,
		FirstAttrOffset: firstAttr, Flags: flags, BaseRecordRef: baseRef,
		Attrs: attrs,
	}, nil
}
