package adaptor

import (
	"bufio"
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/pax-oss/paxl/internal/model"
)

//go:embed pi_bridge_extension.js
var piBridgeAssets embed.FS

const (
	piBridgeDeliveryMethod = "pi_extension_steer"
	piBridgeExtensionFile  = "paxl-bridge.js"
)

type piBridgeRecord struct {
	SessionID  string `json:"session_id"`
	CWD        string `json:"cwd"`
	Title      string `json:"title"`
	SocketPath string `json:"socket_path"`
	PID        int    `json:"pid"`
	UpdatedAt  string `json:"updated_at"`
}

type piBridgeRPCResponse struct {
	ID     int64           `json:"id,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *acpRPCError    `json:"error,omitempty"`
}

func InstallPiBridgeExtension(path string) (string, error) {
	if path == "" {
		root, err := piRoot()
		if err != nil {
			return "", err
		}
		path = filepath.Join(root, "extensions", piBridgeExtensionFile)
	}
	source, err := piBridgeAssets.ReadFile("pi_bridge_extension.js")
	if err != nil {
		return "", fmt.Errorf("read embedded pi bridge extension: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", fmt.Errorf("create pi extension directory: %w", err)
	}
	if err := os.WriteFile(path, source, 0o600); err != nil {
		return "", fmt.Errorf("write pi bridge extension: %w", err)
	}
	return path, nil
}

func PiBridgeExtensionSource() (string, error) {
	source, err := piBridgeAssets.ReadFile("pi_bridge_extension.js")
	if err != nil {
		return "", fmt.Errorf("read embedded pi bridge extension: %w", err)
	}
	return string(source), nil
}

func listPiBridgeSessions(ctx context.Context) (map[string]*model.Session, error) {
	records, err := loadPiBridgeRecords(ctx)
	if err != nil {
		return nil, err
	}
	sessions := make(map[string]*model.Session, len(records))
	for _, record := range records {
		if record.SessionID == "" || record.SocketPath == "" {
			continue
		}
		if !pathExists(record.SocketPath) {
			continue
		}
		sessionID := "pi:" + record.SessionID
		sessions[sessionID] = &model.Session{
			ID:         sessionID,
			Agent:      model.AgentNamePi,
			NativeID:   record.SessionID,
			Title:      firstNonEmpty(record.Title, record.SessionID),
			Status:     "online",
			ProjectID:  record.CWD,
			LastActive: record.UpdatedAt,
			UpdatedAt:  record.UpdatedAt,
		}
	}
	return sessions, nil
}

func promptPiBridgeSession(ctx context.Context, nativeID string, text string) (string, error) {
	if runtime.GOOS == "windows" {
		return "", fmt.Errorf("pi bridge sockets are unsupported on windows")
	}
	record, err := findPiBridgeRecord(ctx, nativeID)
	if err != nil {
		return "", err
	}
	if record.SocketPath == "" {
		return "", fmt.Errorf("pi bridge socket is missing")
	}
	result := struct {
		Delivery string `json:"delivery"`
	}{}
	if err := piBridgeCall(
		ctx,
		record.SocketPath,
		1,
		"session/prompt",
		map[string]any{
			"sessionId": nativeID,
			"delivery":  "steer",
			"prompt": []map[string]string{
				{"type": "text", "text": text},
			},
		},
		&result,
	); err != nil {
		return "", err
	}
	return firstNonEmpty(result.Delivery, piBridgeDeliveryMethod), nil
}

func findPiBridgeRecord(ctx context.Context, nativeID string) (*piBridgeRecord, error) {
	records, err := loadPiBridgeRecords(ctx)
	if err != nil {
		return nil, err
	}
	for _, record := range records {
		if record.SessionID == nativeID && record.SocketPath != "" &&
			pathExists(record.SocketPath) {
			return record, nil
		}
	}
	return nil, fmt.Errorf("active pi bridge session %q not found", nativeID)
}

func loadPiBridgeRecords(ctx context.Context) ([]*piBridgeRecord, error) {
	root, err := piBridgeRoot()
	if err != nil {
		return nil, err
	}
	dir := filepath.Join(root, "sessions")
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read pi bridge sessions: %w", err)
	}
	records := make([]*piBridgeRecord, 0, len(entries))
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		record, err := readPiBridgeRecord(filepath.Join(dir, entry.Name()))
		if err == nil {
			records = append(records, record)
		}
	}
	return records, nil
}

func readPiBridgeRecord(path string) (*piBridgeRecord, error) {
	// Bridge registry files are discovered under the local Pi bridge root.
	// #nosec G304
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	record := &piBridgeRecord{}
	if err := json.Unmarshal(raw, record); err != nil {
		return nil, err
	}
	return record, nil
}

func piBridgeCall(
	ctx context.Context,
	socketPath string,
	id int64,
	method string,
	params any,
	out any,
) error {
	dialer := net.Dialer{Timeout: 2 * time.Second}
	conn, err := dialer.DialContext(ctx, "unix", socketPath)
	if err != nil {
		return fmt.Errorf("dial pi bridge: %w", err)
	}
	defer closeConn(conn)
	request, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"method":  method,
		"params":  params,
	})
	if err != nil {
		return fmt.Errorf("encode pi bridge request: %w", err)
	}
	if _, err := conn.Write(append(request, '\n')); err != nil {
		return fmt.Errorf("write pi bridge request: %w", err)
	}
	line, err := bufio.NewReader(conn).ReadBytes('\n')
	if err != nil {
		return fmt.Errorf("read pi bridge response: %w", err)
	}
	var response piBridgeRPCResponse
	if err := json.Unmarshal(line, &response); err != nil {
		return fmt.Errorf("decode pi bridge response: %w", err)
	}
	if response.Error != nil {
		return fmt.Errorf(
			"pi bridge rpc error %d: %s",
			response.Error.Code,
			response.Error.Message,
		)
	}
	if out == nil || len(response.Result) == 0 {
		return nil
	}
	if err := json.Unmarshal(response.Result, out); err != nil {
		return fmt.Errorf("decode pi bridge result: %w", err)
	}
	return nil
}

func piBridgeRoot() (string, error) {
	if raw := strings.TrimSpace(os.Getenv("PAXL_PI_BRIDGE_DIR")); raw != "" {
		return raw, nil
	}
	root, err := piRoot()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "paxl-bridge"), nil
}

func closeConn(conn net.Conn) {
	if conn != nil {
		_ = conn.Close()
	}
}
