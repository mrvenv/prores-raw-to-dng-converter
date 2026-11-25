package dng

import (
	"bytes"
	"encoding/binary"
	"slices"
	"testing"
	"time"

	"rawconv/internal/ljpeg"
)

type parsed struct {
	tags map[uint16][]byte // raw value bytes
	typ  map[uint16]uint16
}

func parseTIFF(t *testing.T, b []byte) parsed {
	t.Helper()
	if string(b[:4]) != "II*\x00" {
		t.Fatalf("bad header %q", b[:4])
	}
	return parseIFD(t, b, int(binary.LittleEndian.Uint32(b[4:])))
}

func parseIFD(t *testing.T, b []byte, off int) parsed {
	t.Helper()
	n := int(binary.LittleEndian.Uint16(b[off:]))
	p := parsed{tags: map[uint16][]byte{}, typ: map[uint16]uint16{}}
	prev := uint16(0)
	for i := 0; i < n; i++ {
		e := b[off+2+12*i:]
		tag := binary.LittleEndian.Uint16(e)
		if tag <= prev {
			t.Fatalf("tags not ascending: %d after %d", tag, prev)
		}
		prev = tag
		typ := binary.LittleEndian.Uint16(e[2:])
		cnt := int(binary.LittleEndian.Uint32(e[4:]))
		size := cnt * sizeOf(typ)
		var v []byte
		if size <= 4 {
			v = e[8 : 8+size]
		} else {
			o := int(binary.LittleEndian.Uint32(e[8:]))
			if o%2 != 0 {
				t.Errorf("tag %d at odd offset %d", tag, o)
			}
			v = b[o : o+size]
		}
		p.tags[tag], p.typ[tag] = v, typ
	}
	return p
}

func TestWriteRoundTrip(t *testing.T) {
	const w, h = 8, 6
	data := make([]byte, w*h*2)
	for i := 0; i < w*h; i++ {
		binary.LittleEndian.PutUint16(data[2*i:], uint16(i*1000))
	}
	tc := [8]byte{0x05, 0x01, 0x01, 0x01}
	f := &Frame{
		Width: w, Height: h, Data: data,
		Make: "Test", Model: "Cam", Software: "rawconv",
		BlackLevel: 256, WhiteLevel: 4351,
		ColorMatrix1:  [9]float64{1, -0.5, 0, 0, 1, 0, 0.25, 0, 1},
		AsShotNeutral: [3]float64{0.5, 1, 0.6},
		CropX:         2, CropY: 2, CropW: 4, CropH: 2,
		TimeCode:   &tc,
		FrameRateN: 24000, FrameRateD: 1001,
	}
	var buf bytes.Buffer
	if err := Write(&buf, f); err != nil {
		t.Fatal(err)
	}
	b := buf.Bytes()
	p := parseTIFF(t, b)

	u32 := func(tag uint16) uint32 { return binary.LittleEndian.Uint32(p.tags[tag]) }
	u16 := func(tag uint16) uint16 { return binary.LittleEndian.Uint16(p.tags[tag]) }
	if u32(256) != w || u32(257) != h {
		t.Errorf("size %dx%d", u32(256), u32(257))
	}
	if u16(262) != 32803 {
		t.Errorf("photometric %d", u16(262))
	}
	if !bytes.Equal(p.tags[33422], []byte{0, 1, 1, 2}) {
		t.Errorf("CFA %v", p.tags[33422])
	}
	if u32(50714) != 256 || u32(50717) != 4351 {
		t.Errorf("black/white %d/%d", u32(50714), u32(50717))
	}
	if !bytes.Equal(p.tags[51043], tc[:]) {
		t.Errorf("timecode %x", p.tags[51043])
	}
	fr := p.tags[51044]
	if binary.LittleEndian.Uint32(fr) != 24000 || binary.LittleEndian.Uint32(fr[4:]) != 1001 {
		t.Errorf("framerate %x", fr)
	}
	cm := p.tags[50721]
	num := int32(binary.LittleEndian.Uint32(cm[8:]))
	den := int32(binary.LittleEndian.Uint32(cm[12:]))
	if float64(num)/float64(den) != -0.5 {
		t.Errorf("ColorMatrix1[1] = %d/%d", num, den)
	}
	if string(p.tags[50708]) != "ProRes RAW Camera\x00" {
		t.Errorf("UniqueCameraModel %q", p.tags[50708])
	}
	so, sc := int(u32(273)), int(u32(279))
	if sc != len(data) || so+sc != len(b) {
		t.Fatalf("strip offset %d count %d file %d", so, sc, len(b))
	}
	if !bytes.Equal(b[so:so+sc], data) {
		t.Error("pixel data mismatch")
	}
}

// decodeTiles reassembles the CFA image from a tiled lossless-JPEG DNG.
func decodeTiles(t *testing.T, b []byte, p parsed, w, h int) []uint16 {
	t.Helper()
	tw := int(binary.LittleEndian.Uint32(p.tags[tagTileWidth]))
	th := int(binary.LittleEndian.Uint32(p.tags[tagTileLength]))
	offs, cnts := p.tags[tagTileOffsets], p.tags[tagTileByteCounts]
	tilesX := (w + tw - 1) / tw
	out := make([]uint16, w*h)
	for i := 0; i < len(offs)/4; i++ {
		o := int(binary.LittleEndian.Uint32(offs[4*i:]))
		n := int(binary.LittleEndian.Uint32(cnts[4*i:]))
		img, err := ljpeg.Decode(b[o : o+n])
		if err != nil {
			t.Fatalf("tile %d: %v", i, err)
		}
		if img.Width*img.Components != tw || img.Height != th {
			t.Fatalf("tile %d is %dx%d", i, img.Width*img.Components, img.Height)
		}
		x0, y0 := (i%tilesX)*tw, (i/tilesX)*th
		for y := 0; y < th && y0+y < h; y++ {
			for x := 0; x < tw && x0+x < w; x++ {
				out[(y0+y)*w+x0+x] = img.Samples[y*tw+x]
			}
		}
	}
	return out
}

func TestWriteLosslessJPEG(t *testing.T) {
	// 300x260 is not a multiple of the tile size: exercises edge padding.
	const w, h = 300, 260
	data := make([]byte, w*h*2)
	want := make([]uint16, w*h)
	for i := range want {
		want[i] = uint16(256 + (i*7919)%61000)
		binary.LittleEndian.PutUint16(data[2*i:], want[i])
	}
	for _, c := range []Compression{LosslessJPEG, LosslessJPEG12} {
		var buf bytes.Buffer
		if err := Write(&buf, &Frame{Width: w, Height: h, Data: data, Compression: c}); err != nil {
			t.Fatal(err)
		}
		b := buf.Bytes()
		p := parseTIFF(t, b)
		if got := binary.LittleEndian.Uint16(p.tags[tagCompression]); got != compressionJPEG {
			t.Fatalf("%v: compression tag %d", c, got)
		}
		got := decodeTiles(t, b, p, w, h)
		if c == LosslessJPEG {
			if !slices.Equal(got, want) {
				t.Error("lossless round trip differs")
			}
			continue
		}
		lin := p.tags[tagLinearizationTable]
		if len(lin) != levels12*2 {
			t.Fatalf("linearization table is %d bytes", len(lin))
		}
		for i, code := range got {
			v := int(binary.LittleEndian.Uint16(lin[2*int(code):]))
			// Max error is half a curve step: 65535*2*4095/4095^2/2 ≈ 16 at the top.
			if d := abs(v - int(want[i])); d > 17 {
				t.Fatalf("sample %d: %d -> %d (error %d)", i, want[i], v, d)
			}
		}
	}
}

func TestWriteExif(t *testing.T) {
	const w, h = 4, 2
	shot := time.Date(2018, 4, 12, 1, 37, 4, 0, time.UTC)
	f := &Frame{
		Width: w, Height: h, Data: make([]byte, w*h*2),
		Make: "Sony", Description: "Recorded on Atomos ShogunInferno 9.0", DateTime: shot,
		Exif: Exif{ISO: 4000, FNumber: 1.0, ExposureTime: 0.02},
	}
	var buf bytes.Buffer
	if err := Write(&buf, f); err != nil {
		t.Fatal(err)
	}
	b := buf.Bytes()
	p := parseTIFF(t, b)
	if got := string(p.tags[tagImageDescription]); got != f.Description+"\x00" {
		t.Errorf("description %q", got)
	}
	if got := string(p.tags[tagDateTime]); got != "2018:04:12 01:37:04\x00" {
		t.Errorf("DateTime %q", got)
	}
	exif := parseIFD(t, b, int(binary.LittleEndian.Uint32(p.tags[tagExifIFD])))
	if iso := binary.LittleEndian.Uint16(exif.tags[exifISO]); iso != 4000 {
		t.Errorf("ISO %d", iso)
	}
	et := exif.tags[exifExposureTime]
	if num, den := binary.LittleEndian.Uint32(et), binary.LittleEndian.Uint32(et[4:]); float64(num)/float64(den) != 0.02 {
		t.Errorf("ExposureTime %d/%d", num, den)
	}
	if got := string(exif.tags[exifDateTimeOriginal]); got != "2018:04:12 01:37:04\x00" {
		t.Errorf("DateTimeOriginal %q", got)
	}
	if got := string(exif.tags[exifVersion]); got != "0230" {
		t.Errorf("ExifVersion %q", got)
	}
	// Pixel data still lands where StripOffsets says.
	so := int(binary.LittleEndian.Uint32(p.tags[tagStripOffsets]))
	if so+w*h*2 != len(b) {
		t.Errorf("strip at %d, file %d bytes", so, len(b))
	}
}

func TestWriteWithoutExif(t *testing.T) {
	var buf bytes.Buffer
	if err := Write(&buf, &Frame{Width: 2, Height: 2, Data: make([]byte, 8)}); err != nil {
		t.Fatal(err)
	}
	if _, ok := parseTIFF(t, buf.Bytes()).tags[tagExifIFD]; ok {
		t.Error("EXIF IFD written for a frame with no EXIF data")
	}
}

func TestParseCompression(t *testing.T) {
	for _, c := range Compressions {
		got, err := ParseCompression(c.String())
		if err != nil || got != c {
			t.Errorf("%v: got %v, %v", c, got, err)
		}
	}
	if _, err := ParseCompression("zip"); err == nil {
		t.Error("unknown name accepted")
	}
}

func TestWriteRejectsWrongSize(t *testing.T) {
	err := Write(&bytes.Buffer{}, &Frame{Width: 4, Height: 4, Data: make([]byte, 10)})
	if err == nil {
		t.Fatal("expected error")
	}
}

// noisyFrame is a gradient with jitter of the given amplitude, so that
// ratios respond to quantisation the way real sensor data does.
func noisyFrame(w, h, jitterAmp int) *Frame {
	data := make([]byte, w*h*2)
	seed := uint32(1)
	for i := 0; i < w*h; i++ {
		seed = seed*1664525 + 1013904223
		base := 256 + (i%w)*60000/w
		jitter := 0
		if jitterAmp > 0 {
			jitter = int(seed>>16)%(2*jitterAmp+1) - jitterAmp
		}
		binary.LittleEndian.PutUint16(data[2*i:], uint16(min(max(base+jitter, 0), 65535)))
	}
	return &Frame{Width: w, Height: h, Data: data}
}

func TestCalibrateLevelsReachesTarget(t *testing.T) {
	const w, h, jitter = 768, 512, 2048
	for _, c := range []Compression{Ratio3, Ratio5, Ratio8} {
		f := noisyFrame(w, h, jitter)
		f.Compression = c
		levels, est, err := CalibrateLevels(f)
		if err != nil {
			t.Fatal(err)
		}
		f.CurveLevels = levels
		var buf bytes.Buffer
		if err := Write(&buf, f); err != nil {
			t.Fatal(err)
		}
		p := parseTIFF(t, buf.Bytes())
		stored := 0
		cnts := p.tags[tagTileByteCounts]
		for i := 0; i < len(cnts)/4; i++ {
			stored += int(binary.LittleEndian.Uint32(cnts[4*i:]))
		}
		actual := float64(w*h*referenceBits/8) / float64(stored)
		t.Logf("%v: %d levels, estimated %.2f:1, actual %.2f:1", c, levels, est, actual)
		floor := noiseFloorLevels(f)
		if levels < floor {
			t.Errorf("%v: %d levels below the noise floor %d", c, levels, floor)
		}
		if est < c.TargetRatio() && levels != floor {
			t.Errorf("%v: estimate %.2f below target with %d levels", c, est, levels)
		}
		if levels > floor && actual < c.TargetRatio()*0.85 {
			t.Errorf("%v: actual ratio %.2f far below target", c, actual)
		}
		lin := p.tags[tagLinearizationTable]
		if got := len(lin) / 2; got != 1<<curvePrecision {
			t.Errorf("%v: linearization table has %d entries, want %d", c, got, 1<<curvePrecision)
		}
		if top := binary.LittleEndian.Uint16(lin[2*(levels-1):]); top != maxSample16 {
			t.Errorf("%v: top code %d maps to %d, want %d", c, levels-1, top, maxSample16)
		}
	}
}

func TestRatioModeRequiresLevels(t *testing.T) {
	f := noisyFrame(32, 32, 128)
	f.Compression = Ratio5
	if err := Write(&bytes.Buffer{}, f); err == nil {
		t.Error("Ratio5 without CurveLevels accepted")
	}
}

func TestCurveModesDeclare12Bits(t *testing.T) {
	f := noisyFrame(256, 256, 128)
	f.Compression, f.CurveLevels = Ratio8, minCurveLevels
	var buf bytes.Buffer
	if err := Write(&buf, f); err != nil {
		t.Fatal(err)
	}
	p := parseTIFF(t, buf.Bytes())
	if bps := binary.LittleEndian.Uint16(p.tags[tagBitsPerSample]); bps != curvePrecision {
		t.Errorf("BitsPerSample %d, want %d", bps, curvePrecision)
	}
}

// flatFrame is a uniform mid-grey with jitter of the given amplitude: the
// neighbour differences are pure noise.
func flatFrame(w, h, jitterAmp int) *Frame {
	const grey = 20000
	data := make([]byte, w*h*2)
	seed := uint32(1)
	for i := 0; i < w*h; i++ {
		seed = seed*1664525 + 1013904223
		jitter := 0
		if jitterAmp > 0 {
			jitter = int(seed>>16)%(2*jitterAmp+1) - jitterAmp
		}
		binary.LittleEndian.PutUint16(data[2*i:], uint16(grey+jitter))
	}
	return &Frame{Width: w, Height: h, Data: data}
}

func TestNoiseFloorFollowsNoise(t *testing.T) {
	clean := noiseFloorLevels(flatFrame(512, 512, 0))
	quiet := noiseFloorLevels(flatFrame(512, 512, 128))
	loud := noiseFloorLevels(flatFrame(512, 512, 1024))
	t.Logf("noise floor: clean %d, quiet %d, loud %d levels", clean, quiet, loud)
	if clean != levels12 {
		t.Errorf("noiseless frame: floor %d, want %d", clean, levels12)
	}
	if !(loud < quiet && quiet < clean) {
		t.Errorf("floor does not fall with noise: clean %d, quiet %d, loud %d", clean, quiet, loud)
	}
}
