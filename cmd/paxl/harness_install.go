package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/pax-oss/paxl/internal/facade"
	"github.com/urfave/cli/v3"
)

func newHarnessInstallCommand(stdin io.Reader, stdout, stderr io.Writer) *cli.Command {
	return &cli.Command{
		Name:      "install",
		Usage:     "Install a harness using its official installer",
		ArgsUsage: "<harness>",
		Description: `SUPPORTED HARNESSES (official route):
  claude    Claude Code native installer
  codex     Codex standalone installer
  pi        Pi managed installer
  gemini    Official @google/gemini-cli npm package
  antigravity  Antigravity CLI official installer (command: agy)
  kimi      Kimi Code native installer
  opencode  OpenCode v1 official installer
  hermes    Official prebuilt runtime; checked source fallback on macOS Intel
  openclaw  OpenClaw installer (onboarding deferred)
  kiro      Kiro CLI installer (may open its app on macOS)
  dsh       Published @deepseek-ai/dsh npm package, installed persistently
  dscode    DSCODE standalone installer

EXAMPLES:
  paxl harness install claude
  paxl harness install antigravity
  paxl harness install pi --via official
  paxl harness install gemini --dry-run
  paxl harness install claude --component acp
  paxl harness install pi --component acp
  paxl harness install pi --via npm

The default route is official, the component is cli, and the version is the
publisher's current default release. Scripts are downloaded completely from
publisher URLs, then run locally with your environment and terminal input.
Installer interaction goes to stderr. Results default to readable text; use
--format json to reserve stdout for the structured final result.
The official installer owns paths, dependencies, migrations, and platform checks.
A script may change shell profiles; open a new terminal if needed after install.
Pi's npm route remains available. Other alternative routes are not implemented.
ACP package installation is supported separately for Claude, Codex, and Pi.
Use --dry-run to inspect the route without downloading or installing.
Exact --version is supported only where the installer has a verified option.
Official shell installers currently support macOS/Linux/WSL.
Hermes prefers a matching prebuilt package. On macOS Intel only, missing
prebuilt artifacts fall back to the official source installer after checking
Rust/Cargo >= 1.83, Apple command-line tools, and OpenSSL build prerequisites.`,
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "format", Value: "text", Usage: "Output format: text or json"},
			&cli.StringFlag{
				Name:  "via",
				Value: "official",
				Usage: "Installation route: official (default), npm (Pi only)",
			},
			&cli.StringFlag{Name: "component", Value: "cli", Usage: "Component: cli or acp"},
			&cli.StringFlag{Name: "version", Usage: "Package version (defaults to latest)"},
			&cli.BoolFlag{
				Name:  "dry-run",
				Usage: "Show the official install command without running it",
			},
			&cli.BoolFlag{Name: "verbose", Usage: "Print installation diagnostics"},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			if err := validateHarnessOutputFormat(cmd.String("format")); err != nil {
				return err
			}
			if cmd.Args().Len() != 1 {
				return fmt.Errorf(
					"usage: paxl harness install <harness> [--component acp] [--dry-run]; supported harnesses: %s; run paxl harness install -h for examples",
					facade.SupportedHarnessInstallNames(),
				)
			}
			component, err := facade.ParseHarnessComponent(cmd.String("component"))
			if err != nil {
				return err
			}
			via, err := facade.ParseHarnessInstallVia(cmd.String("via"))
			if err != nil {
				return err
			}
			ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
			defer stop()
			var verbose io.Writer
			if cmd.Bool("verbose") {
				verbose = stderr
			}
			result, err := facade.NewHarnessInstallFacade(nil).
				Install(ctx, &facade.HarnessInstallRequest{Harness: cmd.Args().First(), Via: via, Component: component, Version: cmd.String("version"), DryRun: cmd.Bool("dry-run")}, facade.WithStreams(stdin, stdout, stderr), facade.WithVerboseWriter(verbose))
			if err != nil {
				return err
			}
			return renderHarnessOutput(stdout, result, cmd.String("format"), "install")
		},
	}
}
