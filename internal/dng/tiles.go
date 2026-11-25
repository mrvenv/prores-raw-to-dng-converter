package dng

import (
	"fmt"
	"math"
	"sync"

	"rawconv/internal/ljpeg"
)

const (
	// tileSize is the lossless tile edge in pixels. TIFF requires a
	// multiple of 16.
	tileSize = 256
	// ljpegComponents: each tile row is encoded as pixel pairs (R,G / G,B),
	// the layout Adobe's DNG SDK uses, so each predictor sees one colour.
	ljpegComponents = 2
	precision16     = 16
	// levels12 is the number of curve codes in the 12-bit mode.
	levels12 = 1 << 12
	// minCurveLevels is the absolute floor of the ratio search (6 bits).
	// The real floor is per clip, from its noise (noiseFloorLevels).
	minCurveLevels = 64
	// maxStepToNoise caps the curve step at this multiple of the clip's
	// noise. At 2 the quantisation adds about 16% to the noise and acts as
	// its own dither; coarser steps posterise shadows into colour blotches
	// once white balance scales red and blue apart.
	maxStepToNoise = 2.0
	// madToSigma turns the median absolute difference of two samples into
	// the noise sigma of one: the MAD of a normal is 0.6745 sigma and a
	// difference of two samples has sigma*sqrt(2).
	madToSigma = 1 / (0.6745 * math.Sqrt2)
	// curveFootroom is how far below the black level (16-bit units) the
	// curve starts, so noise just under black is not clipped.
	curveFootroom = 64
	// referenceBits is the bit depth compression ratios are quoted against
	// (uncompressed 12-bit raw), as camera makers quote "3:1".
	referenceBits = 12
	// calibrationTileStep: the ratio search encodes every n-th tile only.
	calibrationTileStep = 3
	// curvePrecision is the BitsPerSample of every curve mode, however few
	// codes the curve has. It costs nothing (lossless JPEG codes differences,
	// not values) and matches Blackmagic's own 3:1/4:1 CinemaDNG, which
	// readers are tested against: macOS renders frames below 8 bits black and
	// DaVinci Resolve garbles tiles of an 8-bit frame with a short table.
	curvePrecision = 12
	bitsPerByte    = 8
	// maxSample16 is the largest 16-bit sample value.
	maxSample16 = math.MaxUint16
)

// encodeTiled compresses f into lossless JPEG tiles.
func encodeTiled(f *Frame) (encodedImage, error) {
	levels, err := f.curveLevels()
	if err != nil {
		return encodedImage{}, err
	}
	var c *curve
	precision := precision16
	if levels > 0 {
		c = curveFor(levels, f.BlackLevel)
		precision = curvePrecision
	}
	return losslessTiling.encode(f, c, precision, losslessTileEncoder(precision))
}

// tileEncoder compresses one filled tile of the given tiling.
type tileEncoder func(buf []uint16, t tiling) ([]byte, error)

// losslessTileEncoder encodes a tile as lossless JPEG of pixel pairs.
func losslessTileEncoder(precision int) tileEncoder {
	return func(buf []uint16, t tiling) ([]byte, error) {
		return ljpeg.Encode(buf, t.width/ljpegComponents, t.height, ljpegComponents, precision)
	}
}

// tiling is a tile size; it lays a frame out as a row-major tile grid.
type tiling struct{ width, height int }

var losslessTiling = tiling{tileSize, tileSize}

func (t tiling) grid(f *Frame) (int, int) {
	return (f.Width + t.width - 1) / t.width, (f.Height + t.height - 1) / t.height
}

func (t tiling) count(f *Frame) int {
	x, y := t.grid(f)
	return x * y
}

// origin is the top-left pixel of tile i.
func (t tiling) origin(f *Frame, i int) (int, int) {
	tilesX, _ := t.grid(f)
	return (i % tilesX) * t.width, (i / tilesX) * t.height
}

// encode stores every tile of f through curve c with enc and returns the
// tile tags, declaring bitsPerSample.
func (t tiling) encode(f *Frame, c *curve, bitsPerSample int, enc tileEncoder) (encodedImage, error) {
	n := t.count(f)
	chunks := make([][]byte, 0, n)
	counts := make([]int, 0, n)
	buf := make([]uint16, t.width*t.height)
	for i := range n {
		data, err := t.encodeTile(buf, f, i, c, enc)
		if err != nil {
			return encodedImage{}, err
		}
		chunks = append(chunks, data)
		counts = append(counts, len(data))
	}
	img := encodedImage{
		entries: []entry{
			{tagBitsPerSample, typeShort, 1, shorts(bitsPerSample)},
			{tagCompression, typeShort, 1, shorts(compressionJPEG)},
			{tagTileWidth, typeLong, 1, longs(t.width)},
			{tagTileLength, typeLong, 1, longs(t.height)},
			{tagTileOffsets, typeLong, uint32(n), make([]byte, 4*n)},
			{tagTileByteCounts, typeLong, uint32(n), longs(counts...)},
		},
		offsetsTag: tagTileOffsets,
		chunks:     chunks,
	}
	if c != nil {
		img.entries = append(img.entries, entry{tagLinearizationTable, typeShort, uint32(len(c.table)), shorts(c.table...)})
	}
	return img, nil
}

// encodeTile fills tile i of f through c (nil = no curve) and encodes it.
func (t tiling) encodeTile(buf []uint16, f *Frame, i int, c *curve, enc tileEncoder) ([]byte, error) {
	x0, y0 := t.origin(f, i)
	t.fill(buf, f, x0, y0, c)
	data, err := enc(buf, t)
	if err != nil {
		return nil, fmt.Errorf("dng: tile at %d,%d: %w", x0, y0, err)
	}
	return data, nil
}

// fill copies the tile at (x0, y0) into buf. Pixels past the image edge
// repeat the last 2x2 Bayer cell so the CFA phase is kept; they are never
// displayed.
func (t tiling) fill(buf []uint16, f *Frame, x0, y0 int, c *curve) {
	for y := range t.height {
		sy := clampKeepingPhase(y0+y, f.Height)
		row := f.Data[sy*f.Width*bytesPerSample:]
		for x := range t.width {
			sx := clampKeepingPhase(x0+x, f.Width)
			v := le.Uint16(row[sx*bytesPerSample:])
			if c != nil {
				v = c.encode[v]
			}
			buf[y*t.width+x] = v
		}
	}
}

// sampled lists every calibrationTileStep-th tile. A step sharing a factor
// with the row length would sample the same columns on every row; it is
// made coprime so the samples spread over the frame.
func (t tiling) sampled(f *Frame) []int {
	tilesX, _ := t.grid(f)
	step := calibrationTileStep
	for gcd(step, tilesX) != 1 {
		step++
	}
	var tiles []int
	for i := 0; i < t.count(f); i += step {
		tiles = append(tiles, i)
	}
	return tiles
}

// estimateRatio encodes the sampled tiles through c with enc and
// extrapolates the ratio against uncompressed referenceBits raw.
func (t tiling) estimateRatio(f *Frame, c *curve, enc tileEncoder) (float64, error) {
	buf := make([]uint16, t.width*t.height)
	tiles := t.sampled(f)
	size := 0
	for _, i := range tiles {
		data, err := t.encodeTile(buf, f, i, c, enc)
		if err != nil {
			return 0, err
		}
		size += len(data)
	}
	stored := float64(size) * float64(t.count(f)) / float64(len(tiles))
	return float64(f.Width*f.Height) * referenceBits / bitsPerByte / stored, nil
}

// curveLevels returns the number of curve codes f is stored with, or 0
// for no curve (16-bit lossless).
func (f *Frame) curveLevels() (int, error) {
	switch {
	case f.Compression == LosslessJPEG:
		return 0, nil
	case f.Compression == LosslessJPEG12:
		return levels12, nil
	case f.Compression.TargetRatio() > 0 && !f.Compression.IsDCT():
		if f.CurveLevels < minCurveLevels || f.CurveLevels > levels12 {
			return 0, fmt.Errorf("dng: %v needs CurveLevels in [%d, %d] (see CalibrateLevels), got %d",
				f.Compression, minCurveLevels, levels12, f.CurveLevels)
		}
		return f.CurveLevels, nil
	default:
		return 0, fmt.Errorf("dng: compression %v is not tiled", f.Compression)
	}
}

// clampKeepingPhase maps i >= n back into the last 2x2 Bayer cell.
func clampKeepingPhase(i, n int) int {
	if i < n {
		return i
	}
	return n - cfaRepeat + (i-n)%cfaRepeat
}

// CalibrateLevels finds the most curve levels (best quality) at which f,
// stored with f.Compression, reaches the compression's target ratio. It
// returns the levels and the ratio they give. It never goes below the
// frame's noise floor (noiseFloorLevels): if the target needs fewer
// levels, it returns the floor and the ratio reached there.
func CalibrateLevels(f *Frame) (int, float64, error) {
	target := f.Compression.TargetRatio()
	if target <= 0 || f.Compression.IsDCT() {
		return 0, 0, fmt.Errorf("dng: %v is not a curve ratio mode", f.Compression)
	}
	lo, hi := noiseFloorLevels(f), levels12
	hiRatio, err := estimateRatio(f, hi)
	if err != nil || hiRatio >= target || lo >= hi {
		return hi, hiRatio, err
	}
	loRatio, err := estimateRatio(f, lo)
	if err != nil || loRatio < target {
		return lo, loRatio, err
	}
	// Invariant: ratio(lo) >= target > ratio(hi). Bisect geometrically:
	// ratio changes with log(levels).
	for hi-lo > 1 {
		mid := int(math.Sqrt(float64(lo) * float64(hi)))
		mid = min(max(mid, lo+1), hi-1)
		r, err := estimateRatio(f, mid)
		if err != nil {
			return 0, 0, err
		}
		if r >= target {
			lo, loRatio = mid, r
		} else {
			hi = mid
		}
	}
	return lo, loRatio, nil
}

// estimateRatio is the lossless-JPEG ratio of f with a curve of levels.
func estimateRatio(f *Frame, levels int) (float64, error) {
	return losslessTiling.estimateRatio(f, curveFor(levels, f.BlackLevel), losslessTileEncoder(curvePrecision))
}

// noiseFloorLevels is the fewest curve levels whose step stays within
// maxStepToNoise of f's noise. On the square-root curve photon noise is
// the same at every brightness, so one number measures it: the median
// difference between neighbouring samples of one colour. Texture and
// edges only raise that median, i.e. err towards a coarser floor where
// detail masks the steps; clipped areas lower it, towards more levels.
func noiseFloorLevels(f *Frame) int {
	c := curveFor(levels12, f.BlackLevel)
	hist := make([]int, levels12)
	buf := make([]uint16, tileSize*tileSize)
	total := 0
	for _, i := range losslessTiling.sampled(f) {
		x0, y0 := losslessTiling.origin(f, i)
		losslessTiling.fill(buf, f, x0, y0, c)
		for row := 0; row < tileSize; row++ {
			line := buf[row*tileSize : (row+1)*tileSize]
			for x := 0; x+cfaRepeat < tileSize; x++ {
				hist[abs(int(line[x])-int(line[x+cfaRepeat]))]++
				total++
			}
		}
	}
	median, seen := 0, 0
	for median < len(hist)-1 && seen+hist[median] <= total/2 {
		seen += hist[median]
		median++
	}
	sigma := float64(median) * madToSigma
	if sigma == 0 {
		return levels12
	}
	levels := int(math.Ceil((levels12-1)/(maxStepToNoise*sigma))) + 1
	return min(max(levels, minCurveLevels), levels12)
}

func gcd(a, b int) int {
	for b != 0 {
		a, b = b, a%b
	}
	return a
}

// curve is a square-root transfer curve with a given number of codes,
// starting just below the black level so no code is spent on values the
// sensor never produces: table[i] = base + (65535-base) * (i/(levels-1))^2.
// Its step grows with the square root of the signal, as photon shot noise
// does, so the error stays a constant fraction of the noise.
type curve struct {
	// table maps code -> linear 16-bit value (LinearizationTable). It always
	// has 2^curvePrecision entries, the unused tail repeating the top value,
	// because readers index it by the declared bit depth.
	table  []int
	encode [maxSample16 + 1]uint16 // linear 16-bit value -> nearest code
}

type curveKey struct{ levels, base int }

var curves sync.Map // curveKey -> *curve

// curveFor returns the shared curve with the given number of levels for a
// sensor with the given black level.
func curveFor(levels, blackLevel int) *curve {
	key := curveKey{levels, max(blackLevel-curveFootroom, 0)}
	if c, ok := curves.Load(key); ok {
		return c.(*curve)
	}
	c, _ := curves.LoadOrStore(key, buildCurve(key))
	return c.(*curve)
}

func buildCurve(key curveKey) *curve {
	levels, base := key.levels, float64(key.base)
	span := maxSample16 - base
	top := levels - 1
	c := &curve{table: make([]int, 1<<curvePrecision)}
	for i := range c.table {
		c.table[i] = int(math.Round(base + span*math.Pow(float64(min(i, top))/float64(top), 2)))
	}
	for v := 0; v <= maxSample16; v++ {
		i := int(math.Round(float64(top) * math.Sqrt(max(float64(v)-base, 0)/span)))
		best := i
		for _, j := range []int{i - 1, i + 1} {
			if j >= 0 && j <= top && abs(c.table[j]-v) < abs(c.table[best]-v) {
				best = j
			}
		}
		c.encode[v] = uint16(best)
	}
	return c
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}
