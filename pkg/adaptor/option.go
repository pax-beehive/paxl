package adaptor

import "io"

type Option struct {
	VerboseWriter io.Writer
}

func WithVerboseWriter(writer io.Writer) func(*Option) {
	return func(option *Option) {
		option.VerboseWriter = writer
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
