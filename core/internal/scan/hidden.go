// Package scan phát hiện dữ liệu bị giấu và dấu hiệu worm lây qua USB.
package scan

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	dfs "github.com/soi/doctorx/core/internal/fs"
	"github.com/soi/doctorx/core/internal/guard"
)

// Concealed là một mục bị giấu khỏi tầm nhìn người dùng.
type Concealed struct {
	Path     string    `json:"path"`
	Size     int64     `json:"size"`
	IsDir    bool      `json:"isDir"`
	Attrs    []string  `json:"attrs"`
	Modified time.Time `json:"modified"`
	// Protected đúng khi đây là thư mục hệ thống hợp lệ, không phải nạn nhân
	// của virus. UI phải hiện xám và không cho chọn.
	Protected bool `json:"protected"`
}

// FindConcealedRaw quét qua driver raw, cho biết CHÍNH XÁC cả bit Hidden lẫn
// System. Dùng cho FAT/exFAT — nơi DoctorX có driver riêng.
func FindConcealedRaw(ctx context.Context, vol dfs.Volume, opt dfs.WalkOpt) ([]Concealed, error) {
	var out []Concealed
	err := vol.Walk(ctx, opt, func(e *dfs.Entry) error {
		if !e.Attrs.IsConcealed() {
			return nil
		}
		out = append(out, Concealed{
			Path:      e.Path,
			Size:      e.Size,
			IsDir:     e.Attrs.IsDir(),
			Attrs:     e.Attrs.Names(),
			Modified:  e.Modified,
			Protected: IsNotUserData(e.Path),
		})
		return nil
	})
	return out, err
}

// ufHidden là cờ UF_HIDDEN của macOS. msdosfs và exfat map bit Hidden của DOS
// sang cờ này, nên quét qua mount thấy được Hidden — nhưng KHÔNG thấy được bit
// System. Đó là giới hạn cố hữu của đường quét này.
const ufHidden = 0x8000

// FindConcealedMounted quét qua mount point. Dùng cho filesystem mà DoctorX
// chưa có driver raw (NTFS, HFS+, APFS).
//
// Hạn chế đã biết: chỉ thấy bit Hidden. File chỉ có bit System sẽ không bị phát
// hiện. Với kịch bản virus USB điển hình (đặt cả Hidden lẫn System) thì vẫn bắt
// được, nhưng đây là lý do FAT/exFAT dùng đường raw.
func FindConcealedMounted(ctx context.Context, mountPoint string, maxDepth int) ([]Concealed, error) {
	var out []Concealed
	rootDepth := strings.Count(filepath.Clean(mountPoint), string(filepath.Separator))

	err := filepath.WalkDir(mountPoint, func(path string, d os.DirEntry, err error) error {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		if err != nil {
			return nil // thư mục không đọc được: bỏ qua, quét tiếp phần còn lại
		}
		if path == mountPoint {
			return nil
		}
		depth := strings.Count(filepath.Clean(path), string(filepath.Separator)) - rootDepth
		if maxDepth > 0 && depth > maxDepth {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		info, err := d.Info()
		if err != nil {
			return nil
		}
		st, ok := info.Sys().(*syscall.Stat_t)
		if !ok || st.Flags&ufHidden == 0 {
			return nil
		}

		rel := "/" + strings.TrimPrefix(strings.TrimPrefix(path, mountPoint), "/")
		out = append(out, Concealed{
			Path:      rel,
			Size:      info.Size(),
			IsDir:     d.IsDir(),
			Attrs:     []string{"hidden"},
			Modified:  info.ModTime(),
			Protected: IsNotUserData(rel),
		})
		return nil
	})
	if err != nil && ctx.Err() == nil {
		return out, err
	}
	return out, ctx.Err()
}

// systemPaths là các mục ở cấp gốc bị ẩn một cách hợp lệ — của Windows hoặc
// macOS, không phải do virus. Đưa vào kết quả nhưng đánh dấu Protected để người
// dùng không nhầm là dữ liệu của mình.
var systemPaths = map[string]bool{
	"system volume information": true,
	"$recycle.bin":              true,
	"recycler":                  true,
	"recovery":                  true,
	"$winreagent":               true,
	"boot":                      true,
	"efi":                       true,
	"msocache":                  true,
	"found.000":                 true,
	"hiberfil.sys":              true,
	"pagefile.sys":              true,
	"swapfile.sys":              true,
	".spotlight-v100":           true,
	".fseventsd":                true,
	".trashes":                  true,
	".documentrevisions-v100":   true,
	".temporaryitems":           true,
	"._.trashes":                true,
}

// IsNotUserData gộp hai nhóm mục KHÔNG phải dữ liệu của người dùng: thư mục hệ
// thống ở cấp gốc, và file phụ trợ do macOS sinh ra ở bất kỳ cấp nào. Cả hai
// đều bị ẩn hợp lệ, không phải nạn nhân của virus.
func IsNotUserData(path string) bool {
	return IsSystemPath(path) || guard.IsMacOSMetadata(path)
}

// IsSystemPath cho biết path là mục hệ thống ở cấp gốc.
func IsSystemPath(path string) bool {
	p := strings.TrimPrefix(path, "/")
	if i := strings.IndexByte(p, '/'); i >= 0 {
		p = p[:i]
	}
	return systemPaths[strings.ToLower(p)]
}
