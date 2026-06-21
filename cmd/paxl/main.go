package main

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime/debug"
	"runtime/pprof"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/pax-oss/paxl/internal/facade"
	"github.com/pax-oss/paxl/internal/model"
	"github.com/pax-oss/paxl/internal/model/store"
	"github.com/urfave/cli/v3"
)

var version = "0.1.0"
var buildCommit = ""

func main() {
	if err := run(context.Background(), os.Args[1:], os.Stdout, os.Stderr); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, "Error:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, stdout io.Writer, stderr io.Writer) error {
	command := newCommand(stdout, stderr)
	if err := command.Run(ctx, append([]string{"paxl"}, args...)); err != nil {
		return fmt.Errorf("run paxl command: %w", err)
	}
	return nil
}

func newCommand(stdout io.Writer, stderr io.Writer) *cli.Command {
	agentFacade := facade.NewAgentFacade(nil)
	return &cli.Command{
		Name:      "paxl",
		Usage:     "Local-first Pax agent session tools",
		Writer:    stdout,
		ErrWriter: stderr,
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "db", Usage: "SQLite database path"},
		},
		ExitErrHandler: func(ctx context.Context, cmd *cli.Command, err error) {
			_ = ctx
			_ = cmd
			_ = err
		},
		Commands: []*cli.Command{
			newVersionCommand(stdout),
			newAgentCommand(agentFacade, stdout, stderr),
			newSessionCommand(stdout, stderr),
			newCapsuleCommand(stdout, stderr),
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
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			_ = ctx
			return versionCommand(cmd, stdout)
		},
	}
}

func newAgentCommand(
	agentFacade *facade.AgentFacade,
	stdout io.Writer,
	stderr io.Writer,
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
					return agentList(ctx, cmd, agentFacade, stdout, stderr)
				},
			},
			{
				Name:  "setup",
				Usage: "Install supported local agent CLIs",
				Flags: []cli.Flag{
					&cli.StringFlag{
						Name:  "agent",
						Usage: "Comma-separated agents to install: codex or claude",
					},
					&cli.BoolFlag{
						Name:  "dry-run",
						Usage: "Print install commands without running them",
					},
				},
				Action: func(ctx context.Context, cmd *cli.Command) error {
					return agentSetup(ctx, cmd, stdout, stderr)
				},
			},
		},
	}
}

func newSessionCommand(stdout io.Writer, stderr io.Writer) *cli.Command {
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
						Usage: "Agent to scan: codex, claude, pi, or kiro",
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
					return sessionList(ctx, cmd, stdout, stderr)
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
					return sessionGet(ctx, cmd, stdout, stderr)
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
					return sessionMirror(ctx, cmd, stdout, stderr)
				},
			},
		},
	}
}

func newCapsuleCommand(stdout io.Writer, stderr io.Writer) *cli.Command {
	return &cli.Command{
		Name:  "capsule",
		Usage: "Create, list, and inject knowledge capsules",
		Commands: []*cli.Command{
			{
				Name:      "create",
				Usage:     "Ask the source session to produce a knowledge capsule",
				ArgsUsage: "<source-session-id>",
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
					return capsuleCreate(ctx, cmd, stdout, stderr)
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
				Name:      "inject",
				Usage:     "Inject a knowledge capsule into a target session",
				ArgsUsage: "<capsule-id> [target-session-id]",
				Flags: []cli.Flag{
					&cli.StringFlag{
						Name:  "agent",
						Usage: "Agent for bare native target session IDs or new sessions",
					},
					&cli.StringFlag{
						Name:  "output",
						Usage: "Also write the sent system_handoff message to this path",
					},
					&cli.StringFlag{
						Name:  "timeout",
						Value: "30s",
						Usage: "Agent delivery timeout, for example 10s or 1m",
					},
					&cli.BoolFlag{Name: "new", Usage: "Start a new target agent session"},
					&cli.BoolFlag{Name: "verbose", Usage: "Print injection delivery details"},
				},
				Action: func(ctx context.Context, cmd *cli.Command) error {
					return capsuleInject(ctx, cmd, stdout, stderr)
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

func agentList(
	ctx context.Context,
	cmd *cli.Command,
	agentFacade *facade.AgentFacade,
	stdout io.Writer,
	stderr io.Writer,
) error {
	req, err := parseListAgentsRequest(cmd)
	if err != nil {
		return fmt.Errorf("parse agent list request: %w", err)
	}
	resp, err := agentFacade.List(ctx, req, facade.WithVerboseWriter(verboseWriter(cmd, stderr)))
	if err != nil {
		return fmt.Errorf("list agents: %w", err)
	}
	if err := renderAgentList(stdout, resp, cmd.String("format")); err != nil {
		return fmt.Errorf("render agent list: %w", err)
	}
	return nil
}

type versionMetadata struct {
	Version string `json:"version"`
	Commit  string `json:"commit"`
	Dirty   string `json:"dirty,omitempty"`
}

func versionCommand(cmd *cli.Command, stdout io.Writer) error {
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

func agentSetup(ctx context.Context, cmd *cli.Command, stdout io.Writer, stderr io.Writer) error {
	_ = stderr
	agents, err := parseAgentSelection(cmd.String("agent"))
	if err != nil {
		return fmt.Errorf("parse setup agents: %w", err)
	}
	for _, agent := range agents {
		if agentCommandAvailable(agent) && !cmd.Bool("dry-run") {
			if _, err := fmt.Fprintf(stdout, "%s already available.\n", agent); err != nil {
				return fmt.Errorf("write setup status: %w", err)
			}
			continue
		}
		if err := runAgentInstall(ctx, stdout, agent, cmd.Bool("dry-run")); err != nil {
			return err
		}
	}
	return nil
}

func sessionList(ctx context.Context, cmd *cli.Command, stdout io.Writer, stderr io.Writer) error {
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
		facade.WithVerboseWriter(verboseWriter(cmd, stderr)),
	)
	if err != nil {
		return fmt.Errorf("list sessions: %w", err)
	}
	if err := renderSessionList(stdout, resp, cmd.String("format")); err != nil {
		return fmt.Errorf("render session list: %w", err)
	}
	return nil
}

func sessionGet(ctx context.Context, cmd *cli.Command, stdout io.Writer, stderr io.Writer) error {
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
	resp, err := sessionFacade.Get(ctx, req, facade.WithVerboseWriter(verboseWriter(cmd, stderr)))
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
		facade.WithVerboseWriter(verboseWriter(cmd, stderr)),
	)
	if err != nil {
		return fmt.Errorf("mirror session: %w", err)
	}
	if err := renderMirrorResult(stdout, resp, cmd.String("format")); err != nil {
		return fmt.Errorf("render mirror result: %w", err)
	}
	return nil
}

func capsuleCreate(
	ctx context.Context,
	cmd *cli.Command,
	stdout io.Writer,
	stderr io.Writer,
) error {
	req, err := parseCreateCapsuleRequest(cmd)
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
		facade.WithVerboseWriter(verboseWriter(cmd, stderr)),
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

func capsuleInject(
	ctx context.Context,
	cmd *cli.Command,
	stdout io.Writer,
	stderr io.Writer,
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
		facade.WithVerboseWriter(verboseWriter(cmd, stderr)),
	)
	if err != nil {
		return fmt.Errorf("inject capsule: %w", err)
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
		agent, err := model.ParseAgentName(rawAgent)
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
		agent, err := model.ParseAgentName(rawAgent)
		if err != nil {
			return nil, fmt.Errorf("parse source agent: %w", err)
		}
		req.Agent = agent
	}
	if rawTargetAgent := strings.TrimSpace(cmd.String("to")); rawTargetAgent != "" {
		agent, err := model.ParseAgentName(rawTargetAgent)
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

func parseCreateCapsuleRequest(cmd *cli.Command) (*facade.CreateCapsuleRequest, error) {
	sourceID := cmd.Args().First()
	if sourceID == "" {
		return nil, fmt.Errorf("source session id is required")
	}
	req := &facade.CreateCapsuleRequest{
		SourceSessionID: sourceID,
		Keyword:         strings.TrimSpace(cmd.String("keyword")),
		Local:           cmd.Bool("local"),
	}
	if rawAgent := strings.TrimSpace(cmd.String("agent")); rawAgent != "" {
		agent, err := model.ParseAgentName(rawAgent)
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

func parseInjectCapsuleRequest(cmd *cli.Command) (*facade.InjectCapsuleRequest, error) {
	if cmd.Args().Len() < 1 {
		return nil, fmt.Errorf("capsule id is required")
	}
	req := &facade.InjectCapsuleRequest{
		CapsuleID:       cmd.Args().Get(0),
		TargetSessionID: cmd.Args().Get(1),
		NewSession:      cmd.Bool("new"),
	}
	if rawAgent := strings.TrimSpace(cmd.String("agent")); rawAgent != "" {
		agent, err := model.ParseAgentName(rawAgent)
		if err != nil {
			return nil, fmt.Errorf("parse agent: %w", err)
		}
		req.Agent = agent
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
	return req, nil
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

func parseAgentSelection(raw string) ([]model.AgentName, error) {
	values := parseCSV(raw)
	if len(values) == 0 {
		return []model.AgentName{model.AgentNameCodex, model.AgentNameClaude}, nil
	}
	agents := make([]model.AgentName, 0, len(values))
	for _, value := range values {
		agent, err := model.ParseAgentName(value)
		if err != nil {
			return nil, err
		}
		agents = append(agents, agent)
	}
	return agents, nil
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

func agentCommandAvailable(agent model.AgentName) bool {
	_, err := exec.LookPath(string(agent))
	return err == nil
}

func runAgentInstall(
	ctx context.Context,
	stdout io.Writer,
	agent model.AgentName,
	dryRun bool,
) error {
	command, err := agentInstallCommand(agent)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(stdout, "$ %s\n", strings.Join(command, " ")); err != nil {
		return fmt.Errorf("write setup command: %w", err)
	}
	if dryRun {
		return nil
	}
	// The command is selected from a fixed allowlist for supported local agents.
	proc := exec.CommandContext(ctx, command[0], command[1:]...) // #nosec G204
	proc.Stdout = stdout
	proc.Stderr = stdout
	if err := proc.Run(); err != nil {
		return fmt.Errorf("install %s: %w", agent, err)
	}
	return nil
}

func agentInstallCommand(agent model.AgentName) ([]string, error) {
	switch agent {
	case model.AgentNameCodex:
		return []string{"npm", "install", "-g", "@openai/codex"}, nil
	case model.AgentNameClaude:
		return []string{"npm", "install", "-g", "@anthropic-ai/claude-code"}, nil
	case model.AgentNamePi, model.AgentNameKiro:
		return nil, fmt.Errorf("%s install is not managed by paxl setup", agent)
	case model.AgentNameUnknown:
		return nil, fmt.Errorf("unknown agent cannot be installed")
	default:
		return nil, fmt.Errorf("unsupported agent %q", agent)
	}
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
			"sourceSessionId": resp.Capsule.SourceSessionID,
			"sourceAgent":     resp.Capsule.SourceAgent,
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
				"targetSessionId":     injection.TargetSessionID,
				"targetAgent":         injection.TargetAgent,
				"deliveryMethod":      injection.DeliveryMethod,
				"deliveryMessageType": injection.DeliveryMessageType,
				"status":              injection.Status,
				"createdAt":           injection.CreatedAt,
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
		"Title: %s\nKeyword: %s\nSource session: %s\nStatus: %s\nCreated: %s\n\nSummary:\n%s\n\nContent:\n%s",
		capsule.Title,
		capsule.Keyword,
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
		if _, err := fmt.Fprintln(writer, "AGENT\tSTATUS\tCAPABILITY\tCOMMAND"); err != nil {
			return fmt.Errorf("write agent list header: %w", err)
		}
		for _, agent := range resp.Agents {
			if _, err := fmt.Fprintf(
				writer,
				"%s\t%s\t%s\t%s\n",
				agent.Name,
				availability(agent.Available),
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

func availability(available bool) string {
	if available {
		return "available"
	}
	return "missing"
}

func verboseWriter(cmd *cli.Command, stderr io.Writer) io.Writer {
	if cmd.Bool("verbose") {
		return stderr
	}
	return nil
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

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
