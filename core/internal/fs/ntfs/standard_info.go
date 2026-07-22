package ntfs

import (
	"encoding/binary"
	"fmt"

	"github.com/soi/doctorx/core/internal/fs"
)

// stdInfoFileAttrOffset là offset của trường FileAttributes trong nội dung
// $STANDARD_INFORMATION.
//
// LƯU Ý: giá trị này là 0x20, đã kiểm chứng thực nghiệm trên fixture
// testdata/ntfs.img (record 0 = $MFT có FileAttributes=0x06 Hidden+System
// đúng tại content+0x20; record 5 = root directory có FileAttributes=0x26
// tại cùng offset). Layout thật của $STANDARD_INFORMATION là 4 mốc thời gian
// 8 byte (Creation/Modification/MFTModification/Access, tổng 0x20 byte) rồi
// mới đến FileAttributes — KHÔNG phải offset 0x00 như một số tài liệu tóm tắt
// ghi nhầm.
const stdInfoFileAttrOffset = 0x20

// parseStandardInfoAttrs đọc FileAttributes (DOS attribute bits) từ nội dung
// resident của $STANDARD_INFORMATION.
func parseStandardInfoAttrs(content []byte) (uint32, error) {
	if len(content) < stdInfoFileAttrOffset+4 {
		return 0, fmt.Errorf("%w: nội dung $STANDARD_INFORMATION quá ngắn (%d byte)", fs.ErrCorrupt, len(content))
	}
	return binary.LittleEndian.Uint32(content[stdInfoFileAttrOffset : stdInfoFileAttrOffset+4]), nil
}
