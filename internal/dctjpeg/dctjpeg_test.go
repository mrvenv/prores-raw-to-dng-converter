package dctjpeg

import (
	"bytes"
	"math"
	"testing"
)

// gradient is a smooth 12-bit image with mild texture, two components.
func gradient(w, h int) []uint16 {
	s := make([]uint16, w*h*2)
	for y := range h {
		for x := range w {
			base := 400 + 3000*x/w + 200*y/h
			s[(y*w+x)*2] = uint16(base + (x*7+y*3)%9)
			s[(y*w+x)*2+1] = uint16(base/2 + (x*5+y*11)%13)
		}
	}
	return s
}

func roundTrip(t *testing.T, samples []uint16, w, h int, q Quant) (maxErr float64, size int) {
	t.Helper()
	enc, err := Encode(samples, w, h, 2, &q)
	if err != nil {
		t.Fatal(err)
	}
	img, err := Decode(enc)
	if err != nil {
		t.Fatal(err)
	}
	if img.Width != w || img.Height != h || img.Components != 2 || img.Precision != Precision {
		t.Fatalf("decoded header %+v", img)
	}
	for i, v := range samples {
		maxErr = math.Max(maxErr, math.Abs(float64(img.Samples[i])-float64(v)))
	}
	return maxErr, len(enc)
}

func TestRoundTripFineQuantIsNearLossless(t *testing.T) {
	const w, h = 64, 48
	var q Quant
	for i := range q {
		q[i] = 1
	}
	maxErr, _ := roundTrip(t, gradient(w, h), w, h, q)
	if maxErr > 2 {
		t.Errorf("max error %.0f with unit quantisation, want <= 2", maxErr)
	}
}

func TestCoarserQuantIsSmallerAndBounded(t *testing.T) {
	const w, h = 128, 64
	s := gradient(w, h)
	fineErr, fineSize := roundTrip(t, s, w, h, ScaledQuant(0.25))
	coarseErr, coarseSize := roundTrip(t, s, w, h, ScaledQuant(2))
	t.Logf("fine: %d bytes, max error %.0f; coarse: %d bytes, max error %.0f", fineSize, fineErr, coarseSize, coarseErr)
	if coarseSize >= fineSize {
		t.Errorf("coarse table did not shrink output: %d >= %d", coarseSize, fineSize)
	}
	if coarseErr > 200 {
		t.Errorf("coarse max error %.0f out of 4095", coarseErr)
	}
}

func TestExtremeValues(t *testing.T) {
	const w, h = 16, 8
	s := make([]uint16, w*h*2)
	for i := range s {
		if (i/2+i/(2*w))%2 == 0 {
			s[i] = 4095
		}
	}
	var q Quant
	for i := range q {
		q[i] = 1
	}
	maxErr, _ := roundTrip(t, s, w, h, q)
	if maxErr > 2 {
		t.Errorf("checkerboard max error %.0f", maxErr)
	}
}

func TestHeader(t *testing.T) {
	q := ScaledQuant(1)
	enc, err := Encode(make([]uint16, 16*8*2), 16, 8, 2, &q)
	if err != nil {
		t.Fatal(err)
	}
	// SOI, then DQT (2 + 2 + 1 + 64 bytes), then SOF1 with precision 12,
	// height 8, width 16, 2 components.
	sof := 2 + 2 + 2 + 1 + BlockArea
	want := []byte{0xFF, 0xC1, 0, 14, Precision, 0, 8, 0, 16, 2}
	if !bytes.Equal(enc[sof:sof+len(want)], want) {
		t.Errorf("SOF1 % x, want % x", enc[sof:sof+len(want)], want)
	}
	if q[0] != 16 || q[63] != 99 {
		t.Errorf("ScaledQuant(1) is not table K.1: %d..%d", q[0], q[63])
	}
}

func TestRejectsBadInput(t *testing.T) {
	q := ScaledQuant(1)
	for _, c := range []struct {
		name          string
		n, w, h, comp int
	}{
		{"not a block multiple", 10 * 8 * 2, 10, 8, 2},
		{"wrong length", 5, 8, 8, 2},
		{"no components", 0, 8, 8, 0},
	} {
		if _, err := Encode(make([]uint16, c.n), c.w, c.h, c.comp, &q); err == nil {
			t.Errorf("%s: accepted", c.name)
		}
	}
	for _, data := range [][]byte{nil, {0xFF, 0xD8}, {0xFF, 0xD8, 0xFF, 0xC1, 0, 2}} {
		if _, err := Decode(data); err == nil {
			t.Errorf("Decode(% x) accepted", data)
		}
	}
}
