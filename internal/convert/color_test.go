package convert

import (
	"math"
	"testing"

	"rawconv/internal/prr"
)

func TestBaselineExposureFromGain(t *testing.T) {
	// Panasonic GH7 header values; DNGs Resolve shows at the source's
	// brightness carry BaselineExposure -0.97393 for this gain.
	h := prr.Header{Gain: 8.1459, WBRed: 2.31934, WBBlue: 1.63281}
	if got := ColorFromHeader(h).BaselineExposure; math.Abs(got-(-0.97393)) > 1e-4 {
		t.Errorf("BaselineExposure %.5f, want -0.97393", got)
	}
	if got := ColorFromHeader(prr.Header{}).BaselineExposure; got != 0 {
		t.Errorf("no gain: BaselineExposure %v, want 0", got)
	}
}
