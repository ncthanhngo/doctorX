package imaging

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPartitionArgs(t *testing.T) {
	got := strings.Join(partitionArgs("/dev/disk5", "DATA"), " ")
	want := "partitionDisk disk5 GPT MS-DOS FAT32 DATA 100%"
	if got != want {
		t.Fatalf("partitionArgs = %q, want %q", got, want)
	}
}

func TestMkntfsArgs(t *testing.T) {
	got := strings.Join(mkntfsArgs("disk5s2", "DATA"), " ")
	want := "--quick --force --label DATA /dev/rdisk5s2"
	if got != want {
		t.Fatalf("mkntfsArgs = %q, want %q", got, want)
	}
}

func TestResolveMkntfsFromEnv(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "mkntfs")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv(mkntfsEnv, bin)
	got, err := resolveMkntfs()
	if err != nil || got != bin {
		t.Fatalf("resolveMkntfs = %q, %v; want %q", got, err, bin)
	}
}

func TestResolveMkntfsEnvNotExecutable(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "notexec")
	if err := os.WriteFile(bin, []byte("x"), 0o644); err != nil { // không có bit thực thi
		t.Fatal(err)
	}
	t.Setenv(mkntfsEnv, bin)
	if _, err := resolveMkntfs(); err == nil {
		t.Fatal("file không thực thi được phải trả lỗi")
	}
}

func TestNormalizeLabelNTFS(t *testing.T) {
	// NTFS không áp ràng buộc FAT32/exFAT; nhãn rỗng vẫn về UNTITLED.
	got, err := normalizeLabel("ntfs", "")
	if err != nil || got != "UNTITLED" {
		t.Fatalf("got %q err %v", got, err)
	}
}
