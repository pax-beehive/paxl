package adaptor

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"
)

type acpClient struct {
	command []string
	timeout time.Duration
}

type acpRPCMessage struct {
	ID     int64           `json:"id,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *acpRPCError    `json:"error,omitempty"`
}

type acpRPCError struct {
	Code    int64  `json:"code,omitempty"`
	Message string `json:"message,omitempty"`
}

type acpInitializeResult struct {
	AuthMethods []acpAuthMethod `json:"authMethods"`
}

type acpAuthMethod struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Type string `json:"type"`
	Vars []struct {
		Name string `json:"name"`
	} `json:"vars"`
}

type acpSessionListResult struct {
	Sessions []json.RawMessage `json:"sessions"`
}

func (c *acpClient) initialize(ctx context.Context) error {
	return c.withSession(ctx, func(
		sessionCtx context.Context,
		stdin io.Writer,
		reader *bufio.Reader,
		stderr *bytes.Buffer,
	) error {
		_, err := acpInitialize(sessionCtx, stdin, reader, stderr)
		return err
	})
}

func (c *acpClient) listSessions(ctx context.Context) ([]hermesSessionInfo, error) {
	var sessions []hermesSessionInfo
	err := c.withSession(ctx, func(
		sessionCtx context.Context,
		stdin io.Writer,
		reader *bufio.Reader,
		stderr *bytes.Buffer,
	) error {
		nextID, err := acpInitialize(sessionCtx, stdin, reader, stderr)
		if err != nil {
			return err
		}
		result, err := acpCall[acpSessionListResult](
			sessionCtx,
			stdin,
			reader,
			nextID,
			"session/list",
			map[string]any{},
		)
		if err != nil {
			return acpWithStderr("session/list", err, stderr.String())
		}
		for _, raw := range result.Sessions {
			session := decodeHermesACPSession(raw)
			if session.SessionID != "" {
				sessions = append(sessions, session)
			}
		}
		return nil
	})
	return sessions, err
}

func (c *acpClient) prompt(ctx context.Context, sessionID string, text string) error {
	return c.withSession(ctx, func(
		sessionCtx context.Context,
		stdin io.Writer,
		reader *bufio.Reader,
		stderr *bytes.Buffer,
	) error {
		nextID, err := acpInitialize(sessionCtx, stdin, reader, stderr)
		if err != nil {
			return err
		}
		_, err = acpCall[map[string]any](
			sessionCtx,
			stdin,
			reader,
			nextID,
			"session/prompt",
			map[string]any{
				"sessionId": sessionID,
				"prompt": []map[string]string{
					{"type": "text", "text": text},
				},
			},
		)
		if err != nil {
			return acpWithStderr("session/prompt", err, stderr.String())
		}
		return nil
	})
}

func (c *acpClient) withSession(
	ctx context.Context,
	fn func(context.Context, io.Writer, *bufio.Reader, *bytes.Buffer) error,
) error {
	if len(c.command) == 0 {
		return fmt.Errorf("acp command is required")
	}
	timeout := c.timeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	sessionCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(sessionCtx, c.command[0], c.command[1:]...) // #nosec G204
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		return fmt.Errorf("stdout pipe: %w", err)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		_ = stdin.Close()
		_ = stdout.Close()
		return fmt.Errorf("start acp command: %w", err)
	}
	defer func() {
		_ = stdin.Close()
		_ = stdout.Close()
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		_ = cmd.Wait()
	}()
	return fn(sessionCtx, stdin, bufio.NewReader(stdout), &stderr)
}

func acpInitialize(
	ctx context.Context,
	stdin io.Writer,
	reader *bufio.Reader,
	stderr *bytes.Buffer,
) (int64, error) {
	nextID := int64(1)
	result, err := acpCall[acpInitializeResult](
		ctx,
		stdin,
		reader,
		nextID,
		"initialize",
		map[string]any{
			"protocolVersion":    1,
			"clientCapabilities": map[string]any{},
			"clientInfo": map[string]any{
				"name":    "paxl",
				"version": "0.1.0",
			},
		},
	)
	if err != nil {
		return nextID, acpWithStderr("initialize", err, stderr.String())
	}
	nextID++
	if methodID := acpFirstNonTerminalAuthMethod(result.AuthMethods); methodID != "" {
		if _, err := acpCall[map[string]any](
			ctx,
			stdin,
			reader,
			nextID,
			"authenticate",
			map[string]any{"methodId": methodID},
		); err != nil {
			return nextID, acpWithStderr("authenticate", err, stderr.String())
		}
		nextID++
	}
	return nextID, nil
}

func acpCall[T any](
	ctx context.Context,
	stdin io.Writer,
	reader *bufio.Reader,
	id int64,
	method string,
	params any,
) (T, error) {
	var zero T
	if err := acpWriteRequest(stdin, id, method, params); err != nil {
		return zero, err
	}
	return acpReadResponse[T](ctx, reader, id)
}

func acpWriteRequest(stdin io.Writer, id int64, method string, params any) error {
	payload, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"method":  method,
		"params":  params,
	})
	if err != nil {
		return fmt.Errorf("encode request: %w", err)
	}
	if _, err := stdin.Write(append(payload, '\n')); err != nil {
		return fmt.Errorf("write request: %w", err)
	}
	return nil
}

func acpReadResponse[T any](
	ctx context.Context,
	reader *bufio.Reader,
	id int64,
) (T, error) {
	var zero T
	for {
		select {
		case <-ctx.Done():
			return zero, ctx.Err()
		default:
		}
		line, err := reader.ReadBytes('\n')
		if err != nil {
			if ctx.Err() != nil {
				return zero, ctx.Err()
			}
			return zero, fmt.Errorf("read response: %w", err)
		}
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		var message acpRPCMessage
		if err := json.Unmarshal(line, &message); err != nil {
			continue
		}
		if message.ID != id {
			continue
		}
		if message.Error != nil {
			return zero, fmt.Errorf("rpc error %d: %s", message.Error.Code, message.Error.Message)
		}
		var out T
		if len(message.Result) > 0 {
			if err := json.Unmarshal(message.Result, &out); err != nil {
				return zero, fmt.Errorf("decode result: %w", err)
			}
		}
		return out, nil
	}
}

func acpFirstNonTerminalAuthMethod(methods []acpAuthMethod) string {
	for _, method := range methods {
		if method.Type != "env_var" {
			continue
		}
		for _, envVar := range method.Vars {
			if envVar.Name != "" && os.Getenv(envVar.Name) != "" {
				return method.ID
			}
		}
	}
	for _, method := range methods {
		if method.ID != "" && !acpInteractiveAuthMethod(method) {
			return method.ID
		}
	}
	return ""
}

func acpInteractiveAuthMethod(method acpAuthMethod) bool {
	if method.Type == "terminal" || method.Type == "oauth" {
		return true
	}
	text := strings.ToLower(method.ID + " " + method.Name)
	return strings.Contains(text, "oauth") ||
		strings.Contains(text, "api-key") ||
		strings.Contains(text, "api key") ||
		strings.Contains(text, "gateway") ||
		strings.Contains(text, "vertex") ||
		strings.Contains(text, "setup") ||
		strings.Contains(text, "log in") ||
		strings.Contains(text, "login")
}

func acpWithStderr(operation string, err error, stderr string) error {
	stderr = strings.TrimSpace(stderr)
	if stderr == "" {
		return fmt.Errorf("%s: %w", operation, err)
	}
	const maxStderr = 512
	if len(stderr) > maxStderr {
		stderr = stderr[len(stderr)-maxStderr:]
	}
	return fmt.Errorf("%s: %w: %s", operation, err, stderr)
}
