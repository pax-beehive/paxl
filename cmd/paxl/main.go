package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"runtime/pprof"
	"strconv"
	"strings"
	"sync"
	"text/tabwriter"
	"time"

	"github.com/pax-oss/paxl/internal/facade"
	"github.com/pax-oss/paxl/internal/model"
	"github.com/pax-oss/paxl/internal/model/store"
	"github.com/urfave/cli/v3"
)

var version = "0.1.0"
var buildCommit = ""
var updateHTTPClient facade.UpdateHTTPClient = http.DefaultClient
var authHTTPClient facade.AuthHTTPClient = http.DefaultClient
var executablePath = os.Executable
var scheduleSessionQueryBackgroundSync = startSessionQueryBackgroundSync

func main() {
	if err := run(context.Background(), os.Args[1:], os.Stdout, os.Stderr); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, "Error:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, stdout io.Writer, stderr io.Writer) error {
	return runWithInput(ctx, args, os.Stdin, stdout, stderr)
}

func runWithInput(
	ctx context.Context,
	args []string,
	stdin io.Reader,
	stdout io.Writer,
	stderr io.Writer,
) error {
	logger := openExecutionLogger(args)
	defer closeExecutionLogger(logger)
	command := newCommandWithDiagnostics(stdin, stdout, stderr, diagnosticWriter(logger))
	if err := command.Run(ctx, append([]string{"paxl"}, args...)); err != nil {
		wrapped := fmt.Errorf("run paxl command: %w", err)
		finishExecutionLog(logger, wrapped)
		return wrapped
	}
	finishExecutionLog(logger, nil)
	return nil
}

func newCommand(stdout io.Writer, stderr io.Writer) *cli.Command {
	return newCommandWithDiagnostics(os.Stdin, stdout, stderr, nil)
}

func newCommandWithDiagnostics(
	stdin io.Reader,
	stdout io.Writer,
	stderr io.Writer,
	diagnostics io.Writer,
) *cli.Command {
	agentFacade := facade.NewAgentFacade(nil)
	return &cli.Command{
		Name:      "paxl",
		Usage:     "Local-first Pax agent session tools",
		Writer:    stdout,
		ErrWriter: stderr,
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "db", Usage: "SQLite database path"},
			&cli.StringFlag{
				Name:    "caller-agent",
				Usage:   "Agent invoking paxl",
				Hidden:  true,
				Sources: cli.EnvVars("PAXL_CALLER_AGENT", "PAXL_AGENT"),
			},
		},
		ExitErrHandler: func(ctx context.Context, cmd *cli.Command, err error) {
			_ = ctx
			_ = cmd
			_ = err
		},
		Commands: []*cli.Command{
			newVersionCommand(stdout),
			newUpdateCommand(stdout),
			newLoginCommand(stdout),
			newWhoamiCommand(stdout),
			newLogoutCommand(stdout),
			newNodeCommand(stdout),
			newDaemonCommand(stdout),
			newSetupCommand(stdout),
			newAgentCommand(agentFacade, stdout, stderr, diagnostics),
			newSessionCommand(stdout, stderr, diagnostics),
			newCapsuleCommand(stdin, stdout, stderr, diagnostics),
			newInboxCommand(stdout),
			newOutboxCommand(stdout),
			newFriendCommand(stdout),
			newTeamCommand(stdout),
			newAgentHookCommand(stdout),
			newAgentEnvCommand(stdout),
		},
	}
}

func newLoginCommand(stdout io.Writer) *cli.Command {
	return &cli.Command{
		Name:  "login",
		Usage: "Authenticate paxl with pax-manager",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:   "manager-url",
				Value:  facade.DefaultManagerURL,
				Usage:  "pax-manager base URL",
				Hidden: true,
			},
			&cli.StringFlag{
				Name:   "timeout",
				Value:  "2m",
				Usage:  "Maximum time to wait for browser approval",
				Hidden: true,
			},
			&cli.StringFlag{
				Name:   "format",
				Value:  "text",
				Usage:  "Output format: text or json",
				Hidden: true,
			},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			return loginCommand(ctx, cmd, stdout)
		},
	}
}

func newWhoamiCommand(stdout io.Writer) *cli.Command {
	return &cli.Command{
		Name:  "whoami",
		Usage: "Show the logged-in pax-manager user",
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "format", Value: "text", Usage: "Output format: text or json"},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			return whoamiCommand(ctx, cmd, stdout)
		},
	}
}

func newLogoutCommand(stdout io.Writer) *cli.Command {
	return &cli.Command{
		Name:  "logout",
		Usage: "Clear the local pax-manager credential",
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "format", Value: "text", Usage: "Output format: text or json"},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			return logoutCommand(ctx, cmd, stdout)
		},
	}
}

func newNodeCommand(stdout io.Writer) *cli.Command {
	return &cli.Command{
		Name:  "node",
		Usage: "Inspect pax-manager nodes for the logged-in user",
		Commands: []*cli.Command{
			{
				Name:  "list",
				Usage: "List nodes visible to the logged-in user",
				Flags: []cli.Flag{
					&cli.StringFlag{
						Name:  "format",
						Value: "table",
						Usage: "Output format: table or jsonl",
					},
				},
				Action: func(ctx context.Context, cmd *cli.Command) error {
					return nodeList(ctx, cmd, stdout)
				},
			},
			{
				Name:  "agent",
				Usage: "Inspect remote agents hosted by a node",
				Commands: []*cli.Command{
					{
						Name:      "list",
						Usage:     "List remote agents hosted by a node",
						ArgsUsage: "<node-id>",
						Flags: []cli.Flag{
							&cli.StringFlag{
								Name:  "format",
								Value: "table",
								Usage: "Output format: table or jsonl",
							},
						},
						Action: func(ctx context.Context, cmd *cli.Command) error {
							return nodeAgentList(ctx, cmd, stdout)
						},
					},
				},
			},
			{
				Name:  "session",
				Usage: "Inspect remote sessions hosted by a node agent",
				Commands: []*cli.Command{
					{
						Name:      "list",
						Usage:     "List remote sessions for a node agent",
						ArgsUsage: "<node-id> <agent-id>",
						Flags: []cli.Flag{
							&cli.StringFlag{
								Name:  "format",
								Value: "table",
								Usage: "Output format: table or jsonl",
							},
						},
						Action: func(ctx context.Context, cmd *cli.Command) error {
							return nodeSessionList(ctx, cmd, stdout)
						},
					},
				},
			},
		},
	}
}

func newSetupCommand(stdout io.Writer) *cli.Command {
	return &cli.Command{
		Name:  "setup",
		Usage: "Install local paxl agent integrations",
		Flags: []cli.Flag{
			&cli.StringSliceFlag{
				Name:  "agent",
				Usage: "Agent to set up: codex, claude, pi, kiro, hermes, or openclaw. Repeat to select multiple agents.",
			},
			&cli.StringFlag{
				Name:  "format",
				Value: "table",
				Usage: "Output format: table or jsonl",
			},
			&cli.BoolFlag{Name: "dry-run", Usage: "Show setup actions without writing files"},
			&cli.BoolFlag{
				Name:  "with-daemon",
				Usage: "Install and set up paxd after local agent integrations",
			},
			&cli.StringFlag{Name: "cloud-url", Usage: "Pax cloud API URL for --with-daemon"},
			&cli.StringFlag{
				Name:  "daemon-resolver-url",
				Value: facade.DefaultDaemonResolverURL,
				Usage: "paxd artifact resolver URL for --with-daemon",
			},
			&cli.StringFlag{
				Name:  "daemon-platform",
				Usage: "paxd release platform override like darwin/arm64",
			},
			&cli.StringFlag{
				Name:  "daemon-tag",
				Value: facade.DefaultUpdateTag,
				Usage: "paxd release tag for --with-daemon",
			},
			&cli.StringFlag{
				Name:  "daemon-install-dir",
				Usage: "Directory to install paxd into for --with-daemon",
			},
			&cli.StringFlag{
				Name:   "paxl-command",
				Usage:  "paxl command path to write into installed hooks",
				Hidden: true,
			},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			return setupCommand(ctx, cmd, stdout)
		},
	}
}

func newAgentHookCommand(stdout io.Writer) *cli.Command {
	return &cli.Command{
		Name:   "__agent-hook",
		Usage:  "Internal paxl agent hook entrypoint",
		Hidden: true,
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "agent", Usage: "Agent that fired the hook"},
			&cli.StringFlag{Name: "event", Usage: "Hook event name"},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			return agentHook(ctx, cmd, stdout)
		},
	}
}

func newAgentEnvCommand(stdout io.Writer) *cli.Command {
	return &cli.Command{
		Name:   "__agent-env",
		Usage:  "Internal paxl agent environment hook entrypoint",
		Hidden: true,
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "agent", Usage: "Agent that fired the hook"},
			&cli.StringFlag{Name: "event", Usage: "Hook event name"},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			_ = ctx
			return agentEnv(cmd, stdout)
		},
	}
}

func newVersionCommand(stdout io.Writer) *cli.Command {
	return &cli.Command{
		Name:  "version",
		Usage: "Print paxl version and build metadata",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:  "format",
				Value: "text",
				Usage: "Output format: text or json",
			},
			&cli.BoolFlag{Name: "check", Usage: "Check the latest hosted paxl release"},
			&cli.StringFlag{
				Name:  "manifest-url",
				Usage: "Release manifest URL override for --check",
			},
			&cli.StringFlag{
				Name:  "resolver-url",
				Value: facade.DefaultUpdateResolverURL,
				Usage: "Artifact resolver URL for --check",
			},
			&cli.StringFlag{
				Name:  "tag",
				Value: facade.DefaultUpdateTag,
				Usage: "Release tag to check",
			},
			&cli.StringFlag{
				Name:  "platform",
				Usage: "Release platform override like darwin/arm64",
			},
			&cli.StringFlag{
				Name:  "timeout",
				Value: "3s",
				Usage: "Update check timeout",
			},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			return versionCommand(ctx, cmd, stdout)
		},
	}
}

func newUpdateCommand(stdout io.Writer) *cli.Command {
	updateFlags := func(timeout string) []cli.Flag {
		return []cli.Flag{
			&cli.StringFlag{
				Name:  "format",
				Value: "text",
				Usage: "Output format: text, json, or jsonl",
			},
			&cli.StringFlag{
				Name:  "manifest-url",
				Usage: "Release manifest URL override",
			},
			&cli.StringFlag{
				Name:  "resolver-url",
				Value: facade.DefaultUpdateResolverURL,
				Usage: "Artifact resolver URL",
			},
			&cli.StringFlag{
				Name:  "tag",
				Value: facade.DefaultUpdateTag,
				Usage: "Release tag to check",
			},
			&cli.StringFlag{
				Name:  "platform",
				Usage: "Release platform override like darwin/arm64",
			},
			&cli.StringFlag{
				Name:  "timeout",
				Value: timeout,
				Usage: "Update timeout",
			},
		}
	}
	return &cli.Command{
		Name:  "update",
		Usage: "Update the paxl binary in place",
		Flags: updateFlags("30s"),
		Action: func(ctx context.Context, cmd *cli.Command) error {
			return updateCommand(ctx, cmd, stdout)
		},
		Commands: []*cli.Command{
			{
				Name:  "check",
				Usage: "Check the latest hosted paxl release",
				Flags: updateFlags("3s"),
				Action: func(ctx context.Context, cmd *cli.Command) error {
					return updateCheck(ctx, cmd, stdout)
				},
			},
		},
	}
}

func newAgentCommand(
	agentFacade *facade.AgentFacade,
	stdout io.Writer,
	stderr io.Writer,
	diagnostics io.Writer,
) *cli.Command {
	return &cli.Command{
		Name:  "agent",
		Usage: "Inspect local agent sources",
		Commands: []*cli.Command{
			{
				Name:  "list",
				Usage: "List supported local agents and adapters",
				Flags: []cli.Flag{
					&cli.StringFlag{
						Name:  "format",
						Value: "table",
						Usage: "Output format: table or jsonl",
					},
					&cli.BoolFlag{
						Name:  "probe",
						Usage: "Probe gateway-backed agents for live reachability",
					},
					&cli.BoolFlag{Name: "verbose", Usage: "Print adapter probing details"},
				},
				Action: func(ctx context.Context, cmd *cli.Command) error {
					return agentList(ctx, cmd, agentFacade, stdout, stderr, diagnostics)
				},
			},
		},
	}
}

func newSessionCommand(stdout io.Writer, stderr io.Writer, diagnostics io.Writer) *cli.Command {
	return &cli.Command{
		Name:  "session",
		Usage: "List, render, and mirror local agent sessions",
		Commands: []*cli.Command{
			{
				Name:  "list",
				Usage: "List session metadata",
				Flags: []cli.Flag{
					&cli.StringFlag{
						Name:  "agent",
						Usage: "Agent to scan: codex, claude, pi, kiro, hermes, or openclaw",
					},
					&cli.StringFlag{
						Name:  "updated-since",
						Usage: "Only show sessions updated since a duration like 24h or 7d",
					},
					&cli.IntFlag{Name: "limit", Usage: "Maximum sessions to show"},
					&cli.StringFlag{
						Name:  "format",
						Value: "table",
						Usage: "Output format: table, jsonl, or html",
					},
					&cli.BoolFlag{
						Name:  "no-sync",
						Usage: "Use cached SQLite metadata without scanning local logs",
					},
					&cli.StringFlag{
						Name:  "timeout",
						Value: "30s",
						Usage: "Adapter timeout when refreshing sessions",
					},
					&cli.BoolFlag{Name: "verbose", Usage: "Print session scan details"},
				},
				Action: func(ctx context.Context, cmd *cli.Command) error {
					return sessionList(ctx, cmd, stdout, stderr, diagnostics)
				},
			},
			{
				Name:      "get",
				Usage:     "Render a session timeline",
				ArgsUsage: "<session-id>",
				Flags: []cli.Flag{
					&cli.StringFlag{Name: "agent", Usage: "Agent for bare native session IDs"},
					&cli.StringFlag{
						Name:  "format",
						Value: "transcript",
						Usage: "Output format: transcript, jsonl, or html",
					},
					&cli.StringFlag{Name: "output", Usage: "Output path"},
					&cli.BoolFlag{Name: "verbose", Usage: "Print session sync details"},
				},
				Action: func(ctx context.Context, cmd *cli.Command) error {
					return sessionGet(ctx, cmd, stdout, stderr, diagnostics)
				},
			},
			{
				Name:      "mirror",
				Usage:     "Mirror a source session into another agent session",
				ArgsUsage: "<source-session-id>",
				Flags: []cli.Flag{
					&cli.StringFlag{
						Name:  "agent",
						Usage: "Agent for bare native source session IDs",
					},
					&cli.StringFlag{Name: "to", Usage: "Target agent for a new session"},
					&cli.StringFlag{Name: "to-session", Usage: "Existing target session id"},
					&cli.StringFlag{
						Name:  "format",
						Value: "table",
						Usage: "Output format: table or jsonl",
					},
					&cli.StringFlag{
						Name:  "timeout",
						Value: "2m",
						Usage: "Timeout for target agent delivery",
					},
					&cli.BoolFlag{Name: "verbose", Usage: "Print mirror delivery details"},
				},
				Action: func(ctx context.Context, cmd *cli.Command) error {
					return sessionMirror(ctx, cmd, stdout, stderr, diagnostics)
				},
			},
			{
				Name:      "query",
				Usage:     "Search session contents across all agents",
				ArgsUsage: "<query>",
				Flags: []cli.Flag{
					&cli.IntFlag{Name: "limit", Value: 10, Usage: "Maximum results to show"},
					&cli.StringFlag{
						Name:  "format",
						Value: "table",
						Usage: "Output format: table or jsonl",
					},
					&cli.BoolFlag{Name: "sync", Usage: "Scan local logs before searching"},
					&cli.BoolFlag{
						Name:  "no-background-sync",
						Usage: "Skip the default background refresh after cached search",
					},
					&cli.StringFlag{Name: "agent", Usage: "Filter search results by agent"},
					&cli.StringFlag{
						Name:  "timeout",
						Value: "30s",
						Usage: "Adapter timeout when --sync is enabled",
					},
					&cli.BoolFlag{Name: "verbose", Usage: "Print session search sync details"},
				},
				Action: func(ctx context.Context, cmd *cli.Command) error {
					return sessionQuery(ctx, cmd, stdout, stderr, diagnostics)
				},
			},
		},
	}
}

func newCapsuleCommand(
	stdin io.Reader,
	stdout io.Writer,
	stderr io.Writer,
	diagnostics io.Writer,
) *cli.Command {
	return &cli.Command{
		Name:  "capsule",
		Usage: "Create, list, and inject knowledge capsules",
		Commands: []*cli.Command{
			{
				Name:      "create",
				Usage:     "Create a knowledge capsule from a source session or manual content",
				ArgsUsage: "[source-session-id]",
				Flags: []cli.Flag{
					&cli.StringFlag{
						Name:  "agent",
						Usage: "Agent for bare native source session IDs",
					},
					&cli.StringFlag{
						Name:  "keyword",
						Usage: "Keyword to extract from the source session",
					},
					&cli.StringFlag{
						Name:  "title",
						Usage: "Title for a capsule created from provided content",
					},
					&cli.StringFlag{
						Name:  "summary",
						Usage: "Summary for a capsule created from provided content",
					},
					&cli.StringFlag{
						Name:  "content",
						Usage: "Create the capsule from this content instead of prompting the source agent",
					},
					&cli.BoolFlag{
						Name:  "manual",
						Usage: "Create a capsule from --content or stdin without a source session",
					},
					&cli.StringFlag{
						Name:  "format",
						Value: "table",
						Usage: "Output format: table or jsonl",
					},
					&cli.StringFlag{
						Name:  "timeout",
						Value: "2m",
						Usage: "Timeout waiting for the source agent to produce the capsule",
					},
					&cli.StringFlag{
						Name:  "debug-stack-after",
						Usage: "Dump goroutine stacks after a duration like 5s",
					},
					&cli.BoolFlag{Name: "local", Usage: "Use local transcript extraction"},
					&cli.BoolFlag{Name: "verbose", Usage: "Print capsule creation details"},
				},
				Action: func(ctx context.Context, cmd *cli.Command) error {
					return capsuleCreate(ctx, cmd, stdin, stdout, stderr, diagnostics)
				},
			},
			{
				Name:  "list",
				Usage: "List local knowledge capsules",
				Flags: []cli.Flag{
					&cli.StringFlag{
						Name:  "status",
						Value: "active",
						Usage: "Capsule status filter",
					},
					&cli.StringFlag{Name: "keyword", Usage: "Keyword filter"},
					&cli.StringFlag{Name: "source-session", Usage: "Source session filter"},
					&cli.IntFlag{Name: "limit", Usage: "Maximum capsules to show"},
					&cli.StringFlag{
						Name:  "format",
						Value: "table",
						Usage: "Output format: table or jsonl",
					},
				},
				Action: func(ctx context.Context, cmd *cli.Command) error {
					return capsuleList(ctx, cmd, stdout)
				},
			},
			{
				Name:      "get",
				Usage:     "Render a local knowledge capsule",
				ArgsUsage: "<capsule-id>",
				Flags: []cli.Flag{
					&cli.StringFlag{
						Name:  "format",
						Value: "text",
						Usage: "Output format: text or jsonl",
					},
				},
				Action: func(ctx context.Context, cmd *cli.Command) error {
					return capsuleGet(ctx, cmd, stdout)
				},
			},
			{
				Name:      "archive",
				Usage:     "Archive a local knowledge capsule",
				ArgsUsage: "<capsule-id>",
				Action: func(ctx context.Context, cmd *cli.Command) error {
					return capsuleArchive(ctx, cmd, stdout)
				},
			},
			{
				Name:      "send",
				Usage:     "Send a local knowledge capsule to another user's inbox",
				ArgsUsage: "<capsule-id>",
				Flags: []cli.Flag{
					&cli.StringFlag{Name: "to", Usage: "Accepted friend alias, for example @alice"},
					&cli.StringFlag{
						Name:  "to-agent-id",
						Usage: "Target team agent id for agent-to-agent delivery",
					},
					&cli.StringFlag{
						Name:   "from-agent-id",
						Usage:  "Source team agent id override",
						Hidden: true,
					},
					&cli.StringFlag{Name: "message", Usage: "Optional note for the recipient"},
					&cli.StringFlag{
						Name:  "match",
						Usage: "Envelope route for recipient hook injection: any, project, or keyword",
					},
					&cli.StringFlag{
						Name:  "project",
						Usage: "Project basename for --match project",
					},
					&cli.StringFlag{
						Name:  "keyword",
						Usage: "Prompt substring for --match keyword",
					},
					&cli.StringFlag{
						Name:  "agent",
						Usage: "Optional recipient agent filter for the route",
					},
					&cli.StringFlag{
						Name:  "format",
						Value: "table",
						Usage: "Output format: table or jsonl",
					},
				},
				Action: func(ctx context.Context, cmd *cli.Command) error {
					return capsuleSend(ctx, cmd, stdout)
				},
			},
			{
				Name:      "inject",
				Usage:     "Queue or deliver a knowledge capsule to a target session",
				ArgsUsage: "<capsule-id> [target-session-id]",
				Flags: []cli.Flag{
					&cli.StringFlag{
						Name:  "agent",
						Usage: "Agent for bare native target session IDs or new sessions",
					},
					&cli.StringFlag{
						Name:  "output",
						Usage: "Also write the sent system_handoff message for --new delivery",
					},
					&cli.StringFlag{
						Name:  "timeout",
						Value: "30s",
						Usage: "Agent delivery timeout, for example 10s or 1m",
					},
					&cli.StringFlag{
						Name:  "match",
						Usage: "Queue hook injection route: any, project, or keyword",
					},
					&cli.StringFlag{
						Name:  "project",
						Usage: "Project basename for --match project",
					},
					&cli.StringFlag{
						Name:  "keyword",
						Usage: "Prompt substring for --match keyword",
					},
					&cli.BoolFlag{Name: "new", Usage: "Start a new target agent session"},
					&cli.StringSliceFlag{
						Name: "action-items",
						Usage: "Action item to include in the handoff. " +
							"Repeat to include multiple items.",
					},
					&cli.BoolFlag{Name: "verbose", Usage: "Print injection delivery details"},
				},
				Action: func(ctx context.Context, cmd *cli.Command) error {
					return capsuleInject(ctx, cmd, stdout, stderr, diagnostics)
				},
			},
			{
				Name:  "injection",
				Usage: "List local capsule injection records",
				Flags: []cli.Flag{
					&cli.StringFlag{Name: "target-session", Usage: "Target session filter"},
					&cli.IntFlag{Name: "limit", Usage: "Maximum injections to show"},
					&cli.StringFlag{
						Name:  "format",
						Value: "table",
						Usage: "Output format: table or jsonl",
					},
				},
				Action: func(ctx context.Context, cmd *cli.Command) error {
					return capsuleInjectionList(ctx, cmd, stdout)
				},
			},
		},
	}
}

func newInboxCommand(stdout io.Writer) *cli.Command {
	return &cli.Command{
		Name:  "inbox",
		Usage: "Read and accept received envelopes",
		Commands: []*cli.Command{
			{
				Name:  "list",
				Usage: "List received envelopes",
				Flags: []cli.Flag{
					&cli.StringFlag{
						Name:  "status",
						Value: "pending",
						Usage: "Envelope status filter",
					},
					&cli.IntFlag{Name: "limit", Usage: "Maximum envelopes to show"},
					&cli.StringFlag{
						Name:  "format",
						Value: "table",
						Usage: "Output format: table or jsonl",
					},
				},
				Action: func(ctx context.Context, cmd *cli.Command) error {
					return inboxList(ctx, cmd, stdout)
				},
			},
			{
				Name:      "get",
				Usage:     "Render a received envelope",
				ArgsUsage: "<envelope-id>",
				Flags: []cli.Flag{
					&cli.StringFlag{
						Name:  "format",
						Value: "text",
						Usage: "Output format: text or jsonl",
					},
				},
				Action: func(ctx context.Context, cmd *cli.Command) error {
					return inboxGet(ctx, cmd, stdout)
				},
			},
			{
				Name:      "accept",
				Usage:     "Accept an envelope and store its capsule locally",
				ArgsUsage: "[<envelope-id>]",
				Flags: []cli.Flag{
					&cli.BoolFlag{
						Name:  "all",
						Usage: "Accept all pending envelopes",
					},
					&cli.IntFlag{
						Name:  "limit",
						Usage: "Maximum envelopes to accept with --all",
					},
					&cli.StringFlag{
						Name:  "format",
						Value: "table",
						Usage: "Output format: table or jsonl",
					},
				},
				Action: func(ctx context.Context, cmd *cli.Command) error {
					return inboxAccept(ctx, cmd, stdout)
				},
			},
			{
				Name:  "watch",
				Usage: "Continuously accept pending inbox envelopes",
				Flags: []cli.Flag{
					&cli.StringFlag{
						Name:  "interval",
						Value: "30s",
						Usage: "Polling interval",
					},
					&cli.IntFlag{
						Name:  "limit",
						Usage: "Maximum envelopes to accept per poll",
					},
					&cli.StringFlag{
						Name:  "format",
						Value: "table",
						Usage: "Output format: table or jsonl",
					},
				},
				Action: func(ctx context.Context, cmd *cli.Command) error {
					return inboxWatch(ctx, cmd, stdout)
				},
			},
			{
				Name:      "archive",
				Usage:     "Archive an envelope",
				ArgsUsage: "<envelope-id>",
				Action: func(ctx context.Context, cmd *cli.Command) error {
					return inboxArchive(ctx, cmd, stdout)
				},
			},
		},
	}
}

func newOutboxCommand(stdout io.Writer) *cli.Command {
	return &cli.Command{
		Name:  "outbox",
		Usage: "Inspect sent envelopes",
		Commands: []*cli.Command{
			{
				Name:  "list",
				Usage: "List sent envelopes",
				Flags: []cli.Flag{
					&cli.StringFlag{Name: "status", Usage: "Envelope status filter"},
					&cli.IntFlag{Name: "limit", Usage: "Maximum envelopes to show"},
					&cli.StringFlag{
						Name:  "format",
						Value: "table",
						Usage: "Output format: table or jsonl",
					},
				},
				Action: func(ctx context.Context, cmd *cli.Command) error {
					return outboxList(ctx, cmd, stdout)
				},
			},
			{
				Name:      "get",
				Usage:     "Render a sent envelope",
				ArgsUsage: "<envelope-id>",
				Flags: []cli.Flag{
					&cli.StringFlag{
						Name:  "format",
						Value: "text",
						Usage: "Output format: text or jsonl",
					},
				},
				Action: func(ctx context.Context, cmd *cli.Command) error {
					return outboxGet(ctx, cmd, stdout)
				},
			},
		},
	}
}

func newFriendCommand(stdout io.Writer) *cli.Command {
	return &cli.Command{
		Name:  "friend",
		Usage: "Manage friends for envelope sharing",
		Commands: []*cli.Command{
			{
				Name:      "request",
				Usage:     "Request a friend connection",
				ArgsUsage: "<email>",
				Flags: []cli.Flag{
					&cli.StringFlag{Name: "alias", Usage: "Local alias for this friend"},
					&cli.StringFlag{
						Name:  "format",
						Value: "table",
						Usage: "Output format: table or jsonl",
					},
				},
				Action: func(ctx context.Context, cmd *cli.Command) error {
					return friendRequest(ctx, cmd, stdout)
				},
			},
			{
				Name:  "list",
				Usage: "List friends and friend requests",
				Flags: []cli.Flag{
					&cli.StringFlag{
						Name:  "status",
						Value: "accepted",
						Usage: "Friend status filter",
					},
					&cli.StringFlag{Name: "direction", Usage: "Direction filter: sent or received"},
					&cli.IntFlag{Name: "limit", Usage: "Maximum friends to show"},
					&cli.StringFlag{
						Name:  "format",
						Value: "table",
						Usage: "Output format: table or jsonl",
					},
				},
				Action: func(ctx context.Context, cmd *cli.Command) error {
					return friendList(ctx, cmd, stdout)
				},
			},
			{
				Name:      "accept",
				Usage:     "Accept a friend request",
				ArgsUsage: "<friend-id>",
				Flags: []cli.Flag{
					&cli.StringFlag{Name: "alias", Usage: "Local alias for this friend"},
					&cli.StringFlag{
						Name:  "format",
						Value: "table",
						Usage: "Output format: table or jsonl",
					},
				},
				Action: func(ctx context.Context, cmd *cli.Command) error {
					return friendAccept(ctx, cmd, stdout)
				},
			},
			{
				Name:      "alias",
				Usage:     "Update a friend alias",
				ArgsUsage: "<friend-id> <alias>",
				Flags: []cli.Flag{
					&cli.StringFlag{
						Name:  "format",
						Value: "table",
						Usage: "Output format: table or jsonl",
					},
				},
				Action: func(ctx context.Context, cmd *cli.Command) error {
					return friendAlias(ctx, cmd, stdout)
				},
			},
			{
				Name:      "remove",
				Usage:     "Remove a friend",
				ArgsUsage: "<friend-id>",
				Flags: []cli.Flag{
					&cli.StringFlag{
						Name:  "format",
						Value: "table",
						Usage: "Output format: table or jsonl",
					},
				},
				Action: func(ctx context.Context, cmd *cli.Command) error {
					return friendRemove(ctx, cmd, stdout)
				},
			},
			{
				Name:      "block",
				Usage:     "Block a friend",
				ArgsUsage: "<friend-id>",
				Flags: []cli.Flag{
					&cli.StringFlag{
						Name:  "format",
						Value: "table",
						Usage: "Output format: table or jsonl",
					},
				},
				Action: func(ctx context.Context, cmd *cli.Command) error {
					return friendBlock(ctx, cmd, stdout)
				},
			},
		},
	}
}

func newTeamCommand(stdout io.Writer) *cli.Command {
	formatFlag := func() cli.Flag {
		return &cli.StringFlag{
			Name:  "format",
			Value: "table",
			Usage: "Output format: table or jsonl",
		}
	}
	return &cli.Command{
		Name:  "team",
		Usage: "Read teams and team agents for delivery discovery",
		Commands: []*cli.Command{
			{
				Name:   "list",
				Usage:  "List teams you belong to",
				Flags:  []cli.Flag{formatFlag()},
				Action: func(ctx context.Context, cmd *cli.Command) error { return teamList(ctx, cmd, stdout) },
			},
			{
				Name:      "get",
				Usage:     "Show a single team",
				ArgsUsage: "<team-id>",
				Flags:     []cli.Flag{formatFlag()},
				Action:    func(ctx context.Context, cmd *cli.Command) error { return teamGet(ctx, cmd, stdout) },
			},
			{
				Name:      "agents",
				Usage:     "List team agents (delivery candidates)",
				ArgsUsage: "<team-id> | --all",
				Flags: []cli.Flag{
					&cli.BoolFlag{Name: "all", Usage: "Aggregate agents across all your teams"},
					&cli.BoolFlag{
						Name:  "include-self",
						Usage: "Include agents you own (excluded by default; with --all)",
					},
					&cli.BoolFlag{
						Name:  "online",
						Usage: "Only agents reporting online (with --all)",
					},
					&cli.StringFlag{
						Name:  "agent",
						Usage: "Filter to a single agent id (with --all)",
					},
					formatFlag(),
				},
				Action: func(ctx context.Context, cmd *cli.Command) error {
					return teamAgents(ctx, cmd, stdout)
				},
			},
		},
	}
}

func agentList(
	ctx context.Context,
	cmd *cli.Command,
	agentFacade *facade.AgentFacade,
	stdout io.Writer,
	stderr io.Writer,
	diagnostics io.Writer,
) error {
	req, err := parseListAgentsRequest(cmd)
	if err != nil {
		return fmt.Errorf("parse agent list request: %w", err)
	}
	resp, err := agentFacade.List(
		ctx,
		req,
		facade.WithVerboseWriter(verboseWriter(cmd, stderr, diagnostics)),
	)
	if err != nil {
		return fmt.Errorf("list agents: %w", err)
	}
	if err := renderAgentList(stdout, resp, cmd.String("format")); err != nil {
		return fmt.Errorf("render agent list: %w", err)
	}
	return nil
}

func setupCommand(ctx context.Context, cmd *cli.Command, stdout io.Writer) error {
	req, err := parseSetupRequest(cmd)
	if err != nil {
		return fmt.Errorf("parse setup request: %w", err)
	}
	resp, err := facade.NewSetupFacade().Install(ctx, req)
	if err != nil {
		return fmt.Errorf("setup hooks: %w", err)
	}
	if err := renderSetup(stdout, resp, cmd.String("format")); err != nil {
		return fmt.Errorf("render setup result: %w", err)
	}
	return nil
}

func agentHook(ctx context.Context, cmd *cli.Command, stdout io.Writer) error {
	raw, err := io.ReadAll(os.Stdin)
	if err != nil {
		return fmt.Errorf("read hook input: %w", err)
	}
	agent, err := model.ParseAgentName(cmd.String("agent"))
	if err != nil {
		return fmt.Errorf("parse hook agent: %w", err)
	}
	event := parseAgentHookInput(raw)
	opened, err := store.Open(ctx, &store.OpenRequest{Path: cmd.String("db")})
	if err != nil {
		return fmt.Errorf("open hook store: %w", err)
	}
	defer closeStore(opened.Store)
	sessionFacade := facade.NewSessionFacade(nil, opened.Store)
	hookFacade := facade.NewAgentHookFacadeWithSession(opened.Store, sessionFacade)
	resp, err := hookFacade.Run(ctx, &facade.AgentHookRequest{
		Agent:       agent,
		Event:       cmd.String("event"),
		SessionID:   event.SessionID,
		ProjectPath: event.ProjectPath,
		Prompt:      event.Prompt,
	})
	if err != nil {
		return fmt.Errorf("run agent hook: %w", err)
	}
	if resp == nil || strings.TrimSpace(resp.Message) == "" {
		return nil
	}
	delivered, err := hookFacade.Deliver(ctx, &facade.DeliverAgentHookRequest{
		Agent:       agent,
		SessionID:   event.SessionID,
		InjectionID: resp.Injection.InjectionID,
		Message:     resp.Message,
	})
	if err != nil {
		return fmt.Errorf("deliver agent hook: %w", err)
	}
	if delivered.DeliveryMethod == "stdout" {
		if _, err := fmt.Fprintln(stdout, delivered.Message); err != nil {
			return fmt.Errorf("write hook injection: %w", err)
		}
	}
	if _, err := hookFacade.Complete(ctx, &facade.CompleteAgentHookRequest{
		InjectionID: resp.Injection.InjectionID,
	}); err != nil {
		return fmt.Errorf("complete agent hook: %w", err)
	}
	return nil
}

type agentEnvPayload struct {
	SchemaVersion string            `json:"schemaVersion"`
	Agent         model.AgentName   `json:"agent"`
	Event         string            `json:"event"`
	Env           map[string]string `json:"env"`
	Context       string            `json:"context"`
	AdditionalCtx string            `json:"additionalContext"`
}

func agentEnv(cmd *cli.Command, stdout io.Writer) error {
	agent, err := model.ParseAgentName(cmd.String("agent"))
	if err != nil {
		return fmt.Errorf("parse environment hook agent: %w", err)
	}
	event := strings.TrimSpace(cmd.String("event"))
	if event == "" {
		return fmt.Errorf("parse environment hook event: event is required")
	}
	context := "paxl caller agent: " + string(agent)
	payload := &agentEnvPayload{
		SchemaVersion: "paxl.agent_environment.v1",
		Agent:         agent,
		Event:         event,
		Env: map[string]string{
			"PAXL_CALLER_AGENT": string(agent),
			"PAXL_AGENT":        string(agent),
		},
		Context:       context,
		AdditionalCtx: context,
	}
	if err := json.NewEncoder(stdout).Encode(payload); err != nil {
		return fmt.Errorf("write environment hook payload: %w", err)
	}
	return nil
}

type agentHookInput struct {
	SessionID        string `json:"session_id"`
	SessionIDCamel   string `json:"sessionId"`
	Prompt           string `json:"prompt"`
	UserPrompt       string `json:"user_prompt"`
	UserPromptCamel  string `json:"userPrompt"`
	CWD              string `json:"cwd"`
	Workspace        string `json:"workspace"`
	ProjectID        string `json:"project_id"`
	ProjectIDCamel   string `json:"projectId"`
	ProjectPath      string `json:"project_path"`
	ProjectPathCamel string `json:"projectPath"`
}

func parseAgentHookInput(raw []byte) agentHookInput {
	var input agentHookInput
	if err := json.Unmarshal(raw, &input); err != nil {
		return agentHookInput{
			Prompt:      strings.TrimSpace(string(raw)),
			ProjectPath: currentWorkingDirectory(),
		}
	}
	input.SessionID = firstNonEmpty(input.SessionID, input.SessionIDCamel)
	if strings.TrimSpace(input.Prompt) == "" {
		input.Prompt = firstNonEmpty(input.UserPrompt, input.UserPromptCamel)
	}
	input.ProjectPath = firstNonEmpty(
		input.ProjectPath,
		input.ProjectPathCamel,
		input.ProjectID,
		input.ProjectIDCamel,
		input.Workspace,
		input.CWD,
		currentWorkingDirectory(),
	)
	return input
}

type versionMetadata struct {
	Version string `json:"version"`
	Commit  string `json:"commit"`
	Dirty   string `json:"dirty,omitempty"`
}

func versionCommand(ctx context.Context, cmd *cli.Command, stdout io.Writer) error {
	if cmd.Bool("check") {
		return updateCheck(ctx, cmd, stdout)
	}
	meta := currentVersionMetadata()
	switch cmd.String("format") {
	case "text":
		if _, err := fmt.Fprintf(stdout, "paxl %s\n", meta.Version); err != nil {
			return fmt.Errorf("write version: %w", err)
		}
		if _, err := fmt.Fprintf(
			stdout,
			"commit %s\n",
			firstNonEmpty(meta.Commit, "unknown"),
		); err != nil {
			return fmt.Errorf("write commit: %w", err)
		}
		if meta.Dirty != "" {
			if _, err := fmt.Fprintf(stdout, "dirty %s\n", meta.Dirty); err != nil {
				return fmt.Errorf("write dirty state: %w", err)
			}
		}
		return nil
	case "json":
		if err := json.NewEncoder(stdout).Encode(meta); err != nil {
			return fmt.Errorf("encode version: %w", err)
		}
		return nil
	default:
		return fmt.Errorf("unsupported format %q", cmd.String("format"))
	}
}

func currentVersionMetadata() *versionMetadata {
	meta := &versionMetadata{
		Version: version,
		Commit:  buildCommit,
	}
	if info, ok := debug.ReadBuildInfo(); ok {
		for _, setting := range info.Settings {
			switch setting.Key {
			case "vcs.revision":
				if meta.Commit == "" {
					meta.Commit = setting.Value
				}
			case "vcs.modified":
				meta.Dirty = setting.Value
			}
		}
	}
	return meta
}

func updateCheck(ctx context.Context, cmd *cli.Command, stdout io.Writer) error {
	req, err := parseCheckUpdateRequest(cmd)
	if err != nil {
		return fmt.Errorf("parse update check request: %w", err)
	}
	runCtx, cancel, err := contextWithTimeout(ctx, cmd.String("timeout"))
	if err != nil {
		return fmt.Errorf("parse update check timeout: %w", err)
	}
	defer cancel()
	resp, err := facade.NewUpdateFacade(updateHTTPClient).Check(runCtx, req)
	if err != nil {
		return fmt.Errorf("check update: %w", err)
	}
	if err := renderUpdateCheck(stdout, resp, cmd.String("format")); err != nil {
		return fmt.Errorf("render update check: %w", err)
	}
	return nil
}

type applyUpdateResponse struct {
	CurrentVersion  string              `json:"current_version"`
	LatestVersion   string              `json:"latest_version"`
	Status          facade.UpdateStatus `json:"status"`
	UpdateAvailable bool                `json:"update_available"`
	Updated         bool                `json:"updated"`
	Path            string              `json:"path,omitempty"`
	Platform        string              `json:"platform"`
	SHA256          string              `json:"sha256,omitempty"`
	SizeBytes       int64               `json:"size_bytes,omitempty"`
}

func updateCommand(ctx context.Context, cmd *cli.Command, stdout io.Writer) error {
	req, err := parseCheckUpdateRequest(cmd)
	if err != nil {
		return fmt.Errorf("parse update request: %w", err)
	}
	runCtx, cancel, err := contextWithTimeout(ctx, cmd.String("timeout"))
	if err != nil {
		return fmt.Errorf("parse update timeout: %w", err)
	}
	defer cancel()
	check, err := facade.NewUpdateFacade(updateHTTPClient).Check(runCtx, req)
	if err != nil {
		return fmt.Errorf("check update: %w", err)
	}
	shouldApply := check.UpdateAvailable || check.Status == facade.UpdateStatusDevelopment
	resp := &applyUpdateResponse{
		CurrentVersion:  check.CurrentVersion,
		LatestVersion:   check.LatestVersion,
		Status:          check.Status,
		UpdateAvailable: shouldApply,
		Platform:        check.Platform,
		SHA256:          check.SHA256,
		SizeBytes:       check.SizeBytes,
	}
	if !shouldApply {
		return renderApplyUpdate(stdout, resp, cmd.String("format"))
	}
	binary, err := downloadUpdateBinary(
		runCtx,
		updateHTTPClient,
		check.DownloadURL,
		check.SizeBytes,
	)
	if err != nil {
		return fmt.Errorf("download update: %w", err)
	}
	if err := verifyUpdateBinary(binary, check.SHA256); err != nil {
		return fmt.Errorf("verify update: %w", err)
	}
	path, err := executablePath()
	if err != nil {
		return fmt.Errorf("resolve executable path: %w", err)
	}
	if err := replaceExecutable(path, binary); err != nil {
		return fmt.Errorf("replace executable: %w", err)
	}
	resp.Updated = true
	resp.Path = path
	return renderApplyUpdate(stdout, resp, cmd.String("format"))
}

func downloadUpdateBinary(
	ctx context.Context,
	client facade.UpdateHTTPClient,
	url string,
	expectedSize int64,
) ([]byte, error) {
	if client == nil {
		client = http.DefaultClient
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil) // #nosec G107
	if err != nil {
		return nil, fmt.Errorf("create download request: %w", err)
	}
	req.Header.Set("User-Agent", "paxl-update")
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request download: %w", err)
	}
	defer func() {
		_ = resp.Body.Close()
	}()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("download returned HTTP %d", resp.StatusCode)
	}
	limit := expectedSize + 1
	if limit <= 1 {
		limit = 128 << 20
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, limit))
	if err != nil {
		return nil, fmt.Errorf("read download: %w", err)
	}
	if expectedSize > 0 && int64(len(body)) != expectedSize {
		return nil, fmt.Errorf(
			"download size %d does not match expected %d",
			len(body),
			expectedSize,
		)
	}
	return body, nil
}

func verifyUpdateBinary(binary []byte, expectedSHA string) error {
	sum := sha256.Sum256(binary)
	got := hex.EncodeToString(sum[:])
	if !strings.EqualFold(got, strings.TrimSpace(expectedSHA)) {
		return fmt.Errorf("sha256 %s does not match expected %s", got, expectedSHA)
	}
	return nil
}

func replaceExecutable(path string, binary []byte) error {
	if strings.TrimSpace(path) == "" {
		return fmt.Errorf("executable path is required")
	}
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("stat executable: %w", err)
	}
	dir := filepath.Dir(path)
	base := filepath.Base(path)
	tmp, err := os.CreateTemp(dir, "."+base+".update-*")
	if err != nil {
		return fmt.Errorf("create temp executable: %w", err)
	}
	tmpPath := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpPath)
		}
	}()
	if _, err := tmp.Write(binary); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write temp executable: %w", err)
	}
	if err := tmp.Chmod(info.Mode().Perm() | 0o700); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("chmod temp executable: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp executable: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("rename temp executable: %w", err)
	}
	cleanup = false
	return nil
}

func loginCommand(ctx context.Context, cmd *cli.Command, stdout io.Writer) error {
	timeout, err := parseDuration(cmd.String("timeout"))
	if err != nil {
		return err
	}
	opened, err := store.Open(ctx, &store.OpenRequest{Path: cmd.String("db")})
	if err != nil {
		return fmt.Errorf("open session store: %w", err)
	}
	defer closeStore(opened.Store)
	authFacade := facade.NewAuthFacade(authHTTPClient, opened.Store)
	format := cmd.String("format")
	resp, err := authFacade.Login(ctx, &facade.LoginRequest{
		ManagerURL: cmd.String("manager-url"),
		ClientName: defaultLoginClientName(),
		Timeout:    timeout,
		OnStart: func(start *facade.LoginStart) error {
			if format != "text" {
				return nil
			}
			return renderLoginStart(stdout, start)
		},
	})
	if err != nil {
		return err
	}
	if format == "text" {
		return renderLoginComplete(stdout, resp)
	}
	return renderLogin(stdout, resp, format)
}

func whoamiCommand(ctx context.Context, cmd *cli.Command, stdout io.Writer) error {
	opened, err := store.Open(ctx, &store.OpenRequest{Path: cmd.String("db")})
	if err != nil {
		return fmt.Errorf("open session store: %w", err)
	}
	defer closeStore(opened.Store)
	authFacade := facade.NewAuthFacade(authHTTPClient, opened.Store)
	resp, err := authFacade.Whoami(ctx)
	if err != nil {
		return err
	}
	return renderWhoami(stdout, resp, cmd.String("format"))
}

func logoutCommand(ctx context.Context, cmd *cli.Command, stdout io.Writer) error {
	opened, err := store.Open(ctx, &store.OpenRequest{Path: cmd.String("db")})
	if err != nil {
		return fmt.Errorf("open session store: %w", err)
	}
	defer closeStore(opened.Store)
	authFacade := facade.NewAuthFacade(authHTTPClient, opened.Store)
	resp, err := authFacade.Logout(ctx)
	if err != nil {
		return err
	}
	return renderLogout(stdout, resp, cmd.String("format"))
}

func nodeList(ctx context.Context, cmd *cli.Command, stdout io.Writer) error {
	opened, err := store.Open(ctx, &store.OpenRequest{Path: cmd.String("db")})
	if err != nil {
		return fmt.Errorf("open session store: %w", err)
	}
	defer closeStore(opened.Store)
	nodeFacade := facade.NewNodeFacade(authHTTPClient, opened.Store)
	resp, err := nodeFacade.List(ctx, &facade.ListNodesRequest{})
	if err != nil {
		return fmt.Errorf("list nodes: %w", err)
	}
	return renderNodeList(stdout, resp, cmd.String("format"))
}

func nodeAgentList(ctx context.Context, cmd *cli.Command, stdout io.Writer) error {
	nodeID := cmd.Args().First()
	if nodeID == "" {
		return fmt.Errorf("node id is required")
	}
	opened, err := store.Open(ctx, &store.OpenRequest{Path: cmd.String("db")})
	if err != nil {
		return fmt.Errorf("open session store: %w", err)
	}
	defer closeStore(opened.Store)
	nodeFacade := facade.NewNodeFacade(authHTTPClient, opened.Store)
	resp, err := nodeFacade.ListAgents(ctx, &facade.ListNodeAgentsRequest{NodeID: nodeID})
	if err != nil {
		return fmt.Errorf("list node agents: %w", err)
	}
	return renderNodeAgentList(stdout, resp, cmd.String("format"))
}

func nodeSessionList(ctx context.Context, cmd *cli.Command, stdout io.Writer) error {
	nodeID := cmd.Args().First()
	agentID := cmd.Args().Get(1)
	if nodeID == "" || agentID == "" {
		return fmt.Errorf("node id and agent id are required")
	}
	opened, err := store.Open(ctx, &store.OpenRequest{Path: cmd.String("db")})
	if err != nil {
		return fmt.Errorf("open session store: %w", err)
	}
	defer closeStore(opened.Store)
	nodeFacade := facade.NewNodeFacade(authHTTPClient, opened.Store)
	resp, err := nodeFacade.ListSessions(ctx, &facade.ListNodeSessionsRequest{
		NodeID:  nodeID,
		AgentID: agentID,
	})
	if err != nil {
		return fmt.Errorf("list node sessions: %w", err)
	}
	return renderNodeSessionList(stdout, resp, cmd.String("format"))
}

func sessionList(
	ctx context.Context,
	cmd *cli.Command,
	stdout io.Writer,
	stderr io.Writer,
	diagnostics io.Writer,
) error {
	req, err := parseListSessionsRequest(cmd)
	if err != nil {
		return fmt.Errorf("parse session list request: %w", err)
	}
	runCtx, cancel, err := contextWithTimeout(ctx, cmd.String("timeout"))
	if err != nil {
		return fmt.Errorf("parse session list timeout: %w", err)
	}
	defer cancel()
	opened, err := store.Open(ctx, &store.OpenRequest{Path: cmd.String("db")})
	if err != nil {
		return fmt.Errorf("open session store: %w", err)
	}
	defer closeStore(opened.Store)
	sessionFacade := facade.NewSessionFacade(nil, opened.Store)
	resp, err := sessionFacade.List(
		runCtx,
		req,
		facade.WithVerboseWriter(verboseWriter(cmd, stderr, diagnostics)),
	)
	if err != nil {
		return fmt.Errorf("list sessions: %w", err)
	}
	if err := renderSessionList(stdout, resp, cmd.String("format")); err != nil {
		return fmt.Errorf("render session list: %w", err)
	}
	return nil
}

func sessionGet(
	ctx context.Context,
	cmd *cli.Command,
	stdout io.Writer,
	stderr io.Writer,
	diagnostics io.Writer,
) error {
	req, err := parseGetSessionRequest(cmd)
	if err != nil {
		return fmt.Errorf("parse session get request: %w", err)
	}
	opened, err := store.Open(ctx, &store.OpenRequest{Path: cmd.String("db")})
	if err != nil {
		return fmt.Errorf("open session store: %w", err)
	}
	defer closeStore(opened.Store)
	sessionFacade := facade.NewSessionFacade(nil, opened.Store)
	resp, err := sessionFacade.Get(
		ctx,
		req,
		facade.WithVerboseWriter(verboseWriter(cmd, stderr, diagnostics)),
	)
	if err != nil {
		return fmt.Errorf("get session: %w", err)
	}
	if err := renderSessionTimelineOutput(
		stdout,
		resp,
		cmd.String("format"),
		cmd.String("output"),
	); err != nil {
		return fmt.Errorf("render session timeline: %w", err)
	}
	return nil
}

func sessionMirror(
	ctx context.Context,
	cmd *cli.Command,
	stdout io.Writer,
	stderr io.Writer,
	diagnostics io.Writer,
) error {
	req, err := parseMirrorSessionRequest(cmd)
	if err != nil {
		return fmt.Errorf("parse session mirror request: %w", err)
	}
	runCtx, cancel, err := contextWithTimeout(ctx, cmd.String("timeout"))
	if err != nil {
		return fmt.Errorf("parse session mirror timeout: %w", err)
	}
	defer cancel()
	opened, err := store.Open(ctx, &store.OpenRequest{Path: cmd.String("db")})
	if err != nil {
		return fmt.Errorf("open session store: %w", err)
	}
	defer closeStore(opened.Store)
	capsuleFacade := facade.NewCapsuleFacade(nil, opened.Store)
	resp, err := capsuleFacade.MirrorSession(
		runCtx,
		req,
		facade.WithVerboseWriter(verboseWriter(cmd, stderr, diagnostics)),
	)
	if err != nil {
		return fmt.Errorf("mirror session: %w", err)
	}
	if err := renderMirrorResult(stdout, resp, cmd.String("format")); err != nil {
		return fmt.Errorf("render mirror result: %w", err)
	}
	return nil
}

func sessionQuery(
	ctx context.Context,
	cmd *cli.Command,
	stdout io.Writer,
	stderr io.Writer,
	diagnostics io.Writer,
) error {
	query := strings.TrimSpace(cmd.Args().First())
	if query == "" {
		return fmt.Errorf("search query is required")
	}
	runCtx, cancel, err := contextWithTimeout(ctx, cmd.String("timeout"))
	if err != nil {
		return fmt.Errorf("parse session query timeout: %w", err)
	}
	defer cancel()
	opened, err := store.Open(ctx, &store.OpenRequest{Path: cmd.String("db")})
	if err != nil {
		return fmt.Errorf("open session store: %w", err)
	}
	defer closeStore(opened.Store)
	var agent model.AgentName
	if rawAgent := strings.TrimSpace(cmd.String("agent")); rawAgent != "" {
		parsed, err := model.ParseAgentName(rawAgent)
		if err != nil {
			return fmt.Errorf("parse query agent: %w", err)
		}
		agent = parsed
	}
	sessionFacade := facade.NewSessionFacade(nil, opened.Store)
	resp, err := sessionFacade.Search(
		runCtx,
		&facade.SearchRequest{
			Query:  query,
			Limit:  cmd.Int("limit"),
			NoSync: !cmd.Bool("sync"),
			Agent:  agent,
		},
		facade.WithVerboseWriter(verboseWriter(cmd, stderr, diagnostics)),
	)
	if err != nil {
		return fmt.Errorf("search sessions: %w", err)
	}
	if err := renderSearchResults(stdout, resp, cmd.String("format")); err != nil {
		return err
	}
	if shouldScheduleSessionQueryBackgroundSync(cmd) {
		err := scheduleSessionQueryBackgroundSync(&sessionQueryBackgroundSyncRequest{
			DBPath: cmd.String("db"),
			Agent:  agent,
			Limit:  cmd.Int("limit"),
		})
		if err != nil {
			writeVerbose(
				verboseWriter(cmd, stderr, diagnostics),
				"Session search background sync was not started: %v.",
				err,
			)
		}
	}
	return nil
}

type sessionQueryBackgroundSyncRequest struct {
	DBPath string
	Agent  model.AgentName
	Limit  int
}

const (
	sessionQueryBackgroundSyncQuery   = "__paxl_background_session_index_refresh__"
	sessionQueryBackgroundSyncTimeout = 10 * time.Second
)

func shouldScheduleSessionQueryBackgroundSync(cmd *cli.Command) bool {
	return !cmd.Bool("sync") && !cmd.Bool("no-background-sync")
}

func startSessionQueryBackgroundSync(req *sessionQueryBackgroundSyncRequest) error {
	if req == nil {
		return fmt.Errorf("background session sync request is required")
	}
	executable, err := executablePath()
	if err != nil {
		return fmt.Errorf("resolve executable path: %w", err)
	}
	args := []string{}
	if strings.TrimSpace(req.DBPath) != "" {
		args = append(args, "--db", req.DBPath)
	}
	args = append(args,
		"session",
		"query",
		sessionQueryBackgroundSyncQuery,
		"--sync",
		"--no-background-sync",
		"--format",
		"jsonl",
		"--limit",
		strconv.Itoa(req.Limit),
		"--timeout",
		sessionQueryBackgroundSyncTimeout.String(),
	)
	if req.Agent != "" && req.Agent != model.AgentNameUnknown {
		args = append(args, "--agent", string(req.Agent))
	}
	command := exec.CommandContext(
		context.Background(),
		executable,
		args...) // #nosec G204 -- paxl intentionally re-execs itself.
	command.Stdout = io.Discard
	command.Stderr = io.Discard
	if err := command.Start(); err != nil {
		return fmt.Errorf("start background session sync: %w", err)
	}
	if err := command.Process.Release(); err != nil {
		return fmt.Errorf("release background session sync process: %w", err)
	}
	return nil
}

func writeVerbose(writer io.Writer, format string, args ...any) {
	if writer == nil {
		return
	}
	_, _ = fmt.Fprintf(writer, format+"\n", args...)
}

func renderSearchResults(stdout io.Writer, resp *facade.SearchResponse, format string) error {
	if format == "jsonl" {
		for _, r := range resp.Results {
			obj := map[string]any{
				"session_id":   r.SessionID,
				"agent":        string(r.Agent),
				"title":        r.Title,
				"snippet":      r.Snippet,
				"element_seq":  r.ElementSeq,
				"role":         r.Role,
				"content_text": r.ContentText,
			}
			raw, err := json.Marshal(obj)
			if err != nil {
				return fmt.Errorf("encode search result: %w", err)
			}
			if _, err := fmt.Fprintln(stdout, string(raw)); err != nil {
				return fmt.Errorf("write search result: %w", err)
			}
		}
		return nil
	}
	if len(resp.Results) == 0 {
		if _, err := fmt.Fprintln(stdout, "No matching sessions found."); err != nil {
			return fmt.Errorf("write empty result: %w", err)
		}
		return nil
	}
	for i, r := range resp.Results {
		if i > 0 {
			if _, err := fmt.Fprintln(stdout); err != nil {
				return fmt.Errorf("write separator: %w", err)
			}
		}
		if _, err := fmt.Fprintf(stdout, "[%d] %s - %q\n", i+1, r.SessionID, r.Title); err != nil {
			return fmt.Errorf("write search header: %w", err)
		}
		snippet := r.Snippet
		if snippet == "" {
			snippet = r.ContentText
			if len(snippet) > 80 {
				snippet = snippet[:80] + "..."
			}
		}
		if _, err := fmt.Fprintf(stdout, "    %s\n", snippet); err != nil {
			return fmt.Errorf("write search snippet: %w", err)
		}
	}
	return nil
}

func capsuleCreate(
	ctx context.Context,
	cmd *cli.Command,
	stdin io.Reader,
	stdout io.Writer,
	stderr io.Writer,
	diagnostics io.Writer,
) error {
	req, err := parseCreateCapsuleRequest(cmd, stdin)
	if err != nil {
		return fmt.Errorf("parse capsule create request: %w", err)
	}
	cancelDebug, err := scheduleDebugStack(cmd.String("debug-stack-after"), stderr)
	if err != nil {
		return fmt.Errorf("parse debug-stack-after: %w", err)
	}
	defer cancelDebug()
	runCtx, cancel, err := contextWithTimeout(ctx, cmd.String("timeout"))
	if err != nil {
		return fmt.Errorf("parse capsule create timeout: %w", err)
	}
	defer cancel()
	opened, err := store.Open(ctx, &store.OpenRequest{Path: cmd.String("db")})
	if err != nil {
		return fmt.Errorf("open capsule store: %w", err)
	}
	defer closeStore(opened.Store)
	capsuleFacade := facade.NewCapsuleFacade(nil, opened.Store)
	resp, err := capsuleFacade.Create(
		runCtx,
		req,
		facade.WithVerboseWriter(verboseWriter(cmd, stderr, diagnostics)),
	)
	if err != nil {
		return fmt.Errorf("create capsule: %w", err)
	}
	if err := renderCapsuleList(stdout, &facade.ListCapsulesResponse{
		Capsules: []*model.KnowledgeCapsule{resp.Capsule},
	}, cmd.String("format")); err != nil {
		return fmt.Errorf("render capsule create result: %w", err)
	}
	return nil
}

func capsuleList(ctx context.Context, cmd *cli.Command, stdout io.Writer) error {
	req, err := parseListCapsulesRequest(cmd)
	if err != nil {
		return fmt.Errorf("parse capsule list request: %w", err)
	}
	opened, err := store.Open(ctx, &store.OpenRequest{Path: cmd.String("db")})
	if err != nil {
		return fmt.Errorf("open capsule store: %w", err)
	}
	defer closeStore(opened.Store)
	capsuleFacade := facade.NewCapsuleFacade(nil, opened.Store)
	resp, err := capsuleFacade.List(ctx, req)
	if err != nil {
		return fmt.Errorf("list capsules: %w", err)
	}
	if err := renderCapsuleList(stdout, resp, cmd.String("format")); err != nil {
		return fmt.Errorf("render capsule list: %w", err)
	}
	return nil
}

func capsuleGet(ctx context.Context, cmd *cli.Command, stdout io.Writer) error {
	req, err := parseGetCapsuleRequest(cmd)
	if err != nil {
		return fmt.Errorf("parse capsule get request: %w", err)
	}
	opened, err := store.Open(ctx, &store.OpenRequest{Path: cmd.String("db")})
	if err != nil {
		return fmt.Errorf("open capsule store: %w", err)
	}
	defer closeStore(opened.Store)
	capsuleFacade := facade.NewCapsuleFacade(nil, opened.Store)
	resp, err := capsuleFacade.Get(ctx, req)
	if err != nil {
		return fmt.Errorf("get capsule: %w", err)
	}
	if err := renderCapsule(stdout, resp, cmd.String("format")); err != nil {
		return fmt.Errorf("render capsule: %w", err)
	}
	return nil
}

func capsuleArchive(ctx context.Context, cmd *cli.Command, stdout io.Writer) error {
	req, err := parseArchiveCapsuleRequest(cmd)
	if err != nil {
		return fmt.Errorf("parse capsule archive request: %w", err)
	}
	opened, err := store.Open(ctx, &store.OpenRequest{Path: cmd.String("db")})
	if err != nil {
		return fmt.Errorf("open capsule store: %w", err)
	}
	defer closeStore(opened.Store)
	capsuleFacade := facade.NewCapsuleFacade(nil, opened.Store)
	resp, err := capsuleFacade.Archive(ctx, req)
	if err != nil {
		return fmt.Errorf("archive capsule: %w", err)
	}
	if _, err := fmt.Fprintf(stdout, "Archived %s\n", resp.Capsule.CapsuleID); err != nil {
		return fmt.Errorf("write archive result: %w", err)
	}
	return nil
}

func capsuleSend(ctx context.Context, cmd *cli.Command, stdout io.Writer) error {
	req, err := parseSendEnvelopeRequest(cmd)
	if err != nil {
		return fmt.Errorf("parse capsule send request: %w", err)
	}
	opened, err := store.Open(ctx, &store.OpenRequest{Path: cmd.String("db")})
	if err != nil {
		return fmt.Errorf("open envelope store: %w", err)
	}
	defer closeStore(opened.Store)
	friendFacade := facade.NewFriendFacade(nil, opened.Store)
	if strings.TrimSpace(req.RecipientEmail) != "" {
		resolved, err := friendFacade.ResolveAlias(ctx, &facade.ResolveFriendAliasRequest{
			Alias: req.RecipientEmail,
		})
		if err != nil {
			return fmt.Errorf("resolve friend alias: %w", err)
		}
		req.RecipientEmail = resolved.Email
	}
	envelopeFacade := facade.NewEnvelopeFacade(nil, opened.Store)
	resp, err := envelopeFacade.Send(ctx, req)
	if err != nil {
		return fmt.Errorf("send envelope: %w", err)
	}
	if err := renderEnvelopeList(
		stdout,
		&facade.ListInboxResponse{Envelopes: []*model.Envelope{resp.Envelope}},
		cmd.String("format"),
	); err != nil {
		return fmt.Errorf("render sent envelope: %w", err)
	}
	return nil
}

func capsuleInject(
	ctx context.Context,
	cmd *cli.Command,
	stdout io.Writer,
	stderr io.Writer,
	diagnostics io.Writer,
) error {
	req, err := parseInjectCapsuleRequest(cmd)
	if err != nil {
		return fmt.Errorf("parse capsule inject request: %w", err)
	}
	runCtx, cancel, err := contextWithTimeout(ctx, cmd.String("timeout"))
	if err != nil {
		return fmt.Errorf("parse capsule inject timeout: %w", err)
	}
	defer cancel()
	opened, err := store.Open(ctx, &store.OpenRequest{Path: cmd.String("db")})
	if err != nil {
		return fmt.Errorf("open capsule store: %w", err)
	}
	defer closeStore(opened.Store)
	capsuleFacade := facade.NewCapsuleFacade(nil, opened.Store)
	resp, err := capsuleFacade.Inject(
		runCtx,
		req,
		facade.WithVerboseWriter(verboseWriter(cmd, stderr, diagnostics)),
	)
	if err != nil {
		return fmt.Errorf("inject capsule: %w", err)
	}
	if resp.Injection.Status == "pending" {
		if _, err := fmt.Fprintf(
			stdout,
			"Queued %s for %s hook injection\n",
			resp.Injection.InjectionID,
			firstNonEmpty(string(resp.Injection.TargetAgent), "any agent"),
		); err != nil {
			return fmt.Errorf("write injection route result: %w", err)
		}
		return nil
	}
	if output := strings.TrimSpace(cmd.String("output")); output != "" {
		if err := os.WriteFile(output, []byte(resp.Message+"\n"), 0o600); err != nil {
			return fmt.Errorf("write injection message: %w", err)
		}
		if _, err := fmt.Fprintf(
			stdout,
			"Injected %s into %s and wrote %s\n",
			resp.Injection.InjectionID,
			resp.Injection.TargetSessionID,
			output,
		); err != nil {
			return fmt.Errorf("write injection result: %w", err)
		}
		return nil
	}
	if _, err := fmt.Fprintf(
		stdout,
		"Injected %s into %s\n",
		resp.Injection.InjectionID,
		resp.Injection.TargetSessionID,
	); err != nil {
		return fmt.Errorf("write injection result: %w", err)
	}
	return nil
}

func outboxList(ctx context.Context, cmd *cli.Command, stdout io.Writer) error {
	opened, err := store.Open(ctx, &store.OpenRequest{Path: cmd.String("db")})
	if err != nil {
		return fmt.Errorf("open envelope store: %w", err)
	}
	defer closeStore(opened.Store)
	envelopeFacade := facade.NewEnvelopeFacade(nil, opened.Store)
	resp, err := envelopeFacade.ListOutbox(ctx, &facade.ListOutboxRequest{
		Status: cmd.String("status"),
		Limit:  cmd.Int("limit"),
	})
	if err != nil {
		return fmt.Errorf("list outbox: %w", err)
	}
	if err := renderOutboxEnvelopeList(stdout, resp, cmd.String("format")); err != nil {
		return fmt.Errorf("render outbox: %w", err)
	}
	return nil
}

func outboxGet(ctx context.Context, cmd *cli.Command, stdout io.Writer) error {
	req, err := parseGetEnvelopeRequest(cmd)
	if err != nil {
		return fmt.Errorf("parse outbox get request: %w", err)
	}
	opened, err := store.Open(ctx, &store.OpenRequest{Path: cmd.String("db")})
	if err != nil {
		return fmt.Errorf("open envelope store: %w", err)
	}
	defer closeStore(opened.Store)
	envelopeFacade := facade.NewEnvelopeFacade(nil, opened.Store)
	resp, err := envelopeFacade.Get(ctx, req)
	if err != nil {
		return fmt.Errorf("get envelope: %w", err)
	}
	if err := renderEnvelope(stdout, resp.Envelope, cmd.String("format")); err != nil {
		return fmt.Errorf("render envelope: %w", err)
	}
	return nil
}

func inboxList(ctx context.Context, cmd *cli.Command, stdout io.Writer) error {
	opened, err := store.Open(ctx, &store.OpenRequest{Path: cmd.String("db")})
	if err != nil {
		return fmt.Errorf("open envelope store: %w", err)
	}
	defer closeStore(opened.Store)
	envelopeFacade := facade.NewEnvelopeFacade(nil, opened.Store)
	resp, err := envelopeFacade.ListInbox(ctx, &facade.ListInboxRequest{
		Status: cmd.String("status"),
		Limit:  cmd.Int("limit"),
	})
	if err != nil {
		return fmt.Errorf("list inbox: %w", err)
	}
	if err := renderEnvelopeList(stdout, resp, cmd.String("format")); err != nil {
		return fmt.Errorf("render inbox: %w", err)
	}
	return nil
}

func inboxGet(ctx context.Context, cmd *cli.Command, stdout io.Writer) error {
	req, err := parseGetEnvelopeRequest(cmd)
	if err != nil {
		return fmt.Errorf("parse inbox get request: %w", err)
	}
	opened, err := store.Open(ctx, &store.OpenRequest{Path: cmd.String("db")})
	if err != nil {
		return fmt.Errorf("open envelope store: %w", err)
	}
	defer closeStore(opened.Store)
	envelopeFacade := facade.NewEnvelopeFacade(nil, opened.Store)
	resp, err := envelopeFacade.Get(ctx, req)
	if err != nil {
		return fmt.Errorf("get envelope: %w", err)
	}
	if err := renderEnvelope(stdout, resp.Envelope, cmd.String("format")); err != nil {
		return fmt.Errorf("render envelope: %w", err)
	}
	return nil
}

func inboxAccept(ctx context.Context, cmd *cli.Command, stdout io.Writer) error {
	if cmd.Bool("all") {
		return inboxAcceptAll(ctx, cmd, stdout)
	}
	req, err := parseAcceptEnvelopeRequest(cmd)
	if err != nil {
		return fmt.Errorf("parse inbox accept request: %w", err)
	}
	opened, err := store.Open(ctx, &store.OpenRequest{Path: cmd.String("db")})
	if err != nil {
		return fmt.Errorf("open envelope store: %w", err)
	}
	defer closeStore(opened.Store)
	envelopeFacade := facade.NewEnvelopeFacade(nil, opened.Store)
	resp, err := envelopeFacade.Accept(ctx, req)
	if err != nil {
		return fmt.Errorf("accept envelope: %w", err)
	}
	if err := renderAcceptEnvelope(stdout, resp, cmd.String("format")); err != nil {
		return fmt.Errorf("render accepted envelope: %w", err)
	}
	return nil
}

func inboxAcceptAll(ctx context.Context, cmd *cli.Command, stdout io.Writer) error {
	switch cmd.String("format") {
	case "table", "jsonl":
	default:
		return fmt.Errorf("unsupported format %q", cmd.String("format"))
	}
	opened, err := store.Open(ctx, &store.OpenRequest{Path: cmd.String("db")})
	if err != nil {
		return fmt.Errorf("open envelope store: %w", err)
	}
	defer closeStore(opened.Store)
	envelopeFacade := facade.NewEnvelopeFacade(nil, opened.Store)
	resp, err := envelopeFacade.AcceptAll(ctx, &facade.AcceptAllEnvelopesRequest{
		Status:          "pending",
		Limit:           cmd.Int("limit"),
		ContinueOnError: true,
	})
	if err != nil {
		return fmt.Errorf("accept all envelopes: %w", err)
	}
	if err := renderAcceptAllEnvelopes(stdout, resp, cmd.String("format")); err != nil {
		return fmt.Errorf("render accepted envelopes: %w", err)
	}
	if len(resp.Failures) > 0 {
		return fmt.Errorf("accept all envelopes: %d failed", len(resp.Failures))
	}
	return nil
}

func inboxWatch(ctx context.Context, cmd *cli.Command, stdout io.Writer) error {
	switch cmd.String("format") {
	case "table", "jsonl":
	default:
		return fmt.Errorf("unsupported format %q", cmd.String("format"))
	}
	interval, err := parseWatchInterval(cmd.String("interval"))
	if err != nil {
		return err
	}
	opened, err := store.Open(ctx, &store.OpenRequest{Path: cmd.String("db")})
	if err != nil {
		return fmt.Errorf("open envelope store: %w", err)
	}
	defer closeStore(opened.Store)
	envelopeFacade := facade.NewEnvelopeFacade(nil, opened.Store)
	return watchInbox(ctx, &watchInboxRequest{
		Facade:   envelopeFacade,
		Stdout:   stdout,
		Format:   cmd.String("format"),
		Interval: interval,
		Limit:    cmd.Int("limit"),
	})
}

type watchInboxRequest struct {
	Facade   *facade.EnvelopeFacade
	Stdout   io.Writer
	Format   string
	Interval time.Duration
	Limit    int
}

func watchInbox(ctx context.Context, req *watchInboxRequest) error {
	if req == nil || req.Facade == nil {
		return fmt.Errorf("watch inbox: envelope facade is required")
	}
	interval := req.Interval
	if interval <= 0 {
		interval = 30 * time.Second
	}
	if err := renderInboxWatchEvent(
		req.Stdout,
		req.Format,
		"started",
		interval.String(),
		0,
	); err != nil {
		return err
	}
	if err := acceptInboxWatchCycle(ctx, req); err != nil {
		return err
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if err := acceptInboxWatchCycle(ctx, req); err != nil {
				return err
			}
		}
	}
}

func acceptInboxWatchCycle(ctx context.Context, req *watchInboxRequest) error {
	resp, err := req.Facade.AcceptAll(ctx, &facade.AcceptAllEnvelopesRequest{
		Status:          "pending",
		Limit:           req.Limit,
		ContinueOnError: true,
	})
	if err != nil {
		return fmt.Errorf("accept pending envelopes: %w", err)
	}
	if len(resp.Accepted) == 0 && len(resp.Failures) == 0 {
		return nil
	}
	total := len(resp.Accepted) + len(resp.Failures)
	if err := renderInboxWatchEvent(
		req.Stdout,
		req.Format,
		"received",
		"",
		total,
	); err != nil {
		return err
	}
	if err := renderAcceptAllEnvelopes(req.Stdout, resp, req.Format); err != nil {
		return err
	}
	if len(resp.Accepted) > 0 {
		if err := renderInboxWatchEvent(
			req.Stdout,
			req.Format,
			"auto_accepted",
			"",
			len(resp.Accepted),
		); err != nil {
			return err
		}
	}
	if len(resp.Failures) > 0 {
		if err := renderInboxWatchEvent(
			req.Stdout,
			req.Format,
			"failed",
			"",
			len(resp.Failures),
		); err != nil {
			return err
		}
		return fmt.Errorf("accept pending envelopes: %d failed", len(resp.Failures))
	}
	return nil
}

func parseWatchInterval(raw string) (time.Duration, error) {
	interval, err := time.ParseDuration(strings.TrimSpace(raw))
	if err != nil {
		return 0, fmt.Errorf("parse watch interval: %w", err)
	}
	if interval <= 0 {
		return 0, fmt.Errorf("watch interval must be positive")
	}
	return interval, nil
}

func inboxArchive(ctx context.Context, cmd *cli.Command, stdout io.Writer) error {
	req, err := parseArchiveEnvelopeRequest(cmd)
	if err != nil {
		return fmt.Errorf("parse inbox archive request: %w", err)
	}
	opened, err := store.Open(ctx, &store.OpenRequest{Path: cmd.String("db")})
	if err != nil {
		return fmt.Errorf("open envelope store: %w", err)
	}
	defer closeStore(opened.Store)
	envelopeFacade := facade.NewEnvelopeFacade(nil, opened.Store)
	resp, err := envelopeFacade.Archive(ctx, req)
	if err != nil {
		return fmt.Errorf("archive envelope: %w", err)
	}
	if _, err := fmt.Fprintf(stdout, "Archived %s\n", resp.Envelope.EnvelopeID); err != nil {
		return fmt.Errorf("write archive result: %w", err)
	}
	return nil
}

func friendRequest(ctx context.Context, cmd *cli.Command, stdout io.Writer) error {
	req, err := parseRequestFriendRequest(cmd)
	if err != nil {
		return fmt.Errorf("parse friend request: %w", err)
	}
	opened, err := store.Open(ctx, &store.OpenRequest{Path: cmd.String("db")})
	if err != nil {
		return fmt.Errorf("open friend store: %w", err)
	}
	defer closeStore(opened.Store)
	friendFacade := facade.NewFriendFacade(nil, opened.Store)
	resp, err := friendFacade.Request(ctx, req)
	if err != nil {
		return fmt.Errorf("request friend: %w", err)
	}
	return renderFriendList(
		stdout,
		&facade.ListFriendsResponse{Friends: []*model.Friend{resp.Friend}},
		cmd.String("format"),
	)
}

func friendList(ctx context.Context, cmd *cli.Command, stdout io.Writer) error {
	req, err := parseListFriendsRequest(cmd)
	if err != nil {
		return fmt.Errorf("parse friend list: %w", err)
	}
	opened, err := store.Open(ctx, &store.OpenRequest{Path: cmd.String("db")})
	if err != nil {
		return fmt.Errorf("open friend store: %w", err)
	}
	defer closeStore(opened.Store)
	friendFacade := facade.NewFriendFacade(nil, opened.Store)
	resp, err := friendFacade.List(ctx, req)
	if err != nil {
		return fmt.Errorf("list friends: %w", err)
	}
	return renderFriendList(stdout, resp, cmd.String("format"))
}

func friendAccept(ctx context.Context, cmd *cli.Command, stdout io.Writer) error {
	req, err := parseAcceptFriendRequest(cmd)
	if err != nil {
		return fmt.Errorf("parse friend accept: %w", err)
	}
	opened, err := store.Open(ctx, &store.OpenRequest{Path: cmd.String("db")})
	if err != nil {
		return fmt.Errorf("open friend store: %w", err)
	}
	defer closeStore(opened.Store)
	friendFacade := facade.NewFriendFacade(nil, opened.Store)
	resp, err := friendFacade.Accept(ctx, req)
	if err != nil {
		return fmt.Errorf("accept friend: %w", err)
	}
	return renderFriendList(
		stdout,
		&facade.ListFriendsResponse{Friends: []*model.Friend{resp.Friend}, UserID: resp.UserID},
		cmd.String("format"),
	)
}

func friendAlias(ctx context.Context, cmd *cli.Command, stdout io.Writer) error {
	req, err := parseUpdateFriendAliasRequest(cmd)
	if err != nil {
		return fmt.Errorf("parse friend alias: %w", err)
	}
	opened, err := store.Open(ctx, &store.OpenRequest{Path: cmd.String("db")})
	if err != nil {
		return fmt.Errorf("open friend store: %w", err)
	}
	defer closeStore(opened.Store)
	friendFacade := facade.NewFriendFacade(nil, opened.Store)
	resp, err := friendFacade.UpdateAlias(ctx, req)
	if err != nil {
		return fmt.Errorf("update friend alias: %w", err)
	}
	return renderFriendList(
		stdout,
		&facade.ListFriendsResponse{Friends: []*model.Friend{resp.Friend}, UserID: resp.UserID},
		cmd.String("format"),
	)
}

func friendRemove(ctx context.Context, cmd *cli.Command, stdout io.Writer) error {
	req, err := parseRemoveFriendRequest(cmd)
	if err != nil {
		return fmt.Errorf("parse friend remove: %w", err)
	}
	opened, err := store.Open(ctx, &store.OpenRequest{Path: cmd.String("db")})
	if err != nil {
		return fmt.Errorf("open friend store: %w", err)
	}
	defer closeStore(opened.Store)
	friendFacade := facade.NewFriendFacade(nil, opened.Store)
	resp, err := friendFacade.Remove(ctx, req)
	if err != nil {
		return fmt.Errorf("remove friend: %w", err)
	}
	return renderFriendList(
		stdout,
		&facade.ListFriendsResponse{Friends: []*model.Friend{resp.Friend}, UserID: resp.UserID},
		cmd.String("format"),
	)
}

func friendBlock(ctx context.Context, cmd *cli.Command, stdout io.Writer) error {
	req, err := parseBlockFriendRequest(cmd)
	if err != nil {
		return fmt.Errorf("parse friend block: %w", err)
	}
	opened, err := store.Open(ctx, &store.OpenRequest{Path: cmd.String("db")})
	if err != nil {
		return fmt.Errorf("open friend store: %w", err)
	}
	defer closeStore(opened.Store)
	friendFacade := facade.NewFriendFacade(nil, opened.Store)
	resp, err := friendFacade.Block(ctx, req)
	if err != nil {
		return fmt.Errorf("block friend: %w", err)
	}
	return renderFriendList(
		stdout,
		&facade.ListFriendsResponse{Friends: []*model.Friend{resp.Friend}, UserID: resp.UserID},
		cmd.String("format"),
	)
}

func teamList(ctx context.Context, cmd *cli.Command, stdout io.Writer) error {
	req, err := parseListTeamsRequest(cmd)
	if err != nil {
		return fmt.Errorf("parse team list: %w", err)
	}
	opened, err := store.Open(ctx, &store.OpenRequest{Path: cmd.String("db")})
	if err != nil {
		return fmt.Errorf("open team store: %w", err)
	}
	defer closeStore(opened.Store)
	teamFacade := facade.NewTeamFacade(nil, opened.Store)
	resp, err := teamFacade.ListTeams(ctx, req)
	if err != nil {
		return fmt.Errorf("list teams: %w", err)
	}
	return renderTeamList(stdout, resp, cmd.String("format"))
}

func teamGet(ctx context.Context, cmd *cli.Command, stdout io.Writer) error {
	req, err := parseGetTeamRequest(cmd)
	if err != nil {
		return fmt.Errorf("parse team get: %w", err)
	}
	opened, err := store.Open(ctx, &store.OpenRequest{Path: cmd.String("db")})
	if err != nil {
		return fmt.Errorf("open team store: %w", err)
	}
	defer closeStore(opened.Store)
	teamFacade := facade.NewTeamFacade(nil, opened.Store)
	resp, err := teamFacade.GetTeam(ctx, req)
	if err != nil {
		return fmt.Errorf("get team: %w", err)
	}
	return renderTeamDetail(stdout, resp, cmd.String("format"))
}

func teamAgents(ctx context.Context, cmd *cli.Command, stdout io.Writer) error {
	singleReq, allReq, err := parseTeamAgentsRequest(cmd)
	if err != nil {
		return fmt.Errorf("parse team agents: %w", err)
	}
	opened, err := store.Open(ctx, &store.OpenRequest{Path: cmd.String("db")})
	if err != nil {
		return fmt.Errorf("open team store: %w", err)
	}
	defer closeStore(opened.Store)
	teamFacade := facade.NewTeamFacade(nil, opened.Store)
	if allReq != nil {
		resp, err := teamFacade.ListAllAgents(ctx, allReq)
		if err != nil {
			return fmt.Errorf("list all team agents: %w", err)
		}
		return renderAggregatedTeamAgents(stdout, resp, cmd.String("format"))
	}
	resp, err := teamFacade.ListAgents(ctx, singleReq)
	if err != nil {
		return fmt.Errorf("list team agents: %w", err)
	}
	return renderTeamAgents(stdout, resp, cmd.String("format"))
}

func capsuleInjectionList(ctx context.Context, cmd *cli.Command, stdout io.Writer) error {
	req, err := parseListInjectionsRequest(cmd)
	if err != nil {
		return fmt.Errorf("parse capsule injection request: %w", err)
	}
	opened, err := store.Open(ctx, &store.OpenRequest{Path: cmd.String("db")})
	if err != nil {
		return fmt.Errorf("open capsule store: %w", err)
	}
	defer closeStore(opened.Store)
	capsuleFacade := facade.NewCapsuleFacade(nil, opened.Store)
	resp, err := capsuleFacade.ListInjections(ctx, req)
	if err != nil {
		return fmt.Errorf("list capsule injections: %w", err)
	}
	if err := renderInjectionList(stdout, resp, cmd.String("format")); err != nil {
		return fmt.Errorf("render capsule injections: %w", err)
	}
	return nil
}

func parseListSessionsRequest(cmd *cli.Command) (*facade.ListSessionsRequest, error) {
	updatedSince, hasUpdatedSince, err := parseUpdatedSince(cmd.String("updated-since"))
	if err != nil {
		return nil, err
	}
	req := &facade.ListSessionsRequest{
		Limit:  cmd.Int("limit"),
		NoSync: cmd.Bool("no-sync"),
	}
	if hasUpdatedSince {
		req.UpdatedSince = &updatedSince
	}
	if rawAgent := strings.TrimSpace(cmd.String("agent")); rawAgent != "" {
		agents, err := parseAgentSelection(rawAgent)
		if err != nil {
			return nil, fmt.Errorf("parse agent: %w", err)
		}
		req.Agents = agents
	}
	switch cmd.String("format") {
	case "table", "jsonl", "html":
		return req, nil
	default:
		return nil, fmt.Errorf("unsupported format %q", cmd.String("format"))
	}
}

func parseGetSessionRequest(cmd *cli.Command) (*facade.GetSessionRequest, error) {
	sessionID := cmd.Args().First()
	if sessionID == "" {
		return nil, fmt.Errorf("session id is required")
	}
	req := &facade.GetSessionRequest{ID: sessionID}
	if rawAgent := strings.TrimSpace(cmd.String("agent")); rawAgent != "" {
		agent, err := parseActiveAgentName(rawAgent)
		if err != nil {
			return nil, fmt.Errorf("parse agent: %w", err)
		}
		req.Agent = agent
	}
	switch cmd.String("format") {
	case "transcript", "jsonl", "html":
		return req, nil
	default:
		return nil, fmt.Errorf("unsupported format %q", cmd.String("format"))
	}
}

func parseMirrorSessionRequest(cmd *cli.Command) (*facade.MirrorSessionRequest, error) {
	sourceID := cmd.Args().First()
	if sourceID == "" {
		return nil, fmt.Errorf("source session id is required")
	}
	req := &facade.MirrorSessionRequest{
		SourceSessionID: sourceID,
		TargetSessionID: strings.TrimSpace(cmd.String("to-session")),
	}
	if rawAgent := strings.TrimSpace(cmd.String("agent")); rawAgent != "" {
		agent, err := parseActiveAgentName(rawAgent)
		if err != nil {
			return nil, fmt.Errorf("parse source agent: %w", err)
		}
		req.Agent = agent
	}
	if rawTargetAgent := strings.TrimSpace(cmd.String("to")); rawTargetAgent != "" {
		agent, err := parseActiveAgentName(rawTargetAgent)
		if err != nil {
			return nil, fmt.Errorf("parse target agent: %w", err)
		}
		req.TargetAgent = agent
	}
	if req.TargetSessionID == "" && req.TargetAgent == "" {
		return nil, fmt.Errorf("target agent is required when target session is omitted")
	}
	switch cmd.String("format") {
	case "table", "jsonl":
		return req, nil
	default:
		return nil, fmt.Errorf("unsupported format %q", cmd.String("format"))
	}
}

func parseCreateCapsuleRequest(
	cmd *cli.Command,
	stdin io.Reader,
) (*facade.CreateCapsuleRequest, error) {
	sourceID := cmd.Args().First()
	manual := cmd.Bool("manual")
	if sourceID == "" && !manual {
		return nil, fmt.Errorf("source session id is required")
	}
	if sourceID != "" && manual {
		return nil, fmt.Errorf("source session id cannot be used with manual capsule creation")
	}
	content, err := readCapsuleContent(cmd, stdin)
	if err != nil {
		return nil, err
	}
	req := &facade.CreateCapsuleRequest{
		SourceSessionID: sourceID,
		Keyword:         strings.TrimSpace(cmd.String("keyword")),
		Title:           strings.TrimSpace(cmd.String("title")),
		Summary:         strings.TrimSpace(cmd.String("summary")),
		Content:         content,
		Manual:          manual,
		Local:           cmd.Bool("local"),
	}
	if req.Manual && req.Local {
		return nil, fmt.Errorf("manual capsule creation cannot be used with local extraction")
	}
	if req.Manual && strings.TrimSpace(cmd.String("agent")) != "" {
		return nil, fmt.Errorf("agent cannot be used with manual capsule creation")
	}
	if req.Manual && strings.TrimSpace(req.Content) == "" {
		return nil, fmt.Errorf("content is required for manual capsule creation")
	}
	if req.Content != "" && req.Local {
		return nil, fmt.Errorf("content cannot be used with local extraction")
	}
	if rawAgent := strings.TrimSpace(cmd.String("agent")); rawAgent != "" {
		agent, err := parseActiveAgentName(rawAgent)
		if err != nil {
			return nil, fmt.Errorf("parse agent: %w", err)
		}
		req.Agent = agent
	}
	if req.Keyword == "" {
		return nil, fmt.Errorf("keyword is required")
	}
	switch cmd.String("format") {
	case "table", "jsonl":
		return req, nil
	default:
		return nil, fmt.Errorf("unsupported format %q", cmd.String("format"))
	}
}

func readCapsuleContent(cmd *cli.Command, stdin io.Reader) (string, error) {
	if cmd.IsSet("content") {
		return cmd.String("content"), nil
	}
	if !cmd.Bool("manual") {
		return "", nil
	}
	if file, ok := stdin.(*os.File); ok {
		info, err := file.Stat()
		if err == nil && info.Mode()&os.ModeCharDevice != 0 {
			return "", nil
		}
	}
	raw, err := io.ReadAll(stdin)
	if err != nil {
		return "", fmt.Errorf("read stdin content: %w", err)
	}
	if strings.TrimSpace(string(raw)) == "" {
		return "", nil
	}
	return string(raw), nil
}

func parseListCapsulesRequest(cmd *cli.Command) (*facade.ListCapsulesRequest, error) {
	req := &facade.ListCapsulesRequest{
		Status:          cmd.String("status"),
		Keyword:         cmd.String("keyword"),
		SourceSessionID: cmd.String("source-session"),
		Limit:           cmd.Int("limit"),
	}
	switch cmd.String("format") {
	case "table", "jsonl":
		return req, nil
	default:
		return nil, fmt.Errorf("unsupported format %q", cmd.String("format"))
	}
}

func parseGetCapsuleRequest(cmd *cli.Command) (*facade.GetCapsuleRequest, error) {
	capsuleID := cmd.Args().First()
	if capsuleID == "" {
		return nil, fmt.Errorf("capsule id is required")
	}
	switch cmd.String("format") {
	case "text", "jsonl":
		return &facade.GetCapsuleRequest{CapsuleID: capsuleID}, nil
	default:
		return nil, fmt.Errorf("unsupported format %q", cmd.String("format"))
	}
}

func parseArchiveCapsuleRequest(cmd *cli.Command) (*facade.ArchiveCapsuleRequest, error) {
	capsuleID := cmd.Args().First()
	if capsuleID == "" {
		return nil, fmt.Errorf("capsule id is required")
	}
	return &facade.ArchiveCapsuleRequest{CapsuleID: capsuleID}, nil
}

func parseSendEnvelopeRequest(cmd *cli.Command) (*facade.SendEnvelopeRequest, error) {
	capsuleID := cmd.Args().First()
	if capsuleID == "" {
		return nil, fmt.Errorf("capsule id is required")
	}
	recipient := strings.TrimSpace(cmd.String("to"))
	toAgentID := strings.TrimSpace(cmd.String("to-agent-id"))
	fromAgentID := strings.TrimSpace(cmd.String("from-agent-id"))
	if recipient == "" && toAgentID == "" {
		return nil, fmt.Errorf("recipient friend alias or --to-agent-id is required")
	}
	if recipient != "" && !strings.HasPrefix(recipient, "@") {
		return nil, fmt.Errorf("recipient must be an accepted friend alias like @alice")
	}
	switch cmd.String("format") {
	case "table", "jsonl":
	default:
		return nil, fmt.Errorf("unsupported format %q", cmd.String("format"))
	}
	req := &facade.SendEnvelopeRequest{
		CapsuleID:      capsuleID,
		RecipientEmail: recipient,
		Message:        cmd.String("message"),
		FromAgentID:    fromAgentID,
		ToAgentID:      toAgentID,
	}
	if rawCallerAgent := strings.TrimSpace(cmd.String("caller-agent")); rawCallerAgent != "" {
		callerAgent, err := parseActiveAgentName(rawCallerAgent)
		if err != nil {
			return nil, fmt.Errorf("parse caller agent: %w", err)
		}
		req.CallerAgent = callerAgent
	}
	if rawAgent := strings.TrimSpace(cmd.String("agent")); rawAgent != "" {
		agent, err := parseActiveAgentName(rawAgent)
		if err != nil {
			return nil, fmt.Errorf("parse agent: %w", err)
		}
		req.TargetAgent = agent
	}
	if rawMatch := strings.TrimSpace(cmd.String("match")); rawMatch != "" {
		matchValue, err := parseInjectRouteMatchValue(cmd, rawMatch)
		if err != nil {
			return nil, err
		}
		req.MatchType = rawMatch
		req.MatchValue = matchValue
		return req, nil
	}
	if req.TargetAgent != "" {
		return nil, fmt.Errorf("--agent requires --match")
	}
	if strings.TrimSpace(cmd.String("project")) != "" {
		return nil, fmt.Errorf("--project requires --match project")
	}
	if strings.TrimSpace(cmd.String("keyword")) != "" {
		return nil, fmt.Errorf("--keyword requires --match keyword")
	}
	return req, nil
}

func parseInjectCapsuleRequest(cmd *cli.Command) (*facade.InjectCapsuleRequest, error) {
	if cmd.Args().Len() < 1 {
		return nil, fmt.Errorf("capsule id is required")
	}
	req := &facade.InjectCapsuleRequest{
		CapsuleID:       cmd.Args().Get(0),
		TargetSessionID: cmd.Args().Get(1),
		NewSession:      cmd.Bool("new"),
		ActionItems:     cmd.StringSlice("action-items"),
	}
	if rawAgent := strings.TrimSpace(cmd.String("agent")); rawAgent != "" {
		agent, err := parseActiveAgentName(rawAgent)
		if err != nil {
			return nil, fmt.Errorf("parse agent: %w", err)
		}
		req.Agent = agent
	}
	if rawMatch := strings.TrimSpace(cmd.String("match")); rawMatch != "" {
		matchValue, err := parseInjectRouteMatchValue(cmd, rawMatch)
		if err != nil {
			return nil, err
		}
		if req.NewSession {
			return nil, fmt.Errorf("--new cannot be combined with --match")
		}
		if req.TargetSessionID != "" {
			return nil, fmt.Errorf("target session id must be omitted when --match is set")
		}
		if strings.TrimSpace(cmd.String("output")) != "" {
			return nil, fmt.Errorf("--output cannot be combined with --match")
		}
		req.MatchType = rawMatch
		req.MatchValue = matchValue
		return req, nil
	}
	if req.NewSession && req.TargetSessionID != "" {
		return nil, fmt.Errorf("target session id must be omitted when --new is set")
	}
	if req.NewSession && req.Agent == "" {
		return nil, fmt.Errorf("agent is required when --new is set")
	}
	if !req.NewSession && req.TargetSessionID == "" {
		return nil, fmt.Errorf("target session id is required unless --new is set")
	}
	if !req.NewSession && strings.TrimSpace(cmd.String("output")) != "" {
		return nil, fmt.Errorf(
			"--output cannot be combined with existing target session hook injection",
		)
	}
	return req, nil
}

func parseInjectRouteMatchValue(cmd *cli.Command, matchType string) (string, error) {
	switch matchType {
	case "any":
		return "", nil
	case "project":
		project := strings.TrimSpace(cmd.String("project"))
		if project == "" {
			return "", fmt.Errorf("--project is required when --match project is set")
		}
		return project, nil
	case "keyword":
		keyword := strings.TrimSpace(cmd.String("keyword"))
		if keyword == "" {
			return "", fmt.Errorf("--keyword is required when --match keyword is set")
		}
		return keyword, nil
	default:
		return "", fmt.Errorf("unsupported match %q", matchType)
	}
}

func parseGetEnvelopeRequest(cmd *cli.Command) (*facade.GetEnvelopeRequest, error) {
	envelopeID := cmd.Args().First()
	if envelopeID == "" {
		return nil, fmt.Errorf("envelope id is required")
	}
	switch cmd.String("format") {
	case "text", "jsonl":
		return &facade.GetEnvelopeRequest{EnvelopeID: envelopeID}, nil
	default:
		return nil, fmt.Errorf("unsupported format %q", cmd.String("format"))
	}
}

func parseAcceptEnvelopeRequest(cmd *cli.Command) (*facade.AcceptEnvelopeRequest, error) {
	envelopeID := cmd.Args().First()
	if envelopeID == "" {
		return nil, fmt.Errorf("envelope id is required")
	}
	switch cmd.String("format") {
	case "table", "jsonl":
		return &facade.AcceptEnvelopeRequest{EnvelopeID: envelopeID}, nil
	default:
		return nil, fmt.Errorf("unsupported format %q", cmd.String("format"))
	}
}

func parseArchiveEnvelopeRequest(cmd *cli.Command) (*facade.ArchiveEnvelopeRequest, error) {
	envelopeID := cmd.Args().First()
	if envelopeID == "" {
		return nil, fmt.Errorf("envelope id is required")
	}
	return &facade.ArchiveEnvelopeRequest{EnvelopeID: envelopeID}, nil
}

func parseRequestFriendRequest(cmd *cli.Command) (*facade.RequestFriendRequest, error) {
	email := strings.TrimSpace(cmd.Args().First())
	if email == "" {
		return nil, fmt.Errorf("email is required")
	}
	if err := validateFormat(cmd.String("format"), "table", "jsonl"); err != nil {
		return nil, err
	}
	return &facade.RequestFriendRequest{
		Email: email,
		Alias: strings.TrimSpace(cmd.String("alias")),
	}, nil
}

func parseListFriendsRequest(cmd *cli.Command) (*facade.ListFriendsRequest, error) {
	if err := validateFormat(cmd.String("format"), "table", "jsonl"); err != nil {
		return nil, err
	}
	direction := strings.TrimSpace(cmd.String("direction"))
	switch direction {
	case "", "sent", "received":
	default:
		return nil, fmt.Errorf("unsupported direction %q", direction)
	}
	return &facade.ListFriendsRequest{
		Status:    strings.TrimSpace(cmd.String("status")),
		Direction: direction,
		Limit:     cmd.Int("limit"),
	}, nil
}

func parseAcceptFriendRequest(cmd *cli.Command) (*facade.AcceptFriendRequest, error) {
	friendID := cmd.Args().First()
	if friendID == "" {
		return nil, fmt.Errorf("friend id is required")
	}
	if err := validateFormat(cmd.String("format"), "table", "jsonl"); err != nil {
		return nil, err
	}
	return &facade.AcceptFriendRequest{
		FriendID: friendID,
		Alias:    strings.TrimSpace(cmd.String("alias")),
	}, nil
}

func parseUpdateFriendAliasRequest(cmd *cli.Command) (*facade.UpdateFriendAliasRequest, error) {
	friendID := strings.TrimSpace(cmd.Args().First())
	if friendID == "" {
		return nil, fmt.Errorf("friend id is required")
	}
	alias := strings.TrimSpace(cmd.Args().Get(1))
	if alias == "" {
		return nil, fmt.Errorf("alias is required")
	}
	if err := validateFormat(cmd.String("format"), "table", "jsonl"); err != nil {
		return nil, err
	}
	return &facade.UpdateFriendAliasRequest{
		FriendID: friendID,
		Alias:    alias,
	}, nil
}

func parseRemoveFriendRequest(cmd *cli.Command) (*facade.RemoveFriendRequest, error) {
	friendID := cmd.Args().First()
	if friendID == "" {
		return nil, fmt.Errorf("friend id is required")
	}
	if err := validateFormat(cmd.String("format"), "table", "jsonl"); err != nil {
		return nil, err
	}
	return &facade.RemoveFriendRequest{FriendID: friendID}, nil
}

func parseBlockFriendRequest(cmd *cli.Command) (*facade.BlockFriendRequest, error) {
	friendID := cmd.Args().First()
	if friendID == "" {
		return nil, fmt.Errorf("friend id is required")
	}
	if err := validateFormat(cmd.String("format"), "table", "jsonl"); err != nil {
		return nil, err
	}
	return &facade.BlockFriendRequest{FriendID: friendID}, nil
}

func parseListTeamsRequest(cmd *cli.Command) (*facade.ListTeamsRequest, error) {
	if err := validateFormat(cmd.String("format"), "table", "jsonl"); err != nil {
		return nil, err
	}
	return &facade.ListTeamsRequest{}, nil
}

func parseGetTeamRequest(cmd *cli.Command) (*facade.GetTeamRequest, error) {
	teamID := strings.TrimSpace(cmd.Args().First())
	if teamID == "" {
		return nil, fmt.Errorf("team id is required")
	}
	if err := validateFormat(cmd.String("format"), "table", "jsonl"); err != nil {
		return nil, err
	}
	return &facade.GetTeamRequest{TeamID: teamID}, nil
}

// parseTeamAgentsRequest returns either a single-team request or an aggregate
// request, never both. Exactly one of <team-id> and --all must be provided.
func parseTeamAgentsRequest(
	cmd *cli.Command,
) (*facade.ListTeamAgentsRequest, *facade.ListAllTeamAgentsRequest, error) {
	if err := validateFormat(cmd.String("format"), "table", "jsonl"); err != nil {
		return nil, nil, err
	}
	teamID := strings.TrimSpace(cmd.Args().First())
	all := cmd.Bool("all")
	if all && teamID != "" {
		return nil, nil, fmt.Errorf("use either <team-id> or --all, not both")
	}
	if !all && teamID == "" {
		return nil, nil, fmt.Errorf("provide a <team-id> or use --all")
	}
	if !all && (strings.TrimSpace(cmd.String("agent")) != "" ||
		cmd.Bool("include-self") || cmd.Bool("online")) {
		return nil, nil, fmt.Errorf("--agent, --include-self, and --online require --all")
	}
	if all {
		return nil, &facade.ListAllTeamAgentsRequest{
			AgentID:     strings.TrimSpace(cmd.String("agent")),
			IncludeSelf: cmd.Bool("include-self"),
			OnlineOnly:  cmd.Bool("online"),
		}, nil
	}
	return &facade.ListTeamAgentsRequest{TeamID: teamID}, nil, nil
}

func validateFormat(format string, values ...string) error {
	for _, value := range values {
		if format == value {
			return nil
		}
	}
	return fmt.Errorf("unsupported format %q", format)
}

func parseListInjectionsRequest(cmd *cli.Command) (*facade.ListInjectionsRequest, error) {
	req := &facade.ListInjectionsRequest{
		TargetSessionID: cmd.String("target-session"),
		Limit:           cmd.Int("limit"),
	}
	switch cmd.String("format") {
	case "table", "jsonl":
		return req, nil
	default:
		return nil, fmt.Errorf("unsupported format %q", cmd.String("format"))
	}
}

func parseListAgentsRequest(cmd *cli.Command) (*facade.ListAgentsRequest, error) {
	switch cmd.String("format") {
	case "table", "jsonl":
		return &facade.ListAgentsRequest{}, nil
	default:
		return nil, fmt.Errorf("unsupported format %q", cmd.String("format"))
	}
}

func parseSetupRequest(cmd *cli.Command) (*facade.SetupRequest, error) {
	if err := validateFormat(cmd.String("format"), "table", "jsonl"); err != nil {
		return nil, err
	}
	var agents []model.AgentName
	for _, raw := range cmd.StringSlice("agent") {
		for _, part := range strings.Split(raw, ",") {
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}
			agent, err := parseActiveAgentName(part)
			if err != nil {
				return nil, fmt.Errorf("parse agent: %w", err)
			}
			switch agent {
			case model.AgentNameCodex,
				model.AgentNameClaude,
				model.AgentNamePi,
				model.AgentNameKiro,
				model.AgentNameHermes,
				model.AgentNameOpenClaw:
				agents = append(agents, agent)
			case model.AgentNameUnknown,
				model.AgentNameGemini,
				model.AgentNamePaxl:
				return nil, fmt.Errorf("agent %q does not support setup", agent)
			}
		}
	}
	return &facade.SetupRequest{
		Agents:      agents,
		PaxlCommand: firstNonEmpty(strings.TrimSpace(cmd.String("paxl-command")), "paxl"),
		DryRun:      cmd.Bool("dry-run"),
		WithDaemon:  cmd.Bool("with-daemon"),
		CloudURL:    cmd.String("cloud-url"),
		ResolverURL: cmd.String("daemon-resolver-url"),
		Platform:    cmd.String("daemon-platform"),
		Tag:         cmd.String("daemon-tag"),
		InstallDir:  cmd.String("daemon-install-dir"),
	}, nil
}

func parseCheckUpdateRequest(cmd *cli.Command) (*facade.CheckUpdateRequest, error) {
	switch cmd.String("format") {
	case "text", "json", "jsonl":
	default:
		return nil, fmt.Errorf("unsupported format %q", cmd.String("format"))
	}
	meta := currentVersionMetadata()
	return &facade.CheckUpdateRequest{
		CurrentVersion: meta.Version,
		CurrentCommit:  meta.Commit,
		ManifestURL:    cmd.String("manifest-url"),
		ResolverURL:    cmd.String("resolver-url"),
		Platform:       cmd.String("platform"),
		Tag:            cmd.String("tag"),
	}, nil
}

func parseAgentSelection(raw string) ([]model.AgentName, error) {
	values := parseCSV(raw)
	if len(values) == 0 {
		return []model.AgentName{
			model.AgentNameCodex,
			model.AgentNameClaude,
			model.AgentNameOpenClaw,
		}, nil
	}
	agents := make([]model.AgentName, 0, len(values))
	for _, value := range values {
		agent, err := parseActiveAgentName(value)
		if err != nil {
			return nil, err
		}
		agents = append(agents, agent)
	}
	return agents, nil
}

func parseActiveAgentName(raw string) (model.AgentName, error) {
	agent, err := model.ParseAgentName(raw)
	if err != nil {
		return model.AgentNameUnknown, err
	}
	if isRetiredAgentName(agent) {
		return model.AgentNameUnknown, fmt.Errorf("agent %q is no longer supported", agent)
	}
	return agent, nil
}

func isRetiredAgentName(agent model.AgentName) bool {
	return agent == model.AgentNameGemini
}

func parseCSV(raw string) []string {
	parts := strings.Split(raw, ",")
	values := make([]string, 0, len(parts))
	for _, part := range parts {
		value := strings.TrimSpace(part)
		if value != "" {
			values = append(values, value)
		}
	}
	return values
}

func parseUpdatedSince(raw string) (time.Time, bool, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}, false, nil
	}
	duration, err := parseDuration(raw)
	if err != nil {
		return time.Time{}, false, fmt.Errorf("parse updated-since: %w", err)
	}
	cutoff := time.Now().UTC().Add(-duration)
	return cutoff, true, nil
}

func parseDuration(raw string) (time.Duration, error) {
	raw = strings.TrimSpace(raw)
	if strings.HasSuffix(raw, "d") {
		days, err := time.ParseDuration(strings.TrimSuffix(raw, "d") + "h")
		if err != nil {
			return 0, err
		}
		return days * 24, nil
	}
	return time.ParseDuration(raw)
}

func contextWithTimeout(
	ctx context.Context,
	raw string,
) (context.Context, context.CancelFunc, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ctx, func() {}, nil
	}
	duration, err := parseDuration(raw)
	if err != nil {
		return nil, nil, err
	}
	if duration <= 0 {
		return ctx, func() {}, nil
	}
	nextCtx, cancel := context.WithTimeout(ctx, duration)
	return nextCtx, cancel, nil
}

func scheduleDebugStack(raw string, stderr io.Writer) (func(), error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return func() {}, nil
	}
	duration, err := parseDuration(raw)
	if err != nil {
		return nil, err
	}
	timer := time.AfterFunc(duration, func() {
		_, _ = fmt.Fprintf(stderr, "Debug stack after %s.\n", duration)
		_ = pprof.Lookup("goroutine").WriteTo(stderr, 2)
	})
	return func() {
		timer.Stop()
	}, nil
}

func renderMirrorResult(
	stdout io.Writer,
	resp *facade.MirrorSessionResponse,
	format string,
) error {
	switch format {
	case "table":
		writer := tabwriter.NewWriter(stdout, 0, 4, 2, ' ', 0)
		if _, err := fmt.Fprintln(
			writer,
			"MIRROR\tCAPSULE\tSOURCE\tTARGET\tMETHOD\tTITLE",
		); err != nil {
			return fmt.Errorf("write mirror header: %w", err)
		}
		if _, err := fmt.Fprintf(
			writer,
			"%s\t%s\t%s\t%s\t%s\t%s\n",
			resp.Injection.InjectionID,
			resp.Capsule.CapsuleID,
			resp.Capsule.SourceSessionID,
			resp.Injection.TargetSessionID,
			resp.Injection.DeliveryMethod,
			resp.Capsule.Title,
		); err != nil {
			return fmt.Errorf("write mirror row: %w", err)
		}
		return writer.Flush()
	case "jsonl":
		if err := json.NewEncoder(stdout).Encode(map[string]any{
			"schemaVersion":   "paxl.session.mirror.v1",
			"mirrorId":        resp.Injection.InjectionID,
			"capsuleId":       resp.Capsule.CapsuleID,
			"sourceNodeId":    resp.Capsule.SourceNodeID,
			"sourceSessionId": resp.Capsule.SourceSessionID,
			"sourceAgent":     resp.Capsule.SourceAgent,
			"targetNodeId":    resp.Injection.TargetNodeID,
			"targetAgent":     resp.Injection.TargetAgent,
			"targetSessionId": resp.Injection.TargetSessionID,
			"deliveryMethod":  resp.Injection.DeliveryMethod,
			"title":           resp.Capsule.Title,
		}); err != nil {
			return fmt.Errorf("encode mirror result: %w", err)
		}
		return nil
	default:
		return fmt.Errorf("unsupported format %q", format)
	}
}

func renderInjectionList(
	stdout io.Writer,
	resp *facade.ListInjectionsResponse,
	format string,
) error {
	switch format {
	case "table":
		writer := tabwriter.NewWriter(stdout, 0, 4, 2, ' ', 0)
		if _, err := fmt.Fprintln(
			writer,
			"ID\tCAPSULE\tTARGET\tTYPE\tSTATUS\tCREATED",
		); err != nil {
			return fmt.Errorf("write injection list header: %w", err)
		}
		for _, injection := range resp.Injections {
			if _, err := fmt.Fprintf(
				writer,
				"%s\t%s\t%s\t%s\t%s\t%s\n",
				injection.InjectionID,
				injection.CapsuleID,
				injection.TargetSessionID,
				injection.DeliveryMessageType,
				injection.Status,
				injection.CreatedAt,
			); err != nil {
				return fmt.Errorf("write injection row: %w", err)
			}
		}
		return writer.Flush()
	case "jsonl":
		encoder := json.NewEncoder(stdout)
		for _, injection := range resp.Injections {
			if err := encoder.Encode(map[string]any{
				"schemaVersion":       "paxl.knowledge_injection.v1",
				"injectionId":         injection.InjectionID,
				"capsuleId":           injection.CapsuleID,
				"sourceNodeId":        injection.SourceNodeID,
				"sourceAgent":         injection.SourceAgent,
				"sourceSessionId":     injection.SourceSessionID,
				"targetNodeId":        injection.TargetNodeID,
				"targetSessionId":     injection.TargetSessionID,
				"targetAgent":         injection.TargetAgent,
				"deliveryMethod":      injection.DeliveryMethod,
				"deliveryMessageType": injection.DeliveryMessageType,
				"status":              injection.Status,
				"routeMatchType":      injection.RouteMatchType,
				"routeMatchValue":     injection.RouteMatchValue,
				"actionItems":         injectionActionItems(injection),
				"createdAt":           injection.CreatedAt,
				"claimedAt":           injection.ClaimedAt,
				"consumedAt":          injection.ConsumedAt,
			}); err != nil {
				return fmt.Errorf("encode injection: %w", err)
			}
		}
		return nil
	default:
		return fmt.Errorf("unsupported format %q", format)
	}
}

func renderCapsuleList(stdout io.Writer, resp *facade.ListCapsulesResponse, format string) error {
	switch format {
	case "table":
		writer := tabwriter.NewWriter(stdout, 0, 4, 2, ' ', 0)
		if _, err := fmt.Fprintln(
			writer,
			"ID\tSTATUS\tSOURCE\tKEYWORD\tCREATED\tTITLE",
		); err != nil {
			return fmt.Errorf("write capsule list header: %w", err)
		}
		for _, capsule := range resp.Capsules {
			if _, err := fmt.Fprintf(
				writer,
				"%s\t%s\t%s\t%s\t%s\t%s\n",
				capsule.CapsuleID,
				capsule.Status,
				capsule.SourceSessionID,
				capsule.Keyword,
				capsule.CreatedAt,
				capsule.Title,
			); err != nil {
				return fmt.Errorf("write capsule row: %w", err)
			}
		}
		return writer.Flush()
	case "jsonl":
		for _, capsule := range resp.Capsules {
			if err := encodeCapsuleJSONL(stdout, capsule); err != nil {
				return err
			}
		}
		return nil
	default:
		return fmt.Errorf("unsupported format %q", format)
	}
}

func renderCapsule(stdout io.Writer, resp *facade.GetCapsuleResponse, format string) error {
	switch format {
	case "text":
		if _, err := fmt.Fprintln(stdout, renderCapsuleText(resp.Capsule)); err != nil {
			return fmt.Errorf("write capsule text: %w", err)
		}
		return nil
	case "jsonl":
		return encodeCapsuleJSONL(stdout, resp.Capsule)
	default:
		return fmt.Errorf("unsupported format %q", format)
	}
}

func renderCapsuleText(capsule *model.KnowledgeCapsule) string {
	return fmt.Sprintf(
		"Title: %s\nKeyword: %s\nSource node: %s\nSource session: %s\nStatus: %s\nCreated: %s\n\nSummary:\n%s\n\nContent:\n%s",
		capsule.Title,
		capsule.Keyword,
		capsule.SourceNodeID,
		capsule.SourceSessionID,
		capsule.Status,
		capsule.CreatedAt,
		capsule.Summary,
		capsule.Content,
	)
}

func encodeCapsuleJSONL(stdout io.Writer, capsule *model.KnowledgeCapsule) error {
	if err := json.NewEncoder(stdout).Encode(map[string]any{
		"schemaVersion":          "paxl.knowledge_capsule.v1",
		"capsuleId":              capsule.CapsuleID,
		"sourceNodeId":           capsule.SourceNodeID,
		"sourceSessionId":        capsule.SourceSessionID,
		"sourceAgent":            capsule.SourceAgent,
		"keyword":                capsule.Keyword,
		"title":                  capsule.Title,
		"summary":                capsule.Summary,
		"content":                capsule.Content,
		"status":                 capsule.Status,
		"truncated":              capsule.Truncated,
		"originalEstimatedChars": capsule.OriginalEstimatedChars,
		"createdAt":              capsule.CreatedAt,
		"archivedAt":             capsule.ArchivedAt,
	}); err != nil {
		return fmt.Errorf("encode capsule: %w", err)
	}
	return nil
}

func renderEnvelopeList(stdout io.Writer, resp *facade.ListInboxResponse, format string) error {
	return renderEnvelopeListWithPeer(
		stdout,
		resp.Envelopes,
		format,
		"FROM",
		func(envelope *model.Envelope) string {
			return firstNonEmpty(envelope.SenderEmail, envelope.SenderUserID, "-")
		},
	)
}

func renderOutboxEnvelopeList(
	stdout io.Writer,
	resp *facade.ListOutboxResponse,
	format string,
) error {
	return renderEnvelopeListWithPeer(
		stdout,
		resp.Envelopes,
		format,
		"TO",
		func(envelope *model.Envelope) string {
			return firstNonEmpty(envelope.RecipientEmail, envelope.RecipientUserID, "-")
		},
	)
}

func renderEnvelopeListWithPeer(
	stdout io.Writer,
	envelopes []*model.Envelope,
	format string,
	peerHeader string,
	peer func(*model.Envelope) string,
) error {
	switch format {
	case "table":
		writer := tabwriter.NewWriter(stdout, 0, 4, 2, ' ', 0)
		header := "ID\tSTATUS\t%s\tCREATED\tMESSAGE\n"
		if _, err := fmt.Fprintf(writer, header, peerHeader); err != nil {
			return fmt.Errorf("write envelope list header: %w", err)
		}
		for _, envelope := range envelopes {
			if _, err := fmt.Fprintf(
				writer,
				"%s\t%s\t%s\t%s\t%s\n",
				envelope.EnvelopeID,
				envelope.Status,
				peer(envelope),
				firstNonEmpty(envelope.CreatedAt, "-"),
				firstNonEmpty(envelope.Message, "-"),
			); err != nil {
				return fmt.Errorf("write envelope row: %w", err)
			}
		}
		return writer.Flush()
	case "jsonl":
		for _, envelope := range envelopes {
			if err := encodeEnvelopeJSONL(stdout, envelope); err != nil {
				return err
			}
		}
		return nil
	default:
		return fmt.Errorf("unsupported format %q", format)
	}
}

func renderEnvelope(stdout io.Writer, envelope *model.Envelope, format string) error {
	switch format {
	case "text":
		if _, err := fmt.Fprintln(stdout, renderEnvelopeText(envelope)); err != nil {
			return fmt.Errorf("write envelope text: %w", err)
		}
		return nil
	case "jsonl":
		return encodeEnvelopeJSONL(stdout, envelope)
	default:
		return fmt.Errorf("unsupported format %q", format)
	}
}

func renderEnvelopeText(envelope *model.Envelope) string {
	return fmt.Sprintf(
		"Envelope: %s\nStatus: %s\nFrom: %s\nTo: %s\nCreated: %s\nPayload: %s\n\nMessage:\n%s\n\nPayload JSON:\n%s",
		envelope.EnvelopeID,
		envelope.Status,
		firstNonEmpty(envelope.SenderEmail, envelope.SenderUserID, "-"),
		firstNonEmpty(envelope.RecipientEmail, envelope.RecipientUserID, "-"),
		envelope.CreatedAt,
		envelope.PayloadType,
		firstNonEmpty(envelope.Message, "-"),
		string(envelope.PayloadJSON),
	)
}

func renderAcceptEnvelope(
	stdout io.Writer,
	resp *facade.AcceptEnvelopeResponse,
	format string,
) error {
	switch format {
	case "table":
		if _, err := fmt.Fprintf(
			stdout,
			"Accepted %s as local capsule %s\n",
			resp.Envelope.EnvelopeID,
			resp.Capsule.CapsuleID,
		); err != nil {
			return fmt.Errorf("write accept result: %w", err)
		}
		if resp.Injection != nil {
			if _, err := fmt.Fprintf(
				stdout,
				"Queued hook injection route %s for %s match %s\n",
				resp.Injection.InjectionID,
				firstNonEmpty(string(resp.Injection.TargetAgent), "any agent"),
				firstNonEmpty(resp.Injection.RouteMatchType, "any"),
			); err != nil {
				return fmt.Errorf("write accept route result: %w", err)
			}
		}
		return nil
	case "jsonl":
		if err := encodeEnvelopeJSONL(stdout, resp.Envelope); err != nil {
			return err
		}
		if err := encodeCapsuleJSONL(stdout, resp.Capsule); err != nil {
			return err
		}
		if resp.Injection != nil {
			return encodeInjectionJSONL(stdout, resp.Injection)
		}
		return nil
	default:
		return fmt.Errorf("unsupported format %q", format)
	}
}

func renderAcceptAllEnvelopes(
	stdout io.Writer,
	resp *facade.AcceptAllEnvelopesResponse,
	format string,
) error {
	switch format {
	case "table":
		if len(resp.Accepted) == 0 && len(resp.Failures) == 0 {
			if _, err := fmt.Fprintln(stdout, "No pending envelopes to accept"); err != nil {
				return fmt.Errorf("write accept all empty result: %w", err)
			}
			return nil
		}
		for _, accepted := range resp.Accepted {
			if err := renderAcceptEnvelope(stdout, accepted, format); err != nil {
				return err
			}
		}
		for _, failure := range resp.Failures {
			if _, err := fmt.Fprintf(
				stdout,
				"Failed %s: %s\n",
				failure.EnvelopeID,
				failure.Error,
			); err != nil {
				return fmt.Errorf("write accept all failure: %w", err)
			}
		}
		return nil
	case "jsonl":
		for _, accepted := range resp.Accepted {
			if err := renderAcceptEnvelope(stdout, accepted, format); err != nil {
				return err
			}
		}
		encoder := json.NewEncoder(stdout)
		for _, failure := range resp.Failures {
			if err := encoder.Encode(map[string]any{
				"schemaVersion": "paxl.accept_failure.v1",
				"envelopeId":    failure.EnvelopeID,
				"error":         failure.Error,
			}); err != nil {
				return fmt.Errorf("encode accept all failure: %w", err)
			}
		}
		return nil
	default:
		return fmt.Errorf("unsupported format %q", format)
	}
}

func renderInboxWatchEvent(
	stdout io.Writer,
	format string,
	event string,
	detail string,
	count int,
) error {
	timestamp := time.Now().UTC().Format(time.RFC3339)
	switch format {
	case "table":
		var message string
		switch event {
		case "started":
			message = fmt.Sprintf("Watching inbox every %s; press Ctrl-C to stop", detail)
		case "received":
			message = fmt.Sprintf("Received %d pending envelope(s)", count)
		case "auto_accepted":
			message = fmt.Sprintf("Auto accepted %d envelope(s)", count)
		case "failed":
			message = fmt.Sprintf("Failed to accept %d envelope(s)", count)
		default:
			message = event
		}
		if _, err := fmt.Fprintf(stdout, "[%s] %s\n", timestamp, message); err != nil {
			return fmt.Errorf("write inbox watch event: %w", err)
		}
		return nil
	case "jsonl":
		payload := map[string]any{
			"schemaVersion": "paxl.inbox_watch_event.v1",
			"event":         event,
			"timestamp":     timestamp,
		}
		if detail != "" {
			payload["detail"] = detail
		}
		if count > 0 {
			payload["count"] = count
		}
		if err := json.NewEncoder(stdout).Encode(payload); err != nil {
			return fmt.Errorf("encode inbox watch event: %w", err)
		}
		return nil
	default:
		return fmt.Errorf("unsupported format %q", format)
	}
}

func encodeInjectionJSONL(stdout io.Writer, injection *model.KnowledgeInjection) error {
	return json.NewEncoder(stdout).Encode(map[string]any{
		"schemaVersion":       "paxl.knowledge_injection.v1",
		"injectionId":         injection.InjectionID,
		"capsuleId":           injection.CapsuleID,
		"sourceNodeId":        injection.SourceNodeID,
		"sourceAgent":         injection.SourceAgent,
		"sourceSessionId":     injection.SourceSessionID,
		"targetNodeId":        injection.TargetNodeID,
		"targetAgent":         injection.TargetAgent,
		"targetSessionId":     injection.TargetSessionID,
		"deliveryMethod":      injection.DeliveryMethod,
		"deliveryMessageType": injection.DeliveryMessageType,
		"status":              injection.Status,
		"routeMatchType":      injection.RouteMatchType,
		"routeMatchValue":     injection.RouteMatchValue,
		"actionItems":         injectionActionItems(injection),
		"createdAt":           injection.CreatedAt,
		"claimedAt":           injection.ClaimedAt,
		"consumedAt":          injection.ConsumedAt,
	})
}

func injectionActionItems(injection *model.KnowledgeInjection) []string {
	if injection == nil || strings.TrimSpace(injection.ActionItemsJSON) == "" {
		return nil
	}
	var items []string
	if err := json.Unmarshal([]byte(injection.ActionItemsJSON), &items); err != nil {
		return nil
	}
	out := make([]string, 0, len(items))
	for _, item := range items {
		trimmed := strings.TrimSpace(item)
		if trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

func encodeEnvelopeJSONL(stdout io.Writer, envelope *model.Envelope) error {
	if err := json.NewEncoder(stdout).Encode(map[string]any{
		"schemaVersion":   "paxl.envelope.v1",
		"envelopeId":      envelope.EnvelopeID,
		"senderUserId":    envelope.SenderUserID,
		"senderEmail":     envelope.SenderEmail,
		"recipientUserId": envelope.RecipientUserID,
		"recipientEmail":  envelope.RecipientEmail,
		"fromAgentId":     envelope.FromAgentID,
		"toAgentId":       envelope.ToAgentID,
		"payloadType":     envelope.PayloadType,
		"payloadJson":     envelope.PayloadJSON,
		"message":         envelope.Message,
		"status":          envelope.Status,
		"createdAt":       envelope.CreatedAt,
		"acceptedAt":      envelope.AcceptedAt,
		"archivedAt":      envelope.ArchivedAt,
	}); err != nil {
		return fmt.Errorf("encode envelope: %w", err)
	}
	return nil
}

func renderFriendList(stdout io.Writer, resp *facade.ListFriendsResponse, format string) error {
	switch format {
	case "table":
		writer := tabwriter.NewWriter(stdout, 0, 4, 2, ' ', 0)
		if _, err := fmt.Fprintln(
			writer,
			"ID\tSTATUS\tALIAS\tEMAIL\tDIRECTION\tCREATED",
		); err != nil {
			return fmt.Errorf("write friend list header: %w", err)
		}
		for _, friend := range resp.Friends {
			alias, email, direction := friendView(resp.UserID, friend)
			if _, err := fmt.Fprintf(
				writer,
				"%s\t%s\t%s\t%s\t%s\t%s\n",
				friend.FriendID,
				friend.Status,
				firstNonEmpty(alias, "-"),
				firstNonEmpty(email, "-"),
				direction,
				firstNonEmpty(friend.CreatedAt, "-"),
			); err != nil {
				return fmt.Errorf("write friend row: %w", err)
			}
		}
		return writer.Flush()
	case "jsonl":
		for _, friend := range resp.Friends {
			if err := encodeFriendJSONL(stdout, resp.UserID, friend); err != nil {
				return err
			}
		}
		return nil
	default:
		return fmt.Errorf("unsupported format %q", format)
	}
}

func encodeFriendJSONL(stdout io.Writer, userID string, friend *model.Friend) error {
	alias, email, direction := friendView(userID, friend)
	if err := json.NewEncoder(stdout).Encode(map[string]any{
		"schemaVersion":   "paxl.friend.v1",
		"friendId":        friend.FriendID,
		"alias":           alias,
		"email":           email,
		"direction":       direction,
		"requesterUserId": friend.RequesterUserID,
		"requesterEmail":  friend.RequesterEmail,
		"requesterAlias":  friend.RequesterAlias,
		"recipientUserId": friend.RecipientUserID,
		"recipientEmail":  friend.RecipientEmail,
		"recipientAlias":  friend.RecipientAlias,
		"status":          friend.Status,
		"createdAt":       friend.CreatedAt,
		"acceptedAt":      friend.AcceptedAt,
		"removedAt":       friend.RemovedAt,
		"blockedAt":       friend.BlockedAt,
	}); err != nil {
		return fmt.Errorf("encode friend: %w", err)
	}
	return nil
}

func friendView(userID string, friend *model.Friend) (string, string, string) {
	if friend == nil {
		return "", "", ""
	}
	if userID != "" && friend.RequesterUserID == userID {
		return friend.RequesterAlias, friend.RecipientEmail, "sent"
	}
	if userID != "" &&
		(friend.RecipientUserID == userID || friend.RecipientUserID == "") {
		return friend.RecipientAlias, friend.RequesterEmail, "received"
	}
	return firstNonEmpty(friend.RequesterAlias, friend.RecipientAlias),
		firstNonEmpty(friend.RecipientEmail, friend.RequesterEmail),
		"-"
}

func teamAgentDisplay(teamAgent *model.TeamAgent) string {
	if teamAgent == nil {
		return "-"
	}
	if teamAgent.Agent != nil && strings.TrimSpace(teamAgent.Agent.Name) != "" {
		return teamAgent.Agent.Name
	}
	return teamAgent.AgentID
}

func teamAgentOnline(teamAgent *model.TeamAgent) string {
	if teamAgent != nil && teamAgent.Agent != nil && teamAgent.Agent.Online {
		return "yes"
	}
	return "no"
}

func teamAgentOwner(teamAgent *model.TeamAgent) string {
	if teamAgent == nil {
		return "-"
	}
	return firstNonEmpty(teamAgent.AgentOwnerEmail, teamAgent.AgentOwnerUserID, "-")
}

func teamAgentType(teamAgent *model.TeamAgent) string {
	if teamAgent == nil || teamAgent.Agent == nil {
		return "-"
	}
	return firstNonEmpty(teamAgent.Agent.AgentType, "-")
}

func teamAgentHost(teamAgent *model.TeamAgent) string {
	if teamAgent == nil || teamAgent.Agent == nil {
		return "-"
	}
	return firstNonEmpty(teamAgent.Agent.Hostname, "-")
}

func renderTeamList(stdout io.Writer, resp *facade.ListTeamsResponse, format string) error {
	switch format {
	case "table":
		writer := tabwriter.NewWriter(stdout, 0, 4, 2, ' ', 0)
		if _, err := fmt.Fprintln(writer, "ID\tNAME\tROLE\tMEMBERS\tAGENTS\tSTATUS"); err != nil {
			return fmt.Errorf("write team list header: %w", err)
		}
		for _, team := range resp.Teams {
			if _, err := fmt.Fprintf(
				writer,
				"%s\t%s\t%s\t%d\t%d\t%s\n",
				team.TeamID,
				firstNonEmpty(team.Name, "-"),
				firstNonEmpty(team.MyRole, "-"),
				team.MemberCount,
				team.AgentCount,
				firstNonEmpty(team.Status, "-"),
			); err != nil {
				return fmt.Errorf("write team row: %w", err)
			}
		}
		return writer.Flush()
	case "jsonl":
		for _, team := range resp.Teams {
			if err := json.NewEncoder(stdout).Encode(map[string]any{
				"schemaVersion": "paxl.team.v1",
				"teamId":        team.TeamID,
				"name":          team.Name,
				"myRole":        team.MyRole,
				"memberCount":   team.MemberCount,
				"agentCount":    team.AgentCount,
				"status":        team.Status,
				"ownerUserId":   team.OwnerUserID,
				"createdAt":     team.CreatedAt,
				"archivedAt":    team.ArchivedAt,
			}); err != nil {
				return fmt.Errorf("encode team: %w", err)
			}
		}
		return nil
	default:
		return fmt.Errorf("unsupported format %q", format)
	}
}

func renderTeamDetail(stdout io.Writer, resp *facade.GetTeamResponse, format string) error {
	team := resp.Team
	switch format {
	case "table":
		writer := tabwriter.NewWriter(stdout, 0, 4, 2, ' ', 0)
		if _, err := fmt.Fprintln(writer, "ID\tNAME\tOWNER\tSTATUS\tCREATED"); err != nil {
			return fmt.Errorf("write team header: %w", err)
		}
		if _, err := fmt.Fprintf(
			writer,
			"%s\t%s\t%s\t%s\t%s\n",
			team.TeamID,
			firstNonEmpty(team.Name, "-"),
			firstNonEmpty(team.OwnerUserID, "-"),
			firstNonEmpty(team.Status, "-"),
			firstNonEmpty(team.CreatedAt, "-"),
		); err != nil {
			return fmt.Errorf("write team row: %w", err)
		}
		return writer.Flush()
	case "jsonl":
		if err := json.NewEncoder(stdout).Encode(map[string]any{
			"schemaVersion": "paxl.team.v1",
			"teamId":        team.TeamID,
			"name":          team.Name,
			"ownerUserId":   team.OwnerUserID,
			"status":        team.Status,
			"createdAt":     team.CreatedAt,
			"archivedAt":    team.ArchivedAt,
		}); err != nil {
			return fmt.Errorf("encode team: %w", err)
		}
		return nil
	default:
		return fmt.Errorf("unsupported format %q", format)
	}
}

func renderTeamAgents(stdout io.Writer, resp *facade.ListTeamAgentsResponse, format string) error {
	switch format {
	case "table":
		writer := tabwriter.NewWriter(stdout, 0, 4, 2, ' ', 0)
		header := "NAME\tAGENT_ID\tOWNER\tTYPE\tHOST\tONLINE\tADDED"
		if _, err := fmt.Fprintln(writer, header); err != nil {
			return fmt.Errorf("write team agents header: %w", err)
		}
		for _, teamAgent := range resp.Agents {
			if _, err := fmt.Fprintf(
				writer,
				"%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
				teamAgentDisplay(teamAgent),
				teamAgent.AgentID,
				teamAgentOwner(teamAgent),
				teamAgentType(teamAgent),
				teamAgentHost(teamAgent),
				teamAgentOnline(teamAgent),
				firstNonEmpty(teamAgent.AddedAt, "-"),
			); err != nil {
				return fmt.Errorf("write team agent row: %w", err)
			}
		}
		return writer.Flush()
	case "jsonl":
		for _, teamAgent := range resp.Agents {
			if err := encodeTeamAgentJSONL(stdout, teamAgent, nil); err != nil {
				return err
			}
		}
		return nil
	default:
		return fmt.Errorf("unsupported format %q", format)
	}
}

func renderAggregatedTeamAgents(
	stdout io.Writer,
	resp *facade.ListAllTeamAgentsResponse,
	format string,
) error {
	switch format {
	case "table":
		writer := tabwriter.NewWriter(stdout, 0, 4, 2, ' ', 0)
		header := "NAME\tAGENT_ID\tOWNER\tTYPE\tHOST\tONLINE\tTEAMS"
		if _, err := fmt.Fprintln(writer, header); err != nil {
			return fmt.Errorf("write aggregated agents header: %w", err)
		}
		for _, aggregated := range resp.Agents {
			// Agent is guaranteed non-nil by TeamFacade.ListAllAgents.
			if _, err := fmt.Fprintf(
				writer,
				"%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
				teamAgentDisplay(aggregated.Agent),
				aggregated.Agent.AgentID,
				teamAgentOwner(aggregated.Agent),
				teamAgentType(aggregated.Agent),
				teamAgentHost(aggregated.Agent),
				teamAgentOnline(aggregated.Agent),
				teamRefsLabel(aggregated.Teams),
			); err != nil {
				return fmt.Errorf("write aggregated agent row: %w", err)
			}
		}
		return writer.Flush()
	case "jsonl":
		for _, aggregated := range resp.Agents {
			if err := encodeTeamAgentJSONL(stdout, aggregated.Agent, aggregated.Teams); err != nil {
				return err
			}
		}
		return nil
	default:
		return fmt.Errorf("unsupported format %q", format)
	}
}

func teamRefsLabel(refs []facade.TeamRef) string {
	if len(refs) == 0 {
		return "-"
	}
	labels := make([]string, 0, len(refs))
	for _, ref := range refs {
		labels = append(labels, firstNonEmpty(ref.Name, ref.TeamID))
	}
	return strings.Join(labels, ",")
}

func encodeTeamAgentJSONL(
	stdout io.Writer,
	teamAgent *model.TeamAgent,
	refs []facade.TeamRef,
) error {
	teams := make([]map[string]string, 0, len(refs))
	for _, ref := range refs {
		teams = append(teams, map[string]string{"teamId": ref.TeamID, "name": ref.Name})
	}
	payload := map[string]any{
		"schemaVersion":    "paxl.teamAgent.v1",
		"agentId":          teamAgent.AgentID,
		"agentOwnerUserId": teamAgent.AgentOwnerUserID,
		"agentOwnerEmail":  teamAgent.AgentOwnerEmail,
		"display":          teamAgentDisplay(teamAgent),
		"online":           teamAgent.Agent != nil && teamAgent.Agent.Online,
		"addedAt":          teamAgent.AddedAt,
		"addedByUserId":    teamAgent.AddedByUserID,
		"removedAt":        teamAgent.RemovedAt,
		"removedByUserId":  teamAgent.RemovedByUserID,
	}
	if teamAgent.Agent != nil {
		payload["name"] = teamAgent.Agent.Name
		payload["hostname"] = teamAgent.Agent.Hostname
		payload["agentType"] = teamAgent.Agent.AgentType
		payload["machineType"] = teamAgent.Agent.MachineType
		payload["os"] = teamAgent.Agent.OS
		payload["status"] = teamAgent.Agent.Status
	}
	if teamAgent.TeamID != "" {
		payload["teamId"] = teamAgent.TeamID
	}
	if len(teams) > 0 {
		payload["teams"] = teams
	}
	if err := json.NewEncoder(stdout).Encode(payload); err != nil {
		return fmt.Errorf("encode team agent: %w", err)
	}
	return nil
}

func renderSessionTimelineOutput(
	stdout io.Writer,
	resp *facade.GetSessionResponse,
	format string,
	output string,
) error {
	output = strings.TrimSpace(output)
	if format == "html" && output == "" {
		output = defaultHTMLPath(resp.Session)
	}
	if output == "" {
		return renderSessionTimeline(stdout, resp, format)
	}
	// The output path is an explicit CLI argument, matching standard shell redirection semantics.
	file, err := os.Create(output) // #nosec G304
	if err != nil {
		return fmt.Errorf("create output file: %w", err)
	}
	defer closeFile(file)
	if err := renderSessionTimeline(file, resp, format); err != nil {
		return err
	}
	if format == "html" {
		if _, err := fmt.Fprintf(stdout, "Wrote %s\n", output); err != nil {
			return fmt.Errorf("write output result: %w", err)
		}
	}
	return nil
}

func renderSessionTimeline(stdout io.Writer, resp *facade.GetSessionResponse, format string) error {
	switch format {
	case "transcript":
		for _, element := range resp.Elements {
			if _, err := fmt.Fprintf(
				stdout,
				"[%s] %s\n%s\n\n",
				firstNonEmpty(element.Role, element.Type),
				firstNonEmpty(element.CompletedAt, element.StartedAt),
				element.ContentText,
			); err != nil {
				return fmt.Errorf("write transcript element: %w", err)
			}
		}
		return nil
	case "jsonl":
		encoder := json.NewEncoder(stdout)
		for _, element := range resp.Elements {
			if err := encoder.Encode(map[string]any{
				"schemaVersion": "paxl.session.element.v1",
				"sessionId":     element.SessionID,
				"seq":           element.Seq,
				"type":          element.Type,
				"role":          element.Role,
				"model":         element.Model,
				"startedAt":     element.StartedAt,
				"completedAt":   element.CompletedAt,
				"durationMs":    element.DurationMS,
				"contentText":   element.ContentText,
			}); err != nil {
				return fmt.Errorf("encode session element: %w", err)
			}
		}
		return nil
	case "html":
		return renderSessionHTML(stdout, resp)
	default:
		return fmt.Errorf("unsupported format %q", format)
	}
}

func renderSessionList(stdout io.Writer, resp *facade.ListSessionsResponse, format string) error {
	switch format {
	case "table":
		writer := tabwriter.NewWriter(stdout, 0, 4, 2, ' ', 0)
		if _, err := fmt.Fprintln(writer, "ID\tAGENT\tPROJECT\tUPDATED\tTITLE"); err != nil {
			return fmt.Errorf("write session list header: %w", err)
		}
		for _, session := range resp.Sessions {
			if _, err := fmt.Fprintf(
				writer,
				"%s\t%s\t%s\t%s\t%s\n",
				session.ID,
				session.Agent,
				firstNonEmpty(session.ProjectID, "-"),
				firstNonEmpty(session.UpdatedAt, "-"),
				firstNonEmpty(session.Title, session.Preview, "-"),
			); err != nil {
				return fmt.Errorf("write session row: %w", err)
			}
		}
		return writer.Flush()
	case "jsonl":
		encoder := json.NewEncoder(stdout)
		for _, session := range resp.Sessions {
			if err := encoder.Encode(map[string]any{
				"schemaVersion": "paxl.session.metadata.v1",
				"id":            session.ID,
				"agent":         session.Agent,
				"nativeId":      session.NativeID,
				"title":         session.Title,
				"status":        session.Status,
				"preview":       session.Preview,
				"projectId":     session.ProjectID,
				"updatedAt":     session.UpdatedAt,
				"lastSyncedAt":  session.LastSyncedAt,
			}); err != nil {
				return fmt.Errorf("encode session: %w", err)
			}
		}
		return nil
	case "html":
		return renderSessionListHTML(stdout, resp)
	default:
		return fmt.Errorf("unsupported format %q", format)
	}
}

func renderSessionListHTML(stdout io.Writer, resp *facade.ListSessionsResponse) error {
	if _, err := fmt.Fprintln(stdout, "<!doctype html><html><body><table>"); err != nil {
		return fmt.Errorf("write html header: %w", err)
	}
	if _, err := fmt.Fprintln(
		stdout,
		"<thead><tr><th>ID</th><th>Agent</th><th>Project</th><th>Updated</th><th>Title</th></tr></thead><tbody>",
	); err != nil {
		return fmt.Errorf("write html table header: %w", err)
	}
	for _, session := range resp.Sessions {
		if _, err := fmt.Fprintf(
			stdout,
			"<tr><td>%s</td><td>%s</td><td>%s</td><td>%s</td><td>%s</td></tr>\n",
			html.EscapeString(session.ID),
			html.EscapeString(string(session.Agent)),
			html.EscapeString(firstNonEmpty(session.ProjectID, "-")),
			html.EscapeString(firstNonEmpty(session.UpdatedAt, "-")),
			html.EscapeString(firstNonEmpty(session.Title, session.Preview, "-")),
		); err != nil {
			return fmt.Errorf("write html session row: %w", err)
		}
	}
	if _, err := fmt.Fprintln(stdout, "</tbody></table></body></html>"); err != nil {
		return fmt.Errorf("write html footer: %w", err)
	}
	return nil
}

func renderSessionHTML(stdout io.Writer, resp *facade.GetSessionResponse) error {
	if _, err := fmt.Fprintf(
		stdout,
		"<!doctype html><html><body><h1>%s</h1><ol>\n",
		html.EscapeString(resp.Session.ID),
	); err != nil {
		return fmt.Errorf("write html session header: %w", err)
	}
	for _, element := range resp.Elements {
		if _, err := fmt.Fprintf(
			stdout,
			"<li><strong>%s</strong> <time>%s</time><pre>%s</pre></li>\n",
			html.EscapeString(firstNonEmpty(element.Role, element.Type, "event")),
			html.EscapeString(firstNonEmpty(element.CompletedAt, element.StartedAt)),
			html.EscapeString(element.ContentText),
		); err != nil {
			return fmt.Errorf("write html session element: %w", err)
		}
	}
	if _, err := fmt.Fprintln(stdout, "</ol></body></html>"); err != nil {
		return fmt.Errorf("write html session footer: %w", err)
	}
	return nil
}

func renderAgentList(stdout io.Writer, resp *facade.ListAgentsResponse, format string) error {
	switch format {
	case "table":
		writer := tabwriter.NewWriter(stdout, 0, 4, 2, ' ', 0)
		if _, err := fmt.Fprintln(
			writer,
			"AGENT\tSTATUS\tCLI\tSESSIONS\tCAPABILITY\tCOMMAND",
		); err != nil {
			return fmt.Errorf("write agent list header: %w", err)
		}
		for _, agent := range resp.Agents {
			if _, err := fmt.Fprintf(
				writer,
				"%s\t%s\t%s\t%s\t%s\t%s\n",
				agent.Name,
				agentStatus(agent.Available),
				availability(agent.CLIAvailable),
				availability(agent.SessionsAvailable),
				agent.Capability,
				strings.Join(agent.Command, " "),
			); err != nil {
				return fmt.Errorf("write agent row: %w", err)
			}
		}
		return writer.Flush()
	case "jsonl":
		encoder := json.NewEncoder(stdout)
		for _, agent := range resp.Agents {
			if err := encoder.Encode(agent); err != nil {
				return fmt.Errorf("encode agent: %w", err)
			}
		}
		return nil
	default:
		return fmt.Errorf("unsupported format %q", format)
	}
}

func renderSetup(stdout io.Writer, resp *facade.SetupResponse, format string) error {
	switch format {
	case "table":
		writer := tabwriter.NewWriter(stdout, 0, 4, 2, ' ', 0)
		if _, err := fmt.Fprintln(writer, "AGENT\tSTATUS\tPATH\tMESSAGE"); err != nil {
			return fmt.Errorf("write setup header: %w", err)
		}
		for _, adapter := range resp.Adapters {
			if _, err := fmt.Fprintf(
				writer,
				"%s\t%s\t%s\t%s\n",
				adapter.Agent,
				adapter.Status,
				firstNonEmpty(adapter.Path, "-"),
				adapter.Message,
			); err != nil {
				return fmt.Errorf("write setup row: %w", err)
			}
		}
		if resp.Daemon != nil {
			if _, err := fmt.Fprintf(
				writer,
				"%s\t%s\t%s\t%s\n",
				resp.Daemon.Binary,
				resp.Daemon.Status,
				firstNonEmpty(resp.Daemon.Path, "-"),
				resp.Daemon.Message,
			); err != nil {
				return fmt.Errorf("write setup daemon row: %w", err)
			}
		}
		return writer.Flush()
	case "jsonl":
		encoder := json.NewEncoder(stdout)
		for _, adapter := range resp.Adapters {
			if err := encoder.Encode(map[string]any{
				"schemaVersion": "paxl.setup.adapter.v1",
				"agent":         adapter.Agent,
				"status":        adapter.Status,
				"path":          adapter.Path,
				"message":       adapter.Message,
			}); err != nil {
				return fmt.Errorf("encode setup adapter: %w", err)
			}
		}
		if resp.Daemon != nil {
			if err := encoder.Encode(map[string]any{
				"schemaVersion": "paxl.setup.daemon.v1",
				"binary":        resp.Daemon.Binary,
				"status":        resp.Daemon.Status,
				"path":          resp.Daemon.Path,
				"action":        resp.Daemon.Action,
				"message":       resp.Daemon.Message,
			}); err != nil {
				return fmt.Errorf("encode setup daemon: %w", err)
			}
		}
		return nil
	default:
		return fmt.Errorf("unsupported format %q", format)
	}
}

func renderUpdateCheck(stdout io.Writer, resp *facade.CheckUpdateResponse, format string) error {
	switch format {
	case "text":
		if _, err := fmt.Fprintf(stdout, "Current: %s\n", resp.CurrentVersion); err != nil {
			return fmt.Errorf("write current version: %w", err)
		}
		if _, err := fmt.Fprintf(stdout, "Latest:  %s\n", resp.LatestVersion); err != nil {
			return fmt.Errorf("write latest version: %w", err)
		}
		if _, err := fmt.Fprintf(
			stdout,
			"Status:  %s\n",
			updateStatusText(resp.Status),
		); err != nil {
			return fmt.Errorf("write update status: %w", err)
		}
		if resp.UpdateAvailable {
			if _, err := fmt.Fprintln(stdout, "\nRun `paxl update` to upgrade."); err != nil {
				return fmt.Errorf("write update instruction: %w", err)
			}
		}
		return nil
	case "json", "jsonl":
		if err := json.NewEncoder(stdout).Encode(resp); err != nil {
			return fmt.Errorf("encode update check: %w", err)
		}
		return nil
	default:
		return fmt.Errorf("unsupported format %q", format)
	}
}

func renderApplyUpdate(stdout io.Writer, resp *applyUpdateResponse, format string) error {
	switch format {
	case "text":
		if resp.Updated {
			if _, err := fmt.Fprintf(
				stdout,
				"Updated paxl %s -> %s\n",
				resp.CurrentVersion,
				resp.LatestVersion,
			); err != nil {
				return fmt.Errorf("write update result: %w", err)
			}
			if _, err := fmt.Fprintf(stdout, "Path: %s\n", resp.Path); err != nil {
				return fmt.Errorf("write update path: %w", err)
			}
			return nil
		}
		if _, err := fmt.Fprintf(stdout, "Current: %s\n", resp.CurrentVersion); err != nil {
			return fmt.Errorf("write current version: %w", err)
		}
		if _, err := fmt.Fprintf(stdout, "Latest:  %s\n", resp.LatestVersion); err != nil {
			return fmt.Errorf("write latest version: %w", err)
		}
		if _, err := fmt.Fprintf(
			stdout,
			"Status:  %s\n",
			updateStatusText(resp.Status),
		); err != nil {
			return fmt.Errorf("write update status: %w", err)
		}
		return nil
	case "json", "jsonl":
		if err := json.NewEncoder(stdout).Encode(resp); err != nil {
			return fmt.Errorf("encode update result: %w", err)
		}
		return nil
	default:
		return fmt.Errorf("unsupported format %q", format)
	}
}

func renderLogin(stdout io.Writer, resp *facade.LoginResponse, format string) error {
	switch format {
	case "text":
		return renderLoginStartAndComplete(stdout, resp)
	case "json":
		if err := json.NewEncoder(stdout).Encode(resp); err != nil {
			return fmt.Errorf("encode login: %w", err)
		}
		return nil
	default:
		return fmt.Errorf("unsupported format %q", format)
	}
}

func renderLoginStartAndComplete(stdout io.Writer, resp *facade.LoginResponse) error {
	if err := renderLoginStart(stdout, &facade.LoginStart{
		ManagerURL:              resp.ManagerURL,
		UserCode:                resp.UserCode,
		VerificationURI:         resp.VerificationURI,
		VerificationURIComplete: resp.VerificationURIComplete,
	}); err != nil {
		return err
	}
	return renderLoginComplete(stdout, resp)
}

func renderLoginStart(stdout io.Writer, start *facade.LoginStart) error {
	if _, err := fmt.Fprintf(stdout, "manager %s\n", start.ManagerURL); err != nil {
		return fmt.Errorf("write login manager: %w", err)
	}
	if _, err := fmt.Fprintf(
		stdout,
		"verification %s\n",
		start.VerificationURIComplete,
	); err != nil {
		return fmt.Errorf("write login verification: %w", err)
	}
	if _, err := fmt.Fprintf(stdout, "code %s\n", start.UserCode); err != nil {
		return fmt.Errorf("write login code: %w", err)
	}
	if _, err := fmt.Fprintln(stdout, "waiting for browser approval"); err != nil {
		return fmt.Errorf("write login wait status: %w", err)
	}
	return nil
}

func renderLoginComplete(stdout io.Writer, resp *facade.LoginResponse) error {
	if resp.Credential == nil {
		return nil
	}
	if _, err := fmt.Fprintf(stdout, "logged in %s\n", resp.Credential.Email); err != nil {
		return fmt.Errorf("write login user: %w", err)
	}
	return nil
}

func renderWhoami(stdout io.Writer, resp *facade.WhoamiResponse, format string) error {
	switch format {
	case "text":
		if resp.User == nil {
			return fmt.Errorf("whoami response missing user")
		}
		if _, err := fmt.Fprintf(stdout, "manager %s\n", resp.ManagerURL); err != nil {
			return fmt.Errorf("write whoami manager: %w", err)
		}
		if _, err := fmt.Fprintf(stdout, "user %s\n", resp.User.Email); err != nil {
			return fmt.Errorf("write whoami user: %w", err)
		}
		if _, err := fmt.Fprintf(stdout, "user_id %s\n", resp.User.UserID); err != nil {
			return fmt.Errorf("write whoami user id: %w", err)
		}
		if resp.Credential != nil && resp.Credential.NodeID != "" {
			if _, err := fmt.Fprintf(stdout, "node_id %s\n", resp.Credential.NodeID); err != nil {
				return fmt.Errorf("write whoami node id: %w", err)
			}
		}
		return nil
	case "json":
		if err := json.NewEncoder(stdout).Encode(resp); err != nil {
			return fmt.Errorf("encode whoami: %w", err)
		}
		return nil
	default:
		return fmt.Errorf("unsupported format %q", format)
	}
}

func renderLogout(stdout io.Writer, resp *facade.LogoutResponse, format string) error {
	switch format {
	case "text":
		if resp.Email != "" {
			if _, err := fmt.Fprintf(stdout, "logged out %s\n", resp.Email); err != nil {
				return fmt.Errorf("write logout: %w", err)
			}
			return nil
		}
		if _, err := fmt.Fprintln(stdout, "logged out"); err != nil {
			return fmt.Errorf("write logout: %w", err)
		}
		return nil
	case "json":
		if err := json.NewEncoder(stdout).Encode(resp); err != nil {
			return fmt.Errorf("encode logout: %w", err)
		}
		return nil
	default:
		return fmt.Errorf("unsupported format %q", format)
	}
}

func renderNodeList(stdout io.Writer, resp *facade.ListNodesResponse, format string) error {
	switch format {
	case "table":
		writer := tabwriter.NewWriter(stdout, 0, 4, 2, ' ', 0)
		if _, err := fmt.Fprintln(
			writer,
			"ID\tKIND\tNAME\tHOSTNAME\tSTATUS\tCURRENT\tREGISTERED",
		); err != nil {
			return fmt.Errorf("write node list header: %w", err)
		}
		for _, node := range resp.Nodes {
			current := ""
			if node.NodeID == resp.CurrentNodeID {
				current = "*"
			}
			if _, err := fmt.Fprintf(
				writer,
				"%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
				node.NodeID,
				firstNonEmpty(node.Kind, "-"),
				firstNonEmpty(node.Name, "-"),
				firstNonEmpty(node.Hostname, "-"),
				firstNonEmpty(node.Status, "-"),
				current,
				firstNonEmpty(node.RegisteredAt, "-"),
			); err != nil {
				return fmt.Errorf("write node row: %w", err)
			}
		}
		return writer.Flush()
	case "jsonl":
		for _, node := range resp.Nodes {
			if err := encodeNodeJSONL(stdout, node, node.NodeID == resp.CurrentNodeID); err != nil {
				return err
			}
		}
		return nil
	default:
		return fmt.Errorf("unsupported format %q", format)
	}
}

func encodeNodeJSONL(stdout io.Writer, node *model.Node, current bool) error {
	if err := json.NewEncoder(stdout).Encode(map[string]any{
		"schemaVersion": "paxl.node.v1",
		"nodeId":        node.NodeID,
		"ownerUserId":   node.OwnerUserID,
		"kind":          node.Kind,
		"name":          node.Name,
		"hostname":      node.Hostname,
		"machineType":   node.MachineType,
		"os":            node.OS,
		"arch":          node.Arch,
		"paxdVersion":   node.PaxdVersion,
		"apiEndpoint":   node.APIEndpoint,
		"status":        node.Status,
		"online":        node.Online,
		"current":       current,
		"registeredAt":  node.RegisteredAt,
		"lastHeartbeat": node.LastHeartbeat,
	}); err != nil {
		return fmt.Errorf("encode node: %w", err)
	}
	return nil
}

func renderNodeAgentList(
	stdout io.Writer,
	resp *facade.ListNodeAgentsResponse,
	format string,
) error {
	switch format {
	case "table":
		writer := tabwriter.NewWriter(stdout, 0, 4, 2, ' ', 0)
		if _, err := fmt.Fprintln(
			writer,
			"ID\tNODE\tTYPE\tNAME\tSTATUS\tREGISTERED",
		); err != nil {
			return fmt.Errorf("write node agent list header: %w", err)
		}
		for _, agent := range resp.Agents {
			if _, err := fmt.Fprintf(
				writer,
				"%s\t%s\t%s\t%s\t%s\t%s\n",
				agent.AgentID,
				firstNonEmpty(agent.NodeID, resp.NodeID, "-"),
				firstNonEmpty(agent.AgentType, "-"),
				firstNonEmpty(agent.Name, "-"),
				firstNonEmpty(agent.Status, "-"),
				firstNonEmpty(agent.RegisteredAt, "-"),
			); err != nil {
				return fmt.Errorf("write node agent row: %w", err)
			}
		}
		return writer.Flush()
	case "jsonl":
		for _, agent := range resp.Agents {
			if err := encodeNodeAgentJSONL(stdout, agent); err != nil {
				return err
			}
		}
		return nil
	default:
		return fmt.Errorf("unsupported format %q", format)
	}
}

func encodeNodeAgentJSONL(stdout io.Writer, agent *model.NodeAgent) error {
	if err := json.NewEncoder(stdout).Encode(map[string]any{
		"schemaVersion": "paxl.node_agent.v1",
		"agentId":       agent.AgentID,
		"nodeId":        agent.NodeID,
		"ownerUserId":   agent.OwnerUserID,
		"name":          agent.Name,
		"agentType":     agent.AgentType,
		"status":        agent.Status,
		"online":        agent.Online,
		"registeredAt":  agent.RegisteredAt,
		"lastHeartbeat": agent.LastHeartbeat,
	}); err != nil {
		return fmt.Errorf("encode node agent: %w", err)
	}
	return nil
}

func renderNodeSessionList(
	stdout io.Writer,
	resp *facade.ListNodeSessionsResponse,
	format string,
) error {
	switch format {
	case "table":
		writer := tabwriter.NewWriter(stdout, 0, 4, 2, ' ', 0)
		if _, err := fmt.Fprintln(
			writer,
			"ID\tNODE\tAGENT\tNAME\tSTATUS\tUPDATED\tPREVIEW",
		); err != nil {
			return fmt.Errorf("write node session list header: %w", err)
		}
		for _, session := range resp.Sessions {
			if _, err := fmt.Fprintf(
				writer,
				"%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
				session.SessionID,
				firstNonEmpty(session.NodeID, resp.NodeID, "-"),
				firstNonEmpty(session.AgentID, resp.AgentID, "-"),
				firstNonEmpty(session.Name, "-"),
				firstNonEmpty(session.Status, "-"),
				firstNonEmpty(session.UpdatedAt, session.LastMessageAt, "-"),
				firstNonEmpty(session.Preview, "-"),
			); err != nil {
				return fmt.Errorf("write node session row: %w", err)
			}
		}
		return writer.Flush()
	case "jsonl":
		for _, session := range resp.Sessions {
			if err := encodeNodeSessionJSONL(stdout, session); err != nil {
				return err
			}
		}
		return nil
	default:
		return fmt.Errorf("unsupported format %q", format)
	}
}

func encodeNodeSessionJSONL(stdout io.Writer, session *model.NodeSession) error {
	if err := json.NewEncoder(stdout).Encode(map[string]any{
		"schemaVersion":  "paxl.node_session.v1",
		"id":             session.ID,
		"nodeId":         session.NodeID,
		"agentId":        session.AgentID,
		"sessionId":      session.SessionID,
		"name":           session.Name,
		"agentType":      session.AgentType,
		"projectId":      session.ProjectID,
		"preview":        session.Preview,
		"workspaceRoots": session.WorkspaceRoots,
		"source":         session.Source,
		"status":         session.Status,
		"currentTask":    session.CurrentTask,
		"lastMessageAt":  session.LastMessageAt,
		"messageCount":   session.MessageCount,
		"model":          session.Model,
		"runId":          session.RunID,
		"runStatus":      session.RunStatus,
		"createdAt":      session.CreatedAt,
		"updatedAt":      session.UpdatedAt,
	}); err != nil {
		return fmt.Errorf("encode node session: %w", err)
	}
	return nil
}

func updateStatusText(status facade.UpdateStatus) string {
	switch status {
	case facade.UpdateStatusUnknown:
		return "Unknown"
	case facade.UpdateStatusAvailable:
		return "Update available"
	case facade.UpdateStatusUpToDate:
		return "Up to date"
	case facade.UpdateStatusAhead:
		return "Current build is newer than latest stable"
	case facade.UpdateStatusDevelopment:
		return "Development build; latest stable shown"
	default:
		return "Unknown"
	}
}

func availability(available bool) string {
	if available {
		return "available"
	}
	return "missing"
}

func agentStatus(available bool) string {
	if available {
		return "online"
	}
	return "offline"
}

type executionLogger struct {
	file      *os.File
	encoder   *json.Encoder
	mu        sync.Mutex
	startedAt time.Time
	finished  bool
}

type diagnosticLogWriter struct {
	logger *executionLogger
}

func openExecutionLogger(args []string) *executionLogger {
	dir, err := executionLogDir()
	if err != nil {
		return nil
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil
	}
	now := time.Now().UTC()
	path := filepath.Join(
		dir,
		fmt.Sprintf("paxl-%s-%d.log", now.Format("20060102T150405.000000000Z"), os.Getpid()),
	)
	// The path is constructed from os.UserHomeDir plus paxl's fixed log directory.
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600) // #nosec G304
	if err != nil {
		return nil
	}
	logger := &executionLogger{
		file:      file,
		encoder:   json.NewEncoder(file),
		startedAt: now,
	}
	fields := map[string]any{
		"args":    args,
		"cwd":     currentWorkingDirectory(),
		"version": version,
		"commit":  buildCommit,
	}
	if callerAgent := callerAgentNameFromInvocation(args); callerAgent != model.AgentNameUnknown {
		fields["callerAgent"] = callerAgent
	}
	logger.write("command_start", fields)
	return logger
}

func callerAgentNameFromInvocation(args []string) model.AgentName {
	raw := callerAgentRawFromArgs(args)
	if strings.TrimSpace(raw) == "" {
		raw = firstNonEmpty(os.Getenv("PAXL_CALLER_AGENT"), os.Getenv("PAXL_AGENT"))
	}
	agent, err := model.ParseAgentName(raw)
	if err != nil {
		return model.AgentNameUnknown
	}
	return agent
}

func callerAgentRawFromArgs(args []string) string {
	for index := 0; index < len(args); index++ {
		arg := args[index]
		if arg == "--" {
			return ""
		}
		if arg == "--caller-agent" {
			if index+1 >= len(args) {
				return ""
			}
			return args[index+1]
		}
		if value, ok := strings.CutPrefix(arg, "--caller-agent="); ok {
			return value
		}
	}
	return ""
}

func executionLogDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".pax", "paxl", "logs"), nil
}

func diagnosticWriter(logger *executionLogger) io.Writer {
	if logger == nil {
		return nil
	}
	return &diagnosticLogWriter{logger: logger}
}

func (w *diagnosticLogWriter) Write(data []byte) (int, error) {
	message := strings.TrimSpace(string(data))
	if message != "" {
		w.logger.write("diagnostic", map[string]any{"message": message})
	}
	return len(data), nil
}

func finishExecutionLog(logger *executionLogger, runErr error) {
	if logger == nil {
		return
	}
	fields := map[string]any{
		"durationMs": time.Since(logger.startedAt).Milliseconds(),
		"status":     "ok",
	}
	if runErr != nil {
		fields["status"] = "error"
		fields["error"] = runErr.Error()
	}
	logger.mu.Lock()
	if logger.finished {
		logger.mu.Unlock()
		return
	}
	logger.finished = true
	logger.mu.Unlock()
	logger.write("command_finish", fields)
}

func closeExecutionLogger(logger *executionLogger) {
	if logger == nil {
		return
	}
	_ = logger.file.Close()
}

func (l *executionLogger) write(event string, fields map[string]any) {
	if l == nil || l.encoder == nil {
		return
	}
	record := map[string]any{
		"timestamp": time.Now().UTC().Format(time.RFC3339Nano),
		"event":     event,
	}
	for key, value := range fields {
		record[key] = value
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	_ = l.encoder.Encode(record)
}

func currentWorkingDirectory() string {
	cwd, err := os.Getwd()
	if err != nil {
		return ""
	}
	return cwd
}

func verboseWriter(cmd *cli.Command, stderr io.Writer, diagnostics io.Writer) io.Writer {
	if cmd.Bool("verbose") && diagnostics != nil {
		return io.MultiWriter(stderr, diagnostics)
	}
	if cmd.Bool("verbose") {
		return stderr
	}
	return diagnostics
}

func closeStore(sessionStore *store.Store) {
	_ = sessionStore.Close()
}

func closeFile(file *os.File) {
	_ = file.Close()
}

func defaultHTMLPath(session *model.Session) string {
	name := strings.NewReplacer(":", "_", "/", "_", "\\", "_").Replace(session.ID)
	return filepath.Join(os.TempDir(), "paxl-"+name+".html")
}

func defaultLoginClientName() string {
	ifaces, err := net.Interfaces()
	if err != nil {
		return loginClientName("")
	}
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 ||
			iface.Flags&net.FlagLoopback != 0 ||
			len(iface.HardwareAddr) == 0 {
			continue
		}
		return loginClientName(iface.HardwareAddr.String())
	}
	return loginClientName("")
}

func loginClientName(macAddress string) string {
	prefix := fmt.Sprintf("paxl-%s-%s", runtime.GOOS, runtime.GOARCH)
	if macAddress == "" {
		return prefix
	}
	sum := sha256.Sum256([]byte(strings.ToLower(macAddress)))
	return prefix + "-" + hex.EncodeToString(sum[:])[:8]
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
