// Package ipc là giao thức giữa app SwiftUI (chạy quyền người dùng) và
// doctorx-core (chạy quyền root).
//
// Định dạng: NDJSON — mỗi dòng một thông điệp JSON. Đủ dùng, dễ soi bằng mắt
// khi gỡ lỗi, và không cần thư viện ngoài.
package ipc

import (
	"encoding/json"
	"fmt"
)

// Request là một lời gọi từ app.
type Request struct {
	ID     int             `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params,omitempty"`
}

// Response là kết quả cuối cùng của một Request.
type Response struct {
	ID     int    `json:"id"`
	OK     bool   `json:"ok"`
	Result any    `json:"result,omitempty"`
	Error  *Error `json:"error,omitempty"`
}

// Event là thông điệp tiến trình gửi giữa chừng, dùng chung ID với Request.
// App phân biệt Event với Response bằng sự có mặt của trường "event".
type Event struct {
	ID    int    `json:"id"`
	Event string `json:"event"`
	Data  any    `json:"data,omitempty"`
}

// Error là lỗi có mã máy đọc được kèm thông điệp cho người dùng.
type Error struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *Error) Error() string { return e.Message }

// Mã lỗi. App dùng để quyết định hiển thị gì, không parse Message.
const (
	CodeBadRequest  = "bad_request"
	CodeNotFound    = "not_found"
	CodeProtected   = "protected_path"
	CodeNotWritable = "not_writable"
	CodeUnsupported = "unsupported_fs"
	CodeIO          = "io_error"
	CodeInternal    = "internal"
	CodeCanceled    = "canceled"
)

func Errf(code, format string, args ...any) *Error {
	return &Error{Code: code, Message: fmt.Sprintf(format, args...)}
}
