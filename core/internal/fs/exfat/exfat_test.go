package exfat

import (
	"context"
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/soi/doctorx/core/internal/blockdev"
	"github.com/soi/doctorx/core/internal/fs"
)

// openTestVolume mở fixture testdata/exfat.img. Fixture thiếu (chưa sinh)
// thì skip kèm hướng dẫn tái tạo, không fail cả suite.
func openTestVolume(t *testing.T) (*Volume, *os.File) {
	t.Helper()
	imgPath := filepath.Join("..", "testdata", "exfat.img")
	f, err := os.Open(imgPath)
	if err != nil {
		t.Skipf("thiếu fixture %s — sinh bằng: cd core/internal/fs/testdata && ./make-images.sh exfat (%v)", imgPath, err)
	}
	t.Cleanup(func() { f.Close() })

	info, err := f.Stat()
	if err != nil {
		t.Fatalf("Stat fixture: %v", err)
	}
	rd, err := blockdev.NewReader(f, 512, info.Size(), 0)
	if err != nil {
		t.Fatalf("blockdev.NewReader: %v", err)
	}
	vol, err := Open(rd)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	return vol, f
}

// TestWalkFullTree duyệt toàn bộ cây và kiểm tra path/attribute của các mục
// mà make-images.sh dựng, bao gồm tên có khoảng trắng, thư mục lồng sâu, và
// các mục bị đánh dấu Hidden+System. Chỉ kiểm tra bằng map (không đếm tổng số
// entry) vì macOS driver tự sinh thêm file "._*"/".fseventsd" khi mount ghi.
func TestWalkFullTree(t *testing.T) {
	vol, _ := openTestVolume(t)

	entries := map[string]*fs.Entry{}
	err := vol.Walk(context.Background(), fs.WalkOpt{}, func(e *fs.Entry) error {
		if _, dup := entries[e.Path]; dup {
			t.Errorf("Walk trả trùng path: %s", e.Path)
		}
		entries[e.Path] = e
		return nil
	})
	if err != nil {
		t.Fatalf("Walk: %v", err)
	}

	want := map[string]fs.Attr{
		"/visible.txt":                    fs.AttrHidden | fs.AttrSystem | fs.AttrArchive,
		"/empty.bin":                      fs.AttrArchive,
		"/Anh gia dinh":                   fs.AttrHidden | fs.AttrSystem | fs.AttrDir | fs.AttrArchive,
		"/Anh gia dinh/ho-so.docx":        fs.AttrArchive,
		"/Anh gia dinh/2019":              fs.AttrDir | fs.AttrArchive,
		"/Anh gia dinh/2019/dam-cuoi.jpg": fs.AttrArchive,
		"/ten file rat dai de kiem tra long file name tren FAT32 co dung khong.txt": fs.AttrArchive,
		"/sau":                                    fs.AttrHidden | fs.AttrSystem | fs.AttrDir | fs.AttrArchive,
		"/sau/hon":                                fs.AttrDir | fs.AttrArchive,
		"/sau/hon/nua":                            fs.AttrDir | fs.AttrArchive,
		"/sau/hon/nua/tang":                       fs.AttrDir | fs.AttrArchive,
		"/sau/hon/nua/tang/cuoi":                  fs.AttrDir | fs.AttrArchive,
		"/sau/hon/nua/tang/cuoi/day.txt":          fs.AttrArchive,
		"/System Volume Information":              fs.AttrDir | fs.AttrArchive,
		"/System Volume Information/tracking.log": fs.AttrArchive,
	}
	for p, wantAttr := range want {
		e, ok := entries[p]
		if !ok {
			t.Errorf("thiếu entry %s trong kết quả Walk", p)
			continue
		}
		if e.Attrs != wantAttr {
			t.Errorf("%s: attr = %#04x, muốn %#04x", p, e.Attrs, wantAttr)
		}
	}

	if e := entries["/empty.bin"]; e != nil && e.Size != 0 {
		t.Errorf("/empty.bin size = %d, muốn 0", e.Size)
	}
	longName := "/ten file rat dai de kiem tra long file name tren FAT32 co dung khong.txt"
	if e := entries[longName]; e != nil && e.Size != 5 {
		t.Errorf("size tên dài = %d, muốn 5", e.Size)
	}

	// MetadataRanges phải hẹp: chỉ gồm cluster thư mục đã đọc, không phải cả
	// cluster heap. Vị trí attribute của mọi entry đã Walk phải nằm trong
	// range đã khai báo.
	ranges := vol.MetadataRanges()
	for p, e := range entries {
		for _, loc := range e.Locs {
			if err := ranges.Check(loc.Offset, loc.Width); err != nil {
				t.Errorf("%s: EntryLoc %+v không nằm trong MetadataRanges: %v", p, loc, err)
			}
		}
	}
	// Boot sector (offset 0) không được nằm trong vùng ghi được phép.
	if err := ranges.Check(0, 2); err == nil {
		t.Error("MetadataRanges cho phép ghi cả boot sector — phải khai báo hẹp hơn")
	}
}

// TestWalkMaxDepthDirsOnly kiểm tra WalkOpt.MaxDepth=1 chỉ trả mục ở cấp 1,
// và DirsOnly bỏ qua file thường trong callback.
func TestWalkMaxDepthDirsOnly(t *testing.T) {
	vol, _ := openTestVolume(t)

	var got []string
	err := vol.Walk(context.Background(), fs.WalkOpt{MaxDepth: 1, DirsOnly: true}, func(e *fs.Entry) error {
		got = append(got, e.Path)
		if !e.Attrs.IsDir() {
			t.Errorf("DirsOnly nhưng nhận được file: %s", e.Path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("Walk: %v", err)
	}

	for _, p := range got {
		if strings.Count(strings.Trim(p, "/"), "/") != 0 {
			t.Errorf("MaxDepth=1 nhưng nhận entry sâu hơn cấp 1: %s", p)
		}
	}

	wantDirs := []string{"/Anh gia dinh", "/sau", "/System Volume Information"}
	for _, want := range wantDirs {
		found := false
		for _, p := range got {
			if p == want {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("thiếu thư mục %s trong kết quả MaxDepth=1+DirsOnly", want)
		}
	}
}

// TestStat kiểm tra Stat theo path tuyệt đối, gồm cả path lồng sâu và path
// không tồn tại.
func TestStat(t *testing.T) {
	vol, _ := openTestVolume(t)

	cases := []struct {
		path     string
		wantAttr fs.Attr
	}{
		{"/visible.txt", fs.AttrHidden | fs.AttrSystem | fs.AttrArchive},
		{"/Anh gia dinh/2019/dam-cuoi.jpg", fs.AttrArchive},
		{"/sau/hon/nua/tang/cuoi/day.txt", fs.AttrArchive},
		{"/Anh gia dinh", fs.AttrHidden | fs.AttrSystem | fs.AttrDir | fs.AttrArchive},
	}
	for _, c := range cases {
		e, err := vol.Stat(context.Background(), c.path)
		if err != nil {
			t.Errorf("Stat(%s): %v", c.path, err)
			continue
		}
		if e.Path != c.path {
			t.Errorf("Stat(%s).Path = %s", c.path, e.Path)
		}
		if e.Attrs != c.wantAttr {
			t.Errorf("Stat(%s) attr = %#04x, muốn %#04x", c.path, e.Attrs, c.wantAttr)
		}
	}

	if _, err := vol.Stat(context.Background(), "/khong-ton-tai.txt"); !errors.Is(err, fs.ErrNotFound) {
		t.Errorf("Stat file không tồn tại: err = %v, muốn fs.ErrNotFound", err)
	}
	if _, err := vol.Stat(context.Background(), "/visible.txt/con-gi-do"); !errors.Is(err, fs.ErrNotFound) {
		t.Errorf("Stat qua path có file làm thư mục cha: err = %v, muốn fs.ErrNotFound", err)
	}
}

// TestSetChecksumMatchesFixture là test quan trọng nhất: đọc thẳng entry set
// của /visible.txt từ fixture (không qua đường ghi của package này) rồi tính
// SetChecksum, phải khớp giá trị đang lưu ở byte 2-3. Sai công thức này thì
// macOS/Windows coi toàn bộ entry hỏng và file biến mất khỏi thư mục.
func TestSetChecksumMatchesFixture(t *testing.T) {
	vol, f := openTestVolume(t)

	e, err := vol.Stat(context.Background(), "/visible.txt")
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	priv, ok := e.FSPrivate.(entryPrivate)
	if !ok {
		t.Fatalf("FSPrivate kiểu sai: %T", e.FSPrivate)
	}

	total := (priv.secondaryCount + 1) * dirEntrySize
	buf := make([]byte, total)
	if _, err := f.ReadAt(buf, priv.start); err != nil {
		t.Fatalf("đọc entry set trực tiếp từ file: %v", err)
	}

	stored := binary.LittleEndian.Uint16(buf[2:4])
	got := SetChecksum(buf)
	if got != stored {
		t.Errorf("SetChecksum(entry set /visible.txt) = %#04x, checksum lưu trong entry = %#04x", got, stored)
	}
}

// TestClearAttrsGeneratesConsistentPatches kiểm tra ClearAttrs sinh đúng hai
// patch (attribute + checksum), Old khớp giá trị thật trên device, và
// checksum mới khớp với checksum tính độc lập trên entry set đã áp attribute
// mới.
func TestClearAttrsGeneratesConsistentPatches(t *testing.T) {
	vol, _ := openTestVolume(t)

	e, err := vol.Stat(context.Background(), "/visible.txt")
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if !e.Attrs.Has(fs.AttrHidden) || !e.Attrs.Has(fs.AttrSystem) {
		t.Fatalf("/visible.txt chưa Hidden+System trong fixture, không test được ClearAttrs: attr=%#04x", e.Attrs)
	}

	patches, err := vol.ClearAttrs(e, fs.AttrHiddenSystem)
	if err != nil {
		t.Fatalf("ClearAttrs: %v", err)
	}
	if len(patches) != 2 {
		t.Fatalf("số patch = %d, muốn 2", len(patches))
	}

	var attrPatch, checksumPatch *fs.Patch
	for i := range patches {
		switch patches[i].Region {
		case "exfat-dir-attr":
			attrPatch = &patches[i]
		case "exfat-dir-checksum":
			checksumPatch = &patches[i]
		default:
			t.Errorf("region lạ: %s", patches[i].Region)
		}
	}
	if attrPatch == nil || checksumPatch == nil {
		t.Fatalf("thiếu patch attr hoặc checksum: %+v", patches)
	}

	priv := e.FSPrivate.(entryPrivate)
	if attrPatch.Offset != priv.start+4 {
		t.Errorf("attr patch offset = %d, muốn %d", attrPatch.Offset, priv.start+4)
	}
	if checksumPatch.Offset != priv.start+2 {
		t.Errorf("checksum patch offset = %d, muốn %d", checksumPatch.Offset, priv.start+2)
	}

	if got := binary.LittleEndian.Uint16(attrPatch.Old); got != uint16(e.Attrs) {
		t.Errorf("attr patch Old = %#04x, muốn %#04x (giá trị thật trên device)", got, uint16(e.Attrs))
	}

	newAttr := binary.LittleEndian.Uint16(attrPatch.New)
	if fs.Attr(newAttr).Has(fs.AttrHidden) || fs.Attr(newAttr).Has(fs.AttrSystem) {
		t.Errorf("attr mới vẫn còn Hidden/System: %#04x", newAttr)
	}

	// Dựng lại đúng buffer entry set đã áp attribute mới, tính checksum độc
	// lập bằng SetChecksum, phải khớp với New của checksum patch.
	total := (priv.secondaryCount + 1) * dirEntrySize
	buf := make([]byte, total)
	if _, err := vol.rd.ReadAt(buf, priv.start); err != nil {
		t.Fatalf("đọc lại entry set: %v", err)
	}
	binary.LittleEndian.PutUint16(buf[4:6], newAttr)
	wantChecksum := SetChecksum(buf)
	gotChecksum := binary.LittleEndian.Uint16(checksumPatch.New)
	if gotChecksum != wantChecksum {
		t.Errorf("checksum patch New = %#04x, tính độc lập trên buffer đã patch = %#04x", gotChecksum, wantChecksum)
	}
}

// TestOpenTruncatedImageFails cắt cụt fixture ngay trước bảng FAT (byte
// 65536, xác định qua boot sector thật của fixture) và kiểm tra Open trả lỗi
// fs.ErrCorrupt thay vì panic hoặc đọc tràn.
func TestOpenTruncatedImageFails(t *testing.T) {
	src := filepath.Join("..", "testdata", "exfat.img")
	data, err := os.ReadFile(src)
	if err != nil {
		t.Skipf("thiếu fixture %s — sinh bằng: cd core/internal/fs/testdata && ./make-images.sh exfat (%v)", src, err)
	}
	const truncateAt = 64 * 1024 // còn boot sector, mất bảng FAT và cluster heap
	if len(data) <= truncateAt {
		t.Fatalf("fixture nhỏ hơn dự kiến (%d byte), không dùng được cho test này", len(data))
	}
	truncated := data[:truncateAt]

	tmpPath := filepath.Join(t.TempDir(), "truncated.img")
	if err := os.WriteFile(tmpPath, truncated, 0o600); err != nil {
		t.Fatalf("ghi file tạm: %v", err)
	}
	f, err := os.Open(tmpPath)
	if err != nil {
		t.Fatalf("mở file tạm: %v", err)
	}
	defer f.Close()

	rd, err := blockdev.NewReader(f, 512, int64(len(truncated)), 0)
	if err != nil {
		t.Fatalf("blockdev.NewReader: %v", err)
	}

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("Open panic trên image bị cắt cụt: %v", r)
		}
	}()
	_, err = Open(rd)
	if err == nil {
		t.Fatal("Open image bị cắt cụt phải trả lỗi, không phải nil")
	}
	if !errors.Is(err, fs.ErrCorrupt) {
		t.Errorf("lỗi = %v, muốn bọc fs.ErrCorrupt", err)
	}
}
