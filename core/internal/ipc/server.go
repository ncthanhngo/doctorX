package ipc

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"os"
	"sync"
)

// Handler xử lý một method. Gửi tiến trình bằng emit; giá trị trả về là kết quả
// cuối cùng.
type Handler func(ctx context.Context, params json.RawMessage, emit func(event string, data any)) (any, error)

// Server phục vụ app SwiftUI qua Unix domain socket.
type Server struct {
	path       string
	handlers   map[string]Handler
	ln         net.Listener
	allowedUID int
}

func NewServer(socketPath string) *Server {
	return &Server{path: socketPath, handlers: map[string]Handler{}, allowedUID: -1}
}

func (s *Server) Handle(method string, h Handler) { s.handlers[method] = h }

// Listen mở socket.
//
// Hai lớp bảo vệ: quyền file socket (mode 0600, chủ là ownerUID) và xác thực
// UID của tiến trình kết nối (authorizePeer). Chưa xác thực chữ ký mã vì cách
// phân phối này chạy từ bản build cục bộ, không ký Developer ID.
func (s *Server) Listen(ownerUID int) error {
	s.allowedUID = ownerUID
	if err := os.RemoveAll(s.path); err != nil {
		return fmt.Errorf("dọn socket cũ: %w", err)
	}
	ln, err := net.Listen("unix", s.path)
	if err != nil {
		return fmt.Errorf("mở socket %s: %w", s.path, err)
	}
	if err := os.Chmod(s.path, 0o600); err != nil {
		ln.Close()
		return err
	}
	if ownerUID >= 0 {
		if err := os.Chown(s.path, ownerUID, -1); err != nil {
			ln.Close()
			return fmt.Errorf("gán chủ sở hữu socket: %w", err)
		}
	}
	s.ln = ln
	return nil
}

func (s *Server) Serve(ctx context.Context) error {
	go func() {
		<-ctx.Done()
		s.ln.Close()
	}()
	for {
		conn, err := s.ln.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		if err := authorizePeer(conn, s.allowedUID); err != nil {
			log.Printf("từ chối kết nối: %v", err)
			conn.Close()
			continue
		}
		go s.serveConn(ctx, conn)
	}
}

func (s *Server) Close() error {
	if s.ln != nil {
		s.ln.Close()
	}
	return os.RemoveAll(s.path)
}

func (s *Server) serveConn(ctx context.Context, conn net.Conn) {
	defer conn.Close()
	var mu sync.Mutex // NDJSON: mỗi dòng phải ghi trọn vẹn, không xen kẽ
	enc := json.NewEncoder(conn)
	writeMsg := func(v any) {
		mu.Lock()
		defer mu.Unlock()
		if err := enc.Encode(v); err != nil {
			log.Printf("ghi phản hồi thất bại: %v", err)
		}
	}

	sc := bufio.NewScanner(conn)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var req Request
		if err := json.Unmarshal(line, &req); err != nil {
			writeMsg(Response{OK: false, Error: Errf(CodeBadRequest, "không đọc được yêu cầu: %v", err)})
			continue
		}
		h, ok := s.handlers[req.Method]
		if !ok {
			writeMsg(Response{ID: req.ID, OK: false, Error: Errf(CodeBadRequest, "method không hỗ trợ: %s", req.Method)})
			continue
		}

		// Xử lý tuần tự trên mỗi kết nối: các thao tác ghi xuống thiết bị không
		// được phép chạy song song trên cùng một ổ.
		result, err := h(ctx, req.Params, func(event string, data any) {
			writeMsg(Event{ID: req.ID, Event: event, Data: data})
		})
		if err != nil {
			e, ok := err.(*Error)
			if !ok {
				e = Errf(CodeInternal, "%v", err)
			}
			writeMsg(Response{ID: req.ID, OK: false, Error: e})
			continue
		}
		writeMsg(Response{ID: req.ID, OK: true, Result: result})
	}
}
