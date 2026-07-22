package imaging

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"

	"github.com/soi/doctorx/core/internal/blockdev"
)

func TestResolveTarget(t *testing.T) {
	disks := []blockdev.Disk{
		{BSD: "disk4", Model: "SanDisk USB", SizeBytes: 1 << 30, Internal: false},
		{BSD: "disk0", Model: "APPLE SSD", SizeBytes: 1 << 40, Internal: true},
	}
	tests := []struct {
		name    string
		bsd     string
		wantErr bool
	}{
		{"external whole disk ok", "disk4", false},
		{"accepts /dev/ prefix", "/dev/disk4", false},
		{"partition rejected", "disk4s2", true},
		{"internal disk rejected", "disk0", true},
		{"unknown disk rejected", "disk9", true},
		{"apfs volume rejected", "disk4s2s1", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := resolveTarget(disks, tt.bsd)
			if (err != nil) != tt.wantErr {
				t.Fatalf("resolveTarget(%q) err = %v, wantErr = %v", tt.bsd, err, tt.wantErr)
			}
		})
	}
}

func TestCheckLock(t *testing.T) {
	d := blockdev.Disk{BSD: "disk4", Model: "SanDisk USB", SizeBytes: 1 << 30}
	confirm := canonicalConfirm(d) // "SanDisk USB"

	if err := checkLock(d, 1<<30, "SanDisk USB", confirm); err != nil {
		t.Fatalf("khớp hoàn toàn phải qua, got %v", err)
	}
	if err := checkLock(d, 1<<20, "SanDisk USB", confirm); err == nil {
		t.Fatal("size lệch phải bị từ chối")
	}
	if err := checkLock(d, 1<<30, "Other USB", confirm); err == nil {
		t.Fatal("model lệch phải bị từ chối")
	}
	if err := checkLock(d, 1<<30, "SanDisk USB", "sai tên"); err == nil {
		t.Fatal("confirm lệch phải bị từ chối")
	}
	// expectSize/expectModel = 0/"" nghĩa là bỏ qua phần đó, confirm vẫn bắt buộc.
	if err := checkLock(d, 0, "", confirm); err != nil {
		t.Fatalf("bỏ qua size/model nhưng confirm đúng phải qua, got %v", err)
	}
}

func TestCanonicalConfirmFallback(t *testing.T) {
	if got := canonicalConfirm(blockdev.Disk{BSD: "disk4", Model: "  "}); got != "disk4" {
		t.Fatalf("model rỗng phải rơi về BSD, got %q", got)
	}
}

// TestCopyImageRoundTrip ghi một image cỡ lẻ (không phải bội số sector) ra một
// file đóng vai thiết bị, rồi verify hash khớp — chứng minh cả padding lẫn
// đường verify. Không cần ổ thật.
func TestCopyImageRoundTrip(t *testing.T) {
	dir := t.TempDir()
	// Cỡ lẻ, cố tình lớn hơn một buffer để ép nhiều vòng lặp.
	imgSize := bufSize + 777
	srcData := make([]byte, imgSize)
	for i := range srcData {
		srcData[i] = byte(i * 7)
	}
	srcPath := filepath.Join(dir, "src.img")
	if err := os.WriteFile(srcPath, srcData, 0o644); err != nil {
		t.Fatal(err)
	}

	dev, err := os.Create(filepath.Join(dir, "dev.raw"))
	if err != nil {
		t.Fatal(err)
	}
	defer dev.Close()
	src, err := os.Open(srcPath)
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()

	var lastDone, lastTotal int64
	written, err := copyImage(context.Background(), dev, src, int64(imgSize), func(done, total int64) {
		lastDone, lastTotal = done, total
	})
	if err != nil {
		t.Fatalf("copyImage: %v", err)
	}
	if written != int64(imgSize) {
		t.Fatalf("written = %d, want %d", written, imgSize)
	}
	if lastDone != int64(imgSize) || lastTotal != int64(imgSize) {
		t.Fatalf("progress cuối = %d/%d, want %d/%d", lastDone, lastTotal, imgSize, imgSize)
	}

	// File đích phải được đệm tới biên sector.
	fi, _ := dev.Stat()
	if fi.Size()%sectorSize != 0 {
		t.Fatalf("kích thước đích %d không phải bội số sector", fi.Size())
	}

	tgtHash, err := hashDevice(context.Background(), dev, int64(imgSize))
	if err != nil {
		t.Fatalf("hashDevice: %v", err)
	}
	want := sha256.Sum256(srcData)
	if tgtHash != hex.EncodeToString(want[:]) {
		t.Fatalf("hash đích khác hash nguồn:\n got %s\nwant %s", tgtHash, hex.EncodeToString(want[:]))
	}
}

func TestCopyImageCancel(t *testing.T) {
	dir := t.TempDir()
	dev, _ := os.Create(filepath.Join(dir, "dev.raw"))
	defer dev.Close()
	src := make([]byte, bufSize*4)
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // huỷ trước khi chạy
	_, err := copyImage(ctx, dev, readerFrom(src), int64(len(src)), nil)
	if err == nil {
		t.Fatal("ctx đã huỷ phải trả lỗi")
	}
}

type sliceReader struct {
	data []byte
	pos  int
}

func readerFrom(b []byte) *sliceReader { return &sliceReader{data: b} }

func (r *sliceReader) Read(p []byte) (int, error) {
	if r.pos >= len(r.data) {
		return 0, os.ErrClosed
	}
	n := copy(p, r.data[r.pos:])
	r.pos += n
	return n, nil
}
