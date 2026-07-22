package imaging

import (
	"context"
	"errors"
	"testing"
)

// failReaderAt trả lỗi khi ReadAt chạm bất kỳ offset nào trong failFrom..failTo.
type failReaderAt struct {
	size             int64
	failFrom, failTo int64
}

func (f *failReaderAt) ReadAt(p []byte, off int64) (int, error) {
	if off < f.failTo && off+int64(len(p)) > f.failFrom {
		return 0, errors.New("I/O error")
	}
	return len(p), nil
}

func TestScanReadMergesAdjacentBad(t *testing.T) {
	size := int64(bufSize) * 5
	// Cho hai chunk giữa (chunk 1 và 2) lỗi → phải gộp thành MỘT khoảng 2*bufSize.
	r := &failReaderAt{size: size, failFrom: bufSize, failTo: bufSize * 3}
	bad, scanned, err := scanRead(context.Background(), r, size, nil)
	if err != nil {
		t.Fatal(err)
	}
	if scanned != size {
		t.Fatalf("scanned = %d, want %d", scanned, size)
	}
	if len(bad) != 1 {
		t.Fatalf("kỳ vọng 1 khoảng bad đã gộp, got %d: %+v", len(bad), bad)
	}
	if bad[0].Offset != bufSize || bad[0].Length != bufSize*2 {
		t.Fatalf("khoảng bad = %+v, want offset %d len %d", bad[0], bufSize, bufSize*2)
	}
}

func TestScanReadClean(t *testing.T) {
	size := int64(bufSize)*2 + 123 // cỡ lẻ, chunk cuối ngắn
	r := &failReaderAt{size: size, failFrom: -1, failTo: -1}
	bad, scanned, err := scanRead(context.Background(), r, size, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(bad) != 0 {
		t.Fatalf("ổ lành phải 0 bad, got %+v", bad)
	}
	if scanned != size {
		t.Fatalf("scanned = %d, want %d", scanned, size)
	}
}

func TestScanReadCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r := &failReaderAt{size: bufSize * 4}
	if _, _, err := scanRead(ctx, r, bufSize*4, nil); err == nil {
		t.Fatal("ctx huỷ phải trả lỗi")
	}
}

// memDevice là thiết bị đọc-ghi trong RAM cho scanWrite; báo mismatch tại một
// vùng để mô phỏng sector ghi được nhưng đọc về sai.
type memDevice struct {
	data             []byte
	corruptFrom, len int64 // vùng đọc về bị hỏng
}

func (m *memDevice) WriteAt(p []byte, off int64) (int, error) {
	copy(m.data[off:], p)
	return len(p), nil
}

func (m *memDevice) ReadAt(p []byte, off int64) (int, error) {
	copy(p, m.data[off:off+int64(len(p))])
	// Bôi hỏng dữ liệu đọc về trong vùng corrupt để scanWrite thấy khác pattern.
	for i := range p {
		if off+int64(i) >= m.corruptFrom && off+int64(i) < m.corruptFrom+m.len {
			p[i] ^= 0xFF
		}
	}
	return len(p), nil
}

func TestScanWriteDetectsMismatch(t *testing.T) {
	size := int64(bufSize) * 3
	dev := &memDevice{data: make([]byte, size), corruptFrom: bufSize, len: bufSize}
	bad, scanned, err := scanWrite(context.Background(), dev, size, nil)
	if err != nil {
		t.Fatal(err)
	}
	if scanned != size {
		t.Fatalf("scanned = %d, want %d", scanned, size)
	}
	if len(bad) != 1 || bad[0].Offset != bufSize || bad[0].Length != bufSize {
		t.Fatalf("kỳ vọng 1 khoảng bad tại chunk giữa, got %+v", bad)
	}
}
