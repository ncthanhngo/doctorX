package ntfs

import (
	"context"
	"encoding/binary"
	"fmt"

	"github.com/soi/doctorx/core/internal/fs"
)

// Các record cố định của NTFS.
const (
	volumeRecord  = 3 // $Volume
	logFileRecord = 2 // $LogFile
)

// attrTypeVolumeInfo là $VOLUME_INFORMATION.
const attrTypeVolumeInfo = 0x70

// firstUserRecord: 16 record MFT đầu là metafile hệ thống của NTFS. File của
// người dùng luôn có số record từ 16 trở lên.
const firstUserRecord = 16

// Cờ trong $VOLUME_INFORMATION.
const volumeFlagDirty = 0x0001

// writability là kết quả kiểm tra tiền điều kiện ghi.
type writability struct {
	ok     bool
	reason string
}

// checkWritable quyết định có an toàn để ghi attribute tại chỗ không.
//
// Mô hình an toàn theo đúng ntfs-3g: chỉ ghi khi volume ở trạng thái shutdown
// sạch. Ba tín hiệu nguy hiểm, thiếu một cái là từ chối:
//
//  1. hiberfil.sys ở gốc — Windows đang hibernate hoặc bật Fast Startup. Windows
//     giữ ảnh chụp metadata trong RAM và khôi phục đè khi khởi động lại; đây là
//     nguyên nhân mất dữ liệu phổ biến nhất khi ghi NTFS từ hệ điều hành khác.
//  2. Dirty bit trong $VOLUME_INFORMATION — Windows đã tự đánh dấu volume cần
//     chkdsk. Ghi thêm vào một volume đã bẩn là chồng lỗi lên lỗi.
//  3. Phiên bản NTFS ngoài 3.0/3.1 — layout có thể khác, không mạo hiểm.
//
// Ghi chú giới hạn: KHÔNG phân tích restart area của $LogFile để xác định có
// giao dịch treo hay không. Dirty bit là tín hiệu mà chính Windows dùng và bắt
// được hầu hết trường hợp shutdown bẩn (shutdown bẩn thì Windows set dirty bit).
// Kết hợp với việc mọi thao tác ghi của DoctorX đều chỉ đổi 4 byte attribute,
// có journal và hoàn tác được, rủi ro còn lại là chấp nhận được. Xem phase-08.
func (v *Volume) checkWritable() writability {
	// (3) Phiên bản.
	major, minor, dirty, err := v.readVolumeInfo()
	if err != nil {
		return writability{false, "không đọc được thông tin volume để kiểm tra an toàn: " + err.Error()}
	}
	if !(major == 3 && (minor == 0 || minor == 1)) {
		return writability{false, fmt.Sprintf("phiên bản NTFS %d.%d chưa được hỗ trợ ghi", major, minor)}
	}
	// (2) Dirty bit.
	if dirty {
		return writability{false, "volume đang được Windows đánh dấu cần kiểm tra đĩa (dirty). " +
			"Hãy chạy chkdsk trên Windows rồi shutdown sạch trước khi khôi phục."}
	}
	// (1) hiberfil.sys.
	if v.hasHiberfil() {
		return writability{false, "ổ đang ở trạng thái Fast Startup/hibernate của Windows (có hiberfil.sys). " +
			"Trên Windows hãy tắt Fast Startup rồi Shut down (không phải Restart), sau đó thử lại."}
	}
	return writability{true, ""}
}

// readVolumeInfo đọc phiên bản và cờ từ $VOLUME_INFORMATION của record $Volume.
func (v *Volume) readVolumeInfo() (major, minor uint8, dirty bool, err error) {
	rec, err := v.readRecord(volumeRecord)
	if err != nil {
		return 0, 0, false, err
	}
	attr, ok := findAttr(rec, attrTypeVolumeInfo)
	if !ok {
		return 0, 0, false, fmt.Errorf("%w: $Volume thiếu $VOLUME_INFORMATION", fs.ErrCorrupt)
	}
	c := attr.Content(rec.Raw)
	// Layout $VOLUME_INFORMATION: 8 byte dành riêng, rồi major@0x08, minor@0x09,
	// flags u16@0x0A.
	if len(c) < 0x0C {
		return 0, 0, false, fmt.Errorf("%w: $VOLUME_INFORMATION quá ngắn (%d byte)", fs.ErrCorrupt, len(c))
	}
	major = c[0x08]
	minor = c[0x09]
	flags := binary.LittleEndian.Uint16(c[0x0A:0x0C])
	return major, minor, flags&volumeFlagDirty != 0, nil
}

// hasHiberfil kiểm tra sự tồn tại của hiberfil.sys ở gốc.
func (v *Volume) hasHiberfil() bool {
	_, _, ok, err := v.findChildInDir(context.Background(), rootDirRecord, "hiberfil.sys")
	return err == nil && ok
}
