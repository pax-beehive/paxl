package model

// Team is a pax-manager team.
type Team struct {
	TeamID      string `json:"team_id"`
	OwnerUserID string `json:"owner_user_id,omitempty"`
	Name        string `json:"name,omitempty"`
	Status      string `json:"status,omitempty"`
	CreatedAt   string `json:"created_at,omitempty"`
	ArchivedAt  string `json:"archived_at,omitempty"`
}

// TeamSummary augments Team with the caller's role and member/agent counts.
type TeamSummary struct {
	Team
	MyRole      string `json:"my_role,omitempty"`
	MemberCount int    `json:"member_count,omitempty"`
	AgentCount  int    `json:"agent_count,omitempty"`
}

// TeamAgent binds a registered agent to a team. Agent is included by the manager
// when available and reuses the registered node-agent shape.
type TeamAgent struct {
	TeamID           string     `json:"team_id"`
	AgentID          string     `json:"agent_id"`
	AgentOwnerUserID string     `json:"agent_owner_user_id,omitempty"`
	AgentOwnerEmail  string     `json:"agent_owner_email,omitempty"`
	AddedByUserID    string     `json:"added_by_user_id,omitempty"`
	AddedAt          string     `json:"added_at,omitempty"`
	RemovedAt        string     `json:"removed_at,omitempty"`
	RemovedByUserID  string     `json:"removed_by_user_id,omitempty"`
	Agent            *NodeAgent `json:"agent,omitempty"`
}
