package convert

import (
	"math"

	"rawconv/internal/prr"
)

type mat3 [9]float64

const (
	// singularDeterminant: matrices with a smaller determinant are treated
	// as not invertible.
	singularDeterminant = 1e-12
	// gainExposureOffset is subtracted from log2(ProRes RAW gain) to get
	// BaselineExposure. The gain alone left clips 4 stops brighter in
	// DaVinci Resolve than the ProRes RAW source; DNGs from another
	// converter that Resolve shows at the source's brightness carry exactly
	// log2(gain) - 4 (GH7: gain 8.1459 -> -0.97393). Likely cause: the gain
	// refers to 12-bit sensel values, ours are scaled to 16 bits (2^4).
	gainExposureOffset = 4
)

var d65 = [3]float64{0.95047, 1.0, 1.08883}

func (a mat3) mulVec(v [3]float64) [3]float64 {
	return [3]float64{
		a[0]*v[0] + a[1]*v[1] + a[2]*v[2],
		a[3]*v[0] + a[4]*v[1] + a[5]*v[2],
		a[6]*v[0] + a[7]*v[1] + a[8]*v[2],
	}
}

func (a mat3) inverse() (mat3, bool) {
	det := a[0]*(a[4]*a[8]-a[5]*a[7]) - a[1]*(a[3]*a[8]-a[5]*a[6]) + a[2]*(a[3]*a[7]-a[4]*a[6])
	if math.Abs(det) < singularDeterminant || math.IsNaN(det) {
		return mat3{}, false
	}
	inv := mat3{
		a[4]*a[8] - a[5]*a[7], a[2]*a[7] - a[1]*a[8], a[1]*a[5] - a[2]*a[4],
		a[5]*a[6] - a[3]*a[8], a[0]*a[8] - a[2]*a[6], a[2]*a[3] - a[0]*a[5],
		a[3]*a[7] - a[4]*a[6], a[1]*a[6] - a[0]*a[7], a[0]*a[4] - a[1]*a[3],
	}
	for i := range inv {
		inv[i] /= det
	}
	return inv, true
}

// chromaDist is the distance between the xy chromaticities of two XYZ values.
func chromaDist(a, b [3]float64) float64 {
	sa, sb := a[0]+a[1]+a[2], b[0]+b[1]+b[2]
	if sa == 0 || sb == 0 {
		return math.Inf(1)
	}
	return math.Hypot(a[0]/sa-b[0]/sb, a[1]/sa-b[1]/sb)
}

// ColorInfo is the DNG colour description derived from a ProRes RAW header.
type ColorInfo struct {
	ColorMatrix1     [9]float64 // XYZ -> camera
	AsShotNeutral    [3]float64
	BaselineExposure float64
	PostWB           bool // header matrix applies to white-balanced RGB
	Fallback         bool // header matrix unusable, sRGB used instead
	AutoWB           bool // header had no white balance; measured from pixels
	AutoExposure     bool // header had no gain; exposure measured from pixels
}

// sRGB(D65) XYZ->linear RGB, used if the header matrix is singular.
var srgbFromXYZ = mat3{3.2406, -1.5372, -0.4986, -0.9689, 1.8758, 0.0415, 0.0557, -0.2040, 1.0570}

// ColorFromHeader derives DNG colour tags. ProRes RAW provides a
// camera->XYZ(D65) matrix; whether it expects white-balanced input is
// detected by checking which interpretation maps a neutral to D65 white.
// White balance or exposure missing from the header comes from auto when
// it measured them; otherwise white balance is left neutral and exposure 0.
func ColorFromHeader(h prr.Header, auto AutoColor) ColorInfo {
	var ci ColorInfo
	wbR, wbB := h.WBRed, h.WBBlue
	if !validFactor(wbR) || !validFactor(wbB) {
		wbR, wbB = 1, 1
		if auto.HasWB {
			wbR, wbB, ci.AutoWB = auto.WBRed, auto.WBBlue, true
		}
	}
	ci.AsShotNeutral = [3]float64{1 / wbR, 1, 1 / wbB}
	switch {
	case validFactor(h.Gain):
		ci.BaselineExposure = math.Log2(h.Gain) - gainExposureOffset
	case auto.HasExposure:
		ci.BaselineExposure, ci.AutoExposure = auto.Exposure, true
	}

	m := mat3(h.ColorMatrix)
	cm, ok := m.inverse()
	if !ok {
		ci.Fallback = true
		cm = srgbFromXYZ
	} else {
		ci.PostWB = chromaDist(m.mulVec([3]float64{1, 1, 1}), d65) <
			chromaDist(m.mulVec(ci.AsShotNeutral), d65)
	}
	if ci.PostWB || ci.Fallback {
		// XYZ -> white-balanced camera, then undo WB to get raw camera space.
		for c := 0; c < 3; c++ {
			cm[c] /= wbR
			cm[6+c] /= wbB
		}
	}
	// DNG convention: normalise so the illuminant white has max channel 1.
	w := cm.mulVec(d65)
	if mx := math.Max(w[0], math.Max(w[1], w[2])); mx > 0 {
		for i := range cm {
			cm[i] /= mx
		}
	}
	ci.ColorMatrix1 = cm
	return ci
}
