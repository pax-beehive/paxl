package adaptor

import (
	"os"
	"sort"
	"strings"
	"time"

	"github.com/pax-oss/paxl/internal/model"
)

const scannerMaxTokenSize = 16 * 1024 * 1024

func sortedSessions(
	sessions map[string]*model.Session,
	req *ListSessionsRequest,
) *ListSessionsResponse {
	out := make([]*model.Session, 0, len(sessions))
	for _, session := range sessions {
		out = append(out, session)
	}
	sort.Slice(out, func(i int, j int) bool {
		return sessionUpdatedAtAfter(out[i], out[j])
	})
	if req != nil && req.Limit > 0 && len(out) > req.Limit {
		out = out[:req.Limit]
	}
	return &ListSessionsResponse{Sessions: out}
}

func sessionUpdatedAtAfter(left *model.Session, right *model.Session) bool {
	leftTime, leftOK := parseSessionTime(left.UpdatedAt)
	rightTime, rightOK := parseSessionTime(right.UpdatedAt)
	if leftOK && rightOK {
		return leftTime.After(rightTime)
	}
	return left.UpdatedAt > right.UpdatedAt
}

func parseSessionTime(value string) (time.Time, bool) {
	parsed, err := time.Parse(time.RFC3339Nano, value)
	return parsed, err == nil
}

func titleCandidate(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || isNoisyTitleText(value) {
		return ""
	}
	if command := xmlTagValue(value, "command-name"); command != "" {
		return trimOneLine(command, 80)
	}
	return trimOneLine(value, 80)
}

func isNoisyTitleText(value string) bool {
	trimmed := strings.TrimSpace(value)
	lower := strings.ToLower(trimmed)
	return strings.HasPrefix(lower, "<local-command-caveat>") ||
		strings.HasPrefix(lower, "<session_context>") ||
		strings.HasPrefix(lower, "system_handoff") ||
		strings.HasPrefix(lower, "<environment_context>") ||
		strings.HasPrefix(
			lower,
			"the following is the codex agent history whose request action you are assessing.",
		) ||
		strings.HasPrefix(lower, "assess the exact planned action below.") ||
		strings.HasPrefix(trimmed, "# AGENTS.md instructions for ") ||
		strings.HasPrefix(trimmed, "AGENTS.md instructions for ") ||
		strings.HasPrefix(trimmed, "<INSTRUCTIONS>")
}

func xmlTagValue(value string, tag string) string {
	start := "<" + tag + ">"
	end := "</" + tag + ">"
	startIndex := strings.Index(value, start)
	if startIndex < 0 {
		return ""
	}
	contentStart := startIndex + len(start)
	contentEnd := strings.Index(value[contentStart:], end)
	if contentEnd < 0 {
		return ""
	}
	return strings.TrimSpace(value[contentStart : contentStart+contentEnd])
}

func trimOneLine(value string, limit int) string {
	value = strings.Join(strings.Fields(value), " ")
	if len([]rune(value)) <= limit {
		return value
	}
	return string([]rune(value)[:limit])
}

func closeFile(file *os.File) {
	_ = file.Close()
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
