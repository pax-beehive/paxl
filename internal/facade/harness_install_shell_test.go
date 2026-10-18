package facade

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

type shellConfigInstallRunner struct{ update func() }

func (*shellConfigInstallRunner) LookPath(name string) (string, error) { return name, nil }
func (r *shellConfigInstallRunner) Run(context.Context, string, []string) error {
	r.update()
	return nil
}

func TestInstallReportsOnlyChangedShellConfigPaths(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("ZDOTDIR", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	changed := filepath.Join(home, ".bashrc")
	require.NoError(t, os.WriteFile(changed, []byte("old private settings"), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(home, ".zshrc"), []byte("unchanged"), 0600))
	runner := &shellConfigInstallRunner{update: func() {
		require.NoError(t, os.WriteFile(changed, []byte("new private settings"), 0600))
	}}
	result, err := NewHarnessInstallFacade(
		runner,
	).Install(t.Context(), &HarnessInstallRequest{Harness: "gemini"})
	require.NoError(t, err)
	data, err := json.Marshal(result)
	require.NoError(t, err)
	var fields map[string]any
	require.NoError(t, json.Unmarshal(data, &fields))
	require.Equal(t, []any{changed}, fields["changed_shell_files"])
	require.NotContains(t, string(data), "private settings")
}

func TestInstallDetectsNewCustomShellConfigWithoutRunningOnDryRun(t *testing.T) {
	home := t.TempDir()
	zdir := t.TempDir()
	xdg := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("ZDOTDIR", zdir)
	t.Setenv("XDG_CONFIG_HOME", xdg)
	paths := []string{
		filepath.Join(home, ".bash_profile"),
		filepath.Join(zdir, ".zshrc"),
		filepath.Join(xdg, "fish", "config.fish"),
	}
	called := false
	runner := &shellConfigInstallRunner{update: func() {
		called = true
		for _, path := range paths {
			require.NoError(t, os.MkdirAll(filepath.Dir(path), 0700))
			require.NoError(t, os.WriteFile(path, []byte("export PATH=fixture"), 0600))
		}
	}}
	f := NewHarnessInstallFacade(runner)
	result, err := f.Install(t.Context(), &HarnessInstallRequest{Harness: "gemini", DryRun: true})
	require.NoError(t, err)
	require.False(t, called)
	require.Empty(t, result.ChangedShellFiles)
	result, err = f.Install(t.Context(), &HarnessInstallRequest{Harness: "gemini"})
	require.NoError(t, err)
	require.ElementsMatch(t, paths, result.ChangedShellFiles)
	result, err = f.Install(t.Context(), &HarnessInstallRequest{Harness: "gemini"})
	require.NoError(t, err)
	require.Empty(t, result.ChangedShellFiles)
}

func TestInstallObservesPublisherSpecificShellFiles(t *testing.T) {
	home := t.TempDir()
	xdg := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("ZDOTDIR", home)
	t.Setenv("XDG_CONFIG_HOME", xdg)
	paths := []string{
		filepath.Join(home, ".ashrc"),
		filepath.Join(home, ".config", "fish", "config.fish"),
		filepath.Join(home, ".config", "fish", "conf.d", "openclaw.fish"),
		filepath.Join(xdg, "bash", ".bashrc"),
		filepath.Join(xdg, "bash", ".bash_profile"),
		filepath.Join(xdg, "zsh", ".zshrc"),
		filepath.Join(xdg, "zsh", ".zshenv"),
	}
	runner := &shellConfigInstallRunner{update: func() {
		for _, path := range paths {
			require.NoError(t, os.MkdirAll(filepath.Dir(path), 0700))
			require.NoError(t, os.WriteFile(path, []byte("fixture"), 0600))
		}
	}}
	result, err := NewHarnessInstallFacade(
		runner,
	).Install(t.Context(), &HarnessInstallRequest{Harness: "gemini"})
	require.NoError(t, err)
	require.ElementsMatch(t, paths, result.ChangedShellFiles)
}
