package imaging

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// Format XOÁ SẠCH một ổ ngoài và tạo filesystem mới. Thao tác PHÁ HUỶ, đi qua
// cùng cổng an toàn với Flash.
//
// Dùng `diskutil eraseDisk`: nó tự tháo mount, ghi bảng phân vùng (MBR/GPT) và
// chạy newfs trong một lệnh — đúng con đường macOS hỗ trợ, không cần tự dựng
// partition table. NTFS không nằm trong khả năng của diskutil nên tạm chưa hỗ trợ
// (chờ đóng gói mkntfs).

// FormatRequest mô tả yêu cầu format.
type FormatRequest struct {
	BSD         string `json:"bsd"`
	FS          string `json:"fs"`     // fat32 | exfat | ntfs
	Scheme      string `json:"scheme"` // mbr | gpt
	Label       string `json:"label"`
	ExpectSize  int64  `json:"expectSize"`
	ExpectModel string `json:"expectModel"`
	Confirm     string `json:"confirm"`
}

// FormatResult là kết quả format.
type FormatResult struct {
	FS     string `json:"fs"`
	Scheme string `json:"scheme"`
	Label  string `json:"label"`
}

// Format thực thi yêu cầu. NTFS trả lỗi rõ ràng cho tới khi mkntfs được đóng gói.
func Format(ctx context.Context, req FormatRequest) (*FormatResult, error) {
	d, err := lockTarget(ctx, req.BSD, req.ExpectSize, req.ExpectModel, req.Confirm)
	if err != nil {
		return nil, err
	}

	// NTFS đi đường riêng: diskutil không format được NTFS nên phải phân vùng rồi
	// gọi mkntfs (đóng gói kèm app) — xem format_ntfs.go.
	if strings.ToLower(strings.TrimSpace(req.FS)) == "ntfs" {
		return formatNTFS(ctx, d, req)
	}

	args, out, err := buildEraseArgs(req)
	if err != nil {
		return nil, err
	}

	cmd := exec.CommandContext(ctx, "diskutil", args...)
	if combined, err := cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("format %s thất bại (%w): %s", d.BSD, err, strings.TrimSpace(string(combined)))
	}
	return out, nil
}

// erasePersonality quy FS nội bộ về "personality" mà diskutil eraseDisk chấp nhận.
var erasePersonality = map[string]string{
	"fat32": "FAT32",
	"exfat": "ExFAT",
}

// eraseScheme quy scheme nội bộ về tên diskutil.
var eraseScheme = map[string]string{
	"mbr": "MBR",
	"gpt": "GPT",
}

// buildEraseArgs dựng tham số cho `diskutil eraseDisk` và chuẩn hoá nhãn. Tách
// riêng để kiểm thử mà không chạy diskutil. Trả thêm FormatResult mô tả kết quả
// mong đợi.
func buildEraseArgs(req FormatRequest) ([]string, *FormatResult, error) {
	fs := strings.ToLower(strings.TrimSpace(req.FS))
	pers, ok := erasePersonality[fs]
	if !ok {
		return nil, nil, fmt.Errorf("filesystem không hỗ trợ: %q (chọn fat32, exfat)", req.FS)
	}
	scheme := strings.ToLower(strings.TrimSpace(req.Scheme))
	if scheme == "" {
		scheme = "mbr" // mặc định: tương thích rộng nhất cho USB
	}
	schemeArg, ok := eraseScheme[scheme]
	if !ok {
		return nil, nil, fmt.Errorf("scheme không hỗ trợ: %q (chọn mbr, gpt)", req.Scheme)
	}

	label, err := normalizeLabel(fs, req.Label)
	if err != nil {
		return nil, nil, err
	}

	bsd := strings.TrimPrefix(req.BSD, "/dev/")
	args := []string{"eraseDisk", pers, label, schemeArg, bsd}
	return args, &FormatResult{FS: fs, Scheme: scheme, Label: label}, nil
}

// normalizeLabel áp ràng buộc nhãn theo filesystem. FAT32 chỉ nhận tối đa 11 ký
// tự IN HOA; exFAT rộng hơn (tối đa 15). Nhãn rỗng rơi về mặc định.
func normalizeLabel(fs, label string) (string, error) {
	label = strings.TrimSpace(label)
	if label == "" {
		label = "UNTITLED"
	}
	switch fs {
	case "fat32":
		label = strings.ToUpper(label)
		if len(label) > 11 {
			label = label[:11]
		}
		if strings.ContainsAny(label, `*?/\|.,;:+=[]<>"`) {
			return "", fmt.Errorf("nhãn FAT32 chứa ký tự không hợp lệ: %q", label)
		}
	case "exfat":
		if len(label) > 15 {
			label = label[:15]
		}
	}
	return label, nil
}
