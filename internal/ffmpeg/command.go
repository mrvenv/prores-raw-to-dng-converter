package ffmpeg

import (
	"context"
	"os/exec"
)

// Command prepares an ffmpeg or ffprobe process. Every process the app runs
// is started through it, so platform setup lives in one place: on Windows
// the process gets no console window (see hideConsole).
func Command(ctx context.Context, name string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, name, args...)
	hideConsole(cmd)
	return cmd
}
