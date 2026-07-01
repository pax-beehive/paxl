package facade

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"github.com/pax-oss/paxl/internal/model"
)

type DaemonControlClient interface {
	GetStatus(ctx context.Context) (*model.DaemonQueryResult, error)
	ListRemotes(ctx context.Context, includeDisabled bool) (*model.DaemonQueryResult, error)
	CreateRemote(
		ctx context.Context,
		commandID string,
		cmd *model.DaemonCreateRemoteCommand,
	) (*model.DaemonCommandAck, error)
	UpdateRemote(
		ctx context.Context,
		commandID string,
		remoteID string,
		cmd *model.DaemonUpdateRemoteCommand,
	) (*model.DaemonCommandAck, error)
	RestartRemote(
		ctx context.Context,
		commandID string,
		remoteID string,
	) (*model.DaemonCommandAck, error)
	DeleteRemote(
		ctx context.Context,
		commandID string,
		remoteID string,
		cascadeAgentConnections bool,
	) (*model.DaemonCommandAck, error)
	ListAgentConnections(
		ctx context.Context,
		includeDisabled bool,
	) (*model.DaemonQueryResult, error)
	CreateAgentConnection(
		ctx context.Context,
		commandID string,
		cmd *model.DaemonCreateAgentConnectionCommand,
	) (*model.DaemonCommandAck, error)
	UpdateAgentConnection(
		ctx context.Context,
		commandID string,
		connectionID string,
		cmd *model.DaemonUpdateAgentConnectionCommand,
	) (*model.DaemonCommandAck, error)
	RestartAgentConnection(
		ctx context.Context,
		commandID string,
		connectionID string,
	) (*model.DaemonCommandAck, error)
	DeleteAgentConnection(
		ctx context.Context,
		commandID string,
		connectionID string,
	) (*model.DaemonCommandAck, error)
	ListHarnesses(ctx context.Context, includeMissing bool) (*model.DaemonQueryResult, error)
	DiscoverHarnesses(
		ctx context.Context,
		probe bool,
		names []string,
	) (*model.DaemonQueryResult, error)
	GetLocalOverview(ctx context.Context) (*model.DaemonQueryResult, error)
	ListLocalSessions(
		ctx context.Context,
		agent string,
		limit int,
	) (*model.DaemonQueryResult, error)
	SyncLocalSessions(
		ctx context.Context,
		agent string,
		limit int,
		timeoutMillis int64,
	) (*model.DaemonQueryResult, error)
}

type DaemonFacade struct {
	client DaemonControlClient
}

type DaemonStatusRequest struct{}

type DaemonStatusResponse struct {
	Status *model.DaemonStatus
}

type ListDaemonRemotesRequest struct {
	IncludeDisabled bool
}

type ListDaemonRemotesResponse struct {
	Remotes []*model.DaemonRemoteView
}

type CreateDaemonRemoteRequest struct {
	RemoteID       string
	Name           string
	CloudAPIURL    string
	NodeID         string
	CloudAPIKeyRef string
	Enabled        bool
}

type UpdateDaemonRemoteRequest struct {
	RemoteID       string
	Name           *string
	CloudAPIURL    *string
	NodeID         *string
	CloudAPIKeyRef *string
	Enabled        *bool
}

type DaemonCommandResponse struct {
	Ack *model.DaemonCommandAck
}

type ListDaemonAgentsRequest struct {
	IncludeDisabled bool
}

type ListDaemonAgentsResponse struct {
	Agents []*model.DaemonAgentConnectionView
}

type CreateDaemonAgentRequest struct {
	RemoteID     string
	Name         string
	CloudAgentID string
	InstanceID   string
	AgentType    string
	Harness      string
	Command      []string
	WorkingDir   string
	Env          map[string]string
}

type UpdateDaemonAgentRequest struct {
	ConnectionID string
	Name         *string
	CloudAgentID *string
	InstanceID   *string
	AgentType    *string
	Harness      *string
	Command      *[]string
	WorkingDir   *string
	Enabled      *bool
	DesiredState *model.DaemonDesiredState
}

type ListDaemonHarnessesRequest struct {
	IncludeMissing bool
}

type ListDaemonHarnessesResponse struct {
	Harnesses []*model.DaemonHarnessView
}

type DiscoverDaemonHarnessesRequest struct {
	Probe bool
	Names []string
}

type DaemonLocalOverviewRequest struct{}

type DaemonLocalOverviewResponse struct {
	Overview *model.DaemonLocalOverview
}

type ListDaemonLocalSessionsRequest struct {
	Agent string
	Limit int
}

type ListDaemonLocalSessionsResponse struct {
	Sessions []*model.DaemonLocalSessionView
}

type SyncDaemonLocalSessionsRequest struct {
	Agent         string
	Limit         int
	TimeoutMillis int64
}

type SyncDaemonLocalSessionsResponse struct {
	Sync *model.DaemonLocalSessionSyncResult
}

func NewDaemonFacade(client DaemonControlClient) *DaemonFacade {
	if client == nil {
		client = NewDaemonUnixClient("")
	}
	return &DaemonFacade{client: client}
}

func (f *DaemonFacade) Status(
	ctx context.Context,
	req *DaemonStatusRequest,
	opts ...func(*Option),
) (*DaemonStatusResponse, error) {
	_ = req
	_ = applyOptions(opts)
	result, err := f.client.GetStatus(ctx)
	if err != nil {
		return nil, daemonAPIGuidance(err)
	}
	if err := queryOK(result); err != nil {
		return nil, err
	}
	if result.Status == nil {
		return nil, fmt.Errorf("daemon status: local daemon API returned no status")
	}
	return &DaemonStatusResponse{Status: result.Status}, nil
}

func (f *DaemonFacade) ListRemotes(
	ctx context.Context,
	req *ListDaemonRemotesRequest,
	opts ...func(*Option),
) (*ListDaemonRemotesResponse, error) {
	_ = applyOptions(opts)
	if req == nil {
		req = &ListDaemonRemotesRequest{}
	}
	result, err := f.client.ListRemotes(ctx, req.IncludeDisabled)
	if err != nil {
		return nil, daemonAPIGuidance(err)
	}
	if err := queryOK(result); err != nil {
		return nil, err
	}
	if result.Remotes == nil {
		return &ListDaemonRemotesResponse{}, nil
	}
	return &ListDaemonRemotesResponse{Remotes: result.Remotes.Items}, nil
}

func (f *DaemonFacade) CreateRemote(
	ctx context.Context,
	req *CreateDaemonRemoteRequest,
	opts ...func(*Option),
) (*DaemonCommandResponse, error) {
	_ = applyOptions(opts)
	if req == nil {
		return nil, fmt.Errorf("create daemon remote: request is required")
	}
	remoteID := firstNonEmpty(req.RemoteID, "default")
	cloudURL := strings.TrimRight(firstNonEmpty(req.CloudAPIURL, DefaultManagerURL), "/")
	enabled := req.Enabled
	ack, err := f.client.CreateRemote(ctx, newDaemonCommandID(), &model.DaemonCreateRemoteCommand{
		Remote: model.DaemonRemote{
			ID:          remoteID,
			Name:        firstNonEmpty(req.Name, remoteID),
			CloudAPIURL: cloudURL,
			NodeID:      strings.TrimSpace(req.NodeID),
			Enabled:     &enabled,
		},
		CloudAPIKeyRef: strings.TrimSpace(req.CloudAPIKeyRef),
	})
	return daemonCommandResponse("create daemon remote", ack, err)
}

func (f *DaemonFacade) UpdateRemote(
	ctx context.Context,
	req *UpdateDaemonRemoteRequest,
	opts ...func(*Option),
) (*DaemonCommandResponse, error) {
	_ = applyOptions(opts)
	if req == nil || strings.TrimSpace(req.RemoteID) == "" {
		return nil, fmt.Errorf("update daemon remote: remote id is required")
	}
	ack, err := f.client.UpdateRemote(
		ctx,
		newDaemonCommandID(),
		strings.TrimSpace(req.RemoteID),
		&model.DaemonUpdateRemoteCommand{
			Remote: model.DaemonRemotePatch{
				Name:        req.Name,
				CloudAPIURL: req.CloudAPIURL,
				NodeID:      req.NodeID,
				Enabled:     req.Enabled,
			},
			CloudAPIKeyRef: req.CloudAPIKeyRef,
		},
	)
	return daemonCommandResponse("update daemon remote", ack, err)
}

func (f *DaemonFacade) RestartRemote(
	ctx context.Context,
	remoteID string,
	opts ...func(*Option),
) (*DaemonCommandResponse, error) {
	_ = applyOptions(opts)
	remoteID = strings.TrimSpace(remoteID)
	if remoteID == "" {
		return nil, fmt.Errorf("restart daemon remote: remote id is required")
	}
	ack, err := f.client.RestartRemote(ctx, newDaemonCommandID(), remoteID)
	return daemonCommandResponse("restart daemon remote", ack, err)
}

func (f *DaemonFacade) DisconnectRemote(
	ctx context.Context,
	remoteID string,
	opts ...func(*Option),
) (*DaemonCommandResponse, error) {
	_ = applyOptions(opts)
	return f.deleteRemote(ctx, remoteID, false)
}

func (f *DaemonFacade) RemoveRemote(
	ctx context.Context,
	remoteID string,
	opts ...func(*Option),
) (*DaemonCommandResponse, error) {
	_ = applyOptions(opts)
	return f.deleteRemote(ctx, remoteID, true)
}

func (f *DaemonFacade) deleteRemote(
	ctx context.Context,
	remoteID string,
	cascade bool,
) (*DaemonCommandResponse, error) {
	remoteID = strings.TrimSpace(remoteID)
	if remoteID == "" {
		return nil, fmt.Errorf("delete daemon remote: remote id is required")
	}
	ack, err := f.client.DeleteRemote(ctx, newDaemonCommandID(), remoteID, cascade)
	return daemonCommandResponse("delete daemon remote", ack, err)
}

func (f *DaemonFacade) ListAgents(
	ctx context.Context,
	req *ListDaemonAgentsRequest,
	opts ...func(*Option),
) (*ListDaemonAgentsResponse, error) {
	_ = applyOptions(opts)
	if req == nil {
		req = &ListDaemonAgentsRequest{}
	}
	result, err := f.client.ListAgentConnections(ctx, req.IncludeDisabled)
	if err != nil {
		return nil, daemonAPIGuidance(err)
	}
	if err := queryOK(result); err != nil {
		return nil, err
	}
	if result.AgentConnections == nil {
		return &ListDaemonAgentsResponse{}, nil
	}
	return &ListDaemonAgentsResponse{Agents: result.AgentConnections.Items}, nil
}

func (f *DaemonFacade) CreateAgent(
	ctx context.Context,
	req *CreateDaemonAgentRequest,
	opts ...func(*Option),
) (*DaemonCommandResponse, error) {
	_ = applyOptions(opts)
	if req == nil {
		return nil, fmt.Errorf("create daemon agent: request is required")
	}
	if strings.TrimSpace(req.Harness) == "" {
		return nil, fmt.Errorf("create daemon agent: harness is required")
	}
	harness := strings.TrimSpace(req.Harness)
	name := firstNonEmpty(req.Name, harness)
	remoteID, err := f.selectCreateAgentRemoteID(ctx, req.RemoteID)
	if err != nil {
		return nil, err
	}
	command, err := f.resolveCreateAgentCommand(ctx, harness, req.Command)
	if err != nil {
		return nil, err
	}
	ack, err := f.client.CreateAgentConnection(
		ctx,
		newDaemonCommandID(),
		&model.DaemonCreateAgentConnectionCommand{
			RemoteID:     remoteID,
			Name:         name,
			CloudAgentID: strings.TrimSpace(req.CloudAgentID),
			InstanceID:   firstNonEmpty(req.InstanceID, name),
			AgentType:    firstNonEmpty(req.AgentType, harness),
			Harness:      harness,
			Command:      command,
			WorkingDir:   strings.TrimSpace(req.WorkingDir),
			Env:          req.Env,
			DesiredState: model.DaemonDesiredStateRunning,
		},
	)
	return daemonCommandResponse("create daemon agent", ack, err)
}

func (f *DaemonFacade) selectCreateAgentRemoteID(
	ctx context.Context,
	explicit string,
) (string, error) {
	explicit = strings.TrimSpace(explicit)
	result, err := f.client.ListRemotes(ctx, false)
	if err != nil {
		return "", daemonAPIGuidance(err)
	}
	if err := queryOK(result); err != nil {
		return "", err
	}
	items := daemonRemoteItems(result)
	if explicit != "" {
		for _, item := range items {
			if item != nil && item.Remote.ID == explicit {
				return item.Remote.ID, nil
			}
		}
		return "", fmt.Errorf(
			"create daemon agent: remote %q is not configured or is disabled",
			explicit,
		)
	}
	switch len(items) {
	case 0:
		return "", fmt.Errorf(
			"create daemon agent: no remotes configured; run `paxl daemon setup` or pass --remote",
		)
	case 1:
		return items[0].Remote.ID, nil
	default:
		return "", fmt.Errorf("create daemon agent: multiple remotes configured; pass --remote")
	}
}

func (f *DaemonFacade) resolveCreateAgentCommand(
	ctx context.Context,
	harnessName string,
	explicit []string,
) ([]string, error) {
	if command := normalizedCommand(explicit); len(command) > 0 {
		return command, nil
	}
	result, err := f.client.ListHarnesses(ctx, true)
	if err != nil {
		return nil, daemonAPIGuidance(err)
	}
	if err := queryOK(result); err != nil {
		return nil, err
	}
	command, cached, cachedErr := commandForDaemonHarness(daemonHarnessItems(result), harnessName)
	if cached && cachedErr == nil {
		return command, nil
	}

	result, err = f.client.DiscoverHarnesses(ctx, false, []string{harnessName})
	if err != nil {
		return nil, daemonAPIGuidance(err)
	}
	if err := queryOK(result); err != nil {
		return nil, err
	}
	if command, ok, err := commandForDaemonHarness(daemonHarnessItems(result), harnessName); ok ||
		err != nil {
		return command, err
	}
	if cachedErr != nil {
		return nil, cachedErr
	}
	return nil, fmt.Errorf(
		"create daemon agent: harness %q is not known; run `paxl daemon harness discover --probe %s` and choose an available harness",
		harnessName,
		harnessName,
	)
}

func commandForDaemonHarness(
	items []*model.DaemonHarnessView,
	harnessName string,
) ([]string, bool, error) {
	for _, item := range items {
		if item == nil || !strings.EqualFold(strings.TrimSpace(item.Harness), harnessName) {
			continue
		}
		state := strings.TrimSpace(item.State)
		if state != "" && state != "available" {
			reason := firstNonEmpty(item.LastError, item.InstallHint, "harness is not available")
			return nil, true, fmt.Errorf(
				"create daemon agent: harness %q is %s: %s",
				harnessName,
				state,
				reason,
			)
		}
		command := normalizedCommand(item.Command)
		if len(command) == 0 {
			return nil, true, fmt.Errorf(
				"create daemon agent: harness %q has no command configured; run `paxl daemon harness discover --probe %s`",
				harnessName,
				harnessName,
			)
		}
		return command, true, nil
	}
	return nil, false, nil
}

func daemonRemoteItems(result *model.DaemonQueryResult) []*model.DaemonRemoteView {
	if result == nil || result.Remotes == nil {
		return nil
	}
	return result.Remotes.Items
}

func daemonHarnessItems(result *model.DaemonQueryResult) []*model.DaemonHarnessView {
	if result == nil || result.Harnesses == nil {
		return nil
	}
	return result.Harnesses.Items
}

func normalizedCommand(command []string) []string {
	normalized := make([]string, 0, len(command))
	for _, word := range command {
		word = strings.TrimSpace(word)
		if word != "" {
			normalized = append(normalized, word)
		}
	}
	return normalized
}

func (f *DaemonFacade) UpdateAgent(
	ctx context.Context,
	req *UpdateDaemonAgentRequest,
	opts ...func(*Option),
) (*DaemonCommandResponse, error) {
	_ = applyOptions(opts)
	if req == nil || strings.TrimSpace(req.ConnectionID) == "" {
		return nil, fmt.Errorf("update daemon agent: connection id is required")
	}
	ack, err := f.client.UpdateAgentConnection(
		ctx,
		newDaemonCommandID(),
		strings.TrimSpace(req.ConnectionID),
		&model.DaemonUpdateAgentConnectionCommand{
			Name:         req.Name,
			CloudAgentID: req.CloudAgentID,
			InstanceID:   req.InstanceID,
			AgentType:    req.AgentType,
			Harness:      req.Harness,
			Command:      req.Command,
			WorkingDir:   req.WorkingDir,
			Enabled:      req.Enabled,
			DesiredState: req.DesiredState,
		},
	)
	return daemonCommandResponse("update daemon agent", ack, err)
}

func (f *DaemonFacade) RestartAgent(
	ctx context.Context,
	connectionID string,
	opts ...func(*Option),
) (*DaemonCommandResponse, error) {
	_ = applyOptions(opts)
	connectionID = strings.TrimSpace(connectionID)
	if connectionID == "" {
		return nil, fmt.Errorf("restart daemon agent: connection id is required")
	}
	ack, err := f.client.RestartAgentConnection(ctx, newDaemonCommandID(), connectionID)
	return daemonCommandResponse("restart daemon agent", ack, err)
}

func (f *DaemonFacade) StopAgent(
	ctx context.Context,
	connectionID string,
	opts ...func(*Option),
) (*DaemonCommandResponse, error) {
	_ = applyOptions(opts)
	state := model.DaemonDesiredStateStopped
	return f.UpdateAgent(ctx, &UpdateDaemonAgentRequest{
		ConnectionID: strings.TrimSpace(connectionID),
		DesiredState: &state,
	})
}

func (f *DaemonFacade) RemoveAgent(
	ctx context.Context,
	connectionID string,
	opts ...func(*Option),
) (*DaemonCommandResponse, error) {
	_ = applyOptions(opts)
	connectionID = strings.TrimSpace(connectionID)
	if connectionID == "" {
		return nil, fmt.Errorf("remove daemon agent: connection id is required")
	}
	ack, err := f.client.DeleteAgentConnection(ctx, newDaemonCommandID(), connectionID)
	return daemonCommandResponse("remove daemon agent", ack, err)
}

func (f *DaemonFacade) ListHarnesses(
	ctx context.Context,
	req *ListDaemonHarnessesRequest,
	opts ...func(*Option),
) (*ListDaemonHarnessesResponse, error) {
	_ = applyOptions(opts)
	if req == nil {
		req = &ListDaemonHarnessesRequest{}
	}
	result, err := f.client.ListHarnesses(ctx, req.IncludeMissing)
	if err != nil {
		return nil, daemonAPIGuidance(err)
	}
	if err := queryOK(result); err != nil {
		return nil, err
	}
	if result.Harnesses == nil {
		return &ListDaemonHarnessesResponse{}, nil
	}
	return &ListDaemonHarnessesResponse{Harnesses: result.Harnesses.Items}, nil
}

func (f *DaemonFacade) DiscoverHarnesses(
	ctx context.Context,
	req *DiscoverDaemonHarnessesRequest,
	opts ...func(*Option),
) (*ListDaemonHarnessesResponse, error) {
	_ = applyOptions(opts)
	if req == nil {
		req = &DiscoverDaemonHarnessesRequest{}
	}
	result, err := f.client.DiscoverHarnesses(ctx, req.Probe, req.Names)
	if err != nil {
		return nil, daemonAPIGuidance(err)
	}
	if err := queryOK(result); err != nil {
		return nil, err
	}
	if result.Harnesses == nil {
		return &ListDaemonHarnessesResponse{}, nil
	}
	return &ListDaemonHarnessesResponse{Harnesses: result.Harnesses.Items}, nil
}

func (f *DaemonFacade) LocalOverview(
	ctx context.Context,
	req *DaemonLocalOverviewRequest,
	opts ...func(*Option),
) (*DaemonLocalOverviewResponse, error) {
	_ = req
	_ = applyOptions(opts)
	result, err := f.client.GetLocalOverview(ctx)
	if err != nil {
		return nil, daemonAPIGuidance(err)
	}
	if err := queryOK(result); err != nil {
		return nil, err
	}
	if result.LocalOverview == nil {
		return &DaemonLocalOverviewResponse{}, nil
	}
	return &DaemonLocalOverviewResponse{Overview: result.LocalOverview}, nil
}

func (f *DaemonFacade) ListLocalSessions(
	ctx context.Context,
	req *ListDaemonLocalSessionsRequest,
	opts ...func(*Option),
) (*ListDaemonLocalSessionsResponse, error) {
	_ = applyOptions(opts)
	if req == nil {
		req = &ListDaemonLocalSessionsRequest{}
	}
	result, err := f.client.ListLocalSessions(ctx, req.Agent, req.Limit)
	if err != nil {
		return nil, daemonAPIGuidance(err)
	}
	if err := queryOK(result); err != nil {
		return nil, err
	}
	if result.LocalSessions == nil {
		return &ListDaemonLocalSessionsResponse{}, nil
	}
	return &ListDaemonLocalSessionsResponse{Sessions: result.LocalSessions.Items}, nil
}

func (f *DaemonFacade) SyncLocalSessions(
	ctx context.Context,
	req *SyncDaemonLocalSessionsRequest,
	opts ...func(*Option),
) (*SyncDaemonLocalSessionsResponse, error) {
	_ = applyOptions(opts)
	if req == nil {
		req = &SyncDaemonLocalSessionsRequest{}
	}
	result, err := f.client.SyncLocalSessions(ctx, req.Agent, req.Limit, req.TimeoutMillis)
	if err != nil {
		return nil, daemonAPIGuidance(err)
	}
	if err := queryOK(result); err != nil {
		return nil, err
	}
	if result.LocalSessionSync == nil {
		return &SyncDaemonLocalSessionsResponse{}, nil
	}
	return &SyncDaemonLocalSessionsResponse{Sync: result.LocalSessionSync}, nil
}

func daemonCommandResponse(
	action string,
	ack *model.DaemonCommandAck,
	err error,
) (*DaemonCommandResponse, error) {
	if err != nil {
		return nil, daemonAPIGuidance(err)
	}
	if err := ackOK(ack); err != nil {
		return nil, fmt.Errorf("%s: %w", action, err)
	}
	return &DaemonCommandResponse{Ack: ack}, nil
}

func queryOK(result *model.DaemonQueryResult) error {
	if result != nil && result.Error != nil {
		return result.Error
	}
	return nil
}

func ackOK(ack *model.DaemonCommandAck) error {
	if ack == nil {
		return fmt.Errorf("local daemon API returned no command ack")
	}
	if ack.OK ||
		ack.Status == model.DaemonCommandStatusReceived ||
		ack.Status == model.DaemonCommandStatusApplied {
		return nil
	}
	if ack.Error != nil {
		return ack.Error
	}
	return fmt.Errorf(
		"local daemon API command failed: %s",
		firstNonEmpty(string(ack.Status), "unknown"),
	)
}

func daemonAPIGuidance(err error) error {
	if err == nil {
		return nil
	}
	var statusErr *daemonHTTPStatusError
	if errors.As(err, &statusErr) && statusErr.clientError() {
		return err
	}
	return fmt.Errorf("%w; is paxd running? try `paxd run` or `paxd setup`", err)
}

func newDaemonCommandID() string {
	var raw [8]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "cmd_local_fallback"
	}
	return "cmd_" + hex.EncodeToString(raw[:])
}
