# DoctorX — cứu dữ liệu USB và ổ cứng gắn ngoài trên macOS
#
#   make build      biên dịch lõi Go và app Swift
#   make test       chạy toàn bộ test
#   make fixtures   sinh lại disk image dùng cho test
#   make app        ráp DoctorX.app
#   make tools      build binary ngoài (mkntfs cho NTFS, smartctl cho SMART)
#   make install    cài lõi vào /usr/local/bin
#   make daemon     chạy dịch vụ nền (cần sudo)

SHELL := /bin/bash
BUILD := build
APP   := $(BUILD)/DoctorX.app
CORE  := $(BUILD)/doctorx-core
MKNTFS := packaging/vendor/mkntfs
SMARTCTL := packaging/vendor/smartctl
BUNDLE_ID := com.doctorx.app

.PHONY: all build core app swift test fixtures clean install daemon fmt mkntfs smartctl tools

all: build

build: core swift

## Lõi Go, universal binary chạy được trên cả Apple Silicon lẫn Intel.
core:
	@mkdir -p $(BUILD)
	cd core && GOOS=darwin GOARCH=arm64 go build -trimpath -o ../$(BUILD)/doctorx-core-arm64 ./cmd/doctorx_core
	cd core && GOOS=darwin GOARCH=amd64 go build -trimpath -o ../$(BUILD)/doctorx-core-amd64 ./cmd/doctorx_core
	lipo -create -output $(CORE) $(BUILD)/doctorx-core-arm64 $(BUILD)/doctorx-core-amd64
	@rm -f $(BUILD)/doctorx-core-arm64 $(BUILD)/doctorx-core-amd64
	@echo "→ $(CORE)"

swift:
	cd app && swift build -c release
	@echo "→ app/.build/release/DoctorX"

## Ráp .app thủ công thay vì dùng project Xcode: ít file sinh tự động, dễ soi
## trong git, và đủ cho ứng dụng phân phối ngoài App Store.
app: core swift
	@rm -rf $(APP)
	@mkdir -p $(APP)/Contents/{MacOS,Resources,Library/LaunchDaemons}
	cp app/.build/release/DoctorX $(APP)/Contents/MacOS/DoctorX
	cp $(CORE) $(APP)/Contents/MacOS/doctorx-core
	cp packaging/Info.plist $(APP)/Contents/Info.plist
	cp packaging/com.doctorx.core.plist $(APP)/Contents/Library/LaunchDaemons/
	cp packaging/icon/DoctorX.icns $(APP)/Contents/Resources/DoctorX.icns
	@# mkntfs (nếu đã build) cho tính năng Format → NTFS; core tìm nó cạnh mình.
	@if [ -f $(MKNTFS) ]; then \
		cp $(MKNTFS) $(APP)/Contents/MacOS/mkntfs; \
		codesign --force --sign - --options runtime $(APP)/Contents/MacOS/mkntfs; \
	else \
		echo "⚠ $(MKNTFS) chưa có (chạy 'make mkntfs') — Format NTFS sẽ báo lỗi lúc chạy"; \
	fi
	@if [ -f $(SMARTCTL) ]; then \
		cp $(SMARTCTL) $(APP)/Contents/MacOS/smartctl; \
		codesign --force --sign - --options runtime $(APP)/Contents/MacOS/smartctl; \
	else \
		echo "⚠ $(SMARTCTL) chưa có (chạy 'make smartctl') — SMART health sẽ báo lỗi lúc chạy"; \
	fi
	@# Ký ad-hoc để chạy được trên máy dev. Bản phát hành cần Developer ID.
	codesign --force --sign - --options runtime $(APP)/Contents/MacOS/doctorx-core
	codesign --force --sign - --options runtime $(APP)
	@echo "→ $(APP)"

## Build binary mkntfs universal (arm64+x86_64) từ nguồn ntfs-3g (GPLv3) để đóng
## gói cho tính năng Format → NTFS. Chạy một lần; kết quả cache ở packaging/vendor/.
mkntfs:
	packaging/build-mkntfs.sh
	@echo "→ $(MKNTFS)"

## Build binary smartctl universal từ nguồn smartmontools (GPLv2) cho SMART health.
smartctl:
	packaging/build-smartmontools.sh
	@echo "→ $(SMARTCTL)"

## Build cả hai binary công cụ ngoài.
tools: mkntfs smartctl

test:
	cd core && go vet ./... && go test ./...

## Disk image dùng cho test. FAT/exFAT không cần quyền quản trị; NTFS cần Docker
## (ntfs-3g) vì macOS không có mkntfs.
fixtures:
	cd core/internal/fs/testdata && ./make-images.sh all
	cd core/internal/fs/testdata && ./make-ntfs.sh

fmt:
	cd core && gofmt -l -w . && go vet ./...

install: core
	install -m 0755 $(CORE) /usr/local/bin/doctorx-core
	@echo "Đã cài /usr/local/bin/doctorx-core"

## Chạy dịch vụ nền ở tiền cảnh (tiện gỡ lỗi). Cần root vì phải mở raw device.
daemon: core
	sudo $(CORE) serve -owner-uid $(shell id -u)

## Cài dịch vụ nền tự khởi động cùng máy, KHÔNG cần tài khoản Apple Developer.
## Dùng LaunchDaemon thủ công thay cho SMAppService.
install-service: core
	sudo packaging/install-daemon.sh

uninstall-service:
	sudo packaging/install-daemon.sh uninstall

clean:
	rm -rf $(BUILD) app/.build
