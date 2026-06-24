package model

type Node struct {
	NodeID        string  `json:"node_id"`
	OwnerUserID   string  `json:"owner_user_id,omitempty"`
	Kind          string  `json:"kind,omitempty"`
	Name          string  `json:"name,omitempty"`
	Hostname      string  `json:"hostname,omitempty"`
	MachineType   string  `json:"machine_type,omitempty"`
	OS            string  `json:"os,omitempty"`
	Arch          string  `json:"arch,omitempty"`
	PaxdVersion   string  `json:"paxd_version,omitempty"`
	APIEndpoint   string  `json:"api_endpoint,omitempty"`
	Status        string  `json:"status,omitempty"`
	Online        bool    `json:"online,omitempty"`
	RegisteredAt  string  `json:"registered_at,omitempty"`
	LastHeartbeat *string `json:"last_heartbeat,omitempty"`
}

type NodeAgent struct {
	AgentID       string  `json:"agent_id"`
	NodeID        string  `json:"node_id,omitempty"`
	OwnerUserID   string  `json:"owner_user_id,omitempty"`
	Name          string  `json:"name,omitempty"`
	AgentType     string  `json:"agent_type,omitempty"`
	Status        string  `json:"status,omitempty"`
	Online        bool    `json:"online,omitempty"`
	LastHeartbeat *string `json:"last_heartbeat,omitempty"`
	RegisteredAt  string  `json:"registered_at,omitempty"`
}

type NodeSession struct {
	ID             int64    `json:"id,omitempty"`
	NodeID         string   `json:"node_id,omitempty"`
	AgentID        string   `json:"agent_id,omitempty"`
	SessionID      string   `json:"session_id"`
	Name           string   `json:"name,omitempty"`
	AgentType      string   `json:"agent_type,omitempty"`
	ProjectID      string   `json:"project_id,omitempty"`
	Preview        string   `json:"preview,omitempty"`
	WorkspaceRoots []string `json:"workspace_roots,omitempty"`
	Source         string   `json:"source,omitempty"`
	Status         string   `json:"status,omitempty"`
	CurrentTask    string   `json:"current_task,omitempty"`
	LastMessageAt  string   `json:"last_message_at,omitempty"`
	MessageCount   int      `json:"message_count,omitempty"`
	Model          string   `json:"model,omitempty"`
	RunID          string   `json:"run_id,omitempty"`
	RunStatus      string   `json:"run_status,omitempty"`
	CreatedAt      string   `json:"created_at,omitempty"`
	UpdatedAt      string   `json:"updated_at,omitempty"`
}
