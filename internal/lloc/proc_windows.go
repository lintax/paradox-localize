package lloc

import (
	"os/exec"
	"syscall"
)

// hideWindow keeps the Python console from flashing up next to the GUI.
func hideWindow(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
}

// interruptOnCancel keeps the default of killing the process: Windows has no
// interrupt to send to a process without a console. lloc writes its files
// atomically and saves after every batch, so a kill loses only the batches
// in flight.
func interruptOnCancel(cmd *exec.Cmd) {}
