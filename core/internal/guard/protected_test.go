package guard

import (
	"errors"
	"testing"
)

func TestProtectedRootsAreRejected(t *testing.T) {
	// Script .bat gốc chỉ in cảnh báo rồi để người dùng tự quyết; ở đây phải
	// chặn cứng, không có đường vòng.
	blocked := []string{
		"/System Volume Information",
		"/system volume information/con",
		"/$RECYCLE.BIN",
		"/$Recycle.Bin/S-1-5-21",
		"/Recovery",
		"/RECOVERY/WindowsRE",
		"/$WinREAgent",
		"/EFI",
		"/Boot",
		"/hiberfil.sys",
		"/pagefile.sys",
		"/.Spotlight-V100",
		"/.fseventsd",
		"/.Trashes",
		"/", // chính gốc volume không phải một entry
		"",
	}
	for _, p := range blocked {
		if err := AllowWrite(p); err == nil {
			t.Errorf("AllowWrite(%q) phải bị từ chối", p)
			continue
		}
		var ep *ErrProtected
		if err := AllowWrite(p); !errors.As(err, &ep) {
			t.Errorf("AllowWrite(%q) phải trả *ErrProtected, nhận %T", p, err)
		}
	}
}

func TestUserDataIsAllowed(t *testing.T) {
	allowed := []string{
		"/Anh gia dinh",
		"/Anh gia dinh/2019/dam-cuoi.jpg",
		"/sau/hon/nua",
		"/visible.txt",
		// Chỉ chặn ở CẤP GỐC: thư mục người dùng tự đặt tên "Recovery" ở cấp
		// sâu hơn vẫn phải khôi phục được.
		"/Du lieu/Recovery",
		"/backup/System Volume Information",
	}
	for _, p := range allowed {
		if err := AllowWrite(p); err != nil {
			t.Errorf("AllowWrite(%q) phải được phép, nhận lỗi: %v", p, err)
		}
	}
}

func TestRangeSetBoundaries(t *testing.T) {
	rs := NewRangeSet()
	rs.Add(1000, 2000, "dir-a")
	rs.Add(5000, 6000, "dir-b")

	ok := []struct {
		off int64
		n   int
	}{
		{1000, 1}, {1999, 1}, {1996, 4}, {5000, 2}, {5999, 1},
	}
	for _, c := range ok {
		if err := rs.Check(c.off, c.n); err != nil {
			t.Errorf("Check(%d,%d) phải hợp lệ: %v", c.off, c.n, err)
		}
	}

	bad := []struct {
		off int64
		n   int
		why string
	}{
		{999, 1, "ngay trước vùng"},
		{2000, 1, "ngay sau vùng"},
		{1999, 2, "tràn qua biên cuối"},
		{0, 1, "trước mọi vùng"},
		{4999, 2, "tràn vào từ ngoài"},
		{2500, 1, "nằm giữa hai vùng"},
		{1500, 4000, "bắc cầu qua cả hai vùng"},
	}
	for _, c := range bad {
		if err := rs.Check(c.off, c.n); err == nil {
			t.Errorf("Check(%d,%d) phải bị từ chối (%s)", c.off, c.n, c.why)
		}
	}
}

func TestEmptyRangeSetRejectsEverything(t *testing.T) {
	rs := NewRangeSet()
	if err := rs.Check(0, 1); err == nil {
		t.Fatal("RangeSet rỗng phải từ chối mọi thao tác ghi")
	}
}

func TestCheckPatchSize(t *testing.T) {
	for _, n := range []int{1, 2, 4, MaxPatchBytes} {
		if err := CheckPatchSize(n); err != nil {
			t.Errorf("kích thước %d phải hợp lệ: %v", n, err)
		}
	}
	for _, n := range []int{0, -1, MaxPatchBytes + 1, 512} {
		if err := CheckPatchSize(n); err == nil {
			t.Errorf("kích thước %d phải bị từ chối", n)
		}
	}
}
