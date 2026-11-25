package ljpeg

import (
	"bytes"
	"math/rand"
	"slices"
	"testing"
)

func roundTrip(t *testing.T, samples []uint16, w, h, comps, prec int) []byte {
	t.Helper()
	enc, err := Encode(samples, w, h, comps, prec)
	if err != nil {
		t.Fatal(err)
	}
	img, err := Decode(enc)
	if err != nil {
		t.Fatal(err)
	}
	if img.Width != w || img.Height != h || img.Components != comps || img.Precision != prec {
		t.Fatalf("header %+v", img)
	}
	if !slices.Equal(img.Samples, samples) {
		for i := range samples {
			if img.Samples[i] != samples[i] {
				t.Fatalf("sample %d: got %d want %d", i, img.Samples[i], samples[i])
			}
		}
	}
	return enc
}

func TestRoundTrip(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	for _, tc := range []struct{ w, h, comps, prec int }{
		{1, 1, 1, 16}, {7, 5, 1, 12}, {64, 32, 2, 16}, {128, 64, 2, 12}, {33, 9, 3, 14},
	} {
		n := tc.w * tc.h * tc.comps
		maxVal := 1<<tc.prec - 1
		smooth := make([]uint16, n)
		noisy := make([]uint16, n)
		for i := range smooth {
			smooth[i] = uint16((i*37 + rng.Intn(64)) % (maxVal + 1))
			noisy[i] = uint16(rng.Intn(maxVal + 1))
		}
		roundTrip(t, smooth, tc.w, tc.h, tc.comps, tc.prec)
		roundTrip(t, noisy, tc.w, tc.h, tc.comps, tc.prec)
		roundTrip(t, make([]uint16, n), tc.w, tc.h, tc.comps, tc.prec) // flat: one symbol
	}
}

func TestExtremeDifferences(t *testing.T) {
	// Alternating 0 / 65535 and 0 / 32768 produce differences of ±1 and -32768.
	s := []uint16{0, 65535, 0, 65535, 0, 32768, 0, 32768}
	roundTrip(t, s, 8, 1, 1, 16)
	roundTrip(t, s, 4, 1, 2, 16)
}

func TestHeaderBytes(t *testing.T) {
	enc := roundTrip(t, []uint16{100, 200, 300, 400}, 2, 1, 2, 12)
	if !bytes.HasPrefix(enc, []byte{0xFF, 0xD8, 0xFF, 0xC4}) {
		t.Errorf("want SOI then DHT, got % x", enc[:4])
	}
	sof := bytes.Index(enc, []byte{0xFF, 0xC3})
	if sof < 0 {
		t.Fatal("no SOF3")
	}
	// length 14, P=12, Y=1, X=2, Nf=2, comp0 (0,0x11,0), comp1 (1,0x11,0)
	want := []byte{0x00, 0x0E, 12, 0, 1, 0, 2, 2, 0, 0x11, 0, 1, 0x11, 0}
	if got := enc[sof+2 : sof+2+len(want)]; !bytes.Equal(got, want) {
		t.Errorf("SOF3 % x, want % x", got, want)
	}
	if !bytes.HasSuffix(enc, []byte{0xFF, 0xD9}) {
		t.Error("missing EOI")
	}
}

func TestEncodeRejectsBadInput(t *testing.T) {
	if _, err := Encode(make([]uint16, 3), 2, 2, 1, 16); err == nil {
		t.Error("wrong sample count accepted")
	}
	if _, err := Encode(make([]uint16, 4), 2, 2, 1, 17); err == nil {
		t.Error("precision 17 accepted")
	}
}

func TestDecodeRejectsGarbage(t *testing.T) {
	for _, b := range [][]byte{nil, {0xFF}, {0xFF, 0xD8, 0xFF, 0xC4, 0x00}, {0xFF, 0xD8, 0x12, 0x34, 0x00, 0x02}} {
		if _, err := Decode(b); err == nil {
			t.Errorf("% x accepted", b)
		}
	}
}
