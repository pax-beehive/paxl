package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/pax-oss/paxl/internal/facade"
	"github.com/pax-oss/paxl/internal/model"
	"github.com/urfave/cli/v3"
)

func newCronCommand(stdin io.Reader, stdout, stderr io.Writer) *cli.Command {
	commands := []*cli.Command{
		{Name: "create", Usage: "Create a scheduled task", Flags: cronConfigFlags()},
		{
			Name:      "update",
			Usage:     "Update supplied fields of a task",
			ArgsUsage: "ID",
			Flags: append(
				cronConfigFlags(),
				&cli.BoolFlag{Name: "reset-session", Usage: "Clear a reused session binding"},
			),
		},
		{Name: "list", Usage: "List tasks", Flags: []cli.Flag{&cli.StringFlag{Name: "agent"}}},
		{Name: "get", Usage: "Get a task", ArgsUsage: "ID"},
		{Name: "delete", Usage: "Delete a task, retaining run history", ArgsUsage: "ID"},
		{Name: "pause", Usage: "Pause future scheduled runs", ArgsUsage: "ID"},
		{Name: "resume", Usage: "Resume future scheduled runs", ArgsUsage: "ID"},
		{Name: "run", Usage: "Queue a run now, including for paused tasks", ArgsUsage: "ID"},
		{
			Name:      "runs",
			Usage:     "List recent runs",
			ArgsUsage: "[ID]",
			Flags:     []cli.Flag{&cli.IntFlag{Name: "limit", Value: 50}},
		},
		{Name: "status", Usage: "Check whether the scheduler is running"},
	}
	for _, command := range commands {
		command.Action = func(ctx context.Context, cmd *cli.Command) error {
			f := facade.NewCronFacade(facade.NewDaemonUnixClient(cmd.String("socket")))
			var opts []func(*facade.Option)
			if cmd.Bool("verbose") {
				opts = append(opts, facade.WithVerboseWriter(stderr))
			}
			result, err := executeCronCommand(ctx, cmd, stdin, f, opts...)
			if err != nil {
				return err
			}
			encoder := json.NewEncoder(stdout)
			encoder.SetIndent("", "  ")
			if err := encoder.Encode(result); err != nil {
				return fmt.Errorf("render cron result: %w", err)
			}
			return nil
		}
	}
	return &cli.Command{
		Name:  "cron",
		Usage: "Manage Pax scheduled tasks (JSON output; requires paxd)",
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "socket", Usage: "paxd Unix socket path"},
		},
		Commands: commands,
	}
}

func cronConfigFlags() []cli.Flag {
	return []cli.Flag{
		&cli.StringFlag{
			Name: "name",
		}, &cli.StringFlag{Name: "connection", Usage: "Registered paxd agent connection ID"},
		&cli.StringFlag{Name: "session-mode", Value: "new", Usage: "new or reuse"},
		&cli.StringFlag{Name: "schedule", Usage: "Five-field cron expression"},
		&cli.StringFlag{Name: "timezone", Value: "UTC", Usage: "IANA timezone"},
		&cli.StringFlag{Name: "cwd", Value: ".", Usage: "Working directory on this machine"},
		&cli.StringFlag{Name: "prompt-file", Usage: "Read prompt from a file, or - for stdin"},
		&cli.DurationFlag{Name: "timeout", Value: time.Hour},
		&cli.BoolFlag{Name: "disabled", Usage: "Create paused, or set false to enable on update"},
	}
}

func executeCronCommand(
	ctx context.Context,
	cmd *cli.Command,
	stdin io.Reader,
	f *facade.CronFacade,
	opts ...func(*facade.Option),
) (any, error) {
	if err := validateCronArgs(cmd); err != nil {
		return nil, err
	}
	id := cmd.Args().First()
	switch cmd.Name {
	case "create":
		req, err := parseCreateCron(cmd, stdin)
		if err != nil {
			return nil, err
		}
		return f.Create(ctx, req, opts...)
	case "update":
		req, err := parseUpdateCron(cmd, stdin)
		if err != nil {
			return nil, err
		}
		return f.Update(ctx, id, req, opts...)
	case "pause", "resume":
		enabled := cmd.Name == "resume"
		return f.Update(ctx, id, &facade.UpdateCronRequest{Enabled: &enabled}, opts...)
	case "list":
		agent := model.AgentNameUnknown
		if cmd.String("agent") != "" {
			var err error
			agent, err = model.ParseAgentName(cmd.String("agent"))
			if err != nil {
				return nil, err
			}
		}
		return f.List(ctx, agent, opts...)
	case "get":
		return f.Get(ctx, id, opts...)
	case "delete":
		return map[string]bool{"deleted": true}, f.Delete(ctx, id, opts...)
	case "run":
		return f.Run(ctx, id, opts...)
	case "runs":
		return f.Runs(ctx, id, cmd.Int("limit"), opts...)
	case "status":
		return f.Status(ctx, opts...)
	default:
		return nil, fmt.Errorf("unknown cron command %q", cmd.Name)
	}
}

func validateCronArgs(cmd *cli.Command) error {
	count := cmd.Args().Len()
	switch cmd.Name {
	case "create", "list", "status":
		if count != 0 {
			return fmt.Errorf("%s does not accept positional arguments", cmd.Name)
		}
	case "runs":
		if count > 1 || cmd.Int("limit") < 1 || cmd.Int("limit") > 200 {
			return fmt.Errorf("runs accepts one optional ID and a limit between 1 and 200")
		}
	default:
		if count != 1 {
			return fmt.Errorf("%s requires exactly one job ID", cmd.Name)
		}
	}
	return nil
}

func parseCreateCron(cmd *cli.Command, stdin io.Reader) (*facade.CreateCronRequest, error) {
	for _, flag := range []string{"name", "connection", "schedule", "prompt-file"} {
		if strings.TrimSpace(cmd.String(flag)) == "" {
			return nil, fmt.Errorf("--%s is required", flag)
		}
	}
	mode, err := model.ParseCronSessionMode(cmd.String("session-mode"))
	if err != nil {
		return nil, err
	}
	prompt, err := readCronPrompt(cmd.String("prompt-file"), stdin)
	if err != nil {
		return nil, err
	}
	dir, err := filepath.Abs(cmd.String("cwd"))
	if err != nil {
		return nil, fmt.Errorf("resolve working directory: %w", err)
	}
	seconds, err := cronTimeout(cmd)
	if err != nil {
		return nil, err
	}
	enabled := !cmd.Bool("disabled")
	return &facade.CreateCronRequest{
		Name:           cmd.String("name"),
		ConnectionID:   cmd.String("connection"),
		SessionMode:    mode,
		Prompt:         prompt,
		WorkingDir:     dir,
		Schedule:       cmd.String("schedule"),
		Timezone:       cmd.String("timezone"),
		Enabled:        &enabled,
		TimeoutSeconds: seconds,
	}, nil
}

func parseUpdateCron(cmd *cli.Command, stdin io.Reader) (*facade.UpdateCronRequest, error) {
	req := &facade.UpdateCronRequest{}
	changed := cmd.Bool("reset-session")
	req.ResetSession = changed
	for flag, target := range map[string]**string{"name": &req.Name, "schedule": &req.Schedule, "timezone": &req.Timezone, "connection": &req.ConnectionID} {
		if cmd.IsSet(flag) {
			value := cmd.String(flag)
			*target = &value
			changed = true
		}
	}
	if cmd.IsSet("session-mode") {
		mode, err := model.ParseCronSessionMode(cmd.String("session-mode"))
		if err != nil {
			return nil, err
		}
		req.SessionMode, changed = &mode, true
	}
	if cmd.IsSet("cwd") {
		dir, err := filepath.Abs(cmd.String("cwd"))
		if err != nil {
			return nil, fmt.Errorf("resolve working directory: %w", err)
		}
		req.WorkingDir, changed = &dir, true
	}
	if cmd.IsSet("prompt-file") {
		prompt, err := readCronPrompt(cmd.String("prompt-file"), stdin)
		if err != nil {
			return nil, err
		}
		req.Prompt, changed = &prompt, true
	}
	if cmd.IsSet("timeout") {
		seconds, err := cronTimeout(cmd)
		if err != nil {
			return nil, err
		}
		req.TimeoutSeconds, changed = &seconds, true
	}
	if cmd.IsSet("disabled") {
		enabled := !cmd.Bool("disabled")
		req.Enabled, changed = &enabled, true
	}
	if !changed {
		return nil, fmt.Errorf("update requires at least one field")
	}
	return req, nil
}

func cronTimeout(cmd *cli.Command) (int, error) {
	duration := cmd.Duration("timeout")
	if duration < time.Second || duration > 24*time.Hour || duration%time.Second != 0 {
		return 0, fmt.Errorf("timeout must be whole seconds between 1s and 24h")
	}
	return int(duration / time.Second), nil
}

func readCronPrompt(path string, stdin io.Reader) (string, error) {
	reader := stdin
	if path != "-" {
		file, err := os.Open(path) // #nosec G304 -- Explicit user-selected prompt file.
		if err != nil {
			return "", fmt.Errorf("open prompt file: %w", err)
		}
		defer func() { _ = file.Close() }()
		reader = file
	}
	data, err := io.ReadAll(io.LimitReader(reader, 128*1024+1))
	if err != nil {
		return "", fmt.Errorf("read prompt: %w", err)
	}
	if strings.TrimSpace(string(data)) == "" || len(data) > 128*1024 {
		return "", fmt.Errorf("prompt must contain between 1 and 131072 bytes")
	}
	return string(data), nil
}
