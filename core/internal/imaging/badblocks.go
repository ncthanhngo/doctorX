package imaging

import (
	"bytes"
	"context"
	"fmt"
	"os"

	"github.com/soi/doctorx/core/internal/blockdev"
)

// BadRange là một khoảng byte đọc/ghi lỗi trên ổ.
type BadRange struct {
	Offset int64 `json:"offset"`
	Length int64 `json:"length"`
}

// BadBlocksRequest mô tả yêu cầu kiểm tra bad block.
type BadBlocksRequest struct {
	BSD string `json:"bsd"`
	// Write bật chế độ ghi-thử (PHÁ HUỶ dữ liệu): ghi một pattern rồi đọc lại so
	// sánh. Bắt buộc qua cổng xác nhận như Flash. Read-only (mặc định) chỉ đọc.
	Write       bool   `json:"write"`
	ExpectSize  int64  `json:"expectSize"`
	ExpectModel string `json:"expectModel"`
	Confirm     string `json:"confirm"`
}

// BadBlocksResult tổng hợp kết quả quét.
type BadBlocksResult struct {
	Mode         string     `json:"mode"` // read | write
	BytesScanned int64      `json:"bytesScanned"`
	Bad          []BadRange `json:"bad"`
	Destroyed    bool       `json:"destroyed"` // true nếu write-test đã xoá dữ liệu
}

// testPattern là mẫu byte dùng cho write-test. Xen kẽ bit để bắt lỗi dính bit
// (stuck-at) tốt hơn toàn 0 hay toàn 1.
const testPattern = 0xA5

// CheckBadBlocks quét toàn ổ tìm sector lỗi.
func CheckBadBlocks(ctx context.Context, req BadBlocksRequest, progress ProgressFunc) (*BadBlocksResult, error) {
	if req.Write {
		return checkBadBlocksWrite(ctx, req, progress)
	}
	return checkBadBlocksRead(ctx, req, progress)
}

// checkBadBlocksRead mở ổ chỉ-đọc và quét. Không phá dữ liệu nên không cần cổng
// xác nhận, nhưng vẫn yêu cầu whole disk gắn ngoài để không lỡ đọc ổ hệ thống.
func checkBadBlocksRead(ctx context.Context, req BadBlocksRequest, progress ProgressFunc) (*BadBlocksResult, error) {
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

	bad, scanned, err := scanRead(ctx, dev, d.SizeBytes, progress)
	if err != nil {
		return nil, err
	}
	return &BadBlocksResult{Mode: "read", BytesScanned: scanned, Bad: bad}, nil
}

// checkBadBlocksWrite ghi pattern rồi đọc lại so sánh — PHÁ HUỶ dữ liệu, nên đi
// qua cổng an toàn đầy đủ và tháo toàn ổ trước khi ghi.
func checkBadBlocksWrite(ctx context.Context, req BadBlocksRequest, progress ProgressFunc) (*BadBlocksResult, error) {
	d, err := lockTarget(ctx, req.BSD, req.ExpectSize, req.ExpectModel, req.Confirm)
	if err != nil {
		return nil, err
	}
	if err := blockdev.UnmountDisk(ctx, d.BSD); err != nil {
		return nil, err
	}
	dev, err := os.OpenFile(blockdev.RawDevicePath(d.BSD), os.O_RDWR, 0)
	if err != nil {
		return nil, fmt.Errorf("mở thiết bị để ghi-thử: %w", err)
	}
	defer dev.Close()

	bad, scanned, err := scanWrite(ctx, dev, d.SizeBytes, progress)
	if err != nil {
		return nil, err
	}
	return &BadBlocksResult{Mode: "write", BytesScanned: scanned, Bad: bad, Destroyed: true}, nil
}

// scanRead đọc [0,size) theo từng chunk. Chunk nào ReadAt lỗi thì ghi lại khoảng
// đó là bad và đi tiếp — mục tiêu là liệt kê vùng hỏng, không dừng ở lỗi đầu.
// Thuần theo io.ReaderAt để test được không cần ổ thật.
func scanRead(ctx context.Context, r readerAt, size int64, progress ProgressFunc) ([]BadRange, int64, error) {
	buf := make([]byte, bufSize)
	var bad []BadRange
	var off int64
	for off < size {
		if err := ctx.Err(); err != nil {
			return bad, off, err
		}
		n := chunkLen(off, size)
		if _, err := r.ReadAt(buf[:n], off); err != nil {
			bad = appendBad(bad, off, n)
		}
		off += n
		if progress != nil {
			progress(off, size)
		}
	}
	return bad, off, nil
}

// scanWrite ghi pattern rồi đọc lại từng chunk; lỗi ghi, lỗi đọc, hoặc nội dung
// đọc về khác pattern đều tính là bad.
func scanWrite(ctx context.Context, rw readWriterAt, size int64, progress ProgressFunc) ([]BadRange, int64, error) {
	want := bytes.Repeat([]byte{testPattern}, bufSize)
	got := make([]byte, bufSize)
	var bad []BadRange
	var off int64
	for off < size {
		if err := ctx.Err(); err != nil {
			return bad, off, err
		}
		n := chunkLen(off, size)
		if _, err := rw.WriteAt(want[:n], off); err != nil {
			bad = appendBad(bad, off, n)
			off += n
			continue
		}
		if _, err := rw.ReadAt(got[:n], off); err != nil || !bytes.Equal(got[:n], want[:n]) {
			bad = appendBad(bad, off, n)
		}
		off += n
		if progress != nil {
			progress(off, size)
		}
	}
	return bad, off, nil
}

// chunkLen trả kích thước chunk tại off: bufSize, hoặc phần dư nếu gần cuối ổ.
// Kích thước ổ luôn là bội số sector nên không cần đệm.
func chunkLen(off, size int64) int64 {
	if rem := size - off; rem < bufSize {
		return rem
	}
	return bufSize
}

// appendBad gộp chunk lỗi vào khoảng bad liền kề trước đó nếu nối tiếp, tránh
// sinh hàng nghìn khoảng 4 MiB rời rạc khi cả một vùng lớn hỏng.
func appendBad(bad []BadRange, off, n int64) []BadRange {
	if len(bad) > 0 {
		last := &bad[len(bad)-1]
		if last.Offset+last.Length == off {
			last.Length += n
			return bad
		}
	}
	return append(bad, BadRange{Offset: off, Length: n})
}

// readerAt / readWriterAt cố ý hẹp hơn os.File để scanRead/scanWrite test được
// với thiết bị giả.
type readerAt interface {
	ReadAt(p []byte, off int64) (int, error)
}
type readWriterAt interface {
	readerAt
	WriteAt(p []byte, off int64) (int, error)
}
