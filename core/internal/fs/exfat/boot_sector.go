package exfat

import (
	"bytes"
	"encoding/binary"
	"fmt"

	"github.com/soi/doctorx/core/internal/fs"
)

// bootSectorSize là kích thước Main Boot Sector, luôn là 512 byte trên exFAT
// (kể cả khi BytesPerSector logic lớn hơn — 512 byte đầu vẫn chứa toàn bộ các
// trường ta cần).
const bootSectorSize = 512

// exfatSignature nằm ở offset 3, dài 8 byte, dùng để nhận diện exFAT.
var exfatSignature = []byte("EXFAT   ")

// firstValidCluster là cluster hợp lệ nhỏ nhất; 0 và 1 dành riêng.
const firstValidCluster = 2

// bootSector là các trường của Main Boot Sector cần cho việc đọc metadata.
type bootSector struct {
	VolumeLength                uint64 // tính bằng sector
	FatOffset                   uint32 // tính bằng sector, từ đầu volume
	FatLength                   uint32 // tính bằng sector
	ClusterHeapOffset           uint32 // tính bằng sector, từ đầu volume
	ClusterCount                uint32
	FirstClusterOfRootDirectory uint32
	BytesPerSectorShift         uint8
	SectorsPerClusterShift      uint8
	NumberOfFats                uint8
}

func (b *bootSector) BytesPerSector() uint32    { return 1 << b.BytesPerSectorShift }
func (b *bootSector) SectorsPerCluster() uint32 { return 1 << b.SectorsPerClusterShift }
func (b *bootSector) BytesPerCluster() uint32 {
	return b.BytesPerSector() * b.SectorsPerCluster()
}

// validCluster báo cluster c có nằm trong phạm vi cluster heap không.
func (b *bootSector) validCluster(c uint32) bool {
	if c < firstValidCluster {
		return false
	}
	return uint64(c)-firstValidCluster < uint64(b.ClusterCount)
}

// clusterOffset quy đổi số cluster sang offset byte tuyệt đối trên volume.
// Chỉ gọi sau khi validCluster(c) đã true.
func clusterOffset(b *bootSector, c uint32) int64 {
	heapStart := int64(b.ClusterHeapOffset) * int64(b.BytesPerSector())
	return heapStart + int64(c-firstValidCluster)*int64(b.BytesPerCluster())
}

// parseBootSector đọc và xác thực Main Boot Sector tại offset 0 của volume.
// buf phải dài ít nhất bootSectorSize byte.
func parseBootSector(buf []byte) (*bootSector, error) {
	if len(buf) < bootSectorSize {
		return nil, fmt.Errorf("%w: boot sector ngắn hơn %d byte", fs.ErrCorrupt, bootSectorSize)
	}
	if !bytes.Equal(buf[3:11], exfatSignature) {
		return nil, fmt.Errorf("%w: không phải exFAT (thiếu chữ ký EXFAT)", fs.ErrUnsupported)
	}

	b := &bootSector{
		VolumeLength:                binary.LittleEndian.Uint64(buf[72:80]),
		FatOffset:                   binary.LittleEndian.Uint32(buf[80:84]),
		FatLength:                   binary.LittleEndian.Uint32(buf[84:88]),
		ClusterHeapOffset:           binary.LittleEndian.Uint32(buf[88:92]),
		ClusterCount:                binary.LittleEndian.Uint32(buf[92:96]),
		FirstClusterOfRootDirectory: binary.LittleEndian.Uint32(buf[96:100]),
		BytesPerSectorShift:         buf[108],
		SectorsPerClusterShift:      buf[109],
		NumberOfFats:                buf[110],
	}

	// Xác thực range hợp lý theo đặc tả exFAT, chặn giá trị rác gây tính toán
	// offset tràn số hoặc âm ở tầng trên.
	if b.BytesPerSectorShift < 9 || b.BytesPerSectorShift > 12 {
		return nil, fmt.Errorf("%w: BytesPerSectorShift bất thường: %d", fs.ErrCorrupt, b.BytesPerSectorShift)
	}
	if int(b.SectorsPerClusterShift) > 25-int(b.BytesPerSectorShift) {
		return nil, fmt.Errorf("%w: SectorsPerClusterShift bất thường: %d", fs.ErrCorrupt, b.SectorsPerClusterShift)
	}
	if b.NumberOfFats == 0 || b.NumberOfFats > 2 {
		return nil, fmt.Errorf("%w: NumberOfFats bất thường: %d", fs.ErrCorrupt, b.NumberOfFats)
	}
	if b.ClusterCount == 0 {
		return nil, fmt.Errorf("%w: ClusterCount = 0", fs.ErrCorrupt)
	}
	if !b.validCluster(b.FirstClusterOfRootDirectory) {
		return nil, fmt.Errorf("%w: FirstClusterOfRootDirectory không hợp lệ: %d", fs.ErrCorrupt, b.FirstClusterOfRootDirectory)
	}
	return b, nil
}
