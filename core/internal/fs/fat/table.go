package fat

import (
	"context"
	"encoding/binary"
	"fmt"

	"github.com/soi/doctorx/core/internal/fs"
)

// readFATEntry đọc giá trị bảng FAT (FAT#0) tại cluster n, đã che các bit dự
// trữ. Không cache riêng — dựa vào cache sector sẵn có trong blockdev.Reader.
func (v *Volume) readFATEntry(n uint32) (uint32, error) {
	switch v.kind {
	case fs.KindFAT12:
		off := v.fatOffset + int64(n) + int64(n/2)
		buf := make([]byte, 2)
		if _, err := v.rd.ReadAt(buf, off); err != nil {
			return 0, fmt.Errorf("đọc FAT12 tại cluster %d: %w: %w", n, fs.ErrCorrupt, err)
		}
		val := binary.LittleEndian.Uint16(buf)
		if n%2 == 0 {
			return uint32(val & 0x0FFF), nil
		}
		return uint32(val >> 4), nil
	case fs.KindFAT16:
		off := v.fatOffset + int64(n)*2
		buf := make([]byte, 2)
		if _, err := v.rd.ReadAt(buf, off); err != nil {
			return 0, fmt.Errorf("đọc FAT16 tại cluster %d: %w: %w", n, fs.ErrCorrupt, err)
		}
		return uint32(binary.LittleEndian.Uint16(buf)), nil
	default: // fs.KindFAT32
		off := v.fatOffset + int64(n)*4
		buf := make([]byte, 4)
		if _, err := v.rd.ReadAt(buf, off); err != nil {
			return 0, fmt.Errorf("đọc FAT32 tại cluster %d: %w: %w", n, fs.ErrCorrupt, err)
		}
		return binary.LittleEndian.Uint32(buf) & 0x0FFFFFFF, nil
	}
}

// isEOC báo raw là dấu hết chuỗi (end-of-cluster-chain).
func (v *Volume) isEOC(raw uint32) bool {
	switch v.kind {
	case fs.KindFAT12:
		return raw >= 0x0FF8
	case fs.KindFAT16:
		return raw >= 0xFFF8
	default:
		return raw >= 0x0FFFFFF8
	}
}

// isBadCluster báo raw là dấu cluster hỏng, không được dùng trong chuỗi.
func (v *Volume) isBadCluster(raw uint32) bool {
	switch v.kind {
	case fs.KindFAT12:
		return raw == 0x0FF7
	case fs.KindFAT16:
		return raw == 0xFFF7
	default:
		return raw == 0x0FFFFFF7
	}
}

// clusterChain đọc toàn bộ chuỗi cluster bắt đầu từ start bằng cách đi theo
// bảng FAT. Dùng visited-set để chặn vòng lặp vô hạn do ảnh hỏng hoặc cố ý —
// mỗi cluster hợp lệ chỉ được ghé một lần nên vòng lặp bị phát hiện ngay khi
// quay lại cluster đã thăm, thay vì chạy tới khi hết bộ nhớ.
func (v *Volume) clusterChain(ctx context.Context, start uint32) ([]uint32, error) {
	if start < 2 {
		return nil, nil
	}
	maxCluster := v.totalClusters + 1 // cluster hợp lệ: [2, totalClusters+1]

	visited := make(map[uint32]bool)
	var chain []uint32
	cur := start
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if cur < 2 || cur > maxCluster {
			return nil, fmt.Errorf("cluster %d ngoài phạm vi hợp lệ [2,%d]: %w", cur, maxCluster, fs.ErrCorrupt)
		}
		if visited[cur] {
			return nil, fs.ErrLoopDetected
		}
		visited[cur] = true
		chain = append(chain, cur)

		next, err := v.readFATEntry(cur)
		if err != nil {
			return nil, err
		}
		if v.isEOC(next) {
			break
		}
		if next == 0 || v.isBadCluster(next) {
			return nil, fmt.Errorf("chuỗi FAT tại cluster %d trỏ tới giá trị hỏng/rỗng (0x%x): %w", cur, next, fs.ErrCorrupt)
		}
		cur = next
	}
	return chain, nil
}
