// Package timecode handles SMPTE timecode parsing, frame arithmetic and the
// 8-byte BCD encoding used by the CinemaDNG TimeCodes tag.
package timecode

import (
	"fmt"
	"math"
	"strings"
)

// TC is a timecode at a nominal (integer) frame rate.
type TC struct {
	Base      int  // nominal fps, e.g. 24, 25, 30
	DropFrame bool // 29.97/59.94 drop-frame
	Start     int  // frame count of the start timecode
}

// Parse parses "HH:MM:SS:FF" (or with ';' for drop frame) at the given rate.
func Parse(s string, fps float64) (TC, error) {
	var t TC
	t.Base = int(math.Round(fps))
	if t.Base <= 0 {
		return t, fmt.Errorf("timecode: bad fps %v", fps)
	}
	t.DropFrame = strings.ContainsAny(s, ";,.") && (t.Base == 30 || t.Base == 60)
	var h, m, sec, f int
	norm := strings.NewReplacer(";", ":", ",", ":", ".", ":").Replace(s)
	if _, err := fmt.Sscanf(norm, "%d:%d:%d:%d", &h, &m, &sec, &f); err != nil {
		return t, fmt.Errorf("timecode: parse %q: %w", s, err)
	}
	fr := ((h*60+m)*60+sec)*t.Base + f
	if t.DropFrame {
		drop := t.Base / 15 // 2 for 30, 4 for 60
		totalMin := h*60 + m
		fr -= drop * (totalMin - totalMin/10)
	}
	t.Start = fr
	return t, nil
}

// At returns h, m, s, f for the frame at offset n from the start.
func (t TC) At(n int) (h, m, s, f int) {
	fr := t.Start + n
	if t.DropFrame {
		drop := t.Base / 15
		per10 := t.Base*600 - drop*9
		per1 := t.Base*60 - drop
		d, r := fr/per10, fr%per10
		if r > drop {
			fr += drop*9*d + drop*((r-drop)/per1)
		} else {
			fr += drop * 9 * d
		}
	}
	day := t.Base * 86400
	fr = ((fr % day) + day) % day
	f = fr % t.Base
	s = (fr / t.Base) % 60
	m = (fr / (t.Base * 60)) % 60
	h = fr / (t.Base * 3600)
	return
}

func bcd(v int) byte { return byte((v/10)<<4 | v%10) }

// BCD returns the SMPTE 12M encoding for frame offset n.
func (t TC) BCD(n int) [8]byte {
	h, m, s, f := t.At(n)
	var b [8]byte
	fb := f
	if t.Base > 30 { // frame field only holds up to 39
		fb = f / 2
	}
	b[0] = bcd(fb) & 0x3f
	if t.DropFrame {
		b[0] |= 0x40
	}
	b[1] = bcd(s) & 0x7f
	b[2] = bcd(m) & 0x7f
	b[3] = bcd(h) & 0x3f
	return b
}

// String formats the timecode for frame offset n.
func (t TC) String(n int) string {
	h, m, s, f := t.At(n)
	sep := ":"
	if t.DropFrame {
		sep = ";"
	}
	return fmt.Sprintf("%02d:%02d:%02d%s%02d", h, m, s, sep, f)
}
