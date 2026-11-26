package convert

import (
	"encoding/binary"
	"math"
	"testing"

	"rawconv/internal/prr"
)

// gh7Header holds Panasonic GH7 header values; DNGs Resolve shows at the
// source's brightness carry BaselineExposure -0.97393 for this gain.
var gh7Header = prr.Header{Gain: 8.1459, WBRed: 2.31934, WBBlue: 1.63281}

const gh7BaselineExposure = -0.97393

const tolerance = 1e-4

func TestBaselineExposureFromGain(t *testing.T) {
	if got := ColorFromHeader(gh7Header, AutoColor{}).BaselineExposure; math.Abs(got-gh7BaselineExposure) > tolerance {
		t.Errorf("BaselineExposure %.5f, want %.5f", got, gh7BaselineExposure)
	}
	if got := ColorFromHeader(prr.Header{}, AutoColor{}).BaselineExposure; got != 0 {
		t.Errorf("no gain, nothing measured: BaselineExposure %v, want 0", got)
	}
}

// A header's own white balance and gain win over a measurement.
func TestColorFromHeaderIgnoresAutoWhenHeaderHasValues(t *testing.T) {
	auto := AutoColor{HasWB: true, WBRed: 5, WBBlue: 5, HasExposure: true, Exposure: 3}
	ci := ColorFromHeader(gh7Header, auto)
	if ci.AutoWB || ci.AutoExposure {
		t.Fatalf("AutoWB=%v AutoExposure=%v, want both false", ci.AutoWB, ci.AutoExposure)
	}
	if math.Abs(ci.AsShotNeutral[0]-1/gh7Header.WBRed) > tolerance || math.Abs(ci.BaselineExposure-gh7BaselineExposure) > tolerance {
		t.Errorf("AsShotNeutral %v BaselineExposure %v, want the header's", ci.AsShotNeutral, ci.BaselineExposure)
	}
}

func TestColorFromHeaderUsesAutoForMissingValues(t *testing.T) {
	auto := AutoColor{HasWB: true, WBRed: 2, WBBlue: 1.25, HasExposure: true, Exposure: 1.5}
	ci := ColorFromHeader(prr.Header{}, auto)
	if !ci.AutoWB || !ci.AutoExposure {
		t.Fatalf("AutoWB=%v AutoExposure=%v, want both true", ci.AutoWB, ci.AutoExposure)
	}
	want := [3]float64{0.5, 1, 0.8}
	for i := range want {
		if math.Abs(ci.AsShotNeutral[i]-want[i]) > tolerance {
			t.Errorf("AsShotNeutral %v, want %v", ci.AsShotNeutral, want)
			break
		}
	}
	if ci.BaselineExposure != auto.Exposure {
		t.Errorf("BaselineExposure %v, want %v", ci.BaselineExposure, auto.Exposure)
	}
}

// Synthetic frame geometry and levels for MeasureAuto.
const (
	testWidth  = 64
	testHeight = 32
	testRange  = 4096
	testBlack  = 0x100
)

// bayerFrame builds a bayer_rggb16le frame whose every quad has the given
// normalised R, G and B, except quads for which clipped returns true,
// which are white.
func bayerFrame(r, g, b float64, clipped func(qx, qy int) bool) ([]byte, prr.Header) {
	h := prr.Header{Width: testWidth, Height: testHeight, SenselValueRange: testRange}
	data := make([]byte, testWidth*testHeight*bytesPerSensel)
	put := func(x, y int, v float64) {
		binary.LittleEndian.PutUint16(data[bytesPerSensel*(y*testWidth+x):], uint16(testBlack+v*testRange))
	}
	for y := 0; y < testHeight; y += cfaQuad {
		for x := 0; x < testWidth; x += cfaQuad {
			qr, qg, qb := r, g, b
			if clipped != nil && clipped(x/cfaQuad, y/cfaQuad) {
				qr, qg, qb = 1, 1, 1
			}
			put(x, y, qr)
			put(x+1, y, qg)
			put(x, y+1, qg)
			put(x+1, y+1, qb)
		}
	}
	return data, h
}

func TestMeasureAuto(t *testing.T) {
	const r, g, b = 0.1, 0.2, 0.16
	// A quarter of the sampled quads are clipped: white balance must ignore
	// them, and the median green stays g.
	data, h := bayerFrame(r, g, b, func(qx, qy int) bool {
		return qx%(2*autoSampleStep) == autoSampleStep && qy%(2*autoSampleStep) == autoSampleStep
	})
	auto := MeasureAuto(data, h)
	if !auto.HasWB || math.Abs(auto.WBRed-g/r) > 0.01 || math.Abs(auto.WBBlue-g/b) > 0.01 {
		t.Errorf("WB %+v, want R=%.3f B=%.3f", auto, g/r, g/b)
	}
	if want := autoMidGreyStops - math.Log2(g); !auto.HasExposure || math.Abs(auto.Exposure-want) > 0.01 {
		t.Errorf("Exposure %+v, want %.3f", auto, want)
	}
}

// A nearly black first frame does not push exposure past the bound.
func TestMeasureAutoExposureBound(t *testing.T) {
	const dim = 0.0005
	data, h := bayerFrame(dim, dim, dim, nil)
	auto := MeasureAuto(data, h)
	if want := typicalExposure + maxAutoExposure; !auto.HasExposure || math.Abs(auto.Exposure-want) > tolerance {
		t.Errorf("Exposure %+v, want the bound %.2f", auto, want)
	}
}

func TestMeasureAutoBlackFrame(t *testing.T) {
	data, h := bayerFrame(0, 0, 0, nil)
	if auto := MeasureAuto(data, h); auto.HasWB || auto.HasExposure {
		t.Errorf("black frame measured %+v, want nothing", auto)
	}
}

func TestMeasureAutoShortBuffer(t *testing.T) {
	data, h := bayerFrame(0.1, 0.2, 0.1, nil)
	if auto := MeasureAuto(data[:len(data)/2], h); auto.HasWB || auto.HasExposure {
		t.Errorf("short buffer measured %+v, want nothing", auto)
	}
}

func TestNeedsAuto(t *testing.T) {
	if NeedsAuto(gh7Header) {
		t.Error("GH7 header needs auto, want not")
	}
	if !NeedsAuto(prr.Header{WBRed: 2, WBBlue: 1.5}) {
		t.Error("header without gain does not need auto, want it to")
	}
	if !NeedsAuto(prr.Header{Gain: 8, WBRed: math.NaN(), WBBlue: 1.5}) {
		t.Error("header with NaN white balance does not need auto, want it to")
	}
}
