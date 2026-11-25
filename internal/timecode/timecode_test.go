package timecode

import "testing"

func TestNonDrop(t *testing.T) {
	tc, err := Parse("01:00:00:00", 23.976)
	if err != nil {
		t.Fatal(err)
	}
	if tc.Base != 24 || tc.DropFrame {
		t.Fatalf("base=%d df=%v", tc.Base, tc.DropFrame)
	}
	cases := map[int]string{0: "01:00:00:00", 23: "01:00:00:23", 24: "01:00:01:00", 24 * 3600: "02:00:00:00"}
	for n, want := range cases {
		if got := tc.String(n); got != want {
			t.Errorf("At(%d)=%s want %s", n, got, want)
		}
	}
	b := tc.BCD(24*61 + 5) // 01:01:01:05
	want := [8]byte{0x05, 0x01, 0x01, 0x01}
	if b != want {
		t.Errorf("BCD=%x want %x", b, want)
	}
}

func TestDropFrame(t *testing.T) {
	tc, err := Parse("00:00:59;29", 29.97)
	if err != nil {
		t.Fatal(err)
	}
	if !tc.DropFrame {
		t.Fatal("expected drop frame")
	}
	// The frame after 00:00:59;29 is 00:01:00;02 (frames 00 and 01 dropped).
	if got := tc.String(1); got != "00:01:00;02" {
		t.Errorf("got %s", got)
	}
	// Every 10th minute is not dropped.
	tc10, _ := Parse("00:09:59;29", 29.97)
	if got := tc10.String(1); got != "00:10:00;00" {
		t.Errorf("got %s", got)
	}
	if b := tc.BCD(1); b[0]&0x40 == 0 {
		t.Error("drop-frame flag not set")
	}
}

func TestRoundTrip(t *testing.T) {
	for _, s := range []string{"10:23:45:12", "00:00:00:00", "23:59:59:24"} {
		tc, err := Parse(s, 25)
		if err != nil {
			t.Fatal(err)
		}
		if got := tc.String(0); got != s {
			t.Errorf("%s -> %s", s, got)
		}
	}
}
