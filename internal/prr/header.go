// Package prr parses Apple ProRes RAW frame headers ("prrf" packets).
//
// Layout follows FFmpeg's libavcodec/prores_raw.c (offsets are relative to
// the start of the packet).
package prr

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
)

// Header holds the per-frame metadata stored in a ProRes RAW packet.
type Header struct {
	Version          int
	Vendor           string
	Width, Height    int
	CropLeft         int
	CropRight        int
	CropTop          int
	CropBottom       int
	BayerPattern     int // 0 = RGGB
	SenselValueRange int
	WBRed            float64    // white balance multiplier for red
	WBBlue           float64    // white balance multiplier for blue
	ColorMatrix      [9]float64 // camera RGB -> CIE XYZ (D65), row-major
	Gain             float64
	WBCCT            int // Kelvin
	Flags            int
}

// BlackLevel and WhiteLevel are expressed in the 16-bit sample domain that
// FFmpeg's decoder produces (black is fixed at 0x100 by the format).
func (h Header) BlackLevel() int { return 0x100 }
func (h Header) WhiteLevel() int { return h.SenselValueRange + 0x100 }

const minHeaderBytes = 80 // through the flags field

// ParseHeader parses the frame header at the start of a ProRes RAW packet.
func ParseHeader(pkt []byte) (Header, error) {
	var h Header
	if len(pkt) < minHeaderBytes {
		return h, errors.New("prr: packet too short")
	}
	be := binary.BigEndian
	if string(pkt[4:8]) != "prrf" {
		return h, fmt.Errorf("prr: bad fourcc %q", pkt[4:8])
	}
	headerLen := int(be.Uint16(pkt[8:10]))
	if headerLen < 70 || 8+headerLen > len(pkt) {
		return h, fmt.Errorf("prr: bad header length %d", headerLen)
	}
	f32 := func(off int) float64 { return float64(math.Float32frombits(be.Uint32(pkt[off:]))) }

	h.Version = int(pkt[11])
	h.Vendor = string(pkt[12:16])
	h.Width = int(be.Uint16(pkt[16:]))
	h.Height = int(be.Uint16(pkt[18:]))
	h.CropLeft = int(pkt[20])
	h.CropRight = int(pkt[21])
	h.CropTop = int(pkt[22])
	h.CropBottom = int(pkt[23])
	h.BayerPattern = int(be.Uint16(pkt[24:]) & 3)
	h.SenselValueRange = int(be.Uint16(pkt[26:]))
	h.WBRed = f32(28)
	h.WBBlue = f32(32)
	for i := 0; i < 9; i++ {
		h.ColorMatrix[i] = f32(36 + 4*i)
	}
	h.Gain = f32(72)
	h.WBCCT = int(be.Uint16(pkt[76:]))
	h.Flags = int(be.Uint16(pkt[78:]))
	return h, nil
}

// PacketReader splits a concatenated stream of ProRes RAW packets
// (as produced by `ffmpeg -c copy -f data -`). Each packet begins with its
// own big-endian 32-bit size, which includes the size field itself.
type PacketReader struct {
	r   io.Reader
	buf []byte
}

func NewPacketReader(r io.Reader) *PacketReader { return &PacketReader{r: r} }

// Next returns the next packet. The returned slice is reused on the next call.
func (p *PacketReader) Next() ([]byte, error) {
	var sz [4]byte
	if _, err := io.ReadFull(p.r, sz[:]); err != nil {
		return nil, err
	}
	n := int(binary.BigEndian.Uint32(sz[:]))
	if n < minHeaderBytes || n > 1<<30 {
		return nil, fmt.Errorf("prr: implausible packet size %d", n)
	}
	if cap(p.buf) < n {
		p.buf = make([]byte, n)
	}
	p.buf = p.buf[:n]
	copy(p.buf, sz[:])
	if _, err := io.ReadFull(p.r, p.buf[4:]); err != nil {
		return nil, err
	}
	return p.buf, nil
}
