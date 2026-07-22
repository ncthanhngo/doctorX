package ipc

import (
	"fmt"
	"net"
	"syscall"
	"unsafe"
)

// Xác thực tiến trình kết nối bằng UID lấy từ socket.
//
// Không thể xác thực chữ ký mã (SecCode/TeamID) vì cách phân phối này không ký
// Developer ID — app chạy từ bản build cục bộ. Thay vào đó kiểm tra UID của
// tiến trình đầu kia: chỉ đúng người dùng đã khởi động dịch vụ mới được gọi.
// Đây là lớp phòng thủ chồng lên quyền 0600 của file socket, chặn cả tiến trình
// chạy dưới root hoặc người dùng khác kết nối vào.

// solLocal và localPeercred là hằng của macOS cho getsockopt trên Unix socket.
const (
	solLocal      = 0
	localPeercred = 0x001
)

// xucred khớp struct xucred của Darwin (<sys/ucred.h>).
type xucred struct {
	version uint32
	uid     uint32
	ngroups int16
	groups  [16]uint32
}

// peerUID trả về UID hiệu lực của tiến trình ở đầu kia kết nối.
func peerUID(conn *net.UnixConn) (uint32, error) {
	raw, err := conn.SyscallConn()
	if err != nil {
		return 0, err
	}
	var cred xucred
	var sockErr error
	ctrlErr := raw.Control(func(fd uintptr) {
		size := uint32(unsafe.Sizeof(cred))
		_, _, errno := syscall.Syscall6(
			syscall.SYS_GETSOCKOPT, fd, solLocal, localPeercred,
			uintptr(unsafe.Pointer(&cred)), uintptr(unsafe.Pointer(&size)), 0,
		)
		if errno != 0 {
			sockErr = errno
		}
	})
	if ctrlErr != nil {
		return 0, ctrlErr
	}
	if sockErr != nil {
		return 0, fmt.Errorf("đọc thông tin tiến trình kết nối: %w", sockErr)
	}
	return cred.uid, nil
}

// authorizePeer đóng conn và trả lỗi nếu UID đầu kia không khớp allowedUID.
// allowedUID < 0 nghĩa là không giới hạn (chỉ dùng khi chạy thủ công để gỡ lỗi).
func authorizePeer(conn net.Conn, allowedUID int) error {
	if allowedUID < 0 {
		return nil
	}
	uc, ok := conn.(*net.UnixConn)
	if !ok {
		return fmt.Errorf("kết nối không phải Unix socket")
	}
	uid, err := peerUID(uc)
	if err != nil {
		return err
	}
	if int(uid) != allowedUID {
		return fmt.Errorf("từ chối kết nối từ UID %d (chỉ cho phép %d)", uid, allowedUID)
	}
	return nil
}
