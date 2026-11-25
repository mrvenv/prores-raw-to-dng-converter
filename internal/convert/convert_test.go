package convert

import (
	"testing"

	"rawconv/internal/dng"
)

func TestOutputName(t *testing.T) {
	for c, want := range map[dng.Compression]string{
		dng.Uncompressed:   "camraw_none",
		dng.LosslessJPEG:   "camraw_lossless",
		dng.LosslessJPEG12: "camraw_12bit",
		dng.Ratio3:         "camraw_3to1",
		dng.Ratio8:         "camraw_8to1",
	} {
		if got := OutputName("/clips/camraw.MOV", c); got != want {
			t.Errorf("%v: got %q, want %q", c, got, want)
		}
	}
}
