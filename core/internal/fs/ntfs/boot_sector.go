package ntfs

import (
	"bytes"
	"encoding/binary"
	"fmt"

	"github.com/soi/doctorx/core/internal/fs"
)

// bootSectorSize là kích thước boot sector NTFS luôn đọc, dù BytesPerSector
// logic có thể lớn hơn — 512 byte đầu vẫn chứa mọi trường ta cần.
const bootSectorSize = 512

// ntfsSignature nằm ở offset 3, dài 8 byte, dùng để nhận diện NTFS.
var ntfsSignature = []byte("NTFS    ")

// bootSector là các trường của NTFS Boot Sector cần cho việc đọc metadata.
type bootSector struct {
	BytesPerSector    uint32
	SectorsPerCluster uint32
	TotalSectors      uint64
	MFTStartCluster   uint64
	// RecordSize là kích thước một MFT file record, byte. Suy ra từ trường
	// "clusters per MFT record" (signed byte @0x40): âm nghĩa là
	// 2^abs(value) byte, dương nghĩa là value*ClusterSize byte.
	RecordSize uint32
}

func (b *bootSector) ClusterSize() uint32 { return b.BytesPerSector * b.SectorsPerCluster }

// parseBootSector đọc và xác thực Boot Sector tại offset 0 của volume.
// buf phải dài ít nhất bootSectorSize byte.
func parseBootSector(buf []byte) (*bootSector, error) {
	if len(buf) < bootSectorSize {
		return nil, fmt.Errorf("%w: boot sector ngắn hơn %d byte", fs.ErrCorrupt, bootSectorSize)
	}
	if !bytes.Equal(buf[3:11], ntfsSignature) {
		return nil, fmt.Errorf("%w: không phải NTFS (thiếu chữ ký NTFS)", fs.ErrUnsupported)
	}

	bytesPerSector := uint32(binary.LittleEndian.Uint16(buf[0x0B:0x0D]))
	sectorsPerCluster := uint32(buf[0x0D])
	totalSectors := binary.LittleEndian.Uint64(buf[0x28:0x30])
	mftStartCluster := binary.LittleEndian.Uint64(buf[0x30:0x38])
	clustersPerRecordRaw := int8(buf[0x40])

	// Xác thực range hợp lý, chặn giá trị rác gây tính toán offset tràn số
	// hoặc âm ở tầng trên — cùng triết lý với exfat.parseBootSector.
	if bytesPerSector < 256 || bytesPerSector > 4096 || bytesPerSector&(bytesPerSector-1) != 0 {
		return nil, fmt.Errorf("%w: BytesPerSector bất thường: %d", fs.ErrCorrupt, bytesPerSector)
	}
	if sectorsPerCluster == 0 || sectorsPerCluster > 128 || sectorsPerCluster&(sectorsPerCluster-1) != 0 {
		return nil, fmt.Errorf("%w: SectorsPerCluster bất thường: %d", fs.ErrCorrupt, sectorsPerCluster)
	}
	if totalSectors == 0 {
		return nil, fmt.Errorf("%w: TotalSectors = 0", fs.ErrCorrupt)
	}
	if mftStartCluster == 0 {
		return nil, fmt.Errorf("%w: cluster bắt đầu $MFT = 0", fs.ErrCorrupt)
	}

	clusterSize := bytesPerSector * sectorsPerCluster
	var recordSize uint32
	switch {
	case clustersPerRecordRaw < 0:
		shift := uint(-int(clustersPerRecordRaw))
		if shift > 20 {
			return nil, fmt.Errorf("%w: ClustersPerMFTRecord bất thường: %d", fs.ErrCorrupt, clustersPerRecordRaw)
		}
		recordSize = 1 << shift
	case clustersPerRecordRaw > 0:
		recordSize = uint32(clustersPerRecordRaw) * clusterSize
	default:
		return nil, fmt.Errorf("%w: ClustersPerMFTRecord = 0", fs.ErrCorrupt)
	}
	if recordSize < 256 || recordSize > 65536 {
		return nil, fmt.Errorf("%w: kích thước MFT record bất thường: %d", fs.ErrCorrupt, recordSize)
	}

	return &bootSector{
		BytesPerSector:    bytesPerSector,
		SectorsPerCluster: sectorsPerCluster,
		TotalSectors:      totalSectors,
		MFTStartCluster:   mftStartCluster,
		RecordSize:        recordSize,
	}, nil
}
