package imaging

import (
	"strings"
	"testing"
)

func TestBuildEraseArgs(t *testing.T) {
	tests := []struct {
		name      string
		req       FormatRequest
		wantArgs  []string
		wantLabel string
		wantErr   bool
	}{
		{
			name:     "fat32 mbr uppercases and defaults scheme",
			req:      FormatRequest{BSD: "disk4", FS: "fat32", Label: "myusb"},
			wantArgs: []string{"eraseDisk", "FAT32", "MYUSB", "MBR", "disk4"},
		},
		{
			name:     "exfat gpt keeps case",
			req:      FormatRequest{BSD: "/dev/disk4", FS: "exfat", Scheme: "gpt", Label: "Data"},
			wantArgs: []string{"eraseDisk", "ExFAT", "Data", "GPT", "disk4"},
		},
		{
			name:      "fat32 label truncated to 11",
			req:       FormatRequest{BSD: "disk4", FS: "fat32", Label: "abcdefghijklmnop"},
			wantLabel: "ABCDEFGHIJK",
		},
		{name: "ntfs rejected pending mkntfs", req: FormatRequest{BSD: "disk4", FS: "ntfs"}, wantErr: true},
		{name: "unknown fs rejected", req: FormatRequest{BSD: "disk4", FS: "hfs"}, wantErr: true},
		{name: "bad scheme rejected", req: FormatRequest{BSD: "disk4", FS: "exfat", Scheme: "apm"}, wantErr: true},
		{name: "fat32 illegal label char", req: FormatRequest{BSD: "disk4", FS: "fat32", Label: "a?b"}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			args, out, err := buildEraseArgs(tt.req)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr = %v", err, tt.wantErr)
			}
			if tt.wantErr {
				return
			}
			if tt.wantArgs != nil && strings.Join(args, " ") != strings.Join(tt.wantArgs, " ") {
				t.Fatalf("args = %v, want %v", args, tt.wantArgs)
			}
			if tt.wantLabel != "" && out.Label != tt.wantLabel {
				t.Fatalf("label = %q, want %q", out.Label, tt.wantLabel)
			}
		})
	}
}

func TestNormalizeLabelEmptyDefault(t *testing.T) {
	got, err := normalizeLabel("exfat", "   ")
	if err != nil || got != "UNTITLED" {
		t.Fatalf("nhãn rỗng phải về UNTITLED, got %q err %v", got, err)
	}
}
