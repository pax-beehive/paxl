//go:build !windows

package adaptor

import (
	"errors"
	"os"
	"os/exec"
	"syscall"

	"golang.org/x/term"
)

func prepareNativeAuth(cmd *exec.Cmd) {
	// A child reading a real terminal must stay in the foreground group.
	// Pipe-backed commands can own a group so cancellation reaps descendants.
	if file, ok := cmd.Stdin.(*os.File); !ok || !term.IsTerminal(int(file.Fd())) {
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	}
	cmd.Cancel = func() error { return killNativeAuth(cmd) }
}
func killNativeAuth(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	if cmd.SysProcAttr == nil || !cmd.SysProcAttr.Setpgid {
		return cmd.Process.Kill()
	}
	err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	if errors.Is(err, syscall.ESRCH) {
		return os.ErrProcessDone
	}
	return err
}
func stopNativeAuth(cmd *exec.Cmd) { _ = killNativeAuth(cmd) }
