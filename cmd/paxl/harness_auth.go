package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/pax-oss/paxl/internal/facade"
	"github.com/pax-oss/paxl/internal/model"
	"github.com/urfave/cli/v3"
)

const harnessAuthLoginHelp = `Choose a harness; --method is optional. Login runs on this machine.

DEFAULTS:
  claude  subscription
  codex   device
  kimi    device
  pi      Choose a provider and its available method interactively
  opencode Choose a provider and its available method in the native CLI
  kiro    Use native browser/device detection
  hermes  Native provider default; --provider is required
  openclaw Choose a provider and method in the native CLI (TTY required)

METHODS:
  Claude:
    subscription  Sign in with a Claude subscription account (default).
    console       Sign in with an Anthropic Console account for API billing.

  Codex:
    device        Open the displayed URL and enter the code in your browser
                  (default; suitable for remote terminals).
    browser       Sign in through a browser with a localhost callback.
    api-key       Read an OpenAI API key from stdin; requires --secret-stdin.
    access-token  Read a Codex access token from stdin; requires --secret-stdin.

  Kimi:
    device        Sign in to Kimi Code using a browser and device code (default).
                  Requires a Kimi CLI version that supports kimi login.

  Pi:
    oauth         Provider subscription/OAuth login, when supported.
    api-key       Provider API key; explicit use requires --secret-stdin.
                  Run paxl harness auth methods --harness pi for current choices.

  OpenCode:
    Omit --method to choose subscription/OAuth or an API key using the native
    provider menu. --provider optionally skips provider selection.
    With --provider, --method accepts the exact native method label, not the
    generic Pi/Codex method names. Installed plugins own the available methods;
    an unknown label is reported by OpenCode with its supported choices.
    API keys use the native masked prompt; --secret-stdin is not supported.

  Kiro:
    browser       Run kiro-cli login (native environment detection).
    device        Run kiro-cli login --use-device-flow.
                  Subscription/account selection stays in the official flow.
                  API-key environments remain owned by KIRO_API_KEY.

  Hermes:
    oauth         Add a native provider OAuth credential.
    api-key       Add a credential through Hermes' secure prompt.
                  Requires --provider for login/status/logout. Omit --method
                  to use that provider's native default; unsupported provider
                  methods fail in Hermes. --secret-stdin is not supported.

  OpenClaw:
    Omit --method to choose interactively. With --provider, --method accepts a
    native plugin method ID. api-key instead saves a key through the native
    paste-api-key command and requires --provider plus --secret-stdin.
    Status lists saved profiles; logout requires --profile with one exact ID.
    Login/logout can update OpenClaw configuration through its own CLI.

EXAMPLES:
  paxl harness auth login --harness pi
  paxl harness auth login --harness pi --provider openai --method oauth
  paxl harness auth login --harness claude
  paxl harness auth login --harness claude --method console
  paxl harness auth login --harness codex
  paxl harness auth login --harness codex --method browser
  paxl harness auth login --harness kimi
  paxl harness auth login --harness opencode
  paxl harness auth login --harness opencode --provider openai
  paxl harness auth login --harness kiro --method device
  paxl harness auth login --harness hermes --provider openai-codex --method oauth
  paxl harness auth login --harness openclaw

  With OPENAI_API_KEY already set:
    printenv OPENAI_API_KEY | paxl harness auth login --harness codex --method api-key --secret-stdin

  With CODEX_ACCESS_TOKEN already set:
    printenv CODEX_ACCESS_TOKEN | paxl harness auth login --harness codex --method access-token --secret-stdin

  With OPENAI_API_KEY already set (OpenClaw):
    printenv OPENAI_API_KEY | paxl harness auth login --harness openclaw --provider openai --method api-key --secret-stdin

  OpenClaw profile removal (native confirmation is preserved):
    paxl harness auth status --harness openclaw
    paxl harness auth logout --harness openclaw --profile openai:manual

Complete browser authorization while this command is running. If prompted for
an authorization code, paste it into this same terminal. Device codes belong in
the browser. Press Ctrl-C to stop login. Native interaction is written to stderr;
--format json reserves stdout for the final result.

Methods are specific to each harness and apply only to login. Status and logout
support Claude, Codex, Pi, OpenCode, Kiro, Hermes, and OpenClaw. The additional
adapters report recognized credential/account metadata, not verified model access.
Credentials use the current shell environment;
existing API keys or cloud settings may affect which credentials the harness uses.`

func newLocalHarnessCommand(stdin io.Reader, stdout, stderr io.Writer) *cli.Command {
	return &cli.Command{
		Name:  "harness",
		Usage: "Manage local harness installations and authentication",
		Commands: []*cli.Command{
			newHarnessInstallCommand(stdin, stdout, stderr),
			newHarnessInstallationCommand("inspect", stdout),
			newHarnessInstallationCommand("upgrade", stdout),
			newHarnessInstallationCommand("rollback", stdout),
			newHarnessAuthCommand(stdin, stdout, stderr),
		},
	}
}

func newHarnessAuthCommand(stdin io.Reader, stdout, stderr io.Writer) *cli.Command {
	command := &cli.Command{
		Name:        "auth",
		Usage:       "Authenticate harnesses on this machine",
		Description: "Local native authentication; no paxd required. Usually only --harness is needed; Hermes requires --provider. Run paxl harness auth login -h for methods, defaults, required flags, and examples.",
	}
	for _, operation := range []string{"login", "status", "logout", "methods"} {
		flags := harnessAuthFlags()
		if operation == "login" {
			flags = append(
				flags,
				&cli.StringFlag{
					Name:  "method",
					Usage: "Login method (optional); see DESCRIPTION for choices and harness defaults",
				},
				&cli.BoolFlag{
					Name:  "secret-stdin",
					Usage: "Read one secret line for Codex, Pi, or OpenClaw API-key login (also Codex access-token)",
				},
			)
		}
		description := ""
		if operation == "login" {
			description = harnessAuthLoginHelp
		}
		command.Commands = append(command.Commands, &cli.Command{
			Description: description,
			Name:        operation,
			Usage:       map[string]string{"login": "Run official login locally", "status": "Read native authentication status", "logout": "Remove native credentials", "methods": "List installed Pi providers and login methods"}[operation],
			Flags:       flags,
			Action: func(ctx context.Context, cmd *cli.Command) error {
				return runLocalHarnessAuth(ctx, cmd, stdin, stdout, stderr)
			},
		})
	}
	return command
}

func harnessAuthFlags() []cli.Flag {
	return []cli.Flag{
		&cli.StringFlag{
			Name:     "harness",
			Required: true,
			Usage:    "Harness: claude (alias: claude-code), codex, kimi, pi, opencode, kiro (alias: kiro-cli), hermes, or openclaw",
		},
		&cli.StringFlag{
			Name:  "provider",
			Usage: "Pi/OpenCode/OpenClaw provider ID (optional for login); required for Hermes",
		},
		&cli.StringFlag{
			Name:  "profile",
			Usage: "OpenClaw logout only: exact saved profile ID from auth status",
		},
		&cli.StringFlag{Name: "format", Value: "text", Usage: "Output format: text or json"},
		&cli.BoolFlag{Name: "verbose", Usage: "Print operation diagnostics"},
	}
}

func harnessAuthVerbose(cmd *cli.Command, stderr io.Writer) func(*facade.Option) {
	if cmd.Bool("verbose") {
		return facade.WithVerboseWriter(stderr)
	}
	return facade.WithVerboseWriter(nil)
}

func runLocalHarnessAuth(
	ctx context.Context,
	cmd *cli.Command,
	stdin io.Reader,
	stdout, stderr io.Writer,
) error {
	req, err := parseLocalHarnessAuthRequest(cmd)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()
	result, err := facade.NewHarnessAuthFacade().
		Execute(ctx, req, facade.WithStreams(stdin, stdout, stderr), harnessAuthVerbose(cmd, stderr))
	if err != nil {
		return fmt.Errorf("%s harness authentication: %w", cmd.Name, err)
	}
	return renderLocalHarnessAuth(stdout, result, cmd.String("format"))
}

func parseLocalHarnessAuthRequest(cmd *cli.Command) (*facade.HarnessAuthRequest, error) {
	if cmd.Args().Len() != 0 {
		return nil, fmt.Errorf("unexpected positional arguments; use --harness claude")
	}
	if format := cmd.String("format"); format != "text" && format != "json" {
		return nil, fmt.Errorf("unsupported output format %q", format)
	}
	harness, err := model.ParseLocalAuthHarness(cmd.String("harness"))
	if err != nil {
		return nil, err
	}
	method, err := model.ParseHarnessAuthMethod(cmd.String("method"), harness)
	if err != nil {
		return nil, err
	}
	var operation model.HarnessAuthOperation
	switch cmd.Name {
	case "login":
		operation = model.HarnessAuthLogin
	case "status":
		operation = model.HarnessAuthStatus
	case "methods":
		operation = model.HarnessAuthMethods
	case "logout":
		operation = model.HarnessAuthLogout
	default:
		return nil, fmt.Errorf("unsupported authentication operation")
	}
	return &facade.HarnessAuthRequest{
		Profile:     cmd.String("profile"),
		Harness:     harness,
		Provider:    cmd.String("provider"),
		Operation:   operation,
		Method:      method,
		SecretStdin: cmd.Bool("secret-stdin"),
	}, nil
}

func renderLocalHarnessAuth(out io.Writer, result *model.HarnessAuthResult, format string) error {
	if format == "json" {
		if err := json.NewEncoder(out).Encode(result); err != nil {
			return fmt.Errorf("render local authentication JSON: %w", err)
		}
		return nil
	}
	if _, err := fmt.Fprintf(
		out,
		"Harness: %s\nState: %s\n",
		result.Harness,
		result.State,
	); err != nil {
		return fmt.Errorf("render local authentication: %w", err)
	}
	if result.Provider != "" {
		if _, err := fmt.Fprintf(out, "Provider: %s\n", result.Provider); err != nil {
			return err
		}
	}
	if result.RuntimeVersion != "" {
		if _, err := fmt.Fprintf(out, "Pi version: %s\n", result.RuntimeVersion); err != nil {
			return err
		}
	}
	if err := renderAuthProviders(out, result); err != nil {
		return err
	}
	if result.Message != "" {
		_, err := fmt.Fprintln(out, result.Message)
		return err
	}
	return nil
}

// Group discovery output by method combination; credential status stays per provider.
func renderAuthProviders(out io.Writer, result *model.HarnessAuthResult) error {
	if result.State != model.HarnessAuthAvailable {
		for _, provider := range result.Providers {
			label := provider.ID
			if provider.ProfileID != "" {
				label += " (" + provider.ProfileID + ")"
			}
			if _, err := fmt.Fprintf(
				out, "%s: %s\n", label, provider.CredentialType,
			); err != nil {
				return err
			}
		}
		return nil
	}
	groups := make(map[string][]string)
	var order []string
	for _, provider := range result.Providers {
		methods := make([]string, 0, len(provider.Methods))
		for _, method := range provider.Methods {
			methods = append(methods, string(method))
		}
		key := strings.Join(methods, " + ")
		if _, exists := groups[key]; !exists {
			order = append(order, key)
		}
		groups[key] = append(groups[key], provider.ID)
	}
	for _, key := range order {
		if _, err := fmt.Fprintf(out, "\n%s (%d providers):\n", key, len(groups[key])); err != nil {
			return err
		}
		if err := renderProviderNames(out, groups[key]); err != nil {
			return err
		}
	}
	return nil
}

func renderProviderNames(out io.Writer, names []string) error {
	line := "  "
	for _, name := range names {
		if len(line) > 2 && len(line)+len(name)+2 > 80 {
			if _, err := fmt.Fprintln(out, line); err != nil {
				return err
			}
			line = "  "
		}
		if len(line) > 2 {
			line += ", "
		}
		line += name
	}
	_, err := fmt.Fprintln(out, line)
	return err
}
