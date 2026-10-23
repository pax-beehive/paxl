//go:build windows

package main

import "fmt"

// Detached cleanup is enabled on the distributed macOS and Linux binaries.
func startExecutionLogCleanup(string) {}

func runExecutionLogCleanupWorker() error {
	return fmt.Errorf("detached log cleanup is unsupported on Windows")
}
