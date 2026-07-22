package imaging

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
)

// Nhật ký thao tác — audit trail cho IT: ổ nào (kèm serial/model), làm gì, khi
// nào, kết quả. Ghi dạng JSONL (mỗi dòng một bản ghi) để nối thêm rẻ và dễ soi.

// HistoryRecord là một dòng nhật ký. Time do CALLER điền (core không phụ thuộc
// clock cố định) để test tái lập được.
type HistoryRecord struct {
	Time   string `json:"time"`   // RFC3339, caller điền
	Op     string `json:"op"`     // flash | format | wipe | capture | bad_blocks
	Device string `json:"device"` // BSD
	Model  string `json:"model"`  // model ổ
	Result string `json:"result"` // ok | error
	Detail string `json:"detail"` // tóm tắt cho người đọc
}

// AppendHistory nối một bản ghi vào file JSONL, tạo thư mục cha nếu chưa có.
func AppendHistory(path string, rec HistoryRecord) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	line, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	line = append(line, '\n')
	_, err = f.Write(line)
	return err
}

// LoadHistory đọc tối đa `limit` bản ghi GẦN NHẤT (mới trước cũ sau). limit<=0 =
// tất cả. File chưa tồn tại → danh sách rỗng, không lỗi.
func LoadHistory(path string, limit int) ([]HistoryRecord, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()

	var recs []HistoryRecord
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var r HistoryRecord
		if json.Unmarshal(line, &r) == nil {
			recs = append(recs, r)
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	// Đảo để mới nhất lên đầu.
	for i, j := 0, len(recs)-1; i < j; i, j = i+1, j-1 {
		recs[i], recs[j] = recs[j], recs[i]
	}
	if limit > 0 && len(recs) > limit {
		recs = recs[:limit]
	}
	return recs, nil
}
