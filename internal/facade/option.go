package facade

import (
	"io"

	"github.com/pax-oss/paxl/internal/model"
)

type Option struct {
	AuthEvents    model.HarnessAuthEventSink
	VerboseWriter io.Writer
	Stdin         io.Reader
	Stdout        io.Writer
	Stderr        io.Writer
}

func WithVerboseWriter(writer io.Writer) func(*Option) {
	return func(option *Option) {
		option.VerboseWriter = writer
	}
}

func WithStreams(stdin io.Reader, stdout io.Writer, stderr io.Writer) func(*Option) {
	return func(option *Option) {
		option.Stdin = stdin
		option.Stdout = stdout
		option.Stderr = stderr
	}
}

func applyOptions(opts []func(*Option)) *Option {
	option := &Option{}
	for _, opt := range opts {
		if opt != nil {
			opt(option)
		}
	}
	return option
}

func WithAuthEvents(sink model.HarnessAuthEventSink) func(*Option) {
	return func(option *Option) { option.AuthEvents = sink }
}
