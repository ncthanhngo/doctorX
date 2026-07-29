package imaging

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	"github.com/soi/doctorx/core/internal/blockdev"
)

// sectorSize là đơn vị align khi ghi ra rdisk. rdisk từ chối ghi lệch sector,
// nên block cuối cùng của image (nếu lẻ) được đệm 0 tới bội số này.
const sectorSize = 512

// bufSize là kích thước một lần đọc/ghi. 4 MiB — bội số của sector, đủ lớn để
// throughput không bị chi phối bởi số lượt syscall.
const bufSize = 4 << 20

// Target là thông tin một ổ đích để UI hiển thị và người dùng xác nhận.
type Target struct {
	BSD          string   `json:"bsd"`
	Model        string   `json:"model"`
	SizeBytes    int64    `json:"sizeBytes"`
	Removable    bool     `json:"removable"`
	BusProtocol  string   `json:"busProtocol"`
	MountPoints  []string `json:"mountPoints"`
	ConfirmToken string   `json:"confirmToken"` // chuỗi người dùng phải gõ lại
}

// Preflight phân giải một ổ đích và trả thông tin để UI xác nhận. Không chạm ổ.
func Preflight(ctx context.Context, bsd string) (*Target, error) {
	disks, err := blockdev.ListExternalDisks(ctx)
	if err != nil {
		return nil, err
	}
	d, err := resolveTarget(disks, bsd)
	if err != nil {
		return nil, err
	}
	var mounts []string
	for _, p := range d.Partitions {
		if p.MountPoint != "" {
			mounts = append(mounts, p.MountPoint)
		}
	}
	return &Target{
		BSD:          d.BSD,
		Model:        d.Model,
		SizeBytes:    d.SizeBytes,
		Removable:    d.Removable,
		BusProtocol:  d.BusProtocol,
		MountPoints:  mounts,
		ConfirmToken: canonicalConfirm(d),
	}, nil
}

// FlashRequest mô tả một yêu cầu ghi image ra ổ.
type FlashRequest struct {
	BSD         string `json:"bsd"`
	ImagePath   string `json:"imagePath"`
	ExpectSize  int64  `json:"expectSize"`  // target lock: dung lượng ổ lúc preflight
	ExpectModel string `json:"expectModel"` // target lock: model lúc preflight
	Confirm     string `json:"confirm"`     // chuỗi xác nhận người dùng gõ lại
	Verify      bool   `json:"verify"`      // đọc lại + so hash sau khi ghi
}

// FlashResult là kết quả một lần flash.
type FlashResult struct {
	BytesWritten int64  `json:"bytesWritten"`
	Verified     bool   `json:"verified"`
	SourceSHA256 string `json:"sourceSha256"`
	TargetSHA256 string `json:"targetSha256,omitempty"`
}

// ProgressFunc nhận số byte đã ghi trên tổng số byte của image.
type ProgressFunc func(done, total int64)

// Flash ghi ImagePath ra whole disk BSD. Đây là thao tác PHÁ HUỶ — xoá sạch mọi
// dữ liệu trên ổ. Trình tự: phân giải + khoá target → tháo toàn ổ → ghi tuần tự
// → (tuỳ chọn) verify → gắn lại best-effort.
func Flash(ctx context.Context, req FlashRequest, progress ProgressFunc) (*FlashResult, error) {
	d, err := lockTarget(ctx, req.BSD, req.ExpectSize, req.ExpectModel, req.Confirm)
	if err != nil {
		return nil, err
	}

	src, err := os.Open(req.ImagePath)
	if err != nil {
		return nil, fmt.Errorf("mở image nguồn: %w", err)
	}
	defer src.Close()
	st, err := src.Stat()
	if err != nil {
		return nil, err
	}
	imgSize := st.Size()
	if imgSize == 0 {
		return nil, errors.New("image nguồn rỗng")
	}
	if imgSize > d.SizeBytes {
		return nil, fmt.Errorf("image %d byte lớn hơn dung lượng ổ %d byte", imgSize, d.SizeBytes)
	}

	if err := blockdev.UnmountDisk(ctx, d.BSD); err != nil {
		return nil, err
	}
	defer blockdev.MountDisk(ctx, d.BSD)

	// Tính hash nguồn trước khi ghi.
	srcHash := sha256.New()
	if _, err := io.Copy(srcHash, src); err != nil {
		return nil, fmt.Errorf("tính hash nguồn: %w", err)
	}
	src.Close()

	// Ghi bằng /bin/dd (Apple-signed, có quyền hệ thống) thay vì os.OpenFile
	// trực tiếp — macOS 15 TCC chặn daemon ad-hoc mở /dev/rdiskN O_RDWR.
	devPath := blockdev.RawDevicePath(d.BSD)
	if err := writeWithDD(ctx, req.ImagePath, devPath, imgSize, progress); err != nil {
		return nil, err
	}

	res := &FlashResult{BytesWritten: imgSize, SourceSHA256: hex.EncodeToString(srcHash.Sum(nil))}
	if req.Verify {
		// Đọc lại bằng dd (O_RDONLY cũng có thể bị TCC).
		tgtHash, err := hashDeviceDD(ctx, devPath, imgSize)
		if err != nil {
			return res, fmt.Errorf("đọc lại ổ để kiểm tra: %w", err)
		}
		res.TargetSHA256 = tgtHash
		if tgtHash != res.SourceSHA256 {
			return res, fmt.Errorf("kiểm tra sau ghi thất bại: hash ổ (%s) khác hash nguồn (%s)",
				tgtHash, res.SourceSHA256)
		}
		res.Verified = true
	}
	return res, nil
}

// writeWithDD ghi image ra raw device bằng /bin/dd — binary ký Apple có quyền
// truy cập thiết bị trên macOS 15+ mà daemon ad-hoc không có.
func writeWithDD(ctx context.Context, src, dst string, size int64, progress ProgressFunc) error {
	bs := fmt.Sprintf("bs=%d", bufSize)
	cmd := exec.CommandContext(ctx, "/bin/dd",
		"if="+src, "of="+dst, bs, "conv=notrunc")
	cmd.Stderr = nil // bỏ qua stats output
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("ghi ổ bằng dd: %w: %s", err, strings.TrimSpace(string(out)))
	}
	if progress != nil {
		progress(size, size)
	}
	return nil
}

// hashDeviceDD đọc lại size byte đầu của device qua dd rồi băm SHA-256. Dùng dd
// thay vì os.OpenFile vì TCC có thể chặn cả O_RDONLY.
func hashDeviceDD(ctx context.Context, dev string, size int64) (string, error) {
	count := (size + int64(bufSize) - 1) / int64(bufSize)
	cmd := exec.CommandContext(ctx, "/bin/dd",
		"if="+dev, fmt.Sprintf("bs=%d", bufSize), fmt.Sprintf("count=%d", count))
	cmd.Stderr = nil
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("đọc lại ổ bằng dd: %w", err)
	}
	// dd có thể đọc nhiều hơn size (count * bs), chỉ hash đúng size byte.
	if int64(len(out)) > size {
		out = out[:size]
	}
	h := sha256.Sum256(out)
	return hex.EncodeToString(h[:]), nil
}

// copyImage đọc size byte từ src ghi tuần tự ra dst. Block cuối được đệm 0 tới
// biên sector để thoả ràng buộc align của rdisk. Trả về số byte THỰC của image
// (không tính phần đệm). Tách riêng để test được với file thường, không cần ổ.
func copyImage(ctx context.Context, dst io.WriterAt, src io.Reader, size int64, progress ProgressFunc) (int64, error) {
	buf := make([]byte, bufSize)
	var off int64
	for off < size {
		if err := ctx.Err(); err != nil {
			return off, err
		}
		want := int64(len(buf))
		if rem := size - off; rem < want {
			want = rem
		}
		n, err := io.ReadFull(src, buf[:want])
		if err != nil && err != io.ErrUnexpectedEOF {
			return off, fmt.Errorf("đọc image tại %d: %w", off, err)
		}
		writeN := padToSector(buf[:n])
		if _, werr := dst.WriteAt(buf[:writeN], off); werr != nil {
			return off, fmt.Errorf("ghi ổ tại %d: %w", off, werr)
		}
		off += int64(n)
		if progress != nil {
			progress(off, size)
		}
	}
	return off, nil
}

// padToSector đệm 0 phần đuôi của buf tới bội số sector và trả độ dài đã đệm.
// buf phải có sức chứa tới biên sector (bufSize là bội số sector nên luôn đủ).
func padToSector(buf []byte) int {
	n := len(buf)
	if r := n % sectorSize; r != 0 {
		pad := sectorSize - r
		for i := 0; i < pad; i++ {
			buf = append(buf, 0)
		}
		n += pad
	}
	return n
}

// hashDevice đọc size byte đầu của dst và trả SHA-256 (hex). Đọc theo bội số
// sector rồi chỉ băm đúng size byte thật.
func hashDevice(ctx context.Context, dst io.ReaderAt, size int64) (string, error) {
	h := sha256.New()
	buf := make([]byte, bufSize)
	var off int64
	for off < size {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		want := size - off
		if want > int64(len(buf)) {
			want = int64(len(buf))
		}
		readN := int64(padToSectorLen(int(want)))
		if _, err := dst.ReadAt(buf[:readN], off); err != nil && err != io.EOF {
			return "", err
		}
		h.Write(buf[:want])
		off += want
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func padToSectorLen(n int) int {
	if r := n % sectorSize; r != 0 {
		return n + (sectorSize - r)
	}
	return n
}
