package facade

import (
	"fmt"
	"io"
	"os"

	"golang.org/x/term"
)

func requireHermesAuthTerminal(input io.Reader) error {
	file, ok := input.(*os.File)
	if !ok || !term.IsTerminal(int(file.Fd())) {
		return fmt.Errorf(
			"hermes provider selection requires a terminal; use --provider for noninteractive login",
		)
	}
	return nil
}
