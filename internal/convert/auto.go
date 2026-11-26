package convert

import (
	"encoding/binary"
	"math"
	"slices"

	"rawconv/internal/prr"
)

// AutoColor is white balance and exposure measured from a frame's pixels,
// for clips whose header lacks them. It is measured once, on the first
// frame, so the whole clip gets one setting and does not flicker. The zero
// value measured nothing.
type AutoColor struct {
	HasWB         bool
	WBRed, WBBlue float64 // multipliers like the header's, green = 1
	HasExposure   bool
	Exposure      float64 // BaselineExposure, in stops
}

// Auto measurement settings.
const (
	// autoSampleStep measures every 4th CFA quad in each direction: a 5.7K
	// frame still gives about a quarter of a million quads, at a sixteenth
	// of the cost.
	autoSampleStep = 4
	// cfaQuad is the side of one RGGB quad.
	cfaQuad = 2
	// autoClipLevel and autoDarkLevel (normalised 0..1) leave clipped
	// highlights, whose colour is the sensor's limit rather than the light,
	// and near-black sensels, which are mostly noise, out of white balance.
	autoClipLevel = 0.9
	autoDarkLevel = 0.002
	// minAutoWB and maxAutoWB bound the measured multipliers to what real
	// light gives; outside them the frame is one strong colour, not a cast.
	minAutoWB = 0.25
	maxAutoWB = 8
	// autoMidGreyStops is where the median green sensel lands after
	// BaselineExposure, in stops below white, on clips whose exposure is
	// checked: -6.72 on a Panasonic GH7, -6.37 on a Sony through an Atomos
	// Shogun Inferno. Auto exposure puts a clip's median there.
	autoMidGreyStops = -6.5
	// typicalExposure is the BaselineExposure of those checked clips (GH7
	// -0.97, Sony -0.45); auto exposure stays within maxAutoExposure stops
	// of it either way, so a black or blown-out first frame cannot push
	// the clip to an extreme.
	typicalExposure = -0.7
	maxAutoExposure = 4
)

// NeedsAuto reports whether h lacks white balance or exposure, so the clip
// needs MeasureAuto.
func NeedsAuto(h prr.Header) bool {
	return !validFactor(h.WBRed) || !validFactor(h.WBBlue) || !validFactor(h.Gain)
}

// MeasureAuto measures white balance (grey world: the frame's average
// colour is taken as neutral) and exposure (the median green lands on
// autoMidGreyStops) from a bayer_rggb16le frame described by h. What it
// cannot measure, such as either on a black frame, is left out.
func MeasureAuto(data []byte, h prr.Header) AutoColor {
	var auto AutoColor
	if h.Width < cfaQuad || h.Height < cfaQuad || len(data) < h.Width*h.Height*bytesPerSensel {
		return auto
	}
	black := float64(h.BlackLevel())
	span := float64(h.WhiteLevel()) - black
	if span <= 0 {
		return auto
	}
	sensel := func(x, y int) float64 {
		v := float64(binary.LittleEndian.Uint16(data[bytesPerSensel*(y*h.Width+x):]))
		return (v - black) / span
	}

	var sumR, sumG, sumB float64
	var greens []float64
	const step = cfaQuad * autoSampleStep
	for y := 0; y+1 < h.Height; y += step {
		for x := 0; x+1 < h.Width; x += step {
			r, g1, g2, b := sensel(x, y), sensel(x+1, y), sensel(x, y+1), sensel(x+1, y+1)
			g := (g1 + g2) / 2
			greens = append(greens, g)
			if max(r, g1, g2, b) >= autoClipLevel || min(r, g, b) <= autoDarkLevel {
				continue
			}
			sumR, sumG, sumB = sumR+r, sumG+g, sumB+b
		}
	}

	if sumR > 0 && sumB > 0 {
		wbR, wbB := sumG/sumR, sumG/sumB
		if inRange(wbR, minAutoWB, maxAutoWB) && inRange(wbB, minAutoWB, maxAutoWB) {
			auto.HasWB, auto.WBRed, auto.WBBlue = true, wbR, wbB
		}
	}
	slices.Sort(greens)
	if median := greens[len(greens)/2]; median > 0 {
		auto.HasExposure = true
		ev := autoMidGreyStops - math.Log2(median)
		auto.Exposure = max(typicalExposure-maxAutoExposure, min(typicalExposure+maxAutoExposure, ev))
	}
	return auto
}

// validFactor reports whether a header multiplier is usable: positive and finite.
func validFactor(x float64) bool { return x > 0 && !math.IsNaN(x) && !math.IsInf(x, 0) }

func inRange(x, lo, hi float64) bool { return x >= lo && x <= hi }
