//go:build !windows

package facade

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestExecutableUpdateLockRejectsConcurrentWriter(t *testing.T) {
	path := filepath.Join(t.TempDir(), "paxl")
	require.NoError(t, os.WriteFile(path, []byte("old"), 0755))
	unlock, err := LockExecutableUpdate(path)
	require.NoError(t, err)
	_, err = LockExecutableUpdate(path)
	require.Error(t, err)
	unlock()
	unlock, err = LockExecutableUpdate(path)
	require.NoError(t, err)
	unlock()
}
