// Package ffmpeg locates ffmpeg/ffprobe binaries and probes input clips.
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

// Tools holds paths to the ffmpeg and ffprobe executables.
type Tools struct {
	FFmpeg  string
	FFprobe string
	Version string
}

func candidates() []string {
	var dirs []string
	if d := os.Getenv("RAWCONV_FFMPEG_DIR"); d != "" {
		dirs = append(dirs, d)
	}
	if exe, err := os.Executable(); err == nil {
		d := filepath.Dir(exe)
		dirs = append(dirs, d, filepath.Join(d, "..", "Resources"), filepath.Join(d, "ffmpeg"))
	}
	dirs = append(dirs, filepath.SplitList(os.Getenv("PATH"))...)
	// GUI apps on macOS get a minimal PATH; check the usual locations too.
	dirs = append(dirs, "/opt/homebrew/bin", "/usr/local/bin", "/opt/local/bin")
	return dirs
}

func exeName(n string) string {
	if os.PathSeparator == '\\' {
		return n + ".exe"
	}
	return n
}

// HasProResRAW reports whether the ffmpeg binary has the prores_raw decoder.
func HasProResRAW(ffmpegPath string) bool {
	out, err := exec.Command(ffmpegPath, "-hide_banner", "-decoders").Output()
	return err == nil && strings.Contains(string(out), " prores_raw ")
}

// Find returns the first ffmpeg/ffprobe pair that can decode ProRes RAW.
func Find() (Tools, error) {
	seen := map[string]bool{}
	for _, d := range candidates() {
		ff := filepath.Join(d, exeName("ffmpeg"))
		if seen[ff] {
			continue
		}
		seen[ff] = true
		fp := filepath.Join(d, exeName("ffprobe"))
		if _, err := os.Stat(ff); err != nil {
			continue
		}
		if _, err := os.Stat(fp); err != nil {
			continue
		}
		if !HasProResRAW(ff) {
			continue
		}
		t := Tools{FFmpeg: ff, FFprobe: fp}
		if out, err := exec.Command(ff, "-hide_banner", "-version").Output(); err == nil {
			t.Version = strings.SplitN(string(out), "\n", 2)[0]
		}
		return t, nil
	}
	return Tools{}, errors.New("ffmpeg with ProRes RAW support (8.0+) not found: install it (brew install ffmpeg) or put ffmpeg/ffprobe next to the app")
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

func (p Probe) FPS() float64 {
	if p.FrameRateD == 0 {
		return 0
	}
	return float64(p.FrameRateN) / float64(p.FrameRateD)
}

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

func parseRate(s string) (int, int) {
	a, b, ok := strings.Cut(s, "/")
	if !ok {
		return 0, 0
	}
	n, _ := strconv.Atoi(a)
	d, _ := strconv.Atoi(b)
	return n, d
}

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

// ProbeFile runs ffprobe on path.
func (t Tools) ProbeFile(ctx context.Context, path string) (Probe, error) {
	var p Probe
	out, err := exec.CommandContext(ctx, t.FFprobe, "-v", "error", "-print_format", "json",
		"-show_streams", "-show_format", path).Output()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return p, fmt.Errorf("ffprobe: %s", strings.TrimSpace(string(ee.Stderr)))
		}
		return p, err
	}
	var j probeJSON
	if err := json.Unmarshal(out, &j); err != nil {
		return p, fmt.Errorf("ffprobe json: %w", err)
	}
	videoFound := false
	for _, s := range j.Streams {
		switch s.CodecType {
		case "video":
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
			p.Duration, _ = strconv.ParseFloat(s.Duration, 64)
			p.TimeCode = firstTag(s.Tags, "timecode")
			p.VideoTags = s.Tags
		case "audio":
			if !p.HasAudio {
				p.HasAudio = true
				p.AudioSampleRate, _ = strconv.Atoi(s.SampleRate)
			}
		case "data":
			if p.TimeCode == "" {
				p.TimeCode = firstTag(s.Tags, "timecode")
			}
		}
	}
	if !videoFound {
		return p, errors.New("no video stream")
	}
	if p.TimeCode == "" {
		p.TimeCode = firstTag(j.Format.Tags, "timecode")
	}
	if p.Duration == 0 {
		p.Duration, _ = strconv.ParseFloat(j.Format.Duration, 64)
	}
	if p.Frames == 0 && p.Duration > 0 && p.FPS() > 0 {
		p.Frames = int(p.Duration*p.FPS() + 0.5)
	}
	p.FormatTags = j.Format.Tags
	return p, nil
}
