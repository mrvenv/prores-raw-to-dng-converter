package ffmpeg

import (
	"os/exec"
	"syscall"

	"golang.org/x/sys/windows"
)

// hideConsole starts cmd without a console window. The GUI is a windowed
// app with no console of its own, so Windows would otherwise open a new
// terminal window for every ffmpeg it runs. The process needs no console:
// its stdin is unused (-nostdin) and its output goes to pipes. Cancellation
// goes through the context, not Ctrl+C on a shared console.
func hideConsole(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: windows.CREATE_NO_WINDOW,
	}
}
