#!/bin/bash
# Build một binary `mkntfs` UNIVERSAL (arm64 + x86_64) từ mã nguồn ntfs-3g để
# đóng gói kèm DoctorX.app. macOS không có tool format NTFS, nên tính năng
# "Format → NTFS" của core gọi tới binary này (xem core/internal/imaging/format_ntfs.go).
#
# Kết quả: packaging/vendor/mkntfs
#
# ntfs-3g là GPLv3 — khi phân phối DoctorX.app kèm mkntfs phải kèm giấy phép và
# chào mã nguồn (xem packaging/vendor/README sau khi build).
#
# Yêu cầu: Xcode Command Line Tools (clang, lipo), curl, tar, make.
# Chạy trên macOS (Apple Silicon hoặc Intel — script tự cross-build cả hai arch).
set -euo pipefail

VERSION="2022.10.3"
TARBALL="ntfs-3g_ntfsprogs-${VERSION}.tgz"
URL="https://tuxera.com/opensource/${TARBALL}"
# Để trống = script in sha256 tải về rồi tiếp tục (có cảnh báo). Điền giá trị vào
# đây để khoá supply-chain: build sẽ dừng nếu hash không khớp.
EXPECT_SHA256="f20e36ee68074b845e3629e6bced4706ad053804cbaf062fbae60738f854170c"

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
VENDOR="${HERE}/vendor"
WORK="$(mktemp -d)"
trap 'rm -rf "${WORK}"' EXIT

echo "→ Tải ${URL}"
curl -fsSL "${URL}" -o "${WORK}/${TARBALL}"

GOT_SHA="$(shasum -a 256 "${WORK}/${TARBALL}" | awk '{print $1}')"
if [[ -n "${EXPECT_SHA256}" ]]; then
	if [[ "${GOT_SHA}" != "${EXPECT_SHA256}" ]]; then
		echo "✗ sha256 không khớp!" >&2
		echo "  kỳ vọng: ${EXPECT_SHA256}" >&2
		echo "  nhận:    ${GOT_SHA}" >&2
		exit 1
	fi
	echo "✓ sha256 khớp"
else
	echo "⚠ EXPECT_SHA256 để trống — sha256 tải về: ${GOT_SHA}"
	echo "  Nên điền giá trị này vào script để khoá supply-chain."
fi

tar -xzf "${WORK}/${TARBALL}" -C "${WORK}"
SRC="${WORK}/ntfs-3g_ntfsprogs-${VERSION}"

# Build mkntfs cho một kiến trúc, in đường dẫn Mach-O ra stdout.
build_arch() {
	local arch="$1" host="$2"
	local out="${WORK}/build-${arch}"
	cp -R "${SRC}" "${out}"
	(
		cd "${out}"
		# --without-fuse: chỉ cần ntfsprogs, không cần driver mount.
		# --disable-shared: link tĩnh libntfs-3g để binary tự chứa, không phụ thuộc dylib.
		CC="clang -arch ${arch}" \
		./configure --host="${host}" \
			--without-fuse --disable-shared --enable-static \
			--disable-plugins --quiet >/dev/null
		make -C libntfs-3g >/dev/null
		make -C ntfsprogs mkntfs >/dev/null
	)
	# libtool có thể để binary thật trong .libs/
	if [[ -f "${out}/ntfsprogs/.libs/mkntfs" ]]; then
		echo "${out}/ntfsprogs/.libs/mkntfs"
	else
		echo "${out}/ntfsprogs/mkntfs"
	fi
}

echo "→ Build arm64"
ARM64_BIN="$(build_arch arm64 aarch64-apple-darwin)"
echo "→ Build x86_64"
AMD64_BIN="$(build_arch x86_64 x86_64-apple-darwin)"

mkdir -p "${VENDOR}"
lipo -create -output "${VENDOR}/mkntfs" "${ARM64_BIN}" "${AMD64_BIN}"
strip -S "${VENDOR}/mkntfs" 2>/dev/null || true
chmod +x "${VENDOR}/mkntfs"

# Kèm giấy phép để tuân thủ GPLv3 khi phân phối.
cp "${SRC}/COPYING" "${VENDOR}/mkntfs.COPYING" 2>/dev/null || true
cat > "${VENDOR}/README" <<EOF
mkntfs — build từ ntfs-3g_ntfsprogs ${VERSION} (${URL}).
Giấy phép: GPLv3 (xem mkntfs.COPYING). Khi phân phối DoctorX.app kèm binary này,
phải kèm giấy phép và chào mã nguồn theo GPLv3.
Sinh lại bằng: packaging/build-mkntfs.sh
EOF

echo
echo "✓ ${VENDOR}/mkntfs"
lipo -info "${VENDOR}/mkntfs"
file "${VENDOR}/mkntfs"
