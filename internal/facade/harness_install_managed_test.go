package facade

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPiManagedInspectionKeepsOfficialUpdateOwnership(t *testing.T) {
	agent := t.TempDir()
	managed := filepath.Join(agent, "install")
	modules := filepath.Join(managed, "releases", "1.3.0", "node_modules")
	pkg := filepath.Join(modules, "@earendil-works", "pi-coding-agent")
	require.NoError(t, os.MkdirAll(filepath.Join(pkg, "dist"), 0700))
	require.NoError(t, os.MkdirAll(filepath.Join(modules, ".bin"), 0700))
	require.NoError(t, os.MkdirAll(filepath.Join(agent, "bin"), 0700))
	executable := filepath.Join(pkg, "dist", "cli.js")
	require.NoError(t, os.WriteFile(executable, []byte("#!/bin/sh\nexit 99\n"), 0700))
	require.NoError(
		t,
		os.WriteFile(
			filepath.Join(pkg, "package.json"),
			[]byte(`{"name":"@earendil-works/pi-coding-agent","version":"1.3.0"}`),
			0600,
		),
	)
	require.NoError(t, os.Symlink(executable, filepath.Join(modules, ".bin", "pi")))
	require.NoError(
		t,
		os.WriteFile(
			filepath.Join(managed, "managed-install.json"),
			[]byte(`{"kind":"pi-managed-install","schemaVersion":1,"layout":"releases-v1"}`),
			0600,
		),
	)
	require.NoError(
		t,
		os.WriteFile(filepath.Join(managed, "current-version"), []byte("1.3.0\n"), 0600),
	)
	launcher := filepath.Join(agent, "bin", "pi")
	require.NoError(t, os.WriteFile(launcher, []byte("#!/bin/sh\nexit 99\n"), 0700))
	req := HarnessInspectRequest{Harness: "pi", Component: HarnessComponentCLI, Path: launcher}
	facade := NewHarnessUpgradeFacade()
	result, err := facade.Inspect(t.Context(), &req)
	require.NoError(t, err)
	require.Equal(t, "pi-managed", result.Source)
	require.Equal(t, "1.3.0", result.Version)
	resolved, err := filepath.EvalSymlinks(executable)
	require.NoError(t, err)
	require.Equal(t, resolved, result.ResolvedPath)
	_, err = facade.Upgrade(
		t.Context(),
		&HarnessUpgradeRequest{HarnessInspectRequest: req, DryRun: true},
	)
	require.ErrorContains(t, err, "run pi update")
}
