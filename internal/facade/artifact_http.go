package facade

import (
	"context"
	"errors"
	"net/http"
)

var (
	errInvalidArtifactURL      = errors.New("invalid artifact URL")
	errInvalidArtifactResponse = errors.New("invalid artifact HTTP response")
	errArtifactResponseRead    = errors.New("artifact HTTP response read failed")
)

// NewArtifactHTTPClient returns an artifact client that exposes redirect
// responses to the caller. Concrete HTTP clients are cloned so callers keep
// their transport, timeout, cookie jar, and test configuration without
// mutating the injected client. Non-HTTP fakes remain directly injectable.
func NewArtifactHTTPClient(client UpdateHTTPClient) UpdateHTTPClient {
	if client == nil {
		client = http.DefaultClient
	}
	httpClient, ok := client.(*http.Client)
	if !ok {
		return client
	}
	clone := *httpClient
	clone.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}
	return &clone
}

// SanitizeArtifactHTTPError removes transport details that can include a
// complete presigned object URL. Cancellation remains inspectable by callers.
func SanitizeArtifactHTTPError(err error) error {
	switch {
	case errors.Is(err, context.Canceled):
		return context.Canceled
	case errors.Is(err, context.DeadlineExceeded):
		return context.DeadlineExceeded
	default:
		return errors.New("artifact HTTP transport failed")
	}
}
