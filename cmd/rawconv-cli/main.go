// Command rawconv-cli converts ProRes RAW clips to CinemaDNG from the terminal.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"time"

	"rawconv/internal/convert"
	"rawconv/internal/dng"
	"rawconv/internal/ffmpeg"
	"rawconv/internal/version"
)

// Exit codes.
const (
	exitConversionFailed = 1
	exitBadUsage         = 2
)

const (
	usageLine = "usage: rawconv-cli [flags] clip.mov [clip2.mov ...]"
	// progressInterval limits how often the progress line is redrawn.
	progressInterval = 500 * time.Millisecond
	// progressLineFormat redraws the line in place; the trailing spaces
	// clear what a longer previous line left behind.
	progressLineFormat = "\r  %s    "
	doneFormat         = "done: %d frames -> %s (%s, %.1f fps)\n"
)

func main() {
	showVersion := flag.Bool("version", false, "print the version and exit")
	out := flag.String("o", ".", "output directory")
	audio := flag.Bool("audio", true, "extract audio to WAV")
	frames := flag.Int("frames", 0, "convert only the first N frames (0 = all)")
	workers := flag.Int("j", 0, "parallel DNG writers (0 = auto)")
	compressionName := flag.String("compression", dng.LosslessJPEG12.String(),
		"DNG compression: none, lossless (16-bit lossless JPEG), 12bit (visually lossless), 3:1, 5:1, 8:1, or resolve-3:1 ... resolve-16:1 (lossy DCT, DaVinci Resolve only)")
	flag.Usage = func() {
		fmt.Fprintln(os.Stderr, usageLine)
		flag.PrintDefaults()
	}
	flag.Parse()
	if *showVersion {
		fmt.Println("rawconv-cli", version.Version)
		return
	}
	if flag.NArg() == 0 {
		flag.Usage()
		os.Exit(exitBadUsage)
	}
	compression, err := dng.ParseCompression(*compressionName)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(exitBadUsage)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	tools, err := ffmpeg.Find(ctx)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(exitConversionFailed)
	}
	fmt.Println("using", tools.FFmpeg, "-", tools.Version)

	failed := 0
	for _, in := range flag.Args() {
		start := time.Now()
		var last time.Time
		res, err := convert.Convert(ctx, tools, in, convert.Options{
			OutDir: *out, Audio: *audio, Workers: *workers, MaxFrames: *frames, Compression: compression,
			Log: func(s string) { fmt.Println(s) },
			Progress: func(done, total int) {
				if time.Since(last) > progressInterval || done == total {
					last = time.Now()
					p := convert.Progress{Done: done, Total: total, Elapsed: time.Since(start)}
					fmt.Printf(progressLineFormat, p)
				}
			},
		})
		fmt.Println()
		if err != nil {
			fmt.Fprintf(os.Stderr, "%s: %v\n", in, err)
			failed++
			continue
		}
		el := time.Since(start)
		fmt.Printf(doneFormat, res.Frames, res.OutDir, convert.FormatDuration(el), float64(res.Frames)/el.Seconds())
	}
	if failed > 0 {
		os.Exit(exitConversionFailed)
	}
}
