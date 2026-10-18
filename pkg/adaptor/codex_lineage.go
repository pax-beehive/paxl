package adaptor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/pax-oss/paxl/internal/model"
)

type codexConversationRollout struct {
	path string
	meta *codexMetaLine
}

func isCodexInternal(meta *codexMetaLine) bool {
	if meta.Payload.ThreadSource == "subagent" || meta.Payload.ThreadSource == "guardian_review" {
		return true
	}
	var source struct {
		Subagent json.RawMessage `json:"subagent"`
	}
	return json.Unmarshal(meta.Payload.Source, &source) == nil && len(source.Subagent) > 0 &&
		string(source.Subagent) != "null"
}

func codexConversationRollouts(
	ctx context.Context,
	paths []string,
) ([]codexConversationRollout, map[string]string, error) {
	rollouts := make([]codexConversationRollout, 0, len(paths))
	parents := make(map[string]string)
	for _, path := range paths {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		meta, err := readCodexMeta(path)
		if shouldSkipCodexMeta(meta, err) {
			continue
		}
		rollouts = append(rollouts, codexConversationRollout{path: path, meta: meta})
		// Internal threads must retain their own identity when explicitly requested.
		if isCodexInternal(meta) {
			continue
		}
		parent := firstNonEmpty(meta.Payload.ForkedFromID, meta.Payload.ParentThreadID)
		if parent == "" && meta.Payload.SessionID != meta.Payload.ID {
			parent = meta.Payload.SessionID
		}
		if parent != "" && parent != meta.Payload.ID {
			parents[meta.Payload.ID] = parent
		}
	}
	return rollouts, parents, nil
}

func codexConversationRoot(id string, parents map[string]string) string {
	seen := make(map[string]bool)
	for parents[id] != "" && !seen[id] {
		seen[id] = true
		id = parents[id]
	}
	return id
}

func mergeCodexConversationIndex(
	sessions map[string]*model.Session,
	rollouts []codexConversationRollout,
	parents map[string]string,
	includeInternal bool,
) {
	internal := make(map[string]bool)
	for _, r := range rollouts {
		if isCodexInternal(r.meta) {
			internal[r.meta.Payload.ID] = true
		}
	}
	merged := make(map[string]*model.Session)
	for id, session := range sessions {
		delete(sessions, id)
		if !includeInternal && internal[session.NativeID] {
			continue
		}
		root := codexConversationRoot(session.NativeID, parents)
		key := "codex:" + root
		existing := merged[key]
		if existing == nil {
			copy := *session
			copy.ID = key
			copy.NativeID = root
			merged[key] = &copy
			continue
		}
		// Compare index timestamps before rollout activity changes UpdatedAt.
		if session.Title != "" &&
			(existing.Title == "" || laterCodexTimestamp(existing.UpdatedAt, session.UpdatedAt) == session.UpdatedAt) {
			existing.Title = session.Title
		}
		existing.UpdatedAt = laterCodexTimestamp(existing.UpdatedAt, session.UpdatedAt)
		existing.LastActive = laterCodexTimestamp(existing.LastActive, session.LastActive)
	}
	for id, session := range merged {
		sessions[id] = session
	}
}

func findCodexConversationRollout(root, nativeID string) (string, error) {
	var paths []string
	for _, dir := range []string{"sessions", "archived_sessions"} {
		found, err := recentCodexRolloutPaths(context.Background(), filepath.Join(root, dir), 0)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
		paths = append(paths, found...)
	}
	rollouts, parents, err := codexConversationRollouts(context.Background(), paths)
	if err != nil {
		return "", err
	}
	key := codexConversationRoot(nativeID, parents)
	var best string
	var modified time.Time
	for _, r := range rollouts {
		if isCodexInternal(r.meta) {
			if r.meta.Payload.ID == nativeID {
				return r.path, nil
			}
			continue
		}
		if codexConversationRoot(r.meta.Payload.ID, parents) != key {
			continue
		}
		info, err := os.Stat(r.path)
		if err != nil {
			return "", fmt.Errorf("stat codex rollout: %w", err)
		}
		if best == "" || info.ModTime().After(modified) {
			best = r.path
			modified = info.ModTime()
		}
	}
	if best != "" {
		return best, nil
	}
	// Metadata can be missing in older rollouts; exact filename lookup still works.
	for _, dir := range []string{"sessions", "archived_sessions"} {
		path, err := findCodexRolloutInDir(filepath.Join(root, dir), nativeID)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
		if path != "" {
			return path, nil
		}
	}
	return "", fmt.Errorf("codex rollout %q not found", nativeID)
}
