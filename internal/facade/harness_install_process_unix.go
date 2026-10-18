//go:build !windows

package facade

import (
	"os"
	"os/exec"

	"golang.org/x/term"
)

func configureHarnessInstallProcess(cmd *exec.Cmd) {
	// Native installers may read /dev/tty. Keep interactive invocations in the
	// foreground group, as with direct execution of the official script.
	if file, ok := cmd.Stdin.(*os.File); ok && term.IsTerminal(int(file.Fd())) {
		return
	}
	configureHarnessProcess(cmd)
}
