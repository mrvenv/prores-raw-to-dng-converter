package convert

import (
	"testing"
	"time"

	"rawconv/internal/dng"
	"rawconv/internal/ffmpeg"
)

func TestProgressString(t *testing.T) {
	for _, tc := range []struct {
		p    Progress
		want string
	}{
		{Progress{Done: 0, Total: 450}, "0 / 450"},
		{Progress{Done: 120, Total: 450, Elapsed: 20 * time.Second}, "120 / 450 · 6.0 fps · 0:20, ~0:55 left"},
		{Progress{Done: 450, Total: 450, Elapsed: 75 * time.Second}, "450 / 450 · 6.0 fps · 1:15"},
	} {
		if got := tc.p.String(); got != tc.want {
			t.Errorf("%+v: got %q, want %q", tc.p, got, tc.want)
		}
	}
}

func TestFormatDuration(t *testing.T) {
	for d, want := range map[time.Duration]string{
		7 * time.Second:                           "0:07",
		12*time.Minute + 34*time.Second:           "12:34",
		time.Hour + 2*time.Minute + 3*time.Second: "1:02:03",
		1500 * time.Millisecond:                   "0:02",
	} {
		if got := FormatDuration(d); got != want {
			t.Errorf("%v: got %q, want %q", d, got, want)
		}
	}
}

func TestEstimateSize(t *testing.T) {
	p := ffmpeg.Probe{Width: 100, Height: 10, Frames: 30}
	if got, want := EstimateSize(p, dng.Uncompressed, 0), int64(30*100*10*2); got != want {
		t.Errorf("all frames: got %d, want %d", got, want)
	}
	if got, want := EstimateSize(p, dng.Uncompressed, 5), int64(5*100*10*2); got != want {
		t.Errorf("5 frames: got %d, want %d", got, want)
	}
}

func TestFormatSize(t *testing.T) {
	for bytes, want := range map[int64]string{
		820_000_000:       "820 MB",
		12_400_000_000:    "12.4 GB",
		1_500_000_000_000: "1.50 TB",
	} {
		if got := FormatSize(bytes); got != want {
			t.Errorf("%d: got %q, want %q", bytes, got, want)
		}
	}
}
