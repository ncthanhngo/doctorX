#!/bin/bash
# Cài doctorx-core làm dịch vụ nền chạy quyền root, KHÔNG cần tài khoản Apple
# Developer trả phí.
#
# Dùng LaunchDaemon + launchctl thủ công thay cho SMAppService (vốn đòi app ký
# Developer ID). Cách này chỉ cần chạy một lần với sudo; sau đó dịch vụ tự khởi
# động cùng máy và app DoctorX kết nối được ngay.
#
#   sudo ./install-daemon.sh            cài và khởi động
#   sudo ./install-daemon.sh uninstall  gỡ bỏ
set -euo pipefail

LABEL=com.doctorx.core
PLIST=/Library/LaunchDaemons/$LABEL.plist
BIN=/usr/local/bin/doctorx-core
REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"

if [ "$(id -u)" -ne 0 ]; then
  echo "Cần chạy với sudo: sudo $0 ${1:-}" >&2
  exit 1
fi

# UID của người dùng thật (không phải root) để dịch vụ chỉ chấp nhận kết nối từ
# đúng người đó và đặt journal vào home của họ.
OWNER_UID=$(stat -f%u "$REPO_ROOT")
OWNER_NAME=$(id -un "$OWNER_UID")

uninstall() {
  echo "Gỡ dịch vụ $LABEL..."
  launchctl bootout system "$PLIST" 2>/dev/null || true
  rm -f "$PLIST"
  rm -f "$(dirname "$BIN")/mkntfs" "$(dirname "$BIN")/smartctl"
  echo "Đã gỡ. (Giữ lại $BIN; xoá thủ công nếu muốn.)"
}

if [ "${1:-}" = "uninstall" ]; then
  uninstall
  exit 0
fi

# 1. Cài binary.
if [ ! -f "$REPO_ROOT/build/doctorx-core" ]; then
  echo "Chưa thấy build/doctorx-core — chạy 'make core' trước." >&2
  exit 1
fi
install -m 0755 "$REPO_ROOT/build/doctorx-core" "$BIN"
echo "→ $BIN"

# 1b. Cài các công cụ ngoài (mkntfs cho Format NTFS, smartctl cho SMART) CẠNH
#     core: dịch vụ tìm chúng ngay bên cạnh binary của mình. Không có thì bỏ qua
#     — tính năng tương ứng sẽ báo lỗi rõ ràng khi dùng, phần còn lại vẫn chạy.
BINDIR="$(dirname "$BIN")"
for tool in mkntfs smartctl; do
  if [ -f "$REPO_ROOT/packaging/vendor/$tool" ]; then
    install -m 0755 "$REPO_ROOT/packaging/vendor/$tool" "$BINDIR/$tool"
    echo "→ $BINDIR/$tool"
  else
    echo "⚠ chưa có packaging/vendor/$tool (chạy 'make tools') — bỏ qua"
  fi
done

# 2. Viết LaunchDaemon plist. Truyền -owner-uid để dịch vụ chỉ chấp nhận kết nối
#    từ đúng người dùng này.
cat > "$PLIST" <<PLISTEOF
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key><string>$LABEL</string>
	<key>ProgramArguments</key>
	<array>
		<string>$BIN</string>
		<string>serve</string>
		<string>-owner-uid</string>
		<string>$OWNER_UID</string>
	</array>
	<key>RunAtLoad</key><true/>
	<key>KeepAlive</key><true/>
	<key>EnvironmentVariables</key>
	<dict>
		<key>SUDO_USER</key><string>$OWNER_NAME</string>
	</dict>
</dict>
</plist>
PLISTEOF
chmod 0644 "$PLIST"
echo "→ $PLIST"

# 3. (Nạp lại nếu đã chạy) rồi khởi động.
launchctl bootout system "$PLIST" 2>/dev/null || true
launchctl bootstrap system "$PLIST"
launchctl enable system/$LABEL 2>/dev/null || true

echo
echo "Xong. Dịch vụ đang chạy và sẽ tự khởi động cùng máy."
echo "Kiểm tra:  sudo launchctl print system/$LABEL | grep state"
echo "Gỡ bỏ:     sudo $0 uninstall"
