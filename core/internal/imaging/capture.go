package imaging

import (
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"

	"github.com/soi/doctorx/core/internal/blockdev"
)

// Capture đọc toàn bộ một ổ ngoài và ghi ra một file ảnh (.img, hoặc .img.gz nếu
// nén) — chiều ngược của Flash. IT dùng để bắt nguyên một USB/ổ đang hỏng ra ảnh
// để phân tích/khôi phục sau, hoặc nhân bản máy mẫu.
//
// Chỉ ĐỌC ổ, không phá dữ liệu, nên không cần gõ xác nhận — nhưng vẫn chỉ nhận
// whole disk gắn ngoài để không lỡ đọc ổ hệ thống.

// CaptureRequest mô tả yêu cầu bắt ảnh.
type CaptureRequest struct {
	BSD      string `json:"bsd"`
	DestPath string `json:"destPath"`
	Compress bool   `json:"compress"` // nén gzip
}

// CaptureResult tổng hợp kết quả.
type CaptureResult struct {
	DestPath   string `json:"destPath"`
	BytesRead  int64  `json:"bytesRead"`
	SHA256     string `json:"sha256"` // hash của DỮ LIỆU THÔ (trước nén)
	Compressed bool   `json:"compressed"`
}

// Capture thực thi. progress báo theo số byte thô đã đọc trên tổng dung lượng ổ.
func Capture(ctx context.Context, req CaptureRequest, progress ProgressFunc) (*CaptureResult, error) {
	if req.DestPath == "" {
		return nil, fmt.Errorf("thiếu đường dẫn file đích")
	}
	disks, err := blockdev.ListExternalDisks(ctx)
	if err != nil {
		return nil, err
	}
	d, err := resolveTarget(disks, req.BSD)
	if err != nil {
		return nil, err
	}

	dev, err := os.OpenFile(blockdev.RawDevicePath(d.BSD), os.O_RDONLY, 0)
	if err != nil {
		return nil, fmt.Errorf("mở thiết bị để đọc: %w", err)
	}
	defer dev.Close()

	out, err := os.Create(req.DestPath)
	if err != nil {
		return nil, fmt.Errorf("tạo file ảnh: %w", err)
	}
	defer out.Close()

	var w io.Writer = out
	var gz *gzip.Writer
	if req.Compress {
		gz = gzip.NewWriter(out)
		w = gz
	}

	sum, err := captureStream(ctx, w, dev, d.SizeBytes, progress)
	if err != nil {
		return nil, err
	}
	if gz != nil {
		if err := gz.Close(); err != nil {
			return nil, fmt.Errorf("đóng gzip: %w", err)
		}
	}
	if err := out.Sync(); err != nil {
		return nil, fmt.Errorf("đồng bộ file ảnh: %w", err)
	}
	return &CaptureResult{
		DestPath: req.DestPath, BytesRead: d.SizeBytes,
		SHA256: sum, Compressed: req.Compress,
	}, nil
}

// captureStream đọc [0,size) từ src ghi tuần tự ra dst, đồng thời băm dữ liệu thô.
// Thuần theo io.ReaderAt/io.Writer để test được với file, không cần ổ thật.
func captureStream(ctx context.Context, dst io.Writer, src io.ReaderAt, size int64, progress ProgressFunc) (string, error) {
	h := sha256.New()
	buf := make([]byte, bufSize)
	var off int64
	for off < size {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		n := chunkLen(off, size)
		if _, err := src.ReadAt(buf[:n], off); err != nil && err != io.EOF {
			return "", fmt.Errorf("đọc ổ tại %d: %w", off, err)
		}
		h.Write(buf[:n])
		if _, err := dst.Write(buf[:n]); err != nil {
			return "", fmt.Errorf("ghi file ảnh: %w", err)
		}
		off += n
		if progress != nil {
			progress(off, size)
		}
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
