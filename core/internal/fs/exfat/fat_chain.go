package exfat

import (
	"encoding/binary"
	"fmt"

	"github.com/soi/doctorx/core/internal/blockdev"
	"github.com/soi/doctorx/core/internal/fs"
)

// clusterEOF đánh dấu cluster cuối cùng của một chain trong bảng FAT.
const clusterEOF = 0xFFFFFFFF

// fatEntry đọc entry thứ `cluster` trong bảng FAT đầu tiên (FAT#0).
func fatEntry(rd *blockdev.Reader, b *bootSector, cluster uint32) (uint32, error) {
	off := int64(b.FatOffset)*int64(b.BytesPerSector()) + int64(cluster)*4
	var buf [4]byte
	if _, err := rd.ReadAt(buf[:], off); err != nil {
		return 0, fmt.Errorf("%w: đọc FAT entry của cluster %d: %v", fs.ErrCorrupt, cluster, err)
	}
	return binary.LittleEndian.Uint32(buf[:]), nil
}

// followFATChain đi theo FAT chain bắt đầu từ `first`, trả về danh sách
// cluster theo đúng thứ tự, dừng khi gặp EOF (0xFFFFFFFF).
//
// Có visited-set chống vòng lặp: nếu chain quay lại một cluster đã thăm,
// trả fs.ErrLoopDetected thay vì lặp vô hạn.
func followFATChain(rd *blockdev.Reader, b *bootSector, first uint32) ([]uint32, error) {
	if !b.validCluster(first) {
		return nil, fmt.Errorf("%w: cluster bắt đầu chain không hợp lệ: %d", fs.ErrCorrupt, first)
	}

	var chain []uint32
	visited := make(map[uint32]bool)
	cur := first
	for {
		if visited[cur] {
			return nil, fs.ErrLoopDetected
		}
		visited[cur] = true
		chain = append(chain, cur)

		next, err := fatEntry(rd, b, cur)
		if err != nil {
			return nil, err
		}
		if next == clusterEOF {
			break
		}
		if !b.validCluster(next) {
			return nil, fmt.Errorf("%w: FAT chain trỏ tới cluster không hợp lệ: %d", fs.ErrCorrupt, next)
		}
		cur = next
	}
	return chain, nil
}

// contiguousChain sinh danh sách cluster liên tiếp nhau, dùng khi entry set
// bật cờ NoFatChain (dữ liệu được cấp phát liền khối, không cần tra FAT).
func contiguousChain(first uint32, count uint32) []uint32 {
	chain := make([]uint32, count)
	for i := range chain {
		chain[i] = first + uint32(i)
	}
	return chain
}
