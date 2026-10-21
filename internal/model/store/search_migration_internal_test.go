package store

import (
	"context"
	"database/sql"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSearchMigrationGivenCanceledContextWhenStartingThenReturnsCancellation(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err = migrateSessionSearchIndex(ctx, db)

	require.ErrorIs(t, err, context.Canceled)
	require.ErrorContains(t, err, "read session search schema")
}
