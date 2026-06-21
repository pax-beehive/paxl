package model

import (
	"fmt"
	"strings"
)

type AgentName string

const (
	AgentNameUnknown AgentName = "unknown"
	AgentNameCodex   AgentName = "codex"
	AgentNameClaude  AgentName = "claude"
	AgentNamePi      AgentName = "pi"
	AgentNameKiro    AgentName = "kiro"
)

func ParseAgentName(raw string) (AgentName, error) {
	switch AgentName(strings.TrimSpace(strings.ToLower(raw))) {
	case AgentNameUnknown:
		return AgentNameUnknown, fmt.Errorf("parse agent name %q: unsupported agent", raw)
	case AgentNameCodex:
		return AgentNameCodex, nil
	case AgentNameClaude:
		return AgentNameClaude, nil
	case AgentNamePi:
		return AgentNamePi, nil
	case AgentNameKiro:
		return AgentNameKiro, nil
	default:
		return AgentNameUnknown, fmt.Errorf("parse agent name %q: unsupported agent", raw)
	}
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
)

type AgentInfo struct {
	Name       AgentName
	Kind       AgentKind
	Available  bool
	Capability AgentCapability
	Command    []string
	Reason     string
}
