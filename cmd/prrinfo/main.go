// Command prrinfo prints ProRes RAW frame headers found in a file and the
// DNG colour tags rawconv would derive from them. It scans for "prrf"
// packets directly, so it needs no ffmpeg.
package main

import (
	"bufio"
	"bytes"
	"flag"
	"fmt"
	"io"
	"os"

	"rawconv/internal/convert"
	"rawconv/internal/prr"
)

// Exit codes.
const (
	exitFailed   = 1
	exitBadUsage = 2
)

// Scanning settings.
const (
	readBuffer = 1 << 20
	scanChunk  = 8 << 20
	// headerWindow is kept past a "prrf" match, so a header that straddles
	// two chunks is still parsed whole.
	headerWindow = 512
	// sizeFieldLen is the packet size that precedes the "prrf" fourcc.
	sizeFieldLen = 4
	fourCC       = "prrf"
)

func main() {
	n := flag.Int("n", 1, "number of frame headers to print")
	flag.Parse()
	if flag.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "usage: prrinfo [-n N] clip.mov")
		os.Exit(exitBadUsage)
	}
	f, err := os.Open(flag.Arg(0))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(exitFailed)
	}
	// The file is only read, so a close error loses nothing.
	defer func() { _ = f.Close() }()

	r := bufio.NewReaderSize(f, readBuffer)
	var buf []byte
	var base int64
	found := 0
	for found < *n {
		tmp := make([]byte, scanChunk)
		// A short or failed read ends the scan after what it returned.
		k, err := io.ReadFull(r, tmp)
		buf = append(buf, tmp[:k]...)
		for found < *n {
			i := bytes.Index(buf, []byte(fourCC))
			if i < 0 || i+headerWindow > len(buf) {
				break
			}
			if i >= sizeFieldLen {
				if h, perr := prr.ParseHeader(buf[i-sizeFieldLen:]); perr == nil {
					found++
					show(found, base+int64(i-sizeFieldLen), h)
				}
			}
			buf = buf[i+len(fourCC):]
			base += int64(i + len(fourCC))
		}
		if err != nil {
			break
		}
		if len(buf) > headerWindow { // keep a tail in case "prrf" straddles chunks
			drop := len(buf) - headerWindow
			buf = buf[drop:]
			base += int64(drop)
		}
	}
	if found == 0 {
		fmt.Println("no ProRes RAW frame headers found")
		os.Exit(exitFailed)
	}
}

// show prints one header and the colour derived from it. prrinfo reads no
// pixels, so white balance or exposure missing from the header shows the
// neutral default, not what auto measurement would pick.
func show(i int, off int64, h prr.Header) {
	ci := convert.ColorFromHeader(h, convert.AutoColor{})
	fmt.Printf("frame %d @%d: vendor=%q v%d %dx%d crop(l%d r%d t%d b%d) bayer=%d range=%d black=%d white=%d\n",
		i, off, h.Vendor, h.Version, h.Width, h.Height, h.CropLeft, h.CropRight, h.CropTop, h.CropBottom,
		h.BayerPattern, h.SenselValueRange, h.BlackLevel(), h.WhiteLevel())
	fmt.Printf("  WB R=%.5f B=%.5f CCT=%dK gain=%.4f flags=0x%04x\n", h.WBRed, h.WBBlue, h.WBCCT, h.Gain, h.Flags)
	m := h.ColorMatrix
	fmt.Printf("  cam->XYZ: [%.4f %.4f %.4f; %.4f %.4f %.4f; %.4f %.4f %.4f]\n", m[0], m[1], m[2], m[3], m[4], m[5], m[6], m[7], m[8])
	c := ci.ColorMatrix1
	fmt.Printf("  DNG ColorMatrix1: [%.4f %.4f %.4f; %.4f %.4f %.4f; %.4f %.4f %.4f]\n", c[0], c[1], c[2], c[3], c[4], c[5], c[6], c[7], c[8])
	fmt.Printf("  AsShotNeutral=%.4f BaselineExposure=%.3f postWB=%v fallback=%v\n",
		ci.AsShotNeutral, ci.BaselineExposure, ci.PostWB, ci.Fallback)
}
