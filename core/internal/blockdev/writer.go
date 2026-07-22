package blockdev

import (
	"bytes"
	"fmt"
	"io"
	"sort"

	"github.com/soi/doctorx/core/internal/fs"
	"github.com/soi/doctorx/core/internal/guard"
)

// ReadWriterAt là thiết bị hỗ trợ cả đọc lẫn ghi ở offset tuỳ ý.
type ReadWriterAt interface {
	io.ReaderAt
	io.WriterAt
}

// Writer là ĐƯỜNG GHI DUY NHẤT xuống device trong toàn bộ DoctorX.
// Mọi thay đổi đều phải đi qua đây để chắc chắn có đủ 4 lớp bảo vệ:
//
//  1. guard.RangeSet — offset phải nằm trong vùng metadata do parser khai báo
//  2. journal — nội dung gốc được fsync trước khi ghi đè
//  3. read-modify-write theo block — thoả ràng buộc align của /dev/rdiskN
//  4. verify-after-write — đọc lại và so byte, lệch thì rollback ngay
type Writer struct {
	dev     ReadWriterAt
	rd      *Reader
	ranges  *guard.RangeSet
	jnl     *Journal
	written []int64 // offset block đã ghi, theo thứ tự, dùng để rollback
}

func NewWriter(dev ReadWriterAt, rd *Reader, ranges *guard.RangeSet, jnl *Journal) *Writer {
	return &Writer{dev: dev, rd: rd, ranges: ranges, jnl: jnl}
}

// Apply ghi toàn bộ patch xuống device.
//
// Các patch nằm trong cùng một block được gộp thành MỘT lần ghi. Đây là điều
// kiện sống còn về hiệu năng chứ không phải tối ưu sớm: một sector FAT chứa 16
// directory entry, nên cây 100k file cần ~6k lượt ghi thay vì 100k.
//
// Bán nguyên tử: lỗi giữa chừng sẽ tự rollback các block đã ghi trong CHÍNH lần
// gọi này rồi trả về lỗi gốc.
func (w *Writer) Apply(patches []fs.Patch) (err error) {
	if len(patches) == 0 {
		return nil
	}
	for _, p := range patches {
		if err := guard.CheckPatchSize(len(p.New)); err != nil {
			return err
		}
		if len(p.Old) != len(p.New) {
			return fmt.Errorf("patch tại %d: độ dài Old (%d) khác New (%d)", p.Offset, len(p.Old), len(p.New))
		}
		if err := w.ranges.Check(p.Offset, len(p.New)); err != nil {
			return fmt.Errorf("%w (vùng %q)", err, p.Region)
		}
	}

	bs := w.rd.BlockSize()
	byBlock := map[int64][]fs.Patch{}
	for _, p := range patches {
		// Patch bắc cầu qua hai block nghĩa là parser tính offset sai —
		// attribute luôn nằm gọn trong một entry, entry luôn gọn trong sector.
		if p.Offset/bs != (p.Offset+int64(len(p.New))-1)/bs {
			return fmt.Errorf("patch tại %d dài %d bắc cầu qua hai block", p.Offset, len(p.New))
		}
		idx := p.Offset / bs
		byBlock[idx] = append(byBlock[idx], p)
	}

	blocks := make([]int64, 0, len(byBlock))
	for idx := range byBlock {
		blocks = append(blocks, idx)
	}
	sort.Slice(blocks, func(i, j int) bool { return blocks[i] < blocks[j] })

	defer func() {
		if err != nil {
			if rbErr := w.rollbackWritten(); rbErr != nil {
				err = fmt.Errorf("%w; rollback cũng thất bại: %v", err, rbErr)
			}
		}
	}()

	for _, idx := range blocks {
		if err := w.applyBlock(idx*bs, bs, byBlock[idx]); err != nil {
			return err
		}
	}
	return nil
}

// applyBlock thực hiện read-modify-write cho một block.
func (w *Writer) applyBlock(blockOff, bs int64, patches []fs.Patch) error {
	orig := make([]byte, bs)
	if _, err := w.dev.ReadAt(orig, blockOff); err != nil {
		return fmt.Errorf("đọc block tại %d: %w", blockOff, err)
	}

	// Kiểm tra Old khớp thực tế TRƯỚC khi ghi bất cứ thứ gì: nếu lệch nghĩa là
	// ổ đã bị thay đổi kể từ lúc parse, tiếp tục ghi sẽ hỏng dữ liệu.
	for _, p := range patches {
		at := p.Offset - blockOff
		if got := orig[at : at+int64(len(p.Old))]; !bytes.Equal(got, p.Old) {
			return fmt.Errorf("dữ liệu tại %d đã thay đổi (đọc %x, kỳ vọng %x) — ổ bị sửa bởi tiến trình khác?",
				p.Offset, got, p.Old)
		}
	}

	if err := w.jnl.Save(blockOff, orig); err != nil {
		return fmt.Errorf("lưu journal cho block %d: %w", blockOff, err)
	}

	modified := make([]byte, bs)
	copy(modified, orig)
	for _, p := range patches {
		copy(modified[p.Offset-blockOff:], p.New)
	}

	if _, err := w.dev.WriteAt(modified, blockOff); err != nil {
		return fmt.Errorf("ghi block tại %d: %w", blockOff, err)
	}
	w.written = append(w.written, blockOff)
	w.rd.Invalidate()

	verify := make([]byte, bs)
	if _, err := w.dev.ReadAt(verify, blockOff); err != nil {
		return fmt.Errorf("đọc lại để kiểm tra block %d: %w", blockOff, err)
	}
	if !bytes.Equal(verify, modified) {
		return fmt.Errorf("kiểm tra sau ghi thất bại tại block %d: nội dung trên ổ khác nội dung vừa ghi", blockOff)
	}
	return nil
}

// rollbackWritten hoàn tác các block đã ghi trong lần Apply hiện tại, theo thứ
// tự ngược.
func (w *Writer) rollbackWritten() error {
	_, recs, err := ReadJournal(w.jnl.Path())
	if err != nil {
		return err
	}
	orig := make(map[int64][]byte, len(recs))
	for _, r := range recs {
		orig[r.Offset] = r.Data
	}
	for i := len(w.written) - 1; i >= 0; i-- {
		off := w.written[i]
		data, ok := orig[off]
		if !ok {
			return fmt.Errorf("journal thiếu bản gốc của block %d", off)
		}
		if _, err := w.dev.WriteAt(data, off); err != nil {
			return fmt.Errorf("hoàn tác block %d: %w", off, err)
		}
	}
	w.written = nil
	w.rd.Invalidate()
	return nil
}

// Rollback hoàn tác toàn bộ một journal đã đóng. Dùng cho nút "Hoàn tác" trong
// UI, có thể chạy ở phiên làm việc khác.
func Rollback(dev ReadWriterAt, journalPath string) (int, error) {
	_, recs, err := ReadJournal(journalPath)
	if err != nil {
		return 0, err
	}
	// Ngược thứ tự ghi để trạng thái trung gian luôn hợp lệ.
	for i := len(recs) - 1; i >= 0; i-- {
		r := recs[i]
		if _, err := dev.WriteAt(r.Data, r.Offset); err != nil {
			return len(recs) - 1 - i, fmt.Errorf("hoàn tác block %d: %w", r.Offset, err)
		}
		verify := make([]byte, len(r.Data))
		if _, err := dev.ReadAt(verify, r.Offset); err != nil {
			return len(recs) - 1 - i, err
		}
		if !bytes.Equal(verify, r.Data) {
			return len(recs) - 1 - i, fmt.Errorf("kiểm tra sau hoàn tác thất bại tại block %d", r.Offset)
		}
	}
	return len(recs), nil
}
