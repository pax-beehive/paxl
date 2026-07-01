package facade

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

const (
	daemonCloudAgentRegisterPath = "/api/v1/node/agents/register"
	daemonHeaderPaxKey           = "X-Pax-Key"
	daemonHeaderCFClientID       = "CF-Access-Client-Id"
	daemonHeaderCFClientSecret   = "CF-Access-Client-Secret"
)

var daemonSafeRemoteID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]*$`)

type daemonHomeNodeKeyLoader struct{}

func (daemonHomeNodeKeyLoader) LoadRemoteNodeKey(
	ctx context.Context,
	remoteID string,
) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	remoteID = strings.TrimSpace(remoteID)
	if !daemonSafeRemoteID.MatchString(remoteID) {
		return "", fmt.Errorf("invalid remote id: %q", remoteID)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("get home dir: %w", err)
	}
	path := filepath.Join(home, ".paxd", "secrets", "remotes", remoteID, "node_key")
	raw, err := os.ReadFile(path) // #nosec G304
	if err != nil {
		return "", fmt.Errorf("read node key secret: %w", err)
	}
	nodeKey := strings.TrimSpace(string(raw))
	if nodeKey == "" {
		return "", errors.New("node key secret is empty")
	}
	return nodeKey, nil
}

type DaemonHTTPCloudAgentRegistrar struct {
	client DaemonHTTPClient
}

func NewDaemonHTTPCloudAgentRegistrar(client DaemonHTTPClient) *DaemonHTTPCloudAgentRegistrar {
	if client == nil {
		client = &http.Client{
			Timeout: 30 * time.Second,
			CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
				return http.ErrUseLastResponse
			},
		}
	}
	return &DaemonHTTPCloudAgentRegistrar{client: client}
}

func (r *DaemonHTTPCloudAgentRegistrar) RegisterCloudAgent(
	ctx context.Context,
	req *DaemonCloudAgentRegistrationRequest,
) (string, error) {
	if req == nil {
		return "", errors.New("registration request is required")
	}
	remoteID := strings.TrimSpace(req.Remote.Remote.ID)
	cloudURL := strings.TrimRight(strings.TrimSpace(req.Remote.Remote.CloudAPIURL), "/")
	nodeKey := strings.TrimSpace(req.NodeKey)
	if cloudURL == "" {
		return "", fmt.Errorf("remote %q cloud api url is required", remoteID)
	}
	if nodeKey == "" {
		return "", fmt.Errorf("remote %q node key is required", remoteID)
	}
	body := daemonCloudAgentRegisterRequest{
		Agent: daemonCloudAgentRegisterPayload{
			Name:      strings.TrimSpace(req.Name),
			AgentType: strings.TrimSpace(req.AgentType),
		},
	}
	data, err := json.Marshal(body)
	if err != nil {
		return "", fmt.Errorf("encode register cloud agent request: %w", err)
	}
	httpReq, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		cloudURL+daemonCloudAgentRegisterPath,
		bytes.NewReader(data),
	)
	if err != nil {
		return "", fmt.Errorf("create register cloud agent request: %w", err)
	}
	httpReq.Header.Set("Accept", "application/json")
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("User-Agent", "paxl-daemon")
	httpReq.Header.Set(daemonHeaderPaxKey, nodeKey)
	if err := setDaemonCloudAgentAuthHeaders(ctx, httpReq, req); err != nil {
		return "", err
	}
	resp, err := r.client.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("request register cloud agent: %w", err)
	}
	defer closeBody(resp.Body)
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return "", fmt.Errorf(
			"register cloud agent returned HTTP %d: %s",
			resp.StatusCode,
			strings.TrimSpace(string(body)),
		)
	}
	var envelope daemonCloudAgentRegisterEnvelope
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&envelope); err != nil {
		return "", fmt.Errorf("decode register cloud agent response: %w", err)
	}
	agentID := strings.TrimSpace(envelope.Data.AgentID)
	if agentID == "" {
		return "", errors.New("register cloud agent returned empty agent id")
	}
	return agentID, nil
}

func setDaemonCloudAgentAuthHeaders(
	ctx context.Context,
	httpReq *http.Request,
	req *DaemonCloudAgentRegistrationRequest,
) error {
	if req.Remote.Auth == nil ||
		req.Remote.Auth.Kind == "" ||
		req.Remote.Auth.Kind == "none" {
		return nil
	}
	if req.Remote.Auth.Kind != "cloudflare_access" {
		return fmt.Errorf(
			"remote %q has unsupported auth kind %q",
			req.Remote.Remote.ID,
			req.Remote.Auth.Kind,
		)
	}
	clientID := strings.TrimSpace(req.Remote.Auth.ClientID)
	if clientID == "" {
		return fmt.Errorf(
			"remote %q cloudflare access auth is missing client id",
			req.Remote.Remote.ID,
		)
	}
	secretRef := strings.TrimSpace(req.Remote.Auth.ClientSecretRef)
	if secretRef == "" {
		return fmt.Errorf(
			"remote %q cloudflare access auth is missing client secret ref",
			req.Remote.Remote.ID,
		)
	}
	clientSecret, err := daemonResolveSecretRef(ctx, secretRef)
	if err != nil {
		return fmt.Errorf(
			"resolve cloudflare access client secret for remote %q: %w",
			req.Remote.Remote.ID,
			err,
		)
	}
	httpReq.Header.Set(daemonHeaderCFClientID, clientID)
	httpReq.Header.Set(daemonHeaderCFClientSecret, clientSecret)
	return nil
}

func daemonResolveSecretRef(ctx context.Context, ref string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	ref = strings.TrimSpace(ref)
	path, ok := strings.CutPrefix(ref, "file:")
	if !ok || strings.TrimSpace(path) == "" {
		return "", fmt.Errorf("unsupported secret ref %q", ref)
	}
	raw, err := os.ReadFile(path) // #nosec G304
	if err != nil {
		return "", fmt.Errorf("read secret file: %w", err)
	}
	secret := strings.TrimSpace(string(raw))
	if secret == "" {
		return "", errors.New("secret file is empty")
	}
	return secret, nil
}

type daemonCloudAgentRegisterRequest struct {
	Agent daemonCloudAgentRegisterPayload `json:"agent"`
}

type daemonCloudAgentRegisterPayload struct {
	Name      string `json:"name,omitempty"`
	AgentType string `json:"agent_type,omitempty"`
}

type daemonCloudAgentRegisterEnvelope struct {
	Data daemonCloudAgentRegisterResponse `json:"data"`
}

type daemonCloudAgentRegisterResponse struct {
	AgentID string `json:"agent_id"`
}
