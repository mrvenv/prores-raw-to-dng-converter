// Package ljpeg encodes and decodes lossless JPEG (ITU-T T.81 process 14,
// SOF3), the compression DNG uses for raw images (DNG spec, "Compression" 7).
//
// Only what DNG needs is supported: predictor 1 (left neighbour), one shared
// Huffman table, point transform 0, interleaved components with 1x1 sampling.
package ljpeg

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math/bits"

	"rawconv/internal/jpegbits"
)

const (
	// predictorLeft is selection value 1 (T.81 table H.1): Px = Ra.
	predictorLeft = 1
	// numCategories is the number of difference categories SSSS (0..16, T.81 H.1.2.2).
	numCategories = 17
	// diffModulus: differences are computed modulo 2^16 (T.81 H.1.2.1).
	diffModulus = 1 << 16
	// tableClassLossless and tableID select Huffman table class 0, destination 0.
	tableClassLossless = 0
	tableID            = 0
	minPrecision       = 2
	maxPrecision       = 16
	maxComponents      = 4
)

// Encode compresses samples laid out as height rows of width pixels, each
// pixel holding components interleaved values of the given bit precision.
func Encode(samples []uint16, width, height, components, precision int) ([]byte, error) {
	if err := validate(len(samples), width, height, components, precision); err != nil {
		return nil, err
	}
	diffs, freq := differences(samples, width, height, components, precision)
	table := jpegbits.BuildTable(freq[:])

	out := make([]byte, 0, len(samples)) // compressed output is usually < 1 byte/sample
	out = binary.BigEndian.AppendUint16(out, jpegbits.MarkerSOI)
	out = jpegbits.AppendDHT(out, &table, tableClassLossless, tableID)
	out = appendSOF3(out, width, height, components, precision)
	out = appendSOS(out, components)
	out = appendScan(out, diffs, &table)
	out = binary.BigEndian.AppendUint16(out, jpegbits.MarkerEOI)
	return out, nil
}

func validate(n, width, height, components, precision int) error {
	switch {
	case width <= 0 || height <= 0 || width > 0xFFFF || height > 0xFFFF:
		return fmt.Errorf("ljpeg: bad size %dx%d", width, height)
	case components < 1 || components > maxComponents:
		return fmt.Errorf("ljpeg: bad component count %d", components)
	case precision < minPrecision || precision > maxPrecision:
		return fmt.Errorf("ljpeg: bad precision %d", precision)
	case n != width*height*components:
		return fmt.Errorf("ljpeg: have %d samples, want %d", n, width*height*components)
	}
	return nil
}

// predict returns the predictor for sample i (T.81 H.1.2.1): the first
// sample uses 2^(P-1), the first column uses the sample above, everything
// else (including the first row) uses predictorLeft.
func predict(samples []uint16, i, row, col, stride, components, precision int) int {
	switch {
	case row == 0 && col == 0:
		return 1 << (precision - 1)
	case col == 0:
		return int(samples[i-stride])
	default:
		return int(samples[i-components])
	}
}

func differences(samples []uint16, width, height, components, precision int) ([]int32, [numCategories]int) {
	var freq [numCategories]int
	diffs := make([]int32, len(samples))
	stride := width * components
	for row := 0; row < height; row++ {
		for col := 0; col < width; col++ {
			for c := 0; c < components; c++ {
				i := row*stride + col*components + c
				pred := predict(samples, i, row, col, stride, components, precision)
				d := int32(int16(uint16(int(samples[i]) - pred)))
				diffs[i] = d
				freq[category(d)]++
			}
		}
	}
	return diffs, freq
}

// category is SSSS, the number of bits needed for the difference magnitude.
func category(d int32) int {
	if d == -diffModulus/2 {
		return numCategories - 1
	}
	if d < 0 {
		d = -d
	}
	return bits.Len32(uint32(d))
}

func appendSOF3(out []byte, width, height, components, precision int) []byte {
	body := []byte{byte(precision)}
	body = binary.BigEndian.AppendUint16(body, uint16(height))
	body = binary.BigEndian.AppendUint16(body, uint16(width))
	body = append(body, byte(components))
	for c := 0; c < components; c++ {
		body = append(body, byte(c), jpegbits.SamplingFactor1x1, 0)
	}
	return jpegbits.AppendSegment(out, jpegbits.MarkerSOF3, body)
}

func appendSOS(out []byte, components int) []byte {
	body := []byte{byte(components)}
	for c := 0; c < components; c++ {
		body = append(body, byte(c), tableID)
	}
	body = append(body, predictorLeft, 0, 0) // Ss = predictor, Se unused, Ah/Al = 0
	return jpegbits.AppendSegment(out, jpegbits.MarkerSOS, body)
}

func appendScan(out []byte, diffs []int32, t *jpegbits.Table) []byte {
	w := jpegbits.BitWriter{Out: out}
	for _, d := range diffs {
		s := category(d)
		w.WriteSymbol(t, s)
		if s == 0 || s == numCategories-1 {
			continue // category 16 has no extra bits (T.81 H.1.2.2)
		}
		w.Write(jpegbits.ExtraBits(d, s), s)
	}
	w.Flush()
	return w.Out
}

// Image is a decoded lossless JPEG.
type Image struct {
	Width, Height, Components, Precision int
	Samples                              []uint16
}

// Decode decodes streams produced by Encode. It exists to verify output and
// does not handle the full T.81 feature set (restart markers, several tables).
func Decode(data []byte) (Image, error) {
	var img Image
	var table *jpegbits.Table
	scan, err := jpegbits.Segments(data, func(s jpegbits.Segment) error {
		switch s.Marker {
		case jpegbits.MarkerDHT:
			return jpegbits.ParseDHT(s.Body, func(_, _ byte, t jpegbits.Table) error {
				table = &t
				return nil
			})
		case jpegbits.MarkerSOF3:
			if len(s.Body) < 6 {
				return jpegbits.ErrTruncated
			}
			img.Precision = int(s.Body[0])
			img.Height = int(binary.BigEndian.Uint16(s.Body[1:]))
			img.Width = int(binary.BigEndian.Uint16(s.Body[3:]))
			img.Components = int(s.Body[5])
			return nil
		case jpegbits.MarkerSOS:
			return nil
		default:
			return fmt.Errorf("ljpeg: unexpected marker %#04x", s.Marker)
		}
	})
	if err != nil {
		return img, err
	}
	if table == nil {
		return img, errors.New("ljpeg: no Huffman table")
	}
	for _, v := range table.Values {
		if int(v) >= numCategories {
			return img, errors.New("ljpeg: bad DHT symbol")
		}
	}
	if err := validate(img.Width*img.Height*img.Components, img.Width, img.Height, img.Components, img.Precision); err != nil {
		return img, err
	}
	return img, decodeScan(&img, scan, table)
}

func decodeScan(img *Image, data []byte, t *jpegbits.Table) error {
	img.Samples = make([]uint16, img.Width*img.Height*img.Components)
	r := jpegbits.NewBitReader(data)
	stride := img.Width * img.Components
	for row := 0; row < img.Height; row++ {
		for col := 0; col < img.Width; col++ {
			for c := 0; c < img.Components; c++ {
				i := row*stride + col*img.Components + c
				d, err := difference(r, t)
				if err != nil {
					return err
				}
				pred := predict(img.Samples, i, row, col, stride, img.Components, img.Precision)
				img.Samples[i] = uint16(pred + int(d))
			}
		}
	}
	return nil
}

func difference(r *jpegbits.BitReader, t *jpegbits.Table) (int32, error) {
	s, err := r.Symbol(t)
	if err != nil || s == 0 {
		return 0, err
	}
	if s == numCategories-1 {
		return -diffModulus / 2, nil
	}
	v, err := r.Read(s)
	if err != nil {
		return 0, err
	}
	return jpegbits.Extend(v, s), nil
}
