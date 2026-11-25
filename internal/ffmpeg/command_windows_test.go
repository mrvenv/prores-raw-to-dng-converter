package ffmpeg

import (
	"context"
	"testing"

	"golang.org/x/sys/windows"
)

// A process started by the windowed GUI must not open a console window.
func TestCommandHasNoConsoleWindow(t *testing.T) {
	cmd := Command(context.Background(), ffmpegName)
	if cmd.SysProcAttr == nil || cmd.SysProcAttr.CreationFlags&windows.CREATE_NO_WINDOW == 0 {
		t.Fatalf("SysProcAttr = %+v, want CREATE_NO_WINDOW", cmd.SysProcAttr)
	}
}
