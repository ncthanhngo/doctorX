# DoctorX

Cứu dữ liệu USB và ổ cứng gắn ngoài trên macOS: tìm file bị virus giấu, gỡ cờ ẩn, sao chép dữ liệu ra nơi an toàn.

Hỗ trợ FAT12/16/32, exFAT và NTFS (đầy đủ: quét, sao chép, gỡ cờ ẩn tại chỗ); HFS+/APFS (quét và sao chép).

## Vấn đề nó giải quyết

Virus USB kiểu shortcut đánh dấu thư mục của bạn là `Hidden` + `System` rồi thay bằng file `.lnk`. Trên Windows có thể sửa bằng `attrib -h -s`. Trên macOS thì không: **không API nào của macOS xoá được bit `System`** — `chflags nohidden` chỉ xoá được `Hidden`. Mà Windows Explorer mặc định vẫn giấu file `System`, nên gỡ nửa vời là chưa cứu được gì.

DoctorX sửa thẳng directory entry trên thiết bị, có journal để hoàn tác.

## Bắt đầu nhanh

```bash
make build            # biên dịch lõi Go + app Swift
make test             # chạy toàn bộ test
make install          # cài doctorx-core vào /usr/local/bin
```

Dùng bằng dòng lệnh:

```bash
doctorx-core list                                   # liệt kê ổ gắn ngoài
doctorx-core scan disk4s1                           # tìm dữ liệu bị giấu (không cần sudo)
doctorx-core hidden-dirs disk4s1                    # thư mục ẩn ở gốc ổ
doctorx-core copy disk4s1 "/Anh gia dinh"           # cứu dữ liệu ra ~/DoctorX Rescued/

sudo doctorx-core unhide disk4s1 "/Anh gia dinh" -recursive
sudo doctorx-core rollback 20260721-205059-a3f2c1 disk4s1   # hoàn tác
```

Dùng bằng giao diện:

```bash
make app              # ráp build/DoctorX.app
make install-service  # cài dịch vụ nền tự khởi động (cần sudo một lần)
open build/DoctorX.app
```

Gỡ dịch vụ nền: `make uninstall-service`.

## Không cần tài khoản Apple Developer trả phí

DoctorX chạy từ bản build cục bộ trên chính máy bạn, nên **không cần** ký Developer ID, notarize, hay Mac App Store. Vì build tại chỗ (không tải về) nên không có cờ quarantine — Gatekeeper không chặn.

Đánh đổi so với bản có tài khoản trả phí:

- Dịch vụ nền cài bằng LaunchDaemon thủ công (`make install-service`) thay cho `SMAppService`. Chạy một lệnh sudo một lần; sau đó tự khởi động cùng máy.
- Không phân phối cho máy khác qua DMG một cách liền mạch (máy khác sẽ vướng Gatekeeper). Muốn chia sẻ thì người nhận cũng build từ nguồn.
- Dịch vụ nền xác thực tiến trình gọi bằng **UID** (chỉ đúng người dùng đã cài mới gọi được), không bằng chữ ký mã — vì bản build cục bộ không ký Developer ID.

Mọi lệnh đều nhận đường dẫn file ảnh đĩa thay cho tên thiết bị. Khi ổ có dấu hiệu hỏng, cách an toàn là dump ra file rồi thao tác trên bản sao:

```bash
sudo dd if=/dev/rdisk4s1 of=~/usb-dump.img bs=1m
doctorx-core scan ~/usb-dump.img
```

## An toàn

Đường ghi đi qua đúng một chỗ trong mã nguồn, với bốn lớp bảo vệ:

1. **Vùng ghi** — offset phải nằm trong vùng metadata mà driver đã khai báo. Không thể chạm vào FAT table, boot sector hay vùng dữ liệu.
2. **Journal** — nội dung gốc được `fsync` xuống đĩa *trước* khi ghi đè. Mất điện giữa chừng vẫn hoàn tác được.
3. **Kiểm tra trước ghi** — nếu byte hiện tại khác giá trị lúc quét, thao tác dừng lại (ổ đã bị tiến trình khác sửa).
4. **Kiểm tra sau ghi** — đọc lại và so từng byte; lệch thì tự hoàn tác.

Mỗi lần ghi chỉ đổi 1–2 byte attribute. Thư mục hệ thống (`System Volume Information`, `$RECYCLE.BIN`, `Recovery`, EFI…) bị chặn cứng trong mã, không có tuỳ chọn bỏ qua.

Hoàn tác được kiểm chứng bằng so sánh SHA-256 toàn ảnh đĩa trước và sau — phải khớp từng bit.

## Cấu trúc

```
core/                     lõi Go
  cmd/doctorx_core/       CLI + daemon
  internal/blockdev/      raw device I/O, journal, đường ghi duy nhất
  internal/fs/            hợp đồng chung + driver fat/ và exfat/
  internal/guard/         thư mục cấm + giới hạn vùng ghi
  internal/scan/          phát hiện mục bị giấu + heuristic worm
  internal/rescue/        sao chép ra nơi an toàn + gỡ cờ ẩn
app/                      app SwiftUI
docs/                     tài liệu
plans/                    kế hoạch triển khai
```

Chi tiết kiến trúc và các quyết định thiết kế: [docs/system-architecture.md](docs/system-architecture.md).

## Giới hạn hiện tại

- **NTFS** gỡ cờ ẩn tại chỗ chỉ khi volume shutdown sạch: không có `hiberfil.sys` (Fast Startup), dirty bit tắt. Nếu bị chặn, tắt Fast Startup trong Windows rồi Shut down (không phải Restart). Chưa test chéo trên Windows thật — đã kiểm bằng ntfs-3g + mount của macOS, gồm cả ổ có thư mục hàng nghìn entry, index phân mảnh.
- Quét qua mount (dùng cho HFS+/APFS) chỉ thấy cờ `Hidden`, không thấy `System`. FAT/exFAT/NTFS dùng driver raw nên thấy cả hai.
- Dịch vụ nền hiện chỉ xác thực bằng quyền sở hữu socket, chưa kiểm tra chữ ký mã của tiến trình gọi.
- Chưa ký Developer ID nên chưa phân phối ra ngoài được.

## Không phải phần mềm diệt virus

Phần phát hiện worm nhận diện **cấu trúc đặc trưng** — thư mục bị giấu kèm shortcut cùng tên chạy `cmd.exe`, `autorun.inf` trỏ tới file thực thi — chứ không so khớp chữ ký mã độc. Nó cảnh báo và để bạn quyết định, không tự xoá gì.
