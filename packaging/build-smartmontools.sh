#!/bin/bash
# Build một binary `smartctl` UNIVERSAL (arm64 + x86_64) từ mã nguồn smartmontools
# để đóng gói kèm DoctorX.app cho tính năng "SMART health" (xem
# core/internal/imaging/smart.go).
#
# Kết quả: packaging/vendor/smartctl
#
# smartmontools là GPLv2 — khi phân phối kèm binary phải kèm giấy phép và chào
# mã nguồn.
#
# Yêu cầu: Xcode Command Line Tools (clang++, lipo), curl, tar, make.
set -euo pipefail

VERSION="7.4"
TARBALL="smartmontools-${VERSION}.tar.gz"
URL="https://downloads.sourceforge.net/project/smartmontools/smartmontools/${VERSION}/${TARBALL}"
# Để trống = in sha256 tải về rồi tiếp tục (có cảnh báo). Điền để khoá supply-chain.
EXPECT_SHA256="e9a61f641ff96ca95319edfb17948cd297d0cd3342736b2c49c99d4716fb993d"

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
VENDOR="${HERE}/vendor"
WORK="$(mktemp -d)"
trap 'rm -rf "${WORK}"' EXIT

echo "→ Tải ${URL}"
curl -fsSL "${URL}" -o "${WORK}/${TARBALL}"

GOT_SHA="$(shasum -a 256 "${WORK}/${TARBALL}" | awk '{print $1}')"
if [[ -n "${EXPECT_SHA256}" ]]; then
	[[ "${GOT_SHA}" == "${EXPECT_SHA256}" ]] || { echo "✗ sha256 lệch: ${GOT_SHA}" >&2; exit 1; }
	echo "✓ sha256 khớp"
else
	echo "⚠ EXPECT_SHA256 trống — sha256 tải về: ${GOT_SHA} (nên điền để khoá)"
fi

tar -xzf "${WORK}/${TARBALL}" -C "${WORK}"
SRC="${WORK}/smartmontools-${VERSION}"

build_arch() {
	local arch="$1" host="$2"
	local out="${WORK}/build-${arch}"
	cp -R "${SRC}" "${out}"
	(
		cd "${out}"
		# smartmontools là C++; đặt cả CC lẫn CXX. --without-... để chỉ dựng smartctl.
		CC="clang -arch ${arch}" CXX="clang++ -arch ${arch}" \
		./configure --host="${host}" \
			--without-libcap-ng --without-selinux --without-systemdsystemunitdir \
			--disable-scsi-cdb-check --quiet >/dev/null
		make smartctl >/dev/null
	)
	echo "${out}/smartctl"
}

echo "→ Build arm64"
ARM64_BIN="$(build_arch arm64 aarch64-apple-darwin)"
echo "→ Build x86_64"
AMD64_BIN="$(build_arch x86_64 x86_64-apple-darwin)"

mkdir -p "${VENDOR}"
lipo -create -output "${VENDOR}/smartctl" "${ARM64_BIN}" "${AMD64_BIN}"
strip -S "${VENDOR}/smartctl" 2>/dev/null || true
chmod +x "${VENDOR}/smartctl"

cp "${SRC}/COPYING" "${VENDOR}/smartctl.COPYING" 2>/dev/null || true

echo
echo "✓ ${VENDOR}/smartctl"
lipo -info "${VENDOR}/smartctl"
