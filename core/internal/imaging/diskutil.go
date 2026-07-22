package imaging

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
)

// diskutilInfoJSON chạy `diskutil info -plist <bsd>` và giải mã sang out. Bản gọn
// riêng cho package imaging (helper cùng chức năng trong blockdev không export).
func diskutilInfoJSON(ctx context.Context, out any, bsd string) error {
	bsd = strings.TrimPrefix(bsd, "/dev/")
	plist, err := exec.CommandContext(ctx, "diskutil", "info", "-plist", bsd).Output()
	if err != nil {
		return fmt.Errorf("diskutil info %s: %w", bsd, err)
	}
	conv := exec.CommandContext(ctx, "plutil", "-convert", "json", "-o", "-", "-")
	conv.Stdin = strings.NewReader(string(plist))
	js, err := conv.Output()
	if err != nil {
		return fmt.Errorf("plutil chuyển plist sang json: %w", err)
	}
	return json.Unmarshal(js, out)
}
