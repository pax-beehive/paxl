package model

import (
	"fmt"
	"strings"
)

type AgentName string

const (
	AgentNameUnknown  AgentName = "unknown"
	AgentNameCodex    AgentName = "codex"
	AgentNameClaude   AgentName = "claude"
	AgentNamePi       AgentName = "pi"
	AgentNameKiro     AgentName = "kiro"
	AgentNameOpenCode AgentName = "opencode"
	AgentNameKimi     AgentName = "kimi"
	AgentNameGemini   AgentName = "gemini"
	AgentNameHermes   AgentName = "hermes"
	AgentNameOpenClaw AgentName = "openclaw"
	AgentNamePaxl     AgentName = "paxl"
)

var supportedAgentNames = map[AgentName]struct{}{
	AgentNameCodex:    {},
	AgentNameClaude:   {},
	AgentNamePi:       {},
	AgentNameKiro:     {},
	AgentNameOpenCode: {},
	AgentNameKimi:     {},
	AgentNameGemini:   {},
	AgentNameHermes:   {},
	AgentNameOpenClaw: {},
	AgentNamePaxl:     {},
}

func ParseAgentName(raw string) (AgentName, error) {
	agent := AgentName(strings.TrimSpace(strings.ToLower(raw)))
	if agent == AgentNameUnknown {
		return AgentNameUnknown, fmt.Errorf("parse agent name %q: unsupported agent", raw)
	}
	if _, ok := supportedAgentNames[agent]; !ok {
		return AgentNameUnknown, fmt.Errorf("parse agent name %q: unsupported agent", raw)
	}
	return agent, nil
}

type AgentKind string

const (
	AgentKindUnknown AgentKind = "unknown"
	AgentKindLocal   AgentKind = "local"
)

type AgentCapability string

const (
	AgentCapabilityUnknown  AgentCapability = "unknown"
	AgentCapabilityLocalCLI AgentCapability = "local_cli"
	AgentCapabilityGateway  AgentCapability = "gateway"
)

type AgentInfo struct {
	Name              AgentName
	Kind              AgentKind
	Available         bool
	CLIAvailable      bool
	SessionsAvailable bool
	Capability        AgentCapability
	Command           []string
	Reason            string
}
