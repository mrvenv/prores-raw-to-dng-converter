// Package convert turns a ProRes RAW clip into a CinemaDNG sequence.
package convert

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
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

// Software is written into the DNG Software tag.
const Software = "rawconv"

// Pipeline sizes.
const (
	// outDirPerm is the permission of the created output folder.
	outDirPerm = 0o755
	// minAutoWorkers and maxAutoWorkers bound the automatic worker count,
	// which is otherwise one per CPU.
	minAutoWorkers = 2
	maxAutoWorkers = 6
	// spareFrameBuffers are frame buffers beyond one per worker, so the
	// reader can fill the next frame while every worker is busy.
	spareFrameBuffers = 2
	// bytesPerSensel is the size of a bayer_rggb16le sample.
	bytesPerSensel = 2
	// Read buffers for the packet and frame streams; the frame one carries
	// whole 16-bit frames, so it gets the larger buffer.
	packetReadBuffer = 4 << 20
	decodeReadBuffer = 8 << 20
)

// Source is everything a conversion reads from a clip. Convert runs no
// processes itself: ffmpeg.Tools is the desktop Source, running ffmpeg and
// ffprobe; a platform that cannot start processes (iOS, Android) supplies
// its own, backed by a linked decoder library or the system's decoder.
type Source interface {
	// ProbeFile describes the clip.
	ProbeFile(ctx context.Context, path string) (ffmpeg.Probe, error)
	// Packets streams the raw ProRes RAW packets of the first video track,
	// read with prr.PacketReader; maxFrames limits them (0 = all).
	Packets(ctx context.Context, path string, maxFrames int) (ffmpeg.Stream, error)
	// Frames streams the same frames decoded to bayer_rggb16le, full sensor
	// area, Width*Height*2 bytes each, in packet order.
	Frames(ctx context.Context, path string, maxFrames int) (ffmpeg.Stream, error)
	// ExtractAudio writes the first audio track as a 24-bit WAV.
	ExtractAudio(ctx context.Context, req ffmpeg.AudioRequest) error
}

// ffmpeg.Tools is the desktop Source.
var _ Source = ffmpeg.Tools{}

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
func Convert(ctx context.Context, src Source, input string, opt Options) (Result, error) {
	var res Result
	probe, err := src.ProbeFile(ctx, input)
	if err != nil {
		return res, err
	}
	if probe.Codec != ffmpeg.ProResRAWCodec {
		return res, fmt.Errorf("%s: codec is %q, not ProRes RAW", filepath.Base(input), probe.Codec)
	}
	total := probe.Frames
	if opt.MaxFrames > 0 && (total == 0 || opt.MaxFrames < total) {
		total = opt.MaxFrames
	}

	base := OutputName(input, opt.Compression)
	outDir := filepath.Join(opt.OutDir, base)
	if err := os.MkdirAll(outDir, outDirPerm); err != nil {
		return res, fmt.Errorf("create output folder: %w", err)
	}
	res.OutDir = outDir
	camera := cameraInfoFromProbe(probe)
	if err := writeMetadata(outDir, input, probe); err != nil {
		return res, fmt.Errorf("write metadata: %w", err)
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

	packets, err := src.Packets(ctx, input, opt.MaxFrames)
	if err != nil {
		return res, err
	}
	frames, err := src.Frames(ctx, input, opt.MaxFrames)
	if err != nil {
		cancel()
		// The packet stream was just stopped by cancel; its error says only
		// that, and the failure to start decoding is the cause to report.
		_ = packets.Wait()
		return res, err
	}

	workers := opt.Workers
	if workers <= 0 {
		workers = max(minAutoWorkers, min(runtime.NumCPU(), maxAutoWorkers))
	}
	jobs := make(chan job, workers)
	free := make(chan []byte, workers+spareFrameBuffers)
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

	pr := prr.NewPacketReader(bufio.NewReaderSize(packets, packetReadBuffer))
	dec := bufio.NewReaderSize(frames, decodeReadBuffer)
	// decodeErr is set when the frame stream ends early; the stream's own
	// error, known only after Wait, carries the cause.
	var decodeErr error
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
			frameSize = hdr.Width * hdr.Height * bytesPerSensel
			for i := 0; i < workers+spareFrameBuffers; i++ {
				free <- make([]byte, frameSize)
			}
		} else if hdr.Width*hdr.Height*bytesPerSensel != frameSize {
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
			decodeErr = fmt.Errorf("decoding frame %d: %w", idx, err)
			fail(decodeErr)
			break
		}
		j := job{idx: idx, buf: buf, hdr: hdr}
		if idx == 0 {
			// Like calibrate below, this is written before any worker
			// receives a job.
			res.Color = fw.measureColor(j)
			logColor(opt, hdr, res.Color, fw.auto)
		}
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
	pErr, dErr := packets.Wait(), frames.Wait()

	if decodeErr != nil && dErr != nil {
		return res, fmt.Errorf("%w: %w", decodeErr, dErr)
	}
	if firstErr != nil {
		return res, firstErr
	}
	if err := ctx.Err(); err != nil {
		return res, err
	}
	if pErr != nil {
		return res, pErr
	}
	if dErr != nil {
		return res, dErr
	}
	res.Frames = int(done.Load())

	if opt.Audio && probe.HasAudio {
		req := ffmpeg.AudioRequest{Input: input, WAV: filepath.Join(outDir, base+wavExt)}
		if opt.MaxFrames > 0 && probe.FPS() > 0 {
			req.Duration = framesDuration(res.Frames, probe.FPS())
		}
		if err := src.ExtractAudio(ctx, req); err != nil {
			return res, err
		}
	}
	return res, nil
}

// wavExt names the audio file next to the frames.
const wavExt = ".wav"

// framesDuration is how long n frames last at fps, so audio trimmed to it
// matches a -frames conversion.
func framesDuration(n int, fps float64) time.Duration {
	return time.Duration(float64(n) / fps * float64(time.Second))
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
	// auto is measured once, from the first frame, when its header lacks
	// white balance or exposure.
	auto AutoColor
}

// measureColor measures auto colour from the first frame when its header
// needs it, and returns the colour the clip starts with.
func (w *frameWriter) measureColor(first job) ColorInfo {
	if NeedsAuto(first.hdr) {
		w.auto = MeasureAuto(first.buf, first.hdr)
	}
	return ColorFromHeader(first.hdr, w.auto)
}

// logColor reports the first frame's header and the colour derived from
// it, warning when white balance or exposure had to be measured or left
// at a neutral default.
func logColor(opt Options, hdr prr.Header, ci ColorInfo, auto AutoColor) {
	opt.logf("header: vendor=%q %dx%d range=%d WB(R=%.4f B=%.4f) CCT=%dK gain=%.3f postWB=%v fallback=%v",
		hdr.Vendor, hdr.Width, hdr.Height, hdr.SenselValueRange, hdr.WBRed, hdr.WBBlue,
		hdr.WBCCT, hdr.Gain, ci.PostWB, ci.Fallback)
	switch {
	case ci.AutoWB:
		opt.logf("warning: the clip has no white balance; using auto white balance (R=%.3f B=%.3f)", auto.WBRed, auto.WBBlue)
	case !validFactor(hdr.WBRed) || !validFactor(hdr.WBBlue):
		opt.logf("warning: the clip has no white balance and none could be measured; set it in Resolve")
	}
	switch {
	case ci.AutoExposure:
		opt.logf("warning: the clip has no exposure gain; using auto exposure (%+.2f stops)", ci.BaselineExposure)
	case !validFactor(hdr.Gain):
		opt.logf("warning: the clip has no exposure gain and none could be measured; set Exposure in Resolve")
	}
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
		return fmt.Errorf("create frame: %w", err)
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
	ci := ColorFromHeader(h, w.auto)
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
