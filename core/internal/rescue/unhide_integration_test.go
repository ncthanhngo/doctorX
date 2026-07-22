package rescue_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/soi/doctorx/core/internal/blockdev"
	dfs "github.com/soi/doctorx/core/internal/fs"
	"github.com/soi/doctorx/core/internal/fsprobe"
	"github.com/soi/doctorx/core/internal/rescue"
)

// Kiểm thử toàn bộ đường ghi trên disk image thật: nhận diện filesystem → duyệt
// cây → sinh patch → journal → ghi → kiểm tra lại → hoàn tác.
//
// Chạy trên file ảnh chứ không phải /dev nên không cần quyền quản trị.

func fixturePath(t *testing.T, name string) string {
	t.Helper()
	p := filepath.Join("..", "fs", "testdata", name)
	if _, err := os.Stat(p); err != nil {
		t.Skipf("thiếu fixture %s — sinh bằng: cd internal/fs/testdata && ./make-images.sh", p)
	}
	return p
}

// copyFixture chép ảnh sang thư mục tạm để test ghi thoải mái.
func copyFixture(t *testing.T, name string) string {
	t.Helper()
	src, err := os.Open(fixturePath(t, name))
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()

	dst := filepath.Join(t.TempDir(), name)
	out, err := os.Create(dst)
	if err != nil {
		t.Fatal(err)
	}
	defer out.Close()
	if _, err := io.Copy(out, src); err != nil {
		t.Fatal(err)
	}
	return dst
}

func sumFile(t *testing.T, path string) string {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// attrsOf đọc lại attribute của một đường dẫn qua driver, dùng làm kiểm chứng
// độc lập với giá trị mà Unhide tự báo cáo.
func attrsOf(t *testing.T, img, path string) dfs.Attr {
	t.Helper()
	s, err := fsprobe.OpenPath(img, fsprobe.OpenOpts{})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	e, err := s.Volume.Stat(context.Background(), path)
	if err != nil {
		t.Fatalf("Stat(%q): %v", path, err)
	}
	return e.Attrs
}

func runUnhide(t *testing.T, img string, target string, recursive bool) *rescue.UnhideResult {
	t.Helper()
	s, err := fsprobe.OpenPath(img, fsprobe.OpenOpts{Write: true})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	res, err := rescue.Unhide(context.Background(), s.Volume, s.File, s.Reader, rescue.UnhideRequest{
		Path:        target,
		Recursive:   recursive,
		JournalDir:  filepath.Join(t.TempDir(), "journal"),
		JournalID:   "test-run",
		DeviceBSD:   "test",
		VolumeLabel: "DOCTORX",
	}, nil)
	if err != nil {
		t.Fatalf("Unhide: %v", err)
	}
	return res
}

func TestUnhideRecursiveClearsWholeSubtree(t *testing.T) {
	for _, fx := range []string{"fat32.img", "exfat.img", "ntfs.img"} {
		t.Run(fx, func(t *testing.T) {
			img := copyFixture(t, fx)

			if got := attrsOf(t, img, "/Anh gia dinh"); !got.IsConcealed() {
				t.Fatalf("điều kiện ban đầu sai: %q phải đang bị giấu, attr 0x%02x", "/Anh gia dinh", got)
			}

			res := runUnhide(t, img, "/Anh gia dinh", true)
			if res.EntriesChanged == 0 {
				t.Fatal("không có mục nào được gỡ cờ ẩn")
			}

			// Mục gốc và mọi mục trong cây con phải hết bị giấu.
			for _, p := range []string{"/Anh gia dinh", "/Anh gia dinh/2019", "/Anh gia dinh/ho-so.docx"} {
				if got := attrsOf(t, img, p); got.IsConcealed() {
					t.Errorf("%q vẫn còn bị giấu sau unhide: attr 0x%02x", p, got)
				}
			}
			// Cây khác không được đụng tới.
			if got := attrsOf(t, img, "/sau"); !got.IsConcealed() {
				t.Errorf("/sau nằm ngoài phạm vi nhưng đã bị sửa: attr 0x%02x", got)
			}
		})
	}
}

func TestUnhideNonRecursiveLeavesChildrenAlone(t *testing.T) {
	img := copyFixture(t, "fat32.img")
	runUnhide(t, img, "/sau", false)

	if got := attrsOf(t, img, "/sau"); got.IsConcealed() {
		t.Errorf("/sau vẫn bị giấu: attr 0x%02x", got)
	}
}

func TestRollbackRestoresImageBitForBit(t *testing.T) {
	for _, fx := range []string{"fat32.img", "exfat.img", "ntfs.img"} {
		t.Run(fx, func(t *testing.T) {
			img := copyFixture(t, fx)
			before := sumFile(t, img)

			jnlDir := filepath.Join(t.TempDir(), "journal")
			s, err := fsprobe.OpenPath(img, fsprobe.OpenOpts{Write: true})
			if err != nil {
				t.Fatal(err)
			}
			res, err := rescue.Unhide(context.Background(), s.Volume, s.File, s.Reader, rescue.UnhideRequest{
				Path: "/Anh gia dinh", Recursive: true,
				JournalDir: jnlDir, JournalID: "rb", VolumeLabel: "DOCTORX",
			}, nil)
			s.Close()
			if err != nil {
				t.Fatal(err)
			}
			if res.JournalID == "" {
				t.Fatal("không sinh journal dù đã có thay đổi")
			}
			if after := sumFile(t, img); after == before {
				t.Fatal("ảnh không đổi sau unhide")
			}

			dev, err := os.OpenFile(img, os.O_RDWR, 0)
			if err != nil {
				t.Fatal(err)
			}
			n, err := blockdev.Rollback(dev, filepath.Join(jnlDir, "rb.jnl"))
			dev.Close()
			if err != nil {
				t.Fatalf("Rollback: %v", err)
			}
			if n == 0 {
				t.Fatal("Rollback không khôi phục block nào")
			}
			if got := sumFile(t, img); got != before {
				t.Errorf("sau rollback ảnh KHÔNG khớp bit-for-bit\n trước: %s\n sau  : %s", before, got)
			}
		})
	}
}

// Thư mục hệ thống phải bị từ chối, và không được ghi một byte nào.
func TestUnhideRefusesProtectedPath(t *testing.T) {
	img := copyFixture(t, "fat32.img")
	before := sumFile(t, img)

	s, err := fsprobe.OpenPath(img, fsprobe.OpenOpts{Write: true})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	_, err = rescue.Unhide(context.Background(), s.Volume, s.File, s.Reader, rescue.UnhideRequest{
		Path:       "/System Volume Information",
		Recursive:  true,
		JournalDir: filepath.Join(t.TempDir(), "journal"),
		JournalID:  "protected",
	}, nil)
	if err == nil {
		t.Fatal("phải từ chối thư mục hệ thống")
	}
	if got := sumFile(t, img); got != before {
		t.Error("thao tác bị từ chối nhưng ảnh vẫn bị thay đổi")
	}
}

// File phụ trợ của macOS không phải dữ liệu người dùng nên phải giữ nguyên cờ ẩn.
func TestUnhideSkipsAppleDoubleFiles(t *testing.T) {
	img := copyFixture(t, "fat32.img")
	runUnhide(t, img, "/Anh gia dinh", true)

	if got := attrsOf(t, img, "/Anh gia dinh/._ho-so.docx"); !got.IsConcealed() {
		t.Errorf("file AppleDouble bị gỡ cờ ẩn, sẽ thành rác lộ thiên trên Windows: attr 0x%02x", got)
	}
}
