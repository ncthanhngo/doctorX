#!/bin/bash
# Sinh disk image FAT32 / exFAT dùng làm fixture test.
#
# Chạy được KHÔNG cần root: hdiutil attach -nomount cấp /dev/diskN cho file
# image, và newfs_msdos/newfs_exfat ghi lên đó bình thường.
#
# Cây thư mục cố ý gồm: tên tiếng Việt có dấu, tên dài >64 ký tự (ép LFN nhiều
# entry), file rỗng, thư mục lồng nhau, và bộ file bị đánh dấu Hidden+System mô
# phỏng đúng cách virus giấu dữ liệu.
#
#   ./make-images.sh          sinh cả hai image
#   ./make-images.sh fat32    chỉ FAT32
set -euo pipefail

cd "$(dirname "$0")"
SIZE_MB=48

cleanup() {
  [ -n "${MNT:-}" ] && mount | grep -q " $MNT " && umount "$MNT" || true
  [ -n "${DEV:-}" ] && hdiutil detach "$DEV" >/dev/null 2>&1 || true
  [ -n "${MNT:-}" ] && rmdir "$MNT" 2>/dev/null || true
}
trap cleanup EXIT

populate() {
  local mnt=$1
  echo "noi dung file thay duoc" > "$mnt/visible.txt"
  : > "$mnt/empty.bin"

  # Thư mục "nạn nhân" — thứ virus giấu đi và DoctorX phải cứu
  mkdir -p "$mnt/Anh gia dinh/2019"
  echo "anh cuoi" > "$mnt/Anh gia dinh/2019/dam-cuoi.jpg"
  echo "tai lieu quan trong" > "$mnt/Anh gia dinh/ho-so.docx"

  # Tên dài ép LFN trải qua nhiều directory entry
  echo "long" > "$mnt/ten file rat dai de kiem tra long file name tren FAT32 co dung khong.txt"

  # Cây lồng sâu, kiểm tra duyệt đệ quy
  mkdir -p "$mnt/sau/hon/nua/tang/cuoi"
  echo "day cung" > "$mnt/sau/hon/nua/tang/cuoi/day.txt"

  # Mồi cho heuristic phát hiện worm
  mkdir -p "$mnt/System Volume Information"
  echo "khong duoc dung vao" > "$mnt/System Volume Information/tracking.log"

  # Lưu ý: macOS tự thêm .fseventsd/.Spotlight-V100 khi mount, thời điểm không
  # cố định giữa các lần chạy. Không xoá được dứt điểm ở đây (daemon tạo lại),
  # nên test phải tự bỏ qua chúng. File AppleDouble "._*" thì giữ nguyên: USB
  # thật dùng qua Mac luôn có, và bộ lọc nhiễu cần dữ liệu thật để kiểm thử.

  sync
}

make_image() {
  local fmt=$1 img=$2 fstype=$3
  echo "==> $img ($fmt)"
  rm -f "$img"
  dd if=/dev/zero of="$img" bs=1m count=$SIZE_MB 2>/dev/null

  DEV=$(hdiutil attach -nomount "$img" | head -1 | awk '{print $1}')
  if [ "$fmt" = fat32 ]; then
    newfs_msdos -F 32 -v DOCTORX "$DEV" >/dev/null 2>&1
  else
    newfs_exfat -v DOCTORX "$DEV" >/dev/null 2>&1
  fi

  MNT=$(mktemp -d)
  mount -t "$fstype" "$DEV" "$MNT"
  populate "$MNT"
  umount "$MNT"; rmdir "$MNT"; MNT=
  hdiutil detach "$DEV" >/dev/null; DEV=

  # Đánh dấu Hidden+System bằng cách vá thẳng vào image. Cố ý KHÔNG dùng code
  # Go của dự án — fixture phải độc lập với thứ đang được kiểm thử.
  python3 hide-entries.py "$img" "$fmt"
  echo "    xong: $img"
}

case "${1:-all}" in
  fat32) make_image fat32 fat32.img msdos ;;
  exfat) make_image exfat exfat.img exfat ;;
  all)   make_image fat32 fat32.img msdos; make_image exfat exfat.img exfat ;;
  *) echo "dùng: $0 [fat32|exfat|all]" >&2; exit 2 ;;
esac
