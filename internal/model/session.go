package model

import "encoding/json"

type Session struct {
	ID                 string
	Agent              AgentName
	NativeID           string
	Title              string
	Status             string
	Preview            string
	ProjectID          string
	WorkspaceRootsJSON string
	LastActive         string
	UpdatedAt          string
	LastListedAt       string
	LastSyncedAt       string
	CurrentSyncVersion int64
	RawJSON            string
}

type Element struct {
	SessionID     string
	SyncVersion   int64
	Seq           int64
	Type          string
	Role          string
	Model         string
	StartedAt     string
	CompletedAt   string
	DurationMS    int64
	UsageJSON     string
	ContentText   string
	NormalizedRaw map[string]any
	RawJSON       string
}

type KnowledgeCapsule struct {
	CapsuleID              string
	SourceNodeID           string
	SourceSessionID        string
	SourceAgent            AgentName
	Keyword                string
	Title                  string
	Summary                string
	Content                string
	Status                 string
	Truncated              bool
	OriginalEstimatedChars int64
	CreatedAt              string
	ArchivedAt             string
}

type KnowledgeInjection struct {
	InjectionID         string
	CapsuleID           string
	SourceNodeID        string
	SourceAgent         AgentName
	SourceSessionID     string
	TargetNodeID        string
	TargetSessionID     string
	TargetAgent         AgentName
	DeliveryMethod      string
	DeliveryMessageType string
	Status              string
	RouteMatchType      string
	RouteMatchValue     string
	ActionItemsJSON     string
	CreatedAt           string
	ClaimedAt           string
	ConsumedAt          string
}

type Envelope struct {
	EnvelopeID      string          `json:"envelope_id"`
	SenderUserID    string          `json:"sender_user_id"`
	SenderEmail     string          `json:"sender_email"`
	RecipientUserID string          `json:"recipient_user_id"`
	RecipientEmail  string          `json:"recipient_email"`
	PayloadType     string          `json:"payload_type"`
	PayloadJSON     json.RawMessage `json:"payload_json"`
	Message         string          `json:"message"`
	Status          string          `json:"status"`
	CreatedAt       string          `json:"created_at"`
	AcceptedAt      string          `json:"accepted_at"`
	ArchivedAt      string          `json:"archived_at"`
}

type Friend struct {
	FriendID        string `json:"friend_id"`
	RequesterUserID string `json:"requester_user_id"`
	RequesterEmail  string `json:"requester_email"`
	RequesterAlias  string `json:"requester_alias"`
	RecipientUserID string `json:"recipient_user_id"`
	RecipientEmail  string `json:"recipient_email"`
	RecipientAlias  string `json:"recipient_alias"`
	Status          string `json:"status"`
	CreatedAt       string `json:"created_at"`
	AcceptedAt      string `json:"accepted_at"`
	RemovedAt       string `json:"removed_at"`
	BlockedAt       string `json:"blocked_at"`
}
