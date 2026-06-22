package facade

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/pax-oss/paxl/internal/model"
	"github.com/pax-oss/paxl/internal/model/store"
)

const (
	DefaultManagerURL                 = "https://api.paxtech.net"
	defaultDeviceLoginPollIntervalSec = int64(2)
)

type AuthHTTPClient interface {
	Do(req *http.Request) (*http.Response, error)
}

type AuthFacade struct {
	client AuthHTTPClient
	store  *store.Store
}

type LoginRequest struct {
	ManagerURL string
	ClientName string
	Timeout    time.Duration
}

type LoginResponse struct {
	ManagerURL              string                `json:"manager_url"`
	UserCode                string                `json:"user_code"`
	VerificationURI         string                `json:"verification_uri"`
	VerificationURIComplete string                `json:"verification_uri_complete"`
	Credential              *model.AuthCredential `json:"credential"`
}

type WhoamiResponse struct {
	ManagerURL string                `json:"manager_url"`
	Credential *model.AuthCredential `json:"credential"`
	User       *AuthUser             `json:"user"`
}

type LogoutResponse struct {
	ManagerURL string `json:"manager_url,omitempty"`
	Email      string `json:"email,omitempty"`
}

type AuthUser struct {
	UserID      string `json:"user_id"`
	Email       string `json:"email"`
	DisplayName string `json:"display_name"`
	Role        string `json:"role"`
}

type deviceLoginStartResponse struct {
	LoginID                 string `json:"login_id"`
	UserCode                string `json:"user_code"`
	PollToken               string `json:"poll_token"`
	VerificationURI         string `json:"verification_uri"`
	VerificationURIComplete string `json:"verification_uri_complete"`
	Interval                int64  `json:"interval"`
}

type deviceLoginPollResponse struct {
	Status    string      `json:"status"`
	APIKey    string      `json:"api_key"`
	User      *AuthUser   `json:"user"`
	APIKeyRef *apiKeyMeta `json:"api_key_meta"`
}

type apiKeyMeta struct {
	KeyID string `json:"key_id"`
}

type managerEnvelope[T any] struct {
	Data    T      `json:"data"`
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func NewAuthFacade(client AuthHTTPClient, sessionStore *store.Store) *AuthFacade {
	if client == nil {
		client = http.DefaultClient
	}
	return &AuthFacade{client: client, store: sessionStore}
}

func (f *AuthFacade) Login(
	ctx context.Context,
	req *LoginRequest,
	opts ...func(*Option),
) (*LoginResponse, error) {
	_ = applyOptions(opts)
	if f.store == nil {
		return nil, fmt.Errorf("login: store is required")
	}
	if req == nil {
		return nil, fmt.Errorf("login: request is required")
	}
	managerURL, err := normalizeManagerURL(req.ManagerURL)
	if err != nil {
		return nil, fmt.Errorf("login: %w", err)
	}
	timeout := req.Timeout
	if timeout <= 0 {
		timeout = 2 * time.Minute
	}
	start, err := f.startDeviceLogin(ctx, managerURL, req.ClientName)
	if err != nil {
		return nil, err
	}
	pollInterval := time.Duration(start.Interval) * time.Second
	if start.Interval <= 0 {
		pollInterval = time.Duration(defaultDeviceLoginPollIntervalSec) * time.Second
	}
	pollCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	for {
		poll, err := f.pollDeviceLogin(pollCtx, managerURL, start.LoginID, start.PollToken)
		if err != nil {
			return nil, err
		}
		switch poll.Status {
		case "approved":
			if poll.APIKey == "" || poll.User == nil {
				return nil, fmt.Errorf("login: approved response missing credential")
			}
			credential := &model.AuthCredential{
				ManagerURL:   managerURL,
				APIKey:       poll.APIKey,
				UserID:       poll.User.UserID,
				Email:        poll.User.Email,
				DisplayName:  poll.User.DisplayName,
				Role:         poll.User.Role,
				UserAPIKeyID: apiKeyID(poll.APIKeyRef),
			}
			if _, err := f.store.SaveAuthCredential(
				pollCtx,
				&store.SaveAuthCredentialRequest{Credential: credential},
			); err != nil {
				return nil, err
			}
			return &LoginResponse{
				ManagerURL:              managerURL,
				UserCode:                start.UserCode,
				VerificationURI:         start.VerificationURI,
				VerificationURIComplete: start.VerificationURIComplete,
				Credential:              credential,
			}, nil
		case "pending":
			timer := time.NewTimer(pollInterval)
			select {
			case <-pollCtx.Done():
				timer.Stop()
				return nil, fmt.Errorf("login: timed out waiting for approval")
			case <-timer.C:
			}
		default:
			return nil, fmt.Errorf("login: device login status %q", poll.Status)
		}
	}
}

func (f *AuthFacade) Whoami(
	ctx context.Context,
	opts ...func(*Option),
) (*WhoamiResponse, error) {
	_ = applyOptions(opts)
	credential, err := f.requireCredential(ctx)
	if err != nil {
		return nil, err
	}
	var envelope managerEnvelope[struct {
		User AuthUser `json:"user"`
	}]
	if err := f.managerJSON(
		ctx,
		http.MethodGet,
		credential.ManagerURL,
		"/api/v1/user/self/me",
		credential.APIKey,
		nil,
		&envelope,
	); err != nil {
		return nil, err
	}
	user := envelope.Data.User
	credential.UserID = user.UserID
	credential.Email = user.Email
	credential.DisplayName = user.DisplayName
	credential.Role = user.Role
	if _, err := f.store.SaveAuthCredential(
		ctx,
		&store.SaveAuthCredentialRequest{Credential: credential},
	); err != nil {
		return nil, fmt.Errorf("save auth credential: %w", err)
	}
	return &WhoamiResponse{
		ManagerURL: credential.ManagerURL,
		Credential: credential,
		User:       &user,
	}, nil
}

func (f *AuthFacade) Logout(
	ctx context.Context,
	opts ...func(*Option),
) (*LogoutResponse, error) {
	_ = applyOptions(opts)
	credential, err := f.requireCredential(ctx)
	if err != nil {
		return nil, err
	}
	if credential.UserAPIKeyID != "" {
		var envelope managerEnvelope[map[string]bool]
		if err := f.managerJSON(
			ctx,
			http.MethodDelete,
			credential.ManagerURL,
			"/api/v1/user/self/api-keys/"+url.PathEscape(credential.UserAPIKeyID),
			credential.APIKey,
			nil,
			&envelope,
		); err != nil {
			return nil, fmt.Errorf("logout: revoke api key: %w", err)
		}
	}
	if _, err := f.store.DeleteAuthCredential(ctx); err != nil {
		return nil, err
	}
	return &LogoutResponse{ManagerURL: credential.ManagerURL, Email: credential.Email}, nil
}

func (f *AuthFacade) startDeviceLogin(
	ctx context.Context,
	managerURL string,
	clientName string,
) (*deviceLoginStartResponse, error) {
	body := map[string]string{"client_name": strings.TrimSpace(clientName)}
	var envelope managerEnvelope[deviceLoginStartResponse]
	if err := f.managerJSON(
		ctx,
		http.MethodPost,
		managerURL,
		"/api/v1/paxl/device-login/start",
		"",
		body,
		&envelope,
	); err != nil {
		return nil, err
	}
	return &envelope.Data, nil
}

func (f *AuthFacade) pollDeviceLogin(
	ctx context.Context,
	managerURL string,
	loginID string,
	pollToken string,
) (*deviceLoginPollResponse, error) {
	body := map[string]string{"login_id": loginID, "poll_token": pollToken}
	var envelope managerEnvelope[deviceLoginPollResponse]
	if err := f.managerJSON(
		ctx,
		http.MethodPost,
		managerURL,
		"/api/v1/paxl/device-login/poll",
		"",
		body,
		&envelope,
	); err != nil {
		return nil, err
	}
	return &envelope.Data, nil
}

func (f *AuthFacade) managerJSON(
	ctx context.Context,
	method string,
	managerURL string,
	path string,
	bearerToken string,
	body any,
	out any,
) error {
	requestURL := strings.TrimRight(managerURL, "/") + path
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("encode manager request: %w", err)
		}
		reader = bytes.NewReader(data)
	}
	httpReq, err := http.NewRequestWithContext(ctx, method, requestURL, reader) // #nosec G107
	if err != nil {
		return fmt.Errorf("create manager request: %w", err)
	}
	httpReq.Header.Set("Accept", "application/json")
	httpReq.Header.Set("User-Agent", "paxl-auth")
	if body != nil {
		httpReq.Header.Set("Content-Type", "application/json")
	}
	if bearerToken != "" {
		httpReq.Header.Set("Authorization", "Bearer "+bearerToken)
	}
	resp, err := f.client.Do(httpReq)
	if err != nil {
		return fmt.Errorf("request manager: %w", err)
	}
	defer closeBody(resp.Body)
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("manager returned HTTP %d", resp.StatusCode)
	}
	decoder := json.NewDecoder(io.LimitReader(resp.Body, 1<<20))
	if err := decoder.Decode(out); err != nil {
		return fmt.Errorf("decode manager response: %w", err)
	}
	return nil
}

func (f *AuthFacade) requireCredential(ctx context.Context) (*model.AuthCredential, error) {
	if f.store == nil {
		return nil, fmt.Errorf("auth: store is required")
	}
	resp, err := f.store.GetAuthCredential(ctx)
	if err != nil {
		return nil, err
	}
	if resp.Credential == nil || resp.Credential.APIKey == "" || resp.Credential.ManagerURL == "" {
		return nil, fmt.Errorf("not logged in")
	}
	return resp.Credential, nil
}

func normalizeManagerURL(raw string) (string, error) {
	managerURL := strings.TrimSpace(raw)
	if managerURL == "" {
		managerURL = DefaultManagerURL
	}
	parsed, err := url.Parse(managerURL)
	if err != nil {
		return "", err
	}
	if parsed.Scheme == "" || parsed.Host == "" {
		return "", fmt.Errorf("manager URL must include scheme and host")
	}
	return strings.TrimRight(parsed.String(), "/"), nil
}

func apiKeyID(meta *apiKeyMeta) string {
	if meta == nil {
		return ""
	}
	return meta.KeyID
}
