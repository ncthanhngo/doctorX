// Package guard chặn mọi thao tác ghi có thể phá hỏng ổ đĩa.
//
// Script .bat gốc chỉ in cảnh báo rồi để người dùng tự cân nhắc có đụng vào
// thư mục Recovery/WinRE hay không. Ở đây danh sách cấm được enforce trong
// code, không có cờ override — người dùng không thể chọn nhầm.
package guard

import (
	"fmt"
	"strings"
)

// protectedRoots là các mục ở CẤP GỐC volume mà DoctorX không bao giờ ghi vào.
// Chỉ so khớp ở cấp gốc: thư mục tên "Recovery" do người dùng tạo ở cấp sâu
// hơn vẫn khôi phục được bình thường.
var protectedRoots = []string{
	// Windows
	"system volume information",
	"$recycle.bin",
	"recycler",
	"recovery",
	"$winreagent",
	"boot",
	"efi",
	"msocache",
	"hiberfil.sys",
	"pagefile.sys",
	"swapfile.sys",
	"dumpstack.log.tmp",
	// macOS
	".spotlight-v100",
	".fseventsd",
	".trashes",
	".documentrevisions-v100",
	".temporaryitems",
}

// ErrProtected báo đường dẫn nằm trong danh sách cấm ghi.
type ErrProtected struct {
	Path   string
	Reason string
}

func (e *ErrProtected) Error() string {
	return fmt.Sprintf("từ chối ghi vào %q: %s", e.Path, e.Reason)
}

// IsMacOSMetadata nhận ra file phụ trợ do chính macOS sinh ra, ở BẤT KỲ cấp nào.
//
// Khi chép file lên FAT/exFAT, macOS tạo file AppleDouble "._<tên>" để giữ
// metadata mà các filesystem này không lưu được, và đánh dấu ẩn. Chúng không
// phải dữ liệu người dùng, cũng không phải nạn nhân của virus: hiện chúng lên
// chỉ làm người dùng không tìm thấy dữ liệu thật, còn gỡ cờ ẩn thì tạo ra một
// đống file rác lộ thiên khi cắm sang Windows.
func IsMacOSMetadata(path string) bool {
	base := path
	if i := strings.LastIndexByte(base, '/'); i >= 0 {
		base = base[i+1:]
	}
	if strings.HasPrefix(base, "._") {
		return true
	}
	switch strings.ToLower(base) {
	case ".ds_store", ".localized", ".apledb", ".appledouble":
		return true
	}
	return false
}

// IsProtected cho biết path có bị cấm ghi không. path dùng dấu "/" và tính từ
// gốc volume, ví dụ "/System Volume Information/abc".
func IsProtected(path string) bool {
	root := firstComponent(path)
	if root == "" {
		// Chính gốc volume: không phải một entry, không ai ghi attribute lên nó.
		return true
	}
	root = strings.ToLower(root)
	for _, p := range protectedRoots {
		if root == p {
			return true
		}
	}
	return false
}

// AllowWrite trả về nil nếu được phép ghi vào path.
//
// Phải gọi cho MỌI entry trong thao tác đệ quy, không chỉ thư mục gốc được
// chọn: cây con có thể chứa junction/symlink trỏ ngược ra vùng cấm.
func AllowWrite(path string) error {
	if IsProtected(path) {
		return &ErrProtected{
			Path:   path,
			Reason: "đây là thư mục hệ thống hoặc vùng khôi phục, sửa vào có thể làm ổ không boot được",
		}
	}
	return nil
}

// firstComponent lấy thành phần đầu tiên của đường dẫn kiểu "/a/b/c" → "a".
func firstComponent(path string) string {
	path = strings.TrimPrefix(path, "/")
	if i := strings.IndexByte(path, '/'); i >= 0 {
		return path[:i]
	}
	return path
}
