package facade

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestArtifactHTTPClientReturnsRedirectWithoutFollowing(t *testing.T) {
	t.Parallel()

	requestCount := 0
	client := &http.Client{
		Transport: artifactRoundTripperFunc(func(req *http.Request) (*http.Response, error) {
			requestCount++
			if requestCount > 1 {
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader("redirected HTML")),
					Header:     make(http.Header),
					Request:    req,
				}, nil
			}
			return &http.Response{
				StatusCode: http.StatusFound,
				Body:       io.NopCloser(strings.NewReader("redirect")),
				Header: http.Header{
					"Location": []string{"https://login.example.test/?state=redirect-secret"},
				},
				Request: req,
			}, nil
		}),
	}

	req, err := http.NewRequestWithContext(
		context.Background(),
		http.MethodGet,
		"https://manager.example.test/resolver",
		nil,
	)
	require.NoError(t, err)
	resp, err := NewArtifactHTTPClient(client).Do(req)

	require.NoError(t, err)
	require.NotNil(t, resp)
	defer closeBody(resp.Body)
	assert.Equal(t, http.StatusFound, resp.StatusCode)
	assert.Equal(t, 1, requestCount)
}

func TestArtifactHTTPClientPreservesInjectedFake(t *testing.T) {
	t.Parallel()

	fake := &artifactFakeHTTPClient{}
	assert.Same(t, fake, NewArtifactHTTPClient(fake))
}

func TestSanitizeArtifactHTTPErrorRemovesRequestURL(t *testing.T) {
	t.Parallel()

	err := &url.Error{
		Op:  "Get",
		URL: "https://objects.example.test/paxl?X-Amz-Signature=transport-secret",
		Err: errors.New("network unavailable"),
	}

	safe := SanitizeArtifactHTTPError(err)
	require.Error(t, safe)
	assert.EqualError(t, safe, "artifact HTTP transport failed")
	assert.NotContains(t, safe.Error(), "transport-secret")
	assert.NotContains(t, safe.Error(), "objects.example.test")
}

func TestSanitizeArtifactHTTPErrorPreservesContextCancellation(t *testing.T) {
	t.Parallel()

	assert.ErrorIs(t, SanitizeArtifactHTTPError(context.Canceled), context.Canceled)
	assert.ErrorIs(t, SanitizeArtifactHTTPError(context.DeadlineExceeded), context.DeadlineExceeded)
}

type artifactRoundTripperFunc func(*http.Request) (*http.Response, error)

func (f artifactRoundTripperFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

type artifactFakeHTTPClient struct{}

func (*artifactFakeHTTPClient) Do(*http.Request) (*http.Response, error) {
	return nil, errors.New("unused")
}
