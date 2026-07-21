package guard

import (
	"fmt"
	"sort"
)

// Region là một khoảng byte trên volume mà tầng ghi được phép chạm vào.
// Driver filesystem khai báo các vùng metadata (directory, MFT, index block)
// sau khi parse; mọi write ngoài các vùng này bị từ chối.
//
// Đây là lớp phòng thủ cuối: kể cả khi parser tính sai offset, write cũng
// không thể rơi vào FAT table, boot sector hay vùng dữ liệu người dùng.
type Region struct {
	Start int64
	End   int64 // không bao gồm
	Name  string
}

// RangeSet là tập các vùng được phép ghi của một volume.
type RangeSet struct {
	regions []Region
}

func NewRangeSet() *RangeSet { return &RangeSet{} }

// Add khai báo thêm một vùng được phép ghi.
func (rs *RangeSet) Add(start, end int64, name string) {
	if end <= start {
		return
	}
	rs.regions = append(rs.regions, Region{Start: start, End: end, Name: name})
	sort.Slice(rs.regions, func(i, j int) bool { return rs.regions[i].Start < rs.regions[j].Start })
}

// ErrOutOfRange báo write rơi ra ngoài mọi vùng metadata đã khai báo.
type ErrOutOfRange struct {
	Start, End int64
}

func (e *ErrOutOfRange) Error() string {
	return fmt.Sprintf("từ chối ghi [%d,%d): nằm ngoài vùng metadata của filesystem", e.Start, e.End)
}

// Check trả về nil nếu khoảng [off, off+n) nằm gọn trong một vùng cho phép.
// Cố ý không cho phép write bắc cầu qua hai vùng liền kề — attribute luôn nằm
// gọn trong một entry, bắc cầu nghĩa là tính offset sai.
func (rs *RangeSet) Check(off int64, n int) error {
	end := off + int64(n)
	i := sort.Search(len(rs.regions), func(i int) bool { return rs.regions[i].End > off })
	if i < len(rs.regions) && off >= rs.regions[i].Start && end <= rs.regions[i].End {
		return nil
	}
	return &ErrOutOfRange{Start: off, End: end}
}

// MaxPatchBytes chặn một patch attribute quá lớn. Thực tế: FAT 1 byte,
// exFAT 2 byte attr + 2 byte checksum, NTFS 4 byte flags. 8 là dư thoải mái.
const MaxPatchBytes = 8

// CheckPatchSize từ chối patch lớn bất thường — dấu hiệu logic sai ở tầng trên.
func CheckPatchSize(n int) error {
	if n <= 0 || n > MaxPatchBytes {
		return fmt.Errorf("kích thước patch bất thường: %d byte (tối đa %d)", n, MaxPatchBytes)
	}
	return nil
}
