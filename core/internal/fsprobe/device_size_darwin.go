package fsprobe

import (
	"fmt"
	"os"
	"syscall"
	"unsafe"
)

// ioctl để hỏi kích thước thiết bị khối trên macOS. Không lấy được bằng
// os.Stat: file thiết bị luôn báo size 0.
const (
	dkGetBlockSize  = 0x40046418 // DKIOCGETBLOCKSIZE  _IOR('d', 24, uint32)
	dkGetBlockCount = 0x40086419 // DKIOCGETBLOCKCOUNT _IOR('d', 25, uint64)
)

// deviceSize trả về dung lượng thiết bị theo byte. Với file thường (disk image
// dùng trong test) thì lấy trực tiếp từ Stat.
func deviceSize(f *os.File) (int64, error) {
	st, err := f.Stat()
	if err != nil {
		return 0, err
	}
	if st.Mode().IsRegular() {
		return st.Size(), nil
	}

	var blockSize uint32
	var blockCount uint64
	if err := ioctl(f, dkGetBlockSize, unsafe.Pointer(&blockSize)); err != nil {
		return 0, fmt.Errorf("hỏi kích thước block của thiết bị: %w", err)
	}
	if err := ioctl(f, dkGetBlockCount, unsafe.Pointer(&blockCount)); err != nil {
		return 0, fmt.Errorf("hỏi số block của thiết bị: %w", err)
	}
	return int64(blockSize) * int64(blockCount), nil
}

func ioctl(f *os.File, req uintptr, arg unsafe.Pointer) error {
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, f.Fd(), req, uintptr(arg))
	if errno != 0 {
		return errno
	}
	return nil
}
