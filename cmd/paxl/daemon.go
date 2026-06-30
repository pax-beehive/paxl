package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/pax-oss/paxl/internal/facade"
	"github.com/pax-oss/paxl/internal/model"
	"github.com/urfave/cli/v3"
)

var newDaemonFacade = func() *facade.DaemonFacade {
	return facade.NewDaemonFacade(nil)
}

var newDaemonLifecycleFacade = func() *facade.DaemonLifecycleFacade {
	return facade.NewDaemonLifecycleFacade(nil)
}

func newDaemonCommand(stdout io.Writer) *cli.Command {
	return &cli.Command{
		Name:  "daemon",
		Usage: "Control a local paxd daemon",
		Commands: []*cli.Command{
			newDaemonInstallCommand(stdout),
			newDaemonUpdateCommand(stdout),
			newDaemonSetupCommand(stdout),
			newDaemonServiceCommand(stdout),
			newDaemonStatusCommand(stdout),
			newDaemonRemoteCommand(stdout),
			newDaemonAgentCommand(stdout),
			newDaemonHarnessCommand(stdout),
			newDaemonLocalCommand(stdout),
		},
	}
}

func newDaemonUpdateCommand(stdout io.Writer) *cli.Command {
	return &cli.Command{
		Name:  "update",
		Usage: "Update the paxd daemon binary",
		Flags: []cli.Flag{
			&cli.BoolFlag{Name: "dry-run", Usage: "Show update actions without writing files"},
			&cli.StringFlag{Name: "resolver-url", Value: facade.DefaultDaemonResolverURL, Usage: "Artifact resolver URL"},
			&cli.StringFlag{Name: "platform", Usage: "Release platform override like darwin/arm64"},
			&cli.StringFlag{Name: "tag", Value: facade.DefaultUpdateTag, Usage: "Release tag to install"},
			&cli.StringFlag{Name: "install-dir", Usage: "Directory to install paxd into"},
			&cli.StringFlag{Name: "format", Value: "text", Usage: "Output format: text or json"},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			resp, err := newDaemonLifecycleFacade().Update(ctx, &facade.DaemonUpdateRequest{
				DryRun:      cmd.Bool("dry-run"),
				ResolverURL: cmd.String("resolver-url"),
				Platform:    cmd.String("platform"),
				Tag:         cmd.String("tag"),
				InstallDir:  cmd.String("install-dir"),
			})
			if err != nil {
				return fmt.Errorf("update daemon: %w", err)
			}
			return renderDaemonLifecycle(stdout, resp, cmd.String("format"))
		},
	}
}

func newDaemonInstallCommand(stdout io.Writer) *cli.Command {
	return &cli.Command{
		Name:  "install",
		Usage: "Install the paxd daemon binary",
		Flags: []cli.Flag{
			&cli.BoolFlag{Name: "dry-run", Usage: "Show install actions without writing files"},
			&cli.StringFlag{Name: "resolver-url", Value: facade.DefaultDaemonResolverURL, Usage: "Artifact resolver URL"},
			&cli.StringFlag{Name: "platform", Usage: "Release platform override like darwin/arm64"},
			&cli.StringFlag{Name: "tag", Value: facade.DefaultUpdateTag, Usage: "Release tag to install"},
			&cli.StringFlag{Name: "install-dir", Usage: "Directory to install paxd into"},
			&cli.StringFlag{Name: "format", Value: "text", Usage: "Output format: text or json"},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			resp, err := newDaemonLifecycleFacade().Install(ctx, &facade.DaemonInstallRequest{
				DryRun:      cmd.Bool("dry-run"),
				ResolverURL: cmd.String("resolver-url"),
				Platform:    cmd.String("platform"),
				Tag:         cmd.String("tag"),
				InstallDir:  cmd.String("install-dir"),
			})
			if err != nil {
				return fmt.Errorf("install daemon: %w", err)
			}
			return renderDaemonLifecycle(stdout, resp, cmd.String("format"))
		},
	}
}

func newDaemonSetupCommand(stdout io.Writer) *cli.Command {
	return &cli.Command{
		Name:  "setup",
		Usage: "Set up paxd pairing and background service",
		Flags: []cli.Flag{
			&cli.BoolFlag{Name: "dry-run", Usage: "Show setup actions without writing files"},
			&cli.StringFlag{Name: "cloud-url", Usage: "Pax cloud API URL"},
			&cli.StringFlag{Name: "resolver-url", Value: facade.DefaultDaemonResolverURL, Usage: "Artifact resolver URL used when paxd is missing"},
			&cli.StringFlag{Name: "platform", Usage: "Release platform override like darwin/arm64"},
			&cli.StringFlag{Name: "tag", Value: facade.DefaultUpdateTag, Usage: "Release tag to install when paxd is missing"},
			&cli.StringFlag{Name: "install-dir", Usage: "Directory to install paxd into when missing"},
			&cli.StringFlag{Name: "format", Value: "text", Usage: "Output format: text or json"},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			resp, err := newDaemonLifecycleFacade().Setup(ctx, &facade.DaemonSetupRequest{
				DryRun:      cmd.Bool("dry-run"),
				CloudURL:    cmd.String("cloud-url"),
				ResolverURL: cmd.String("resolver-url"),
				Platform:    cmd.String("platform"),
				Tag:         cmd.String("tag"),
				InstallDir:  cmd.String("install-dir"),
			})
			if err != nil {
				return fmt.Errorf("set up daemon: %w", err)
			}
			return renderDaemonLifecycle(stdout, resp, cmd.String("format"))
		},
	}
}

func newDaemonServiceCommand(stdout io.Writer) *cli.Command {
	actions := []string{"status", "start", "stop", "restart"}
	commands := make([]*cli.Command, 0, len(actions))
	for _, action := range actions {
		action := action
		commands = append(commands, &cli.Command{
			Name:  action,
			Usage: strings.Title(action) + " the paxd background service",
			Flags: []cli.Flag{
				&cli.BoolFlag{Name: "dry-run", Usage: "Show service action without running paxd"},
				&cli.StringFlag{Name: "format", Value: "text", Usage: "Output format: text or json"},
			},
			Action: func(ctx context.Context, cmd *cli.Command) error {
				resp, err := newDaemonLifecycleFacade().Service(ctx, &facade.DaemonServiceRequest{
					Action: action,
					DryRun: cmd.Bool("dry-run"),
				})
				if err != nil {
					return fmt.Errorf("%s daemon service: %w", action, err)
				}
				return renderDaemonLifecycle(stdout, resp, cmd.String("format"))
			},
		})
	}
	return &cli.Command{
		Name:     "service",
		Usage:    "Manage paxd as a background service",
		Commands: commands,
	}
}

func newDaemonStatusCommand(stdout io.Writer) *cli.Command {
	return &cli.Command{
		Name:  "status",
		Usage: "Show local paxd status",
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "format", Value: "table", Usage: "Output format: table or jsonl"},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			resp, err := newDaemonFacade().Status(ctx, &facade.DaemonStatusRequest{})
			if err != nil {
				return fmt.Errorf("show daemon status: %w", err)
			}
			return renderDaemonStatus(stdout, resp, cmd.String("format"))
		},
	}
}

func newDaemonRemoteCommand(stdout io.Writer) *cli.Command {
	return &cli.Command{
		Name:  "remote",
		Usage: "Manage local paxd remotes",
		Commands: []*cli.Command{
			{
				Name:  "list",
				Usage: "List configured remotes",
				Flags: []cli.Flag{
					&cli.BoolFlag{Name: "all", Usage: "Include disabled remotes"},
					&cli.StringFlag{Name: "format", Value: "table", Usage: "Output format: table or jsonl"},
				},
				Action: func(ctx context.Context, cmd *cli.Command) error {
					resp, err := newDaemonFacade().ListRemotes(ctx, &facade.ListDaemonRemotesRequest{
						IncludeDisabled: cmd.Bool("all"),
					})
					if err != nil {
						return fmt.Errorf("list daemon remotes: %w", err)
					}
					return renderDaemonRemotes(stdout, resp, cmd.String("format"))
				},
			},
			{
				Name:      "create",
				Usage:     "Create a daemon remote from node credentials",
				ArgsUsage: "<remote>",
				Flags: []cli.Flag{
					&cli.StringFlag{Name: "name", Usage: "Remote display name"},
					&cli.StringFlag{Name: "cloud-url", Usage: "Pax cloud API URL"},
					&cli.StringFlag{Name: "node-id", Usage: "Pax node id"},
					&cli.StringFlag{Name: "api-key-ref", Usage: "Secret reference for the node API key"},
					&cli.BoolFlag{Name: "disabled", Usage: "Create the remote disabled"},
					&cli.StringFlag{Name: "format", Value: "text", Usage: "Output format: text or json"},
				},
				Action: func(ctx context.Context, cmd *cli.Command) error {
					resp, err := newDaemonFacade().CreateRemote(ctx, &facade.CreateDaemonRemoteRequest{
						RemoteID:       cmd.Args().First(),
						Name:           cmd.String("name"),
						CloudAPIURL:    cmd.String("cloud-url"),
						NodeID:         cmd.String("node-id"),
						CloudAPIKeyRef: cmd.String("api-key-ref"),
						Enabled:        !cmd.Bool("disabled"),
					})
					if err != nil {
						return fmt.Errorf("create daemon remote: %w", err)
					}
					return renderDaemonAck(stdout, "Remote create requested.", resp, cmd.String("format"))
				},
			},
			{
				Name:      "update",
				Usage:     "Update daemon remote desired state",
				ArgsUsage: "<remote>",
				Flags: []cli.Flag{
					&cli.StringFlag{Name: "name", Usage: "Remote display name"},
					&cli.StringFlag{Name: "cloud-url", Usage: "Pax cloud API URL"},
					&cli.StringFlag{Name: "node-id", Usage: "Pax node id"},
					&cli.StringFlag{Name: "api-key-ref", Usage: "Secret reference for the node API key"},
					&cli.BoolFlag{Name: "enabled", Usage: "Enable the remote"},
					&cli.BoolFlag{Name: "disabled", Usage: "Disable the remote"},
					&cli.StringFlag{Name: "format", Value: "text", Usage: "Output format: text or json"},
				},
				Action: func(ctx context.Context, cmd *cli.Command) error {
					req := &facade.UpdateDaemonRemoteRequest{RemoteID: cmd.Args().First()}
					if cmd.IsSet("name") {
						value := cmd.String("name")
						req.Name = &value
					}
					if cmd.IsSet("cloud-url") {
						value := cmd.String("cloud-url")
						req.CloudAPIURL = &value
					}
					if cmd.IsSet("node-id") {
						value := cmd.String("node-id")
						req.NodeID = &value
					}
					if cmd.IsSet("api-key-ref") {
						value := cmd.String("api-key-ref")
						req.CloudAPIKeyRef = &value
					}
					if cmd.Bool("enabled") && cmd.Bool("disabled") {
						return fmt.Errorf("remote update: --enabled and --disabled are mutually exclusive")
					}
					if cmd.Bool("enabled") || cmd.Bool("disabled") {
						value := cmd.Bool("enabled")
						req.Enabled = &value
					}
					resp, err := newDaemonFacade().UpdateRemote(ctx, req)
					if err != nil {
						return fmt.Errorf("update daemon remote: %w", err)
					}
					return renderDaemonAck(stdout, "Remote update requested.", resp, cmd.String("format"))
				},
			},
			daemonRemoteActionCommand("restart", "Restart a remote control connection", stdout),
			daemonRemoteActionCommand("disconnect", "Disable a remote", stdout),
			daemonRemoteActionCommand("remove", "Remove a remote and its local agent connections", stdout),
		},
	}
}

func daemonRemoteActionCommand(name string, usage string, stdout io.Writer) *cli.Command {
	return &cli.Command{
		Name:      name,
		Usage:     usage,
		ArgsUsage: "<remote>",
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "format", Value: "text", Usage: "Output format: text or json"},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			remoteID := cmd.Args().First()
			var resp *facade.DaemonCommandResponse
			var err error
			switch name {
			case "restart":
				resp, err = newDaemonFacade().RestartRemote(ctx, remoteID)
			case "disconnect":
				resp, err = newDaemonFacade().DisconnectRemote(ctx, remoteID)
			case "remove":
				resp, err = newDaemonFacade().RemoveRemote(ctx, remoteID)
			default:
				err = fmt.Errorf("unsupported daemon remote action %q", name)
			}
			if err != nil {
				return fmt.Errorf("%s daemon remote: %w", name, err)
			}
			return renderDaemonAck(stdout, "Remote "+name+" requested.", resp, cmd.String("format"))
		},
	}
}

func newDaemonAgentCommand(stdout io.Writer) *cli.Command {
	return &cli.Command{
		Name:  "agent",
		Usage: "Manage local paxd agent connections",
		Commands: []*cli.Command{
			{
				Name:  "list",
				Usage: "List local agent connections",
				Flags: []cli.Flag{
					&cli.BoolFlag{Name: "all", Usage: "Include disabled/deleted connections"},
					&cli.StringFlag{Name: "format", Value: "table", Usage: "Output format: table or jsonl"},
				},
				Action: func(ctx context.Context, cmd *cli.Command) error {
					resp, err := newDaemonFacade().ListAgents(ctx, &facade.ListDaemonAgentsRequest{
						IncludeDisabled: cmd.Bool("all"),
					})
					if err != nil {
						return fmt.Errorf("list daemon agents: %w", err)
					}
					return renderDaemonAgents(stdout, resp, cmd.String("format"))
				},
			},
			{
				Name:  "create",
				Usage: "Create a local daemon agent connection",
				Flags: []cli.Flag{
					&cli.StringFlag{Name: "remote", Usage: "Remote id"},
					&cli.StringFlag{Name: "name", Usage: "Agent connection name"},
					&cli.StringFlag{Name: "harness", Usage: "Harness to run"},
					&cli.StringSliceFlag{Name: "command", Usage: "Command argv element. Repeat for multiple elements."},
					&cli.StringFlag{Name: "cloud-agent-id", Usage: "Existing cloud agent id"},
					&cli.StringFlag{Name: "instance-id", Usage: "Agent instance id"},
					&cli.StringFlag{Name: "agent-type", Usage: "Cloud agent type"},
					&cli.StringFlag{Name: "working-dir", Usage: "Working directory"},
					&cli.StringFlag{Name: "format", Value: "text", Usage: "Output format: text or json"},
				},
				Action: func(ctx context.Context, cmd *cli.Command) error {
					resp, err := newDaemonFacade().CreateAgent(ctx, &facade.CreateDaemonAgentRequest{
						RemoteID:     cmd.String("remote"),
						Name:         cmd.String("name"),
						Harness:      cmd.String("harness"),
						Command:      cmd.StringSlice("command"),
						CloudAgentID: cmd.String("cloud-agent-id"),
						InstanceID:   cmd.String("instance-id"),
						AgentType:    cmd.String("agent-type"),
						WorkingDir:   cmd.String("working-dir"),
					})
					if err != nil {
						return fmt.Errorf("create daemon agent: %w", err)
					}
					return renderDaemonAck(stdout, "Agent create requested.", resp, cmd.String("format"))
				},
			},
			{
				Name:      "update",
				Usage:     "Update local daemon agent desired state",
				ArgsUsage: "<name-or-id>",
				Flags: []cli.Flag{
					&cli.StringFlag{Name: "name", Usage: "Agent connection name"},
					&cli.StringFlag{Name: "harness", Usage: "Harness to run"},
					&cli.StringSliceFlag{Name: "command", Usage: "Command argv element. Repeat for multiple elements."},
					&cli.StringFlag{Name: "cloud-agent-id", Usage: "Existing cloud agent id"},
					&cli.StringFlag{Name: "instance-id", Usage: "Agent instance id"},
					&cli.StringFlag{Name: "agent-type", Usage: "Cloud agent type"},
					&cli.StringFlag{Name: "working-dir", Usage: "Working directory"},
					&cli.StringFlag{Name: "desired-state", Usage: "Desired state: running, stopped, or deleted"},
					&cli.BoolFlag{Name: "enabled", Usage: "Enable the agent connection"},
					&cli.BoolFlag{Name: "disabled", Usage: "Disable the agent connection"},
					&cli.StringFlag{Name: "format", Value: "text", Usage: "Output format: text or json"},
				},
				Action: func(ctx context.Context, cmd *cli.Command) error {
					req, err := parseDaemonAgentUpdateRequest(cmd)
					if err != nil {
						return err
					}
					resp, err := newDaemonFacade().UpdateAgent(ctx, req)
					if err != nil {
						return fmt.Errorf("update daemon agent: %w", err)
					}
					return renderDaemonAck(stdout, "Agent update requested.", resp, cmd.String("format"))
				},
			},
			daemonAgentActionCommand("restart", "Restart a local agent connection", stdout),
			daemonAgentActionCommand("stop", "Stop a local agent connection", stdout),
			daemonAgentActionCommand("remove", "Remove a local agent connection", stdout),
		},
	}
}

func daemonAgentActionCommand(name string, usage string, stdout io.Writer) *cli.Command {
	return &cli.Command{
		Name:      name,
		Usage:     usage,
		ArgsUsage: "<name-or-id>",
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "format", Value: "text", Usage: "Output format: text or json"},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			connectionID := cmd.Args().First()
			var resp *facade.DaemonCommandResponse
			var err error
			switch name {
			case "restart":
				resp, err = newDaemonFacade().RestartAgent(ctx, connectionID)
			case "stop":
				resp, err = newDaemonFacade().StopAgent(ctx, connectionID)
			case "remove":
				resp, err = newDaemonFacade().RemoveAgent(ctx, connectionID)
			default:
				err = fmt.Errorf("unsupported daemon agent action %q", name)
			}
			if err != nil {
				return fmt.Errorf("%s daemon agent: %w", name, err)
			}
			return renderDaemonAck(stdout, "Agent "+name+" requested.", resp, cmd.String("format"))
		},
	}
}

func newDaemonHarnessCommand(stdout io.Writer) *cli.Command {
	return &cli.Command{
		Name:  "harness",
		Usage: "List and discover local daemon harnesses",
		Commands: []*cli.Command{
			{
				Name:  "list",
				Usage: "List known harnesses",
				Flags: []cli.Flag{
					&cli.BoolFlag{Name: "all", Usage: "Include missing harnesses"},
					&cli.StringFlag{Name: "format", Value: "table", Usage: "Output format: table or jsonl"},
				},
				Action: func(ctx context.Context, cmd *cli.Command) error {
					resp, err := newDaemonFacade().ListHarnesses(ctx, &facade.ListDaemonHarnessesRequest{
						IncludeMissing: cmd.Bool("all"),
					})
					if err != nil {
						return fmt.Errorf("list daemon harnesses: %w", err)
					}
					return renderDaemonHarnesses(stdout, resp, cmd.String("format"))
				},
			},
			{
				Name:      "discover",
				Usage:     "Discover harness availability",
				ArgsUsage: "[harness...]",
				Flags: []cli.Flag{
					&cli.BoolFlag{Name: "probe", Usage: "Probe live reachability where supported"},
					&cli.StringFlag{Name: "format", Value: "table", Usage: "Output format: table or jsonl"},
				},
				Action: func(ctx context.Context, cmd *cli.Command) error {
					resp, err := newDaemonFacade().DiscoverHarnesses(ctx, &facade.DiscoverDaemonHarnessesRequest{
						Probe: cmd.Bool("probe"),
						Names: cmd.Args().Slice(),
					})
					if err != nil {
						return fmt.Errorf("discover daemon harnesses: %w", err)
					}
					return renderDaemonHarnesses(stdout, resp, cmd.String("format"))
				},
			},
		},
	}
}

func newDaemonLocalCommand(stdout io.Writer) *cli.Command {
	return &cli.Command{
		Name:  "local",
		Usage: "Inspect local data through paxd",
		Commands: []*cli.Command{
			{
				Name:  "overview",
				Usage: "Show local paxd overview",
				Flags: []cli.Flag{
					&cli.StringFlag{Name: "format", Value: "table", Usage: "Output format: table or jsonl"},
				},
				Action: func(ctx context.Context, cmd *cli.Command) error {
					resp, err := newDaemonFacade().LocalOverview(ctx, &facade.DaemonLocalOverviewRequest{})
					if err != nil {
						return fmt.Errorf("show daemon local overview: %w", err)
					}
					return renderDaemonLocalOverview(stdout, resp, cmd.String("format"))
				},
			},
			{
				Name:  "session",
				Usage: "Inspect local sessions through paxd",
				Commands: []*cli.Command{
					{
						Name:  "list",
						Usage: "List local sessions through paxd",
						Flags: []cli.Flag{
							&cli.StringFlag{Name: "agent", Usage: "Agent filter"},
							&cli.IntFlag{Name: "limit", Usage: "Maximum sessions to show"},
							&cli.StringFlag{Name: "format", Value: "table", Usage: "Output format: table or jsonl"},
						},
						Action: func(ctx context.Context, cmd *cli.Command) error {
							resp, err := newDaemonFacade().ListLocalSessions(ctx, &facade.ListDaemonLocalSessionsRequest{
								Agent: cmd.String("agent"),
								Limit: cmd.Int("limit"),
							})
							if err != nil {
								return fmt.Errorf("list daemon local sessions: %w", err)
							}
							return renderDaemonLocalSessions(stdout, resp.Sessions, cmd.String("format"))
						},
					},
					{
						Name:  "sync",
						Usage: "Sync local sessions through paxd",
						Flags: []cli.Flag{
							&cli.StringFlag{Name: "agent", Usage: "Agent filter"},
							&cli.IntFlag{Name: "limit", Usage: "Maximum sessions to sync"},
							&cli.StringFlag{Name: "timeout", Value: "10s", Usage: "Sync timeout"},
							&cli.StringFlag{Name: "format", Value: "text", Usage: "Output format: text or json"},
						},
						Action: func(ctx context.Context, cmd *cli.Command) error {
							timeout, err := time.ParseDuration(cmd.String("timeout"))
							if err != nil {
								return fmt.Errorf("parse sync timeout: %w", err)
							}
							resp, err := newDaemonFacade().SyncLocalSessions(ctx, &facade.SyncDaemonLocalSessionsRequest{
								Agent:         cmd.String("agent"),
								Limit:         cmd.Int("limit"),
								TimeoutMillis: timeout.Milliseconds(),
							})
							if err != nil {
								return fmt.Errorf("sync daemon local sessions: %w", err)
							}
							return renderDaemonLocalSessionSync(stdout, resp, cmd.String("format"))
						},
					},
				},
			},
		},
	}
}

func parseDaemonAgentUpdateRequest(cmd *cli.Command) (*facade.UpdateDaemonAgentRequest, error) {
	req := &facade.UpdateDaemonAgentRequest{ConnectionID: cmd.Args().First()}
	if cmd.IsSet("name") {
		value := cmd.String("name")
		req.Name = &value
	}
	if cmd.IsSet("cloud-agent-id") {
		value := cmd.String("cloud-agent-id")
		req.CloudAgentID = &value
	}
	if cmd.IsSet("instance-id") {
		value := cmd.String("instance-id")
		req.InstanceID = &value
	}
	if cmd.IsSet("agent-type") {
		value := cmd.String("agent-type")
		req.AgentType = &value
	}
	if cmd.IsSet("harness") {
		value := cmd.String("harness")
		req.Harness = &value
	}
	if cmd.IsSet("command") {
		value := cmd.StringSlice("command")
		req.Command = &value
	}
	if cmd.IsSet("working-dir") {
		value := cmd.String("working-dir")
		req.WorkingDir = &value
	}
	if cmd.Bool("enabled") && cmd.Bool("disabled") {
		return nil, fmt.Errorf("agent update: --enabled and --disabled are mutually exclusive")
	}
	if cmd.Bool("enabled") || cmd.Bool("disabled") {
		value := cmd.Bool("enabled")
		req.Enabled = &value
	}
	if cmd.IsSet("desired-state") {
		state, err := parseDaemonDesiredState(cmd.String("desired-state"))
		if err != nil {
			return nil, err
		}
		req.DesiredState = &state
	}
	return req, nil
}

func parseDaemonDesiredState(raw string) (model.DaemonDesiredState, error) {
	switch model.DaemonDesiredState(strings.TrimSpace(raw)) {
	case model.DaemonDesiredStateRunning:
		return model.DaemonDesiredStateRunning, nil
	case model.DaemonDesiredStateStopped:
		return model.DaemonDesiredStateStopped, nil
	case model.DaemonDesiredStateDeleted:
		return model.DaemonDesiredStateDeleted, nil
	default:
		return model.DaemonDesiredStateUnknown, fmt.Errorf("unsupported desired state %q", raw)
	}
}

func renderDaemonStatus(
	stdout io.Writer,
	resp *facade.DaemonStatusResponse,
	format string,
) error {
	if resp == nil || resp.Status == nil {
		return fmt.Errorf("daemon status response is empty")
	}
	switch format {
	case "table":
		fmt.Fprintf(stdout, "DAEMON\t%s\n", firstNonEmpty(resp.Status.Phase, "unknown"))
		if len(resp.Status.Remotes) > 0 {
			tw := tabwriter.NewWriter(stdout, 0, 4, 2, ' ', 0)
			fmt.Fprintln(tw, "REMOTE\tPHASE\tERROR")
			for _, item := range resp.Status.Remotes {
				fmt.Fprintf(tw, "%s\t%s\t%s\n", item.RemoteID, firstNonEmpty(item.Phase, "-"), firstNonEmpty(item.LastErrorMessage, "-"))
			}
			if err := tw.Flush(); err != nil {
				return err
			}
		}
		if len(resp.Status.AgentConnections) > 0 {
			tw := tabwriter.NewWriter(stdout, 0, 4, 2, ' ', 0)
			fmt.Fprintln(tw, "AGENT\tPHASE\tERROR")
			for _, item := range resp.Status.AgentConnections {
				fmt.Fprintf(tw, "%s\t%s\t%s\n", item.ConnectionID, firstNonEmpty(item.Phase, "-"), firstNonEmpty(item.LastErrorMessage, "-"))
			}
			return tw.Flush()
		}
		return nil
	case "jsonl":
		return json.NewEncoder(stdout).Encode(resp.Status)
	default:
		return fmt.Errorf("unsupported format %q", format)
	}
}

func renderDaemonRemotes(
	stdout io.Writer,
	resp *facade.ListDaemonRemotesResponse,
	format string,
) error {
	items := []*model.DaemonRemoteView(nil)
	if resp != nil {
		items = resp.Remotes
	}
	switch format {
	case "table":
		tw := tabwriter.NewWriter(stdout, 0, 4, 2, ' ', 0)
		fmt.Fprintln(tw, "REMOTE\tNAME\tURL\tENABLED\tPHASE")
		for _, item := range items {
			enabled := true
			if item.Remote.Enabled != nil {
				enabled = *item.Remote.Enabled
			}
			phase := "-"
			if item.Status != nil {
				phase = firstNonEmpty(item.Status.Phase, "-")
			}
			fmt.Fprintf(tw, "%s\t%s\t%s\t%t\t%s\n", item.Remote.ID, item.Remote.Name, item.Remote.CloudAPIURL, enabled, phase)
		}
		return tw.Flush()
	case "jsonl":
		return encodeDaemonJSONL(stdout, items)
	default:
		return fmt.Errorf("unsupported format %q", format)
	}
}

func renderDaemonAgents(
	stdout io.Writer,
	resp *facade.ListDaemonAgentsResponse,
	format string,
) error {
	items := []*model.DaemonAgentConnectionView(nil)
	if resp != nil {
		items = resp.Agents
	}
	switch format {
	case "table":
		tw := tabwriter.NewWriter(stdout, 0, 4, 2, ' ', 0)
		fmt.Fprintln(tw, "AGENT\tREMOTE\tNAME\tHARNESS\tDESIRED\tPHASE")
		for _, item := range items {
			phase := "-"
			if item.Status != nil {
				phase = firstNonEmpty(item.Status.Phase, "-")
			}
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n", item.ID, item.RemoteID, item.Name, item.Harness, item.DesiredState, phase)
		}
		return tw.Flush()
	case "jsonl":
		return encodeDaemonJSONL(stdout, items)
	default:
		return fmt.Errorf("unsupported format %q", format)
	}
}

func renderDaemonHarnesses(
	stdout io.Writer,
	resp *facade.ListDaemonHarnessesResponse,
	format string,
) error {
	items := []*model.DaemonHarnessView(nil)
	if resp != nil {
		items = resp.Harnesses
	}
	switch format {
	case "table":
		tw := tabwriter.NewWriter(stdout, 0, 4, 2, ' ', 0)
		fmt.Fprintln(tw, "HARNESS\tSTATE\tCOMMAND\tSOURCE\tNOTE")
		for _, item := range items {
			command := "-"
			if len(item.Command) > 0 {
				command = strings.Join(item.Command, " ")
			}
			note := firstNonEmpty(item.LastError, item.InstallHint, "-")
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", item.Harness, firstNonEmpty(item.State, "-"), command, firstNonEmpty(item.Source, "-"), note)
		}
		return tw.Flush()
	case "jsonl":
		return encodeDaemonJSONL(stdout, items)
	default:
		return fmt.Errorf("unsupported format %q", format)
	}
}

func renderDaemonLocalOverview(
	stdout io.Writer,
	resp *facade.DaemonLocalOverviewResponse,
	format string,
) error {
	overview := &model.DaemonLocalOverview{}
	if resp != nil && resp.Overview != nil {
		overview = resp.Overview
	}
	switch format {
	case "table":
		fmt.Fprintf(stdout, "SESSIONS\t%d\n", len(overview.Sessions))
		fmt.Fprintf(stdout, "HARNESSES\t%d\n", len(overview.Harnesses))
		return nil
	case "jsonl":
		return json.NewEncoder(stdout).Encode(overview)
	default:
		return fmt.Errorf("unsupported format %q", format)
	}
}

func renderDaemonLocalSessions(
	stdout io.Writer,
	items []*model.DaemonLocalSessionView,
	format string,
) error {
	switch format {
	case "table":
		tw := tabwriter.NewWriter(stdout, 0, 4, 2, ' ', 0)
		fmt.Fprintln(tw, "ID\tAGENT\tUPDATED\tTITLE")
		for _, item := range items {
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", item.ID, item.Agent, firstNonEmpty(item.UpdatedAt, "-"), firstNonEmpty(item.Title, item.Preview, "-"))
		}
		return tw.Flush()
	case "jsonl":
		return encodeDaemonJSONL(stdout, items)
	default:
		return fmt.Errorf("unsupported format %q", format)
	}
}

func renderDaemonLocalSessionSync(
	stdout io.Writer,
	resp *facade.SyncDaemonLocalSessionsResponse,
	format string,
) error {
	sync := &model.DaemonLocalSessionSyncResult{}
	if resp != nil && resp.Sync != nil {
		sync = resp.Sync
	}
	switch format {
	case "text":
		fmt.Fprintf(stdout, "Synced %d local sessions with %d failures.\n", sync.Synced, sync.Failed)
		return nil
	case "json":
		return json.NewEncoder(stdout).Encode(sync)
	default:
		return fmt.Errorf("unsupported format %q", format)
	}
}

func renderDaemonLifecycle(
	stdout io.Writer,
	resp *facade.DaemonLifecycleResponse,
	format string,
) error {
	if resp == nil {
		return fmt.Errorf("daemon lifecycle response is empty")
	}
	switch format {
	case "text":
		_, err := fmt.Fprintln(stdout, resp.Message)
		return err
	case "json":
		return json.NewEncoder(stdout).Encode(resp)
	default:
		return fmt.Errorf("unsupported format %q", format)
	}
}

func renderDaemonAck(
	stdout io.Writer,
	message string,
	resp *facade.DaemonCommandResponse,
	format string,
) error {
	if resp == nil || resp.Ack == nil {
		return fmt.Errorf("daemon command response is empty")
	}
	switch format {
	case "text":
		_, err := fmt.Fprintln(stdout, message)
		return err
	case "json":
		return json.NewEncoder(stdout).Encode(resp.Ack)
	default:
		return fmt.Errorf("unsupported format %q", format)
	}
}

func encodeDaemonJSONL[T any](stdout io.Writer, items []T) error {
	encoder := json.NewEncoder(stdout)
	for _, item := range items {
		if err := encoder.Encode(item); err != nil {
			return err
		}
	}
	return nil
}
