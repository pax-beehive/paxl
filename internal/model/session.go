package model

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
	CreatedAt           string
}
