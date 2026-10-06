package facade

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/pax-oss/paxl/internal/model"
	"github.com/pax-oss/paxl/internal/model/store"
)

const clientCommitProtocol = "client_commit_v1"

func validateLoginTargets(targets []string) ([]string, error) {
	seen := make(map[string]bool)
	result := make([]string, 0, len(targets))
	for _, target := range targets {
		u, err := url.Parse(target)
		if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" ||
			(u.Path != "" && u.Path != "/") {
			return nil, fmt.Errorf(
				"login target must be an origin without credentials, path or query",
			)
		}
		local := u.Hostname() == "localhost" || net.ParseIP(u.Hostname()).IsLoopback()
		if u.Scheme != "https" && (u.Scheme != "http" || !local) {
			return nil, fmt.Errorf("login target requires HTTPS")
		}
		target = strings.TrimRight(u.String(), "/")
		if !seen[target] {
			seen[target] = true
			result = append(result, target)
		}
	}
	if len(result) == 0 || len(result) > 2 {
		return nil, fmt.Errorf("login requires one or two manager targets")
	}
	return result, nil
}

func (f *AuthFacade) loginRegional(ctx context.Context, req *LoginRequest) (*LoginResponse, error) {
	targets := req.ManagerURLs
	if len(targets) == 0 {
		if req.ManagerURL != "" {
			targets = []string{req.ManagerURL}
		} else {
			targets = []string{"https://api.paxworkspace.net", "https://hk-api.paxworkspace.net"}
		}
	}
	if req.Admin && (req.ManagerURL == "" || len(targets) != 1) {
		return nil, fmt.Errorf("administrator login requires an explicit manager URL")
	}
	targets, err := validateLoginTargets(targets)
	if err != nil {
		return nil, err
	}
	scope := append([]string(nil), targets...)
	sort.Strings(scope)
	id := make([]byte, 24)
	if _, err := rand.Read(id); err != nil {
		return nil, fmt.Errorf("create login attempt: %w", err)
	}
	attempt, err := f.store.EnsureLoginAttempt(
		ctx,
		&store.LoginAttempt{
			ID:        hex.EncodeToString(id),
			Scope:     fmt.Sprintf("%s:admin=%t", strings.Join(scope, ","), req.Admin),
			ExpiresAt: time.Now().Add(9 * time.Minute).Unix(),
		},
	)
	if err != nil {
		return nil, err
	}
	timeout := req.Timeout
	if timeout <= 0 {
		timeout = 2 * time.Minute
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	candidates, err := f.startRegionalCandidates(ctx, req, attempt.ID, targets)
	if err != nil {
		return nil, err
	}
	start, err := regionalLoginLink(candidates, req.Admin)
	if err != nil {
		return nil, err
	}
	if req.OnStart != nil {
		if err := req.OnStart(start); err != nil {
			return nil, fmt.Errorf("login callback: %w", err)
		}
	}
	if attempt.Selection == nil {
		attempt, err = f.awaitRegionalSelection(ctx, attempt, candidates)
		if err != nil {
			return nil, err
		}
	}
	return f.finishRegionalLogin(ctx, attempt, candidates, start)
}

func (f *AuthFacade) startRegionalCandidates(
	ctx context.Context,
	req *LoginRequest,
	attemptID string,
	targets []string,
) ([]*store.LoginCandidate, error) {
	type result struct {
		candidate *store.LoginCandidate
		err       error
	}
	results := make(chan result, len(targets))
	for _, target := range targets {
		go func() { c, err := f.startRegionalCandidate(ctx, req, attemptID, target); results <- result{c, err} }()
	}
	var candidates []*store.LoginCandidate
	var firstError error
	for range targets {
		r := <-results
		if r.err != nil {
			if firstError == nil {
				firstError = r.err
			}
			continue
		}
		candidates = append(candidates, r.candidate)
	}
	if len(candidates) == 0 {
		return nil, firstError
	}
	return candidates, nil
}

func (f *AuthFacade) startRegionalCandidate(
	ctx context.Context,
	req *LoginRequest,
	attemptID, target string,
) (*store.LoginCandidate, error) {
	candidate, err := f.store.GetLoginCandidate(ctx, attemptID, target)
	if !errors.Is(err, sql.ErrNoRows) {
		return candidate, err
	}
	var envelope managerEnvelope[deviceLoginStartResponse]
	err = f.managerJSON(
		ctx,
		http.MethodPost,
		target,
		"/api/v1/paxl/device-login/start",
		"",
		map[string]string{"client_name": req.ClientName, "protocol": clientCommitProtocol},
		&envelope,
	)
	if err != nil {
		return nil, err
	}
	r := envelope.Data
	if r.Protocol != clientCommitProtocol || r.LoginID == "" || r.PollToken == "" ||
		r.UserCode == "" {
		return nil, fmt.Errorf("manager does not support safe client-commit login")
	}
	return f.store.SaveLoginCandidate(
		ctx,
		attemptID,
		&store.LoginCandidate{
			ManagerURL:              target,
			LoginID:                 r.LoginID,
			UserCode:                r.UserCode,
			PollToken:               r.PollToken,
			VerificationURI:         r.VerificationURI,
			VerificationURIComplete: r.VerificationURIComplete,
			Region:                  r.Region,
			Protocol:                r.Protocol,
			Interval:                r.Interval,
		},
	)
}

func regionalLoginLink(candidates []*store.LoginCandidate, admin bool) (*LoginStart, error) {
	first := candidates[0]
	link, err := url.Parse(first.VerificationURI)
	if err != nil || link.Host == "" || link.Scheme != "https" {
		return nil, fmt.Errorf("invalid login verification URL")
	}
	query := url.Values{}
	if admin && first.Region != "us" && first.Region != "hk" {
		return nil, fmt.Errorf("administrator login requires a regional Manager")
	}
	if admin {
		query.Set("admin", "1")
	}
	for _, candidate := range candidates {
		if len(candidates) == 1 {
			query.Set("code", candidate.UserCode)
			if candidate.Region != "" {
				query.Set("region", candidate.Region)
			}
		} else {
			if candidate.Region != "us" && candidate.Region != "hk" {
				return nil, fmt.Errorf("manager returned an unknown region")
			}
			if query.Has(candidate.Region + "_code") {
				return nil, fmt.Errorf("managers returned duplicate regions")
			}
			query.Set(candidate.Region+"_code", candidate.UserCode)
		}
	}
	link.RawQuery = query.Encode()
	return &LoginStart{
		ManagerURL:              first.ManagerURL,
		UserCode:                first.UserCode,
		VerificationURI:         first.VerificationURI,
		VerificationURIComplete: link.String(),
	}, nil
}

func (f *AuthFacade) awaitRegionalSelection(
	ctx context.Context,
	attempt *store.LoginAttempt,
	candidates []*store.LoginCandidate,
) (*store.LoginAttempt, error) {
	type result struct {
		candidate *store.LoginCandidate
		poll      *deviceLoginPollResponse
		err       error
	}
	for {
		results := make(chan result, len(candidates))
		for _, candidate := range candidates {
			go func() {
				poll, err := f.regionalLoginAction(ctx, candidate, "", "")
				results <- result{candidate, poll, err}
			}()
		}
		var winner *store.LoginAttempt
		var selectErr error
		for range candidates {
			r := <-results
			if winner != nil || selectErr != nil || r.err != nil || r.poll.Status != "confirmed" {
				continue
			}
			if r.poll.Region != r.candidate.Region || r.poll.User == nil ||
				r.poll.User.UserID == "" ||
				r.poll.User.Email == "" {
				selectErr = fmt.Errorf("login confirmation identity or region is invalid")
				continue
			}
			winner, selectErr = f.store.SelectLoginIdentity(
				ctx,
				&store.LoginSelection{
					AttemptID:  attempt.ID,
					LoginID:    r.candidate.LoginID,
					ManagerURL: r.candidate.ManagerURL,
					UserID:     r.poll.User.UserID,
					Email:      r.poll.User.Email,
				},
			)
		}
		if selectErr != nil {
			return nil, selectErr
		}
		if winner != nil {
			return winner, nil
		}
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("login: timed out waiting for approval: %w", ctx.Err())
		case <-time.After(regionalPollInterval(candidates)):
		}
	}
}

func (f *AuthFacade) regionalLoginAction(
	ctx context.Context,
	candidate *store.LoginCandidate,
	action, userID string,
) (*deviceLoginPollResponse, error) {
	var envelope managerEnvelope[deviceLoginPollResponse]
	err := f.managerJSON(
		ctx,
		http.MethodPost,
		candidate.ManagerURL,
		"/api/v1/paxl/device-login/poll",
		"",
		map[string]string{
			"login_id":         candidate.LoginID,
			"poll_token":       candidate.PollToken,
			"action":           action,
			"expected_user_id": userID,
		},
		&envelope,
	)
	return &envelope.Data, err
}

func (f *AuthFacade) finishRegionalLogin(
	ctx context.Context,
	attempt *store.LoginAttempt,
	candidates []*store.LoginCandidate,
	start *LoginStart,
) (*LoginResponse, error) {
	var selected *store.LoginCandidate
	for _, candidate := range candidates {
		if candidate.LoginID == attempt.Selection.LoginID &&
			candidate.ManagerURL == attempt.Selection.ManagerURL {
			selected = candidate
		}
	}
	if selected == nil {
		return nil, fmt.Errorf("selected login region is unavailable; retry the same login")
	}
	poll, err := f.regionalLoginAction(ctx, selected, "commit", attempt.Selection.UserID)
	if err != nil {
		return nil, fmt.Errorf("login commit outcome is unknown; rerun login to resume: %w", err)
	}
	credential, err := f.persistRegionalCredential(ctx, attempt, selected, poll)
	if err != nil {
		return nil, err
	}
	ack, err := f.regionalLoginAction(ctx, selected, "ack", attempt.Selection.UserID)
	if err != nil {
		return nil, fmt.Errorf("credential saved; rerun login to acknowledge receipt: %w", err)
	}
	if ack.Status != "consumed" {
		return nil, fmt.Errorf(
			"credential saved but receipt was not acknowledged; rerun login to resume",
		)
	}
	for _, candidate := range candidates {
		if candidate.ManagerURL != selected.ManagerURL {
			_, _ = f.regionalLoginAction(ctx, candidate, "cancel", "")
		}
	}
	if err := f.store.FinishLoginAttempt(ctx, attempt.ID); err != nil {
		return nil, err
	}
	return &LoginResponse{
		ManagerURL:              selected.ManagerURL,
		UserCode:                selected.UserCode,
		VerificationURI:         start.VerificationURI,
		VerificationURIComplete: start.VerificationURIComplete,
		Credential:              credential,
	}, nil
}

func (f *AuthFacade) persistRegionalCredential(
	ctx context.Context,
	attempt *store.LoginAttempt,
	selected *store.LoginCandidate,
	poll *deviceLoginPollResponse,
) (*model.AuthCredential, error) {
	if poll.Status == "consumed" {
		saved, err := f.store.GetAuthCredential(ctx)
		if err != nil {
			return nil, err
		}
		if saved.Credential == nil || saved.Credential.UserAPIKeyID != attempt.SavedKeyID ||
			attempt.SavedKeyID == "" {
			return nil, fmt.Errorf("login receipt no longer matches the saved credential")
		}
		return saved.Credential, nil
	}
	if poll.Status != "approved" || poll.Region != selected.Region || poll.APIKey == "" ||
		poll.User == nil ||
		poll.User.UserID != attempt.Selection.UserID ||
		poll.User.Email != attempt.Selection.Email ||
		apiKeyID(poll.APIKeyRef) == "" {
		return nil, fmt.Errorf("issued credential does not match the pinned login identity")
	}
	credential := &model.AuthCredential{
		ManagerURL:   selected.ManagerURL,
		APIKey:       poll.APIKey,
		UserAPIKeyID: apiKeyID(poll.APIKeyRef),
		NodeID:       poll.NodeID,
		UserID:       poll.User.UserID,
		Email:        poll.User.Email,
		DisplayName:  poll.User.DisplayName,
		Role:         poll.User.Role,
	}
	if err := f.store.SaveLoginCredential(ctx, attempt.Selection, credential); err != nil {
		return nil, err
	}
	return credential, nil
}

func regionalPollInterval(candidates []*store.LoginCandidate) time.Duration {
	interval := int64(2)
	for _, candidate := range candidates {
		if candidate.Interval > interval {
			interval = candidate.Interval
		}
	}
	if interval > 30 {
		interval = 30
	}
	return time.Duration(interval) * time.Second
}
