package convert

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"math"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"rawconv/internal/dng"
	"rawconv/internal/ffmpeg"
)

func TestOutputName(t *testing.T) {
	for c, want := range map[dng.Compression]string{
		dng.Uncompressed:   "camraw_none",
		dng.LosslessJPEG:   "camraw_lossless",
		dng.LosslessJPEG12: "camraw_12bit",
		dng.Ratio3:         "camraw_3to1",
		dng.Ratio8:         "camraw_8to1",
	} {
		if got := OutputName("/clips/camraw.MOV", c); got != want {
			t.Errorf("%v: got %q, want %q", c, got, want)
		}
	}
}

// fakeStream is a Stream over fixed bytes that records whether it was
// waited for.
type fakeStream struct {
	io.Reader
	err    error
	waited bool
}

func (s *fakeStream) Wait() error { s.waited = true; return s.err }

// fakeSource is a Source with no ffmpeg behind it, as on a mobile platform.
type fakeSource struct {
	probe           ffmpeg.Probe
	packets, frames *fakeStream
	audio           []ffmpeg.AudioRequest
}

func (f *fakeSource) ProbeFile(context.Context, string) (ffmpeg.Probe, error) { return f.probe, nil }
func (f *fakeSource) Packets(context.Context, string, int) (ffmpeg.Stream, error) {
	return f.packets, nil
}
func (f *fakeSource) Frames(context.Context, string, int) (ffmpeg.Stream, error) {
	return f.frames, nil
}
func (f *fakeSource) ExtractAudio(_ context.Context, req ffmpeg.AudioRequest) error {
	f.audio = append(f.audio, req)
	return nil
}

func newFakeSource(codec string) *fakeSource {
	return &fakeSource{
		probe:   ffmpeg.Probe{Codec: codec, FrameRateN: 24, FrameRateD: 1, HasAudio: true},
		packets: &fakeStream{Reader: bytes.NewReader(nil)},
		frames:  &fakeStream{Reader: bytes.NewReader(nil)},
	}
}

func TestConvertRejectsOtherCodecs(t *testing.T) {
	src := newFakeSource("h264")
	if _, err := Convert(context.Background(), src, "clip.mov", Options{OutDir: t.TempDir()}); err == nil {
		t.Fatal("want an error for a non-ProRes RAW clip")
	}
	if src.packets.waited || src.frames.waited {
		t.Error("streams were opened for a rejected clip")
	}
}

func TestConvertThroughSource(t *testing.T) {
	src := newFakeSource(ffmpeg.ProResRAWCodec)
	out := t.TempDir()
	res, err := Convert(context.Background(), src, "/clips/camraw.mov",
		Options{OutDir: out, Audio: true, Compression: dng.LosslessJPEG12})
	if err != nil {
		t.Fatal(err)
	}
	if res.Frames != 0 {
		t.Errorf("frames: got %d, want 0", res.Frames)
	}
	if !src.packets.waited || !src.frames.waited {
		t.Error("streams were not waited for")
	}
	wantWAV := filepath.Join(out, "camraw_12bit", "camraw_12bit.wav")
	if len(src.audio) != 1 || src.audio[0].WAV != wantWAV || src.audio[0].Duration != 0 {
		t.Errorf("audio requests: got %+v, want one for %s with no trim", src.audio, wantWAV)
	}
}

func TestConvertReportsStreamError(t *testing.T) {
	src := newFakeSource(ffmpeg.ProResRAWCodec)
	src.packets.err = errors.New("packets failed")
	_, err := Convert(context.Background(), src, "clip.mov", Options{OutDir: t.TempDir()})
	if !errors.Is(err, src.packets.err) {
		t.Errorf("got %v, want the packet stream's error", err)
	}
}

// prrfPacket builds a ProRes RAW packet whose header has the given size,
// an identity colour matrix and no white balance or gain (all zero), laid
// out as prr.ParseHeader reads it.
func prrfPacket(width, height, senselRange int) []byte {
	const (
		packetLen   = 96
		headerLen   = 88
		matrixStart = 36
		floatLen    = 4
		matrixSide  = 3
	)
	be := binary.BigEndian
	pkt := make([]byte, packetLen)
	be.PutUint32(pkt[0:], packetLen)
	copy(pkt[4:], "prrf")
	be.PutUint16(pkt[8:], headerLen)
	copy(pkt[12:], "test")
	be.PutUint16(pkt[16:], uint16(width))
	be.PutUint16(pkt[18:], uint16(height))
	be.PutUint16(pkt[26:], uint16(senselRange))
	for i := 0; i < matrixSide; i++ {
		be.PutUint32(pkt[matrixStart+floatLen*(i*matrixSide+i):], math.Float32bits(1))
	}
	return pkt
}

func TestConvertMeasuresMissingColour(t *testing.T) {
	const width, height = 16, 16
	pkt := prrfPacket(width, height, testRange)
	data, _ := bayerFrame(0.1, 0.2, 0.16, nil)
	src := newFakeSource(ffmpeg.ProResRAWCodec)
	src.packets.Reader = bytes.NewReader(pkt)
	src.frames.Reader = bytes.NewReader(data[:width*height*bytesPerSensel])
	var log []string
	res, err := Convert(context.Background(), src, "clip.mov", Options{
		OutDir: t.TempDir(), Compression: dng.Uncompressed,
		Log: func(s string) { log = append(log, s) },
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Frames != 1 || !res.Color.AutoWB || !res.Color.AutoExposure {
		t.Fatalf("frames %d AutoWB %v AutoExposure %v, want 1 frame with both measured",
			res.Frames, res.Color.AutoWB, res.Color.AutoExposure)
	}
	for _, want := range []string{"auto white balance", "auto exposure"} {
		if !slices.ContainsFunc(log, func(line string) bool { return strings.Contains(line, want) }) {
			t.Errorf("log %q has no %q warning", log, want)
		}
	}
}

func TestFramesDuration(t *testing.T) {
	if got, want := framesDuration(48, 24), 2*time.Second; got != want {
		t.Errorf("got %v, want %v", got, want)
	}
}
