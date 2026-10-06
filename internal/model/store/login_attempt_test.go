package store

import (
	"database/sql"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/pax-oss/paxl/internal/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoginAttemptGivenTwoProcessesWhenDifferentIdentitiesRaceThenOneDurableSelectionWins(
	t *testing.T,
) {
	path := filepath.Join(t.TempDir(), "auth.sqlite")
	first, err := Open(t.Context(), &OpenRequest{Path: path})
	require.NoError(t, err)
	defer func() { _ = first.Store.Close() }()
	second, err := Open(t.Context(), &OpenRequest{Path: path})
	require.NoError(t, err)
	defer func() { _ = second.Store.Close() }()
	attempt := &LoginAttempt{
		Scope:     "hosted",
		ID:        "attempt",
		ExpiresAt: time.Now().Add(time.Minute).Unix(),
	}
	actual, err := first.Store.EnsureLoginAttempt(t.Context(), attempt)
	require.NoError(t, err)
	assert.Equal(t, attempt.ID, actual.ID)
	var wg sync.WaitGroup
	results := make(chan *LoginAttempt, 2)
	for i, db := range []*Store{first.Store, second.Store} {
		wg.Go(func() {
			selected, err := db.SelectLoginIdentity(
				t.Context(),
				&LoginSelection{
					AttemptID:  attempt.ID,
					LoginID:    "same-regional-id",
					ManagerURL: []string{"https://us", "https://hk"}[i],
					UserID:     []string{"usr_a", "usr_b"}[i],
					Email:      []string{"a@example.com", "b@example.com"}[i],
				},
			)
			assert.NoError(t, err)
			results <- selected
		})
	}
	wg.Wait()
	close(results)
	var winner *LoginAttempt
	for result := range results {
		require.NotNil(t, result)
		if winner == nil {
			winner = result
		}
		assert.Equal(t, winner.Selection, result.Selection)
	}
	reopened, err := Open(t.Context(), &OpenRequest{Path: path})
	require.NoError(t, err)
	defer func() { _ = reopened.Store.Close() }()
	recovered, err := reopened.Store.EnsureLoginAttempt(
		t.Context(),
		&LoginAttempt{Scope: "hosted", ID: "replacement", ExpiresAt: attempt.ExpiresAt},
	)
	require.NoError(t, err)
	assert.Equal(t, winner.Selection, recovered.Selection)
	assert.Equal(t, attempt.ID, recovered.ID)
}

func TestLoginAttemptGivenRecoveryAndInvalidInputsThenOnlyPinnedCredentialIsSaved(t *testing.T) {
	opened, err := Open(t.Context(), &OpenRequest{Path: filepath.Join(t.TempDir(), "auth.sqlite")})
	require.NoError(t, err)
	db := opened.Store
	defer func() { _ = db.Close() }()
	for _, input := range []*LoginAttempt{nil, {}, {ID: "id", Scope: "scope", ExpiresAt: 1}} {
		_, err := db.EnsureLoginAttempt(t.Context(), input)
		assert.Error(t, err)
	}
	for _, input := range []*LoginSelection{nil, {}, {AttemptID: "missing", LoginID: "id", ManagerURL: "https://hk", UserID: "usr_a", Email: "a@example.com"}} {
		_, err := db.SelectLoginIdentity(t.Context(), input)
		assert.Error(t, err)
	}
	_, err = db.SaveLoginCandidate(t.Context(), "missing", nil)
	assert.Error(t, err)
	candidate := &LoginCandidate{ManagerURL: "https://hk", LoginID: "login", PollToken: "poll"}
	_, err = db.SaveLoginCandidate(t.Context(), "missing", candidate)
	assert.Error(t, err)
	attempt, err := db.EnsureLoginAttempt(
		t.Context(),
		&LoginAttempt{ID: "attempt", Scope: "scope", ExpiresAt: time.Now().Add(time.Minute).Unix()},
	)
	require.NoError(t, err)
	saved, err := db.SaveLoginCandidate(t.Context(), attempt.ID, candidate)
	require.NoError(t, err)
	assert.Equal(t, candidate, saved)
	retry := *candidate
	retry.LoginID = "replacement"
	saved, err = db.SaveLoginCandidate(t.Context(), attempt.ID, &retry)
	require.NoError(t, err)
	assert.Equal(t, candidate, saved, "restart must retain the first request for a region")
	selection := &LoginSelection{
		AttemptID:  attempt.ID,
		LoginID:    candidate.LoginID,
		ManagerURL: candidate.ManagerURL,
		UserID:     "usr_a",
		Email:      "a@example.com",
	}
	_, err = db.SelectLoginIdentity(t.Context(), selection)
	require.NoError(t, err)
	credential := &model.AuthCredential{
		ManagerURL:   candidate.ManagerURL,
		APIKey:       "key",
		UserAPIKeyID: "key_id",
		UserID:       selection.UserID,
		Email:        selection.Email,
	}
	assert.Error(t, db.SaveLoginCredential(t.Context(), nil, credential))
	assert.Error(t, db.SaveLoginCredential(t.Context(), selection, nil))
	wrong := *credential
	wrong.Email = "other@example.com"
	assert.Error(t, db.SaveLoginCredential(t.Context(), selection, &wrong))
	require.NoError(t, db.SaveLoginCredential(t.Context(), selection, credential))
	recovered, err := db.EnsureLoginAttempt(
		t.Context(),
		&LoginAttempt{ID: "new", Scope: "scope", ExpiresAt: attempt.ExpiresAt},
	)
	require.NoError(t, err)
	assert.Equal(t, "key_id", recovered.SavedKeyID)
	require.NoError(t, db.FinishLoginAttempt(t.Context(), attempt.ID))
	assert.Error(
		t,
		db.SaveLoginCredential(t.Context(), selection, credential),
		"an old process cannot overwrite credentials after its attempt was replaced",
	)
	saved, err = db.GetLoginCandidate(t.Context(), attempt.ID, candidate.ManagerURL)
	require.ErrorIs(t, err, sql.ErrNoRows)
	assert.Nil(t, saved)
	require.NoError(t, db.Close())
	_, err = db.EnsureLoginAttempt(
		t.Context(),
		&LoginAttempt{ID: "closed", Scope: "scope", ExpiresAt: attempt.ExpiresAt},
	)
	assert.Error(t, err)
	_, err = db.SelectLoginIdentity(t.Context(), selection)
	assert.Error(t, err)
	_, err = db.GetLoginCandidate(t.Context(), attempt.ID, candidate.ManagerURL)
	assert.Error(t, err)
	_, err = db.SaveLoginCandidate(t.Context(), attempt.ID, candidate)
	assert.Error(t, err)
	assert.Error(t, db.SaveLoginCredential(t.Context(), selection, credential))
	assert.Error(t, db.FinishLoginAttempt(t.Context(), attempt.ID))
}
