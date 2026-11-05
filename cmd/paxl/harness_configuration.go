package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"

	"github.com/pax-oss/paxl/internal/facade"
	"github.com/pax-oss/paxl/internal/model"
	"github.com/urfave/cli/v3"
)

func newHarnessConfigurationCommand(stdout, stderr io.Writer) *cli.Command {
	return &cli.Command{
		Name:  "config",
		Usage: "Read native skills and MCP configuration without launching a harness",
		Commands: []*cli.Command{
			{
				Name:        "read",
				Usage:       "Read a versioned JSON inventory of native configuration",
				ArgsUsage:   "<codex|claude|pi|kiro|opencode|kimi|hermes|openclaw|dsh|dscode>",
				Description: "Reads local configuration files only. Does not start a harness, connect to MCP servers, or change native configuration. Missing sources and partial coverage are reported in JSON. Sensitive argument, environment and header values are redacted by default.",
				Flags: []cli.Flag{
					&cli.StringFlag{
						Name:  "cwd",
						Usage: "Working directory used for project configuration discovery (defaults to the current directory)",
					},
					&cli.StringFlag{
						Name:  "kind",
						Value: "all",
						Usage: "Configuration to read: all, skills, or mcp",
					},
					&cli.StringFlag{Name: "format", Value: "json", Usage: "Output format: json"},
					&cli.BoolFlag{
						Name:  "include-content",
						Usage: "Include skill instruction bodies",
					},
					&cli.BoolFlag{
						Name:  "include-sensitive",
						Usage: "Include literal argument, environment, header and native configuration values",
					},
					&cli.BoolFlag{Name: "verbose", Usage: "Write read diagnostics to stderr"},
				},
				Action: func(ctx context.Context, cmd *cli.Command) error {
					return readHarnessConfiguration(ctx, cmd, stdout, stderr)
				},
			},
			{
				Name:  "schema",
				Usage: "Print the JSON Schema for configuration inventory version 1",
				Action: func(_ context.Context, cmd *cli.Command) error {
					if cmd.Args().Len() != 0 {
						return fmt.Errorf("usage: paxl harness config schema")
					}
					if _, err := stdout.Write(facade.AgentConfigurationSchema()); err != nil {
						return fmt.Errorf("write configuration schema: %w", err)
					}
					return nil
				},
			},
		},
	}
}

func readHarnessConfiguration(
	ctx context.Context,
	cmd *cli.Command,
	stdout, stderr io.Writer,
) error {
	req, err := parseReadHarnessConfiguration(cmd)
	if err != nil {
		return fmt.Errorf("parse configuration read request: %w", err)
	}
	var verbose io.Writer
	if cmd.Bool("verbose") {
		verbose = stderr
	}
	result, err := facade.NewAgentConfigurationFacade().
		Read(ctx, req, facade.WithVerboseWriter(verbose))
	if err != nil {
		return fmt.Errorf("read harness configuration: %w", err)
	}
	if err := json.NewEncoder(stdout).Encode(result); err != nil {
		return fmt.Errorf("render configuration JSON: %w", err)
	}
	return nil
}

func parseReadHarnessConfiguration(
	cmd *cli.Command,
) (*facade.ReadAgentConfigurationRequest, error) {
	if cmd.Args().Len() != 1 {
		return nil, fmt.Errorf(
			"usage: paxl harness config read <agent> [--cwd path] [--kind all|skills|mcp]",
		)
	}
	if cmd.String("format") != "json" {
		return nil, fmt.Errorf(
			"unsupported configuration output format %q; supported: json",
			cmd.String("format"),
		)
	}
	agent, err := model.ParseAgentName(cmd.Args().First())
	if err != nil {
		return nil, fmt.Errorf("parse configuration agent: %w", err)
	}
	kind, err := model.ParseAgentConfigurationKind(cmd.String("kind"))
	if err != nil {
		return nil, err
	}
	return &facade.ReadAgentConfigurationRequest{
		Agent: agent, CWD: cmd.String("cwd"), Kind: kind,
		IncludeContent: cmd.Bool(
			"include-content",
		), IncludeSensitive: cmd.Bool("include-sensitive"),
	}, nil
}
