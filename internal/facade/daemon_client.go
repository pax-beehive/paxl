package facade

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/pax-oss/paxl/internal/model"
)

const daemonCommandIDHeader = "X-Pax-Command-ID"

type DaemonHTTPClient interface {
	Do(req *http.Request) (*http.Response, error)
}

type DaemonLocalAPIClient struct {
	baseURL string
	http    DaemonHTTPClient
}

func DefaultDaemonControlSocketPath() string {
	home, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(home) == "" {
		return filepath.Join(".paxd", "paxd.sock")
	}
	return filepath.Join(home, ".paxd", "paxd.sock")
}

func NewDaemonUnixClient(socketPath string) *DaemonLocalAPIClient {
	if strings.TrimSpace(socketPath) == "" {
		socketPath = DefaultDaemonControlSocketPath()
	}
	transport := &http.Transport{
		DialContext: func(ctx context.Context, network string, addr string) (net.Conn, error) {
			_ = network
			_ = addr
			var dialer net.Dialer
			return dialer.DialContext(ctx, "unix", socketPath)
		},
	}
	return &DaemonLocalAPIClient{
		baseURL: "http://paxd",
		http:    &http.Client{Transport: transport},
	}
}

func NewDaemonHTTPClient(baseURL string, client DaemonHTTPClient) *DaemonLocalAPIClient {
	if client == nil {
		client = http.DefaultClient
	}
	return &DaemonLocalAPIClient{
		baseURL: strings.TrimRight(baseURL, "/"),
		http:    client,
	}
}

func (c *DaemonLocalAPIClient) GetStatus(ctx context.Context) (*model.DaemonQueryResult, error) {
	return c.get(ctx, "/v1/status")
}

func (c *DaemonLocalAPIClient) ListRemotes(
	ctx context.Context,
	includeDisabled bool,
) (*model.DaemonQueryResult, error) {
	path := "/v1/remotes"
	if includeDisabled {
		path += "?include_disabled=true"
	}
	return c.get(ctx, path)
}

func (c *DaemonLocalAPIClient) CreateRemote(
	ctx context.Context,
	commandID string,
	cmd *model.DaemonCreateRemoteCommand,
) (*model.DaemonCommandAck, error) {
	return c.postCommand(ctx, "/v1/remotes", cmd, commandID)
}

func (c *DaemonLocalAPIClient) UpdateRemote(
	ctx context.Context,
	commandID string,
	remoteID string,
	cmd *model.DaemonUpdateRemoteCommand,
) (*model.DaemonCommandAck, error) {
	if cmd == nil {
		cmd = &model.DaemonUpdateRemoteCommand{}
	}
	cmd.RemoteID = remoteID
	return c.patchCommand(ctx, "/v1/remotes/"+url.PathEscape(remoteID), cmd, commandID)
}

func (c *DaemonLocalAPIClient) RestartRemote(
	ctx context.Context,
	commandID string,
	remoteID string,
) (*model.DaemonCommandAck, error) {
	return c.postCommand(ctx, "/v1/remotes/"+url.PathEscape(remoteID)+"/restart", nil, commandID)
}

func (c *DaemonLocalAPIClient) DeleteRemote(
	ctx context.Context,
	commandID string,
	remoteID string,
	cascadeAgentConnections bool,
) (*model.DaemonCommandAck, error) {
	path := "/v1/remotes/" + url.PathEscape(remoteID)
	if cascadeAgentConnections {
		path += "?cascade_agent_connections=true"
	}
	return c.deleteCommand(ctx, path, commandID)
}

func (c *DaemonLocalAPIClient) ListAgentConnections(
	ctx context.Context,
	includeDisabled bool,
) (*model.DaemonQueryResult, error) {
	path := "/v1/agent-connections"
	if includeDisabled {
		path += "?include_disabled=true"
	}
	return c.get(ctx, path)
}

func (c *DaemonLocalAPIClient) CreateAgentConnection(
	ctx context.Context,
	commandID string,
	cmd *model.DaemonCreateAgentConnectionCommand,
) (*model.DaemonCommandAck, error) {
	return c.postCommand(ctx, "/v1/agent-connections", cmd, commandID)
}

func (c *DaemonLocalAPIClient) UpdateAgentConnection(
	ctx context.Context,
	commandID string,
	connectionID string,
	cmd *model.DaemonUpdateAgentConnectionCommand,
) (*model.DaemonCommandAck, error) {
	if cmd == nil {
		cmd = &model.DaemonUpdateAgentConnectionCommand{}
	}
	cmd.ConnectionID = connectionID
	return c.patchCommand(ctx, "/v1/agent-connections/"+url.PathEscape(connectionID), cmd, commandID)
}

func (c *DaemonLocalAPIClient) RestartAgentConnection(
	ctx context.Context,
	commandID string,
	connectionID string,
) (*model.DaemonCommandAck, error) {
	return c.postCommand(ctx, "/v1/agent-connections/"+url.PathEscape(connectionID)+"/restart", nil, commandID)
}

func (c *DaemonLocalAPIClient) DeleteAgentConnection(
	ctx context.Context,
	commandID string,
	connectionID string,
) (*model.DaemonCommandAck, error) {
	return c.deleteCommand(ctx, "/v1/agent-connections/"+url.PathEscape(connectionID), commandID)
}

func (c *DaemonLocalAPIClient) ListHarnesses(
	ctx context.Context,
	includeMissing bool,
) (*model.DaemonQueryResult, error) {
	path := "/v1/harnesses"
	if includeMissing {
		path += "?include_missing=true"
	}
	return c.get(ctx, path)
}

func (c *DaemonLocalAPIClient) DiscoverHarnesses(
	ctx context.Context,
	probe bool,
	names []string,
) (*model.DaemonQueryResult, error) {
	body := map[string]any{
		"probe": probe,
		"names": names,
	}
	return c.post(ctx, "/v1/harnesses/discover", body)
}

func (c *DaemonLocalAPIClient) GetLocalOverview(
	ctx context.Context,
) (*model.DaemonQueryResult, error) {
	return c.get(ctx, "/v1/local/overview")
}

func (c *DaemonLocalAPIClient) ListLocalSessions(
	ctx context.Context,
	agent string,
	limit int,
) (*model.DaemonQueryResult, error) {
	values := url.Values{}
	if strings.TrimSpace(agent) != "" {
		values.Set("agent", strings.TrimSpace(agent))
	}
	if limit > 0 {
		values.Set("limit", fmt.Sprintf("%d", limit))
	}
	path := "/v1/local/sessions"
	if encoded := values.Encode(); encoded != "" {
		path += "?" + encoded
	}
	return c.get(ctx, path)
}

func (c *DaemonLocalAPIClient) SyncLocalSessions(
	ctx context.Context,
	agent string,
	limit int,
	timeoutMillis int64,
) (*model.DaemonQueryResult, error) {
	body := map[string]any{
		"agent":          strings.TrimSpace(agent),
		"limit":          limit,
		"timeout_millis": timeoutMillis,
	}
	return c.post(ctx, "/v1/local/sessions/sync", body)
}

func (c *DaemonLocalAPIClient) get(
	ctx context.Context,
	path string,
) (*model.DaemonQueryResult, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return nil, err
	}
	return c.doQuery(req)
}

func (c *DaemonLocalAPIClient) post(
	ctx context.Context,
	path string,
	body any,
) (*model.DaemonQueryResult, error) {
	reader, err := jsonReader(body)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, reader)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	return c.doQuery(req)
}

func (c *DaemonLocalAPIClient) postCommand(
	ctx context.Context,
	path string,
	body any,
	commandID string,
) (*model.DaemonCommandAck, error) {
	return c.commandWithBody(ctx, http.MethodPost, path, body, commandID)
}

func (c *DaemonLocalAPIClient) patchCommand(
	ctx context.Context,
	path string,
	body any,
	commandID string,
) (*model.DaemonCommandAck, error) {
	return c.commandWithBody(ctx, http.MethodPatch, path, body, commandID)
}

func (c *DaemonLocalAPIClient) deleteCommand(
	ctx context.Context,
	path string,
	commandID string,
) (*model.DaemonCommandAck, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, c.baseURL+path, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set(daemonCommandIDHeader, commandID)
	return c.doCommand(req)
}

func (c *DaemonLocalAPIClient) commandWithBody(
	ctx context.Context,
	method string,
	path string,
	body any,
	commandID string,
) (*model.DaemonCommandAck, error) {
	reader, err := jsonReader(body)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reader)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(daemonCommandIDHeader, commandID)
	return c.doCommand(req)
}

func (c *DaemonLocalAPIClient) doQuery(
	req *http.Request,
) (*model.DaemonQueryResult, error) {
	client := c.http
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var result model.DaemonQueryResult
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		return &result, fmt.Errorf("local daemon API returned %s", resp.Status)
	}
	return &result, nil
}

func (c *DaemonLocalAPIClient) doCommand(
	req *http.Request,
) (*model.DaemonCommandAck, error) {
	client := c.http
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var ack model.DaemonCommandAck
	if err := json.NewDecoder(resp.Body).Decode(&ack); err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		return &ack, fmt.Errorf("local daemon API returned %s", resp.Status)
	}
	return &ack, nil
}

func jsonReader(body any) (*bytes.Reader, error) {
	if body == nil {
		return bytes.NewReader(nil), nil
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	return bytes.NewReader(raw), nil
}
