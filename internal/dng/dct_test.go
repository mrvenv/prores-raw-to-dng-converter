package dng

import (
	"bytes"
	"encoding/binary"
	"math"
	"testing"

	"rawconv/internal/dctjpeg"
)

func TestDCTTilingSplitsRowsOnBlocks(t *testing.T) {
	for width, want := range map[int]int{5728: 2864, 4112: 2064, 100: 64} {
		tl := dctTiling(&Frame{Width: width})
		if tl.width != want || tl.height != dctTileLength {
			t.Errorf("width %d: tile %dx%d, want %dx%d", width, tl.width, tl.height, want, dctTileLength)
		}
		if (tl.width/2)%dctjpeg.BlockSize != 0 {
			t.Errorf("width %d: half tile %d splits a DCT block", width, tl.width/2)
		}
	}
}

// decodeDCTFrame rebuilds the linear 16-bit image of a DCT DNG.
func decodeDCTFrame(t *testing.T, file []byte, w, h int) []float64 {
	t.Helper()
	p := parseTIFF(t, file)
	le := binary.LittleEndian
	tw, th := int(le.Uint32(p.tags[tagTileWidth])), int(le.Uint32(p.tags[tagTileLength]))
	lin := p.tags[tagLinearizationTable]
	offs, counts := p.tags[tagTileOffsets], p.tags[tagTileByteCounts]
	tilesX := (w + tw - 1) / tw
	img := make([]float64, w*h)
	for i := 0; i < len(offs)/4; i++ {
		o, n := le.Uint32(offs[4*i:]), le.Uint32(counts[4*i:])
		d, err := dctjpeg.Decode(file[o : o+n])
		if err != nil {
			t.Fatalf("tile %d: %v", i, err)
		}
		x0, y0 := i%tilesX*tw, i/tilesX*th
		for k, v := range d.Samples { // one stream per tile, row-major
			x, y := x0+k%tw, y0+k/tw
			if x < w && y < h {
				img[y*w+x] = float64(le.Uint16(lin[2*int(v):]))
			}
		}
	}
	return img
}

func TestWriteDCTRoundTrip(t *testing.T) {
	const w, h = 600, 400
	f := noisyFrame(w, h, 64)
	f.BlackLevel, f.Compression, f.QuantScale = 256, ResolveRatio8, 0.5
	var buf bytes.Buffer
	if err := Write(&buf, f); err != nil {
		t.Fatal(err)
	}
	p := parseTIFF(t, buf.Bytes())
	le := binary.LittleEndian
	if c := le.Uint16(p.tags[tagCompression]); c != compressionJPEG {
		t.Errorf("Compression %d", c)
	}
	if bps := le.Uint16(p.tags[tagBitsPerSample]); bps != dctBitsPerSample {
		t.Errorf("BitsPerSample %d", bps)
	}
	if n := len(p.tags[tagLinearizationTable]) / 2; n != levels12 {
		t.Errorf("LinearizationTable has %d entries", n)
	}
	got := decodeDCTFrame(t, buf.Bytes(), w, h)
	var se float64
	for i := range got {
		d := got[i] - float64(le.Uint16(f.Data[2*i:]))
		se += d * d
	}
	// A wrong tile layout scrambles pixels across a 60000-wide gradient.
	if rms := math.Sqrt(se / float64(w*h)); rms > 300 {
		t.Errorf("rms error %.0f of 65535", rms)
	}
}

func TestCalibrateQuantReachesTarget(t *testing.T) {
	const w, h = 1200, 608
	for _, c := range []Compression{ResolveRatio5, ResolveRatio12} {
		f := noisyFrame(w, h, 256)
		f.Compression = c
		scale, est, err := CalibrateQuant(f)
		if err != nil {
			t.Fatal(err)
		}
		f.QuantScale = scale
		var buf bytes.Buffer
		if err := Write(&buf, f); err != nil {
			t.Fatal(err)
		}
		stored := 0
		cnts := parseTIFF(t, buf.Bytes()).tags[tagTileByteCounts]
		for i := 0; i < len(cnts)/4; i++ {
			stored += int(binary.LittleEndian.Uint32(cnts[4*i:]))
		}
		actual := float64(w*h*referenceBits/bitsPerByte) / float64(stored)
		t.Logf("%v: scale %.2f, estimated %.2f:1, actual %.2f:1", c, scale, est, actual)
		if scale < maxQuantScale && (est < c.TargetRatio() || actual < c.TargetRatio()*0.85) {
			t.Errorf("%v: missed the target: estimated %.2f, actual %.2f", c, est, actual)
		}
	}
}

func TestDCTModeRequiresScale(t *testing.T) {
	f := noisyFrame(64, 32, 16)
	f.Compression = ResolveRatio3
	if err := Write(&bytes.Buffer{}, f); err == nil {
		t.Error("ResolveRatio3 without QuantScale accepted")
	}
	if _, _, err := CalibrateQuant(&Frame{Compression: Ratio3}); err == nil {
		t.Error("CalibrateQuant accepted a curve mode")
	}
	if _, _, err := CalibrateLevels(&Frame{Compression: ResolveRatio3}); err == nil {
		t.Error("CalibrateLevels accepted a DCT mode")
	}
}
