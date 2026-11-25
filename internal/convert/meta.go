package convert

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"rawconv/internal/ffmpeg"
)

// QuickTime metadata keys written by ProRes RAW cameras and recorders
// (observed in Atomos files; Apple "proapps" keys and SMPTE RDD 18 keys).
const (
	keyManufacturer   = "com.apple.proapps.manufacturer"
	keyModelName      = "com.apple.proapps.modelname"
	keyCameraName     = "com.apple.proapps.cameraName"
	keyQTMake         = "com.apple.quicktime.make"
	keyQTModel        = "com.apple.quicktime.model"
	keyMake           = "make"
	keyModel          = "model"
	keyTIFFMake       = "com.apple.proapps.image.{TIFF}.Make"
	keyTIFFModel      = "com.apple.proapps.image.{TIFF}.Model"
	keyTIFFSoftware   = "com.apple.proapps.image.{TIFF}.Software"
	keyEncoder        = "encoder"
	keyISO            = "org.smpte.rdd18.camera.isosensitivity"
	keyExposureIndex  = "com.apple.proapps.exif.{Exif}.ExposureIndex"
	keyExifFNumber    = "com.apple.proapps.exif.{Exif}.FNumber"
	keyIrisFNumber    = "org.smpte.rdd18.lens.irisfnumber"
	keyShutterAngle   = "org.smpte.rdd18.camera.shutterspeed_angle"
	keyWhiteBalance   = "org.smpte.rdd18.camera.whitebalance"
	keyCreationTime   = "creation_time"
	shutterAngleUnit  = "deg"
	whiteBalanceUnit  = "K"
	fNumberPrefix     = "F"
	fullCircleDegrees = 360.0
)

// CameraInfo is what the clip's metadata says about the shot.
type CameraInfo struct {
	Make, Model   string // the camera
	Recorder      string // external recorder, e.g. "Atomos ShogunInferno 9.0"
	ISO           int
	FNumber       float64
	ExposureTime  float64 // seconds
	WhiteBalanceK int
	Created       time.Time
}

// unknownValue stands in for metadata the clip does not carry.
const unknownValue = "unknown"

// Summary describes the shot for the log; missing fields read "unknown"
// rather than as a zero that looks like a real value.
func (c CameraInfo) Summary() string {
	orUnknown := func(ok bool, format string, v any) string {
		if !ok {
			return unknownValue
		}
		return fmt.Sprintf(format, v)
	}
	shutter := unknownValue
	switch {
	case c.ExposureTime >= 1:
		shutter = fmt.Sprintf("%.1f s", c.ExposureTime)
	case c.ExposureTime > 0:
		shutter = fmt.Sprintf("1/%.0f s", 1/c.ExposureTime)
	}
	return fmt.Sprintf("camera %s, recorder %s, ISO %s, aperture %s, shutter %s, WB %s",
		orUnknown(c.UniqueModel() != "", "%q", c.UniqueModel()),
		orUnknown(c.Recorder != "", "%q", c.Recorder),
		orUnknown(c.ISO > 0, "%d", c.ISO),
		orUnknown(c.FNumber > 0, "f/%.1f", c.FNumber),
		shutter,
		orUnknown(c.WhiteBalanceK > 0, "%dK", c.WhiteBalanceK))
}

// UniqueModel is the camera name for the DNG UniqueCameraModel tag.
func (c CameraInfo) UniqueModel() string {
	return strings.TrimSpace(c.Make + " " + c.Model)
}

// cameraInfoFromProbe reads camera metadata from the container tags. The
// recorder's own make/model (TIFF keys) is kept apart from the camera's so
// an Atomos recording is not labelled as an Atomos camera.
func cameraInfoFromProbe(p ffmpeg.Probe) CameraInfo {
	tags := p.FormatTags
	var c CameraInfo
	recorderModel := lookup(tags, keyTIFFModel)
	c.Recorder = strings.Join(nonEmpty(lookup(tags, keyTIFFMake), recorderModel, lookup(tags, keyTIFFSoftware)), " ")
	if c.Recorder == "" {
		c.Recorder = lookup(tags, keyEncoder)
	}

	c.Make = first(tags, keyManufacturer, keyQTMake, keyMake)
	for _, k := range []string{keyModelName, keyCameraName, keyQTModel, keyModel} {
		if v := lookup(tags, k); v != "" && v != recorderModel {
			c.Model = v
			break
		}
	}
	c.ISO = atoi(first(tags, keyISO, keyExposureIndex))
	c.FNumber = atof(strings.TrimPrefix(first(tags, keyIrisFNumber, keyExifFNumber), fNumberPrefix))
	if angle := atof(strings.TrimSuffix(lookup(tags, keyShutterAngle), shutterAngleUnit)); angle > 0 && p.FPS() > 0 {
		c.ExposureTime = angle / fullCircleDegrees / p.FPS()
	}
	c.WhiteBalanceK = atoi(strings.TrimSuffix(lookup(tags, keyWhiteBalance), whiteBalanceUnit))
	if t, err := time.Parse(time.RFC3339Nano, lookup(tags, keyCreationTime)); err == nil {
		c.Created = t
	}
	return c
}

// metadataFileName is the sidecar that keeps every source tag, including
// those DNG has no field for.
const metadataFileName = "metadata.json"

// sidecar is the JSON layout of metadataFileName.
type sidecar struct {
	Source     string            `json:"source"`
	FormatTags map[string]string `json:"format_tags"`
	VideoTags  map[string]string `json:"video_tags"`
}

// writeMetadata saves the clip's original metadata next to the frames.
func writeMetadata(outDir, input string, p ffmpeg.Probe) error {
	b, err := json.MarshalIndent(sidecar{filepath.Base(input), p.FormatTags, p.VideoTags}, "", "  ")
	if err != nil {
		return fmt.Errorf("metadata: %w", err)
	}
	return os.WriteFile(filepath.Join(outDir, metadataFileName), b, 0o644)
}

// lookup returns tags[key] trimmed, matching the key case-insensitively
// because muxers differ in capitalisation.
func lookup(tags map[string]string, key string) string {
	if v, ok := tags[key]; ok {
		return strings.TrimSpace(v)
	}
	for k, v := range tags {
		if strings.EqualFold(k, key) {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func first(tags map[string]string, keys ...string) string {
	for _, k := range keys {
		if v := lookup(tags, k); v != "" {
			return v
		}
	}
	return ""
}

func nonEmpty(vals ...string) []string {
	var out []string
	for _, v := range vals {
		if v != "" {
			out = append(out, v)
		}
	}
	return out
}

// atoi and atof return 0 for missing or malformed values: metadata is
// optional and a bad field must not stop a conversion.
func atoi(s string) int {
	return int(atof(s) + 0.5)
}

func atof(s string) float64 {
	f, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil || f < 0 {
		return 0
	}
	return f
}
