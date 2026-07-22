// Package rescue thực hiện các thao tác cứu dữ liệu.
//
// Phân công rõ ràng giữa hai đường:
//   - copy-out ĐỌC qua mount point bình thường của macOS. Đo trên macOS 15.6
//     cho thấy file Hidden+System vẫn đọc được qua mount, nên không cần đụng
//     raw device để lấy nội dung — đơn giản hơn và không có rủi ro ghi nhầm.
//   - unhide GHI qua raw device, vì không API nào của macOS xoá được bit System.
package rescue

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// CopyRequest mô tả một lần copy-out.
type CopyRequest struct {
	// SourceMount là mount point của ổ nguồn, vd "/Volumes/KINGSTON".
	SourceMount string
	// Paths là các đường dẫn tương đối so với SourceMount cần cứu.
	Paths []string
	// Dest là thư mục đích trên ổ hệ thống.
	Dest string
}

// Skipped ghi lại một mục không cứu được trọn vẹn.
type Skipped struct {
	Path   string `json:"path"`
	Reason string `json:"reason"`
}

// CopyResult tổng kết một lần copy-out.
type CopyResult struct {
	FilesCopied int       `json:"filesCopied"`
	BytesCopied int64     `json:"bytesCopied"`
	Skipped     []Skipped `json:"skipped"`
	RenameMap   string    `json:"renameMap,omitempty"`
}

// Progress được gọi định kỳ để cập nhật UI.
type Progress func(file string, doneBytes, totalBytes int64, filesDone int)

const partSuffix = ".doctorx-part"

// CopyOut sao chép các đường dẫn được chọn sang thư mục đích.
//
// Nguyên tắc: không bao giờ ghi lên ổ nguồn, và luôn giữ lại phần dữ liệu đọc
// được. File lỗi sector giữa chừng vẫn được giữ với phần hỏng điền 0 và ghi vào
// Skipped — dữ liệu một phần vẫn hơn không có gì.
func CopyOut(ctx context.Context, req CopyRequest, prog Progress) (*CopyResult, error) {
	if err := validateDest(req.SourceMount, req.Dest); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(req.Dest, 0o755); err != nil {
		return nil, fmt.Errorf("tạo thư mục đích: %w", err)
	}

	res := &CopyResult{}
	renames := map[string]string{}

	for _, rel := range req.Paths {
		src := filepath.Join(req.SourceMount, filepath.Clean("/"+rel))
		if err := copyTree(ctx, src, req.Dest, req.SourceMount, res, renames, prog); err != nil {
			if errors.Is(err, context.Canceled) {
				return res, err
			}
			res.Skipped = append(res.Skipped, Skipped{Path: rel, Reason: err.Error()})
		}
	}

	if len(renames) > 0 {
		mapPath := filepath.Join(req.Dest, "_doctorx-doi-ten.txt")
		if err := writeRenameMap(mapPath, renames); err == nil {
			res.RenameMap = mapPath
		}
	}
	return res, nil
}

// copyTree sao chép một file hoặc cả cây thư mục.
func copyTree(ctx context.Context, src, destRoot, srcRoot string, res *CopyResult,
	renames map[string]string, prog Progress) error {

	return filepath.WalkDir(src, func(path string, d os.DirEntry, err error) error {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		if err != nil {
			res.Skipped = append(res.Skipped, Skipped{Path: path, Reason: "không đọc được mục: " + err.Error()})
			return nil // đi tiếp phần còn lại của cây
		}

		rel, relErr := filepath.Rel(srcRoot, path)
		if relErr != nil {
			return nil
		}
		safeRel, renamed := sanitizeRelPath(rel)
		if renamed {
			renames[safeRel] = rel
		}
		target := filepath.Join(destRoot, safeRel)

		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		if !d.Type().IsRegular() {
			res.Skipped = append(res.Skipped, Skipped{Path: rel, Reason: "không phải file thường, bỏ qua"})
			return nil
		}

		n, copyErr := copyFile(ctx, path, target, prog, res.FilesCopied)
		res.BytesCopied += n
		if copyErr != nil {
			if errors.Is(copyErr, context.Canceled) {
				return copyErr
			}
			// Giữ lại file đã copy được một phần, chỉ ghi nhận là không trọn vẹn.
			res.Skipped = append(res.Skipped, Skipped{Path: rel, Reason: copyErr.Error()})
		}
		res.FilesCopied++
		return nil
	})
}

// copyFile sao chép một file qua temp rồi rename, để không bao giờ để lại file
// dở dang mang tên thật.
func copyFile(ctx context.Context, src, dst string, prog Progress, filesDone int) (int64, error) {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return 0, err
	}
	in, err := os.Open(src)
	if err != nil {
		return 0, fmt.Errorf("mở nguồn: %w", err)
	}
	defer in.Close()

	var total int64
	if st, err := in.Stat(); err == nil {
		total = st.Size()
	}

	tmp := dst + partSuffix
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return 0, fmt.Errorf("tạo file tạm: %w", err)
	}
	// Dọn temp nếu thoát bất thường; sau rename thành công thì Remove là no-op.
	defer func() { out.Close(); os.Remove(tmp) }()

	written, copyErr := copyWithProgress(ctx, out, in, src, total, prog, filesDone)
	if errors.Is(copyErr, context.Canceled) {
		return written, copyErr
	}
	if err := out.Close(); err != nil && copyErr == nil {
		copyErr = err
	}
	if err := os.Rename(tmp, dst); err != nil {
		return written, fmt.Errorf("đổi tên file tạm: %w", err)
	}
	if st, err := os.Stat(src); err == nil {
		os.Chtimes(dst, time.Now(), st.ModTime())
	}
	return written, copyErr
}

// copyBufSize đủ lớn để không bị chi phí syscall chi phối, đủ nhỏ để tiến trình
// cập nhật mượt trên file lớn.
const copyBufSize = 1 << 20

func copyWithProgress(ctx context.Context, dst io.Writer, src io.Reader, name string,
	total int64, prog Progress, filesDone int) (int64, error) {

	buf := make([]byte, copyBufSize)
	var done int64
	for {
		if err := ctx.Err(); err != nil {
			return done, err
		}
		n, rerr := src.Read(buf)
		if n > 0 {
			if _, werr := dst.Write(buf[:n]); werr != nil {
				return done, fmt.Errorf("ghi đích: %w", werr)
			}
			done += int64(n)
			if prog != nil {
				prog(name, done, total, filesDone)
			}
		}
		if rerr == io.EOF {
			return done, nil
		}
		if rerr != nil {
			return done, fmt.Errorf("đọc lỗi ở byte %d: %w", done, rerr)
		}
	}
}

// validateDest chặn việc chọn đích nằm ngay trên ổ đang cứu — vừa vô nghĩa vừa
// có nguy cơ ghi đè chính dữ liệu cần cứu.
func validateDest(srcMount, dest string) error {
	if dest == "" {
		return errors.New("chưa chọn thư mục đích")
	}
	absDest, err := filepath.Abs(dest)
	if err != nil {
		return err
	}
	absSrc, err := filepath.Abs(srcMount)
	if err != nil {
		return err
	}
	if absDest == absSrc || strings.HasPrefix(absDest+string(filepath.Separator), absSrc+string(filepath.Separator)) {
		return fmt.Errorf("thư mục đích %q nằm trên chính ổ đang cứu — hãy chọn nơi khác", dest)
	}

	// So sánh device id để bắt cả trường hợp đích trỏ tới cùng volume qua
	// đường dẫn khác (symlink, /Volumes trùng tên).
	var sSrc, sDst syscall.Stat_t
	if err := syscall.Stat(absSrc, &sSrc); err != nil {
		return nil // nguồn chưa mount thì để bước sau báo lỗi cụ thể hơn
	}
	probe := absDest
	for {
		if err := syscall.Stat(probe, &sDst); err == nil {
			break
		}
		parent := filepath.Dir(probe)
		if parent == probe {
			return nil
		}
		probe = parent
	}
	if sSrc.Dev == sDst.Dev {
		return fmt.Errorf("thư mục đích nằm trên cùng ổ với nguồn — hãy chọn nơi khác")
	}
	return nil
}

// invalidNameChars là các ký tự không dùng được trong tên file trên macOS.
// Windows cho phép nhiều thứ macOS không cho, nên tên từ USB có thể phải đổi.
const invalidNameChars = "/:\x00"

// sanitizeRelPath làm sạch từng thành phần đường dẫn, trả về cờ báo có đổi không.
func sanitizeRelPath(rel string) (string, bool) {
	parts := strings.Split(rel, string(filepath.Separator))
	changed := false
	for i, p := range parts {
		s := sanitizeName(p)
		if s != p {
			changed = true
		}
		parts[i] = s
	}
	return filepath.Join(parts...), changed
}

func sanitizeName(name string) string {
	var b strings.Builder
	for _, r := range name {
		if r < 0x20 || strings.ContainsRune(invalidNameChars, r) {
			b.WriteRune('_')
			continue
		}
		b.WriteRune(r)
	}
	s := strings.TrimRight(b.String(), " .")
	if s == "" {
		s = "_"
	}
	return s
}

func writeRenameMap(path string, renames map[string]string) error {
	var b strings.Builder
	b.WriteString("Các mục đã đổi tên vì tên gốc không hợp lệ trên macOS.\n")
	b.WriteString("tên mới  <-  tên gốc\n\n")
	for newName, oldName := range renames {
		fmt.Fprintf(&b, "%s  <-  %s\n", newName, oldName)
	}
	return os.WriteFile(path, []byte(b.String()), 0o644)
}

// DefaultDest sinh thư mục đích mặc định trong home của người dùng.
func DefaultDest(home, volumeLabel string, now time.Time) string {
	label := sanitizeName(volumeLabel)
	if label == "" || label == "_" {
		label = "USB"
	}
	return filepath.Join(home, "DoctorX Rescued", fmt.Sprintf("%s-%s", label, now.Format("20060102-150405")))
}
