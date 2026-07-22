package blockdev

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
)

// Liệt kê ổ đĩa qua `diskutil -plist` rồi chuyển sang JSON bằng `plutil`.
//
// Cố ý không dùng IOKit qua cgo: diskutil đã tổng hợp sẵn quan hệ whole-disk →
// partition, loại filesystem, mount point và cờ Internal — đúng những gì cần,
// không phải duyệt cây IORegistry và tự suy ra.

// Partition là một phân vùng có thể thao tác.
type Partition struct {
	BSD        string `json:"bsd"`
	Label      string `json:"label"`
	FSType     string `json:"fs"`
	SizeBytes  int64  `json:"sizeBytes"`
	MountPoint string `json:"mountPoint"`
	// SystemPartition đúng với EFI, Recovery, Microsoft Reserved... UI phải
	// chặn mọi thao tác ghi lên các phân vùng này.
	SystemPartition bool `json:"systemPartition"`
	Content         string
}

// Disk là một ổ vật lý.
type Disk struct {
	BSD         string      `json:"bsd"`
	Model       string      `json:"model"`
	SizeBytes   int64       `json:"sizeBytes"`
	Internal    bool        `json:"internal"`
	Removable   bool        `json:"removable"`
	BusProtocol string      `json:"busProtocol"`
	Scheme      string      `json:"partitionScheme"`
	Partitions  []Partition `json:"partitions"`
}

// systemContentTypes là các loại phân vùng không bao giờ được ghi vào.
// Khớp cả tên kiểu Apple lẫn GUID của GPT.
var systemContentTypes = map[string]bool{
	"EFI":                                  true,
	"Apple_Boot":                           true,
	"Apple_APFS_Recovery":                  true,
	"Apple_APFS_ISC":                       true,
	"Microsoft Reserved":                   true,
	"Windows Recovery":                     true,
	"C12A7328-F81F-11D2-BA4B-00A0C93EC93B": true, // EFI System Partition
	"E3C9E316-0B5C-4DB8-817D-F92DF00215AE": true, // Microsoft Reserved
	"DE94BBA4-06D1-4D40-A16A-BFD50179D6AC": true, // Windows Recovery
	"426F6F74-0000-11AA-AA11-00306543ECAC": true, // Apple Boot (Recovery HD)
}

type duList struct {
	AllDisksAndPartitions []struct {
		DeviceIdentifier string `json:"DeviceIdentifier"`
		Content          string `json:"Content"`
		Size             int64  `json:"Size"`
		OSInternal       bool   `json:"OSInternal"`
		Partitions       []struct {
			DeviceIdentifier string `json:"DeviceIdentifier"`
			Content          string `json:"Content"`
			Size             int64  `json:"Size"`
			VolumeName       string `json:"VolumeName"`
			MountPoint       string `json:"MountPoint"`
		} `json:"Partitions"`
		APFSVolumes []struct {
			DeviceIdentifier string `json:"DeviceIdentifier"`
			Size             int64  `json:"Size"`
			VolumeName       string `json:"VolumeName"`
			MountPoint       string `json:"MountPoint"`
		} `json:"APFSVolumes"`
	} `json:"AllDisksAndPartitions"`
}

type duInfo struct {
	Internal          bool   `json:"Internal"`
	Ejectable         bool   `json:"Ejectable"`
	RemovableMedia    bool   `json:"RemovableMedia"`
	VolumeName        string `json:"VolumeName"`
	FilesystemType    string `json:"FilesystemType"`
	MountPoint        string `json:"MountPoint"`
	Size              int64  `json:"Size"`
	BusProtocol       string `json:"BusProtocol"`
	MediaName         string `json:"MediaName"`
	Content           string `json:"Content"`
	DeviceIdentifie   string `json:"DeviceIdentifier"`
	VirtualOrPhysical string `json:"VirtualOrPhysical"`
}

// diskutilJSON chạy `diskutil <verb> -plist <rest...>` và giải mã kết quả.
// Thứ tự tham số quan trọng: diskutil từ chối khi -plist đứng sau tên thiết bị.
func diskutilJSON(ctx context.Context, out any, verb string, rest ...string) error {
	args := append([]string{verb, "-plist"}, rest...)
	cmd := exec.CommandContext(ctx, "diskutil", args...)
	plist, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("diskutil %s: %w", strings.Join(args, " "), err)
	}
	conv := exec.CommandContext(ctx, "plutil", "-convert", "json", "-o", "-", "-")
	conv.Stdin = strings.NewReader(string(plist))
	js, err := conv.Output()
	if err != nil {
		return fmt.Errorf("plutil chuyển plist sang json: %w", err)
	}
	return json.Unmarshal(js, out)
}

// ListExternalDisks trả về các ổ GẮN NGOÀI kèm phân vùng của chúng.
//
// Lọc theo Internal chứ không theo Removable: HDD/SSD gắn ngoài qua USB hay
// Thunderbolt báo RemovableMedia = false, lọc nhầm sẽ mất sạch nhóm ổ này —
// đúng nhóm mà DoctorX cần hỗ trợ. Ổ nội bộ không bao giờ xuất hiện, để loại
// trừ hoàn toàn khả năng thao tác nhầm lên ổ khởi động.
func ListExternalDisks(ctx context.Context) ([]Disk, error) {
	// "physical" loại bỏ mọi ổ ảo: disk image (.dmg), volume của iOS Simulator,
	// cryptex hệ thống, container APFS tổng hợp... — thứ trước đây lọt vào danh
	// sách và làm rối. Chỉ còn ổ vật lý thật. Cũng giảm mạnh số lần gọi diskutil
	// nên nhẹ hơn hẳn trên máy có nhiều disk image.
	var list duList
	if err := diskutilJSON(ctx, &list, "list", "physical"); err != nil {
		return nil, err
	}

	var disks []Disk
	for _, d := range list.AllDisksAndPartitions {
		// OSInternal có sẵn trong danh sách, bỏ ổ nội bộ mà chưa cần gọi info.
		if d.OSInternal {
			continue
		}
		var whole duInfo
		if err := diskutilJSON(ctx, &whole, "info", d.DeviceIdentifier); err != nil {
			continue // ổ vừa bị rút giữa chừng: bỏ qua, không làm hỏng cả danh sách
		}
		// Phòng thêm: bỏ ổ nội bộ và ổ ảo còn sót (disk image gắn qua hdiutil
		// vẫn hiện là physical nên chặn theo bus/loại ở đây).
		if whole.Internal || whole.VirtualOrPhysical == "Virtual" || whole.BusProtocol == "Disk Image" {
			continue
		}

		disk := Disk{
			BSD:         d.DeviceIdentifier,
			Model:       strings.TrimSpace(whole.MediaName),
			SizeBytes:   d.Size,
			Internal:    whole.Internal,
			Removable:   whole.RemovableMedia || whole.Ejectable,
			BusProtocol: whole.BusProtocol,
			Scheme:      d.Content,
		}

		type rawPart struct {
			bsd, content, volName, mount string
			size                         int64
		}
		var raws []rawPart
		for _, p := range d.Partitions {
			raws = append(raws, rawPart{p.DeviceIdentifier, p.Content, p.VolumeName, p.MountPoint, p.Size})
		}
		for _, v := range d.APFSVolumes {
			raws = append(raws, rawPart{v.DeviceIdentifier, "APFS Volume", v.VolumeName, v.MountPoint, v.Size})
		}

		// Ổ không có bảng phân vùng, filesystem nằm thẳng trên whole disk
		// ("superfloppy"). Gặp ở USB format bằng máy ảnh, thiết bị nhúng, và ở
		// disk image. Tạo một mục đại diện để phần còn lại của chương trình
		// không phải phân biệt hai trường hợp.
		if len(raws) == 0 && whole.FilesystemType != "" {
			raws = append(raws, rawPart{
				bsd: d.DeviceIdentifier, content: d.Content,
				volName: whole.VolumeName, mount: whole.MountPoint, size: d.Size,
			})
		}

		for _, r := range raws {
			var pi duInfo
			if err := diskutilJSON(ctx, &pi, "info", r.bsd); err != nil {
				continue
			}
			label := pi.VolumeName
			if label == "" {
				label = r.volName
			}
			mount := pi.MountPoint
			if mount == "" {
				mount = r.mount
			}
			disk.Partitions = append(disk.Partitions, Partition{
				BSD:             r.bsd,
				Label:           label,
				FSType:          normalizeFSType(pi.FilesystemType, r.content),
				SizeBytes:       r.size,
				MountPoint:      mount,
				SystemPartition: systemContentTypes[r.content],
				Content:         r.content,
			})
		}
		disks = append(disks, disk)
	}
	return disks, nil
}

// normalizeFSType quy tên filesystem của diskutil về định danh ngắn dùng nội bộ.
func normalizeFSType(fsType, content string) string {
	s := strings.ToLower(fsType)
	switch {
	case strings.Contains(s, "exfat"):
		return "exfat"
	case strings.Contains(s, "ntfs"):
		return "ntfs"
	case strings.Contains(s, "fat32"), strings.Contains(s, "ms-dos fat32"):
		return "fat32"
	case strings.Contains(s, "fat16"):
		return "fat16"
	case strings.Contains(s, "fat12"):
		return "fat12"
	case strings.Contains(s, "ms-dos"), strings.Contains(s, "msdos"):
		return "fat32"
	case strings.Contains(s, "apfs"):
		return "apfs"
	case strings.Contains(s, "hfs"):
		return "hfs"
	}
	if fsType == "" && content != "" {
		return ""
	}
	return s
}

// Unmount tháo một phân vùng trước khi ghi raw. macOS chặn ghi xuống thiết bị
// khi volume còn đang mount, nên đây là bước bắt buộc.
func Unmount(ctx context.Context, bsd string) error {
	out, err := exec.CommandContext(ctx, "diskutil", "unmount", bsd).CombinedOutput()
	if err != nil {
		return fmt.Errorf("tháo %s thất bại (%w): %s — hãy đóng các ứng dụng đang mở file trên ổ",
			bsd, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// Mount gắn lại phân vùng sau khi ghi xong.
func Mount(ctx context.Context, bsd string) error {
	out, err := exec.CommandContext(ctx, "diskutil", "mount", bsd).CombinedOutput()
	if err != nil {
		return fmt.Errorf("gắn lại %s thất bại (%w): %s", bsd, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// UnmountDisk tháo TOÀN BỘ phân vùng của một ổ vật lý. Ghi raw ra whole disk
// (flash image) đòi hỏi mọi volume trên ổ đều đã tháo, không chỉ một phân vùng.
func UnmountDisk(ctx context.Context, bsd string) error {
	out, err := exec.CommandContext(ctx, "diskutil", "unmountDisk", bsd).CombinedOutput()
	if err != nil {
		return fmt.Errorf("tháo toàn ổ %s thất bại (%w): %s — hãy đóng các ứng dụng đang mở file trên ổ",
			bsd, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// MountDisk gắn lại toàn ổ (best-effort). Sau khi flash một image có filesystem
// hợp lệ, macOS sẽ nhận và mount lại; nếu image không phải filesystem macOS hiểu
// được thì lệnh này sẽ báo lỗi và caller nên bỏ qua.
func MountDisk(ctx context.Context, bsd string) error {
	out, err := exec.CommandContext(ctx, "diskutil", "mountDisk", bsd).CombinedOutput()
	if err != nil {
		return fmt.Errorf("gắn lại toàn ổ %s: %w: %s", bsd, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// RawDevicePath đổi "disk4" thành "/dev/rdisk4" (character device, bỏ qua buffer
// cache của kernel để đọc-lại-kiểm-tra phản ánh đúng dữ liệu thật trên ổ).
func RawDevicePath(bsd string) string {
	bsd = strings.TrimPrefix(bsd, "/dev/")
	bsd = strings.TrimPrefix(bsd, "r")
	return "/dev/r" + bsd
}
