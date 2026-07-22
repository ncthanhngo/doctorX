package fat

import (
	"encoding/binary"
	"fmt"
	"strings"

	"github.com/soi/doctorx/core/internal/fs"
)

// bootSector là kết quả parse Boot Sector + BIOS Parameter Block (BPB) của
// FAT12/16/32. Chỉ giữ lại các trường cần để định vị FAT table, root
// directory và data region — không quan tâm CHS, geometry cũ.
type bootSector struct {
	bytesPerSector    uint32
	sectorsPerCluster uint32
	reservedSectors   uint32
	numFATs           uint32
	rootEntryCount    uint32
	fatSize           uint32 // số sector của MỘT bảng FAT
	rootDirSectors    uint32
	firstDataSector   uint32
	totalClusters     uint32
	rootCluster       uint32 // chỉ có nghĩa với FAT32, 0 với FAT12/16
	totalSectors      uint32
	label             string
	kind              fs.Kind
}

// bootSectorSize là kích thước tối thiểu cần đọc để lấy hết BPB + chữ ký
// 0x55AA (luôn nằm ở offset 510-511 bất kể BytesPerSector thật của volume).
const bootSectorSize = 512

// parseBootSector đọc BPB từ 512 byte đầu volume. Trả fs.ErrCorrupt bọc ngữ
// cảnh cho mọi bất thường — không panic dù giá trị trong buf vô nghĩa.
func parseBootSector(buf []byte) (*bootSector, error) {
	if len(buf) < bootSectorSize {
		return nil, fmt.Errorf("boot sector ngắn hơn %d byte: %w", bootSectorSize, fs.ErrCorrupt)
	}
	if buf[510] != 0x55 || buf[511] != 0xAA {
		return nil, fmt.Errorf("thiếu chữ ký boot sector 0x55AA: %w", fs.ErrCorrupt)
	}

	bs := &bootSector{
		bytesPerSector:    uint32(binary.LittleEndian.Uint16(buf[11:13])),
		sectorsPerCluster: uint32(buf[13]),
		reservedSectors:   uint32(binary.LittleEndian.Uint16(buf[14:16])),
		numFATs:           uint32(buf[16]),
		rootEntryCount:    uint32(binary.LittleEndian.Uint16(buf[17:19])),
	}

	if err := validateGeometry(bs.bytesPerSector, bs.sectorsPerCluster, bs.numFATs); err != nil {
		return nil, err
	}

	totalSectors16 := uint32(binary.LittleEndian.Uint16(buf[19:21]))
	fatSize16 := uint32(binary.LittleEndian.Uint16(buf[22:24]))
	totalSectors32 := binary.LittleEndian.Uint32(buf[32:36])

	// Sự hiện diện của BPB mở rộng kiểu FAT32 (FATSz32, RootCluster, nhãn ở
	// offset 71) được quyết định bởi FATSz16==0 — đúng cách các công cụ format
	// (newfs_msdos, mkfs.vfat) tự chọn layout, độc lập với phân loại FAT12/16/32
	// theo số cluster bên dưới.
	var fatSize32 uint32
	var rootCluster uint32
	var labelBytes []byte
	if fatSize16 == 0 {
		fatSize32 = binary.LittleEndian.Uint32(buf[36:40])
		rootCluster = binary.LittleEndian.Uint32(buf[44:48])
		labelBytes = buf[71:82]
	} else {
		labelBytes = buf[43:54]
	}

	bs.fatSize = fatSize16
	if bs.fatSize == 0 {
		bs.fatSize = fatSize32
	}
	if bs.fatSize == 0 {
		return nil, fmt.Errorf("kích thước bảng FAT bằng 0: %w", fs.ErrCorrupt)
	}

	bs.totalSectors = totalSectors16
	if bs.totalSectors == 0 {
		bs.totalSectors = totalSectors32
	}
	if bs.totalSectors == 0 {
		return nil, fmt.Errorf("tổng số sector bằng 0: %w", fs.ErrCorrupt)
	}

	bs.rootDirSectors = (bs.rootEntryCount*32 + bs.bytesPerSector - 1) / bs.bytesPerSector
	bs.firstDataSector = bs.reservedSectors + bs.numFATs*bs.fatSize + bs.rootDirSectors

	if bs.firstDataSector > bs.totalSectors {
		return nil, fmt.Errorf("vùng reserved+FAT+root (%d sector) vượt quá tổng dung lượng (%d sector): %w",
			bs.firstDataSector, bs.totalSectors, fs.ErrCorrupt)
	}
	dataSectors := bs.totalSectors - bs.firstDataSector
	bs.totalClusters = dataSectors / bs.sectorsPerCluster

	switch {
	case bs.totalClusters < 4085:
		bs.kind = fs.KindFAT12
	case bs.totalClusters < 65525:
		bs.kind = fs.KindFAT16
	default:
		bs.kind = fs.KindFAT32
		if rootCluster < 2 {
			return nil, fmt.Errorf("FAT32 root cluster không hợp lệ: %d: %w", rootCluster, fs.ErrCorrupt)
		}
		bs.rootCluster = rootCluster
	}

	bs.label = strings.TrimRight(string(labelBytes), " ")
	return bs, nil
}

// validateGeometry chặn các giá trị BPB vô lý (thường do đọc nhầm offset trên
// ảnh hỏng) trước khi chúng được dùng để tính kích thước buffer — tránh cấp
// phát khổng lồ hoặc chia cho 0 ở bước sau.
func validateGeometry(bytesPerSector, sectorsPerCluster, numFATs uint32) error {
	switch bytesPerSector {
	case 512, 1024, 2048, 4096:
	default:
		return fmt.Errorf("BytesPerSector bất thường: %d: %w", bytesPerSector, fs.ErrCorrupt)
	}
	if sectorsPerCluster == 0 || sectorsPerCluster > 128 || sectorsPerCluster&(sectorsPerCluster-1) != 0 {
		return fmt.Errorf("SectorsPerCluster bất thường: %d: %w", sectorsPerCluster, fs.ErrCorrupt)
	}
	if numFATs == 0 || numFATs > 8 {
		return fmt.Errorf("NumFATs bất thường: %d: %w", numFATs, fs.ErrCorrupt)
	}
	return nil
}
