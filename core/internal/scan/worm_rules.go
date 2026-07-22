package scan

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Mức độ nghi ngờ của một lần quét.
type Severity string

const (
	SeverityClean      Severity = "clean"
	SeveritySuspicious Severity = "suspicious"
	SeverityInfected   Severity = "likely-infected"
)

// Ngưỡng điểm. Chọn thấp vừa phải vì hậu quả của bỏ sót (người dùng mất dữ
// liệu) nặng hơn hậu quả của báo nhầm (người dùng thấy một cảnh báo thừa).
const (
	scoreSuspicious = 5
	scoreInfected   = 8
)

// Finding là một dấu hiệu cụ thể tìm được.
type Finding struct {
	Path       string `json:"path"`
	Rule       string `json:"rule"`
	Reason     string `json:"reason"`
	Score      int    `json:"score"`
	Deletable  bool   `json:"deletable"`
	IsDangling bool   `json:"-"`
}

// WormReport là kết quả quét heuristic.
type WormReport struct {
	Severity   Severity  `json:"severity"`
	TotalScore int       `json:"totalScore"`
	Findings   []Finding `json:"findings"`
}

// payloadExts là phần mở rộng thực thi được trên Windows.
var payloadExts = map[string]bool{
	".exe": true, ".scr": true, ".pif": true, ".com": true, ".bat": true,
	".cmd": true, ".vbs": true, ".vbe": true, ".js": true, ".jse": true,
	".wsf": true, ".wsh": true, ".hta": true, ".cpl": true,
}

// ScanWorm tìm dấu hiệu của họ worm lây qua USB trên một volume đã mount.
//
// Đây KHÔNG phải phần mềm diệt virus và không được trình bày như vậy. Nó nhận
// diện cấu trúc đặc trưng — thư mục thật bị giấu rồi thay bằng shortcut chạy
// lệnh — chứ không so khớp chữ ký mã độc.
//
// concealed là kết quả từ FindConcealedRaw/FindConcealedMounted trên cùng volume.
func ScanWorm(ctx context.Context, mountPoint string, concealed []Concealed) (*WormReport, error) {
	rep := &WormReport{Severity: SeverityClean}

	rootEntries, err := os.ReadDir(mountPoint)
	if err != nil {
		return nil, fmt.Errorf("đọc thư mục gốc của ổ: %w", err)
	}

	// Tên các mục bị giấu ở cấp gốc, dùng để bắt cặp "thư mục thật bị giấu" với
	// "shortcut cùng tên" — dấu hiệu lõi của shortcut virus.
	concealedRootDirs := map[string]bool{}
	concealedCount, visibleCount := 0, 0
	for _, c := range concealed {
		if c.Protected {
			continue
		}
		concealedCount++
		rel := strings.TrimPrefix(c.Path, "/")
		if c.IsDir && !strings.Contains(rel, "/") {
			concealedRootDirs[strings.ToLower(rel)] = true
		}
	}

	add := func(f Finding) {
		rep.Findings = append(rep.Findings, f)
		rep.TotalScore += f.Score
	}

	for _, e := range rootEntries {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		name := e.Name()
		lower := strings.ToLower(name)
		full := filepath.Join(mountPoint, name)

		if IsSystemPath("/" + name) {
			continue
		}
		if !e.IsDir() {
			visibleCount++
		}

		switch {
		case lower == "autorun.inf":
			if reason, bad := inspectAutorun(full); bad {
				add(Finding{Path: "/" + name, Rule: "autorun-inf", Score: 3, Deletable: true,
					Reason: "có autorun.inf trỏ tới file thực thi — " + reason})
			}

		case filepath.Ext(lower) == ".lnk":
			info, err := InspectLnk(full)
			if err != nil {
				continue
			}
			base := strings.ToLower(strings.TrimSuffix(name, filepath.Ext(name)))
			if len(info.SuspiciousTokens) > 0 {
				add(Finding{Path: "/" + name, Rule: "lnk-chay-lenh", Score: 5, Deletable: true,
					Reason: "shortcut này chạy lệnh hệ thống thay vì mở tài liệu: " +
						strings.Join(info.SuspiciousTokens, ", ")})
			}
			if concealedRootDirs[base] {
				add(Finding{Path: "/" + name, Rule: "lnk-thay-the-thu-muc", Score: 5, Deletable: true,
					Reason: fmt.Sprintf("có shortcut %q trong khi thư mục thật cùng tên đang bị giấu — đúng cách shortcut virus hoạt động", name)})
			}

		case payloadExts[filepath.Ext(lower)]:
			if isConcealedPath(concealed, "/"+name) {
				add(Finding{Path: "/" + name, Rule: "payload-an-o-goc", Score: 4, Deletable: true,
					Reason: "file thực thi bị giấu ngay ở gốc ổ"})
			}

		case e.IsDir() && strings.TrimSpace(name) == "":
			add(Finding{Path: "/" + name, Rule: "ten-vo-hinh", Score: 3, Deletable: false,
				Reason: "thư mục có tên toàn khoảng trắng, thường dùng để né mắt người dùng"})
		}
	}

	// Ổ mà mọi thứ đều bị giấu và không còn gì thấy được là dấu hiệu đã bị quét
	// sạch bởi worm.
	if concealedCount >= 5 && visibleCount == 0 {
		add(Finding{Path: "/", Rule: "toan-bo-bi-giau", Score: 3, Deletable: false,
			Reason: fmt.Sprintf("%d mục bị giấu nhưng không còn mục nào hiện ra ở gốc ổ", concealedCount)})
	}

	switch {
	case rep.TotalScore >= scoreInfected:
		rep.Severity = SeverityInfected
	case rep.TotalScore >= scoreSuspicious:
		rep.Severity = SeveritySuspicious
	}
	return rep, nil
}

func isConcealedPath(concealed []Concealed, path string) bool {
	for _, c := range concealed {
		if strings.EqualFold(c.Path, path) {
			return true
		}
	}
	return false
}

// inspectAutorun đọc autorun.inf và chỉ báo động khi nó thật sự trỏ tới file
// thực thi. Nhiều USB của nhà sản xuất có autorun.inf chỉ đặt icon và nhãn ổ —
// báo động với các file đó là báo nhầm.
func inspectAutorun(path string) (string, bool) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	text := strings.ToLower(string(b))
	for _, key := range []string{"open=", "shellexecute=", "shell\\open\\command="} {
		idx := strings.Index(text, key)
		if idx < 0 {
			continue
		}
		rest := text[idx+len(key):]
		if nl := strings.IndexAny(rest, "\r\n"); nl >= 0 {
			rest = rest[:nl]
		}
		rest = strings.TrimSpace(rest)
		for ext := range payloadExts {
			if strings.Contains(rest, ext) {
				return key + rest, true
			}
		}
	}
	return "", false
}
