package adaptor

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/pax-oss/paxl/internal/model"
)

func decodeCodexHistoryElement(raw []byte, seq int64, sessionID string) (*model.Element, bool) {
	var envelope codexEnvelope
	if json.Unmarshal(raw, &envelope) != nil {
		return nil, false
	}
	var payload struct {
		Type      string          `json:"type"`
		Name      string          `json:"name"`
		CallID    string          `json:"call_id"`
		Arguments string          `json:"arguments"`
		Output    string          `json:"output"`
		Summary   json.RawMessage `json:"summary"`
		Encrypted string          `json:"encrypted_content"`
		Model     string          `json:"model"`
		Info      json.RawMessage `json:"info"`
	}
	if json.Unmarshal(envelope.Payload, &payload) != nil {
		return nil, false
	}
	e := &model.Element{
		SessionID:   sessionID,
		Seq:         seq,
		StartedAt:   envelope.Timestamp,
		CompletedAt: envelope.Timestamp,
		RawJSON:     string(raw),
	}
	if envelope.Type == "event_msg" && payload.Type == "token_count" && len(payload.Info) > 0 &&
		string(payload.Info) != "null" {
		e.Type = "usage"
		e.UsageJSON = string(payload.Info)
		e.ContentText = string(payload.Info)
		e.NormalizedRaw = map[string]any{"kind": "token_count"}
		return e, true
	}
	if envelope.Type != "response_item" {
		return nil, false
	}
	switch payload.Type {
	case "reasoning":
		e.Type = "thinking"
		e.Model = payload.Model
		if len(payload.Summary) > 0 && string(payload.Summary) != "[]" &&
			string(payload.Summary) != "null" {
			e.ContentText = string(payload.Summary)
		} else if payload.Encrypted != "" {
			e.ContentText = "[encrypted reasoning content]"
		}
	case "function_call":
		e.Type = "tool_call"
		e.ContentText = fmt.Sprintf("%s %s\n%s", payload.Name, payload.CallID, payload.Arguments)
		e.NormalizedRaw = map[string]any{
			"name":      payload.Name,
			"callId":    payload.CallID,
			"arguments": codexJSONOrText(payload.Arguments),
		}
	case "function_call_output":
		e.Type = "tool_result"
		e.ContentText = payload.Output
		e.NormalizedRaw = map[string]any{
			"callId": payload.CallID,
			"output": codexJSONOrText(payload.Output),
		}
	default:
		return nil, false
	}
	return e, strings.TrimSpace(e.ContentText) != ""
}

func codexJSONOrText(value string) any {
	var parsed any
	if json.Unmarshal([]byte(value), &parsed) == nil {
		return parsed
	}
	return strings.TrimSpace(value)
}

func codexEventDuration(raw []byte) (string, int64) {
	var event struct {
		Type    string `json:"type"`
		Payload struct {
			CallID   string `json:"call_id"`
			Duration struct {
				Secs  int64 `json:"secs"`
				Nanos int64 `json:"nanos"`
			} `json:"duration"`
		} `json:"payload"`
	}
	if json.Unmarshal(raw, &event) != nil || event.Type != "event_msg" {
		return "", 0
	}
	return event.Payload.CallID, event.Payload.Duration.Secs*1000 + event.Payload.Duration.Nanos/1000000
}
