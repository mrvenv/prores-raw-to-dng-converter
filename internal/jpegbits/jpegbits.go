// Package jpegbits holds the parts of ITU-T T.81 (JPEG) shared by the
// lossless codec (ljpeg) and the DCT codec (dctjpeg): markers, segments,
// Huffman tables and entropy-coded bit I/O.
package jpegbits

import (
	"encoding/binary"
	"errors"
	"fmt"
)

// Markers (T.81 table B.1).
const (
	MarkerSOI  = 0xFFD8
	MarkerEOI  = 0xFFD9
	MarkerSOF0 = 0xFFC0 // baseline DCT
	MarkerSOF1 = 0xFFC1 // extended sequential DCT, Huffman (8- or 12-bit)
	MarkerSOF3 = 0xFFC3 // lossless, Huffman
	MarkerDHT  = 0xFFC4
	MarkerSOS  = 0xFFDA
	MarkerDQT  = 0xFFDB
)

const (
	// MaxCodeLen is the longest Huffman code JPEG allows (T.81 K.2).
	MaxCodeLen = 16
	// NumSymbols is the size of the Huffman symbol alphabet (one byte).
	NumSymbols = 256
	// SamplingFactor1x1 packs horizontal/vertical sampling factors of 1.
	SamplingFactor1x1 = 0x11
	// segmentLengthSize: a segment length counts its own two bytes.
	segmentLengthSize = 2
	// stuffByte follows a 0xFF data byte in entropy-coded data (T.81 F.1.2.3).
	stuffByte  = 0x00
	byteMarker = 0xFF
	// dhtHeaderSize is the class/destination byte plus 16 code counts.
	dhtHeaderSize = 1 + MaxCodeLen
)

// ErrTruncated reports data that ends before a complete structure.
var ErrTruncated = errors.New("jpeg: truncated data")

// Table is a Huffman table in DHT form plus encoder codes and decoder
// lookup (T.81 annex C and F.2.2.3).
type Table struct {
	Counts [MaxCodeLen + 1]int // Counts[l] = number of codes of length l
	Values []byte              // symbols ordered by code length
	code   [NumSymbols]uint32
	size   [NumSymbols]int
	// decoder: first and last code of each length, where its symbols start
	minCode [MaxCodeLen + 1]int32
	maxCode [MaxCodeLen + 1]int32 // -1 when no codes of that length
	valPtr  [MaxCodeLen + 1]int
}

// BuildTable builds an optimal length-limited table for the symbol
// frequencies freq (indexed by symbol, at most NumSymbols long), following
// T.81 Annex K.2/K.3. Symbols with zero frequency get no code.
func BuildTable(freq []int) Table {
	n := len(freq)
	reserved := n // dummy symbol so no code is all ones
	f := make([]int, n+1)
	copy(f, freq)
	f[reserved] = 1
	codeSize := make([]int, n+1)
	others := make([]int, n+1)
	for i := range others {
		others[i] = -1
	}
	for {
		c1, c2 := leastTwo(f)
		if c2 < 0 {
			break
		}
		f[c1] += f[c2]
		f[c2] = 0
		for codeSize[c1]++; others[c1] >= 0; codeSize[c1]++ {
			c1 = others[c1]
		}
		others[c1] = c2
		for codeSize[c2]++; others[c2] >= 0; codeSize[c2]++ {
			c2 = others[c2]
		}
	}

	counts := make([]int, 2*n+2)
	for _, s := range codeSize {
		if s > 0 {
			counts[s]++
		}
	}
	limitCodeLengths(counts)

	var t Table
	copy(t.Counts[:], counts[:MaxCodeLen+1])
	for l := 1; l < len(counts); l++ {
		for sym := 0; sym < n; sym++ {
			if codeSize[sym] == l {
				t.Values = append(t.Values, byte(sym))
			}
		}
	}
	t.assignCodes()
	return t
}

// leastTwo returns the indices of the two smallest non-zero frequencies,
// preferring the larger index on ties (T.81 figure K.1); -1 if missing.
func leastTwo(f []int) (int, int) {
	c1, c2 := -1, -1
	for i, v := range f {
		if v == 0 {
			continue
		}
		switch {
		case c1 < 0 || v <= f[c1]:
			c2, c1 = c1, i
		case c2 < 0 || v <= f[c2]:
			c2 = i
		}
	}
	return c1, c2
}

// limitCodeLengths caps code lengths at MaxCodeLen and drops the reserved
// code (T.81 figure K.3).
func limitCodeLengths(counts []int) {
	for i := len(counts) - 1; i > MaxCodeLen; i-- {
		for counts[i] > 0 {
			j := i - 2
			for counts[j] == 0 {
				j--
			}
			counts[i] -= 2
			counts[i-1]++
			counts[j+1] += 2
			counts[j]--
		}
	}
	i := MaxCodeLen
	for counts[i] == 0 {
		i--
	}
	counts[i]--
}

// assignCodes generates canonical codes in DHT order (T.81 C.2) and the
// decoder tables (T.81 F.2.2.3).
func (t *Table) assignCodes() {
	code, k := int32(0), 0
	for l := 1; l <= MaxCodeLen; l++ {
		t.valPtr[l] = k
		t.minCode[l] = code
		t.maxCode[l] = -1
		for n := 0; n < t.Counts[l] && k < len(t.Values); n++ {
			sym := t.Values[k]
			t.code[sym], t.size[sym] = uint32(code), l
			t.maxCode[l] = code
			code++
			k++
		}
		code <<= 1
	}
	t.Values = t.Values[:k]
}

// AppendSegment appends a marker segment with its length.
func AppendSegment(out []byte, marker uint16, body []byte) []byte {
	out = binary.BigEndian.AppendUint16(out, marker)
	out = binary.BigEndian.AppendUint16(out, uint16(len(body)+segmentLengthSize))
	return append(out, body...)
}

// AppendDHT appends t as a DHT segment for the given class (0 = DC or
// lossless, 1 = AC) and destination.
func AppendDHT(out []byte, t *Table, class, destination byte) []byte {
	body := []byte{class<<4 | destination}
	for l := 1; l <= MaxCodeLen; l++ {
		body = append(body, byte(t.Counts[l]))
	}
	body = append(body, t.Values...)
	return AppendSegment(out, MarkerDHT, body)
}

// ParseDHT reads every table of a DHT segment body and calls store with
// the class, destination and table of each.
func ParseDHT(body []byte, store func(class, destination byte, t Table) error) error {
	for len(body) > 0 {
		if len(body) < dhtHeaderSize {
			return ErrTruncated
		}
		var t Table
		k := 0
		for l := 1; l <= MaxCodeLen; l++ {
			t.Counts[l] = int(body[l])
			k += t.Counts[l]
		}
		if k > NumSymbols || len(body) < dhtHeaderSize+k {
			return errors.New("jpeg: bad DHT")
		}
		t.Values = append([]byte(nil), body[dhtHeaderSize:dhtHeaderSize+k]...)
		t.assignCodes()
		if err := store(body[0]>>4, body[0]&0x0F, t); err != nil {
			return err
		}
		body = body[dhtHeaderSize+k:]
	}
	return nil
}

// Segment is one marker segment of a JPEG stream.
type Segment struct {
	Marker uint16
	Body   []byte
}

// Segments walks the segments after SOI up to and including SOS, calling
// visit for each; it returns the entropy-coded data that follows SOS.
func Segments(data []byte, visit func(Segment) error) ([]byte, error) {
	if len(data) < 2 || binary.BigEndian.Uint16(data) != MarkerSOI {
		return nil, errors.New("jpeg: missing SOI")
	}
	for p := 2; ; {
		if p+4 > len(data) {
			return nil, ErrTruncated
		}
		marker := binary.BigEndian.Uint16(data[p:])
		n := int(binary.BigEndian.Uint16(data[p+2:]))
		if n < segmentLengthSize || p+2+n > len(data) {
			return nil, ErrTruncated
		}
		seg := Segment{marker, data[p+4 : p+2+n]}
		p += 2 + n
		if err := visit(seg); err != nil {
			return nil, err
		}
		if marker == MarkerSOS {
			return data[p:], nil
		}
	}
}

// BitWriter packs bits MSB-first and byte-stuffs 0xFF.
type BitWriter struct {
	Out  []byte
	acc  uint64
	nacc uint
}

// Write appends the low n bits of v.
func (w *BitWriter) Write(v uint32, n int) {
	w.acc = w.acc<<uint(n) | uint64(v)&(1<<uint(n)-1)
	w.nacc += uint(n)
	for w.nacc >= 8 {
		w.nacc -= 8
		b := byte(w.acc >> w.nacc)
		w.Out = append(w.Out, b)
		if b == byteMarker {
			w.Out = append(w.Out, stuffByte)
		}
	}
}

// WriteSymbol appends the code of sym from t.
func (w *BitWriter) WriteSymbol(t *Table, sym int) {
	w.Write(t.code[sym], t.size[sym])
}

// Flush pads the last byte with 1-bits (T.81 F.1.2.3).
func (w *BitWriter) Flush() {
	if pad := (8 - w.nacc%8) % 8; pad > 0 {
		w.Write(1<<pad-1, int(pad))
	}
}

// BitReader reads entropy-coded data MSB-first, removing stuffed bytes.
type BitReader struct {
	data []byte
	pos  int
	acc  uint64
	nacc uint
}

// NewBitReader reads the entropy-coded data in data.
func NewBitReader(data []byte) *BitReader { return &BitReader{data: data} }

// Read returns the next n bits.
func (r *BitReader) Read(n int) (uint32, error) {
	for r.nacc < uint(n) {
		if r.pos >= len(r.data) {
			return 0, ErrTruncated
		}
		b := r.data[r.pos]
		r.pos++
		if b == byteMarker {
			if r.pos >= len(r.data) || r.data[r.pos] != stuffByte {
				return 0, fmt.Errorf("jpeg: marker inside entropy-coded data: %w", ErrTruncated)
			}
			r.pos++
		}
		r.acc = r.acc<<8 | uint64(b)
		r.nacc += 8
	}
	r.nacc -= uint(n)
	return uint32(r.acc>>r.nacc) & (1<<uint(n) - 1), nil
}

// Symbol decodes the next Huffman-coded symbol with t (T.81 F.2.2.3).
func (r *BitReader) Symbol(t *Table) (int, error) {
	code := int32(0)
	for l := 1; l <= MaxCodeLen; l++ {
		b, err := r.Read(1)
		if err != nil {
			return 0, err
		}
		code = code<<1 | int32(b)
		if t.maxCode[l] >= 0 && code >= t.minCode[l] && code <= t.maxCode[l] {
			return int(t.Values[t.valPtr[l]+int(code-t.minCode[l])]), nil
		}
	}
	return 0, errors.New("jpeg: bad Huffman code")
}

// Extend turns the s extra bits v of a category-s value into the signed
// value (T.81 F.2.2.1, EXTEND).
func Extend(v uint32, s int) int32 {
	d := int32(v)
	if s > 0 && v < 1<<(s-1) {
		d -= 1<<s - 1
	}
	return d
}

// ExtraBits is the inverse of Extend: the s low bits that encode d.
func ExtraBits(d int32, s int) uint32 {
	if d < 0 {
		d += 1<<s - 1
	}
	return uint32(d)
}
