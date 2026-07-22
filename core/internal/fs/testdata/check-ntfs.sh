#!/bin/bash
# Kiểm tra toàn vẹn một ảnh NTFS bằng công cụ ntfs-3g trong Docker.
#
# Đây là oracle ĐỘC LẬP với code Go của DoctorX, đóng vai trò thay cho "cắm sang
# Windows chạy chkdsk" mà ta không làm được trên máy macOS. Sau khi DoctorX ghi
# vào ảnh, chạy script này: nếu ntfs-3g mount được, ntfsinfo không báo lỗi cấu
# trúc, và đọc lại được file ở đường dẫn cần kiểm tra thì thay đổi là an toàn.
#
#   ./check-ntfs.sh <image> [đường/dẫn/trong/ổ để đọc thử]
set -euo pipefail
IMG=${1:?dùng: ./check-ntfs.sh <image> [đường-dẫn-đọc-thử]}
PROBE=${2:-}
IMG_ABS=$(cd "$(dirname "$IMG")" && pwd)/$(basename "$IMG")

docker run --rm --privileged --device /dev/fuse -v "$IMG_ABS:/img.ntfs" debian:stable-slim bash -c '
  set -e
  export DEBIAN_FRONTEND=noninteractive
  apt-get update -qq >/dev/null 2>&1
  apt-get install -y -qq ntfs-3g >/dev/null 2>&1

  echo "=== ntfsinfo -m (thông tin volume, kiểm tra boot sector + $MFT) ==="
  ntfsinfo -m /img.ntfs 2>&1 | grep -Ei "cluster size|volume name|mft|dirty|version" || true

  echo "=== ntfsck / kiểm tra nhất quán (chỉ đọc) ==="
  # ntfsfix -n: kiểm tra và BÁO CÁO, không sửa gì (-n = no action).
  if ntfsfix -n /img.ntfs 2>&1 | tee /tmp/fix.log | grep -Eiq "corrupt|error|inconsist/|failed"; then
    echo "!!! ntfsfix phát hiện vấn đề:"; cat /tmp/fix.log; exit 1
  fi
  cat /tmp/fix.log

  echo "=== mount thử + đọc ==="
  mkdir -p /mnt/n
  ntfs-3g -o ro /img.ntfs /mnt/n
  PROBE="'"$PROBE"'"
  if [ -n "$PROBE" ]; then
    echo "attrib của $PROBE:"
    getfattr -h -n system.ntfs_attrib_be --only-values "/mnt/n/$PROBE" 2>/dev/null | od -An -tx1 | tr -d " \n" || true
    echo
  fi
  echo "cây gốc:"; ls -la /mnt/n | grep -v "^total" | awk "{print \$1, \$NF}"
  umount /mnt/n
  echo "=== OK: ảnh mount được và không có lỗi cấu trúc ==="
'
