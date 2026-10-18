//go:build windows

package facade

import "os/exec"

func configureHarnessInstallProcess(cmd *exec.Cmd) { configureHarnessProcess(cmd) }
