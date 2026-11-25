//go:build !windows

package ffmpeg

import "os/exec"

// hideConsole does nothing: only Windows opens a console window for a
// child process.
func hideConsole(*exec.Cmd) {}
