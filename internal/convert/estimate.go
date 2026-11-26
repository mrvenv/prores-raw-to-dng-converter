package convert

import (
	"fmt"

	"rawconv/internal/dng"
	"rawconv/internal/ffmpeg"
)

// EstimateSize is about how many bytes of DNG frames converting a clip
// probed as p with c writes; maxFrames limits the frames (0 = all). The
// WAV and metadata.json are small next to them and left out. A clip whose
// frame count or size is unknown estimates to 0.
func EstimateSize(p ffmpeg.Probe, c dng.Compression, maxFrames int) int64 {
	frames := p.Frames
	if maxFrames > 0 && maxFrames < frames {
		frames = maxFrames
	}
	return int64(frames) * c.EstimatedFrameBytes(p.Width, p.Height)
}

// Decimal size units, as Finder and Explorer's drive sizes count them.
const (
	kilo = 1000
	mega = kilo * kilo
	giga = kilo * mega
	tera = kilo * giga
)

// FormatSize writes a byte count for people: "820 MB", "12.4 GB".
func FormatSize(bytes int64) string {
	switch {
	case bytes >= tera:
		return fmt.Sprintf("%.2f TB", float64(bytes)/tera)
	case bytes >= giga:
		return fmt.Sprintf("%.1f GB", float64(bytes)/giga)
	default:
		return fmt.Sprintf("%.0f MB", float64(bytes)/mega)
	}
}
