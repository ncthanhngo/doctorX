package imaging

import (
	"bytes"
	"context"
	"fmt"
	"os"

	"github.com/soi/doctorx/core/internal/blockdev"
)

// Phát hiện ổ/phân vùng đang mã hoá — giúp kỹ thuật viên IT biết ngay "ổ này khoá
// BitLocker" trước khi loay hoay, không phải thao tác mù.

// EncryptionKind là loại mã hoá nhận ra được.
type EncryptionKind string

const (
	EncNone       EncryptionKind = "none"
	EncBitLocker  EncryptionKind = "bitlocker"
	EncFileVault  EncryptionKind = "filevault" // APFS/CoreStorage đã mã hoá (qua diskutil)
	EncUnknownEnc EncryptionKind = "encrypted" // diskutil báo mã hoá nhưng không rõ loại
)

// bitlockerSig là chữ ký nằm ở offset 3 của volume BitLocker ("-FVE-FS-").
var bitlockerSig = []byte("-FVE-FS-")

// detectBitLocker soi 512 byte boot sector: BitLocker để "-FVE-FS-" tại offset 3
// (chỗ NTFS để "NTFS    "). Thuần để test được.
func detectBitLocker(boot []byte) bool {
	return len(boot) >= 11 && bytes.Equal(boot[3:11], bitlockerSig)
}

// DetectEncryption kiểm tra một phân vùng (hoặc whole disk) xem có mã hoá không.
// Đọc boot sector cho BitLocker; hỏi diskutil cho FileVault/APFS-encrypted.
func DetectEncryption(ctx context.Context, bsd string) (EncryptionKind, error) {
	dev, err := os.OpenFile(blockdev.RawDevicePath(bsd), os.O_RDONLY, 0)
	if err != nil {
		return EncNone, fmt.Errorf("mở %s để đọc boot sector: %w", bsd, err)
	}
	defer dev.Close()

	boot := make([]byte, 512)
	if _, err := dev.ReadAt(boot, 0); err != nil {
		return EncNone, fmt.Errorf("đọc boot sector %s: %w", bsd, err)
	}
	if detectBitLocker(boot) {
		return EncBitLocker, nil
	}
	if k := diskutilEncryption(ctx, bsd); k != EncNone {
		return k, nil
	}
	return EncNone, nil
}

// diskutilEncryption hỏi diskutil xem phân vùng có phải APFS/CoreStorage đã mã
// hoá (FileVault). Lỗi/không rõ trả EncNone — detect là gợi ý, không chặn.
func diskutilEncryption(ctx context.Context, bsd string) EncryptionKind {
	var info struct {
		FileVault  bool   `json:"FileVault"`
		Encryption bool   `json:"Encryption"`
		APFSEncr   bool   `json:"APFSVolumeEncrypted"`
		Type       string `json:"FilesystemType"`
	}
	if err := diskutilInfoJSON(ctx, &info, bsd); err != nil {
		return EncNone
	}
	switch {
	case info.FileVault || info.APFSEncr:
		return EncFileVault
	case info.Encryption:
		return EncUnknownEnc
	default:
		return EncNone
	}
}
