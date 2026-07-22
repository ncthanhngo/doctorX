package imaging

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/soi/doctorx/core/internal/blockdev"
)

const smartctlEnv = "DOCTORX_SMARTCTL"

// DriveHealth là kết quả đọc SMART một ổ — giúp IT cảnh báo ổ sắp chết.
type DriveHealth struct {
	Available          bool   `json:"available"` // ổ có báo SMART không (USB nhiều khi không)
	Passed             bool   `json:"passed"`    // smart_status.passed
	Model              string `json:"model"`
	Serial             string `json:"serial"`
	TemperatureC       int    `json:"temperatureC"`
	PowerOnHours       int    `json:"powerOnHours"`
	ReallocatedSectors int    `json:"reallocatedSectors"` // >0 = ổ đang hỏng dần
	Note               string `json:"note"`
}

// DriveHealthOf đọc SMART của whole disk qua smartctl (đóng gói kèm app).
func DriveHealthOf(ctx context.Context, bsd string) (*DriveHealth, error) {
	disks, err := blockdev.ListExternalDisks(ctx)
	if err != nil {
		return nil, err
	}
	d, err := resolveTarget(disks, bsd)
	if err != nil {
		return nil, err
	}
	smartctl, err := resolveBundled(smartctlEnv, "smartctl")
	if err != nil {
		return nil, err
	}
	// -j JSON, -H health, -A attributes, -i info. Không cần root với hầu hết USB.
	out, _ := exec.CommandContext(ctx, smartctl, "-j", "-H", "-A", "-i",
		blockdev.RawDevicePath(d.BSD)).Output()
	if len(out) == 0 {
		return &DriveHealth{Available: false, Model: d.Model,
			Note: "ổ không trả dữ liệu SMART (thường gặp với USB qua bộ chuyển)"}, nil
	}
	h := parseSmartJSON(out)
	if h.Model == "" {
		h.Model = d.Model
	}
	return &h, nil
}

// parseSmartJSON rút các trường quan tâm từ output `smartctl -j`. Thuần để test.
func parseSmartJSON(data []byte) DriveHealth {
	var raw struct {
		ModelName string `json:"model_name"`
		SerialNum string `json:"serial_number"`
		// Con trỏ: phân biệt "SMART báo fail" (present, passed=false) với "ổ không
		// báo SMART" (absent). Nhiều USB qua bộ chuyển không trả smart_status.
		SmartStatus *struct {
			Passed bool `json:"passed"`
		} `json:"smart_status"`
		Temperature struct {
			Current int `json:"current"`
		} `json:"temperature"`
		PowerOnTime struct {
			Hours int `json:"hours"`
		} `json:"power_on_time"`
		ATAAttrs struct {
			Table []struct {
				ID   int    `json:"id"`
				Name string `json:"name"`
				Raw  struct {
					Value int `json:"value"`
				} `json:"raw"`
			} `json:"table"`
		} `json:"ata_smart_attributes"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return DriveHealth{Available: false, Note: "không đọc được output SMART"}
	}
	// Không có smart_status = ổ không thực sự báo SMART (thường là USB qua bộ
	// chuyển). Không được coi là "có vấn đề" — đó là false alarm.
	if raw.SmartStatus == nil {
		return DriveHealth{
			Available: false,
			Model:     strings.TrimSpace(raw.ModelName),
			Note:      "ổ không báo trạng thái SMART (thường gặp với USB qua bộ chuyển)",
		}
	}
	h := DriveHealth{
		Available:    true,
		Passed:       raw.SmartStatus.Passed,
		Model:        strings.TrimSpace(raw.ModelName),
		Serial:       strings.TrimSpace(raw.SerialNum),
		TemperatureC: raw.Temperature.Current,
		PowerOnHours: raw.PowerOnTime.Hours,
	}
	for _, a := range raw.ATAAttrs.Table {
		if a.ID == 5 || a.Name == "Reallocated_Sector_Ct" {
			h.ReallocatedSectors = a.Raw.Value
		}
	}
	if h.ReallocatedSectors > 0 {
		h.Note = fmt.Sprintf("có %d sector đã ánh xạ lại — ổ đang xuống cấp, nên sao lưu ngay", h.ReallocatedSectors)
	}
	return h
}

// resolveBundled tìm một binary đóng gói kèm app: biến môi trường trước (dev),
// rồi cạnh doctorx-core và trong app bundle, cuối cùng là PATH hệ thống.
func resolveBundled(envVar, name string) (string, error) {
	if p := os.Getenv(envVar); p != "" {
		if isExecutable(p) {
			return p, nil
		}
		return "", fmt.Errorf("%s trỏ tới %q nhưng không chạy được", envVar, p)
	}
	if exe, err := os.Executable(); err == nil {
		dir := filepath.Dir(exe)
		for _, cand := range []string{
			filepath.Join(dir, name),
			filepath.Join(dir, "..", "Resources", name),
		} {
			if isExecutable(cand) {
				return cand, nil
			}
		}
	}
	if p, err := exec.LookPath(name); err == nil {
		return p, nil
	}
	return "", fmt.Errorf("không tìm thấy %s — cần đóng gói binary này kèm app", name)
}
