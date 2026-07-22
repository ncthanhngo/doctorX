package scan

import (
	"bytes"
	"io"
	"os"
	"strings"
	"unicode/utf16"
)

// lnkHeaderSize và lnkMagic nhận diện file .lnk của Windows.
const lnkHeaderSize = 0x4C

var lnkMagic = []byte{0x4C, 0x00, 0x00, 0x00, 0x01, 0x14, 0x02, 0x00}

// maxLnkBytes giới hạn dung lượng đọc. Shortcut hợp lệ chỉ vài KB; file lớn hơn
// nhiều là bất thường và không đáng đọc hết.
const maxLnkBytes = 256 << 10

// LnkInfo là kết quả soi một file .lnk.
type LnkInfo struct {
	Valid bool
	// SuspiciousTokens là các chuỗi đáng ngờ tìm thấy trong shortcut.
	SuspiciousTokens []string
}

// suspiciousLnkTokens là dấu hiệu shortcut được dựng để chạy lệnh thay vì mở
// tài liệu — chữ ký kinh điển của họ worm giấu thư mục rồi thay bằng .lnk.
var suspiciousLnkTokens = []string{
	"cmd.exe", "powershell", "wscript", "cscript", "mshta",
	"rundll32", "regsvr32", "certutil", "bitsadmin",
	"/c start", "-executionpolicy", "-encodedcommand", "-windowstyle hidden",
	"vbscript:", "javascript:",
}

// InspectLnk soi một file shortcut.
//
// Cố ý KHÔNG parse đầy đủ cấu trúc LNK (LinkTargetIDList, LinkInfo,
// StringData...). Mục tiêu chỉ là gắn cờ nghi ngờ, và việc dò chuỗi lệnh trong
// cả dạng ASCII lẫn UTF-16 đã bắt được các biến thể thực tế mà không phải cõng
// theo một parser phức tạp dễ vỡ trước file cố ý làm hỏng.
func InspectLnk(path string) (LnkInfo, error) {
	f, err := os.Open(path)
	if err != nil {
		return LnkInfo{}, err
	}
	defer f.Close()

	buf := make([]byte, maxLnkBytes)
	n, err := io.ReadFull(f, buf)
	if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
		return LnkInfo{}, err
	}
	buf = buf[:n]

	if len(buf) < lnkHeaderSize || !bytes.HasPrefix(buf, lnkMagic) {
		return LnkInfo{Valid: false}, nil
	}

	haystack := strings.ToLower(string(buf) + " " + decodeUTF16Loose(buf))
	info := LnkInfo{Valid: true}
	for _, tok := range suspiciousLnkTokens {
		if strings.Contains(haystack, tok) {
			info.SuspiciousTokens = append(info.SuspiciousTokens, tok)
		}
	}
	return info, nil
}

// decodeUTF16Loose giải mã thô nội dung dạng UTF-16LE để dò chuỗi. Không quan
// tâm ranh giới chuỗi thật — chỉ cần chuỗi lệnh lộ ra là đủ.
func decodeUTF16Loose(b []byte) string {
	if len(b) < 2 {
		return ""
	}
	u := make([]uint16, 0, len(b)/2)
	for i := 0; i+1 < len(b); i += 2 {
		u = append(u, uint16(b[i])|uint16(b[i+1])<<8)
	}
	return string(utf16.Decode(u))
}
