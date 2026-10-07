package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/pax-oss/paxl/internal/facade"
	"github.com/pax-oss/paxl/internal/model"
	"github.com/urfave/cli/v3"
)

var newHarnessAuthFacade = func(socket string) *facade.DaemonHarnessAuthFacade {
	return facade.NewDaemonHarnessAuthFacade(facade.NewDaemonUnixClient(socket))
}

func newHarnessAuthCommand(stdin io.Reader, stdout, stderr io.Writer) *cli.Command {
	return &cli.Command{
		Name:  "auth",
		Usage: "Check and restore harness authentication through paxd (Claude and Codex)",
		Commands: []*cli.Command{
			{
				Name:  "cancel",
				Usage: "Cancel the current owned login without signing out",
				Flags: harnessAuthFlags(),
				Action: func(ctx context.Context, cmd *cli.Command) error {
					harness, err := parseHarnessAuthArgs(cmd)
					if err != nil {
						return err
					}
					view, err := newHarnessAuthFacade(
						cmd.String("socket"),
					).Login(ctx, &facade.DaemonHarnessAuthLoginRequest{Harness: string(harness), Operation: model.DaemonHarnessAuthCancel}, harnessAuthVerbose(cmd, stderr))
					if err != nil {
						return err
					}
					return renderHarnessAuth(stdout, view, cmd.String("format"))
				},
			},
			{
				Name:  "status",
				Usage: "Check authentication and any pending login",
				Flags: harnessAuthFlags(),
				Action: func(ctx context.Context, cmd *cli.Command) error {
					harness, err := parseHarnessAuthArgs(cmd)
					if err != nil {
						return err
					}
					ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
					defer cancel()
					view, err := newHarnessAuthFacade(
						cmd.String("socket"),
					).Status(ctx, &facade.DaemonHarnessAuthStatusRequest{Harness: string(harness)}, harnessAuthVerbose(cmd, stderr))
					if err != nil {
						return fmt.Errorf("check harness authentication: %w", err)
					}
					return renderHarnessAuth(stdout, view, cmd.String("format"))
				},
			},
			{
				Name:  "login",
				Usage: "Start or resume login; use --code-stdin to submit an authorization code",
				Flags: append(
					harnessAuthFlags(),
					&cli.StringFlag{
						Name:  "method",
						Usage: "Claude: subscription or console; Codex: device, api-key, or access-token",
					},
					&cli.BoolFlag{
						Name:  "secret-stdin",
						Usage: "Read a Codex API key or access token from stdin",
					},
					&cli.BoolFlag{
						Name:  "code-stdin",
						Usage: "Read one authorization code line from stdin and submit it to the pending login",
					},
				),
				Action: func(ctx context.Context, cmd *cli.Command) error {
					harness, err := parseHarnessAuthArgs(cmd)
					if err != nil {
						return err
					}
					if err := model.ValidateDaemonAuthMethod(
						harness,
						cmd.String("method"),
						cmd.Bool("secret-stdin"),
						cmd.Bool("code-stdin"),
					); err != nil {
						return err
					}
					req := &facade.DaemonHarnessAuthLoginRequest{
						Harness:   string(harness),
						Method:    cmd.String("method"),
						Operation: model.DaemonHarnessAuthStart,
						Wait:      true,
					}
					if cmd.Bool("code-stdin") || cmd.Bool("secret-stdin") {
						if cmd.Bool("code-stdin") {
							req.Operation = model.DaemonHarnessAuthSubmit
						}
						req.Code, err = readHarnessAuthCode(stdin)
						if err != nil {
							return fmt.Errorf("read login input: %w", err)
						}
					}
					view, loginErr := newHarnessAuthFacade(
						cmd.String("socket"),
					).Login(ctx, req, harnessAuthVerbose(cmd, stderr))
					if view != nil {
						if err := renderHarnessAuth(
							stdout,
							view,
							cmd.String("format"),
						); err != nil {
							return err
						}
					}
					if loginErr != nil {
						return fmt.Errorf("log in harness: %w", loginErr)
					}
					return nil
				},
			},
		},
	}
}

func harnessAuthFlags() []cli.Flag {
	return []cli.Flag{
		&cli.StringFlag{
			Name:     "harness",
			Required: true,
			Usage:    "Harness to authenticate: claude (alias: claude-code) or codex",
		},
		&cli.StringFlag{
			Name:  "socket",
			Usage: "Target paxd Unix socket; defaults to ~/.paxd/paxd.sock",
		},
		&cli.StringFlag{Name: "format", Value: "text", Usage: "Output format: text or json"},
		&cli.BoolFlag{Name: "verbose", Usage: "Print progress without authorization codes"},
	}
}

func parseHarnessAuthArgs(cmd *cli.Command) (model.AgentName, error) {
	if cmd.Args().Len() != 0 {
		return model.AgentNameUnknown, fmt.Errorf(
			"unexpected positional arguments; use --harness claude",
		)
	}
	if format := cmd.String("format"); format != "text" && format != "json" {
		return model.AgentNameUnknown, fmt.Errorf("unsupported output format %q", format)
	}
	harness, err := model.ParseDaemonAuthHarness(cmd.String("harness"))
	if err != nil {
		return model.AgentNameUnknown, fmt.Errorf("parse harness: %w", err)
	}
	return harness, nil
}

func harnessAuthVerbose(cmd *cli.Command, stderr io.Writer) func(*facade.Option) {
	if cmd.Bool("verbose") {
		return facade.WithVerboseWriter(stderr)
	}
	return facade.WithVerboseWriter(nil)
}

func readHarnessAuthCode(stdin io.Reader) (string, error) {
	if stdin == nil {
		return "", fmt.Errorf("stdin is required")
	}
	// Read one line, not until EOF: pasting a code and pressing Enter also works.
	line, err := bufio.NewReader(io.LimitReader(stdin, 4098)).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", fmt.Errorf("read stdin: %w", err)
	}
	code := strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r")
	if code == "" || len(code) > 4096 || strings.TrimSpace(code) != code ||
		strings.ContainsAny(code, "\r\n\x00") {
		return "", fmt.Errorf(
			"expected one non-empty authorization code line of at most 4096 bytes",
		)
	}
	return code, nil
}

// Session handles stay in the daemon protocol, not the public CLI contract.
type harnessAuthOutput struct {
	Harness          string                       `json:"harness"`
	State            model.DaemonHarnessAuthState `json:"state"`
	LoggedIn         *bool                        `json:"logged_in,omitempty"`
	AuthMethod       string                       `json:"auth_method,omitempty"`
	Method           string                       `json:"method,omitempty"`
	UserCode         string                       `json:"user_code,omitempty"`
	AuthorizationURL string                       `json:"authorization_url,omitempty"`
	ExpiresAt        string                       `json:"expires_at,omitempty"`
	ErrorCode        string                       `json:"error_code,omitempty"`
}

func renderHarnessAuth(stdout io.Writer, view *model.DaemonHarnessAuthView, format string) error {
	out := harnessAuthOutput{
		Harness:          view.Harness,
		State:            view.State,
		LoggedIn:         view.LoggedIn,
		AuthMethod:       view.AuthMethod,
		AuthorizationURL: view.AuthorizationURL,
		UserCode:         view.UserCode,
		Method:           view.Method,
		ExpiresAt:        view.ExpiresAt,
		ErrorCode:        view.ErrorCode,
	}
	if format == "json" {
		if err := json.NewEncoder(stdout).Encode(&out); err != nil {
			return fmt.Errorf("render authentication JSON: %w", err)
		}
		return nil
	}
	var text strings.Builder
	fmt.Fprintf(&text, "Harness: %s\nState: %s\n", out.Harness, out.State)
	if out.AuthMethod != "" {
		fmt.Fprintf(&text, "Authentication: %s\n", out.AuthMethod)
	}
	if out.ExpiresAt != "" {
		fmt.Fprintf(&text, "Login deadline: %s\n", out.ExpiresAt)
	}
	if out.AuthorizationURL != "" {
		fmt.Fprintf(&text, "\nOpen this URL to authorize:\n%s\n", out.AuthorizationURL)
		if out.UserCode != "" {
			fmt.Fprintf(
				&text,
				"Enter this one-time code in the browser: %s\nThen check paxl auth status --harness %s with the same --socket.\n",
				out.UserCode,
				out.Harness,
			)
		} else {
			fmt.Fprintf(
				&text,
				"\nThen run paxl auth login --harness %s --code-stdin with the same --socket, if set, and paste the full authorization code followed by Enter.\n",
				out.Harness,
			)
		}
	}
	if out.ErrorCode != "" {
		fmt.Fprintf(&text, "Error: %s\n", out.ErrorCode)
	}
	if _, err := io.WriteString(stdout, text.String()); err != nil {
		return fmt.Errorf("render authentication status: %w", err)
	}
	return nil
}
