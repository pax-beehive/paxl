package adaptor

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/pax-oss/paxl/internal/model"
)

type dshBlock struct {
	Type    string     `json:"type"`
	Text    string     `json:"text"`
	Content []dshBlock `json:"content"`
}

type dshMessage struct {
	Content []dshBlock `json:"content"`
	Source  struct {
		Kind  string `json:"kind"`
		Model string `json:"model"`
	} `json:"source"`
}

func (r *dshReadResult) observeMessage(event *dshEvent, history bool) error {
	role := ""
	var message dshMessage
	var usage json.RawMessage
	switch event.Type {
	case "user/message":
		role = "user"
		if err := json.Unmarshal(event.Data, &message); err != nil {
			return fmt.Errorf("invalid DSH user message")
		}
	case "assistant/message", "tool/result":
		role = "assistant"
		if event.Type == "tool/result" {
			role = "tool"
		}
		var data struct {
			Message dshMessage      `json:"message"`
			Usage   json.RawMessage `json:"usage"`
		}
		if err := json.Unmarshal(event.Data, &data); err != nil {
			return fmt.Errorf("invalid DSH message")
		}
		message, usage = data.Message, data.Usage
	case "tool/call":
		if history {
			r.appendToolCall(event)
		}
		return nil
	default:
		return nil
	}
	// Replacement messages are model-only compacted copies, not new human history.
	if string(event.Surface) != `"append"` {
		return nil
	}
	text := dshBlockText(message.Content, false)
	if role == "user" && message.Source.Kind == "user" && r.session.Preview == "" {
		r.session.Preview = titleCandidate(text)
	}
	if !history {
		return nil
	}
	raw := event.Raw
	var normalized map[string]any
	_ = json.Unmarshal(raw, &normalized)
	kind := "message"
	if role == "tool" {
		kind = "tool_result"
	}
	r.elements = append(r.elements, &model.Element{
		SessionID:     r.session.ID,
		Seq:           event.Seq,
		Type:          kind,
		Role:          role,
		Model:         firstNonEmpty(message.Source.Model, r.model),
		StartedAt:     dshTime(event.Time),
		CompletedAt:   dshTime(event.Time),
		ContentText:   text,
		UsageJSON:     string(usage),
		RawJSON:       string(raw),
		NormalizedRaw: normalized,
	})
	return nil
}

func dshBlockText(blocks []dshBlock, reasoning bool) string {
	var parts []string
	for _, block := range blocks {
		switch block.Type {
		case "text":
			if !reasoning {
				parts = append(parts, block.Text)
			}
		case "reasoning":
			if reasoning {
				parts = append(parts, block.Text)
			}
		case "tool-result":
			parts = append(parts, dshBlockText(block.Content, reasoning))
		case "image", "file":
			if !reasoning {
				parts = append(parts, "["+block.Type+"]")
			}
		}
	}
	return strings.Join(parts, "\n")
}

func (r *dshReadResult) appendToolCall(event *dshEvent) {
	var data struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	}
	if json.Unmarshal(event.Data, &data) != nil {
		return
	}
	raw := event.Raw
	var normalized map[string]any
	_ = json.Unmarshal(raw, &normalized)
	r.elements = append(r.elements, &model.Element{
		SessionID:     r.session.ID,
		Seq:           event.Seq,
		Type:          "tool_call",
		Role:          "assistant",
		StartedAt:     dshTime(event.Time),
		CompletedAt:   dshTime(event.Time),
		ContentText:   data.Name + " " + data.Arguments,
		RawJSON:       string(raw),
		NormalizedRaw: normalized,
	})
}
