package fat_test

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/soi/doctorx/core/internal/blockdev"
	"github.com/soi/doctorx/core/internal/fs"
	"github.com/soi/doctorx/core/internal/fs/fat"
)

const fixturePath = "../testdata/fat32.img"

// openFixture mở fixture FAT32 dùng chung cho mọi test. Nếu chưa sinh (chưa
// chạy make-images.sh) thì skip thay vì fail, đúng yêu cầu.
func openFixture(t *testing.T) *fat.Volume {
	t.Helper()
	f, err := os.Open(fixturePath)
	if err != nil {
		if os.IsNotExist(err) {
			t.Skipf("thiếu fixture %s — chạy: cd ../testdata && ./make-images.sh fat32", fixturePath)
		}
		t.Fatalf("mở fixture: %v", err)
	}
	t.Cleanup(func() { f.Close() })

	info, err := f.Stat()
	if err != nil {
		t.Fatalf("stat fixture: %v", err)
	}
	rd, err := blockdev.NewReader(f, 512, info.Size(), 0)
	if err != nil {
		t.Fatalf("blockdev.NewReader: %v", err)
	}
	v, err := fat.Open(rd)
	if err != nil {
		t.Fatalf("fat.Open: %v", err)
	}
	return v
}

func walkAll(t *testing.T, v *fat.Volume, opt fs.WalkOpt) map[string]*fs.Entry {
	t.Helper()
	got := make(map[string]*fs.Entry)
	err := v.Walk(context.Background(), opt, func(e *fs.Entry) error {
		got[e.Path] = e
		return nil
	})
	if err != nil {
		t.Fatalf("Walk: %v", err)
	}
	return got
}

func TestWalkListsAllEntries(t *testing.T) {
	v := openFixture(t)
	got := walkAll(t, v, fs.WalkOpt{})

	want := []string{
		"/visible.txt",
		"/empty.bin",
		"/Anh gia dinh",
		"/Anh gia dinh/2019",
		"/Anh gia dinh/2019/dam-cuoi.jpg",
		"/Anh gia dinh/ho-so.docx",
		"/ten file rat dai de kiem tra long file name tren FAT32 co dung khong.txt",
		"/sau",
		"/sau/hon",
		"/sau/hon/nua",
		"/sau/hon/nua/tang",
		"/sau/hon/nua/tang/cuoi",
		"/sau/hon/nua/tang/cuoi/day.txt",
		"/System Volume Information",
		"/System Volume Information/tracking.log",
	}
	for _, p := range want {
		if _, ok := got[p]; !ok {
			t.Errorf("thiếu entry %q trong kết quả Walk (có %d entry)", p, len(got))
		}
	}

	// Không được lẫn "." / ".." vào cây.
	for p := range got {
		if p == "/." || p == "/.." {
			t.Errorf("lọt entry giả %q", p)
		}
	}
}

func TestWalkAttrHiddenSystem(t *testing.T) {
	v := openFixture(t)
	got := walkAll(t, v, fs.WalkOpt{})

	hidden := []string{"/visible.txt", "/Anh gia dinh", "/sau"}
	for _, p := range hidden {
		e, ok := got[p]
		if !ok {
			t.Fatalf("thiếu entry %q", p)
		}
		if !e.Attrs.Has(fs.AttrHidden) || !e.Attrs.Has(fs.AttrSystem) {
			t.Errorf("%q: kỳ vọng Hidden+System, được attr 0x%02x", p, e.Attrs)
		}
	}

	notHidden := []string{"/empty.bin", "/Anh gia dinh/ho-so.docx"}
	for _, p := range notHidden {
		e, ok := got[p]
		if !ok {
			t.Fatalf("thiếu entry %q", p)
		}
		if e.Attrs.IsConcealed() {
			t.Errorf("%q: không kỳ vọng bị giấu, được attr 0x%02x", p, e.Attrs)
		}
	}
}

func TestWalkMaxDepthDirsOnly(t *testing.T) {
	v := openFixture(t)
	got := walkAll(t, v, fs.WalkOpt{MaxDepth: 1, DirsOnly: true})
	// macOS tự tạo các thư mục này khi mount ảnh lúc sinh fixture, thời điểm
	// không cố định giữa các lần chạy nên phải bỏ qua để test khỏi lúc pass
	// lúc fail. Chúng cũng là thứ sản phẩm lọc ra khỏi kết quả cho người dùng.
	for _, gen := range []string{"/.fseventsd", "/.Spotlight-V100", "/.TemporaryItems"} {
		delete(got, gen)
	}

	want := map[string]bool{
		"/Anh gia dinh":              true,
		"/sau":                       true,
		"/System Volume Information": true,
	}
	if len(got) != len(want) {
		t.Errorf("kỳ vọng đúng %d thư mục cấp gốc, được %d: %v", len(want), len(got), keys(got))
	}
	for p := range got {
		if !want[p] {
			t.Errorf("entry %q không thuộc cấp gốc hoặc không phải thư mục", p)
		}
		if !got[p].Attrs.IsDir() {
			t.Errorf("%q: DirsOnly nhưng trả về entry không phải thư mục", p)
		}
	}
	for p := range want {
		if _, ok := got[p]; !ok {
			t.Errorf("thiếu thư mục cấp gốc %q", p)
		}
	}
}

func keys(m map[string]*fs.Entry) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func TestStat(t *testing.T) {
	v := openFixture(t)
	ctx := context.Background()

	e, err := v.Stat(ctx, "/Anh gia dinh/2019/dam-cuoi.jpg")
	if err != nil {
		t.Fatalf("Stat file lồng sâu: %v", err)
	}
	if e.Path != "/Anh gia dinh/2019/dam-cuoi.jpg" {
		t.Errorf("path sai: %q", e.Path)
	}
	if e.Attrs.IsDir() {
		t.Errorf("dam-cuoi.jpg không phải thư mục")
	}
	if len(e.Locs) != 1 || e.Locs[0].Width != 1 || e.Locs[0].Region != "fat-dir" {
		t.Errorf("Locs sai: %+v", e.Locs)
	}

	root, err := v.Stat(ctx, "/")
	if err != nil {
		t.Fatalf("Stat root: %v", err)
	}
	if !root.Attrs.IsDir() {
		t.Errorf("root phải là thư mục")
	}
	if len(root.Locs) != 0 {
		t.Errorf("root không được có Locs, được %+v", root.Locs)
	}

	if _, err := v.Stat(ctx, "/khong-ton-tai.txt"); !errors.Is(err, fs.ErrNotFound) {
		t.Errorf("kỳ vọng fs.ErrNotFound, được %v", err)
	}
}

func TestClearAttrs(t *testing.T) {
	v := openFixture(t)
	ctx := context.Background()

	e, err := v.Stat(ctx, "/visible.txt")
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if len(e.Locs) != 1 {
		t.Fatalf("kỳ vọng đúng 1 Loc, được %d", len(e.Locs))
	}

	patches, err := v.ClearAttrs(e, fs.AttrHiddenSystem)
	if err != nil {
		t.Fatalf("ClearAttrs: %v", err)
	}
	if len(patches) != 1 {
		t.Fatalf("kỳ vọng đúng 1 patch, được %d", len(patches))
	}
	p := patches[0]
	if p.Offset != e.Locs[0].Offset {
		t.Errorf("offset patch %d khác offset entry %d", p.Offset, e.Locs[0].Offset)
	}
	if len(p.Old) != 1 || len(p.New) != 1 {
		t.Fatalf("Old/New phải đúng 1 byte, được Old=%v New=%v", p.Old, p.New)
	}
	if fs.Attr(p.Old[0])&fs.AttrHiddenSystem == 0 {
		t.Errorf("Old attr 0x%02x không có bit Hidden/System như kỳ vọng", p.Old[0])
	}
	if fs.Attr(p.New[0])&fs.AttrHiddenSystem != 0 {
		t.Errorf("New attr 0x%02x vẫn còn bit Hidden/System", p.New[0])
	}
	if p.New[0] != p.Old[0]&^byte(fs.AttrHiddenSystem) {
		t.Errorf("New attr không khớp Old &^ mask: Old=0x%02x New=0x%02x", p.Old[0], p.New[0])
	}

	// Entry không có bit nào trong mask -> không sinh patch.
	empty, err := v.Stat(ctx, "/empty.bin")
	if err != nil {
		t.Fatalf("Stat empty.bin: %v", err)
	}
	patches, err = v.ClearAttrs(empty, fs.AttrHiddenSystem)
	if err != nil {
		t.Fatalf("ClearAttrs(empty.bin): %v", err)
	}
	if len(patches) != 0 {
		t.Errorf("kỳ vọng không patch nào khi attr không đổi, được %d", len(patches))
	}
}

func TestMetadataRangesNarrow(t *testing.T) {
	v := openFixture(t)
	_ = walkAll(t, v, fs.WalkOpt{})

	rs := v.MetadataRanges()
	if rs == nil {
		t.Fatal("MetadataRanges trả nil")
	}
	// Vùng chứa attr byte của /visible.txt phải được khai báo (Walk đã đi qua
	// root directory chứa nó).
	e, err := v.Stat(context.Background(), "/visible.txt")
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if err := rs.Check(e.Locs[0].Offset, 1); err != nil {
		t.Errorf("MetadataRanges không phủ offset attribute của /visible.txt: %v", err)
	}
}

func TestOpenTruncatedImageFails(t *testing.T) {
	orig, err := os.Open(fixturePath)
	if err != nil {
		if os.IsNotExist(err) {
			t.Skipf("thiếu fixture %s — chạy: cd ../testdata && ./make-images.sh fat32", fixturePath)
		}
		t.Fatalf("mở fixture: %v", err)
	}
	defer orig.Close()

	const truncSize = 8192
	buf := make([]byte, truncSize)
	if _, err := orig.ReadAt(buf, 0); err != nil {
		t.Fatalf("đọc fixture: %v", err)
	}

	tmp, err := os.CreateTemp(t.TempDir(), "fat32-truncated-*.img")
	if err != nil {
		t.Fatalf("tạo file tạm: %v", err)
	}
	defer tmp.Close()
	if _, err := tmp.Write(buf); err != nil {
		t.Fatalf("ghi file tạm: %v", err)
	}

	rd, err := blockdev.NewReader(tmp, 512, truncSize, 0)
	if err != nil {
		t.Fatalf("blockdev.NewReader: %v", err)
	}

	v, err := fat.Open(rd)
	if err != nil {
		// Boot sector nằm trong 512 byte đầu nên có thể vẫn parse được — chấp
		// nhận cả hai khả năng, miễn KHÔNG panic (nếu panic thì test crash).
		t.Logf("fat.Open trả lỗi ngay (chấp nhận được, ảnh quá ngắn): %v", err)
		return
	}

	err = v.Walk(context.Background(), fs.WalkOpt{}, func(e *fs.Entry) error { return nil })
	if err == nil {
		t.Fatalf("kỳ vọng lỗi khi Walk trên ảnh bị cắt cụt, không có lỗi nào")
	}
	t.Logf("Walk trên ảnh cắt cụt trả lỗi như kỳ vọng: %v", err)
}
