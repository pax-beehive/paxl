package facade

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

type scriptInstallRunner struct {
	path    string
	content string
	args    []string
	runErr  error
}

func (*scriptInstallRunner) LookPath(name string) (string, error) { return name, nil }
func (r *scriptInstallRunner) Run(_ context.Context, _ string, args []string) error {
	r.path = args[0]
	content, err := os.ReadFile(r.path)
	if err != nil {
		return err
	}
	r.content = string(content)
	r.args = args[1:]
	return r.runErr
}

func TestOfficialInstallerIsDownloadedBeforeExecutionAndRemoved(t *testing.T) {
	for _, runErr := range []error{nil, errors.New("installer failed")} {
		runner := &scriptInstallRunner{runErr: runErr}
		f := NewHarnessInstallFacade(runner)
		f.client = roundTripFunc(func(req *http.Request) (*http.Response, error) {
			require.Equal(t, "https://claude.ai/install.sh", req.URL.String())
			require.Equal(t, "paxl", req.Header.Get("User-Agent"))
			return &http.Response{
				StatusCode: 200,
				Body:       io.NopCloser(strings.NewReader("#!/bin/sh\necho fixture\n")),
			}, nil
		})
		result, err := f.Install(t.Context(), &HarnessInstallRequest{Harness: "claude"})
		if runErr != nil {
			require.ErrorIs(t, err, runErr)
		} else {
			require.NoError(t, err)
			require.Equal(t, "completed", result.State)
		}
		require.Equal(t, "#!/bin/sh\necho fixture\n", runner.content)
		require.Equal(t, []string{"latest"}, runner.args)
		_, err = os.Stat(runner.path)
		require.True(t, os.IsNotExist(err))
	}
}

func TestFailedOfficialDownloadsNeverRunInstallers(t *testing.T) {
	for _, tc := range []struct {
		status int
		body   string
		err    error
	}{
		{status: 503, body: "unavailable"}, {status: 200, body: "<html>not a script</html>"},
		{status: 200, body: "#!/bin/sh\n" + strings.Repeat("x", 2*1024*1024)},
		{err: context.Canceled},
	} {
		runner := &scriptInstallRunner{}
		f := NewHarnessInstallFacade(runner)
		f.client = roundTripFunc(func(*http.Request) (*http.Response, error) {
			if tc.err != nil {
				return nil, tc.err
			}
			return &http.Response{
				StatusCode: tc.status,
				Body:       io.NopCloser(strings.NewReader(tc.body)),
			}, nil
		})
		_, err := f.Install(t.Context(), &HarnessInstallRequest{Harness: "claude"})
		require.Error(t, err)
		require.Empty(t, runner.path)
	}
}
