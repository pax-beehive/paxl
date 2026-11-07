package adaptor

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// Paginated rollouts store their inherited prefix separately. Resolve the exact
// referenced file: following the legacy conversation's latest rollout could
// include activity written after the fork or select an unrelated branch.
type codexHistoryReader struct {
	files    []*os.File
	visiting map[string]bool
}

func (r *codexHistoryReader) close() {
	for _, file := range r.files {
		closeFile(file)
	}
}

func (r *codexHistoryReader) open(path string, end *int64) (io.Reader, error) {
	if r.visiting[path] || len(r.files) >= 64 {
		return nil, fmt.Errorf("cyclic or excessively deep codex history reference")
	}
	r.visiting[path] = true
	defer delete(r.visiting, path)
	// Paths originate from discovered local rollout files, never raw metadata.
	// #nosec G304
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open codex history: %w", err)
	}
	r.files = append(r.files, file)
	var current io.Reader = file
	if end != nil {
		info, err := file.Stat()
		if err != nil {
			return nil, fmt.Errorf("stat codex history: %w", err)
		}
		if *end < 0 || *end > info.Size() {
			return nil, fmt.Errorf("codex history byte boundary is outside referenced rollout")
		}
		current = io.LimitReader(file, *end)
	}
	meta, err := readCodexMeta(path)
	if err != nil {
		return nil, fmt.Errorf("read codex history metadata: %w", err)
	}
	if meta == nil || meta.Payload.HistoryBase == nil {
		return current, nil
	}
	base := meta.Payload.HistoryBase
	if meta.Payload.HistoryMode != "paginated" || base.EndByteOffset == nil {
		return nil, fmt.Errorf("unsupported codex history reference without paginated byte boundary")
	}
	parent, err := findExactCodexHistoryRollout(base.ThreadID)
	if err != nil {
		return nil, err
	}
	inherited, err := r.open(parent, base.EndByteOffset)
	if err != nil {
		return nil, err
	}
	return io.MultiReader(inherited, current), nil
}

func findExactCodexHistoryRollout(nativeID string) (string, error) {
	if err := validateNativeSessionID(nativeID); err != nil {
		return "", fmt.Errorf("invalid codex history thread: %w", err)
	}
	root, err := codexRoot()
	if err != nil {
		return "", err
	}
	for _, dir := range []string{"sessions", "archived_sessions"} {
		path, err := findCodexRolloutInDir(context.Background(), filepath.Join(root, dir), nativeID)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return "", fmt.Errorf("find inherited codex rollout: %w", err)
		}
		if path != "" {
			return path, nil
		}
	}
	return "", fmt.Errorf("inherited codex rollout %q not found", nativeID)
}
