// Package ffmpeg locates ffmpeg/ffprobe binaries and runs them: probing a
// clip, streaming its packets and decoded frames, extracting its audio.
// Tools is the desktop implementation of convert.Source.
package ffmpeg

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// ProResRAWCodec is the codec name ffmpeg and ffprobe use for ProRes RAW.
const ProResRAWCodec = "prores_raw"

// Executable names and the places Find looks in besides PATH.
const (
	ffmpegName    = "ffmpeg"
	ffprobeName   = "ffprobe"
	dirEnv        = "RAWCONV_FFMPEG_DIR"
	pathEnv       = "PATH"
	windowsExeExt = ".exe"
	// bundleResourcesDir is where a macOS .app keeps ffmpeg, relative to its executable.
	bundleResourcesDir = "../Resources"
	// bundledSubdir is a folder next to the executable that may hold ffmpeg.
	bundledSubdir = "ffmpeg"
)

// systemDirs are the Homebrew and MacPorts bin folders: GUI apps on macOS
// get a minimal PATH that leaves them out.
var systemDirs = []string{"/opt/homebrew/bin", "/usr/local/bin", "/opt/local/bin"}

// errNotFound is returned by Find when no usable ffmpeg/ffprobe pair exists.
var errNotFound = errors.New("ffmpeg with ProRes RAW support (8.0+) not found: install it (brew install ffmpeg) or put ffmpeg/ffprobe next to the app")

// Tools holds paths to the ffmpeg and ffprobe executables.
type Tools struct {
	FFmpeg  string
	FFprobe string
	Version string
}

// Find returns the first ffmpeg/ffprobe pair that can decode ProRes RAW.
func Find(ctx context.Context) (Tools, error) {
	seen := map[string]bool{}
	for _, d := range candidates() {
		ff := filepath.Join(d, exeName(ffmpegName))
		if seen[ff] {
			continue
		}
		seen[ff] = true
		fp := filepath.Join(d, exeName(ffprobeName))
		if !exists(ff) || !exists(fp) || !HasProResRAW(ctx, ff) {
			continue
		}
		return Tools{FFmpeg: ff, FFprobe: fp, Version: versionLine(ctx, ff)}, nil
	}
	return Tools{}, errNotFound
}

// HasProResRAW reports whether the ffmpeg binary has the prores_raw decoder.
func HasProResRAW(ctx context.Context, ffmpegPath string) bool {
	out, err := Command(ctx, ffmpegPath, "-hide_banner", "-decoders").Output()
	return err == nil && strings.Contains(string(out), " "+ProResRAWCodec+" ")
}

// candidates lists the folders Find searches, in order.
func candidates() []string {
	var dirs []string
	if d := os.Getenv(dirEnv); d != "" {
		dirs = append(dirs, d)
	}
	if exe, err := os.Executable(); err == nil {
		d := filepath.Dir(exe)
		dirs = append(dirs, d, filepath.Join(d, bundleResourcesDir), filepath.Join(d, bundledSubdir))
	}
	dirs = append(dirs, filepath.SplitList(os.Getenv(pathEnv))...)
	return append(dirs, systemDirs...)
}

// exeName adds the Windows executable extension to n where it is needed.
func exeName(n string) string {
	if os.PathSeparator == '\\' {
		return n + windowsExeExt
	}
	return n
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// versionLine is the first line of `ffmpeg -version`, or "" when it fails:
// the version is only shown to the user, so a failure is not worth an error.
func versionLine(ctx context.Context, ffmpegPath string) string {
	out, err := Command(ctx, ffmpegPath, "-hide_banner", "-version").Output()
	if err != nil {
		return ""
	}
	first, _, _ := strings.Cut(string(out), "\n")
	return first
}

// Probe describes an input clip.
type Probe struct {
	Codec           string
	Width, Height   int
	FrameRateN      int
	FrameRateD      int
	Frames          int
	Duration        float64
	TimeCode        string
	HasAudio        bool
	AudioSampleRate int
	// FormatTags and VideoTags are the container and video-stream metadata
	// exactly as ffprobe reports them.
	FormatTags map[string]string
	VideoTags  map[string]string
}

// FPS is the frame rate, or 0 when ffprobe reported none.
func (p Probe) FPS() float64 {
	if p.FrameRateD == 0 {
		return 0
	}
	return float64(p.FrameRateN) / float64(p.FrameRateD)
}

// ffprobe stream types, tag names and number formats the probe reads.
const (
	streamVideo  = "video"
	streamAudio  = "audio"
	streamData   = "data"
	timecodeTag  = "timecode"
	rateSep      = "/"
	floatBitSize = 64
	// roundHalfUp rounds the frame count estimated from the duration.
	roundHalfUp = 0.5
)

type probeJSON struct {
	Streams []struct {
		CodecType  string            `json:"codec_type"`
		CodecName  string            `json:"codec_name"`
		Width      int               `json:"width"`
		Height     int               `json:"height"`
		RFrameRate string            `json:"r_frame_rate"`
		AvgRate    string            `json:"avg_frame_rate"`
		NbFrames   string            `json:"nb_frames"`
		Duration   string            `json:"duration"`
		SampleRate string            `json:"sample_rate"`
		Tags       map[string]string `json:"tags"`
	} `json:"streams"`
	Format struct {
		Duration string            `json:"duration"`
		Tags     map[string]string `json:"tags"`
	} `json:"format"`
}

// ProbeFile runs ffprobe on path.
//
// ffprobe leaves numeric fields empty or "N/A" when it does not know them;
// such a field stays 0 and is filled from another source where there is
// one, so its parse error is ignored.
func (t Tools) ProbeFile(ctx context.Context, path string) (Probe, error) {
	var p Probe
	out, err := Command(ctx, t.FFprobe, "-v", "error", "-print_format", "json",
		"-show_streams", "-show_format", path).Output()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return p, fmt.Errorf("ffprobe: %s", strings.TrimSpace(string(ee.Stderr)))
		}
		return p, fmt.Errorf("ffprobe: %w", err)
	}
	var j probeJSON
	if err := json.Unmarshal(out, &j); err != nil {
		return p, fmt.Errorf("ffprobe json: %w", err)
	}
	videoFound := false
	for _, s := range j.Streams {
		switch s.CodecType {
		case streamVideo:
			if videoFound {
				continue
			}
			videoFound = true
			p.Codec = s.CodecName
			p.Width, p.Height = s.Width, s.Height
			p.FrameRateN, p.FrameRateD = parseRate(s.RFrameRate)
			if p.FrameRateN == 0 {
				p.FrameRateN, p.FrameRateD = parseRate(s.AvgRate)
			}
			p.Frames, _ = strconv.Atoi(s.NbFrames)
			p.Duration, _ = strconv.ParseFloat(s.Duration, floatBitSize)
			p.TimeCode = firstTag(s.Tags, timecodeTag)
			p.VideoTags = s.Tags
		case streamAudio:
			if !p.HasAudio {
				p.HasAudio = true
				p.AudioSampleRate, _ = strconv.Atoi(s.SampleRate)
			}
		case streamData:
			if p.TimeCode == "" {
				p.TimeCode = firstTag(s.Tags, timecodeTag)
			}
		}
	}
	if !videoFound {
		return p, errors.New("no video stream")
	}
	if p.TimeCode == "" {
		p.TimeCode = firstTag(j.Format.Tags, timecodeTag)
	}
	if p.Duration == 0 {
		p.Duration, _ = strconv.ParseFloat(j.Format.Duration, floatBitSize)
	}
	if p.Frames == 0 && p.Duration > 0 && p.FPS() > 0 {
		p.Frames = int(p.Duration*p.FPS() + roundHalfUp)
	}
	p.FormatTags = j.Format.Tags
	return p, nil
}

// parseRate splits an ffprobe rate such as "24000/1001". An unparsable part
// is 0 ("0/0" is what ffprobe itself reports for an unknown rate), which
// FPS reports as unknown.
func parseRate(s string) (int, int) {
	a, b, ok := strings.Cut(s, rateSep)
	if !ok {
		return 0, 0
	}
	n, _ := strconv.Atoi(a)
	d, _ := strconv.Atoi(b)
	return n, d
}

// firstTag returns the first non-empty value among keys, matched
// case-insensitively because muxers differ in tag case.
func firstTag(tags map[string]string, keys ...string) string {
	for _, k := range keys {
		for tk, v := range tags {
			if strings.EqualFold(tk, k) && v != "" {
				return v
			}
		}
	}
	return ""
}
