package model

type AuthCredential struct {
	ManagerURL   string `json:"manager_url"`
	APIKey       string `json:"-"`
	UserAPIKeyID string `json:"user_api_key_id,omitempty"`
	NodeID       string `json:"node_id,omitempty"`
	UserID       string `json:"user_id,omitempty"`
	Email        string `json:"email,omitempty"`
	DisplayName  string `json:"display_name,omitempty"`
	Role         string `json:"role,omitempty"`
	CreatedAt    string `json:"created_at,omitempty"`
	UpdatedAt    string `json:"updated_at,omitempty"`
}
