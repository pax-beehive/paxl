package adaptor

import (
	"context"

	"github.com/pax-oss/paxl/internal/model"
)

type ReadConfigurationRequest struct {
	Agent            model.AgentName
	CWD              string
	Kind             model.AgentConfigurationKind
	IncludeContent   bool
	IncludeSensitive bool
	// Home and Environment allow isolated snapshots without falling back to a
	// different user's configuration. A non-nil Environment is used exclusively.
	Home        string
	Environment map[string]string
}

type configurationReader struct {
	ctx    context.Context
	req    *ReadConfigurationRequest
	home   string
	cwd    string
	root   string
	env    map[string]string
	result *model.AgentConfiguration
}

// ReadConfiguration inventories native files without launching an agent or MCP.
// Implementation and shared parsing helpers live beside the native readers.
func ReadConfiguration(
	ctx context.Context,
	req *ReadConfigurationRequest,
	opts ...func(*Option),
) (*model.AgentConfiguration, error) {
	return readConfiguration(ctx, req, opts...)
}
