// Package fsprobe nhận diện filesystem trên một phân vùng và mở driver tương ứng.
//
// Tách riêng khỏi package fs để tránh phụ thuộc vòng: các driver import fs,
// còn package này import cả fs lẫn driver.
package fsprobe

import (
	"fmt"
	"os"
	"strings"

	"github.com/soi/doctorx/core/internal/blockdev"
	dfs "github.com/soi/doctorx/core/internal/fs"
	"github.com/soi/doctorx/core/internal/fs/exfat"
	"github.com/soi/doctorx/core/internal/fs/fat"
	"github.com/soi/doctorx/core/internal/fs/ntfs"
)

// Session giữ handle thiết bị và volume đã mở. Bắt buộc gọi Close.
type Session struct {
	File   *os.File
	Reader *blockdev.Reader
	Volume dfs.Volume
}

func (s *Session) Close() error {
	if s.File == nil {
		return nil
	}
	return s.File.Close()
}

// rawDevicePath đổi "disk4s2" thành "/dev/rdisk4s2".
//
// Dùng rdisk (character device) thay vì disk (block device): rdisk đi thẳng
// xuống thiết bị, không qua buffer cache của kernel, nên đọc lại để kiểm tra
// sau khi ghi phản ánh đúng dữ liệu thật trên ổ.
func rawDevicePath(bsd string) string {
	bsd = strings.TrimPrefix(bsd, "/dev/")
	bsd = strings.TrimPrefix(bsd, "r")
	return "/dev/r" + bsd
}

// OpenOpts điều khiển cách mở phiên làm việc.
type OpenOpts struct {
	// Write mở thiết bị ở chế độ đọc-ghi. Phân vùng phải đã được tháo mount.
	Write bool
	// CacheBytes giới hạn RAM cho cache sector; 0 dùng mặc định.
	CacheBytes int
}

// Open mở phân vùng theo tên BSD và nhận diện filesystem.
func Open(bsd string, opts OpenOpts) (*Session, error) {
	return OpenPath(rawDevicePath(bsd), opts)
}

// OpenPath mở theo đường dẫn bất kỳ — thiết bị hoặc file ảnh đĩa.
//
// Nhận cả file thường là có chủ đích: nhờ vậy toàn bộ đường ghi (kể cả journal
// và rollback) kiểm thử được trên disk image mà không cần quyền quản trị và
// không cần ổ thật, nên chạy được trong CI.
func OpenPath(path string, opts OpenOpts) (*Session, error) {
	flag := os.O_RDONLY
	if opts.Write {
		flag = os.O_RDWR
	}
	f, err := os.OpenFile(path, flag, 0)
	if err != nil {
		if os.IsPermission(err) {
			return nil, fmt.Errorf("không đủ quyền mở %s — thao tác này cần chạy với quyền quản trị: %w", path, err)
		}
		return nil, fmt.Errorf("mở %s: %w", path, err)
	}

	size, err := deviceSize(f)
	if err != nil {
		f.Close()
		return nil, err
	}

	// 512 là kích thước block logic mọi thiết bị đều chấp nhận; ổ 4Kn vẫn cho
	// đọc theo bội số 512 qua rdisk.
	rd, err := blockdev.NewReader(f, 512, size, opts.CacheBytes)
	if err != nil {
		f.Close()
		return nil, err
	}

	vol, err := detect(rd)
	if err != nil {
		f.Close()
		return nil, err
	}
	return &Session{File: f, Reader: rd, Volume: vol}, nil
}

// detect thử từng driver theo thứ tự đặc trưng nhận diện rõ ràng nhất trước.
func detect(rd *blockdev.Reader) (dfs.Volume, error) {
	boot := make([]byte, 512)
	if _, err := rd.ReadAt(boot, 0); err != nil {
		return nil, fmt.Errorf("đọc boot sector: %w", err)
	}

	switch {
	case string(boot[3:11]) == "EXFAT   ":
		return exfat.Open(rd)
	case string(boot[3:11]) == "NTFS    ":
		return ntfs.Open(rd)
	default:
		// FAT không có chuỗi nhận diện đáng tin, nhưng mọi FAT hợp lệ đều có
		// chữ ký 0x55AA ở offset 510-511. Thiếu nó nghĩa là đây KHÔNG phải FAT
		// (thường là APFS/HFS+ hoặc volume trống) — trả về "không hỗ trợ" để
		// tầng trên lùi về quét qua mount, thay vì báo "cấu trúc hỏng" gây hiểu
		// nhầm là ổ bị lỗi.
		if boot[510] != 0x55 || boot[511] != 0xAA {
			return nil, fmt.Errorf("%w: filesystem không phải FAT/exFAT/NTFS (không có driver quét sâu)", dfs.ErrUnsupported)
		}
		return fat.Open(rd)
	}
}

// FSKindOf đọc nhanh loại filesystem mà không mở driver đầy đủ.
func FSKindOf(bsd string) (dfs.Kind, error) {
	s, err := Open(bsd, OpenOpts{CacheBytes: 1 << 20})
	if err != nil {
		return "", err
	}
	defer s.Close()
	return s.Volume.Info().Kind, nil
}
