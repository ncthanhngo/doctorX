// Package fs định nghĩa hợp đồng để định vị và sửa attribute của directory
// entry trực tiếp trên raw block device.
//
// Phạm vi hẹp có chủ đích. Đo trên macOS 15.6 (xem plan, mục "Kết quả đã kiểm
// chứng") cho thấy file Hidden+System vẫn ĐỌC được bình thường qua mount của
// macOS, nên việc quét và copy-out dùng filesystem API thông thường. Thứ duy
// nhất không API nào của macOS làm được là XOÁ BIT SYSTEM — đó là lý do duy
// nhất package này tồn tại.
//
// Vì vậy ở đây KHÔNG có đọc nội dung file: không cluster chain, không runlist,
// không resident/non-resident. Chỉ định vị entry và lật bit.
package fs

import (
	"context"
	"errors"
	"time"

	"github.com/soi/doctorx/core/internal/guard"
)

// Attr là DOS/NTFS file attribute bits. Giá trị khớp chuẩn FAT để FAT có thể
// dùng trực tiếp; exFAT và NTFS quy đổi về đây.
type Attr uint32

const (
	AttrReadOnly Attr = 0x01
	AttrHidden   Attr = 0x02
	AttrSystem   Attr = 0x04
	AttrVolumeID Attr = 0x08
	AttrDir      Attr = 0x10
	AttrArchive  Attr = 0x20
)

// AttrHiddenSystem là tổ hợp mà virus dùng để giấu file. Đây là thứ DoctorX gỡ.
const AttrHiddenSystem = AttrHidden | AttrSystem

func (a Attr) Has(f Attr) bool { return a&f != 0 }
func (a Attr) IsDir() bool     { return a.Has(AttrDir) }

// IsConcealed đúng khi entry bị giấu khỏi Explorer/Finder.
func (a Attr) IsConcealed() bool { return a.Has(AttrHiddenSystem) }

func (a Attr) Names() []string {
	var out []string
	for _, p := range []struct {
		bit  Attr
		name string
	}{
		{AttrReadOnly, "readonly"}, {AttrHidden, "hidden"}, {AttrSystem, "system"},
		{AttrDir, "directory"}, {AttrArchive, "archive"},
	} {
		if a.Has(p.bit) {
			out = append(out, p.name)
		}
	}
	return out
}

// Kind là loại filesystem đã nhận diện được.
type Kind string

const (
	KindFAT12 Kind = "fat12"
	KindFAT16 Kind = "fat16"
	KindFAT32 Kind = "fat32"
	KindExFAT Kind = "exfat"
	KindNTFS  Kind = "ntfs"
)

// EntryLoc là vị trí byte tuyệt đối trên volume của trường attribute cần sửa.
// Tầng ghi dùng nó để read-modify-write đúng sector, và tầng guard dùng nó để
// kiểm tra write có nằm trong vùng metadata hợp lệ không.
//
// Một entry có thể cần sửa ở nhiều nơi (NTFS: $STANDARD_INFORMATION,
// $FILE_NAME, và bản copy trong index $I30 của thư mục cha) nên đây là slice.
type EntryLoc struct {
	// Offset là vị trí byte của trường attribute tính từ đầu volume.
	Offset int64
	// Width là số byte của trường (FAT: 1, exFAT: 2, NTFS: 4).
	Width int
	// Region mô tả vùng chứa, dùng cho thông báo lỗi của guard.
	Region string
}

// Entry là một mục trong directory tree.
type Entry struct {
	Path     string
	Size     int64
	Attrs    Attr
	Modified time.Time

	// Locs là các vị trí cần ghi để đổi attribute. Rỗng nghĩa là entry này
	// không sửa được (vd: root directory của FAT16 không có entry mô tả nó).
	Locs []EntryLoc

	// FSPrivate cho phép mỗi driver mang theo dữ liệu riêng (first cluster,
	// MFT reference...) mà không rò rỉ vào API chung.
	FSPrivate any
}

// WalkOpt giới hạn phạm vi duyệt. Ổ ngoài 4TB có thể vài triệu entry nên việc
// giới hạn được độ sâu là bắt buộc, không phải tối ưu sớm: Quick Restore chỉ
// cần MaxDepth=1 để liệt kê thư mục ẩn ở gốc.
type WalkOpt struct {
	// Root là đường dẫn bắt đầu, "" hoặc "/" nghĩa là gốc volume.
	Root string
	// MaxDepth là số cấp duyệt tính từ Root; 0 nghĩa là không giới hạn.
	MaxDepth int
	// DirsOnly bỏ qua file thường trong callback (vẫn duyệt vào trong).
	DirsOnly bool
}

// VolumeInfo là metadata tổng quan của volume.
type VolumeInfo struct {
	Kind        Kind
	Label       string
	BytesPerSec uint32
	ClusterSize uint32
	TotalBytes  int64
}

// Volume là hợp đồng chung của một filesystem đã mount-less.
type Volume interface {
	Info() VolumeInfo

	// Walk gọi fn cho mọi entry theo DFS. fn trả error thì dừng và trả về
	// error đó. Cài đặt phải streaming — không được gom toàn bộ vào slice.
	Walk(ctx context.Context, opt WalkOpt, fn func(*Entry) error) error

	// Stat trả về đúng một entry theo đường dẫn.
	Stat(ctx context.Context, path string) (*Entry, error)

	// Writable báo volume có cho ghi attribute không, kèm lý do nếu không.
	// NTFS trả false khi journal bẩn / có hiberfil.sys / dirty bit bật.
	Writable() (bool, string)

	// MetadataRanges khai báo các vùng byte chứa metadata mà tầng ghi được
	// phép chạm vào. Driver phải khai báo hẹp nhất có thể — đây là lớp chặn
	// cuối cùng nếu tính offset sai.
	MetadataRanges() *guard.RangeSet

	// ClearAttrs dựng buffer cần ghi để gỡ các bit trong mask khỏi entry.
	// KHÔNG tự ghi xuống device — trả về patch để tầng rescue journal và ghi,
	// nhờ vậy toàn bộ đường ghi đi qua đúng một chỗ có guard và rollback.
	ClearAttrs(e *Entry, mask Attr) ([]Patch, error)
}

// Patch là một thay đổi byte nguyên tử tại một vị trí trên volume.
type Patch struct {
	Offset int64
	Old    []byte
	New    []byte
	Region string
}

var (
	ErrNotFound     = errors.New("không tìm thấy đường dẫn")
	ErrUnsupported  = errors.New("filesystem không được hỗ trợ")
	ErrCorrupt      = errors.New("cấu trúc filesystem hỏng")
	ErrReadOnlyFS   = errors.New("filesystem này không cho ghi")
	ErrNotWritable  = errors.New("volume đang ở trạng thái không an toàn để ghi")
	ErrDepthLimit   = errors.New("vượt quá độ sâu thư mục cho phép")
	ErrLoopDetected = errors.New("phát hiện vòng lặp trong cấu trúc thư mục")
)

// MaxPathDepth chặn cây thư mục lồng vô hạn do filesystem hỏng hoặc cố ý.
const MaxPathDepth = 128
