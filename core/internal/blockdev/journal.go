package blockdev

import (
	"bufio"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

// journalMagic nhận diện file journal và version format.
const journalMagic = "DXJNL\x01"

// Journal lưu nội dung GỐC của mọi block trước khi bị ghi đè, cho phép hoàn
// tác nguyên khối một lần Quick Restore.
//
// Bất biến: block nào chưa được fsync vào journal thì tuyệt đối chưa được ghi
// xuống device. Nhờ đó mất điện giữa chừng vẫn rollback được đầy đủ.
type Journal struct {
	id   string
	path string
	f    *os.File
	w    *bufio.Writer
	seen map[int64]bool // block đã lưu, tránh ghi trùng khi patch cùng block
}

// JournalMeta là header JSON ở đầu file, giúp đọc lại journal cũ mà không cần
// tra cứu ở đâu khác.
type JournalMeta struct {
	ID          string    `json:"id"`
	CreatedAt   time.Time `json:"createdAt"`
	DeviceBSD   string    `json:"deviceBSD"`
	VolumeLabel string    `json:"volumeLabel"`
	Operation   string    `json:"operation"`
	BlockSize   int64     `json:"blockSize"`
}

// NewJournal tạo journal mới trong dir. id nên là chuỗi ngẫu nhiên/timestamp
// do tầng gọi sinh ra.
func NewJournal(dir string, meta JournalMeta) (*Journal, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("tạo thư mục journal: %w", err)
	}
	path := filepath.Join(dir, meta.ID+".jnl")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, fmt.Errorf("tạo journal %s: %w", path, err)
	}
	j := &Journal{id: meta.ID, path: path, f: f, w: bufio.NewWriter(f), seen: map[int64]bool{}}

	if _, err := j.w.WriteString(journalMagic); err != nil {
		return nil, err
	}
	hdr, err := json.Marshal(meta)
	if err != nil {
		return nil, err
	}
	if err := binary.Write(j.w, binary.LittleEndian, uint32(len(hdr))); err != nil {
		return nil, err
	}
	if _, err := j.w.Write(hdr); err != nil {
		return nil, err
	}
	return j, j.sync()
}

func (j *Journal) ID() string   { return j.id }
func (j *Journal) Path() string { return j.path }

// Save ghi nội dung gốc của một block vào journal và fsync ngay.
// Gọi lại với cùng offset là no-op — nội dung đầu tiên mới là bản gốc thật.
func (j *Journal) Save(off int64, data []byte) error {
	if j.seen[off] {
		return nil
	}
	if err := binary.Write(j.w, binary.LittleEndian, off); err != nil {
		return err
	}
	if err := binary.Write(j.w, binary.LittleEndian, uint32(len(data))); err != nil {
		return err
	}
	if _, err := j.w.Write(data); err != nil {
		return err
	}
	if err := j.sync(); err != nil {
		return err
	}
	j.seen[off] = true
	return nil
}

func (j *Journal) sync() error {
	if err := j.w.Flush(); err != nil {
		return fmt.Errorf("flush journal: %w", err)
	}
	if err := j.f.Sync(); err != nil {
		return fmt.Errorf("fsync journal: %w", err)
	}
	return nil
}

func (j *Journal) Close() error {
	if err := j.sync(); err != nil {
		j.f.Close()
		return err
	}
	return j.f.Close()
}

// JournalRecord là một block gốc đã lưu.
type JournalRecord struct {
	Offset int64
	Data   []byte
}

// ReadJournal đọc lại journal để rollback.
func ReadJournal(path string) (JournalMeta, []JournalRecord, error) {
	var meta JournalMeta
	f, err := os.Open(path)
	if err != nil {
		return meta, nil, err
	}
	defer f.Close()
	r := bufio.NewReader(f)

	magic := make([]byte, len(journalMagic))
	if _, err := io.ReadFull(r, magic); err != nil {
		return meta, nil, fmt.Errorf("đọc magic journal: %w", err)
	}
	if string(magic) != journalMagic {
		return meta, nil, fmt.Errorf("%s không phải journal DoctorX hợp lệ", path)
	}
	var hdrLen uint32
	if err := binary.Read(r, binary.LittleEndian, &hdrLen); err != nil {
		return meta, nil, err
	}
	hdr := make([]byte, hdrLen)
	if _, err := io.ReadFull(r, hdr); err != nil {
		return meta, nil, err
	}
	if err := json.Unmarshal(hdr, &meta); err != nil {
		return meta, nil, fmt.Errorf("header journal hỏng: %w", err)
	}

	var recs []JournalRecord
	for {
		var off int64
		if err := binary.Read(r, binary.LittleEndian, &off); err != nil {
			break // hết file, kể cả khi bị cắt cụt do mất điện
		}
		var n uint32
		if err := binary.Read(r, binary.LittleEndian, &n); err != nil {
			break
		}
		data := make([]byte, n)
		if _, err := io.ReadFull(r, data); err != nil {
			break // bản ghi cuối không trọn vẹn: bỏ, các bản trước vẫn dùng được
		}
		recs = append(recs, JournalRecord{Offset: off, Data: data})
	}
	return meta, recs, nil
}

// PruneJournals xoá journal cũ hơn maxAge.
func PruneJournals(dir string, maxAge time.Duration) error {
	ents, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	cutoff := time.Now().Add(-maxAge)
	for _, e := range ents {
		if filepath.Ext(e.Name()) != ".jnl" {
			continue
		}
		info, err := e.Info()
		if err != nil || info.ModTime().After(cutoff) {
			continue
		}
		os.Remove(filepath.Join(dir, e.Name()))
	}
	return nil
}
