package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/pax-oss/paxl/internal/facade"
	"github.com/urfave/cli/v3"
)

func newHarnessInstallationCommand(action string, stdout io.Writer) *cli.Command {
	flags := []cli.Flag{
		&cli.StringFlag{
			Name:  "component",
			Value: "cli",
			Usage: "Installation component: cli or acp",
		},
		&cli.StringFlag{
			Name:  "path",
			Usage: "Exact local launcher path (defaults to the component on PATH)",
		},
		&cli.StringFlag{Name: "format", Value: "json", Usage: "Output format: json"},
		&cli.BoolFlag{Name: "verbose", Usage: "Write progress to stderr"},
	}
	if action == "upgrade" {
		flags = append(
			flags,
			&cli.StringFlag{Name: "version", Usage: "Target package version (defaults to latest)"},
			&cli.BoolFlag{
				Name:  "dry-run",
				Usage: "Inspect without installing or changing the launcher",
			},
		)
	}
	if action == "rollback" {
		flags = append(
			flags,
			&cli.StringFlag{
				Name:     "rollback-id",
				Required: true,
				Usage:    "Identifier returned by upgrade",
			},
		)
	}
	return &cli.Command{
		Name:      action,
		Usage:     action + " a local Claude, Codex or Pi installation",
		ArgsUsage: "<claude|codex|pi>",
		Flags:     flags,
		Action: func(ctx context.Context, cmd *cli.Command) error {
			ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
			defer stop()
			if cmd.Args().Len() != 1 || cmd.String("format") != "json" {
				return fmt.Errorf("expected one harness name and --format json")
			}
			component, err := facade.ParseHarnessComponent(cmd.String("component"))
			if err != nil {
				return err
			}
			req := &facade.HarnessInspectRequest{
				Harness:   cmd.Args().First(),
				Component: component,
				Path:      cmd.String("path"),
			}
			var verbose io.Writer
			if cmd.Bool("verbose") {
				verbose = cmd.Root().ErrWriter
			}
			f := facade.NewHarnessUpgradeFacade()
			var result any
			switch action {
			case "inspect":
				result, err = f.Inspect(ctx, req, facade.WithVerboseWriter(verbose))
			case "upgrade":
				result, err = f.Upgrade(ctx, &facade.HarnessUpgradeRequest{
					HarnessInspectRequest: *req,
					Version:               cmd.String("version"),
					DryRun:                cmd.Bool("dry-run"),
				}, facade.WithVerboseWriter(verbose))
			case "rollback":
				result, err = f.Rollback(ctx, &facade.HarnessRollbackRequest{
					HarnessInspectRequest: *req,
					RollbackID:            cmd.String("rollback-id"),
				}, facade.WithVerboseWriter(verbose))
			}
			if err != nil {
				return fmt.Errorf("%s harness installation: %w", action, err)
			}
			if err := json.NewEncoder(stdout).Encode(result); err != nil {
				return fmt.Errorf("render harness installation: %w", err)
			}
			return nil
		},
	}
}
