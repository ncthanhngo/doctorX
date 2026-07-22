package ntfs

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"unicode/utf16"

	"github.com/soi/doctorx/core/internal/fs"
)

// indexEntryHeaderSize là kích thước phần đầu cố định của một index entry
// (MFT reference 8 + entry length 2 + key length 2 + flags 2 + 2 byte đệm).
const indexEntryHeaderSize = 16

// Cờ trong index entry flags (offset 0x0C của entry).
const (
	indexEntryHasSubnode = 0x01
	indexEntryLast       = 0x02 // entry cuối, không có key
)

// indexKeyMinLen là độ dài tối thiểu của một key $FILE_NAME trong index entry
// để đọc được namespace (0x41) và NameLength (0x40).
const indexKeyMinLen = fnNameOffset

// indexHeaderFixedSize là kích thước phần "INDEX_HEADER" (entriesOffset,
// indexLength, allocatedSize, flags) xuất hiện cả trong $INDEX_ROOT (sau 16
// byte header riêng) lẫn trong mỗi INDX block (sau 24 byte header riêng).
const indexHeaderFixedSize = 16

// indxBlockHeaderSize là phần cố định đầu một INDX block: magic(4) +
// USAOffset(2) + USACount(2) + LSN(8) + VCN của chính block này(8).
const indxBlockHeaderSize = 24

var indxMagic = []byte("INDX")

// iterateIndexForDir duyệt B-tree $I30 của thư mục dirNum (DFS theo thứ tự
// lưu trữ, không cần đúng thứ tự collation vì chỉ cần liệt kê đủ), gọi fn cho
// mỗi entry có key với (mftNum, tên, offset tuyệt đối của trường FileAttributes
// trong bản copy $FILE_NAME nằm ngay trong entry đó — dùng làm EntryLoc thứ 3).
func (v *Volume) iterateIndexForDir(ctx context.Context, dirNum uint64, fn func(mftNum uint64, name string, attrKeyOffset int64) error) error {
	dirRec, err := v.readRecord(dirNum)
	if err != nil {
		return err
	}
	if !dirRec.isDir() {
		return fmt.Errorf("%w: MFT record %d không phải thư mục", fs.ErrCorrupt, dirNum)
	}
	rootAttr, ok := findAttr(dirRec, attrTypeIndexRoot)
	if !ok {
		return fmt.Errorf("%w: thư mục (record %d) thiếu $INDEX_ROOT", fs.ErrCorrupt, dirNum)
	}
	if rootAttr.NonResident {
		return fmt.Errorf("%w: $INDEX_ROOT non-resident bất thường tại record %d", fs.ErrCorrupt, dirNum)
	}
	content := rootAttr.Content(dirRec.Raw)
	if len(content) < 16+indexHeaderFixedSize {
		return fmt.Errorf("%w: nội dung $INDEX_ROOT quá ngắn tại record %d", fs.ErrCorrupt, dirNum)
	}
	indexRecSize := binary.LittleEndian.Uint32(content[8:12])

	entriesOffset := binary.LittleEndian.Uint32(content[16:20])
	indexLength := binary.LittleEndian.Uint32(content[20:24])
	if uint32(16)+indexLength > uint32(len(content)) || entriesOffset > indexLength {
		return fmt.Errorf("%w: header $INDEX_ROOT bất thường tại record %d", fs.ErrCorrupt, dirNum)
	}
	entriesBuf := content[16+entriesOffset : 16+indexLength]
	baseAbs := dirRec.Offset + int64(rootAttr.recOff) + int64(rootAttr.ContentOffset) + int64(16+entriesOffset)

	// $INDEX_ALLOCATION chỉ cần giải mã khi thực sự gặp entry có sub-node —
	// thư mục nhỏ toàn bộ entry nằm resident trong $INDEX_ROOT, không cần đọc.
	var allocRuns []dataRun
	var allocStartVCN uint64
	allocResolved := false
	resolveAlloc := func() error {
		if allocResolved {
			return nil
		}
		allocResolved = true
		// Gom mọi mảnh $INDEX_ALLOCATION: thư mục lớn có index phân mảnh qua
		// nhiều record phụ (liên kết bởi $ATTRIBUTE_LIST). Chỉ đọc mảnh ở record
		// gốc sẽ thiếu phần đuôi và trượt runlist khi ánh xạ VCN lớn.
		runs, ok, err := v.collectNonResidentRuns(dirRec, attrTypeIndexAlloc)
		if err != nil {
			return err
		}
		if !ok {
			return fmt.Errorf("%w: thư mục record %d thiếu $INDEX_ALLOCATION dù index có sub-node", fs.ErrCorrupt, dirNum)
		}
		allocRuns = runs
		allocStartVCN = 0 // runlist đã nối bắt đầu từ VCN 0
		return nil
	}

	clusterSize := int64(v.boot.ClusterSize())
	// Đơn vị của sub-node VCN: cluster khi index-record >= cluster (trường hợp
	// thường), ngược lại là index-record. Công thức chung là min(cluster,
	// index-record). Fixture nhỏ dùng cluster 4096 = index-record nên hai giá
	// trị trùng và giấu lỗi; ổ format cluster 512 mới lộ ra.
	vcnUnit := clusterSize
	if int64(indexRecSize) < vcnUnit {
		vcnUnit = int64(indexRecSize)
	}

	readIndexBlock := func(vcn uint64) ([]byte, int64, error) {
		if err := resolveAlloc(); err != nil {
			return nil, 0, err
		}
		streamOff := int64(vcn) * vcnUnit
		// Đọc từng cluster qua runlist: một INDX block có thể trải nhiều cluster
		// KHÔNG liền nhau trên đĩa khi index bị phân mảnh, nên không thể đọc một
		// mạch từ offset đầu.
		buf := make([]byte, indexRecSize)
		firstAbs := int64(0)
		for done := int64(0); done < int64(indexRecSize); {
			absOff, err := mapStreamOffset(allocRuns, allocStartVCN, clusterSize, streamOff+done)
			if err != nil {
				return nil, 0, fmt.Errorf("%w: ánh xạ VCN chỉ mục %d của thư mục record %d: %v", fs.ErrCorrupt, vcn, dirNum, err)
			}
			if done == 0 {
				firstAbs = absOff
			}
			n := clusterSize - ((streamOff + done) % clusterSize)
			if done+n > int64(indexRecSize) {
				n = int64(indexRecSize) - done
			}
			if _, err := v.rd.ReadAt(buf[done:done+n], absOff); err != nil {
				return nil, 0, fmt.Errorf("%w: đọc INDX block tại offset %d: %v", fs.ErrCorrupt, absOff, err)
			}
			v.declareRange(absOff, absOff+n, "ntfs-indx")
			done += n
		}
		if !bytes.Equal(buf[0:4], indxMagic) {
			return nil, 0, fmt.Errorf("%w: INDX block VCN %d thiếu chữ ký INDX", fs.ErrCorrupt, vcn)
		}
		if err := applyFixup(buf, v.boot.BytesPerSector); err != nil {
			return nil, 0, fmt.Errorf("%w: fixup INDX block VCN %d: %v", fs.ErrCorrupt, vcn, err)
		}
		return buf, firstAbs, nil
	}

	var walk func(buf []byte, baseAbs int64, depth int) error
	walk = func(buf []byte, baseAbs int64, depth int) error {
		if depth > fs.MaxPathDepth {
			return fs.ErrDepthLimit
		}
		pos := 0
		for pos+indexEntryHeaderSize <= len(buf) {
			select {
			case <-ctx.Done():
				return ctx.Err()
			default:
			}

			mftref := binary.LittleEndian.Uint64(buf[pos : pos+8])
			entryLen := binary.LittleEndian.Uint16(buf[pos+8 : pos+10])
			keyLen := binary.LittleEndian.Uint16(buf[pos+10 : pos+12])
			eflags := binary.LittleEndian.Uint16(buf[pos+12 : pos+14])
			if entryLen < indexEntryHeaderSize || pos+int(entryLen) > len(buf) {
				return fmt.Errorf("%w: entry length bất thường (%d) trong chỉ mục", fs.ErrCorrupt, entryLen)
			}

			hasKey := eflags&indexEntryLast == 0
			hasSub := eflags&indexEntryHasSubnode != 0

			if hasKey {
				if int(indexEntryHeaderSize)+int(keyLen) > int(entryLen) {
					return fmt.Errorf("%w: key length (%d) vượt quá entry length (%d) trong chỉ mục", fs.ErrCorrupt, keyLen, entryLen)
				}
				key := buf[pos+indexEntryHeaderSize : pos+indexEntryHeaderSize+int(keyLen)]
				if len(key) < indexKeyMinLen {
					return fmt.Errorf("%w: key $FILE_NAME trong chỉ mục quá ngắn (%d byte)", fs.ErrCorrupt, len(key))
				}
				namespace := key[fnNamespaceOff]
				nameLen := int(key[fnNameLenOffset])
				nameEnd := fnNameOffset + nameLen*2
				if nameEnd > len(key) {
					return fmt.Errorf("%w: tên trong key chỉ mục vượt quá key (dài %d ký tự, key %d byte)", fs.ErrCorrupt, nameLen, len(key))
				}
				// Bỏ qua bản DOS 8.3 thuần: mọi file luôn có kèm một bản
				// Win32/Win32+DOS/POSIX trỏ tới cùng mftNum, xử lý bản đó
				// tương ứng với entry chỉ mục còn lại là đủ, tránh liệt kê
				// trùng hai path cho một file.
				mftNum := mftref & mftRefRecordMask
				// Root directory tự tham chiếu chính nó qua một entry "."
				// trong $I30 của chính nó (mftNum == dirNum) — đặc thù NTFS,
				// không xuất hiện ở thư mục con. Bỏ qua để không đệ quy vô
				// hạn ở walkDir.
				if namespace != nsDOS && mftNum != dirNum {
					units := make([]uint16, nameLen)
					for i := 0; i < nameLen; i++ {
						units[i] = binary.LittleEndian.Uint16(key[fnNameOffset+i*2 : fnNameOffset+i*2+2])
					}
					name := string(utf16.Decode(units))
					attrKeyOffset := baseAbs + int64(pos) + indexEntryHeaderSize + fnAttrOffset
					if err := fn(mftNum, name, attrKeyOffset); err != nil {
						return err
					}
				}
			}

			if hasSub {
				if pos+int(entryLen) < 8 {
					return fmt.Errorf("%w: entry có sub-node nhưng không đủ chỗ chứa VCN", fs.ErrCorrupt)
				}
				vcn := binary.LittleEndian.Uint64(buf[pos+int(entryLen)-8 : pos+int(entryLen)])
				childBuf, childAbs, err := readIndexBlock(vcn)
				if err != nil {
					return err
				}
				if len(childBuf) < indxBlockHeaderSize+indexHeaderFixedSize {
					return fmt.Errorf("%w: INDX block tại offset %d quá ngắn", fs.ErrCorrupt, childAbs)
				}
				childEntriesOffset := binary.LittleEndian.Uint32(childBuf[indxBlockHeaderSize : indxBlockHeaderSize+4])
				childIndexLength := binary.LittleEndian.Uint32(childBuf[indxBlockHeaderSize+4 : indxBlockHeaderSize+8])
				if uint32(indxBlockHeaderSize)+childIndexLength > uint32(len(childBuf)) || childEntriesOffset > childIndexLength {
					return fmt.Errorf("%w: header INDX block tại offset %d bất thường", fs.ErrCorrupt, childAbs)
				}
				childEntries := childBuf[indxBlockHeaderSize+childEntriesOffset : indxBlockHeaderSize+childIndexLength]
				childBase := childAbs + int64(indxBlockHeaderSize+childEntriesOffset)
				if err := walk(childEntries, childBase, depth+1); err != nil {
					return err
				}
			}

			if eflags&indexEntryLast != 0 {
				break
			}
			pos += int(entryLen)
		}
		return nil
	}

	return walk(entriesBuf, baseAbs, 0)
}
