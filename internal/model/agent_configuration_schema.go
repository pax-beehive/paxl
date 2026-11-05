package model

import _ "embed"

//go:embed agent_configuration.schema.json
var agentConfigurationSchema []byte

func AgentConfigurationSchema() []byte {
	return append([]byte(nil), agentConfigurationSchema...)
}
