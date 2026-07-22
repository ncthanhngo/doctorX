package blockdev

import (
	"context"
	"encoding/json"
	"testing"
)

// TestProbeExternalDisks in ra danh sách ổ ngoài thật đang cắm; chạy tay bằng
// `go test ./internal/blockdev -run Probe -v` để kiểm tra bằng mắt.
func TestProbeExternalDisks(t *testing.T) {
	d, err := ListExternalDisks(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.MarshalIndent(d, "", "  ")
	t.Logf("tìm thấy %d ổ ngoài:\n%s", len(d), b)
}
