//go:build !windows

package lloc

import (
	"os"
	"os/exec"
)

func hideWindow(cmd *exec.Cmd) {}

// interruptOnCancel makes a stopped job send an interrupt, on which lloc
// saves and exits; it is killed only if it does not exit within WaitDelay.
func interruptOnCancel(cmd *exec.Cmd) {
	cmd.Cancel = func() error { return cmd.Process.Signal(os.Interrupt) }
}
