package convert

import (
	"fmt"
	"time"
)

// Progress is a snapshot of a running conversion, from Options.Progress and
// the time since the conversion started.
type Progress struct {
	Done, Total int
	Elapsed     time.Duration
}

// FPS is the average speed so far in frames per second, 0 before the
// first frame.
func (p Progress) FPS() float64 {
	if p.Done == 0 || p.Elapsed <= 0 {
		return 0
	}
	return float64(p.Done) / p.Elapsed.Seconds()
}

// Remaining estimates the time left at the average speed so far; ok is
// false while there is nothing to estimate from.
func (p Progress) Remaining() (left time.Duration, ok bool) {
	fps := p.FPS()
	if fps == 0 || p.Total <= p.Done {
		return 0, false
	}
	return time.Duration(float64(p.Total-p.Done) / fps * float64(time.Second)), true
}

// Progress line pieces.
const (
	progressCountFormat = "%d / %d"
	progressFPSFormat   = " · %.1f fps · %s"
	progressLeftFormat  = ", ~%s left"
)

// String is the progress line the GUI and CLI show:
// "120 / 450 · 6.1 fps · 0:20, ~0:54 left".
func (p Progress) String() string {
	s := fmt.Sprintf(progressCountFormat, p.Done, p.Total)
	if p.Done == 0 {
		return s
	}
	s += fmt.Sprintf(progressFPSFormat, p.FPS(), FormatDuration(p.Elapsed))
	if left, ok := p.Remaining(); ok {
		s += fmt.Sprintf(progressLeftFormat, FormatDuration(left))
	}
	return s
}

const secondsPerMinute, minutesPerHour = 60, 60

// FormatDuration writes d as "0:07", "12:34" or "1:02:03", rounded to the
// second.
func FormatDuration(d time.Duration) string {
	total := int(d.Round(time.Second).Seconds())
	h, m, s := total/(secondsPerMinute*minutesPerHour), total/secondsPerMinute%minutesPerHour, total%secondsPerMinute
	if h > 0 {
		return fmt.Sprintf("%d:%02d:%02d", h, m, s)
	}
	return fmt.Sprintf("%d:%02d", m, s)
}
