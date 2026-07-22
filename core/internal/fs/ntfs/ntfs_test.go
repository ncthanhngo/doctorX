package ntfs

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

// openTestVolume mở fixture testdata/ntfs.img. Fixture thiếu (chưa sinh)
// thì skip kèm hướng dẫn tái tạo, không fail cả suite.
func openTestVolume(t *testing.T) (*Volume, *os.File) {
	t.Helper()
	imgPath := filepath.Join("..", "testdata", "ntfs.img")
	f, err := os.Open(imgPath)
	if err != nil {
		t.Skipf("thiếu fixture %s — sinh bằng: cd core/internal/fs/testdata && ./make-ntfs.sh (%v)", imgPath, err)
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
// mà make-ntfs.sh dựng, bao gồm tên có khoảng trắng, tên dài Unicode, thư mục
// lồng sâu, và các mục bị đánh dấu Hidden+System qua xattr system.ntfs_attrib_be.
// Các giá trị "want" đã được xác thực trực tiếp trên fixture (không suy đoán).
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
		"/visible.txt":                    fs.AttrHidden | fs.AttrSystem,
		"/empty.bin":                      fs.AttrArchive,
		"/Anh gia dinh":                   fs.AttrHidden | fs.AttrSystem | fs.AttrDir,
		"/Anh gia dinh/ho-so.docx":        fs.AttrArchive,
		"/Anh gia dinh/2019":              fs.AttrDir | fs.AttrArchive,
		"/Anh gia dinh/2019/dam-cuoi.jpg": fs.AttrArchive,
		"/ten file rat dai de kiem tra long file name tren NTFS co dung khong.txt": fs.AttrArchive,
		"/sau":                                    fs.AttrHidden | fs.AttrSystem | fs.AttrDir,
		"/sau/hon":                                fs.AttrDir | fs.AttrArchive,
		"/sau/hon/nua":                            fs.AttrDir | fs.AttrArchive,
		"/sau/hon/nua/tang":                       fs.AttrDir | fs.AttrArchive,
		"/sau/hon/nua/tang/cuoi":                  fs.AttrDir | fs.AttrArchive,
		"/sau/hon/nua/tang/cuoi/day.txt":          fs.AttrArchive,
		"/System Volume Information":              fs.AttrDir | fs.AttrArchive,
		"/System Volume Information/tracking.log": fs.AttrArchive,
		"/big.bin":                                fs.AttrArchive,
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
	longName := "/ten file rat dai de kiem tra long file name tren NTFS co dung khong.txt"
	if e := entries[longName]; e == nil {
		t.Errorf("thiếu entry tên dài Unicode: %s", longName)
	}

	// MetadataRanges phải hẹp: chỉ gồm MFT record/INDX block thực sự đã đọc.
	// Vị trí attribute của mọi entry đã Walk phải nằm trong range đã khai báo.
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

// TestStat kiểm tra Stat theo path tuyệt đối, gồm cả path lồng sâu (kiểm
// chứng USA fixup hoạt động đúng qua nhiều cấp MFT record/INDX block, mỗi
// cái đều dài hơn 1 sector) và path không tồn tại.
func TestStat(t *testing.T) {
	vol, _ := openTestVolume(t)

	cases := []struct {
		path     string
		wantAttr fs.Attr
	}{
		{"/visible.txt", fs.AttrHidden | fs.AttrSystem},
		{"/Anh gia dinh/2019/dam-cuoi.jpg", fs.AttrArchive},
		{"/sau/hon/nua/tang/cuoi/day.txt", fs.AttrArchive},
		{"/Anh gia dinh", fs.AttrHidden | fs.AttrSystem | fs.AttrDir},
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

// TestEntryLocsThreeLocations kiểm tra mỗi entry bị giấu có ĐÚNG 3 EntryLoc
// (stdinfo, filename, index của thư mục cha), offset khác nhau, đều Width 4
// — thiếu vị trí thứ 3 (index cha) là lỗi kinh điển khiến Explorer vẫn giấu
// file dù đã sửa MFT.
func TestEntryLocsThreeLocations(t *testing.T) {
	vol, _ := openTestVolume(t)

	for _, p := range []string{"/visible.txt", "/Anh gia dinh", "/sau"} {
		e, err := vol.Stat(context.Background(), p)
		if err != nil {
			t.Fatalf("Stat(%s): %v", p, err)
		}
		if len(e.Locs) != 3 {
			t.Fatalf("%s: số Locs = %d, muốn 3: %+v", p, len(e.Locs), e.Locs)
		}
		regions := map[string]bool{}
		offsets := map[int64]bool{}
		for _, loc := range e.Locs {
			if loc.Width != 4 {
				t.Errorf("%s: loc %+v có Width = %d, muốn 4", p, loc, loc.Width)
			}
			if offsets[loc.Offset] {
				t.Errorf("%s: hai Loc trùng offset %d", p, loc.Offset)
			}
			offsets[loc.Offset] = true
			regions[loc.Region] = true
		}
		for _, want := range []string{"ntfs-stdinfo", "ntfs-filename", "ntfs-index"} {
			if !regions[want] {
				t.Errorf("%s: thiếu Loc vùng %s trong %+v", p, want, e.Locs)
			}
		}
	}
}

// TestClearAttrsGeneratesConsistentPatches kiểm tra ClearAttrs sinh patch cho
// mọi Loc thực sự thay đổi, Old khớp giá trị thật trên device, New đã gỡ đúng
// bit Hidden+System.
//
// Trên fixture này, setfattr của ntfs-3g chỉ cập nhật $STANDARD_INFORMATION
// và bản copy trong $I30 — KHÔNG đụng tới FileAttributes riêng của $FILE_NAME
// (giá trị gốc vẫn là Archive thuần, 0x20) — đúng kiểu lệch pha có thật giữa
// $STANDARD_INFORMATION/$FILE_NAME hay gặp trên NTFS thật. Vì vậy chỉ 2/3 Loc
// (stdinfo, index) thực sự đổi; Loc filename phải bị bỏ qua đúng theo yêu cầu
// "nếu không đổi thì bỏ qua loc đó".
func TestClearAttrsGeneratesConsistentPatches(t *testing.T) {
	vol, _ := openTestVolume(t)

	e, err := vol.Stat(context.Background(), "/visible.txt")
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if !e.Attrs.Has(fs.AttrHidden) || !e.Attrs.Has(fs.AttrSystem) {
		t.Fatalf("/visible.txt chưa Hidden+System trong fixture, không test được ClearAttrs: attr=%#04x", e.Attrs)
	}

	// Xác nhận trước: Loc "ntfs-filename" đọc từ device đã không còn bit
	// Hidden/System (đây là lý do patch tương ứng phải bị bỏ qua).
	for _, loc := range e.Locs {
		if loc.Region != "ntfs-filename" {
			continue
		}
		buf := make([]byte, 4)
		if _, err := vol.rd.ReadAt(buf, loc.Offset); err != nil {
			t.Fatalf("đọc Loc ntfs-filename: %v", err)
		}
		v := fs.Attr(binary.LittleEndian.Uint32(buf))
		if v.Has(fs.AttrHidden) || v.Has(fs.AttrSystem) {
			t.Fatalf("giả định fixture sai: $FILE_NAME của /visible.txt đã có Hidden/System (%#04x) — cập nhật lại test này", v)
		}
	}

	patches, err := vol.ClearAttrs(e, fs.AttrHiddenSystem)
	if err != nil {
		t.Fatalf("ClearAttrs: %v", err)
	}
	if len(patches) != 2 {
		t.Fatalf("số patch = %d, muốn 2 (stdinfo + index; filename không đổi nên bị bỏ qua): %+v", len(patches), patches)
	}
	gotRegions := map[string]bool{}
	for _, p := range patches {
		gotRegions[p.Region] = true
	}
	if !gotRegions["ntfs-stdinfo"] || !gotRegions["ntfs-index"] {
		t.Errorf("thiếu patch stdinfo hoặc index: %+v", patches)
	}
	if gotRegions["ntfs-filename"] {
		t.Errorf("patch ntfs-filename không nên xuất hiện (giá trị không đổi): %+v", patches)
	}

	for _, p := range patches {
		if len(p.Old) != 4 || len(p.New) != 4 {
			t.Errorf("patch %+v: Old/New phải dài 4 byte", p)
			continue
		}
		old := binary.LittleEndian.Uint32(p.Old)
		newVal := binary.LittleEndian.Uint32(p.New)
		if fs.Attr(newVal).Has(fs.AttrHidden) || fs.Attr(newVal).Has(fs.AttrSystem) {
			t.Errorf("region %s: New vẫn còn Hidden/System: %#08x", p.Region, newVal)
		}
		if newVal != old&^uint32(fs.AttrHiddenSystem) {
			t.Errorf("region %s: New = %#08x, muốn %#08x (Old với Hidden+System đã gỡ)", p.Region, newVal, old&^uint32(fs.AttrHiddenSystem))
		}

		// Old phải khớp giá trị thật đang có trên device tại offset đó.
		live := make([]byte, 4)
		if _, err := vol.rd.ReadAt(live, p.Offset); err != nil {
			t.Fatalf("đọc lại offset %d: %v", p.Offset, err)
		}
		if binary.LittleEndian.Uint32(live) != old {
			t.Errorf("region %s: Old = %#08x không khớp giá trị thật trên device %#08x", p.Region, old, binary.LittleEndian.Uint32(live))
		}
	}
}

// TestOpenTruncatedImageFails cắt cụt fixture còn boot sector nhưng mất toàn
// bộ vùng $MFT, kiểm tra Open trả lỗi fs.ErrCorrupt thay vì panic hoặc đọc
// tràn.
func TestOpenTruncatedImageFails(t *testing.T) {
	src := filepath.Join("..", "testdata", "ntfs.img")
	data, err := os.ReadFile(src)
	if err != nil {
		t.Skipf("thiếu fixture %s — sinh bằng: cd core/internal/fs/testdata && ./make-ntfs.sh (%v)", src, err)
	}
	const truncateAt = 8192 // còn boot sector, mất vùng $MFT (bắt đầu ở cluster 4 = byte 16384)
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

// TestWritableCleanVolume: fixture chính là volume shutdown sạch, không
// hiberfil.sys, dirty bit tắt, NTFS 3.1 → cho phép ghi.
func TestWritableCleanVolume(t *testing.T) {
	vol, _ := openTestVolume(t)
	ok, reason := vol.Writable()
	if !ok {
		t.Errorf("Writable() = false trên volume sạch, lý do: %q", reason)
	}
}

// TestWritableRefusesHiberfil: fixture có hiberfil.sys mô phỏng Fast Startup của
// Windows → phải từ chối ghi, vì Windows sẽ khôi phục metadata đè lên khi khởi
// động lại. Đây là nguyên nhân mất dữ liệu phổ biến nhất khi ghi NTFS từ macOS.
func TestWritableRefusesHiberfil(t *testing.T) {
	imgPath := filepath.Join("..", "testdata", "ntfs-hiberfil.img")
	f, err := os.Open(imgPath)
	if err != nil {
		t.Skipf("thiếu fixture %s — sinh bằng make-ntfs.sh (%v)", imgPath, err)
	}
	defer f.Close()
	info, _ := f.Stat()
	rd, err := blockdev.NewReader(f, 512, info.Size(), 0)
	if err != nil {
		t.Fatal(err)
	}
	vol, err := Open(rd)
	if err != nil {
		t.Fatal(err)
	}
	ok, reason := vol.Writable()
	if ok {
		t.Error("Writable() = true dù có hiberfil.sys — phải từ chối")
	}
	if !strings.Contains(reason, "hiberfil") && !strings.Contains(reason, "Fast Startup") {
		t.Errorf("lý do từ chối không nhắc tới hiberfil/Fast Startup: %q", reason)
	}
}

// TestWritableRefusesDirtyBit: bật dirty bit trong $VOLUME_INFORMATION trên bản
// copy fixture → Writable() phải từ chối. Windows tự đặt cờ này khi volume cần
// chkdsk; ghi thêm vào là chồng lỗi lên lỗi.
func TestWritableRefusesDirtyBit(t *testing.T) {
	src := filepath.Join("..", "testdata", "ntfs.img")
	if _, err := os.Stat(src); err != nil {
		t.Skipf("thiếu fixture %s", src)
	}
	dst := filepath.Join(t.TempDir(), "dirty.img")
	data, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dst, data, 0o644); err != nil {
		t.Fatal(err)
	}

	// Tính offset device của trường flags trong $VOLUME_INFORMATION của $Volume.
	f, err := os.OpenFile(dst, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	info, _ := f.Stat()
	rd, err := blockdev.NewReader(f, 512, info.Size(), 0)
	if err != nil {
		t.Fatal(err)
	}
	vol, err := Open(rd)
	if err != nil {
		t.Fatal(err)
	}
	rec, err := vol.readRecord(volumeRecord)
	if err != nil {
		t.Fatal(err)
	}
	attr, ok := findAttr(rec, attrTypeVolumeInfo)
	if !ok {
		t.Fatal("không thấy $VOLUME_INFORMATION")
	}
	flagsOff := rec.Offset + int64(attr.recOff) + int64(attr.ContentOffset) + 0x0A

	var cur [2]byte
	if _, err := f.ReadAt(cur[:], flagsOff); err != nil {
		t.Fatal(err)
	}
	cur[0] |= volumeFlagDirty
	if _, err := f.WriteAt(cur[:], flagsOff); err != nil {
		t.Fatal(err)
	}
	rd.Invalidate()

	vol2, err := Open(rd)
	if err != nil {
		t.Fatal(err)
	}
	ok2, reason := vol2.Writable()
	if ok2 {
		t.Error("Writable() = true dù dirty bit đã bật — phải từ chối")
	}
	if !strings.Contains(reason, "dirty") && !strings.Contains(reason, "kiểm tra đĩa") {
		t.Errorf("lý do không nhắc tới dirty/chkdsk: %q", reason)
	}
}

// TestReadVolumeInfo kiểm tra đọc đúng phiên bản NTFS từ $Volume.
func TestReadVolumeInfo(t *testing.T) {
	vol, _ := openTestVolume(t)
	major, minor, dirty, err := vol.readVolumeInfo()
	if err != nil {
		t.Fatalf("readVolumeInfo: %v", err)
	}
	if major != 3 {
		t.Errorf("major version = %d, muốn 3", major)
	}
	if minor != 0 && minor != 1 {
		t.Errorf("minor version = %d, muốn 0 hoặc 1", minor)
	}
	if dirty {
		t.Error("fixture sạch nhưng báo dirty")
	}
}

// TestLargeFragmentedDirectory kiểm tra reader duyệt đúng thư mục RẤT lớn có
// index phân mảnh (nhiều INDX block, cluster nhỏ khiến VCN tính theo cluster
// khác index-record). Fixture nặng 200MB, cần Docker để sinh — bỏ qua nếu thiếu.
//
// Đây là ca của ổ HDD ngoài dùng lâu, phân mảnh nặng — thứ fixture nhỏ không
// tái hiện. Trước khi sửa, reader vỡ hẳn với lỗi "VCN nằm ngoài runlist".
func TestLargeFragmentedDirectory(t *testing.T) {
	imgPath := filepath.Join("..", "testdata", "ntfs-fragmented.img")
	f, err := os.Open(imgPath)
	if err != nil {
		t.Skipf("thiếu fixture %s — sinh bằng ./make-ntfs-fragmented.sh (cần Docker)", imgPath)
	}
	defer f.Close()
	info, _ := f.Stat()
	rd, err := blockdev.NewReader(f, 512, info.Size(), 0)
	if err != nil {
		t.Fatal(err)
	}
	vol, err := Open(rd)
	if err != nil {
		t.Fatal(err)
	}

	// Đếm entry trong thư mục 5000-file: phải duyệt hết index nhiều tầng.
	var bigCount int
	var foundSecret bool
	err = vol.Walk(context.Background(), fs.WalkOpt{Root: "/bigdir"}, func(e *fs.Entry) error {
		bigCount++
		if strings.HasSuffix(e.Path, "zzz_secret_data.dat") {
			foundSecret = true
			if !e.Attrs.IsConcealed() {
				t.Errorf("file ẩn trong thư mục lớn phải có cờ Hidden+System, attr 0x%02x", e.Attrs)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("Walk /bigdir: %v", err)
	}
	if bigCount < 5000 {
		t.Errorf("chỉ duyệt được %d entry trong /bigdir, mong đợi >= 5000 (index phân mảnh bị đọc thiếu?)", bigCount)
	}
	if !foundSecret {
		t.Error("không tìm thấy file ẩn nằm sâu trong thư mục lớn — bỏ sót entry ngoài trang index đầu")
	}
}
