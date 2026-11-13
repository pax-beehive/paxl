package facade

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDaemonHarnessInstallationUsesExplicitLocalNpmCommand(t *testing.T) {
	for _, dryRun := range []bool{true, false} {
		runner := &fakeDaemonLifecycleRunner{path: "/local/bin/npm"}
		resp, err := NewDaemonLifecycleFacade(
			runner,
		).InstallHarness(t.Context(), &DaemonHarnessInstallRequest{Harness: "dsh", DryRun: dryRun})
		require.NoError(t, err)
		assert.Equal(t, "dsh", resp.Binary)
		if dryRun {
			assert.Empty(t, runner.name)
			assert.Equal(t, SetupStatusPending, resp.Status)
		} else {
			assert.Equal(t, "/local/bin/npm", runner.name)
			assert.Equal(t, []string{"install", "-g", "@deepseek-ai/dsh@latest"}, runner.args)
			assert.Equal(t, SetupStatusInstalled, resp.Status)
		}
	}
}

func TestDaemonHarnessInstallationRejectsInvalidInputsBeforeExecution(t *testing.T) {
	for _, req := range []*DaemonHarnessInstallRequest{nil, {}, {Harness: "other"}, {Harness: "dsh"}} {
		runner := &fakeDaemonLifecycleRunner{}
		_, err := NewDaemonLifecycleFacade(runner).InstallHarness(t.Context(), req)
		require.Error(t, err)
		assert.Empty(t, runner.name)
	}
}

func TestGivenDSCodeInstallWhenOfficialInstallerSucceedsThenUsesStandaloneChannel(t *testing.T) {
	runner := &dscodeInstallRunner{}
	f := NewDaemonLifecycleFacade(runner)
	f.client = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		assert.Equal(t, "raw.githubusercontent.com", r.URL.Host)
		assert.Contains(t, r.URL.Path, "/77db18bcdeb2fc19327f6f60810e249dc4eb47ca/install.sh")
		return &http.Response{
			StatusCode: 200,
			Body:       io.NopCloser(strings.NewReader("#!/bin/sh\nexit 0\n")),
		}, nil
	})
	resp, err := f.InstallHarness(t.Context(), &DaemonHarnessInstallRequest{Harness: "dscode"})
	require.NoError(t, err)
	assert.Equal(t, SetupStatusInstalled, resp.Status)
	assert.Equal(t, "/bin/sh", runner.name)
	assert.Equal(t, "#!/bin/sh\nexit 0\n", runner.script)
	assert.Equal(t, []string{"latest"}, runner.args[1:])
	assert.Contains(t, resp.Message, "DSCODE_HOME")
	_, err = os.Stat(runner.args[0])
	assert.True(t, os.IsNotExist(err))
}

type dscodeInstallRunner struct {
	name, script string
	args         []string
	err          error
}

func (r *dscodeInstallRunner) LookPath(string) (string, error) {
	return "", errors.New("npm must not be required")
}
func (r *dscodeInstallRunner) Run(_ context.Context, name string, args []string) error {
	r.name, r.args = name, append([]string(nil), args...)
	data, err := os.ReadFile(args[0])
	if err != nil {
		return err
	}
	r.script = string(data)
	return r.err
}

func TestGivenDSCodeInstallWhenDownloadOrExecutionFailsThenNeverReportsInstalled(t *testing.T) {
	for _, tc := range []struct {
		name         string
		code         int
		body         string
		network, run error
	}{
		{name: "transport", network: errors.New("offline")},
		{name: "http", code: 404},
		{name: "empty", code: 200},
		{name: "oversized", code: 200, body: strings.Repeat("x", 128*1024+1)},
		{name: "installer failure", code: 200, body: "#!/bin/sh\nexit 1", run: errors.New("foreign command exists")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			runner := &dscodeInstallRunner{err: tc.run}
			f := NewDaemonLifecycleFacade(runner)
			f.client = roundTripFunc(func(*http.Request) (*http.Response, error) {
				if tc.network != nil {
					return nil, tc.network
				}
				return &http.Response{
					StatusCode: tc.code,
					Body:       io.NopCloser(strings.NewReader(tc.body)),
				}, nil
			})
			resp, err := f.InstallHarness(
				t.Context(),
				&DaemonHarnessInstallRequest{Harness: "dscode"},
			)
			require.Error(t, err)
			assert.Nil(t, resp)
			if tc.run == nil {
				assert.Empty(t, runner.name)
			}
		})
	}
}
func TestGivenDSCodeDryRunThenDoesNotDownloadOrExecute(t *testing.T) {
	runner := &dscodeInstallRunner{}
	f := NewDaemonLifecycleFacade(runner)
	f.client = roundTripFunc(
		func(*http.Request) (*http.Response, error) {
			t.Fatal("dry run downloaded installer")
			return nil, errors.New("unexpected download")
		},
	)
	resp, err := f.InstallHarness(
		t.Context(),
		&DaemonHarnessInstallRequest{Harness: "dscode", DryRun: true},
	)
	require.NoError(t, err)
	assert.Equal(t, SetupStatusPending, resp.Status)
	assert.Contains(t, resp.Message, "self-contained")
	assert.Empty(t, runner.name)
}
