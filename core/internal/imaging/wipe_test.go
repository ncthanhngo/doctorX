package imaging

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestPassesFor(t *testing.T) {
	if p, _ := passesFor("zero"); len(p) != 1 || p[0].fill != 0x00 || p[0].random {
		t.Fatalf("zero sai: %+v", p)
	}
	if p, _ := passesFor(""); len(p) != 1 || p[0].fill != 0x00 {
		t.Fatalf("mặc định phải là zero: %+v", p)
	}
	if p, _ := passesFor("3pass"); len(p) != 3 || !p[2].random {
		t.Fatalf("3pass sai: %+v", p)
	}
	if _, err := passesFor("dod7"); err == nil {
		t.Fatal("method lạ phải lỗi")
	}
}

func TestWipePassWriteAndVerify(t *testing.T) {
	dir := t.TempDir()
	dev, _ := os.Create(filepath.Join(dir, "dev.raw"))
	defer dev.Close()
	size := int64(bufSize) + 512 // >1 chunk, align sector

	// Ghi lượt zero.
	if err := wipePassWrite(context.Background(), dev, size, wipePass{fill: 0x00}, nil); err != nil {
		t.Fatal(err)
	}
	ok, err := verifyFill(context.Background(), dev, size, 0x00)
	if err != nil || !ok {
		t.Fatalf("verify zero phải đạt: ok=%v err=%v", ok, err)
	}
	// verify với fill khác phải trả false.
	if ok, _ := verifyFill(context.Background(), dev, size, 0xFF); ok {
		t.Fatal("verify 0xFF trên ổ toàn 0 phải false")
	}

	// Ghi lượt 0xFF rồi kiểm.
	if err := wipePassWrite(context.Background(), dev, size, wipePass{fill: 0xFF}, nil); err != nil {
		t.Fatal(err)
	}
	if ok, _ := verifyFill(context.Background(), dev, size, 0xFF); !ok {
		t.Fatal("verify 0xFF sau khi ghi 0xFF phải đạt")
	}
}

func TestWipePassRandomDiffers(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "dev.raw")
	dev, _ := os.Create(path)
	size := int64(4096)
	if err := wipePassWrite(context.Background(), dev, size, wipePass{random: true}, nil); err != nil {
		t.Fatal(err)
	}
	dev.Close()
	data, _ := os.ReadFile(path)
	if bytes.Equal(data, make([]byte, size)) {
		t.Fatal("lượt random không được để ổ toàn 0")
	}
}

func TestWipePassCancel(t *testing.T) {
	dir := t.TempDir()
	dev, _ := os.Create(filepath.Join(dir, "dev.raw"))
	defer dev.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := wipePassWrite(ctx, dev, bufSize*4, wipePass{fill: 0}, nil); err == nil {
		t.Fatal("ctx huỷ phải trả lỗi")
	}
}
