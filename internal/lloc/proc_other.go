//go:build !windows

package lloc

import "os/exec"

func hideWindow(cmd *exec.Cmd) {}
