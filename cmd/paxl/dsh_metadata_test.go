package main

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/pax-oss/paxl/internal/facade"
	"github.com/pax-oss/paxl/internal/model"
	"github.com/stretchr/testify/require"
)

func TestSessionListJSONPreservesDSHWorkspace(t *testing.T) {
	var output bytes.Buffer
	err := renderSessionList(&output, &facade.ListSessionsResponse{Sessions: []*model.Session{{
		ID:                 "dsh:native",
		Agent:              model.AgentNameDSH,
		Title:              "Named session",
		WorkspaceRootsJSON: `["/work"]`,
	}}}, "jsonl")
	require.NoError(t, err)
	var row map[string]any
	require.NoError(t, json.Unmarshal(output.Bytes(), &row))
	require.Equal(t, []any{"/work"}, row["workspaceRoots"])
}
