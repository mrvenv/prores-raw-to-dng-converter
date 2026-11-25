package dng

import (
	"fmt"
	"math"

	"rawconv/internal/dctjpeg"
)

// DCT tiles for DaVinci Resolve.
//
// DNG allows lossy (DCT) JPEG only for demosaiced images, but Resolve's
// JPEG decoder reads it in CFA tiles too. The layout below is the one of
// CinemaDNG files Resolve is known to decode: a tile is stored as one
// sample stream, a JPEG of half the tile's rows with two interleaved
// components, so one JPEG row holds two CFA rows. With the tile width a
// multiple of 16, the left half of each JPEG row is a red/green CFA row and
// the right half a green/blue one, split on a block boundary, so every 8x8
// DCT block sees a single CFA colour.
const (
	// dctTileLength is the tile height of those files.
	dctTileLength = 304
	// dctTileAlign keeps half a tile row a whole number of DCT blocks.
	dctTileAlign = 2 * dctjpeg.BlockSize
	// dctTileColumns: a tile spans half the frame width, as in those files.
	dctTileColumns = 2
	// dctBitsPerSample is the BitsPerSample of those files; the JPEG data
	// itself is 12-bit and goes through the 4096-entry LinearizationTable.
	dctBitsPerSample = 16
	// The quantisation scale search range (dctjpeg.ScaledQuant): from near
	// lossless to every table entry at its 8-bit maximum.
	minQuantScale = 1.0 / 16
	maxQuantScale = 32.0
	// quantSearchSteps halves the log range of the search each step.
	quantSearchSteps = 10
)

// dctTiling is the tile layout of f in a Resolve mode.
func dctTiling(f *Frame) tiling {
	half := (f.Width + dctTileColumns - 1) / dctTileColumns
	return tiling{(half + dctTileAlign - 1) / dctTileAlign * dctTileAlign, dctTileLength}
}

// encodeDCT stores f, through the 12-bit square-root curve, as DCT JPEG
// tiles quantised with f.QuantScale.
func encodeDCT(f *Frame) (encodedImage, error) {
	if f.QuantScale <= 0 {
		return encodedImage{}, fmt.Errorf("dng: %v needs a QuantScale (see CalibrateQuant)", f.Compression)
	}
	q := dctjpeg.ScaledQuant(f.QuantScale)
	return dctTiling(f).encode(f, curveFor(levels12, f.BlackLevel), dctBitsPerSample, dctTileEncoder(&q))
}

// dctTileEncoder encodes a tile as a DCT JPEG of half its rows with two
// components (see the layout above).
func dctTileEncoder(q *dctjpeg.Quant) tileEncoder {
	return func(buf []uint16, t tiling) ([]byte, error) {
		return dctjpeg.Encode(buf, t.width, t.height/ljpegComponents, ljpegComponents, q)
	}
}

// CalibrateQuant finds the finest quantisation (best quality) at which f,
// stored with f.Compression, reaches the mode's target ratio. It returns
// the scale and the ratio it gives; if even the coarsest table misses the
// target, it returns that and the ratio reached.
func CalibrateQuant(f *Frame) (float64, float64, error) {
	target := f.Compression.TargetRatio()
	if !f.Compression.IsDCT() || target <= 0 {
		return 0, 0, fmt.Errorf("dng: %v is not a DCT mode", f.Compression)
	}
	t, c := dctTiling(f), curveFor(levels12, f.BlackLevel)
	ratio := func(scale float64) (float64, error) {
		q := dctjpeg.ScaledQuant(scale)
		return t.estimateRatio(f, c, dctTileEncoder(&q))
	}
	lo, hi := minQuantScale, maxQuantScale
	hiRatio, err := ratio(hi)
	if err != nil || hiRatio < target {
		return hi, hiRatio, err
	}
	loRatio, err := ratio(lo)
	if err != nil || loRatio >= target {
		return lo, loRatio, err
	}
	// Invariant: ratio(lo) < target <= ratio(hi). Bisect geometrically:
	// size changes with log(scale).
	for range quantSearchSteps {
		mid := math.Sqrt(lo * hi)
		r, err := ratio(mid)
		if err != nil {
			return 0, 0, err
		}
		if r >= target {
			hi, hiRatio = mid, r
		} else {
			lo = mid
		}
	}
	return hi, hiRatio, nil
}
