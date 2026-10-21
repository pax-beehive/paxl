package adaptor

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/pax-oss/paxl/internal/model"
)

var kimiAccessURL = regexp.MustCompile(
	`http://127\.0\.0\.1:([0-9]{1,5})/#token=([A-Za-z0-9_-]{16,512})(?:[\r\n\t ]|$)`,
)
var terminalEscape = regexp.MustCompile(`\x1b\[[0-?]*[ -/]*[@-~]`)

type kimiEndpoint struct{ origin, token string }

type kimiAuthSnapshot struct {
	ProvidersCount  *int `json:"providers_count"`
	ManagedProvider *struct {
		Status string `json:"status"`
	} `json:"managed_provider"`
}

func (s *kimiAuthSnapshot) configured() (bool, error) {
	if s.ProvidersCount == nil || *s.ProvidersCount < 0 {
		return false, fmt.Errorf("unrecognized native Kimi authentication status")
	}
	if s.ManagedProvider != nil {
		switch s.ManagedProvider.Status {
		case "authenticated":
			return true, nil
		case "expired", "revoked", "unauthenticated":
		default:
			return false, fmt.Errorf("unrecognized native Kimi authentication status")
		}
	}
	return *s.ProvidersCount > 0, nil
}

// Startup output contains a bearer token: never forward it, including on failure.
// stdout/stderr may write concurrently.
type kimiStartupOutput struct {
	mu    sync.Mutex
	data  []byte
	ready chan kimiEndpoint
	found bool
}

func (w *kimiStartupOutput) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	n := len(p)
	if w.found {
		return n, nil
	}
	const limit = 64 * 1024
	if len(w.data)+n > limit {
		return n, nil
	}
	w.data = append(w.data, p...)
	clean := terminalEscape.ReplaceAllString(string(w.data), "")
	last := strings.LastIndexByte(clean, '\n')
	if last < 0 {
		return n, nil
	}
	match := kimiAccessURL.FindStringSubmatch(clean[:last+1])
	if len(match) == 3 {
		port, err := strconv.Atoi(match[1])
		if err == nil && port > 0 && port <= 65535 {
			w.found = true
			w.ready <- kimiEndpoint{origin: "http://127.0.0.1:" + match[1], token: match[2]}
			w.data = nil
		}
	}
	return n, nil
}

func validateKimiAuth(req *NativeAuthRequest) error {
	if req.Provider != "" || req.Profile != "" {
		return fmt.Errorf("kimi account auth does not accept --provider or --profile")
	}
	if req.Method != "" && req.Operation != model.HarnessAuthLogin {
		return fmt.Errorf("method is only valid for login")
	}
	if req.Operation == model.HarnessAuthLogin {
		_, err := model.ParseHarnessAuthMethod(string(req.Method), req.Harness)
		return err
	}
	// Login was validated above; other operations have no method.
	//exhaustive:ignore
	switch req.Operation {
	case model.HarnessAuthStatus, model.HarnessAuthLogout, model.HarnessAuthMethods:
		return nil
	}
	return fmt.Errorf("unsupported Kimi authentication operation")
}

// RunKimi uses the installed CLI's authenticated loopback API for operations
// not exposed as standalone commands. The server and token exist only for this call.
func (a *NativeHarnessAuth) RunKimi(
	ctx context.Context,
	req *NativeAuthRequest,
) (*model.HarnessAuthResult, error) {
	if req == nil {
		return nil, fmt.Errorf("authentication request is required")
	}
	if err := validateKimiAuth(req); err != nil {
		return nil, err
	}
	result := &model.HarnessAuthResult{
		Harness: model.AgentNameKimi,
		State:   model.HarnessAuthAvailable,
	}
	if req.Operation == model.HarnessAuthMethods {
		result.Providers = []model.HarnessAuthProvider{
			{ID: "kimi-code", Methods: []model.HarnessAuthMethod{model.HarnessAuthDevice}},
		}
		result.Message = "Kimi Code device login. Native logout also disconnects this machine's Kimi Remote Control session."
		return result, nil
	}
	if req.Operation == model.HarnessAuthLogin {
		if err := a.Run(ctx, req); err != nil {
			return nil, err
		}
		result.State = model.HarnessAuthSucceeded
		result.Method = req.Method
		return result, nil
	}
	childCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	cmd := exec.CommandContext(
		childCtx,
		authExecutable(model.AgentNameKimi),
		"web",
		"--host",
		"127.0.0.1",
		"--port",
		"0",
		"--no-open",
	) // #nosec G204 -- Selected installed native CLI; loopback with native authentication enabled.
	output := &kimiStartupOutput{ready: make(chan kimiEndpoint, 1)}
	cmd.Stdout, cmd.Stderr = output, output
	prepareNativeAuth(cmd)
	cmd.WaitDelay = time.Second
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start native Kimi auth service: %w", err)
	}
	done := make(chan struct{})
	go func() { _ = cmd.Wait(); close(done) }()
	defer func() {
		cancel()
		stopNativeAuth(cmd)
		select {
		case <-done:
		case <-time.After(2 * time.Second):
		}
	}()
	var endpoint kimiEndpoint
	timer := time.NewTimer(10 * time.Second)
	defer timer.Stop()
	select {
	case endpoint = <-output.ready:
	case <-done:
		return nil, fmt.Errorf(
			"native Kimi auth service exited before startup; this requires Kimi Code's web API",
		)
	case <-ctx.Done():
		return nil, fmt.Errorf("start Kimi auth: %w", ctx.Err())
	case <-timer.C:
		return nil, fmt.Errorf("native Kimi auth service did not become ready")
	}
	if req.Operation == model.HarnessAuthLogout {
		var data struct {
			LoggedOut bool `json:"logged_out"`
		}
		err := kimiAuthAPI(ctx, endpoint, http.MethodPost, "/api/v1/oauth/logout", &data)
		if err != nil {
			return nil, err
		}
		if !data.LoggedOut {
			return nil, fmt.Errorf("kimi did not confirm logout")
		}
		result.State = model.HarnessAuthSucceeded
		result.Message = "Native Kimi Code logout completed; the native operation also disconnects this machine's Remote Control session. API-key providers and environment credentials may remain."
		return result, nil
	}
	var data kimiAuthSnapshot
	if err := kimiAuthAPI(ctx, endpoint, http.MethodGet, "/api/v1/auth", &data); err != nil {
		return nil, err
	}
	configured, err := data.configured()
	if err != nil {
		return nil, err
	}
	result.State = model.HarnessAuthStateUnknown
	if configured {
		result.State = model.HarnessAuthConfigured
	}
	result.Message = "Native Kimi account/provider readiness only; API credentials, model access and network availability are not independently verified."
	return result, nil
}

func kimiAuthAPI(
	ctx context.Context,
	endpoint kimiEndpoint,
	method, path string,
	target any,
) error {
	transport := &http.Transport{Proxy: nil}
	defer transport.CloseIdleConnections()
	client := &http.Client{
		Transport:     transport,
		Timeout:       5 * time.Second,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
	}
	// The origin is constructed exclusively from the owned child's loopback port.
	parsed, err := url.Parse(endpoint.origin)
	if err != nil || parsed.Hostname() != "127.0.0.1" {
		return fmt.Errorf("invalid native Kimi service endpoint")
	}
	req, err := http.NewRequestWithContext(
		ctx,
		method,
		endpoint.origin+path,
		bytes.NewBufferString("{}"),
	)
	if err != nil {
		return fmt.Errorf("build native Kimi auth request")
	}
	req.Header.Set("Authorization", "Bearer "+endpoint.token)
	req.Header.Set("Content-Type", "application/json")
	response, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("native Kimi auth request failed")
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("native Kimi auth request returned HTTP %d", response.StatusCode)
	}
	var envelope struct {
		Code *int            `json:"code"`
		Data json.RawMessage `json:"data"`
	}
	decoder := json.NewDecoder(io.LimitReader(response.Body, 64*1024+1))
	if err = decoder.Decode(&envelope); err != nil || envelope.Code == nil || *envelope.Code != 0 {
		return fmt.Errorf("native Kimi auth API returned an invalid or unsuccessful response")
	}
	if len(envelope.Data) == 0 || strings.TrimSpace(string(envelope.Data)) == "null" {
		return fmt.Errorf("native Kimi auth API returned no data")
	}
	if err = json.Unmarshal(envelope.Data, target); err != nil {
		return fmt.Errorf("invalid native Kimi auth data")
	}
	return nil
}
