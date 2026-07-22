package ntfs

import (
	"encoding/binary"
	"fmt"

	"github.com/soi/doctorx/core/internal/fs"
)

// attrListEntrySize là kích thước cố định phần đầu một entry trong nội dung
// $ATTRIBUTE_LIST, trước phần tên (nếu có).
const attrListEntryFixedSize = 0x1A

// findAttrDeep tìm attribute typ trong rec; nếu không thấy trực tiếp và rec
// có $ATTRIBUTE_LIST (0x20), tra theo danh sách đó sang record MFT phụ chứa
// attribute (trường hợp record gốc không đủ chỗ chứa hết attribute, thường
// gặp ở file phân mảnh nặng hoặc nhiều Alternate Data Stream).
//
// $ATTRIBUTE_LIST bản thân nó cũng có thể non-resident; trường hợp đó không
// được hỗ trợ đầy đủ ở đây — trả về không tìm thấy thay vì panic, đúng yêu
// cầu "không panic, log rõ" khi gặp cấu trúc phức tạp ngoài phạm vi rescue
// attribute cơ bản.
func (v *Volume) findAttrDeep(rec *mftRecord, typ uint32) (attrHeader, *mftRecord, bool, error) {
	if a, ok := findAttr(rec, typ); ok {
		return a, rec, true, nil
	}
	listAttr, ok := findAttr(rec, attrTypeAttrList)
	if !ok {
		return attrHeader{}, nil, false, nil
	}
	if listAttr.NonResident {
		// $ATTRIBUTE_LIST non-resident: không đọc runlist riêng cho nó ở đây.
		return attrHeader{}, nil, false, nil
	}
	content := listAttr.Content(rec.Raw)
	pos := 0
	for pos+attrListEntryFixedSize <= len(content) {
		entryType := binary.LittleEndian.Uint32(content[pos : pos+4])
		entryLen := binary.LittleEndian.Uint16(content[pos+4 : pos+6])
		if entryLen < attrListEntryFixedSize || pos+int(entryLen) > len(content) {
			return attrHeader{}, nil, false, fmt.Errorf("%w: $ATTRIBUTE_LIST hỏng tại offset %d của record %d", fs.ErrCorrupt, pos, rec.Num)
		}
		if entryType == typ {
			baseRef := binary.LittleEndian.Uint64(content[pos+0x10 : pos+0x18])
			targetNum := baseRef & mftRefRecordMask
			target := rec
			if targetNum != rec.Num {
				other, err := v.readRecord(targetNum)
				if err != nil {
					return attrHeader{}, nil, false, err
				}
				target = other
			}
			if a, ok := findAttr(target, typ); ok {
				return a, target, true, nil
			}
		}
		pos += int(entryLen)
	}
	return attrHeader{}, nil, false, nil
}

// fragment là một mảnh runlist của một attribute non-resident, kèm VCN bắt đầu.
type fragment struct {
	startVCN uint64
	runs     []dataRun
}

// collectNonResidentRuns gom TẤT CẢ mảnh của một attribute non-resident kiểu
// typ và nối chúng thành một runlist liên tục theo thứ tự VCN.
//
// Khi một attribute (điển hình là $INDEX_ALLOCATION của thư mục lớn, hoặc $DATA
// của file phân mảnh nặng) không đủ chỗ trong một record, NTFS chẻ nó thành
// nhiều mảnh nằm ở các record MFT phụ, liên kết qua $ATTRIBUTE_LIST. Mỗi mảnh
// mô tả một dải VCN riêng. Chỉ đọc mảnh ở record gốc (như findAttr) sẽ thiếu
// phần đuôi và ánh xạ VCN lớn sẽ trượt runlist.
//
// Trả về runlist đã nối (bắt đầu từ VCN 0) và ok=false nếu attribute không tồn
// tại hoặc là resident.
func (v *Volume) collectNonResidentRuns(baseRec *mftRecord, typ uint32) ([]dataRun, bool, error) {
	clusterSize := int64(v.boot.ClusterSize())

	// Thu thập mảnh từ chính record gốc.
	var frags []fragment
	addFromRecord := func(rec *mftRecord) error {
		for _, a := range rec.Attrs {
			if a.Type != typ || !a.NonResident {
				continue
			}
			runs, err := decodeRunList(a.RunListBytes(rec.Raw))
			if err != nil {
				return fmt.Errorf("%w: giải mã runlist mảnh (record %d, VCN %d): %v", fs.ErrCorrupt, rec.Num, a.StartVCN, err)
			}
			frags = append(frags, fragment{startVCN: a.StartVCN, runs: runs})
		}
		return nil
	}
	if err := addFromRecord(baseRec); err != nil {
		return nil, false, err
	}

	// Nếu có $ATTRIBUTE_LIST, đi theo để lấy các mảnh ở record phụ.
	if listAttr, ok := findAttr(baseRec, attrTypeAttrList); ok {
		content, err := v.attrListContent(baseRec, listAttr, clusterSize)
		if err != nil {
			return nil, false, err
		}
		seen := map[uint64]bool{baseRec.Num: true}
		pos := 0
		for pos+attrListEntryFixedSize <= len(content) {
			entryType := binary.LittleEndian.Uint32(content[pos : pos+4])
			entryLen := binary.LittleEndian.Uint16(content[pos+4 : pos+6])
			if entryLen < attrListEntryFixedSize || pos+int(entryLen) > len(content) {
				return nil, false, fmt.Errorf("%w: $ATTRIBUTE_LIST hỏng tại offset %d của record %d", fs.ErrCorrupt, pos, baseRec.Num)
			}
			if entryType == typ {
				targetNum := binary.LittleEndian.Uint64(content[pos+0x10:pos+0x18]) & mftRefRecordMask
				if !seen[targetNum] {
					seen[targetNum] = true
					other, err := v.readRecord(targetNum)
					if err != nil {
						return nil, false, err
					}
					if err := addFromRecord(other); err != nil {
						return nil, false, err
					}
				}
			}
			pos += int(entryLen)
		}
	}

	if len(frags) == 0 {
		return nil, false, nil
	}

	// Sắp theo VCN và nối. NTFS bảo đảm các mảnh phủ liên tục 0..N; kiểm tra
	// tính liên tục để bắt cấu trúc hỏng thay vì ánh xạ sai âm thầm.
	sortFragments(frags)
	if frags[0].startVCN != 0 {
		return nil, false, fmt.Errorf("%w: mảnh đầu của attribute 0x%X bắt đầu tại VCN %d, không phải 0", fs.ErrCorrupt, typ, frags[0].startVCN)
	}
	var merged []dataRun
	var nextVCN uint64
	for _, f := range frags {
		if f.startVCN != nextVCN {
			return nil, false, fmt.Errorf("%w: mảnh runlist không liên tục (chờ VCN %d, gặp %d)", fs.ErrCorrupt, nextVCN, f.startVCN)
		}
		for _, r := range f.runs {
			merged = append(merged, r)
			nextVCN += r.Length
		}
	}
	return merged, true, nil
}

// attrListContent trả về nội dung $ATTRIBUTE_LIST, đọc từ đĩa nếu nó non-resident.
func (v *Volume) attrListContent(rec *mftRecord, listAttr attrHeader, clusterSize int64) ([]byte, error) {
	if !listAttr.NonResident {
		return listAttr.Content(rec.Raw), nil
	}
	runs, err := decodeRunList(listAttr.RunListBytes(rec.Raw))
	if err != nil {
		return nil, fmt.Errorf("%w: giải mã runlist $ATTRIBUTE_LIST của record %d: %v", fs.ErrCorrupt, rec.Num, err)
	}
	size := int64(listAttr.ContentLength)
	buf := make([]byte, size)
	var done int64
	for done < size {
		absOff, err := mapStreamOffset(runs, listAttr.StartVCN, clusterSize, done)
		if err != nil {
			return nil, err
		}
		n := clusterSize - (done % clusterSize)
		if done+n > size {
			n = size - done
		}
		if _, err := v.rd.ReadAt(buf[done:done+n], absOff); err != nil {
			return nil, fmt.Errorf("%w: đọc $ATTRIBUTE_LIST non-resident: %v", fs.ErrCorrupt, err)
		}
		done += n
	}
	return buf, nil
}

// sortFragments sắp xếp mảnh theo startVCN tăng dần (insertion sort — số mảnh
// luôn nhỏ, vài chục là nhiều).
func sortFragments(frags []fragment) {
	for i := 1; i < len(frags); i++ {
		for j := i; j > 0 && frags[j-1].startVCN > frags[j].startVCN; j-- {
			frags[j-1], frags[j] = frags[j], frags[j-1]
		}
	}
}
