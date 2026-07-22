package ntfs

import (
	"fmt"

	"github.com/soi/doctorx/core/internal/fs"
)

// dataRun là một đoạn cluster liên tiếp trong data run list. LCN = -1 nghĩa
// là vùng thưa (sparse), không có cluster thật trên đĩa.
type dataRun struct {
	Length uint64 // số cluster
	LCN    int64
}

// decodeRunList giải mã data run list của một non-resident attribute.
// Mỗi run: 1 byte header (nibble thấp = số byte length, nibble cao = số byte
// offset), rồi length (không dấu), rồi offset (delta có dấu, cộng dồn từ run
// trước). Header 0x00 kết thúc danh sách.
func decodeRunList(buf []byte) ([]dataRun, error) {
	var runs []dataRun
	pos := 0
	var lcn int64
	for pos < len(buf) {
		header := buf[pos]
		if header == 0 {
			break
		}
		lenLen := int(header & 0x0F)
		offLen := int((header >> 4) & 0x0F)
		pos++
		if lenLen == 0 || pos+lenLen > len(buf) {
			return nil, fmt.Errorf("%w: data run length vượt quá buffer tại offset %d", fs.ErrCorrupt, pos)
		}
		length := readUintLE(buf[pos : pos+lenLen])
		pos += lenLen
		if offLen == 0 {
			// Sparse run: không có offset.
			runs = append(runs, dataRun{Length: length, LCN: -1})
			continue
		}
		if pos+offLen > len(buf) {
			return nil, fmt.Errorf("%w: data run offset vượt quá buffer tại offset %d", fs.ErrCorrupt, pos)
		}
		delta := readIntLE(buf[pos : pos+offLen])
		pos += offLen
		lcn += delta
		if lcn < 0 {
			return nil, fmt.Errorf("%w: LCN âm (%d) trong data run", fs.ErrCorrupt, lcn)
		}
		runs = append(runs, dataRun{Length: length, LCN: lcn})
	}
	return runs, nil
}

// readUintLE đọc tối đa 8 byte little-endian không dấu.
func readUintLE(b []byte) uint64 {
	var v uint64
	for i := len(b) - 1; i >= 0; i-- {
		v = v<<8 | uint64(b[i])
	}
	return v
}

// readIntLE đọc tối đa 8 byte little-endian có dấu (sign-extend theo bit cao
// nhất của byte cuối).
func readIntLE(b []byte) int64 {
	v := readUintLE(b)
	if len(b) < 8 && b[len(b)-1]&0x80 != 0 {
		v |= ^uint64(0) << (uint(len(b)) * 8)
	}
	return int64(v)
}

// mapStreamOffset ánh xạ một byte offset trong một stream non-resident
// (được mô tả bởi runs, bắt đầu từ startVCN) sang offset byte tuyệt đối trên
// volume. Dùng chung cho cả $DATA của $MFT lẫn $INDEX_ALLOCATION của thư mục.
func mapStreamOffset(runs []dataRun, startVCN uint64, clusterSize int64, streamOffset int64) (int64, error) {
	if streamOffset < 0 || clusterSize <= 0 {
		return 0, fmt.Errorf("%w: tham số ánh xạ stream không hợp lệ (offset=%d, clusterSize=%d)", fs.ErrCorrupt, streamOffset, clusterSize)
	}
	targetVCN := uint64(streamOffset / clusterSize)
	inCluster := streamOffset % clusterSize
	cur := startVCN
	for _, r := range runs {
		if targetVCN >= cur && targetVCN < cur+r.Length {
			if r.LCN < 0 {
				return 0, fmt.Errorf("%w: vùng thưa (sparse) tại VCN %d không được hỗ trợ", fs.ErrCorrupt, targetVCN)
			}
			return r.LCN*clusterSize + int64(targetVCN-cur)*clusterSize + inCluster, nil
		}
		cur += r.Length
	}
	return 0, fmt.Errorf("%w: VCN %d nằm ngoài runlist đã đọc (có thể do $ATTRIBUTE_LIST phân mảnh chưa được hỗ trợ đầy đủ)", fs.ErrCorrupt, targetVCN)
}
