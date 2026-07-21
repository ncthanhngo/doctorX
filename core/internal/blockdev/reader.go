// Package blockdev cung cấp I/O trên raw block device của macOS.
//
// Ràng buộc của /dev/rdiskN: mọi read/write phải align theo sector cả về offset
// lẫn độ dài. Package này giấu ràng buộc đó đi, cho phép tầng trên đọc ghi ở
// offset tuỳ ý, đồng thời cache sector để việc duyệt directory (đọc đi đọc lại
// vài sector) không đập liên tục xuống thiết bị.
package blockdev

import (
	"container/list"
	"fmt"
	"io"
	"sync"
)

// DefaultCacheBytes giới hạn RAM của cache. Cố định, không phụ thuộc dung lượng
// ổ — ổ 4TB và USB 8GB dùng chung mức này.
const DefaultCacheBytes = 16 << 20

// Reader đọc ở offset tuỳ ý trên một io.ReaderAt yêu cầu align theo block.
// An toàn khi dùng đồng thời từ nhiều goroutine.
type Reader struct {
	src       io.ReaderAt
	blockSize int64
	size      int64

	mu      sync.Mutex
	cache   map[int64]*list.Element
	lru     *list.List // phần tử là *cacheEntry, mới nhất ở đầu
	maxBlks int
}

type cacheEntry struct {
	blockIdx int64
	data     []byte
}

// NewReader tạo Reader trên src. blockSize phải là luỹ thừa của 2 (thường 512
// hoặc 4096). size là tổng số byte truy cập được, dùng để chặn đọc quá biên.
func NewReader(src io.ReaderAt, blockSize, size int64, cacheBytes int) (*Reader, error) {
	if blockSize <= 0 || blockSize&(blockSize-1) != 0 {
		return nil, fmt.Errorf("blockSize phải là luỹ thừa của 2, nhận %d", blockSize)
	}
	if cacheBytes <= 0 {
		cacheBytes = DefaultCacheBytes
	}
	maxBlks := cacheBytes / int(blockSize)
	if maxBlks < 8 {
		maxBlks = 8
	}
	return &Reader{
		src: src, blockSize: blockSize, size: size,
		cache: make(map[int64]*list.Element), lru: list.New(), maxBlks: maxBlks,
	}, nil
}

func (r *Reader) BlockSize() int64 { return r.blockSize }
func (r *Reader) Size() int64      { return r.size }

// ReadAt đọc len(p) byte tại off. Tự gom các block cần thiết và cắt đúng phần
// được yêu cầu.
func (r *Reader) ReadAt(p []byte, off int64) (int, error) {
	if off < 0 {
		return 0, fmt.Errorf("offset âm: %d", off)
	}
	if r.size > 0 && off+int64(len(p)) > r.size {
		return 0, fmt.Errorf("%w: đọc [%d,%d) vượt quá dung lượng %d",
			io.ErrUnexpectedEOF, off, off+int64(len(p)), r.size)
	}
	done := 0
	for done < len(p) {
		cur := off + int64(done)
		blkIdx := cur / r.blockSize
		inBlk := cur % r.blockSize

		blk, err := r.block(blkIdx)
		if err != nil {
			return done, err
		}
		n := copy(p[done:], blk[inBlk:])
		if n == 0 {
			return done, io.ErrUnexpectedEOF
		}
		done += n
	}
	return done, nil
}

// block trả về nội dung block blkIdx, ưu tiên lấy từ cache.
func (r *Reader) block(blkIdx int64) ([]byte, error) {
	r.mu.Lock()
	if el, ok := r.cache[blkIdx]; ok {
		r.lru.MoveToFront(el)
		data := el.Value.(*cacheEntry).data
		r.mu.Unlock()
		return data, nil
	}
	r.mu.Unlock()

	// Đọc ngoài lock: I/O chậm, không nên chặn các goroutine khác.
	buf := make([]byte, r.blockSize)
	if _, err := r.src.ReadAt(buf, blkIdx*r.blockSize); err != nil {
		return nil, fmt.Errorf("đọc block %d: %w", blkIdx, err)
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	// Goroutine khác có thể đã nạp block này trong lúc ta đọc; dùng bản của họ
	// để mọi caller thấy cùng một slice.
	if el, ok := r.cache[blkIdx]; ok {
		r.lru.MoveToFront(el)
		return el.Value.(*cacheEntry).data, nil
	}
	el := r.lru.PushFront(&cacheEntry{blockIdx: blkIdx, data: buf})
	r.cache[blkIdx] = el
	for r.lru.Len() > r.maxBlks {
		old := r.lru.Back()
		r.lru.Remove(old)
		delete(r.cache, old.Value.(*cacheEntry).blockIdx)
	}
	return buf, nil
}

// Invalidate xoá cache. Bắt buộc gọi sau khi ghi xuống device, nếu không lần
// đọc kiểm tra tiếp theo sẽ trả về dữ liệu cũ và verify-after-write thành vô nghĩa.
func (r *Reader) Invalidate() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.cache = make(map[int64]*list.Element)
	r.lru.Init()
}

// Section giới hạn một io.ReaderAt vào khoảng [off, off+n), dùng để cắt
// partition ra khỏi whole-disk device.
type Section struct {
	src io.ReaderAt
	off int64
	n   int64
}

func NewSection(src io.ReaderAt, off, n int64) *Section {
	return &Section{src: src, off: off, n: n}
}

func (s *Section) ReadAt(p []byte, off int64) (int, error) {
	if off < 0 || off+int64(len(p)) > s.n {
		return 0, fmt.Errorf("%w: đọc [%d,%d) ngoài partition dài %d",
			io.ErrUnexpectedEOF, off, off+int64(len(p)), s.n)
	}
	return s.src.ReadAt(p, s.off+off)
}

func (s *Section) Size() int64 { return s.n }
