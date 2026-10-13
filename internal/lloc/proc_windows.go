package lloc

import (
	"os/exec"
	"syscall"
)

// hideWindow keeps the Python console from flashing up next to the GUI.
func hideWindow(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
}
