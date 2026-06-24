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
