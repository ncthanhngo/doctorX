"""Đánh dấu Hidden+System lên một số entry trong disk image FAT32/exFAT.

Mô phỏng đúng cách virus giấu dữ liệu trên USB. Cố ý viết bằng Python, độc lập
hoàn toàn với code Go đang được kiểm thử — fixture không được dùng chung cài đặt
với thứ nó dùng để kiểm tra.

    python3 hide-entries.py <image> <fat32|exfat>
"""
import mmap
import sys

ATTR_HIDDEN = 0x02
ATTR_SYSTEM = 0x04

# Các mục sẽ bị giấu, khớp cây do make-images.sh dựng.
FAT_TARGETS = [b'ANHGIA~1   ', b'SAU        ', b'VISIBLE TXT']
EXFAT_TARGETS = ['Anh gia dinh', 'sau', 'visible.txt']


def hide_fat(m):
    """FAT: attribute là 1 byte tại offset 11 của directory entry 32 byte."""
    n = 0
    for name in FAT_TARGETS:
        i = m.find(name)
        if i < 0:
            print(f'  [bỏ qua] không thấy {name!r}')
            continue
        m[i + 11] |= ATTR_HIDDEN | ATTR_SYSTEM
        print(f'  {name.decode():12s} -> attr 0x{m[i + 11]:02x}')
        n += 1
    return n


def exfat_set_checksum(m, start, secondary_count):
    """Tính lại SetChecksum cho một entry set exFAT.

    Checksum chạy trên toàn bộ byte của mọi entry trong set, BỎ QUA byte 2 và 3
    của entry đầu (chính là trường SetChecksum). Sai bước này thì Windows và
    macOS đều coi entry hỏng và bỏ qua nó.
    """
    total = (secondary_count + 1) * 32
    csum = 0
    for idx in range(total):
        if idx in (2, 3):
            continue
        csum = (((csum << 15) | (csum >> 1)) + m[start + idx]) & 0xFFFF
    m[start + 2] = csum & 0xFF
    m[start + 3] = (csum >> 8) & 0xFF
    return csum


def hide_exfat(m):
    """exFAT: attribute là u16 tại offset 4 của File entry (type 0x85).

    Entry set = File(0x85) + Stream(0xC0) + Name(0xC1)... nên phải dò từ entry
    tên ngược về đầu set, sửa attribute rồi tính lại checksum cho cả set.
    """
    n = 0
    for name in EXFAT_TARGETS:
        i = m.find(name.encode('utf-16-le'))
        if i < 0:
            print(f'  [bỏ qua] không thấy {name!r}')
            continue
        start = (i // 32) * 32
        while start > 0 and m[start] != 0x85:
            start -= 32
        if m[start] != 0x85:
            print(f'  [bỏ qua] không tìm được File entry của {name!r}')
            continue

        secondary = m[start + 1]
        attr = int.from_bytes(m[start + 4:start + 6], 'little')
        attr |= ATTR_HIDDEN | ATTR_SYSTEM
        m[start + 4:start + 6] = attr.to_bytes(2, 'little')
        csum = exfat_set_checksum(m, start, secondary)
        print(f'  {name:12s} -> attr 0x{attr:04x}, SetChecksum 0x{csum:04x}')
        n += 1
    return n


def main():
    if len(sys.argv) != 3:
        print(__doc__)
        return 2
    path, fmt = sys.argv[1], sys.argv[2]
    with open(path, 'r+b') as f:
        m = mmap.mmap(f.fileno(), 0)
        n = hide_fat(m) if fmt == 'fat32' else hide_exfat(m)
        m.flush()
    print(f'  đã giấu {n} mục')
    return 0


if __name__ == '__main__':
    sys.exit(main())
