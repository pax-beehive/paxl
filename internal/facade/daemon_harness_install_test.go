package facade

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDaemonHarnessInstallationUsesExplicitLocalNpmCommand(t *testing.T) {
	for _, dryRun := range []bool{true, false} {
		runner := &fakeDaemonLifecycleRunner{path: "/local/bin/npm"}
		resp, err := NewDaemonLifecycleFacade(runner).InstallHarness(t.Context(), &DaemonHarnessInstallRequest{Harness: "dsh", DryRun: dryRun})
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
