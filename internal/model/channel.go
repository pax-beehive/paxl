package model

type ChannelKind string

const (
	ChannelKindUnknown ChannelKind = ""
	ChannelKindOnPrem  ChannelKind = "onprem"
)

type ChannelProfile struct {
	ProfileID    string      `json:"profile_id"`
	Name         string      `json:"name"`
	Kind         ChannelKind `json:"kind"`
	URL          string      `json:"url"`
	APIKey       string      `json:"-"`
	CAFile       string      `json:"ca_file,omitempty"`
	AgentID      string      `json:"agent_id,omitempty"`
	UserID       string      `json:"user_id,omitempty"`
	CredentialID string      `json:"credential_id,omitempty"`
	Permissions  []string    `json:"permissions,omitempty"`
	Enabled      bool        `json:"enabled"`
	AutoReceive  bool        `json:"auto_receive"`
	CreatedAt    string      `json:"created_at,omitempty"`
	UpdatedAt    string      `json:"updated_at,omitempty"`
}

type ChannelAgent struct {
	AgentID     string `json:"agent_id"`
	DisplayName string `json:"display_name"`
	Description string `json:"description"`
	AgentType   string `json:"agent_type"`
}
