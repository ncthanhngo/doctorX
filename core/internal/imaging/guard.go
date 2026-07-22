// Package imaging cài đặt đường GHI PHÁ HUỶ toàn ổ kiểu Rufus (flash image).
//
// Đây là subsystem TÁCH BIỆT với luồng rescue: nó KHÔNG đi qua guard.RangeSet
// hay blockdev.Writer (vốn chỉ cho lật ≤8 byte metadata và có journal). Ghi cả
// gigabyte không thể journal, nên an toàn ở đây dựa vào ba cổng chặn khác:
//
//  1. chỉ nhận whole disk GẮN NGOÀI — ổ nội bộ/khởi động không bao giờ lọt vào
//  2. target lock — dung lượng & model phải khớp thiết bị lúc preflight
//  3. xác nhận tường minh — chuỗi confirm do người dùng gõ lại phải khớp
package imaging

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/soi/doctorx/core/internal/blockdev"
)

// wholeDiskRe khớp tên whole disk ("disk4"), KHÔNG khớp phân vùng ("disk4s2")
// hay APFS volume. Flash luôn ghi ra whole disk.
var wholeDiskRe = regexp.MustCompile(`^disk[0-9]+$`)

// ErrNotWholeDisk báo target không phải whole disk.
type ErrNotWholeDisk struct{ BSD string }

func (e *ErrNotWholeDisk) Error() string {
	return fmt.Sprintf("%q không phải whole disk — flash chỉ ghi ra ổ dạng diskN, không ghi ra phân vùng", e.BSD)
}

// resolveTarget tìm ổ ngoài khớp bsd trong danh sách. Trả lỗi nếu bsd không phải
// whole disk, hoặc không có trong danh sách ổ ngoài (nghĩa là ổ nội bộ, ổ khởi
// động, hoặc thiết bị không tồn tại) — chặn tận gốc việc ghi nhầm ổ hệ thống.
//
// Nhận []Disk làm tham số thay vì tự gọi diskutil để hàm thuần và test được.
func resolveTarget(disks []blockdev.Disk, bsd string) (blockdev.Disk, error) {
	bsd = strings.TrimPrefix(bsd, "/dev/")
	if !wholeDiskRe.MatchString(bsd) {
		return blockdev.Disk{}, &ErrNotWholeDisk{BSD: bsd}
	}
	for _, d := range disks {
		if d.BSD == bsd {
			if d.Internal {
				return blockdev.Disk{}, fmt.Errorf("%q là ổ nội bộ, DoctorX từ chối ghi", bsd)
			}
			return d, nil
		}
	}
	return blockdev.Disk{}, fmt.Errorf("%q không nằm trong danh sách ổ ngoài — chỉ flash được ổ gắn ngoài", bsd)
}

// canonicalConfirm là chuỗi người dùng phải gõ lại để xác nhận đúng ổ (mô phỏng
// hộp thoại của Rufus). Dùng model nếu có, không thì rơi về tên BSD.
func canonicalConfirm(d blockdev.Disk) string {
	if m := strings.TrimSpace(d.Model); m != "" {
		return m
	}
	return d.BSD
}

// checkLock enforce target lock: ổ hiện tại phải khớp thứ người dùng đã thấy lúc
// preflight (dung lượng + model), và chuỗi confirm phải khớp. Ngăn trường hợp ổ
// bị rút rồi cắm ổ khác vào cùng tên BSD giữa preflight và flash.
func checkLock(d blockdev.Disk, expectSize int64, expectModel, confirm string) error {
	if expectSize != 0 && d.SizeBytes != expectSize {
		return fmt.Errorf("dung lượng ổ đổi từ %d sang %d byte — ổ có thể đã bị thay; hãy quét lại",
			expectSize, d.SizeBytes)
	}
	if expectModel != "" && strings.TrimSpace(d.Model) != strings.TrimSpace(expectModel) {
		return fmt.Errorf("model ổ đổi từ %q sang %q — ổ có thể đã bị thay; hãy quét lại", expectModel, d.Model)
	}
	if confirm != canonicalConfirm(d) {
		return fmt.Errorf("xác nhận không khớp: cần gõ đúng %q để ghi đè ổ", canonicalConfirm(d))
	}
	return nil
}
