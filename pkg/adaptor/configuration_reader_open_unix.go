//go:build !windows

package adaptor

import (
	"os"
	"syscall"
)

// Nonblocking open prevents a path swapped to a FIFO from hanging inventory.
func openConfigurationFile(path string) (*os.File, error) {
	return os.OpenFile(
		path,
		os.O_RDONLY|syscall.O_NONBLOCK,
		0,
	) // #nosec G304 -- Caller-selected native paths are checked as bounded regular files.
}
