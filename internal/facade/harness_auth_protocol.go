package facade

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"

	"github.com/pax-oss/paxl/internal/model"
)

type authProtocol struct {
	mu      sync.Mutex
	writer  io.Writer
	harness model.AgentName
	prompt  string
	kind    string
	secrets []string
	pending []byte
	failure error
}

func (p *authProtocol) emit(event *model.HarnessAuthEvent) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.emitLocked(event)
}
func (p *authProtocol) emitLocked(event *model.HarnessAuthEvent) error {
	if p.failure != nil {
		return p.failure
	}
	copyEvent := *event
	copyEvent.Version = 1
	copyEvent.Message = p.redact(copyEvent.Message)
	copyEvent.URL = p.redact(copyEvent.URL)
	copyEvent.Code = p.redact(copyEvent.Code)
	if event.Type == "prompt" {
		p.prompt = event.ID
		p.kind = event.Kind
	}
	if err := json.NewEncoder(p.writer).Encode(&copyEvent); err != nil {
		p.failure = fmt.Errorf("write auth event: %w", err)
		return p.failure
	}
	return nil
}
func (p *authProtocol) redact(text string) string {
	for _, secret := range p.secrets {
		if secret != "" {
			text = strings.ReplaceAll(text, secret, "[REDACTED]")
		}
	}
	return text
}
func (p *authProtocol) Write(data []byte) (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	n := len(data)
	p.pending = append(p.pending, data...)
	if len(p.pending) > 32*1024 {
		p.pending = nil
		return n, p.emitLocked(
			&model.HarnessAuthEvent{
				Type:    "message",
				Message: "Native diagnostic exceeded the display limit.",
			},
		)
	}
	for {
		index := bytes.IndexByte(p.pending, '\n')
		if index < 0 {
			return n, nil
		}
		line := string(p.pending[:index])
		p.pending = p.pending[index+1:]
		emitErr := p.emitLocked(&model.HarnessAuthEvent{Type: "message", Message: line})
		if emitErr != nil {
			return n, emitErr
		}
	}
}
func (p *authProtocol) accept(line []byte) (string, error) {
	var input struct {
		Version int    `json:"version"`
		Type    string `json:"type"`
		ID      string `json:"id"`
		Value   string `json:"value"`
	}
	if err := json.Unmarshal(line, &input); err != nil {
		return "", fmt.Errorf("invalid auth input frame")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if input.Version != 1 || input.Type != "input" || input.ID != p.prompt || input.ID == "" {
		return "", fmt.Errorf("auth input does not match the current prompt")
	}
	if input.Value == "" || len(input.Value) > 4096 ||
		strings.ContainsAny(input.Value, "\r\n\x00") {
		return "", fmt.Errorf("auth input must be one nonempty line of at most 4096 bytes")
	}
	// Treat every submitted value as confidential, including authorization codes.
	if len(p.secrets) >= 64 {
		return "", fmt.Errorf("too many auth inputs")
	}
	if p.kind != "select" {
		p.secrets = append(p.secrets, input.Value)
	}
	if p.prompt != "native" {
		p.prompt = ""
	}
	return input.Value + "\n", nil
}
func (p *authProtocol) forward(input io.Reader, output *os.File, cancel context.CancelFunc) {
	defer func() { _ = output.Close() }()
	scanner := bufio.NewScanner(input)
	scanner.Buffer(make([]byte, 4096), 32*1024)
	for scanner.Scan() {
		value, err := p.accept(scanner.Bytes())
		if err == nil {
			_, err = io.WriteString(output, value)
		}
		if err != nil {
			p.fail(err)
			cancel()
			return
		}
	}
	if err := scanner.Err(); err != nil {
		p.fail(fmt.Errorf("read auth input: %w", err))
		cancel()
	}
}
func (p *authProtocol) fail(err error) { p.mu.Lock(); defer p.mu.Unlock(); p.failure = err }
func (p *authProtocol) finish(result *model.HarnessAuthResult, err error) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.failure != nil {
		return p.failure
	}
	if len(p.pending) > 0 {
		emitErr := p.emitLocked(
			&model.HarnessAuthEvent{Type: "message", Message: string(p.pending)},
		)
		if emitErr != nil {
			return emitErr
		}
		p.pending = nil
	}
	if err != nil {
		result = &model.HarnessAuthResult{
			Harness: p.harness,
			State:   model.HarnessAuthFailed,
			Message: p.redact(err.Error()),
		}
	}
	writeErr := p.emitLocked(&model.HarnessAuthEvent{Type: "result", Result: result})
	if writeErr != nil {
		return writeErr
	}
	return err
}

// ExecuteProtocol bridges one local invocation to versioned JSONL events/input.
// The daemon owns the invocation; losing a browser connection must not cancel it.
func (f *HarnessAuthFacade) ExecuteProtocol(
	ctx context.Context,
	req *HarnessAuthRequest,
	opts ...func(*Option),
) error {
	if err := validateLocalAuth(req); err != nil {
		return err
	}
	if req.Harness == model.AgentNameAntigravity &&
		(req.Operation != model.HarnessAuthLogin || (req.Method != model.HarnessAuthAPIKey && req.Method != model.HarnessAuthADC)) {
		return fmt.Errorf("AGY account UI requires local terminal or the separate PTY transport")
	}
	option := applyOptions(opts)
	if option.Stdin == nil || option.Stdout == nil {
		return fmt.Errorf("auth protocol requires stdin and stdout")
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	p := &authProtocol{writer: option.Stdout, harness: req.Harness}
	read, write, err := os.Pipe()
	if err != nil {
		return fmt.Errorf("open auth input pipe: %w", err)
	}
	defer func() { _ = read.Close() }()
	defer func() { _ = write.Close() }()
	prompt := &model.HarnessAuthEvent{
		Type:    "prompt",
		ID:      "native",
		Kind:    "secret",
		Message: "If the native login requests an authorization code, enter it here.",
	}
	if req.SecretStdin {
		prompt.ID = "initial-secret"
		prompt.Message = "Enter the API key or access token for this native login."
	}
	if req.Operation == model.HarnessAuthLogin || req.Operation == model.HarnessAuthLogout {
		//exhaustive:ignore
		switch req.Harness {
		case model.AgentNamePi, model.AgentNameGemini, model.AgentNameDSH, model.AgentNameDSCode:
			if req.SecretStdin {
				if err = p.emit(prompt); err != nil {
					return err
				}
			}
		case model.AgentNameCodex, model.AgentNameKimi, model.AgentNameKiro:
			if req.SecretStdin {
				if err = p.emit(prompt); err != nil {
					return err
				}
			}
		default:
			if err = p.emit(prompt); err != nil {
				return err
			}
		}
	}
	go p.forward(option.Stdin, write, cancel)
	result, runErr := f.Execute(
		ctx,
		req,
		WithStreams(read, option.Stdout, p),
		WithAuthEvents(p.emit),
	)
	return p.finish(result, runErr)
}
