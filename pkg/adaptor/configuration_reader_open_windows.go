//go:build windows

package adaptor

import "os"

func openConfigurationFile(path string) (*os.File, error) {
	return os.Open(
		path,
	) // #nosec G304 -- Caller-selected native paths are checked as bounded regular files.
}
