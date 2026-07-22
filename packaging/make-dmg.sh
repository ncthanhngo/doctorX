#!/bin/bash
# Đóng gói DoctorX.app thành một DMG kéo-thả để cài vào /Applications.
#
# App ký ad-hoc (chưa có Developer ID / notarize), nên DMG này dùng để CÀI NỘI BỘ
# trên máy tự build. Máy khác tải về sẽ bị Gatekeeper chặn cho tới khi ký + notarize.
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
APP="${REPO_ROOT}/build/DoctorX.app"
DMG="${REPO_ROOT}/build/DoctorX.dmg"
VOLNAME="DoctorX"

if [ ! -d "${APP}" ]; then
  echo "Chưa thấy ${APP} — chạy 'make app' trước." >&2
  exit 1
fi

STAGE="$(mktemp -d)"
trap 'rm -rf "${STAGE}"' EXIT

# Nội dung cửa sổ DMG: app + lối tắt /Applications để kéo-thả.
cp -R "${APP}" "${STAGE}/"
ln -s /Applications "${STAGE}/Applications"

rm -f "${DMG}"
hdiutil create -volname "${VOLNAME}" -srcfolder "${STAGE}" \
  -ov -format UDZO "${DMG}" >/dev/null

echo "→ ${DMG}"
du -h "${DMG}" | awk '{print $1}'
