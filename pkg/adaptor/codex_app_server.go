package adaptor

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"strings"
)

const (
	codexAppServerTurnDeliveryMethod  = "app_server_turn"
	codexAppServerSteerDeliveryMethod = "app_server_steer"
)

type codexAppServerRequest struct {
	JSONRPC string      `json:"jsonrpc"`
	ID      int         `json:"id,omitempty"`
	Method  string      `json:"method"`
	Params  interface{} `json:"params,omitempty"`
}

type codexAppServerResponse struct {
	ID     int                  `json:"id"`
	Error  *codexAppServerError `json:"error"`
	Method string               `json:"method"`
	Result json.RawMessage      `json:"result"`
}

type codexAppServerError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func isCodexAppSession(nativeID string) bool {
	path, err := findCodexRollout(nativeID)
	if err != nil {
		return false
	}
	meta, err := readCodexMeta(path)
	if shouldSkipCodexMeta(meta, err) {
		return false
	}
	return isCodexAppMeta(meta)
}

func isCodexAppMeta(meta *codexMetaLine) bool {
	if meta == nil {
		return false
	}
	originator := strings.ToLower(meta.Payload.Originator)
	if strings.Contains(originator, "desktop") || strings.Contains(originator, "app") {
		return true
	}
	source := strings.ToLower(codexMetaSource(meta.Payload.Source))
	return strings.Contains(source, "vscode") ||
		strings.Contains(source, "desktop") ||
		strings.Contains(source, "app")
}

func codexMetaSource(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var value string
	if err := json.Unmarshal(raw, &value); err == nil {
		return value
	}
	return string(raw)
}

func promptCodexAppServer(
	ctx context.Context,
	req *PromptRequest,
	option *Option,
) (*PromptResponse, error) {
	command := exec.CommandContext(ctx, "codex", "app-server") // #nosec G204
	stdin, err := command.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("open app-server stdin: %w", err)
	}
	stdout, err := command.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("open app-server stdout: %w", err)
	}
	var stderr bytes.Buffer
	command.Stderr = &stderr
	if err := command.Start(); err != nil {
		return nil, fmt.Errorf("start codex app-server: %w", err)
	}
	client := &codexAppServerClient{
		stdin:   stdin,
		scanner: bufio.NewScanner(stdout),
		stderr:  &stderr,
	}
	client.scanner.Buffer(make([]byte, 0, 64*1024), scannerMaxTokenSize)
	deliveryMethod, err := client.deliver(req.NativeID, req.Text)
	if err != nil {
		_ = stdin.Close()
		_ = command.Wait()
		return nil, err
	}
	if err := stdin.Close(); err != nil {
		return nil, fmt.Errorf("close app-server stdin: %w", err)
	}
	if err := command.Wait(); err != nil {
		return nil, fmt.Errorf("run codex app-server: %w: %s", err, stderr.String())
	}
	writeCommandOutput(option, "stderr", stderr.String())
	return &PromptResponse{DeliveryMethod: deliveryMethod}, nil
}

type codexAppServerClient struct {
	stdin   io.Writer
	scanner *bufio.Scanner
	stderr  *bytes.Buffer
}

func (c *codexAppServerClient) deliver(threadID string, text string) (string, error) {
	if err := c.initialize(); err != nil {
		return "", err
	}
	resumeResult, err := c.resumeThread(threadID, 2)
	if err != nil {
		return "", err
	}
	if activeTurnID := activeCodexTurnID(resumeResult); activeTurnID != "" {
		steerErr := c.send(&codexAppServerRequest{
			JSONRPC: "2.0",
			ID:      3,
			Method:  "turn/steer",
			Params: map[string]interface{}{
				"threadId":            threadID,
				"expectedTurnId":      activeTurnID,
				"clientUserMessageId": nil,
				"input": []map[string]string{
					{"type": "text", "text": text},
				},
			},
		})
		if steerErr == nil {
			steerErr = c.waitForResponse(3)
		}
		if steerErr == nil {
			return codexAppServerSteerDeliveryMethod, nil
		}
		return c.startTurn(threadID, text, 4)
	}
	return c.startTurn(threadID, text, 3)
}

func (c *codexAppServerClient) initialize() error {
	if err := c.send(&codexAppServerRequest{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "initialize",
		Params: map[string]interface{}{
			"clientInfo": map[string]string{
				"name":    "paxl",
				"title":   "paxl",
				"version": "0.1.0",
			},
			"capabilities": map[string]bool{"experimentalApi": true},
		},
	}); err != nil {
		return err
	}
	if err := c.waitForResponse(1); err != nil {
		return err
	}
	return c.send(&codexAppServerRequest{
		JSONRPC: "2.0",
		Method:  "initialized",
		Params:  map[string]interface{}{},
	})
}

func (c *codexAppServerClient) resumeThread(
	threadID string,
	requestID int,
) (json.RawMessage, error) {
	if err := c.send(&codexAppServerRequest{
		JSONRPC: "2.0",
		ID:      requestID,
		Method:  "thread/resume",
		Params:  map[string]string{"threadId": threadID},
	}); err != nil {
		return nil, err
	}
	return c.waitForResponseResult(requestID)
}

func (c *codexAppServerClient) startTurn(
	threadID string,
	text string,
	requestID int,
) (string, error) {
	if err := c.send(&codexAppServerRequest{
		JSONRPC: "2.0",
		ID:      requestID,
		Method:  "turn/start",
		Params: map[string]interface{}{
			"threadId": threadID,
			"input": []map[string]string{
				{"type": "text", "text": text},
			},
		},
	}); err != nil {
		return "", err
	}
	return codexAppServerTurnDeliveryMethod, c.waitForTurnCompletion(requestID)
}

func activeCodexTurnID(raw json.RawMessage) string {
	var result struct {
		Thread struct {
			Turns []struct {
				ID     string `json:"id"`
				Status string `json:"status"`
			} `json:"turns"`
		} `json:"thread"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return ""
	}
	for index := len(result.Thread.Turns) - 1; index >= 0; index-- {
		turn := result.Thread.Turns[index]
		if turn.ID != "" && turn.Status == "inProgress" {
			return turn.ID
		}
	}
	return ""
}

func (c *codexAppServerClient) send(req *codexAppServerRequest) error {
	raw, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("marshal app-server request: %w", err)
	}
	if _, err := c.stdin.Write(append(raw, '\n')); err != nil {
		return fmt.Errorf("write app-server request: %w", err)
	}
	return nil
}

func (c *codexAppServerClient) waitForResponse(id int) error {
	_, err := c.waitForResponseResult(id)
	return err
}

func (c *codexAppServerClient) waitForResponseResult(id int) (json.RawMessage, error) {
	for c.scanner.Scan() {
		var resp codexAppServerResponse
		if err := json.Unmarshal(c.scanner.Bytes(), &resp); err != nil {
			return nil, fmt.Errorf("decode app-server response: %w", err)
		}
		if resp.ID != id {
			continue
		}
		if resp.Error != nil {
			return nil, fmt.Errorf(
				"app-server response %d failed: %d %s",
				id,
				resp.Error.Code,
				resp.Error.Message,
			)
		}
		return resp.Result, nil
	}
	if err := c.scanner.Err(); err != nil {
		return nil, fmt.Errorf("read app-server response: %w", err)
	}
	return nil, fmt.Errorf("app-server closed before response %d: %s", id, c.stderr.String())
}

func (c *codexAppServerClient) waitForTurnCompletion(id int) error {
	sawResponse := false
	sawCompletion := false
	for c.scanner.Scan() {
		var resp codexAppServerResponse
		if err := json.Unmarshal(c.scanner.Bytes(), &resp); err != nil {
			return fmt.Errorf("decode app-server turn event: %w", err)
		}
		if resp.ID == id {
			if resp.Error != nil {
				return fmt.Errorf(
					"app-server response %d failed: %d %s",
					id,
					resp.Error.Code,
					resp.Error.Message,
				)
			}
			sawResponse = true
		}
		if resp.Method == "turn/completed" {
			sawCompletion = true
		}
		if sawResponse && sawCompletion {
			return nil
		}
	}
	if err := c.scanner.Err(); err != nil {
		return fmt.Errorf("read app-server turn event: %w", err)
	}
	return fmt.Errorf("app-server closed before turn completion: %s", c.stderr.String())
}
