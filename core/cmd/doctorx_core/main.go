// doctorx-core là phần lõi của DoctorX: quét ổ ngoài, phát hiện dữ liệu bị
// giấu, cứu dữ liệu và gỡ cờ ẩn.
//
// Chạy được ở hai chế độ:
//   - CLI (mặc định): dùng trực tiếp từ Terminal, tiện kiểm chứng.
//   - `serve`: daemon nói chuyện với app SwiftUI qua Unix socket.
//
// Các lệnh đọc không cần quyền quản trị. Lệnh `unhide` và `rollback` ghi thẳng
// xuống thiết bị nên phải chạy bằng sudo.
package main

import (
	"context"
	"crypto/rand"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/soi/doctorx/core/internal/blockdev"
	"github.com/soi/doctorx/core/internal/fsprobe"
)

const usage = `doctorx-core — cứu dữ liệu USB và ổ cứng gắn ngoài trên macOS

  doctorx-core list
        Liệt kê ổ gắn ngoài và các phân vùng.

  doctorx-core scan <bsd>
        Tìm dữ liệu bị giấu và dấu hiệu worm. Không cần quyền quản trị.

  doctorx-core hidden-dirs <bsd>
        Liệt kê thư mục bị giấu ở gốc ổ (tương đương "dir /b /a:hd").

  doctorx-core copy <bsd> <đường/dẫn> [...] [-dest DIR]
        Sao chép dữ liệu ra nơi an toàn. Không ghi gì lên ổ nguồn.

  sudo doctorx-core unhide <bsd> <đường/dẫn> [-recursive]
        Gỡ cờ Hidden+System. Tự tháo mount và gắn lại.

  sudo doctorx-core rollback <journal-id> <bsd>
        Hoàn tác một lần unhide.

  doctorx-core serve [-socket PATH]
        Chạy ở chế độ daemon cho app DoctorX.

Ví dụ:
  doctorx-core list
  doctorx-core scan disk4s1
  sudo doctorx-core unhide disk4s1 "/Anh gia dinh" -recursive
`

func main() {
	if len(os.Args) < 2 {
		fmt.Print(usage)
		os.Exit(2)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	var err error
	switch os.Args[1] {
	case "list":
		err = cmdList(ctx)
	case "scan":
		err = cmdScan(ctx, os.Args[2:])
	case "hidden-dirs":
		err = cmdHiddenDirs(ctx, os.Args[2:])
	case "copy":
		err = cmdCopy(ctx, os.Args[2:])
	case "unhide":
		err = cmdUnhide(ctx, os.Args[2:])
	case "rollback":
		err = cmdRollback(ctx, os.Args[2:])
	case "serve":
		err = cmdServe(ctx, os.Args[2:])
	case "-h", "--help", "help":
		fmt.Print(usage)
		return
	default:
		fmt.Fprintf(os.Stderr, "lệnh không hợp lệ: %s\n\n%s", os.Args[1], usage)
		os.Exit(2)
	}

	if err != nil {
		fmt.Fprintf(os.Stderr, "Lỗi: %v\n", err)
		os.Exit(1)
	}
}

func cmdList(ctx context.Context) error {
	disks, err := blockdev.ListExternalDisks(ctx)
	if err != nil {
		return err
	}
	if len(disks) == 0 {
		fmt.Println("Không tìm thấy ổ gắn ngoài nào.")
		return nil
	}
	for _, d := range disks {
		fmt.Printf("%s  %s  %s  (%s)\n", d.BSD, humanSize(d.SizeBytes), d.Model, d.BusProtocol)
		for _, p := range d.Partitions {
			flag := " "
			if p.SystemPartition {
				flag = "!"
			}
			label := p.Label
			if label == "" {
				label = "(không nhãn)"
			}
			fmt.Printf("  %s %-10s %-10s %-10s %s\n", flag, p.BSD, p.FSType, humanSize(p.SizeBytes), label)
		}
	}
	fmt.Println("\n! = phân vùng hệ thống, DoctorX không ghi vào.")
	return nil
}

// openVolume mở phân vùng ở chế độ chỉ đọc và trả kèm thông tin mount.
func openVolume(ctx context.Context, bsd string, write bool) (*fsprobe.Session, blockdev.Partition, error) {
	part, err := findPartition(ctx, bsd)
	if err != nil {
		return nil, part, err
	}
	s, err := fsprobe.Open(bsd, fsprobe.OpenOpts{Write: write})
	return s, part, err
}

// findPartition tra cứu phân vùng theo tên BSD.
//
// Cũng chấp nhận đường dẫn tới file ảnh đĩa: khi ổ có dấu hiệu hỏng, cách làm
// an toàn là dump ra file bằng dd rồi phân tích trên bản sao thay vì thao tác
// trực tiếp. Đường này cũng cho phép kiểm thử không cần quyền quản trị.
func findPartition(ctx context.Context, bsd string) (blockdev.Partition, error) {
	if isImageFile(bsd) {
		return blockdev.Partition{BSD: bsd, FSType: "", Label: filepath.Base(bsd)}, nil
	}
	bsd = strings.TrimPrefix(bsd, "/dev/")
	disks, err := blockdev.ListExternalDisks(ctx)
	if err != nil {
		return blockdev.Partition{}, err
	}
	for _, d := range disks {
		for _, p := range d.Partitions {
			if p.BSD == bsd {
				return p, nil
			}
		}
	}
	return blockdev.Partition{}, fmt.Errorf("không thấy phân vùng %q trong danh sách ổ gắn ngoài — chạy `doctorx-core list` để xem danh sách", bsd)
}

// journalDir đặt journal trong Application Support, không phải thư mục tạm:
// người dùng phải hoàn tác được cả sau khi khởi động lại máy.
func journalDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	if sudoUser := os.Getenv("SUDO_USER"); sudoUser != "" && os.Geteuid() == 0 {
		// Chạy qua sudo: lưu vào home thật của người dùng, không phải /var/root.
		home = filepath.Join("/Users", sudoUser)
	}
	dir := filepath.Join(home, "Library", "Application Support", "DoctorX", "journal")
	return dir, os.MkdirAll(dir, 0o700)
}

// isImageFile phân biệt đường dẫn file ảnh với tên thiết bị kiểu "disk4s1".
func isImageFile(arg string) bool {
	if !strings.ContainsAny(arg, "/.") {
		return false
	}
	if strings.HasPrefix(arg, "/dev/") {
		return false
	}
	st, err := os.Stat(arg)
	return err == nil && st.Mode().IsRegular()
}

// openTarget mở thiết bị hoặc file ảnh, tuỳ theo dạng tham số.
func openTarget(target string, write bool) (*fsprobe.Session, error) {
	opts := fsprobe.OpenOpts{Write: write}
	if isImageFile(target) {
		return fsprobe.OpenPath(target, opts)
	}
	return fsprobe.Open(target, opts)
}

func humanSize(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGTPE"[exp])
}

// splitArgs tách cờ khỏi tham số vị trí.
//
// Gói flag của Go ngừng phân tích ngay khi gặp tham số vị trí đầu tiên, nên
// `unhide disk4s1 /thu-muc -recursive` sẽ âm thầm bỏ qua -recursive. Đó đúng là
// cú pháp tự nhiên nhất với người dùng, nên phải sắp lại thay vì bắt họ nhớ
// đặt cờ lên trước.
func splitArgs(args []string) (flags, positional []string) {
	for _, a := range args {
		if strings.HasPrefix(a, "-") && a != "-" {
			flags = append(flags, a)
		} else {
			positional = append(positional, a)
		}
	}
	return flags, positional
}

// newJournalID sinh mã journal duy nhất.
//
// Chỉ dùng dấu thời gian tới giây là không đủ: hai thao tác trong cùng một giây
// sinh trùng tên, và journal mở bằng O_EXCL nên lần thứ hai thất bại. Thêm hậu
// tố ngẫu nhiên để mã vừa duy nhất vừa còn đọc được bằng mắt.
func newJournalID() string {
	var b [3]byte
	if _, err := rand.Read(b[:]); err != nil {
		return time.Now().Format("20060102-150405.000000")
	}
	return fmt.Sprintf("%s-%x", time.Now().Format("20060102-150405"), b)
}
