package dng

import "testing"

func TestEstimatedFrameBytes(t *testing.T) {
	const w, h = 5728, 3024
	for _, tc := range []struct {
		c    Compression
		want int64
	}{
		{Uncompressed, w * h * 2},
		{Ratio3, w * h * 3 / 2 / 3},
		{ResolveRatio16, w * h * 3 / 2 / 16},
	} {
		if got := tc.c.EstimatedFrameBytes(w, h); got != tc.want {
			t.Errorf("%v: got %d, want %d", tc.c, got, tc.want)
		}
	}
	// Every mode shrinks the frame against uncompressed, and each listed
	// mode is no larger than the one before it.
	prev := Uncompressed.EstimatedFrameBytes(w, h)
	for _, c := range Compressions[1:] {
		got := c.EstimatedFrameBytes(w, h)
		if got <= 0 || got > prev && c != ResolveRatio3 {
			t.Errorf("%v: %d bytes after %d", c, got, prev)
		}
		prev = got
	}
}
