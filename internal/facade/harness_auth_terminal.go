package facade

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"golang.org/x/term"
)

func readTerminalAuthSecret(ctx context.Context, input *os.File, output io.Writer) (string, error) {
	// Disable echo before publishing the prompt. Restore from this goroutine so
	// cancellation cannot leave the shell waiting for a blocked reader to restore it.
	state, err := term.MakeRaw(int(input.Fd()))
	if err != nil {
		return "", fmt.Errorf("prepare hidden authentication input: %w", err)
	}
	defer term.Restore(int(input.Fd()), state) //nolint:errcheck // Best effort on every exit path.
	terminal := term.NewTerminal(struct {
		io.Reader
		io.Writer
	}{Reader: io.LimitReader(input, 4098), Writer: output}, "")
	return awaitAuthSecret(ctx, func() (string, error) {
		value, readErr := terminal.ReadPassword("Please input credential: ")
		if readErr != nil {
			return "", fmt.Errorf("read hidden authentication input: %w", readErr)
		}
		return readLocalAuthSecretLine(strings.NewReader(value + "\n"))
	})
}
