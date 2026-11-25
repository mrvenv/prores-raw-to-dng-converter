package convert

import (
	"math"
	"strings"
	"testing"
	"time"

	"rawconv/internal/ffmpeg"
)

// atomosTags are the format tags of a real Atomos Shogun Inferno recording
// of a Sony camera (FFmpeg sample ticket 7887).
var atomosTags = map[string]string{
	"make":                                        "Atomos",
	"encoder":                                     "ShogunInferno - 9.0 ",
	"com.apple.proapps.manufacturer":              "Sony",
	"com.apple.proapps.exif.{Exif}.FNumber":       "1.000084",
	"org.smpte.rdd18.lens.irisfnumber":            "F1.0",
	"org.smpte.rdd18.camera.shutterspeed_angle":   "179.2deg",
	"org.smpte.rdd18.camera.whitebalance":         "4300K",
	"com.apple.proapps.exif.{Exif}.ExposureIndex": "4000.000000",
	"org.smpte.rdd18.camera.isosensitivity":       "4000",
	"com.apple.proapps.image.{TIFF}.Make":         "Atomos",
	"com.apple.proapps.image.{TIFF}.Model":        "ShogunInferno",
	"com.apple.proapps.image.{TIFF}.Software":     "9.0",
	"creation_time":                               "2018-04-12T01:37:04.000000Z",
}

func TestCameraInfoAtomos(t *testing.T) {
	c := cameraInfoFromProbe(ffmpeg.Probe{FormatTags: atomosTags, FrameRateN: 25, FrameRateD: 1})
	if c.Make != "Sony" || c.Model != "" {
		t.Errorf("camera %q %q, want Sony with no model", c.Make, c.Model)
	}
	if c.Recorder != "Atomos ShogunInferno 9.0" {
		t.Errorf("recorder %q", c.Recorder)
	}
	if c.ISO != 4000 || c.FNumber != 1.0 || c.WhiteBalanceK != 4300 {
		t.Errorf("ISO %d f/%v WB %d", c.ISO, c.FNumber, c.WhiteBalanceK)
	}
	if want := 179.2 / 360 / 25; math.Abs(c.ExposureTime-want) > 1e-9 {
		t.Errorf("exposure %v, want %v", c.ExposureTime, want)
	}
	if !c.Created.Equal(time.Date(2018, 4, 12, 1, 37, 4, 0, time.UTC)) {
		t.Errorf("created %v", c.Created)
	}
}

func TestCameraInfoInternalRecording(t *testing.T) {
	c := cameraInfoFromProbe(ffmpeg.Probe{FormatTags: map[string]string{
		"com.apple.quicktime.make":  "Nikon",
		"com.apple.quicktime.model": "Z 9",
	}})
	if c.UniqueModel() != "Nikon Z 9" || c.Recorder != "" {
		t.Errorf("got %q, recorder %q", c.UniqueModel(), c.Recorder)
	}
}

func TestCameraInfoMalformed(t *testing.T) {
	c := cameraInfoFromProbe(ffmpeg.Probe{FormatTags: map[string]string{
		"org.smpte.rdd18.camera.isosensitivity":     "high",
		"org.smpte.rdd18.camera.shutterspeed_angle": "180deg",
		"creation_time": "yesterday",
	}})
	if c.ISO != 0 || c.ExposureTime != 0 || !c.Created.IsZero() {
		t.Errorf("malformed values leaked: %+v", c)
	}
}

// gh7Tags are the format tags of a Panasonic GH7 internal ProRes RAW clip:
// it records no shutter speed and has no external recorder.
var gh7Tags = map[string]string{
	"com.apple.proapps.exif.{Exif}.ExposureIndex": "500.000000",
	"com.apple.proapps.exif.{Exif}.FNumber":       "1.700000",
	"com.apple.proapps.manufacturer":              "Panasonic",
	"com.apple.proapps.modelname":                 "DC-GH7",
	"org.smpte.rdd18.camera.isosensitivity":       "500",
	"org.smpte.rdd18.camera.whitebalance":         "4500K",
	"org.smpte.rdd18.lens.irisfnumber":            "F1.7",
	"creation_time":                               "2026-11-24T11:02:48.000000Z",
}

func TestCameraInfoGH7(t *testing.T) {
	c := cameraInfoFromProbe(ffmpeg.Probe{FormatTags: gh7Tags, FrameRateN: 30000, FrameRateD: 1001})
	want := `camera "Panasonic DC-GH7", recorder unknown, ISO 500, aperture f/1.7, shutter unknown, WB 4500K`
	if got := c.Summary(); got != want {
		t.Errorf("summary\n got %s\nwant %s", got, want)
	}
}

func TestSummaryShutter(t *testing.T) {
	for exp, want := range map[float64]string{0.02: "shutter 1/50 s", 2: "shutter 2.0 s"} {
		if got := (CameraInfo{ExposureTime: exp}).Summary(); !strings.Contains(got, want) {
			t.Errorf("%v: %s", exp, got)
		}
	}
}
