#!/bin/bash
# Sinh ảnh NTFS làm fixture test.
#
# macOS không có mkntfs nên phải mượn một container Linux có ntfs-3g. Cần
# --privileged --device /dev/fuse để ntfs-3g mount được bên trong container.
#
# Cây thư mục khớp fat/exfat để test dùng chung kỳ vọng, cộng thêm các ca đặc
# thù NTFS: file phân mảnh (non-resident nhiều data run), tên Unicode, và một
# Alternate Data Stream (nơi worm hay giấu payload).
#
# Ảnh sinh ra được commit vào repo vì không tái tạo được trên máy không có
# Docker. Chạy lại script này khi cần đổi nội dung fixture.
set -euo pipefail
cd "$(dirname "$0")"

docker run --rm --privileged --device /dev/fuse -v "$PWD:/out" debian:stable-slim bash -euc '
  export DEBIAN_FRONTEND=noninteractive
  apt-get update -qq
  apt-get install -y -qq ntfs-3g fuse3 attr >/dev/null 2>&1

  cd /tmp
  dd if=/dev/zero of=ntfs.img bs=1M count=48 status=none
  mkntfs -F -q -L DOCTORX ntfs.img >/dev/null 2>&1

  mkdir -p /mnt/n
  ntfs-3g ntfs.img /mnt/n

  echo "noi dung file thay duoc" > /mnt/n/visible.txt
  : > /mnt/n/empty.bin

  mkdir -p "/mnt/n/Anh gia dinh/2019"
  echo "anh cuoi" > "/mnt/n/Anh gia dinh/2019/dam-cuoi.jpg"
  echo "tai lieu quan trong" > "/mnt/n/Anh gia dinh/ho-so.docx"

  echo long > "/mnt/n/ten file rat dai de kiem tra long file name tren NTFS co dung khong.txt"

  mkdir -p "/mnt/n/sau/hon/nua/tang/cuoi"
  echo "day cung" > "/mnt/n/sau/hon/nua/tang/cuoi/day.txt"

  mkdir -p "/mnt/n/System Volume Information"
  echo "khong duoc dung vao" > "/mnt/n/System Volume Information/tracking.log"

  # File lớn ép phân mảnh: non-resident, nhiều data run, có thể kéo theo
  # $ATTRIBUTE_LIST — đúng thứ reader phải xử lý đúng.
  dd if=/dev/urandom of="/mnt/n/big.bin" bs=1M count=8 status=none

  # Alternate Data Stream trên một file thấy được.
  printf "payload an trong ADS" > "/mnt/n/visible.txt:hidden_stream"

  # Đánh dấu Hidden+System bằng attribute NTFS thật (0x02|0x04). ntfs-3g phơi
  # ra qua xattr system.ntfs_attrib_be (big-endian, 4 byte).
  for t in "/mnt/n/Anh gia dinh" "/mnt/n/sau" "/mnt/n/visible.txt"; do
    setfattr -h -n system.ntfs_attrib_be -v 0x00000006 "$t"
  done

  sync
  umount /mnt/n
  cp ntfs.img /out/ntfs.img
'
echo "→ $PWD/ntfs.img"
