package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"

	dfs "github.com/soi/doctorx/core/internal/fs"
	"github.com/soi/doctorx/core/internal/rescue"
	"github.com/soi/doctorx/core/internal/scan"
)

func cmdScan(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("scan", flag.ExitOnError)
	asJSON := fs.Bool("json", false, "xuất kết quả dạng JSON")
	showAll := fs.Bool("all", false, "hiện cả mục hệ thống và file phụ trợ của macOS")
	flags, positional := splitArgs(args)
	fs.Parse(flags)
	if len(positional) < 1 {
		return fmt.Errorf("thiếu tên phân vùng, ví dụ: doctorx-core scan disk4s1")
	}
	bsd := positional[0]

	part, err := findPartition(ctx, bsd)
	if err != nil {
		return err
	}

	var concealed []scan.Concealed
	sess, openErr := openTarget(bsd, false)
	if openErr == nil {
		defer sess.Close()
		concealed, err = scan.FindConcealedRaw(ctx, sess.Volume, dfs.WalkOpt{})
		if err != nil {
			return err
		}
	} else {
		// Filesystem chưa có driver raw (NTFS, HFS+, APFS): vẫn quét được qua
		// mount, chỉ mất khả năng thấy bit System.
		if part.MountPoint == "" {
			return fmt.Errorf("%w (và ổ chưa được gắn để quét qua mount)", openErr)
		}
		fmt.Fprintf(os.Stderr, "Lưu ý: %v\n  → quét qua mount point, chỉ phát hiện được cờ Hidden.\n\n", openErr)
		concealed, err = scan.FindConcealedMounted(ctx, part.MountPoint, 0)
		if err != nil {
			return err
		}
	}

	var worm *scan.WormReport
	if part.MountPoint != "" {
		worm, _ = scan.ScanWorm(ctx, part.MountPoint, concealed)
	}

	if *asJSON {
		return json.NewEncoder(os.Stdout).Encode(map[string]any{
			"partition": part, "concealed": concealed, "worm": worm,
		})
	}

	fmt.Printf("Phân vùng %s (%s, nhãn %q)\n\n", part.BSD, part.FSType, part.Label)
	// Mặc định chỉ hiện dữ liệu của người dùng. USB dùng qua Mac có rất nhiều
	// file phụ trợ "._*" bị ẩn hợp lệ; in hết ra thì người dùng không tìm nổi
	// thứ mình cần cứu.
	shown := concealed
	hiddenCount := 0
	if !*showAll {
		shown = shown[:0:0]
		for _, c := range concealed {
			if c.Protected {
				hiddenCount++
				continue
			}
			shown = append(shown, c)
		}
	}

	if len(shown) == 0 {
		fmt.Println("Không tìm thấy dữ liệu nào bị giấu.")
	} else {
		fmt.Printf("Tìm thấy %d mục bị giấu:\n", len(shown))
		for _, c := range shown {
			kind := "file"
			if c.IsDir {
				kind = "thư mục"
			}
			note := ""
			if c.Protected {
				note = "   [mục hệ thống, không đụng vào]"
			}
			fmt.Printf("  %-8s %-10s %s%s\n", kind, humanSize(c.Size), c.Path, note)
		}
	}
	if hiddenCount > 0 {
		fmt.Printf("\n(Đã ẩn %d mục hệ thống và file phụ trợ của macOS — thêm -all để xem)\n", hiddenCount)
	}
	if worm != nil && len(worm.Findings) > 0 {
		fmt.Printf("\nDấu hiệu nghi ngờ (mức: %s, điểm %d):\n", worm.Severity, worm.TotalScore)
		for _, f := range worm.Findings {
			fmt.Printf("  [%s] %s\n      %s\n", f.Rule, f.Path, f.Reason)
		}
		fmt.Println("\nĐây là nhận định theo dấu hiệu cấu trúc, không phải kết quả quét virus.")
	}
	return nil
}

func cmdHiddenDirs(ctx context.Context, args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("thiếu tên phân vùng")
	}
	sess, _, err := openVolume(ctx, args[0], false)
	if err != nil {
		return err
	}
	defer sess.Close()

	dirs, err := rescue.ListRootHiddenDirs(ctx, sess.Volume)
	if err != nil {
		return err
	}
	if len(dirs) == 0 {
		fmt.Println("(Không có thư mục ẩn nào ở gốc ổ)")
		return nil
	}
	for i, d := range dirs {
		note := ""
		if rescue.PathIsProtected(d.Path) {
			note = "   [mục hệ thống — không khôi phục]"
		}
		fmt.Printf("%3d. %s%s\n", i+1, d.Path, note)
	}
	return nil
}
