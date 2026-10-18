package model

// HarnessAuthEvent is ephemeral authentication UI data, never a durable log.
// Prompts and notices may contain one-time codes or authorization URLs.
type HarnessAuthEvent struct {
	Version int                 `json:"version"`
	Type    string              `json:"type"`
	ID      string              `json:"id,omitempty"`
	Kind    string              `json:"kind,omitempty"`
	Message string              `json:"message,omitempty"`
	URL     string              `json:"url,omitempty"`
	Code    string              `json:"code,omitempty"`
	Options []HarnessAuthChoice `json:"options,omitempty"`
	Result  *HarnessAuthResult  `json:"result,omitempty"`
}
type HarnessAuthChoice struct {
	ID    string `json:"id"`
	Label string `json:"label,omitempty"`
}
type HarnessAuthEventSink func(*HarnessAuthEvent) error
