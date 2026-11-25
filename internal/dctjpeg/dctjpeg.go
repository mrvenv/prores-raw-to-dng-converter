// Package dctjpeg encodes and decodes 12-bit DCT JPEG: ITU-T T.81
// extended sequential process with Huffman coding (SOF1). DNG files for
// DaVinci Resolve carry it as lossy-compressed raw tiles.
//
// Only what that needs is supported: one quantisation table, one DC and
// one AC Huffman table shared by all components, interleaved components
// with 1x1 sampling, no restart intervals. Width and height must be
// multiples of the 8x8 block.
package dctjpeg

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"math/bits"

	"rawconv/internal/jpegbits"
)

const (
	// Precision is the sample precision Encode writes, in bits.
	Precision = 12
	// BlockSize is the edge of a DCT block (T.81 A.3.3).
	BlockSize = 8
	// BlockArea is the number of coefficients in a block.
	BlockArea     = BlockSize * BlockSize
	maxComponents = 4
	// precision8 is the other precision SOF1 allows; Decode accepts it.
	precision8 = 8
	// levelShift centres samples on zero before the DCT (T.81 A.3.1).
	levelShift = 1 << (Precision - 1)
	// maxACMagnitude: 12-bit AC coefficients use categories up to 14
	// (T.81 F.1.2.2, table F.2 extended for 12 bits).
	maxACMagnitude = 1<<14 - 1
	// maxQuant is the largest value an 8-bit quantisation table holds.
	maxQuant = 255
	// Huffman table classes and the destination all components share.
	classDC = 0
	classAC = 1
	tableID = 0
	// quantPrecision8 marks an 8-bit DQT table (T.81 B.2.4.1).
	quantPrecision8 = 0
	// AC run-length symbols (T.81 F.1.2.2): end of block, run of 16 zeros.
	symbolEOB     = 0x00
	symbolZRL     = 0xF0
	zeroRunMax    = 15
	nibbleShift   = 4
	nibbleMask    = 0x0F
	lastCoeff     = BlockArea - 1
	sofHeaderSize = 6
	sofEntrySize  = 3
	markerDRI     = 0xFFDD
)

// Quant is a quantisation table in natural (row-major) order.
type Quant [BlockArea]uint16

// annexKLuminance is T.81 table K.1, the example luminance table.
var annexKLuminance = Quant{
	16, 11, 10, 16, 24, 40, 51, 61,
	12, 12, 14, 19, 26, 58, 60, 55,
	14, 13, 16, 24, 40, 57, 69, 56,
	14, 17, 22, 29, 51, 87, 80, 62,
	18, 22, 37, 56, 68, 109, 103, 77,
	24, 35, 55, 64, 81, 104, 113, 92,
	49, 64, 78, 87, 103, 121, 120, 101,
	72, 92, 95, 98, 112, 100, 103, 99,
}

// ScaledQuant returns T.81 table K.1 scaled by scale and clamped to the
// 8-bit range: larger scales compress more.
func ScaledQuant(scale float64) Quant {
	var q Quant
	for i, v := range annexKLuminance {
		q[i] = uint16(min(max(math.Round(float64(v)*scale), 1), maxQuant))
	}
	return q
}

// zigzag maps zigzag position to natural index (T.81 figure A.6).
var zigzag = [BlockArea]int{
	0, 1, 8, 16, 9, 2, 3, 10,
	17, 24, 32, 25, 18, 11, 4, 5,
	12, 19, 26, 33, 40, 48, 41, 34,
	27, 20, 13, 6, 7, 14, 21, 28,
	35, 42, 49, 56, 57, 50, 43, 36,
	29, 22, 15, 23, 30, 37, 44, 51,
	58, 59, 52, 45, 38, 31, 39, 46,
	53, 60, 61, 54, 47, 55, 62, 63,
}

// dctBasis[x][u] = C(u)/2 * cos((2x+1)u*pi/16), so a 2-D DCT is two
// passes of it (T.81 A.3.3).
var dctBasis = func() (b [BlockSize][BlockSize]float64) {
	for x := range BlockSize {
		for u := range BlockSize {
			c := 1.0
			if u == 0 {
				c = 1 / math.Sqrt2
			}
			b[x][u] = c / 2 * math.Cos(float64(2*x+1)*float64(u)*math.Pi/(2*BlockSize))
		}
	}
	return b
}()

// Encode compresses samples laid out as height rows of width pixels, each
// holding components interleaved 12-bit values.
func Encode(samples []uint16, width, height, components int, q *Quant) ([]byte, error) {
	if err := validate(len(samples), width, height, components); err != nil {
		return nil, err
	}
	blocks := quantisedBlocks(samples, width, height, components, q)
	dcFreq, acFreq := symbolFrequencies(blocks, components)
	dc, ac := jpegbits.BuildTable(dcFreq[:]), jpegbits.BuildTable(acFreq[:])

	out := make([]byte, 0, len(samples)/2)
	out = binary.BigEndian.AppendUint16(out, jpegbits.MarkerSOI)
	out = appendDQT(out, q)
	out = appendSOF1(out, width, height, components)
	out = jpegbits.AppendDHT(out, &dc, classDC, tableID)
	out = jpegbits.AppendDHT(out, &ac, classAC, tableID)
	out = appendSOS(out, components)
	out = appendScan(out, blocks, components, &dc, &ac)
	out = binary.BigEndian.AppendUint16(out, jpegbits.MarkerEOI)
	return out, nil
}

func validate(n, width, height, components int) error {
	switch {
	case width <= 0 || height <= 0 || width > 0xFFFF || height > 0xFFFF:
		return fmt.Errorf("dctjpeg: bad size %dx%d", width, height)
	case width%BlockSize != 0 || height%BlockSize != 0:
		return fmt.Errorf("dctjpeg: size %dx%d is not a multiple of %d", width, height, BlockSize)
	case components < 1 || components > maxComponents:
		return fmt.Errorf("dctjpeg: bad component count %d", components)
	case n != width*height*components:
		return fmt.Errorf("dctjpeg: have %d samples, want %d", n, width*height*components)
	}
	return nil
}

// quantisedBlocks returns every block in scan order (MCU by MCU, the
// components of an MCU in turn), coefficients in zigzag order.
func quantisedBlocks(samples []uint16, width, height, components int, q *Quant) [][BlockArea]int32 {
	blocksX, blocksY := width/BlockSize, height/BlockSize
	blocks := make([][BlockArea]int32, 0, blocksX*blocksY*components)
	var spatial [BlockArea]float64
	for by := range blocksY {
		for bx := range blocksX {
			for c := range components {
				for y := range BlockSize {
					row := ((by*BlockSize+y)*width + bx*BlockSize) * components
					for x := range BlockSize {
						spatial[y*BlockSize+x] = float64(samples[row+x*components+c]) - levelShift
					}
				}
				blocks = append(blocks, quantise(forwardDCT(&spatial), q))
			}
		}
	}
	return blocks
}

// forwardDCT returns the coefficients in natural order: index v*8+u for
// vertical frequency v and horizontal frequency u.
func forwardDCT(s *[BlockArea]float64) [BlockArea]float64 {
	var tmp, out [BlockArea]float64
	for y := range BlockSize {
		for u := range BlockSize {
			sum := 0.0
			for x := range BlockSize {
				sum += s[y*BlockSize+x] * dctBasis[x][u]
			}
			tmp[y*BlockSize+u] = sum
		}
	}
	for u := range BlockSize {
		for v := range BlockSize {
			sum := 0.0
			for y := range BlockSize {
				sum += tmp[y*BlockSize+u] * dctBasis[y][v]
			}
			out[v*BlockSize+u] = sum
		}
	}
	return out
}

func quantise(f [BlockArea]float64, q *Quant) [BlockArea]int32 {
	var z [BlockArea]int32
	for k, i := range zigzag {
		v := int32(math.Round(f[i] / float64(q[i])))
		if k > 0 {
			v = min(max(v, -maxACMagnitude), maxACMagnitude)
		}
		z[k] = v
	}
	return z
}

// category is SSSS, the number of bits of a coefficient magnitude.
func category(v int32) int {
	if v < 0 {
		v = -v
	}
	return bits.Len32(uint32(v))
}

// visitSymbols walks the Huffman symbols of the scan in order: emit gets
// the table class, the symbol, and the value whose size extra bits follow.
func visitSymbols(blocks [][BlockArea]int32, components int, emit func(class, sym int, value int32, size int)) {
	pred := make([]int32, components)
	for i, b := range blocks {
		c := i % components
		diff := b[0] - pred[c]
		pred[c] = b[0]
		s := category(diff)
		emit(classDC, s, diff, s)
		run := 0
		for k := 1; k < BlockArea; k++ {
			if b[k] == 0 {
				run++
				continue
			}
			for ; run > zeroRunMax; run -= zeroRunMax + 1 {
				emit(classAC, symbolZRL, 0, 0)
			}
			s := category(b[k])
			emit(classAC, run<<nibbleShift|s, b[k], s)
			run = 0
		}
		if run > 0 {
			emit(classAC, symbolEOB, 0, 0)
		}
	}
}

func symbolFrequencies(blocks [][BlockArea]int32, components int) (dc, ac [jpegbits.NumSymbols]int) {
	visitSymbols(blocks, components, func(class, sym int, _ int32, _ int) {
		if class == classDC {
			dc[sym]++
		} else {
			ac[sym]++
		}
	})
	return dc, ac
}

func appendScan(out []byte, blocks [][BlockArea]int32, components int, dc, ac *jpegbits.Table) []byte {
	w := jpegbits.BitWriter{Out: out}
	visitSymbols(blocks, components, func(class, sym int, value int32, size int) {
		t := dc
		if class == classAC {
			t = ac
		}
		w.WriteSymbol(t, sym)
		if size > 0 {
			w.Write(jpegbits.ExtraBits(value, size), size)
		}
	})
	w.Flush()
	return w.Out
}

func appendDQT(out []byte, q *Quant) []byte {
	body := []byte{quantPrecision8<<nibbleShift | tableID}
	for _, i := range zigzag {
		body = append(body, byte(q[i]))
	}
	return jpegbits.AppendSegment(out, jpegbits.MarkerDQT, body)
}

func appendSOF1(out []byte, width, height, components int) []byte {
	body := []byte{Precision}
	body = binary.BigEndian.AppendUint16(body, uint16(height))
	body = binary.BigEndian.AppendUint16(body, uint16(width))
	body = append(body, byte(components))
	for c := range components {
		body = append(body, byte(c), jpegbits.SamplingFactor1x1, tableID)
	}
	return jpegbits.AppendSegment(out, jpegbits.MarkerSOF1, body)
}

func appendSOS(out []byte, components int) []byte {
	body := []byte{byte(components)}
	for c := range components {
		body = append(body, byte(c), tableID<<nibbleShift|tableID)
	}
	body = append(body, 0, lastCoeff, 0) // Ss = 0, Se = 63, Ah/Al = 0
	return jpegbits.AppendSegment(out, jpegbits.MarkerSOS, body)
}

// Image is a decoded DCT JPEG.
type Image struct {
	Width, Height, Components, Precision int
	Samples                              []uint16
}

// decoder collects the tables a scan needs.
type decoder struct {
	img       Image
	quant     [maxComponents]Quant
	compQuant []int
	dc, ac    [maxComponents]*jpegbits.Table
	compDC    []int
	compAC    []int
}

// Decode decodes 8- or 12-bit sequential Huffman DCT JPEG with 1x1
// sampling and no restart intervals: what Encode writes, and the same
// layout from other DNG writers. It exists to verify output.
func Decode(data []byte) (Image, error) {
	var d decoder
	scan, err := jpegbits.Segments(data, d.segment)
	if err != nil {
		return Image{}, err
	}
	n := d.img.Width * d.img.Height * d.img.Components
	if err := validate(n, d.img.Width, d.img.Height, d.img.Components); err != nil {
		return Image{}, err
	}
	if len(d.compDC) != d.img.Components {
		return Image{}, errors.New("dctjpeg: scan does not cover every component")
	}
	return d.img, d.decodeScan(scan)
}

func (d *decoder) segment(s jpegbits.Segment) error {
	switch s.Marker {
	case jpegbits.MarkerDQT:
		return d.parseDQT(s.Body)
	case jpegbits.MarkerSOF0, jpegbits.MarkerSOF1:
		return d.parseSOF(s.Body)
	case jpegbits.MarkerDHT:
		return jpegbits.ParseDHT(s.Body, d.storeTable)
	case jpegbits.MarkerSOS:
		return d.parseSOS(s.Body)
	case markerDRI:
		return errors.New("dctjpeg: restart intervals are not supported")
	default:
		return nil // APPn, COM and the like carry nothing the decoder needs
	}
}

func (d *decoder) storeTable(class, dest byte, t jpegbits.Table) error {
	if int(dest) >= maxComponents {
		return errors.New("dctjpeg: bad DHT destination")
	}
	if class == classDC {
		d.dc[dest] = &t
	} else {
		d.ac[dest] = &t
	}
	return nil
}

func (d *decoder) parseDQT(body []byte) error {
	for len(body) > 0 {
		wide, dest := body[0]>>nibbleShift != quantPrecision8, int(body[0]&nibbleMask)
		size := BlockArea
		if wide {
			size *= 2
		}
		if dest >= maxComponents || len(body) < 1+size {
			return errors.New("dctjpeg: bad DQT")
		}
		for k, i := range zigzag {
			if wide {
				d.quant[dest][i] = binary.BigEndian.Uint16(body[1+2*k:])
			} else {
				d.quant[dest][i] = uint16(body[1+k])
			}
		}
		body = body[1+size:]
	}
	return nil
}

func (d *decoder) parseSOF(body []byte) error {
	if len(body) < sofHeaderSize {
		return jpegbits.ErrTruncated
	}
	d.img.Precision = int(body[0])
	d.img.Height = int(binary.BigEndian.Uint16(body[1:]))
	d.img.Width = int(binary.BigEndian.Uint16(body[3:]))
	d.img.Components = int(body[5])
	if d.img.Precision != Precision && d.img.Precision != precision8 {
		return fmt.Errorf("dctjpeg: unsupported precision %d", d.img.Precision)
	}
	if d.img.Components > maxComponents || len(body) < sofHeaderSize+sofEntrySize*d.img.Components {
		return errors.New("dctjpeg: bad SOF")
	}
	for c := range d.img.Components {
		e := body[sofHeaderSize+sofEntrySize*c:]
		if e[1] != jpegbits.SamplingFactor1x1 || int(e[2]) >= maxComponents {
			return errors.New("dctjpeg: only 1x1 sampling is supported")
		}
		d.compQuant = append(d.compQuant, int(e[2]))
	}
	return nil
}

func (d *decoder) parseSOS(body []byte) error {
	if len(body) < 1 || len(body) < 1+2*int(body[0]) {
		return jpegbits.ErrTruncated
	}
	for c := range int(body[0]) {
		sel := body[2+2*c]
		d.compDC = append(d.compDC, int(sel>>nibbleShift))
		d.compAC = append(d.compAC, int(sel&nibbleMask))
	}
	for c := range d.compDC {
		if d.compDC[c] >= maxComponents || d.compAC[c] >= maxComponents {
			return errors.New("dctjpeg: bad table selector")
		}
	}
	return nil
}

func (d *decoder) decodeScan(data []byte) error {
	img := &d.img
	img.Samples = make([]uint16, img.Width*img.Height*img.Components)
	r := jpegbits.NewBitReader(data)
	pred := make([]int32, img.Components)
	shift := float64(int(1) << (img.Precision - 1))
	top := float64(int(1)<<img.Precision - 1)
	var coef [BlockArea]float64
	for by := range img.Height / BlockSize {
		for bx := range img.Width / BlockSize {
			for c := range img.Components {
				dc, ac := d.dc[d.compDC[c]], d.ac[d.compAC[c]]
				if dc == nil || ac == nil {
					return errors.New("dctjpeg: missing Huffman table")
				}
				if err := decodeBlock(r, dc, ac, &pred[c], &d.quant[d.compQuant[c]], &coef); err != nil {
					return fmt.Errorf("dctjpeg: block %d,%d: %w", bx, by, err)
				}
				s := inverseDCT(&coef)
				for y := range BlockSize {
					row := ((by*BlockSize+y)*img.Width + bx*BlockSize) * img.Components
					for x := range BlockSize {
						v := min(max(math.Round(s[y*BlockSize+x]+shift), 0), top)
						img.Samples[row+x*img.Components+c] = uint16(v)
					}
				}
			}
		}
	}
	return nil
}

// decodeBlock reads one block's coefficients into coef, dequantised, in
// natural order (T.81 F.2.2).
func decodeBlock(r *jpegbits.BitReader, dc, ac *jpegbits.Table, pred *int32, q *Quant, coef *[BlockArea]float64) error {
	*coef = [BlockArea]float64{}
	s, err := r.Symbol(dc)
	if err != nil {
		return err
	}
	v, err := r.Read(s)
	if err != nil {
		return err
	}
	*pred += jpegbits.Extend(v, s)
	coef[0] = float64(*pred) * float64(q[0])
	for k := 1; k < BlockArea; {
		sym, err := r.Symbol(ac)
		if err != nil {
			return err
		}
		run, size := sym>>nibbleShift, sym&nibbleMask
		if size == 0 {
			if sym != symbolZRL {
				return nil // EOB
			}
			k += zeroRunMax + 1
			continue
		}
		k += run
		if k >= BlockArea {
			return errors.New("bad AC run")
		}
		v, err := r.Read(size)
		if err != nil {
			return err
		}
		i := zigzag[k]
		coef[i] = float64(jpegbits.Extend(v, size)) * float64(q[i])
		k++
	}
	return nil
}

// inverseDCT takes coefficients in natural order (v*8+u) and returns the
// samples, still level-shifted.
func inverseDCT(f *[BlockArea]float64) [BlockArea]float64 {
	var tmp, out [BlockArea]float64
	for v := range BlockSize {
		for x := range BlockSize {
			sum := 0.0
			for u := range BlockSize {
				sum += f[v*BlockSize+u] * dctBasis[x][u]
			}
			tmp[v*BlockSize+x] = sum
		}
	}
	for y := range BlockSize {
		for x := range BlockSize {
			sum := 0.0
			for v := range BlockSize {
				sum += tmp[v*BlockSize+x] * dctBasis[y][v]
			}
			out[y*BlockSize+x] = sum
		}
	}
	return out
}
