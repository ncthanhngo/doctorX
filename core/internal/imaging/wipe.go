package imaging

import (
	"bytes"
	"context"
	"crypto/rand"
	"fmt"
	"io"
	"os"

	"github.com/soi/doctorx/core/internal/blockdev"
)

// Wipe XOÁ AN TOÀN toàn bộ một ổ ngoài — thao tác thanh lý/thu hồi của IT. Ghi đè
// toàn ổ bằng pattern để dữ liệu cũ không khôi phục được (NIST 800-88 "clear").
//
// Phá huỷ hoàn toàn, đi qua đúng cổng an toàn của Flash: external whole disk +
// target lock + xác nhận gõ tên ổ.

// Kiểu wipe.
const (
	WipeZero   = "zero"   // 1 lượt ghi 0x00 — đủ cho SSD/USB hiện đại (NIST clear)
	WipeRandom = "random" // 1 lượt ghi dữ liệu ngẫu nhiên
	Wipe3Pass  = "3pass"  // 0x00 → 0xFF → ngẫu nhiên
)

// WipeRequest mô tả yêu cầu xoá an toàn.
type WipeRequest struct {
	BSD         string `json:"bsd"`
	Method      string `json:"method"` // zero | random | 3pass
	Verify      bool   `json:"verify"` // chỉ có ý nghĩa với lượt zero: đọc lại kiểm 0
	ExpectSize  int64  `json:"expectSize"`
	ExpectModel string `json:"expectModel"`
	Confirm     string `json:"confirm"`
}

// WipeResult tổng hợp kết quả để dựng "chứng chỉ xoá".
type WipeResult struct {
	Method       string `json:"method"`
	Passes       int    `json:"passes"`
	BytesWritten int64  `json:"bytesWritten"`
	DeviceModel  string `json:"deviceModel"`
	Verified     bool   `json:"verified"`
}

// wipePass mô tả một lượt ghi: hoặc byte cố định, hoặc ngẫu nhiên.
type wipePass struct {
	fill   byte
	random bool
}

// passesFor quy method thành danh sách lượt ghi.
func passesFor(method string) ([]wipePass, error) {
	switch method {
	case WipeZero, "":
		return []wipePass{{fill: 0x00}}, nil
	case WipeRandom:
		return []wipePass{{random: true}}, nil
	case Wipe3Pass:
		return []wipePass{{fill: 0x00}, {fill: 0xFF}, {random: true}}, nil
	default:
		return nil, fmt.Errorf("kiểu wipe không hỗ trợ: %q (chọn zero, random, 3pass)", method)
	}
}

// Wipe thực thi. progress báo theo TỔNG số byte của mọi lượt (passes × dung lượng).
func Wipe(ctx context.Context, req WipeRequest, progress ProgressFunc) (*WipeResult, error) {
	passes, err := passesFor(req.Method)
	if err != nil {
		return nil, err
	}
	d, err := lockTarget(ctx, req.BSD, req.ExpectSize, req.ExpectModel, req.Confirm)
	if err != nil {
		return nil, err
	}
	if err := blockdev.UnmountDisk(ctx, d.BSD); err != nil {
		return nil, err
	}
	defer blockdev.MountDisk(ctx, d.BSD)

	dev, err := os.OpenFile(blockdev.RawDevicePath(d.BSD), os.O_RDWR, 0)
	if err != nil {
		return nil, fmt.Errorf("mở thiết bị để xoá: %w", err)
	}
	defer dev.Close()

	size := d.SizeBytes
	total := size * int64(len(passes))
	var written int64
	for _, p := range passes {
		if err := wipePassWrite(ctx, dev, size, p, func(done int64) {
			if progress != nil {
				progress(written+done, total)
			}
		}); err != nil {
			return nil, err
		}
		written += size
	}
	if err := dev.Sync(); err != nil {
		return nil, fmt.Errorf("đồng bộ ổ sau khi xoá: %w", err)
	}

	res := &WipeResult{
		Method: methodName(req.Method), Passes: len(passes),
		BytesWritten: written, DeviceModel: d.Model,
	}
	// Chỉ verify được lượt zero (đọc lại phải toàn 0). Random/3pass không kiểm được
	// nội dung mong đợi vì lượt cuối là ngẫu nhiên.
	if req.Verify && len(passes) == 1 && !passes[0].random {
		ok, err := verifyFill(ctx, dev, size, passes[0].fill)
		if err != nil {
			return res, fmt.Errorf("đọc lại để kiểm tra: %w", err)
		}
		if !ok {
			return res, fmt.Errorf("kiểm tra sau xoá thất bại: ổ còn dữ liệu khác 0x%02x", passes[0].fill)
		}
		res.Verified = true
	}
	return res, nil
}

func methodName(m string) string {
	if m == "" {
		return WipeZero
	}
	return m
}

// wipePassWrite ghi một lượt pattern phủ [0,size) theo chunk align sector.
func wipePassWrite(ctx context.Context, dst io.WriterAt, size int64, p wipePass, onProgress func(int64)) error {
	buf := make([]byte, bufSize)
	if !p.random {
		for i := range buf {
			buf[i] = p.fill
		}
	}
	var off int64
	for off < size {
		if err := ctx.Err(); err != nil {
			return err
		}
		n := chunkLen(off, size)
		if p.random {
			if _, err := rand.Read(buf[:n]); err != nil {
				return fmt.Errorf("sinh dữ liệu ngẫu nhiên: %w", err)
			}
		}
		if _, err := dst.WriteAt(buf[:n], off); err != nil {
			return fmt.Errorf("ghi ổ tại %d: %w", off, err)
		}
		off += n
		if onProgress != nil {
			onProgress(off)
		}
	}
	return nil
}

// verifyFill đọc lại [0,size) và xác nhận mọi byte đúng bằng fill.
func verifyFill(ctx context.Context, src io.ReaderAt, size int64, fill byte) (bool, error) {
	want := bytes.Repeat([]byte{fill}, bufSize)
	buf := make([]byte, bufSize)
	var off int64
	for off < size {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		n := chunkLen(off, size)
		if _, err := src.ReadAt(buf[:n], off); err != nil && err != io.EOF {
			return false, err
		}
		if !bytes.Equal(buf[:n], want[:n]) {
			return false, nil
		}
		off += n
	}
	return true, nil
}
