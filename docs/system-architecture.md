# Kiến trúc DoctorX

## Quyết định nền: vì sao cần raw device

Đo trên macOS 15.6, bằng disk image thật:

| Thí nghiệm | Kết quả |
|---|---|
| FAT32/exFAT, entry `Hidden`+`System` | File **vẫn đọc được** qua mount của macOS, `ls -lO` hiện cờ `hidden` |
| `chflags nohidden` trên entry đó | Xoá được `Hidden`, attr `0x36 → 0x34`. **Không xoá được `System`** |
| exFAT, sửa attribute mà không tính lại `SetChecksum` | Entry **biến mất hoàn toàn** — cả macOS lẫn Windows coi là hỏng |
| exFAT, mount rồi chỉ `cat` một file | Ảnh đĩa **thay đổi** (cập nhật `LastAccessedTimestamp`). FAT32 thì không |
| `newfs_msdos`/`newfs_exfat` trên disk image qua `hdiutil attach -nomount` | Chạy được **không cần root** |

Suy ra ba điều định hình toàn bộ kiến trúc:

1. **Đọc dữ liệu không cần raw device.** File bị giấu vẫn truy cập được qua mount thường, nên quét và sao chép dùng `os` stdlib. Đơn giản hơn nhiều, không cần quyền quản trị, và chạy được trên mọi filesystem macOS hiểu — kể cả NTFS, HFS+, APFS mà DoctorX không có driver riêng.

2. **Chỉ việc xoá bit `System` mới cần raw device.** Đây là lý do duy nhất package `internal/fs` tồn tại. Vì vậy driver **không** cài đọc nội dung file: không cluster chain, không runlist, không resident/non-resident. Chỉ định vị directory entry và lật bit.

3. **exFAT phải tính lại `SetChecksum`.** Sai bước này là mất file, không phải hiển thị sai. Mọi thay đổi attribute trên exFAT sinh hai patch: attribute và checksum.

## Phân tầng

```
 ┌───────────── DoctorX.app (SwiftUI, quyền người dùng) ─────────────┐
 │  AppState        trạng thái, điều phối thao tác                   │
 │  CoreClient      Unix socket + NDJSON                             │
 │  Views           danh sách ổ · kết quả quét · Quick Restore       │
 └────────────────────────────┬──────────────────────────────────────┘
                              │ /var/run/doctorx.sock
 ┌────────────────────────────┴──── doctorx-core (Go, root) ─────────┐
 │  cmd/doctorx_core   CLI + daemon, cùng một binary                 │
 │  ipc/               giao thức NDJSON                              │
 │  rescue/            copy-out (qua mount) · unhide (qua raw)       │
 │  scan/              phát hiện mục bị giấu · heuristic worm        │
 │  fsprobe/           nhận diện filesystem, mở driver               │
 │  fs/ + fat/ exfat/  định vị directory entry, sinh Patch           │
 │  guard/             thư mục cấm · giới hạn vùng ghi               │
 │  blockdev/          raw I/O · journal · ĐƯỜNG GHI DUY NHẤT        │
 └───────────────────────────────────────────────────────────────────┘
```

CLI và daemon là cùng một binary. Nhờ vậy mọi thứ dùng được ngay từ Terminal để kiểm chứng, không phụ thuộc app đã cài xong hay chưa.

## Hợp đồng giữa các tầng

Driver filesystem **không tự ghi**. `ClearAttrs` trả về `[]fs.Patch` mô tả thay đổi mong muốn; tầng `rescue` gom lại và đưa cho `blockdev.Writer`. Nhờ ràng buộc này, toàn bộ đường ghi đi qua đúng một hàm có đủ guard, journal và kiểm tra — thêm driver mới (NTFS) không mở thêm đường ghi nào.

```go
type Volume interface {
    Info() VolumeInfo
    Walk(ctx, WalkOpt, func(*Entry) error) error
    Stat(ctx, path) (*Entry, error)
    Writable() (bool, string)
    MetadataRanges() *guard.RangeSet
    ClearAttrs(e *Entry, mask Attr) ([]Patch, error)
}
```

`WalkOpt.MaxDepth` không phải tối ưu sớm: ổ ngoài 4TB có thể vài triệu entry, còn Quick Restore chỉ cần một cấp. `Walk` bắt buộc streaming qua callback.

## Bốn lớp bảo vệ đường ghi

Trong `blockdev.Writer.Apply`, theo đúng thứ tự:

1. `guard.RangeSet.Check` — offset phải nằm gọn trong vùng metadata driver đã khai báo trong lúc duyệt. Patch bắc cầu qua hai block bị từ chối: attribute luôn nằm gọn trong một entry, bắc cầu nghĩa là tính offset sai.
2. `Journal.Save` + `fsync` — bản gốc xuống đĩa trước khi ghi đè. Bất biến: block chưa vào journal thì chưa được ghi.
3. So `Patch.Old` với byte thật trên thiết bị — lệch nghĩa là ổ đã bị sửa từ lúc quét, dừng ngay.
4. Đọc lại sau ghi và so từng byte; lệch thì hoàn tác và báo lỗi.

Lỗi giữa chừng tự hoàn tác các block đã ghi trong cùng lần gọi, theo thứ tự ngược.

**Gộp patch theo block** là yêu cầu bắt buộc chứ không phải tối ưu: một sector FAT chứa 16 directory entry, nên cây 100k file sinh ~6k lượt ghi thay vì 100k.

## Chặn thao tác nhầm

Ba lớp độc lập:

- **Phân vùng**: `blockdev` chỉ liệt kê ổ có `Internal == false`. Ổ khởi động không bao giờ xuất hiện. Lọc theo `Internal` chứ không theo `Removable` — HDD gắn ngoài qua USB/Thunderbolt báo `RemovableMedia = false`, lọc nhầm sẽ mất sạch nhóm ổ này.
- **Đường dẫn**: `guard.AllowWrite` chặn cứng thư mục hệ thống ở cấp gốc, kiểm tra cho **mọi** mục trong thao tác đệ quy chứ không chỉ mục gốc được chọn. Không có tuỳ chọn bỏ qua.
- **Vùng byte**: `guard.RangeSet` như trên.

Script `.bat` gốc chỉ in `reagentc /info` rồi để người dùng tự cân nhắc có đụng vào thư mục WinRE hay không. DoctorX làm các mục đó **không chọn được**.

## Nhiễu của macOS

USB dùng qua Mac luôn có file AppleDouble `._<tên>` (macOS lưu metadata mà FAT/exFAT không giữ được) và bị đánh dấu ẩn. Chúng làm kết quả quét từ 3 mục thật phồng lên 19 mục. `guard.IsMacOSMetadata` nhận ra chúng ở mọi cấp: quét thì đánh dấu "không phải dữ liệu người dùng", unhide thì bỏ qua — gỡ cờ ẩn chỉ tạo ra một đống file rác lộ thiên khi cắm sang Windows.

## Kiểm thử

Fixture sinh bằng `newfs_msdos`/`newfs_exfat` trên disk image, **không cần root**, nên chạy được trong CI. Bit `Hidden`+`System` do một script Python độc lập vá vào — cố ý không dùng code Go đang được kiểm thử.

Toàn bộ đường ghi kiểm thử trên file ảnh thay vì `/dev`, nên `go test` chạy bằng quyền người dùng thường. Hai kiểm chứng quan trọng nhất:

- **Hoàn tác bit-for-bit**: SHA-256 toàn ảnh trước và sau chu trình unhide → rollback phải khớp.
- **Oracle độc lập**: sau unhide, mount ảnh bằng macOS và xác nhận `ls -lO` không còn cờ `hidden`, nội dung file vẫn đọc đúng. Không phụ thuộc vào code của chính DoctorX.

Lưu ý khi đo trên exFAT: **đừng đọc file nào giữa hai lần băm** — exFAT cập nhật `LastAccessedTimestamp` khi đọc, sẽ thấy lệch và tưởng nhầm là lỗi rollback.

## NTFS

NTFS đọc, sao chép và gỡ cờ ẩn tại chỗ đầy đủ. Ba điểm quyết định:

- **Ghi ba nơi**: xoá cờ ẩn phải sửa đồng bộ `$STANDARD_INFORMATION` (thuộc tính thật của file), `$FILE_NAME` trong MFT record, và bản copy trong index `$I30` của thư mục cha. Explorer đọc `$I30` để hiển thị, nên bỏ sót nơi thứ ba là file vẫn bị giấu dù MFT đã đúng. Reader ghi lại cả ba vị trí byte lúc duyệt $I30; nơi nào đã đúng sẵn thì ClearAttrs bỏ qua (fixture thật có desync $FILE_NAME → ghi 2 patch).
- **Guard Update Sequence Array**: writer đọc-sửa-ghi nguyên block 512, mà USA đặt số tuần tự ở 2 byte cuối mỗi sector của MFT record và INDX block. Nếu trường attribute rơi trúng đó, ghi đè sẽ phá fixup và verify-after-write không phát hiện được (đọc lại đúng thứ vừa ghi sai). ClearAttrs từ chối file nếu Loc rơi vào vùng USA.
- **Tiền điều kiện ghi** (`Writable()`): từ chối nếu có `hiberfil.sys` (Fast Startup — nguyên nhân mất dữ liệu phổ biến nhất khi ghi NTFS từ OS khác), dirty bit trong `$VOLUME_INFORMATION` bật, hoặc phiên bản ngoài 3.0/3.1. Chưa phân tích restart-area của `$LogFile`; dirty bit là tín hiệu Windows tự đặt khi shutdown bẩn nên che được hầu hết ca.

16 metafile đầu ($MFT, $LogFile, $Boot, $Extend…) đều Hidden+System nhưng là cấu trúc của chính filesystem — Walk bỏ qua record < 16, tầng trên không bao giờ thấy.

Kiểm chứng: unhide trên ảnh → mount NTFS của macOS xác nhận hết cờ hidden + `ntfsfix -n` (ntfs-3g trong Docker, thay chkdsk) báo cấu trúc toàn vẹn + rollback bit-for-bit. Chạy bằng `internal/fs/testdata/check-ntfs.sh`.

### Thư mục lớn, index phân mảnh

Ổ HDD ngoài dùng lâu có thư mục hàng nghìn entry với `$INDEX_ALLOCATION` phân mảnh. Hai điểm phải đúng, đã kiểm bằng fixture 200MB (`ntfs-fragmented.img`, thư mục 5000 entry):

- **Đơn vị VCN của sub-node**: là *cluster* VCN khi index-record ≥ cluster (thường), nên byte-offset = `vcn × min(cluster, index-record)`, KHÔNG phải `vcn × index-record`. Bug này ẩn trên fixture nhỏ vì `mkntfs` mặc định cluster 4096 = index-record nên hai giá trị trùng; chỉ ổ format cluster 512 mới lộ. Đọc INDX block theo từng cluster qua runlist để chịu được block trải nhiều cluster không liền nhau.
- **Gom mảnh `$ATTRIBUTE_LIST`**: `collectNonResidentRuns` nối mọi mảnh `$INDEX_ALLOCATION` (base + record phụ) theo thứ tự VCN; xử lý cả `$ATTRIBUTE_LIST` non-resident.

## Ghi phá huỷ: flash / format / bad blocks (package `imaging`)

Ngoài đường rescue read-mostly, DoctorX có một subsystem **ghi phá huỷ toàn ổ** kiểu Rufus. Nó **không** đi qua `blockdev.Writer`/`guard.RangeSet`/`Journal`: ghi cả gigabyte thì không thể sao lưu-trước để hoàn tác, nên bốn lớp bảo vệ của đường rescue không áp dụng được. An toàn ở đây dựa vào ba cổng khác, gom trong `imaging.lockTarget`:

1. **Chỉ whole disk gắn ngoài** — `resolveTarget` bắt tên khớp `^disk[0-9]+$` (từ chối phân vùng/APFS volume) và phải nằm trong `blockdev.ListExternalDisks` (đã lọc `Internal == false`). Ổ nội bộ/khởi động không bao giờ lọt vào.
2. **Target lock** — dung lượng + model phải khớp giá trị người dùng thấy lúc `flash_preflight`. Chặn ca ổ bị rút rồi cắm ổ khác vào cùng tên BSD giữa preflight và thao tác.
3. **Xác nhận tường minh** — người dùng phải gõ lại đúng `ConfirmToken` (model, hoặc tên BSD nếu model rỗng), mô phỏng hộp thoại của Rufus.

Ba tính năng, đều tháo mount toàn ổ (`diskutil unmountDisk`) trước khi ghi:

- **Flash** (`flash_image`): đọc ISO/IMG tuần tự, ghi ra `/dev/rdiskN` theo buffer 4 MiB **align sector** (block cuối đệm 0 tới biên sector — rdisk từ chối ghi lệch sector), rồi `Sync`. Verify mặc định bật: đọc lại qua rdisk (character device, bỏ qua buffer cache nên phản ánh byte thật) và so SHA-256; hash chỉ tính đúng số byte thật của image, không tính phần đệm.
- **Format** (`format_disk`): FAT32/exFAT qua `diskutil eraseDisk` (tự ghi MBR/GPT + newfs một lệnh, không tự dựng partition table). Nhãn chuẩn hoá theo FS (FAT32: IN HOA, ≤11 ký tự, chặn ký tự cấm; exFAT ≤15). NTFS đi đường riêng (macOS không có tool format NTFS): `diskutil partitionDisk` tạo phân vùng Microsoft Basic Data (GUID đã đúng cho NTFS trên GPT) → tháo mount → `mkntfs` (ntfs-3g, đóng gói kèm app) ghi đè. Chỉ hỗ trợ GPT (MBR cần sửa byte type, để cho phase Windows USB). Binary mkntfs resolve theo `DOCTORX_MKNTFS` → cạnh core → `Contents/Resources` → PATH; thiếu thì trả lỗi rõ ràng.
- **Bad blocks** (`check_bad_blocks`): mặc định read-only (mở rdisk O_RDONLY, quét từng chunk, gộp khoảng lỗi liền kề — không cần confirm vì không phá dữ liệu, nhưng vẫn bắt external whole-disk). Chế độ write-test ghi pattern `0xA5` rồi đọc lại so sánh — **phá huỷ**, đi qua cổng đầy đủ, trả cờ `Destroyed`.

Phần thuần (`copyImage`, `hashDevice`, `scanRead`, `scanWrite`, `buildEraseArgs`, `resolveTarget`, `checkLock`) tách khỏi I/O thiết bị nên `go test` chạy trên file thường bằng quyền người dùng — không cần root, không cần USB.

## Điểm chưa hoàn thiện

- **Flash/format/bad-block chưa verify trên USB thật** — đường `/dev/rdiskN` chỉ mới test qua file trong CI; cần chạy tay một lần với ổ thật + quyền root.
- **Format NTFS**: code path xong (GPT), nhưng cần binary `mkntfs` (GPLv3, universal arm64+x86_64) được đóng gói vào `Contents/Resources` — chưa có trong repo. Dev đặt `DOCTORX_MKNTFS` trỏ tới mkntfs cài sẵn. MBR+NTFS chưa hỗ trợ.
- **Windows bootable USB** (ISO9660/UDF reader, split WIM, UEFI:NTFS) đã hoãn.
- NTFS chưa test chéo trên Windows thật (không có máy Windows) — đã thay bằng ntfs-3g + mount macOS.
- Ghi Loc-3 (bản copy index của cha) trong một INDX block **không liền mạch** trên đĩa (hiếm): offset `firstAbs + pos` sẽ sai, nhưng `guard.RangeSet` chặn lại nên không hỏng dữ liệu — chỉ bỏ qua entry đó (an toàn, không phải mất mát). Muốn phủ nốt thì ánh xạ offset từng entry qua runlist.
- `$ATTRIBUTE_LIST` phân mảnh sâu (nhiều tầng) chỉ hỗ trợ một tầng.
