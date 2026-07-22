package exfat

// SetChecksum tính SetChecksum của một entry set exFAT (trường 2 byte tại
// offset 2-3 của File Directory Entry chính, mô tả 0x85).
//
// Chạy trên toàn bộ byte của mọi entry trong set (kể cả entry chính), BỎ QUA
// hai byte tại index 2 và 3 (chính là trường SetChecksum, không tự tham chiếu
// chính nó). Với mỗi byte: rotate right 1 bit trên số 16-bit rồi cộng byte đó
// vào — sai công thức này thì cả macOS lẫn Windows coi entry hỏng và file sẽ
// biến mất khỏi thư mục.
//
// Tham chiếu: internal/fs/testdata/hide-entries.py, hàm exfat_set_checksum —
// cài đặt song song độc lập bằng Python dùng để tạo fixture test.
func SetChecksum(entrySet []byte) uint16 {
	var csum uint16
	for i, b := range entrySet {
		if i == 2 || i == 3 {
			continue
		}
		// csum<<15 | csum>>1 là rotate-right 1 bit trên số 16-bit; kiểu uint16
		// tự tràn số (mod 2^16) nên không cần mask thủ công như bản Python.
		csum = (csum<<15 | csum>>1) + uint16(b)
	}
	return csum
}
