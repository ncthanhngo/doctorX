package ipc

import (
	"net"
	"os"
	"path/filepath"
	"testing"
)

// dialPair mở một Unix socket, chấp nhận một kết nối và trả về đầu server.
func dialPair(t *testing.T) *net.UnixConn {
	t.Helper()
	sock := filepath.Join(t.TempDir(), "t.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })

	accepted := make(chan net.Conn, 1)
	go func() {
		c, _ := ln.Accept()
		accepted <- c
	}()
	client, err := net.Dial("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { client.Close() })

	srv := <-accepted
	t.Cleanup(func() { srv.Close() })
	return srv.(*net.UnixConn)
}

func TestPeerUIDMatchesSelf(t *testing.T) {
	conn := dialPair(t)
	uid, err := peerUID(conn)
	if err != nil {
		t.Fatalf("peerUID: %v", err)
	}
	if int(uid) != os.Getuid() {
		t.Errorf("peerUID = %d, muốn UID hiện tại %d", uid, os.Getuid())
	}
}

func TestAuthorizePeerAcceptsOwner(t *testing.T) {
	conn := dialPair(t)
	if err := authorizePeer(conn, os.Getuid()); err != nil {
		t.Errorf("phải chấp nhận UID của chính mình: %v", err)
	}
}

func TestAuthorizePeerRejectsOtherUID(t *testing.T) {
	conn := dialPair(t)
	// UID chắc chắn khác UID hiện tại.
	other := os.Getuid() + 1000
	if err := authorizePeer(conn, other); err == nil {
		t.Error("phải từ chối UID không khớp")
	}
}

func TestAuthorizePeerUnrestricted(t *testing.T) {
	conn := dialPair(t)
	if err := authorizePeer(conn, -1); err != nil {
		t.Errorf("allowedUID < 0 phải cho qua: %v", err)
	}
}
