#!/bin/bash
# Sinh ảnh NTFS có thư mục RẤT lớn và file phân mảnh nặng — để kiểm tra reader
# xử lý đúng khi $INDEX_ALLOCATION hoặc $DATA tràn sang record phụ qua
# $ATTRIBUTE_LIST.
#
# Đây là tình huống của ổ HDD ngoài dùng lâu, phân mảnh nặng — thứ fixture nhỏ
# ntfs.img không tái hiện được.
set -euo pipefail
cd "$(dirname "$0")"

docker run --rm --privileged --device /dev/fuse -v "$PWD:/out" debian:stable-slim bash -euc '
  export DEBIAN_FRONTEND=noninteractive
  apt-get update -qq && apt-get install -y -qq ntfs-3g attr >/dev/null 2>&1

  cd /tmp
  dd if=/dev/zero of=frag.img bs=1M count=200 status=none
  mkntfs -F -q -c 512 -L FRAGTEST frag.img >/dev/null 2>&1   # cluster nhỏ 512 để dễ phân mảnh

  mkdir -p /mnt/n && ntfs-3g frag.img /mnt/n

  # Thư mục rất lớn: 5000 entry, ép index B-tree nhiều tầng INDX.
  mkdir /mnt/n/bigdir
  for i in $(seq 1 5000); do : > "/mnt/n/bigdir/file_$(printf %05d $i).txt"; done

  # Một file bị GIẤU nằm sâu trong thư mục lớn — phải tìm ra được dù nó không
  # nằm ở trang index đầu.
  echo "du lieu quan trong bi giau trong thu muc lon" > "/mnt/n/bigdir/zzz_secret_data.dat"
  setfattr -h -n system.ntfs_attrib_be -v 0x00000006 "/mnt/n/bigdir/zzz_secret_data.dat"

  # Thư mục lớn thứ hai cũng bị giấu (test unhide đệ quy trên dir lớn).
  mkdir "/mnt/n/An mat"
  for i in $(seq 1 3000); do : > "/mnt/n/An mat/muc_$(printf %04d $i).bin"; done
  setfattr -h -n system.ntfs_attrib_be -v 0x00000016 "/mnt/n/An mat"

  # File phân mảnh nặng: tạo lỗ rồi lấp bằng file lớn.
  for i in $(seq 1 400); do dd if=/dev/urandom of="/mnt/n/pad_$i" bs=64k count=1 status=none; done
  for i in $(seq 2 2 400); do rm -f "/mnt/n/pad_$i"; done
  dd if=/dev/urandom of="/mnt/n/fragmented.bin" bs=64k count=180 status=none
  echo "noi dung thay duoc" > "/mnt/n/visible.txt"

  sync; umount /mnt/n
  cp frag.img /out/ntfs-fragmented.img
'
echo "→ $PWD/ntfs-fragmented.img"
