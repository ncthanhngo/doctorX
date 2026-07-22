package imaging

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/soi/doctorx/core/internal/blockdev"
)

// mkntfsEnv cho phép chỉ định đường dẫn mkntfs khi phát triển/kiểm thử; production
// dùng binary đóng gói cạnh doctorx-core.
const mkntfsEnv = "DOCTORX_MKNTFS"

// formatNTFS format một ổ về NTFS. macOS không có tool format NTFS, nên:
//
//  1. `diskutil partitionDisk` ghi bảng phân vùng + một phân vùng "Microsoft
//     Basic Data" (trên GPT, đúng loại NTFS cần) và format tạm FAT.
//  2. tháo toàn ổ để phân vùng không còn bận.
//  3. `mkntfs` (ntfs-3g, đóng gói kèm app) ghi đè NTFS lên phân vùng đó.
//  4. gắn lại best-effort (macOS mount NTFS chỉ-đọc).
//
// Chỉ hỗ trợ GPT: trên GPT loại phân vùng (GUID Microsoft Basic Data) đã đúng cho
// NTFS mà không phải sửa byte type như MBR. MBR+NTFS để lại cho phase Windows USB.
func formatNTFS(ctx context.Context, d blockdev.Disk, req FormatRequest) (*FormatResult, error) {
	scheme := strings.ToLower(strings.TrimSpace(req.Scheme))
	if scheme == "" {
		scheme = "gpt"
	}
	if scheme != "gpt" {
		return nil, fmt.Errorf("NTFS hiện chỉ hỗ trợ scheme GPT (nhận %q)", req.Scheme)
	}
	label, err := normalizeLabel("ntfs", req.Label)
	if err != nil {
		return nil, err
	}
	mkntfs, err := resolveMkntfs()
	if err != nil {
		return nil, err
	}

	// Bước 1: phân vùng (tạo + format FAT tạm, macOS tự mount).
	part := partitionArgs(d.BSD, label)
	if combined, err := exec.CommandContext(ctx, "diskutil", part...).CombinedOutput(); err != nil {
		return nil, fmt.Errorf("phân vùng %s thất bại (%w): %s", d.BSD, err, strings.TrimSpace(string(combined)))
	}

	// Bước 2: tìm phân vùng dữ liệu vừa tạo (theo nhãn, không phải EFI).
	partBSD, err := findDataPartition(ctx, d.BSD, label)
	if err != nil {
		return nil, err
	}

	// Bước 3: tháo toàn ổ rồi ghi NTFS đè lên.
	if err := blockdev.UnmountDisk(ctx, d.BSD); err != nil {
		return nil, err
	}
	defer blockdev.MountDisk(ctx, d.BSD)

	mk := mkntfsArgs(partBSD, label)
	if combined, err := exec.CommandContext(ctx, mkntfs, mk...).CombinedOutput(); err != nil {
		return nil, fmt.Errorf("mkntfs trên %s thất bại (%w): %s", partBSD, err, strings.TrimSpace(string(combined)))
	}
	return &FormatResult{FS: "ntfs", Scheme: scheme, Label: label}, nil
}

// partitionArgs dựng lệnh `diskutil partitionDisk`. Dùng personality "MS-DOS
// FAT32" để tạo phân vùng Microsoft Basic Data (đúng GUID cho NTFS trên GPT);
// mkntfs sẽ ghi đè filesystem ở bước sau. Tách riêng để test không chạy diskutil.
func partitionArgs(bsd, label string) []string {
	bsd = strings.TrimPrefix(bsd, "/dev/")
	return []string{"partitionDisk", bsd, "GPT", "MS-DOS FAT32", label, "100%"}
}

// mkntfsArgs dựng lệnh mkntfs: format nhanh (--quick, không zero toàn ổ), --force
// để bỏ qua cảnh báo "phân vùng có thể đang bận" (ta đã tháo mount), gắn nhãn.
func mkntfsArgs(partBSD, label string) []string {
	return []string{"--quick", "--force", "--label", label, blockdev.RawDevicePath(partBSD)}
}

// findDataPartition tìm phân vùng dữ liệu (khớp nhãn, không phải phân vùng hệ
// thống như EFI) dưới whole disk sau khi phân vùng xong.
func findDataPartition(ctx context.Context, wholeBSD, label string) (string, error) {
	disks, err := blockdev.ListExternalDisks(ctx)
	if err != nil {
		return "", err
	}
	for _, d := range disks {
		if d.BSD != wholeBSD {
			continue
		}
		for _, p := range d.Partitions {
			if !p.SystemPartition && p.Label == label {
				return p.BSD, nil
			}
		}
	}
	return "", fmt.Errorf("không tìm thấy phân vùng dữ liệu vừa tạo trên %s", wholeBSD)
}

// resolveMkntfs tìm binary mkntfs đóng gói kèm app (xem resolveBundled).
func resolveMkntfs() (string, error) {
	return resolveBundled(mkntfsEnv, "mkntfs")
}

// isExecutable trả true nếu path là file thường có bit thực thi.
func isExecutable(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && fi.Mode().IsRegular() && fi.Mode()&0o111 != 0
}
