package ffmpeg

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"time"
)

// Arguments of the processes Tools starts for a conversion.
const (
	logLevel   = "error"
	firstVideo = "0:v:0"
	firstAudio = "0:a:0"
	stdout     = "-"
	// decodeThreadType makes ffmpeg decode the tiles of one frame in
	// parallel. Its default, frame threading, keeps prores_raw on one core;
	// slice threading decoded a 5.7K clip seven times faster, to identical
	// output.
	decodeThreadType = "slice"
	// noCropping keeps the full sensor area; the camera's crop goes into the
	// DNG as DefaultCrop instead.
	noCropping = "none"
	// bayerFormat is the decoded frame layout: 16-bit little-endian RGGB.
	bayerFormat = "bayer_rggb16le"
	// audioCodec stores the WAV as 24-bit PCM.
	audioCodec = "pcm_s24le"
	// audioDurationFormat is the -t value, in seconds, that trims audio.
	audioDurationFormat = "%.6f"
)

// Names of the two video streams, used in their error messages.
const (
	packetsStream = "packets"
	decodeStream  = "decode"
)

// Stream is the output of a running producer, such as an ffmpeg process.
type Stream interface {
	io.Reader
	// Wait must be called once reading stops, at EOF or early. It discards
	// the rest of the output, so the producer can finish, and reports how
	// the producer ended, with its own diagnostics.
	Wait() error
}

// AudioRequest describes an audio extraction.
type AudioRequest struct {
	Input string
	// WAV is the file to write, overwritten if it exists.
	WAV string
	// Duration trims the audio; 0 keeps the whole track.
	Duration time.Duration
}

// Packets streams the raw ProRes RAW packets of input's first video track,
// unchanged, for the per-frame metadata in their headers. maxFrames limits
// the frames (0 = all).
func (t Tools) Packets(ctx context.Context, input string, maxFrames int) (Stream, error) {
	args := []string{"-v", logLevel, "-nostdin", "-i", input, "-map", firstVideo}
	args = append(args, frameLimit(maxFrames)...)
	args = append(args, "-c", "copy", "-f", "data", stdout)
	return t.start(ctx, packetsStream, args)
}

// Frames streams the decoded frames of input's first video track as
// bayer_rggb16le, full sensor area, one frame after another. maxFrames
// limits the frames (0 = all).
func (t Tools) Frames(ctx context.Context, input string, maxFrames int) (Stream, error) {
	args := []string{"-v", logLevel, "-nostdin", "-thread_type", decodeThreadType,
		"-apply_cropping", noCropping, "-i", input, "-map", firstVideo}
	args = append(args, frameLimit(maxFrames)...)
	args = append(args, "-f", "rawvideo", "-pix_fmt", bayerFormat, stdout)
	return t.start(ctx, decodeStream, args)
}

// ExtractAudio writes the first audio track of req.Input to req.WAV as
// 24-bit PCM.
func (t Tools) ExtractAudio(ctx context.Context, req AudioRequest) error {
	args := []string{"-v", logLevel, "-nostdin", "-y", "-i", req.Input, "-map", firstAudio, "-c:a", audioCodec}
	if req.Duration > 0 {
		args = append(args, "-t", fmt.Sprintf(audioDurationFormat, req.Duration.Seconds()))
	}
	args = append(args, req.WAV)
	if out, err := Command(ctx, t.FFmpeg, args...).CombinedOutput(); err != nil {
		return fmt.Errorf("audio: %w %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func frameLimit(maxFrames int) []string {
	if maxFrames <= 0 {
		return nil
	}
	return []string{"-frames:v", fmt.Sprint(maxFrames)}
}

// start runs ffmpeg with args and returns its standard output as a Stream.
func (t Tools) start(ctx context.Context, name string, args []string) (Stream, error) {
	s := &processStream{name: name, cmd: Command(ctx, t.FFmpeg, args...)}
	s.cmd.Stderr = &s.stderr
	out, err := s.cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("ffmpeg (%s): %w", name, err)
	}
	if err := s.cmd.Start(); err != nil {
		return nil, fmt.Errorf("start ffmpeg (%s): %w", name, err)
	}
	s.out = out
	return s, nil
}

// processStream is the standard output of an ffmpeg process.
type processStream struct {
	name string
	cmd  *exec.Cmd
	out  io.ReadCloser
	// stderr is written by exec's copying goroutine until cmd.Wait returns,
	// so it is read only after that.
	stderr bytes.Buffer
}

func (s *processStream) Read(p []byte) (int, error) { return s.out.Read(p) }

func (s *processStream) Wait() error {
	// Draining lets the process exit if reading stopped early. Once the
	// process ends or is killed the pipe closes, so a copy error says
	// nothing that the process's own exit status does not.
	_, _ = io.Copy(io.Discard, s.out)
	if err := s.cmd.Wait(); err != nil {
		return fmt.Errorf("ffmpeg (%s): %w %s", s.name, err, strings.TrimSpace(s.stderr.String()))
	}
	return nil
}
