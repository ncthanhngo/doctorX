package blockdev

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"io"
	"testing"

	dfs "github.com/soi/doctorx/core/internal/fs"
	"github.com/soi/doctorx/core/internal/guard"
)

// memDevice là thiết bị giả trong RAM, có thể đếm số lần ghi và ép lỗi.
type memDevice struct {
	data   []byte
	writes int
	// failOffset mô phỏng bad sector: mọi lệnh ghi vào block này đều lỗi,
	// các block khác vẫn ghi được — nhờ vậy kiểm tra được rollback vẫn chạy
	// khi một sector hỏng giữa chừng.
	failOffset int64
	hasFail    bool
}

func newMemDevice(size int) *memDevice {
	d := &memDevice{data: make([]byte, size)}
	for i := range d.data {
		d.data[i] = byte(i % 251) // mẫu không lặp theo biên sector
	}
	return d
}

func (m *memDevice) ReadAt(p []byte, off int64) (int, error) {
	if off < 0 || off+int64(len(p)) > int64(len(m.data)) {
		return 0, io.ErrUnexpectedEOF
	}
	return copy(p, m.data[off:]), nil
}

func (m *memDevice) WriteAt(p []byte, off int64) (int, error) {
	m.writes++
	if m.hasFail && off == m.failOffset {
		return 0, errors.New("lỗi ghi giả lập (bad sector)")
	}
	if off < 0 || off+int64(len(p)) > int64(len(m.data)) {
		return 0, io.ErrUnexpectedEOF
	}
	return copy(m.data[off:], p), nil
}

func (m *memDevice) sum() [32]byte { return sha256.Sum256(m.data) }

// setup dựng device + reader + writer với vùng ghi cho phép là [512, 2048).
func setup(t *testing.T) (*memDevice, *Reader, *Writer, *Journal) {
	t.Helper()
	dev := newMemDevice(8192)
	rd, err := NewReader(dev, 512, int64(len(dev.data)), 0)
	if err != nil {
		t.Fatal(err)
	}
	ranges := guard.NewRangeSet()
	ranges.Add(512, 2048, "test-dir")

	jnl, err := NewJournal(t.TempDir(), JournalMeta{ID: "test", BlockSize: 512})
	if err != nil {
		t.Fatal(err)
	}
	return dev, rd, NewWriter(dev, rd, ranges, jnl), jnl
}

// patchAt dựng patch lật bit tại off, đọc Old từ chính device.
func patchAt(t *testing.T, dev *memDevice, off int64, mask byte) dfs.Patch {
	t.Helper()
	old := make([]byte, 1)
	if _, err := dev.ReadAt(old, off); err != nil {
		t.Fatal(err)
	}
	return dfs.Patch{Offset: off, Old: old, New: []byte{old[0] &^ mask}, Region: "test-dir"}
}

func TestApplyThenRollbackIsByteIdentical(t *testing.T) {
	dev, _, w, jnl := setup(t)
	before := dev.sum()

	patches := []dfs.Patch{
		patchAt(t, dev, 600, 0x06),
		patchAt(t, dev, 700, 0x06),
		patchAt(t, dev, 1500, 0x06),
	}
	if err := w.Apply(patches); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if dev.sum() == before {
		t.Fatal("device không thay đổi sau Apply")
	}
	if err := jnl.Close(); err != nil {
		t.Fatal(err)
	}

	n, err := Rollback(dev, jnl.Path())
	if err != nil {
		t.Fatalf("Rollback: %v", err)
	}
	if n == 0 {
		t.Fatal("Rollback không khôi phục block nào")
	}
	if dev.sum() != before {
		t.Fatal("sau rollback nội dung KHÔNG khớp bit-for-bit với ban đầu")
	}
}

// Gộp patch theo block là yêu cầu hiệu năng sống còn: một sector FAT chứa 16
// directory entry nên cây lớn phải sinh số lần ghi bằng số block, không phải
// bằng số entry.
func TestPatchesInSameBlockCoalesceToOneWrite(t *testing.T) {
	dev, _, w, _ := setup(t)

	var patches []dfs.Patch
	for off := int64(512); off < 1024; off += 32 { // 16 entry trong cùng 1 block
		patches = append(patches, patchAt(t, dev, off, 0x06))
	}
	if err := w.Apply(patches); err != nil {
		t.Fatal(err)
	}
	if dev.writes != 1 {
		t.Fatalf("kỳ vọng gộp thành 1 lần ghi, thực tế %d lần với %d patch", dev.writes, len(patches))
	}
}

func TestWriteOutsideDeclaredRangeIsRejected(t *testing.T) {
	dev, _, w, _ := setup(t)
	before := dev.sum()

	// 4096 nằm ngoài vùng [512,2048) đã khai báo.
	err := w.Apply([]dfs.Patch{patchAt(t, dev, 4096, 0x06)})
	if err == nil {
		t.Fatal("kỳ vọng bị từ chối, nhưng Apply thành công")
	}
	var oor *guard.ErrOutOfRange
	if !errors.As(err, &oor) {
		t.Fatalf("kỳ vọng ErrOutOfRange, nhận: %v", err)
	}
	if dev.writes != 0 {
		t.Fatalf("bị từ chối nhưng vẫn ghi %d lần — guard phải chặn TRƯỚC khi ghi", dev.writes)
	}
	if dev.sum() != before {
		t.Fatal("device bị thay đổi dù thao tác đã bị từ chối")
	}
}

func TestOversizedPatchIsRejected(t *testing.T) {
	dev, _, w, _ := setup(t)
	big := make([]byte, guard.MaxPatchBytes+1)
	err := w.Apply([]dfs.Patch{{Offset: 600, Old: big, New: big, Region: "test-dir"}})
	if err == nil {
		t.Fatal("kỳ vọng từ chối patch quá lớn")
	}
	if dev.writes != 0 {
		t.Fatal("không được ghi gì khi patch bị từ chối")
	}
}

// Old không khớp nghĩa là ổ đã bị thay đổi kể từ lúc parse; ghi tiếp sẽ hỏng
// dữ liệu nên phải dừng.
func TestStaleOldValueAborts(t *testing.T) {
	dev, _, w, _ := setup(t)
	p := patchAt(t, dev, 600, 0x06)
	p.Old = []byte{^p.Old[0]} // cố tình sai

	if err := w.Apply([]dfs.Patch{p}); err == nil {
		t.Fatal("kỳ vọng lỗi khi Old không khớp thực tế")
	}
	if dev.writes != 0 {
		t.Fatal("phải phát hiện lệch TRƯỚC khi ghi")
	}
}

// Lỗi giữa chừng phải tự hoàn tác các block đã ghi trong cùng lần Apply.
func TestPartialFailureRollsBackAutomatically(t *testing.T) {
	dev, _, w, _ := setup(t)
	before := dev.sum()
	dev.failOffset, dev.hasFail = 1024, true // block chứa offset 1500 bị hỏng

	patches := []dfs.Patch{
		patchAt(t, dev, 600, 0x06),  // block 1
		patchAt(t, dev, 1500, 0x06), // block 2 -> lỗi
	}
	if err := w.Apply(patches); err == nil {
		t.Fatal("kỳ vọng Apply trả lỗi")
	}
	dev.hasFail = false
	if dev.sum() != before {
		t.Fatal("lỗi giữa chừng nhưng device không được hoàn tác về trạng thái ban đầu")
	}
}

func TestPatchSpanningTwoBlocksIsRejected(t *testing.T) {
	dev, _, w, _ := setup(t)
	old := make([]byte, 4)
	if _, err := dev.ReadAt(old, 1022); err != nil {
		t.Fatal(err)
	}
	err := w.Apply([]dfs.Patch{{Offset: 1022, Old: old, New: make([]byte, 4), Region: "test-dir"}})
	if err == nil {
		t.Fatal("kỳ vọng từ chối patch bắc cầu qua hai block")
	}
	if dev.writes != 0 {
		t.Fatal("không được ghi gì")
	}
}

func TestJournalSurvivesTruncation(t *testing.T) {
	dir := t.TempDir()
	jnl, err := NewJournal(dir, JournalMeta{ID: "trunc", BlockSize: 512})
	if err != nil {
		t.Fatal(err)
	}
	blockA := bytes.Repeat([]byte{0xAA}, 512)
	blockB := bytes.Repeat([]byte{0xBB}, 512)
	if err := jnl.Save(0, blockA); err != nil {
		t.Fatal(err)
	}
	if err := jnl.Save(512, blockB); err != nil {
		t.Fatal(err)
	}
	jnl.Close()

	// Cắt cụt như thể mất điện giữa lúc ghi bản ghi cuối.
	raw, err := io.ReadAll(mustOpen(t, jnl.Path()))
	if err != nil {
		t.Fatal(err)
	}
	truncated := raw[:len(raw)-100]
	writeFile(t, jnl.Path(), truncated)

	_, recs, err := ReadJournal(jnl.Path())
	if err != nil {
		t.Fatalf("journal cắt cụt phải đọc được phần lành: %v", err)
	}
	if len(recs) != 1 || !bytes.Equal(recs[0].Data, blockA) {
		t.Fatalf("kỳ vọng giữ được 1 bản ghi nguyên vẹn, nhận %d", len(recs))
	}
}
