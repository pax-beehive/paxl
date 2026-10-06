package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/pax-oss/paxl/internal/model"
)

type LoginAttempt struct {
	ID, Scope  string
	SavedKeyID string
	ExpiresAt  int64
	Selection  *LoginSelection
}

type LoginSelection struct {
	AttemptID, LoginID, ManagerURL, UserID, Email string
}

type LoginCandidate struct {
	ManagerURL, LoginID, UserCode, PollToken string
	VerificationURI, VerificationURIComplete string
	Region, Protocol                         string
	Interval                                 int64
}

func (s *Store) EnsureLoginAttempt(
	ctx context.Context,
	attempt *LoginAttempt,
) (*LoginAttempt, error) {
	if attempt == nil || attempt.ID == "" || attempt.Scope == "" ||
		attempt.ExpiresAt <= time.Now().Unix() {
		return nil, fmt.Errorf("login attempt: valid identity, scope and expiry are required")
	}
	_, err := s.db.ExecContext(
		ctx,
		`INSERT INTO auth_login_attempts (scope, attempt_id, expires_at) VALUES (?, ?, ?)
		ON CONFLICT(scope) DO UPDATE SET attempt_id = excluded.attempt_id, expires_at = excluded.expires_at,
		selected_login_id = NULL, selected_manager_url = NULL, selected_user_id = NULL, selected_email = NULL, saved_key_id = NULL
		WHERE auth_login_attempts.expires_at <= ?`,
		attempt.Scope,
		attempt.ID,
		attempt.ExpiresAt,
		time.Now().Unix(),
	)
	if err != nil {
		return nil, fmt.Errorf("ensure login attempt: %w", err)
	}
	return scanLoginAttempt(
		s.db.QueryRowContext(ctx, loginAttemptSelect+` WHERE scope = ?`, attempt.Scope),
	)
}

// The conditional update is the selection point shared by all local processes.
// Losing callers receive the persisted winner and must not submit their candidate.
func (s *Store) SelectLoginIdentity(
	ctx context.Context,
	selection *LoginSelection,
) (*LoginAttempt, error) {
	if selection == nil || selection.AttemptID == "" || selection.LoginID == "" ||
		selection.ManagerURL == "" ||
		selection.UserID == "" ||
		selection.Email == "" {
		return nil, fmt.Errorf("select login identity: complete binding is required")
	}
	_, err := s.db.ExecContext(
		ctx,
		`UPDATE auth_login_attempts SET selected_login_id = ?, selected_manager_url = ?, selected_user_id = ?, selected_email = ?
		WHERE attempt_id = ? AND selected_login_id IS NULL AND expires_at > ?`,
		selection.LoginID,
		selection.ManagerURL,
		selection.UserID,
		selection.Email,
		selection.AttemptID,
		time.Now().Unix(),
	)
	if err != nil {
		return nil, fmt.Errorf("select login identity: %w", err)
	}
	attempt, err := scanLoginAttempt(
		s.db.QueryRowContext(
			ctx,
			loginAttemptSelect+` WHERE attempt_id = ? AND expires_at > ?`,
			selection.AttemptID,
			time.Now().Unix(),
		),
	)
	if err != nil {
		return nil, fmt.Errorf("read login identity: %w", err)
	}
	return attempt, nil
}

func (s *Store) SaveLoginCandidate(
	ctx context.Context,
	attemptID string,
	candidate *LoginCandidate,
) (*LoginCandidate, error) {
	if candidate == nil || candidate.ManagerURL == "" || candidate.LoginID == "" ||
		candidate.PollToken == "" {
		return nil, fmt.Errorf("save login candidate: complete candidate is required")
	}
	data, err := json.Marshal(candidate)
	if err != nil {
		return nil, fmt.Errorf("encode login candidate: %w", err)
	}
	_, err = s.db.ExecContext(
		ctx,
		`INSERT INTO auth_login_candidates (attempt_id, manager_url, payload)
		SELECT attempt_id, ?, ? FROM auth_login_attempts WHERE attempt_id = ? AND expires_at > ?
		ON CONFLICT(attempt_id, manager_url) DO NOTHING`,
		candidate.ManagerURL,
		string(data),
		attemptID,
		time.Now().Unix(),
	)
	if err != nil {
		return nil, fmt.Errorf("save login candidate: %w", err)
	}
	saved, err := s.GetLoginCandidate(ctx, attemptID, candidate.ManagerURL)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("login attempt expired or was replaced")
	}
	return saved, err
}

func (s *Store) GetLoginCandidate(
	ctx context.Context,
	attemptID, managerURL string,
) (*LoginCandidate, error) {
	var data string
	err := s.db.QueryRowContext(ctx, `SELECT payload FROM auth_login_candidates WHERE attempt_id = ? AND manager_url = ?`, attemptID, managerURL).
		Scan(&data)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, sql.ErrNoRows
	}
	if err != nil {
		return nil, fmt.Errorf("read login candidate: %w", err)
	}
	var candidate LoginCandidate
	if err := json.Unmarshal([]byte(data), &candidate); err != nil {
		return nil, fmt.Errorf("decode login candidate: %w", err)
	}
	return &candidate, nil
}

func (s *Store) FinishLoginAttempt(ctx context.Context, attemptID string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin login cleanup: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err = tx.ExecContext(
		ctx,
		`DELETE FROM auth_login_candidates WHERE attempt_id = ?`,
		attemptID,
	); err != nil {
		return fmt.Errorf("clear login candidates: %w", err)
	}
	if _, err = tx.ExecContext(
		ctx,
		`DELETE FROM auth_login_attempts WHERE attempt_id = ?`,
		attemptID,
	); err != nil {
		return fmt.Errorf("clear login attempt: %w", err)
	}
	return tx.Commit()
}

const loginAttemptSelect = `SELECT attempt_id, scope, expires_at, COALESCE(selected_login_id, ''), COALESCE(selected_manager_url, ''), COALESCE(selected_user_id, ''), COALESCE(selected_email, ''), COALESCE(saved_key_id, '') FROM auth_login_attempts`

func scanLoginAttempt(row *sql.Row) (*LoginAttempt, error) {
	var attempt LoginAttempt
	var selection LoginSelection
	if err := row.Scan(
		&attempt.ID,
		&attempt.Scope,
		&attempt.ExpiresAt,
		&selection.LoginID,
		&selection.ManagerURL,
		&selection.UserID,
		&selection.Email,
		&attempt.SavedKeyID,
	); err != nil {
		return nil, fmt.Errorf("read login attempt: %w", err)
	}
	if selection.LoginID != "" {
		selection.AttemptID = attempt.ID
		attempt.Selection = &selection
	}
	return &attempt, nil
}

// Save the credential and its receipt together before acknowledging the server.
func (s *Store) SaveLoginCredential(
	ctx context.Context,
	selection *LoginSelection,
	credential *model.AuthCredential,
) error {
	if selection == nil || credential == nil || credential.APIKey == "" ||
		credential.UserAPIKeyID == "" ||
		credential.ManagerURL != selection.ManagerURL ||
		credential.UserID != selection.UserID ||
		credential.Email != selection.Email {
		return fmt.Errorf("save login credential: mismatched identity")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin login receipt: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	result, err := tx.ExecContext(
		ctx,
		`UPDATE auth_login_attempts SET saved_key_id = ? WHERE attempt_id = ? AND selected_login_id = ? AND selected_manager_url = ? AND selected_user_id = ? AND selected_email = ? AND expires_at > ?`,
		credential.UserAPIKeyID,
		selection.AttemptID,
		selection.LoginID,
		selection.ManagerURL,
		selection.UserID,
		selection.Email,
		time.Now().Unix(),
	)
	if err != nil {
		return fmt.Errorf("save login receipt: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count != 1 {
		return fmt.Errorf("login attempt expired or was replaced")
	}
	now := time.Now().UTC().Format(time.RFC3339)
	_, err = tx.ExecContext(
		ctx,
		`INSERT INTO auth_credentials (id, manager_url, api_key, user_api_key_id, node_id, user_id, email, display_name, role, created_at, updated_at)
 VALUES ('default', ?, ?, ?, ?, ?, ?, ?, ?, ?, ?) ON CONFLICT(id) DO UPDATE SET manager_url=excluded.manager_url, api_key=excluded.api_key, user_api_key_id=excluded.user_api_key_id, node_id=excluded.node_id, user_id=excluded.user_id, email=excluded.email, display_name=excluded.display_name, role=excluded.role, updated_at=excluded.updated_at`,
		credential.ManagerURL,
		credential.APIKey,
		credential.UserAPIKeyID,
		credential.NodeID,
		credential.UserID,
		credential.Email,
		credential.DisplayName,
		credential.Role,
		now,
		now,
	)
	if err != nil {
		return fmt.Errorf("save selected credential: %w", err)
	}
	return tx.Commit()
}
