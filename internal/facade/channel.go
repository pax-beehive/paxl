package facade

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/pax-oss/paxl/internal/model"
	"github.com/pax-oss/paxl/internal/model/store"
)

// Tailscale reserves these address ranges for tailnet traffic. Hostnames are
// intentionally not resolved here so origin validation remains deterministic.
var tailscaleAddressPrefixes = []netip.Prefix{
	netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("fd7a:115c:a1e0::/48"),
}

type ChannelFacade struct {
	client AuthHTTPClient
	store  *store.Store
}

type ConnectChannelRequest struct {
	Kind            string
	Name            string
	URL             string
	EnrollmentToken string
	CAFile          string
	AutoReceive     bool
}

type ConnectChannelResponse struct {
	Profile *model.ChannelProfile `json:"profile"`
}

type ListChannelsRequest struct{}

type ListChannelsResponse struct {
	Profiles []*model.ChannelProfile `json:"profiles"`
}

type ChannelStatusRequest struct {
	Name string
}

type ChannelStatusResponse struct {
	Profile *model.ChannelProfile `json:"profile"`
	Status  string                `json:"status"`
}

type ListDirectoryAgentsRequest struct {
	Channel string
	Query   string
	Limit   int
	Cursor  string
}

type ListDirectoryAgentsResponse struct {
	Agents     []*model.ChannelAgent `json:"agents"`
	NextCursor string                `json:"next_cursor,omitempty"`
}

type GetDirectoryAgentRequest struct {
	Channel string
	AgentID string
}

type GetDirectoryAgentResponse struct {
	Agent *model.ChannelAgent `json:"agent"`
}

type exchangeEnrollmentResponse struct {
	CredentialID string `json:"credential_id"`
	APIKey       string `json:"api_key"`
}

type agentIdentityResponse struct {
	UserID       string   `json:"user_id"`
	AgentID      string   `json:"agent_id"`
	CredentialID string   `json:"credential_id"`
	Permissions  []string `json:"permissions"`
}

func NewChannelFacade(client AuthHTTPClient, sessionStore *store.Store) *ChannelFacade {
	if client == nil {
		client = http.DefaultClient
	}
	return &ChannelFacade{client: client, store: sessionStore}
}

func (f *ChannelFacade) List(
	ctx context.Context,
	req *ListChannelsRequest,
	opts ...func(*Option),
) (*ListChannelsResponse, error) {
	_ = req
	_ = applyOptions(opts)
	if f.store == nil {
		return nil, fmt.Errorf("list channels: store is required")
	}
	listed, err := f.store.ListChannelProfiles(ctx, &store.ListChannelProfilesRequest{})
	if err != nil {
		return nil, fmt.Errorf("list channels: %w", err)
	}
	return &ListChannelsResponse{Profiles: listed.Profiles}, nil
}

func (f *ChannelFacade) Status(
	ctx context.Context,
	req *ChannelStatusRequest,
	opts ...func(*Option),
) (*ChannelStatusResponse, error) {
	_ = applyOptions(opts)
	if req == nil || strings.TrimSpace(req.Name) == "" {
		return nil, fmt.Errorf("channel status: profile name is required")
	}
	profile, client, err := f.loadProfileClient(ctx, req.Name)
	if err != nil {
		return nil, err
	}
	identity, err := fetchChannelIdentity(ctx, client, profile)
	if err != nil {
		return nil, fmt.Errorf("channel status: %w", err)
	}
	applyChannelIdentity(profile, identity)
	if _, err := f.store.SaveChannelProfile(
		ctx,
		&store.SaveChannelProfileRequest{Profile: profile},
	); err != nil {
		return nil, fmt.Errorf("channel status: save identity: %w", err)
	}
	return &ChannelStatusResponse{Profile: profile, Status: "connected"}, nil
}

func (f *ChannelFacade) ListAgents(
	ctx context.Context,
	req *ListDirectoryAgentsRequest,
	opts ...func(*Option),
) (*ListDirectoryAgentsResponse, error) {
	_ = applyOptions(opts)
	if req == nil {
		return nil, fmt.Errorf("list channel agents: request is required")
	}
	profile, client, err := f.loadProfileClient(ctx, req.Channel)
	if err != nil {
		return nil, err
	}
	params := url.Values{}
	if strings.TrimSpace(req.Query) != "" {
		params.Set("q", strings.TrimSpace(req.Query))
	}
	if req.Limit > 0 {
		params.Set("limit", fmt.Sprintf("%d", req.Limit))
	}
	if req.Cursor != "" {
		params.Set("cursor", req.Cursor)
	}
	path := "/v1/channel/agents"
	if encoded := params.Encode(); encoded != "" {
		path += "?" + encoded
	}
	var response ListDirectoryAgentsResponse
	if err := doOnPremJSON(ctx, client, http.MethodGet, profile.URL, path, profile.APIKey,
		nil, &response, "list channel agents", "channel_send"); err != nil {
		return nil, err
	}
	return &response, nil
}

func (f *ChannelFacade) GetAgent(
	ctx context.Context,
	req *GetDirectoryAgentRequest,
	opts ...func(*Option),
) (*GetDirectoryAgentResponse, error) {
	_ = applyOptions(opts)
	if req == nil || strings.TrimSpace(req.AgentID) == "" {
		return nil, fmt.Errorf("get channel agent: agent id is required")
	}
	profile, client, err := f.loadProfileClient(ctx, req.Channel)
	if err != nil {
		return nil, err
	}
	var response GetDirectoryAgentResponse
	path := "/v1/channel/agents/" + url.PathEscape(strings.TrimSpace(req.AgentID))
	if err := doOnPremJSON(ctx, client, http.MethodGet, profile.URL, path, profile.APIKey,
		nil, &response, "get channel agent", "channel_send"); err != nil {
		return nil, err
	}
	return &response, nil
}

func (f *ChannelFacade) loadProfileClient(
	ctx context.Context,
	name string,
) (*model.ChannelProfile, AuthHTTPClient, error) {
	if f.store == nil {
		return nil, nil, fmt.Errorf("channel: store is required")
	}
	name = strings.TrimSpace(name)
	if name == "" {
		name = "onprem"
	}
	got, err := f.store.GetChannelProfile(ctx, &store.GetChannelProfileRequest{Name: name})
	if err != nil {
		return nil, nil, fmt.Errorf("load channel profile %q: %w", name, err)
	}
	if got.Profile == nil || !got.Profile.Enabled {
		return nil, nil, fmt.Errorf("channel profile %q is not connected or enabled", name)
	}
	client, err := channelHTTPClient(f.client, got.Profile.CAFile)
	if err != nil {
		return nil, nil, fmt.Errorf("load channel profile %q: %w", name, err)
	}
	return got.Profile, client, nil
}

func (f *ChannelFacade) Connect(
	ctx context.Context,
	req *ConnectChannelRequest,
	opts ...func(*Option),
) (*ConnectChannelResponse, error) {
	_ = applyOptions(opts)
	if req == nil {
		return nil, fmt.Errorf("connect channel: request is required")
	}
	if strings.TrimSpace(req.Kind) != string(model.ChannelKindOnPrem) {
		return nil, fmt.Errorf("connect channel: unsupported kind %q", req.Kind)
	}
	if strings.TrimSpace(req.EnrollmentToken) == "" {
		return nil, fmt.Errorf("connect channel: enrollment token is required")
	}
	origin, originFromToken, err := resolveChannelOrigin(req.URL, req.EnrollmentToken)
	if err != nil {
		return nil, fmt.Errorf("connect channel: %w", err)
	}
	client, err := channelHTTPClient(f.client, req.CAFile)
	if err != nil {
		return nil, fmt.Errorf("connect channel: %w", err)
	}
	if f.store == nil {
		return nil, fmt.Errorf("connect channel: store is required")
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		name = "onprem"
	}
	if err := validateChannelProfileName(name); err != nil {
		return nil, fmt.Errorf("connect channel: %w", err)
	}
	if err := f.confirmEmbeddedOrigin(ctx, name, origin, originFromToken); err != nil {
		return nil, err
	}
	profileID, err := f.channelProfileID(ctx, name, origin)
	if err != nil {
		return nil, err
	}
	var exchanged exchangeEnrollmentResponse
	if err := doOnPremJSON(ctx, client, http.MethodPost, origin,
		"/v1/agent-enrollments/exchange", "", map[string]string{
			"token": req.EnrollmentToken,
		}, &exchanged, "exchange enrollment", ""); err != nil {
		return nil, fmt.Errorf("connect channel: %w", err)
	}
	if strings.TrimSpace(exchanged.APIKey) == "" ||
		strings.TrimSpace(exchanged.CredentialID) == "" {
		return nil, fmt.Errorf(
			"connect channel: enrollment exchange returned incomplete credential",
		)
	}
	profile := &model.ChannelProfile{
		ProfileID: profileID, Name: name, Kind: model.ChannelKindOnPrem, URL: origin,
		APIKey: exchanged.APIKey, CAFile: strings.TrimSpace(req.CAFile),
		CredentialID: exchanged.CredentialID, Enabled: true, AutoReceive: req.AutoReceive,
	}
	if _, err := f.store.SaveChannelProfile(
		ctx,
		&store.SaveChannelProfileRequest{Profile: profile},
	); err != nil {
		return nil, fmt.Errorf(
			"connect channel: enrollment was consumed but save credential failed: %w",
			err,
		)
	}
	identity, err := fetchChannelIdentity(ctx, client, profile)
	if err != nil {
		return nil, fmt.Errorf(
			"connect channel: enrollment was consumed and credential was saved; verify identity: %w",
			err,
		)
	}
	applyChannelIdentity(profile, identity)
	if _, err := f.store.SaveChannelProfile(
		ctx,
		&store.SaveChannelProfileRequest{Profile: profile},
	); err != nil {
		return nil, fmt.Errorf("connect channel: save verified identity: %w", err)
	}
	return &ConnectChannelResponse{Profile: profile}, nil
}

func (f *ChannelFacade) confirmEmbeddedOrigin(
	ctx context.Context,
	name string,
	origin string,
	originFromToken bool,
) error {
	if !originFromToken {
		return nil
	}
	got, err := f.store.GetChannelProfile(ctx, &store.GetChannelProfileRequest{Name: name})
	if err != nil {
		return fmt.Errorf("connect channel: load existing profile: %w", err)
	}
	if got.Profile == nil || got.Profile.URL == origin {
		return nil
	}
	return fmt.Errorf(
		"connect channel: embedded origin %q differs from profile %q origin %q; "+
			"rerun with explicit --url %q to confirm the change",
		origin,
		name,
		got.Profile.URL,
		origin,
	)
}

func resolveChannelOrigin(explicitURL string, enrollmentToken string) (string, bool, error) {
	if strings.TrimSpace(explicitURL) != "" {
		origin, err := normalizeChannelOrigin(explicitURL)
		return origin, false, err
	}
	embedded, found, err := enrollmentTokenOrigin(enrollmentToken)
	if err != nil {
		return "", false, err
	}
	if !found {
		return "", false, fmt.Errorf(
			"on-prem URL is required for a legacy two-part enrollment token",
		)
	}
	origin, err := normalizeChannelOrigin(embedded)
	if err != nil {
		return "", false, fmt.Errorf("embedded enrollment origin: %w", err)
	}
	return origin, true, nil
}

func enrollmentTokenOrigin(token string) (string, bool, error) {
	parts := strings.Split(strings.TrimSpace(token), ".")
	if len(parts) == 2 {
		return "", false, nil
	}
	if len(parts) != 3 || !strings.HasPrefix(parts[0], "tm_enroll_") ||
		parts[0] == "tm_enroll_" || parts[1] == "" || parts[2] == "" {
		return "", false, fmt.Errorf("enrollment token format is invalid")
	}
	decoded, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return "", false, fmt.Errorf("decode embedded enrollment origin: %w", err)
	}
	if len(decoded) == 0 {
		return "", false, fmt.Errorf("embedded enrollment origin is empty")
	}
	return string(decoded), true, nil
}

func (f *ChannelFacade) channelProfileID(
	ctx context.Context,
	name string,
	origin string,
) (string, error) {
	got, err := f.store.GetChannelProfile(ctx, &store.GetChannelProfileRequest{Name: name})
	if err != nil {
		return "", fmt.Errorf("connect channel: load existing profile: %w", err)
	}
	if got.Profile != nil && got.Profile.URL == origin && got.Profile.ProfileID != "" {
		return got.Profile.ProfileID, nil
	}
	profileID, err := newLocalID("chp")
	if err != nil {
		return "", fmt.Errorf("connect channel: create profile id: %w", err)
	}
	return profileID, nil
}

func normalizeChannelOrigin(raw string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return "", fmt.Errorf("parse on-prem origin: %w", err)
	}
	if parsed.Scheme == "" || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" ||
		parsed.Fragment != "" || (parsed.Path != "" && parsed.Path != "/") {
		return "", fmt.Errorf("on-prem URL must be an origin with scheme and host")
	}
	if parsed.Scheme != "https" && parsed.Scheme != "http" {
		return "", fmt.Errorf("on-prem URL must use http or https")
	}
	if parsed.Scheme == "http" &&
		!isLoopbackHost(parsed.Hostname()) &&
		!isTailscaleAddress(parsed.Hostname()) {
		return "", fmt.Errorf(
			"on-prem URL must use https unless the host is loopback or a Tailscale address",
		)
	}
	return parsed.Scheme + "://" + parsed.Host, nil
}

func isLoopbackHost(host string) bool {
	if strings.EqualFold(strings.TrimSpace(host), "localhost") {
		return true
	}
	ip := net.ParseIP(strings.TrimSpace(host))
	return ip != nil && ip.IsLoopback()
}

func isTailscaleAddress(host string) bool {
	address, err := netip.ParseAddr(strings.TrimSpace(host))
	if err != nil {
		return false
	}
	for _, prefix := range tailscaleAddressPrefixes {
		if prefix.Contains(address) {
			return true
		}
	}
	return false
}

func validateChannelProfileName(name string) error {
	if name == "manager" {
		return fmt.Errorf("profile name %q is reserved", name)
	}
	if len(name) == 0 || len(name) > 64 {
		return fmt.Errorf("profile name must contain 1 to 64 characters")
	}
	for index, char := range name {
		if (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') ||
			(char >= '0' && char <= '9') || (index > 0 && (char == '-' || char == '_' || char == '.')) {
			continue
		}
		return fmt.Errorf("profile name may contain letters, digits, dot, dash, and underscore")
	}
	return nil
}

func channelHTTPClient(base AuthHTTPClient, caFile string) (AuthHTTPClient, error) {
	if strings.TrimSpace(caFile) == "" {
		if base == nil {
			return http.DefaultClient, nil
		}
		return base, nil
	}
	pem, err := os.ReadFile(strings.TrimSpace(caFile))
	if err != nil {
		return nil, fmt.Errorf("load CA file: %w", err)
	}
	roots, err := x509.SystemCertPool()
	if err != nil {
		return nil, fmt.Errorf("load system CA pool: %w", err)
	}
	if !roots.AppendCertsFromPEM(pem) {
		return nil, fmt.Errorf("load CA file: no certificates found")
	}
	httpClient, ok := base.(*http.Client)
	if !ok {
		return nil, fmt.Errorf("load CA file: custom HTTP client is not configurable")
	}
	clone := *httpClient
	defaultTransport, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		return nil, fmt.Errorf("load CA file: default HTTP transport is not configurable")
	}
	transport := defaultTransport.Clone()
	if existing, ok := httpClient.Transport.(*http.Transport); ok && existing != nil {
		transport = existing.Clone()
	}
	transport.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots}
	clone.Transport = transport
	return &clone, nil
}

func fetchChannelIdentity(
	ctx context.Context,
	client AuthHTTPClient,
	profile *model.ChannelProfile,
) (*agentIdentityResponse, error) {
	var identity agentIdentityResponse
	if err := doOnPremJSON(ctx, client, http.MethodGet, profile.URL, "/v1/agent-identity",
		profile.APIKey, nil, &identity, "get agent identity", ""); err != nil {
		return nil, err
	}
	if identity.AgentID == "" || identity.UserID == "" || identity.CredentialID == "" {
		return nil, fmt.Errorf("agent identity response is incomplete")
	}
	return &identity, nil
}

func applyChannelIdentity(profile *model.ChannelProfile, identity *agentIdentityResponse) {
	profile.AgentID = identity.AgentID
	profile.UserID = identity.UserID
	profile.CredentialID = identity.CredentialID
	profile.Permissions = append([]string(nil), identity.Permissions...)
}

func doOnPremJSON(
	ctx context.Context,
	client AuthHTTPClient,
	method string,
	origin string,
	path string,
	bearerToken string,
	body any,
	out any,
	operation string,
	requiredPermission string,
) error {
	requestContext, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("encode %s request: %w", operation, err)
		}
		reader = bytes.NewReader(encoded)
	}
	request, err := http.NewRequestWithContext(
		requestContext,
		method,
		strings.TrimRight(origin, "/")+path,
		reader,
	) // #nosec G107
	if err != nil {
		return fmt.Errorf("create %s request: %w", operation, err)
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", "paxl-onprem-channel")
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if bearerToken != "" {
		request.Header.Set("Authorization", "Bearer "+bearerToken)
	}
	response, err := client.Do(request)
	if err != nil {
		return fmt.Errorf("request on-prem %s: %w", operation, err)
	}
	defer closeBody(response.Body)
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return onPremStatusError(operation, requiredPermission, response.StatusCode)
	}
	if out == nil {
		return nil
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(out); err != nil {
		return fmt.Errorf("decode on-prem %s response: %w", operation, err)
	}
	return nil
}

func doOnPremJSONRetry(
	ctx context.Context,
	client AuthHTTPClient,
	method string,
	origin string,
	path string,
	bearerToken string,
	body any,
	out any,
	operation string,
	requiredPermission string,
) error {
	var lastErr error
	for attempt := 0; attempt < onPremRequestAttempts; attempt++ {
		lastErr = doOnPremJSON(
			ctx,
			client,
			method,
			origin,
			path,
			bearerToken,
			body,
			out,
			operation,
			requiredPermission,
		)
		if lastErr == nil || !retryableOnPremError(lastErr) {
			return lastErr
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
	}
	return lastErr
}

type onPremStatusCodeError struct {
	status int
	err    error
}

func (e *onPremStatusCodeError) Error() string { return e.err.Error() }
func (e *onPremStatusCodeError) Unwrap() error { return e.err }

func retryableOnPremError(err error) bool {
	var statusErr *onPremStatusCodeError
	if errors.As(err, &statusErr) {
		return statusErr.status >= http.StatusInternalServerError
	}
	return strings.Contains(err.Error(), "request on-prem")
}

func onPremStatusError(operation string, permission string, status int) error {
	var err error
	switch status {
	case http.StatusUnauthorized:
		err = fmt.Errorf(
			"on-prem %s returned HTTP 401: credential may be revoked or expired; reconnect channel",
			operation,
		)
	case http.StatusForbidden:
		if permission != "" {
			err = fmt.Errorf(
				"on-prem %s returned HTTP 403: missing %s permission or Agent/Membership is suspended",
				operation,
				permission,
			)
			break
		}
		err = fmt.Errorf(
			"on-prem %s returned HTTP 403: Agent or Membership may be suspended",
			operation,
		)
	case http.StatusConflict:
		err = fmt.Errorf(
			"on-prem %s returned HTTP 409: idempotency key conflicts with a different payload",
			operation,
		)
	default:
		err = fmt.Errorf("on-prem %s returned HTTP %d", operation, status)
	}
	return &onPremStatusCodeError{status: status, err: err}
}
