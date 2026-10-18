package model

type DaemonControlError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Target  string `json:"target,omitempty"`
}

func (e *DaemonControlError) Error() string {
	if e == nil {
		return ""
	}
	if e.Code == "" {
		return e.Message
	}
	if e.Message == "" {
		return e.Code
	}
	return e.Code + ": " + e.Message
}

type DaemonCommandStatus string

const (
	DaemonCommandStatusUnknown  DaemonCommandStatus = "unknown"
	DaemonCommandStatusReceived DaemonCommandStatus = "received"
	DaemonCommandStatusRejected DaemonCommandStatus = "rejected"
	DaemonCommandStatusApplied  DaemonCommandStatus = "applied"
	DaemonCommandStatusFailed   DaemonCommandStatus = "failed"
)

type DaemonDesiredState string

const (
	DaemonDesiredStateUnknown DaemonDesiredState = "unknown"
	DaemonDesiredStateRunning DaemonDesiredState = "running"
	DaemonDesiredStateStopped DaemonDesiredState = "stopped"
	DaemonDesiredStateDeleted DaemonDesiredState = "deleted"
)

type DaemonRemoteAuthKind string

const (
	DaemonRemoteAuthUnknown          DaemonRemoteAuthKind = "unknown"
	DaemonRemoteAuthNone             DaemonRemoteAuthKind = "none"
	DaemonRemoteAuthCloudflareAccess DaemonRemoteAuthKind = "cloudflare_access"
)

type DaemonRemote struct {
	ID              string `json:"id,omitempty"`
	Name            string `json:"name"`
	CloudAPIURL     string `json:"cloud_api_url"`
	NodeControlPath string `json:"node_control_path,omitempty"`
	AgentTunnelPath string `json:"agent_tunnel_path,omitempty"`
	NodeID          string `json:"node_id,omitempty"`
	Enabled         *bool  `json:"enabled,omitempty"`
}

type DaemonRemotePatch struct {
	Name            *string `json:"name,omitempty"`
	CloudAPIURL     *string `json:"cloud_api_url,omitempty"`
	NodeControlPath *string `json:"node_control_path,omitempty"`
	AgentTunnelPath *string `json:"agent_tunnel_path,omitempty"`
	NodeID          *string `json:"node_id,omitempty"`
	Enabled         *bool   `json:"enabled,omitempty"`
}

type DaemonCreateRemoteCommand struct {
	Remote         DaemonRemote `json:"remote"`
	CloudAPIKeyRef string       `json:"cloud_api_key_ref,omitempty"`
}

type DaemonUpdateRemoteCommand struct {
	RemoteID       string            `json:"remote_id"`
	Remote         DaemonRemotePatch `json:"remote,omitempty"`
	CloudAPIKeyRef *string           `json:"cloud_api_key_ref,omitempty"`
}

type DaemonCreateAgentConnectionCommand struct {
	ID           string             `json:"id,omitempty"`
	RemoteID     string             `json:"remote_id"`
	Name         string             `json:"name"`
	CloudAgentID string             `json:"cloud_agent_id,omitempty"`
	InstanceID   string             `json:"instance_id"`
	AgentType    string             `json:"agent_type"`
	Harness      string             `json:"harness"`
	Command      []string           `json:"command"`
	WorkingDir   string             `json:"working_dir,omitempty"`
	Env          map[string]string  `json:"env,omitempty"`
	Enabled      *bool              `json:"enabled,omitempty"`
	DesiredState DaemonDesiredState `json:"desired_state,omitempty"`
}

type DaemonUpdateAgentConnectionCommand struct {
	ConnectionID string              `json:"connection_id"`
	Name         *string             `json:"name,omitempty"`
	CloudAgentID *string             `json:"cloud_agent_id,omitempty"`
	InstanceID   *string             `json:"instance_id,omitempty"`
	AgentType    *string             `json:"agent_type,omitempty"`
	Harness      *string             `json:"harness,omitempty"`
	Command      *[]string           `json:"command,omitempty"`
	WorkingDir   *string             `json:"working_dir,omitempty"`
	Env          *map[string]string  `json:"env,omitempty"`
	Enabled      *bool               `json:"enabled,omitempty"`
	DesiredState *DaemonDesiredState `json:"desired_state,omitempty"`
	DesiredSlots *int                `json:"desired_slots,omitempty"`
}

type DaemonCommandAck struct {
	CommandID         string               `json:"command_id"`
	OK                bool                 `json:"ok"`
	Status            DaemonCommandStatus  `json:"status"`
	TargetType        string               `json:"target_type,omitempty"`
	TargetID          string               `json:"target_id,omitempty"`
	DesiredGeneration int64                `json:"desired_generation,omitempty"`
	Error             *DaemonControlError  `json:"error,omitempty"`
	Result            *DaemonCommandResult `json:"result,omitempty"`
}

type DaemonCommandResult struct {
	Remote          *DaemonRemoteView          `json:"remote,omitempty"`
	AgentConnection *DaemonAgentConnectionView `json:"agent_connection,omitempty"`
	Command         *DaemonCommandView         `json:"command,omitempty"`
}

type DaemonQueryResult struct {
	Error            *DaemonControlError               `json:"error,omitempty"`
	Status           *DaemonStatus                     `json:"status,omitempty"`
	Remotes          *DaemonListRemotesResult          `json:"remotes,omitempty"`
	Remote           *DaemonRemoteView                 `json:"remote,omitempty"`
	AgentConnections *DaemonListAgentConnectionsResult `json:"agent_connections,omitempty"`
	AgentConnection  *DaemonAgentConnectionView        `json:"agent_connection,omitempty"`
	Harnesses        *DaemonListHarnessesResult        `json:"harnesses,omitempty"`
	LocalOverview    *DaemonLocalOverview              `json:"local_overview,omitempty"`
	LocalSessions    *DaemonListLocalSessionsResult    `json:"local_sessions,omitempty"`
	LocalSessionSync *DaemonLocalSessionSyncResult     `json:"local_session_sync,omitempty"`
}

type DaemonListRemotesResult struct {
	Items []*DaemonRemoteView `json:"items"`
}

type DaemonListAgentConnectionsResult struct {
	Items []*DaemonAgentConnectionView `json:"items"`
}

type DaemonListHarnessesResult struct {
	Items []*DaemonHarnessView `json:"items"`
}

type DaemonListLocalSessionsResult struct {
	Items []*DaemonLocalSessionView `json:"items"`
}

type DaemonStatus struct {
	Phase               string                    `json:"phase"`
	Remotes             []*DaemonRemoteStatusView `json:"remotes,omitempty"`
	AgentConnections    []*DaemonAgentStatusView  `json:"agent_connections,omitempty"`
	Harnesses           []*DaemonHarnessView      `json:"harnesses,omitempty"`
	LocalSessionSummary DaemonLocalSessionSummary `json:"local_session_summary,omitempty"`
}

type DaemonRemoteView struct {
	Remote       DaemonRemote            `json:"remote"`
	Generation   int64                   `json:"generation"`
	RestartNonce int64                   `json:"restart_nonce"`
	Auth         *DaemonRemoteAuthView   `json:"auth,omitempty"`
	Status       *DaemonRemoteStatusView `json:"status,omitempty"`
}

type DaemonRemoteAuthView struct {
	Kind             DaemonRemoteAuthKind `json:"kind"`
	ClientID         string               `json:"client_id,omitempty"`
	ClientSecretRef  string               `json:"client_secret_ref,omitempty"`
	ClientSecretHint string               `json:"client_secret_hint,omitempty"`
}

type DaemonRemoteStatusView struct {
	RemoteID             string `json:"remote_id"`
	ObservedGeneration   int64  `json:"observed_generation"`
	ObservedRestartNonce int64  `json:"observed_restart_nonce"`
	Phase                string `json:"phase"`
	LastErrorCode        string `json:"last_error_code,omitempty"`
	LastErrorMessage     string `json:"last_error_message,omitempty"`
	FailureClass         string `json:"failure_class,omitempty"`
	ReconnectAttempt     int    `json:"reconnect_attempt,omitempty"`
	NextRetryAt          string `json:"next_retry_at,omitempty"`
	ConnectedAt          string `json:"connected_at,omitempty"`
	StoppedAt            string `json:"stopped_at,omitempty"`
	UpdatedAt            string `json:"updated_at,omitempty"`
}

type DaemonAgentConnectionView struct {
	ID           string                 `json:"id"`
	RemoteID     string                 `json:"remote_id"`
	Name         string                 `json:"name"`
	CloudAgentID string                 `json:"cloud_agent_id,omitempty"`
	InstanceID   string                 `json:"instance_id"`
	AgentType    string                 `json:"agent_type"`
	Harness      string                 `json:"harness"`
	Command      []string               `json:"command"`
	WorkingDir   string                 `json:"working_dir,omitempty"`
	Env          map[string]string      `json:"env,omitempty"`
	Enabled      bool                   `json:"enabled"`
	DesiredState DaemonDesiredState     `json:"desired_state"`
	Generation   int64                  `json:"generation"`
	RestartNonce int64                  `json:"restart_nonce"`
	Status       *DaemonAgentStatusView `json:"status,omitempty"`
}

type DaemonAgentStatusView struct {
	ConnectionID         string            `json:"connection_id"`
	ObservedGeneration   int64             `json:"observed_generation"`
	ObservedRestartNonce int64             `json:"observed_restart_nonce"`
	Phase                string            `json:"phase"`
	PID                  int               `json:"pid,omitempty"`
	LastErrorCode        string            `json:"last_error_code,omitempty"`
	LastErrorMessage     string            `json:"last_error_message,omitempty"`
	FailureClass         string            `json:"failure_class,omitempty"`
	ReconnectAttempt     int               `json:"reconnect_attempt,omitempty"`
	NextRetryAt          string            `json:"next_retry_at,omitempty"`
	StartedAt            string            `json:"started_at,omitempty"`
	ConnectedAt          string            `json:"connected_at,omitempty"`
	StoppedAt            string            `json:"stopped_at,omitempty"`
	UpdatedAt            string            `json:"updated_at,omitempty"`
	Details              map[string]string `json:"details,omitempty"`
}

type DaemonHarnessView struct {
	Harness     string   `json:"harness"`
	DisplayName string   `json:"display_name"`
	State       string   `json:"state"`
	Capability  string   `json:"capability,omitempty"`
	Command     []string `json:"command,omitempty"`
	Version     string   `json:"version,omitempty"`
	Source      string   `json:"source,omitempty"`
	InstallHint string   `json:"install_hint,omitempty"`
	LastError   string   `json:"last_error,omitempty"`
}

type DaemonLocalOverview struct {
	Sessions  []*DaemonLocalSessionView `json:"sessions,omitempty"`
	Harnesses []*DaemonHarnessView      `json:"harnesses,omitempty"`
}

type DaemonLocalSessionSummary struct {
	Total int `json:"total"`
}

type DaemonLocalSessionView struct {
	ID           string                           `json:"id"`
	Agent        string                           `json:"agent"`
	NativeID     string                           `json:"native_id"`
	Title        string                           `json:"title,omitempty"`
	Status       string                           `json:"status,omitempty"`
	Preview      string                           `json:"preview,omitempty"`
	ProjectID    string                           `json:"project_id,omitempty"`
	UpdatedAt    string                           `json:"updated_at,omitempty"`
	LastActive   string                           `json:"last_active,omitempty"`
	LastListedAt string                           `json:"last_listed_at,omitempty"`
	LastSyncedAt string                           `json:"last_synced_at,omitempty"`
	Metadata     map[string]string                `json:"metadata,omitempty"`
	Elements     []*DaemonLocalSessionElementView `json:"elements,omitempty"`
}

type DaemonLocalSessionElementView struct {
	Seq         int64             `json:"seq"`
	Kind        string            `json:"kind"`
	Role        string            `json:"role,omitempty"`
	Text        string            `json:"text,omitempty"`
	Metadata    map[string]string `json:"metadata,omitempty"`
	StartedAt   string            `json:"started_at,omitempty"`
	CompletedAt string            `json:"completed_at,omitempty"`
}

type DaemonLocalSessionSyncResult struct {
	Synced int                   `json:"synced"`
	Failed int                   `json:"failed"`
	Errors []*DaemonControlError `json:"errors,omitempty"`
}

type DaemonCommandView struct {
	CommandID         string              `json:"command_id"`
	Type              string              `json:"type"`
	TargetType        string              `json:"target_type,omitempty"`
	TargetID          string              `json:"target_id,omitempty"`
	Status            DaemonCommandStatus `json:"status"`
	DesiredGeneration int64               `json:"desired_generation,omitempty"`
	ErrorCode         string              `json:"error_code,omitempty"`
	ErrorMessage      string              `json:"error_message,omitempty"`
	ReceivedAt        string              `json:"received_at,omitempty"`
	AppliedAt         string              `json:"applied_at,omitempty"`
	UpdatedAt         string              `json:"updated_at,omitempty"`
}
