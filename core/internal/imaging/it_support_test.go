package imaging

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"testing"
)

// --- Capture ---

func TestCaptureStreamRoundTrip(t *testing.T) {
	size := int64(bufSize) + 333
	src := make([]byte, size)
	for i := range src {
		src[i] = byte(i * 3)
	}
	var out bytes.Buffer
	sum, err := captureStream(context.Background(), &out, bytes.NewReader(src), size, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(out.Bytes(), src) {
		t.Fatal("dữ liệu bắt ra khác nguồn")
	}
	want := sha256.Sum256(src)
	if sum != hex.EncodeToString(want[:]) {
		t.Fatalf("hash lệch: %s vs %s", sum, hex.EncodeToString(want[:]))
	}
}

func TestCaptureStreamGzipRoundTrip(t *testing.T) {
	size := int64(bufSize) * 2
	src := bytes.Repeat([]byte("DoctorX"), int(size)/7+1)[:size]
	var gzbuf bytes.Buffer
	gz := gzip.NewWriter(&gzbuf)
	sum, err := captureStream(context.Background(), gz, bytes.NewReader(src), size, nil)
	if err != nil {
		t.Fatal(err)
	}
	gz.Close()
	// Giải nén lại phải khớp nguồn + hash.
	r, _ := gzip.NewReader(&gzbuf)
	got, _ := io.ReadAll(r)
	if !bytes.Equal(got, src) {
		t.Fatal("giải nén khác nguồn")
	}
	want := sha256.Sum256(src)
	if sum != hex.EncodeToString(want[:]) {
		t.Fatal("hash gzip lệch")
	}
}

// --- Encryption ---

func TestDetectBitLocker(t *testing.T) {
	boot := make([]byte, 512)
	copy(boot[3:], []byte("-FVE-FS-"))
	if !detectBitLocker(boot) {
		t.Fatal("phải nhận ra chữ ký BitLocker")
	}
	ntfs := make([]byte, 512)
	copy(ntfs[3:], []byte("NTFS    "))
	if detectBitLocker(ntfs) {
		t.Fatal("NTFS không phải BitLocker")
	}
	if detectBitLocker(make([]byte, 4)) {
		t.Fatal("buffer ngắn không được panic/nhận nhầm")
	}
}

// --- History ---

func TestHistoryAppendLoad(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "history.jsonl")
	// File chưa tồn tại → rỗng.
	if recs, err := LoadHistory(path, 0); err != nil || len(recs) != 0 {
		t.Fatalf("file chưa có phải rỗng: %v %v", recs, err)
	}
	for i := 0; i < 3; i++ {
		rec := HistoryRecord{Time: "t", Op: "wipe", Device: "disk4", Result: "ok", Detail: string(rune('A' + i))}
		if err := AppendHistory(path, rec); err != nil {
			t.Fatal(err)
		}
	}
	recs, err := LoadHistory(path, 0)
	if err != nil || len(recs) != 3 {
		t.Fatalf("phải có 3 bản ghi: %v %v", len(recs), err)
	}
	// Mới nhất lên đầu: bản ghi cuối cùng ('C').
	if recs[0].Detail != "C" {
		t.Fatalf("bản ghi mới nhất phải ở đầu, got %q", recs[0].Detail)
	}
	// limit.
	if lim, _ := LoadHistory(path, 2); len(lim) != 2 {
		t.Fatalf("limit 2 phải trả 2, got %d", len(lim))
	}
}

func TestHistoryLoadSkipsGarbage(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.jsonl")
	os.WriteFile(path, []byte("{\"op\":\"flash\"}\nrác không phải json\n{\"op\":\"format\"}\n"), 0o600)
	recs, err := LoadHistory(path, 0)
	if err != nil || len(recs) != 2 {
		t.Fatalf("phải bỏ qua dòng hỏng, còn 2: %d %v", len(recs), err)
	}
}

// --- SMART ---

func TestParseSmartJSON(t *testing.T) {
	sample := []byte(`{
	  "model_name": "Samsung SSD",
	  "serial_number": "S123",
	  "smart_status": {"passed": true},
	  "temperature": {"current": 41},
	  "power_on_time": {"hours": 1234},
	  "ata_smart_attributes": {"table": [
	    {"id": 5, "name": "Reallocated_Sector_Ct", "raw": {"value": 7}}
	  ]}
	}`)
	h := parseSmartJSON(sample)
	if !h.Available || !h.Passed || h.Model != "Samsung SSD" || h.Serial != "S123" {
		t.Fatalf("parse sai: %+v", h)
	}
	if h.TemperatureC != 41 || h.PowerOnHours != 1234 || h.ReallocatedSectors != 7 {
		t.Fatalf("số liệu sai: %+v", h)
	}
	if h.Note == "" {
		t.Fatal("reallocated>0 phải có cảnh báo")
	}
}

func TestParseSmartJSONBad(t *testing.T) {
	if h := parseSmartJSON([]byte("không phải json")); h.Available {
		t.Fatal("output hỏng phải Available=false")
	}
}

func TestParseSmartJSONNoStatus(t *testing.T) {
	// smartctl chạy nhưng ổ không báo SMART (USB qua bộ chuyển): có model nhưng
	// KHÔNG có smart_status. Phải là "không khả dụng", KHÔNG phải "có vấn đề".
	h := parseSmartJSON([]byte(`{"model_name": "Generic USB"}`))
	if h.Available {
		t.Fatal("thiếu smart_status phải Available=false")
	}
	if h.Passed {
		t.Fatal("thiếu smart_status không được báo passed (tránh false alarm)")
	}
	if h.Model != "Generic USB" || h.Note == "" {
		t.Fatalf("cần giữ model + có ghi chú: %+v", h)
	}
}
