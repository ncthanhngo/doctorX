package imaging

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"

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
	// Gắn lại best-effort sau khi xong: nếu image có filesystem macOS hiểu được
	// thì ổ dùng được ngay; nếu không, lỗi được bỏ qua.
	defer blockdev.MountDisk(ctx, d.BSD)

	dev, err := os.OpenFile(blockdev.RawDevicePath(d.BSD), os.O_RDWR, 0)
	if err != nil {
		return nil, fmt.Errorf("mở thiết bị để ghi: %w", err)
	}
	defer dev.Close()

	srcHash := sha256.New()
	written, err := copyImage(ctx, dev, io.TeeReader(src, srcHash), imgSize, progress)
	if err != nil {
		return nil, err
	}
	if err := dev.Sync(); err != nil {
		return nil, fmt.Errorf("đồng bộ ổ sau khi ghi: %w", err)
	}

	res := &FlashResult{BytesWritten: written, SourceSHA256: hex.EncodeToString(srcHash.Sum(nil))}
	if req.Verify {
		tgtHash, err := hashDevice(ctx, dev, imgSize)
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
