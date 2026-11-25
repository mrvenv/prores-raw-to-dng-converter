// Package convert turns a ProRes RAW clip into a CinemaDNG sequence.
package convert

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"rawconv/internal/dng"
	"rawconv/internal/ffmpeg"
	"rawconv/internal/prr"
	"rawconv/internal/timecode"
)

const Software = "rawconv"

// Options controls a conversion.
type Options struct {
	OutDir    string
	Audio     bool
	Workers   int // parallel DNG writers; 0 = auto
	MaxFrames int // 0 = all
	// Compression selects how DNG frames are stored.
	Compression dng.Compression
	Progress    func(done, total int)
	Log         func(string)
}

// Result summarises a finished conversion.
type Result struct {
	OutDir string
	Frames int
	Color  ColorInfo
	Header prr.Header
}

type job struct {
	idx int
	buf []byte
	hdr prr.Header
}

func (o Options) logf(format string, a ...any) {
	if o.Log != nil {
		o.Log(fmt.Sprintf(format, a...))
	}
}

// Separators in output names: "<clip>_<compression>", with a ratio's ":"
// spelled "to" because macOS Finder shows ":" as "/" and Windows forbids it.
const (
	nameSeparator      = "_"
	ratioSeparator     = ":"
	pathRatioSeparator = "to"
)

// OutputName is the name of the folder, frames and WAV made from input
// with compression c: "clip_12bit", "clip_3to1". Keeping the compression in
// the frame names too keeps versions of one clip apart in Resolve's bins.
func OutputName(input string, c dng.Compression) string {
	clip := strings.TrimSuffix(filepath.Base(input), filepath.Ext(input))
	return clip + nameSeparator + strings.ReplaceAll(c.String(), ratioSeparator, pathRatioSeparator)
}

// Convert converts input into the CinemaDNG folder OutputName inside opt.OutDir.
func Convert(ctx context.Context, tools ffmpeg.Tools, input string, opt Options) (Result, error) {
	var res Result
	probe, err := tools.ProbeFile(ctx, input)
	if err != nil {
		return res, err
	}
	if probe.Codec != "prores_raw" {
		return res, fmt.Errorf("%s: codec is %q, not ProRes RAW", filepath.Base(input), probe.Codec)
	}
	total := probe.Frames
	if opt.MaxFrames > 0 && (total == 0 || opt.MaxFrames < total) {
		total = opt.MaxFrames
	}

	base := OutputName(input, opt.Compression)
	outDir := filepath.Join(opt.OutDir, base)
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return res, err
	}
	res.OutDir = outDir
	camera := cameraInfoFromProbe(probe)
	if err := writeMetadata(outDir, input, probe); err != nil {
		return res, err
	}
	opt.logf("%s", camera.Summary())

	var tc *timecode.TC
	if probe.TimeCode != "" {
		if t, err := timecode.Parse(probe.TimeCode, probe.FPS()); err == nil {
			tc = &t
		} else {
			opt.logf("warning: %v", err)
		}
	}
	opt.logf("%s: %dx%d, %.3f fps, %d frames, TC %s, audio=%v",
		filepath.Base(input), probe.Width, probe.Height, probe.FPS(), probe.Frames, probe.TimeCode, probe.HasAudio)

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	var limit []string
	if opt.MaxFrames > 0 {
		limit = []string{"-frames:v", fmt.Sprint(opt.MaxFrames)}
	}
	var pktErr, decErr bytes.Buffer
	pktArgs := append([]string{"-v", "error", "-nostdin", "-i", input, "-map", "0:v:0"}, limit...)
	pktArgs = append(pktArgs, "-c", "copy", "-f", "data", "-")
	pktCmd := exec.CommandContext(ctx, tools.FFmpeg, pktArgs...)
	pktCmd.Stderr = &pktErr
	pktOut, err := pktCmd.StdoutPipe()
	if err != nil {
		return res, err
	}
	decArgs := append([]string{"-v", "error", "-nostdin", "-apply_cropping", "none", "-i", input, "-map", "0:v:0"}, limit...)
	decArgs = append(decArgs, "-f", "rawvideo", "-pix_fmt", "bayer_rggb16le", "-")
	decCmd := exec.CommandContext(ctx, tools.FFmpeg, decArgs...)
	decCmd.Stderr = &decErr
	decOut, err := decCmd.StdoutPipe()
	if err != nil {
		return res, err
	}
	if err := pktCmd.Start(); err != nil {
		return res, err
	}
	if err := decCmd.Start(); err != nil {
		cancel()
		pktCmd.Wait()
		return res, err
	}

	workers := opt.Workers
	if workers <= 0 {
		workers = max(2, min(runtime.NumCPU(), 6))
	}
	jobs := make(chan job, workers)
	free := make(chan []byte, workers+2)
	var done atomic.Int64
	var progMu sync.Mutex
	var firstErr error
	var errOnce sync.Once
	fail := func(e error) {
		errOnce.Do(func() { firstErr = e; cancel() })
	}

	fw := frameWriter{outDir: outDir, base: base, probe: probe, tc: tc, compression: opt.Compression, camera: camera}
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range jobs {
				if ctx.Err() == nil {
					if err := fw.write(j); err != nil {
						fail(err)
					} else if opt.Progress != nil {
						progMu.Lock()
						opt.Progress(int(done.Add(1)), total)
						progMu.Unlock()
					} else {
						done.Add(1)
					}
				}
				free <- j.buf
			}
		}()
	}

	pr := prr.NewPacketReader(bufio.NewReaderSize(pktOut, 4<<20))
	dec := bufio.NewReaderSize(decOut, 8<<20)
	frameSize := 0
	idx := 0
readLoop:
	for ctx.Err() == nil {
		pkt, err := pr.Next()
		if err != nil {
			if !errors.Is(err, io.EOF) {
				fail(fmt.Errorf("reading packets: %w", err))
			}
			break
		}
		hdr, err := prr.ParseHeader(pkt)
		if err != nil {
			fail(fmt.Errorf("frame %d: %w", idx, err))
			break
		}
		if idx == 0 {
			res.Header = hdr
			res.Color = ColorFromHeader(hdr)
			frameSize = hdr.Width * hdr.Height * 2
			for i := 0; i < workers+2; i++ {
				free <- make([]byte, frameSize)
			}
			opt.logf("header: vendor=%q %dx%d range=%d WB(R=%.4f B=%.4f) CCT=%dK gain=%.3f postWB=%v fallback=%v",
				hdr.Vendor, hdr.Width, hdr.Height, hdr.SenselValueRange, hdr.WBRed, hdr.WBBlue,
				hdr.WBCCT, hdr.Gain, res.Color.PostWB, res.Color.Fallback)
		} else if hdr.Width*hdr.Height*2 != frameSize {
			fail(fmt.Errorf("frame %d: resolution changed mid-clip", idx))
			break
		}
		var buf []byte
		select {
		case buf = <-free:
		case <-ctx.Done():
			break readLoop
		}
		if _, err := io.ReadFull(dec, buf); err != nil {
			free <- buf
			fail(fmt.Errorf("decoding frame %d: %w (%s)", idx, err, strings.TrimSpace(decErr.String())))
			break
		}
		j := job{idx: idx, buf: buf, hdr: hdr}
		if idx == 0 && opt.Compression.TargetRatio() > 0 {
			// Workers read fw only after receiving a job, so this write
			// happens before any of them uses the calibration.
			report, err := fw.calibrate(j)
			if err != nil {
				free <- buf
				fail(fmt.Errorf("calibrating %v: %w", opt.Compression, err))
				break
			}
			for _, line := range report {
				opt.logf("%s", line)
			}
		}
		jobs <- j
		idx++
	}
	close(jobs)
	wg.Wait()
	// Drain so ffmpeg can exit if we stopped reading early.
	go io.Copy(io.Discard, pktOut)
	go io.Copy(io.Discard, decOut)
	pErr, dErr := pktCmd.Wait(), decCmd.Wait()

	if firstErr != nil {
		return res, firstErr
	}
	if err := ctx.Err(); err != nil {
		return res, err
	}
	if pErr != nil {
		return res, fmt.Errorf("ffmpeg (packets): %v %s", pErr, strings.TrimSpace(pktErr.String()))
	}
	if dErr != nil {
		return res, fmt.Errorf("ffmpeg (decode): %v %s", dErr, strings.TrimSpace(decErr.String()))
	}
	res.Frames = int(done.Load())

	if opt.Audio && probe.HasAudio {
		wav := filepath.Join(outDir, base+".wav")
		args := []string{"-v", "error", "-nostdin", "-y", "-i", input, "-map", "0:a:0", "-c:a", "pcm_s24le"}
		if opt.MaxFrames > 0 && probe.FPS() > 0 {
			args = append(args, "-t", fmt.Sprintf("%.6f", float64(res.Frames)/probe.FPS()))
		}
		args = append(args, wav)
		if out, err := exec.CommandContext(ctx, tools.FFmpeg, args...).CombinedOutput(); err != nil {
			return res, fmt.Errorf("audio: %v %s", err, strings.TrimSpace(string(out)))
		}
	}
	return res, nil
}

// frameNameFormat names frames <clip>_000000.dng, the CinemaDNG sequence
// layout Resolve expects.
const frameNameFormat = "%s_%06d.dng"

// frameWriter writes the DNG frames of one clip.
type frameWriter struct {
	outDir, base string
	probe        ffmpeg.Probe
	tc           *timecode.TC
	compression  dng.Compression
	camera       CameraInfo
	// curveLevels and quantScale are set once, from the first frame, for
	// the curve and DCT ratio modes.
	curveLevels int
	quantScale  float64
}

// recordedOnPrefix starts the ImageDescription naming an external recorder.
const recordedOnPrefix = "Recorded on "

// calibrate picks the setting of a ratio compression from the first
// frame, so the whole clip is stored with one setting (no quality flicker).
// It returns log lines describing the choice.
func (w *frameWriter) calibrate(first job) ([]string, error) {
	c := w.compression
	if c.IsDCT() {
		scale, ratio, err := dng.CalibrateQuant(w.frame(first))
		if err != nil {
			return nil, err
		}
		w.quantScale = scale
		report := []string{fmt.Sprintf("%v: quantisation x%.2f, estimated %.2f:1", c, scale, ratio)}
		if ratio < c.TargetRatio() {
			report = append(report, fmt.Sprintf("warning: %v is not reachable on this clip; using the coarsest table, about %.1f:1", c, ratio))
		}
		return report, nil
	}
	levels, ratio, err := dng.CalibrateLevels(w.frame(first))
	if err != nil {
		return nil, err
	}
	w.curveLevels = levels
	report := []string{fmt.Sprintf("%v: %d-level curve (%.1f bits), estimated %.2f:1", c, levels, math.Log2(float64(levels)), ratio)}
	if ratio < c.TargetRatio() {
		report = append(report, fmt.Sprintf("warning: %v would posterise this clip; stopping at its noise floor, about %.1f:1", c, ratio))
	}
	return report, nil
}

func (w frameWriter) write(j job) error {
	f := w.frame(j)
	path := filepath.Join(w.outDir, fmt.Sprintf(frameNameFormat, w.base, j.idx))
	fh, err := os.Create(path)
	if err != nil {
		return err
	}
	if err := dng.Write(fh, f); err != nil {
		_ = fh.Close() // the write error is the one worth reporting
		return fmt.Errorf("%s: %w", path, err)
	}
	return fh.Close()
}

// frame describes job j as a DNG frame.
func (w frameWriter) frame(j job) *dng.Frame {
	h, probe, cam := j.hdr, w.probe, w.camera
	ci := ColorFromHeader(h)
	ucm := cam.UniqueModel()
	if ucm == "" {
		ucm = "ProRes RAW " + strings.TrimSpace(h.Vendor)
	}
	var description string
	if cam.Recorder != "" {
		description = recordedOnPrefix + cam.Recorder
	}
	var shot time.Time
	if !cam.Created.IsZero() && probe.FPS() > 0 {
		shot = cam.Created.Add(time.Duration(float64(j.idx) / probe.FPS() * float64(time.Second)))
	}
	f := &dng.Frame{
		Width: h.Width, Height: h.Height, Data: j.buf, Compression: w.compression, CurveLevels: w.curveLevels, QuantScale: w.quantScale,
		Make: cam.Make, Model: cam.Model, UniqueCameraModel: ucm, Software: Software,
		Description: description, DateTime: shot,
		Exif:       dng.Exif{ISO: cam.ISO, FNumber: cam.FNumber, ExposureTime: cam.ExposureTime},
		BlackLevel: h.BlackLevel(), WhiteLevel: h.WhiteLevel(),
		ColorMatrix1: ci.ColorMatrix1, AsShotNeutral: ci.AsShotNeutral, BaselineExposure: ci.BaselineExposure,
		CropX: h.CropLeft, CropY: h.CropTop,
		CropW: h.Width - h.CropLeft - h.CropRight, CropH: h.Height - h.CropTop - h.CropBottom,
		FrameRateN: probe.FrameRateN, FrameRateD: probe.FrameRateD,
	}
	if w.tc != nil {
		b := w.tc.BCD(j.idx)
		f.TimeCode = &b
	}
	return f
}
