package adaptor

import (
	"bytes"
	"encoding/json"
	"fmt"

	"github.com/pax-oss/paxl/internal/model"
)

// sdkAuthOutput separates helper-owned events from the final result. Native SDK
// diagnostics cannot become protocol frames: the embedded helper wraps them.
type sdkAuthOutput struct {
	result  boundedAuthOutput
	pending []byte
	sink    model.HarnessAuthEventSink
	err     error
}

func (w *sdkAuthOutput) Write(p []byte) (int, error) {
	n := len(p)
	if w.err != nil {
		return n, w.err
	}
	w.pending = append(w.pending, p...)
	if len(w.pending) > 128*1024 {
		w.err = fmt.Errorf("SDK authentication event exceeds limit")
		return n, nil
	}
	for {
		index := bytes.IndexByte(w.pending, '\n')
		if index < 0 {
			return n, nil
		}
		line := w.pending[:index]
		w.pending = w.pending[index+1:]
		var event model.HarnessAuthEvent
		if json.Unmarshal(line, &event) == nil && event.Version == 1 && event.Type != "" {
			if w.sink == nil {
				w.err = fmt.Errorf("unexpected SDK authentication event")
				return n, nil
			}
			if err := w.sink(&event); err != nil {
				w.err = err
				return n, err
			}
		} else {
			_, _ = w.result.Write(append(append([]byte{}, line...), '\n'))
		}
	}
}
