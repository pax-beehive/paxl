# paxl Team Read-Only Integration Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add read-only `paxl team` commands so the local node can discover which (teammate) agents it could deliver to, by reading the pax-manager Team API.

**Architecture:** Mirror the existing `FriendFacade` pattern exactly — new `model.Team*` types, a read-only `TeamFacade` that calls manager REST via `auth.managerJSON`, and a `paxl team` command tree in `cmd/paxl/main.go`. No mutations, no local SQLite caching; data is fetched live.

**Tech Stack:** Go, `github.com/urfave/cli/v3`, `testify/suite`, local SQLite store (auth credential only), pax-manager REST under `/api/v1/user/:user_id`.

**Spec:** `docs/superpowers/specs/2026-06-27-paxl-team-readonly-integration-design.md`

**Deviation from spec (intentional):** The spec mentions modeling role/status as enum types. The existing models (`model.Node`, `model.Friend`) use plain `string` for status fields, and this read-only iteration never branches on role/status. To avoid unused dead code (YAGNI) and match the codebase, role/status stay plain `string`. Also, `model.NodeAgent` has no `hostname` field, so the display fallback is `name → agent_id` (hostname is only on `model.Node`, not the agent).

---

### Task 1: Team domain models

**Files:**
- Create: `internal/model/team.go`
- Test: `internal/model/team_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/model/team_test.go`:

```go
package model

import (
	"encoding/json"
	"testing"
)

func TestTeamSummaryUnmarshalsEmbeddedTeamAndCounts(t *testing.T) {
	const raw = `{
		"team_id":"team_1",
		"owner_user_id":"usr_owner",
		"name":"Core",
		"status":"active",
		"created_at":"2026-06-27T00:00:00Z",
		"my_role":"operator",
		"member_count":3,
		"agent_count":5
	}`
	var summary TeamSummary
	if err := json.Unmarshal([]byte(raw), &summary); err != nil {
		t.Fatalf("unmarshal team summary: %v", err)
	}
	if summary.TeamID != "team_1" {
		t.Errorf("team id = %q, want team_1", summary.TeamID)
	}
	if summary.Name != "Core" {
		t.Errorf("name = %q, want Core", summary.Name)
	}
	if summary.MyRole != "operator" {
		t.Errorf("my role = %q, want operator", summary.MyRole)
	}
	if summary.MemberCount != 3 || summary.AgentCount != 5 {
		t.Errorf("counts = %d/%d, want 3/5", summary.MemberCount, summary.AgentCount)
	}
}

func TestTeamAgentUnmarshalsEmbeddedAgent(t *testing.T) {
	const raw = `{
		"team_id":"team_1",
		"agent_id":"agent_9",
		"agent_owner_user_id":"usr_mate",
		"added_at":"2026-06-27T00:00:00Z",
		"agent":{"agent_id":"agent_9","name":"codex-laptop","online":true}
	}`
	var teamAgent TeamAgent
	if err := json.Unmarshal([]byte(raw), &teamAgent); err != nil {
		t.Fatalf("unmarshal team agent: %v", err)
	}
	if teamAgent.AgentOwnerUserID != "usr_mate" {
		t.Errorf("owner = %q, want usr_mate", teamAgent.AgentOwnerUserID)
	}
	if teamAgent.Agent == nil || teamAgent.Agent.Name != "codex-laptop" {
		t.Fatalf("embedded agent not decoded: %+v", teamAgent.Agent)
	}
	if !teamAgent.Agent.Online {
		t.Error("expected agent online")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/model/ -run 'TestTeam' -v`
Expected: FAIL — `undefined: TeamSummary` / `undefined: TeamAgent`.

- [ ] **Step 3: Write minimal implementation**

Create `internal/model/team.go`:

```go
package model

// Team is a pax-manager team.
type Team struct {
	TeamID      string `json:"team_id"`
	OwnerUserID string `json:"owner_user_id,omitempty"`
	Name        string `json:"name,omitempty"`
	Status      string `json:"status,omitempty"`
	CreatedAt   string `json:"created_at,omitempty"`
	ArchivedAt  string `json:"archived_at,omitempty"`
}

// TeamSummary augments Team with the caller's role and member/agent counts.
type TeamSummary struct {
	Team
	MyRole      string `json:"my_role,omitempty"`
	MemberCount int    `json:"member_count,omitempty"`
	AgentCount  int    `json:"agent_count,omitempty"`
}

// TeamAgent binds a registered agent to a team. Agent is included by the manager
// when available and reuses the registered node-agent shape.
type TeamAgent struct {
	TeamID           string     `json:"team_id"`
	AgentID          string     `json:"agent_id"`
	AgentOwnerUserID string     `json:"agent_owner_user_id,omitempty"`
	AddedByUserID    string     `json:"added_by_user_id,omitempty"`
	AddedAt          string     `json:"added_at,omitempty"`
	RemovedAt        string     `json:"removed_at,omitempty"`
	RemovedByUserID  string     `json:"removed_by_user_id,omitempty"`
	Agent            *NodeAgent `json:"agent,omitempty"`
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/model/ -run 'TestTeam' -v`
Expected: PASS (both tests).

- [ ] **Step 5: Commit**

```bash
git add internal/model/team.go internal/model/team_test.go
git commit -m "$(printf 'Add team domain models\n\nCo-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>')"
```

---

### Task 2: TeamFacade.ListTeams

**Files:**
- Create: `internal/facade/team.go`
- Create: `internal/facade/team_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/facade/team_test.go`:

```go
package facade

import (
	"context"
	"net/http"
	"path/filepath"
	"testing"

	"github.com/pax-oss/paxl/internal/model"
	"github.com/pax-oss/paxl/internal/model/store"
	"github.com/stretchr/testify/suite"
)

type TeamFacadeSuite struct {
	suite.Suite
	ctx   context.Context
	store *store.Store
}

func TestTeamFacadeSuite(t *testing.T) {
	suite.Run(t, new(TeamFacadeSuite))
}

func (s *TeamFacadeSuite) SetupTest() {
	s.ctx = context.Background()
	opened, err := store.Open(
		s.ctx,
		&store.OpenRequest{Path: filepath.Join(s.T().TempDir(), "paxl.sqlite")},
	)
	s.Require().NoError(err)
	s.store = opened.Store
	s.seedCredential()
}

func (s *TeamFacadeSuite) TearDownTest() {
	s.Require().NoError(s.store.Close())
}

func (s *TeamFacadeSuite) seedCredential() {
	_, err := s.store.SaveAuthCredential(s.ctx, &store.SaveAuthCredentialRequest{
		Credential: &model.AuthCredential{
			ManagerURL: "https://manager.example",
			APIKey:     "paxu_test",
			UserID:     "usr_1",
			Email:      "cli@example.com",
		},
	})
	s.Require().NoError(err)
}

func (s *TeamFacadeSuite) TestListTeamsGetsSummaries() {
	client := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		s.Equal(http.MethodGet, req.Method)
		s.Equal("/api/v1/user/usr_1/teams", req.URL.Path)
		s.Equal("Bearer paxu_test", req.Header.Get("Authorization"))
		return jsonResponse(`{
			"data":{"teams":[
				{"team_id":"team_1","name":"Core","my_role":"owner","member_count":2,"agent_count":3}
			]},
			"code":200,"message":"ok"
		}`), nil
	})
	teamFacade := NewTeamFacade(client, s.store)

	resp, err := teamFacade.ListTeams(s.ctx, &ListTeamsRequest{})

	s.Require().NoError(err)
	s.Require().Len(resp.Teams, 1)
	s.Equal("team_1", resp.Teams[0].TeamID)
	s.Equal("owner", resp.Teams[0].MyRole)
	s.Equal("usr_1", resp.UserID)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/facade/ -run 'TestTeamFacadeSuite/TestListTeamsGetsSummaries' -v`
Expected: FAIL — `undefined: NewTeamFacade` / `undefined: ListTeamsRequest`.

- [ ] **Step 3: Write minimal implementation**

Create `internal/facade/team.go`:

```go
package facade

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/pax-oss/paxl/internal/model"
	"github.com/pax-oss/paxl/internal/model/store"
)

type TeamFacade struct {
	auth *AuthFacade
}

func NewTeamFacade(client AuthHTTPClient, sessionStore *store.Store) *TeamFacade {
	return &TeamFacade{auth: NewAuthFacade(client, sessionStore)}
}

type ListTeamsRequest struct{}

type ListTeamsResponse struct {
	Teams  []*model.TeamSummary
	UserID string
}

func (f *TeamFacade) ListTeams(
	ctx context.Context,
	req *ListTeamsRequest,
	opts ...func(*Option),
) (*ListTeamsResponse, error) {
	_ = applyOptions(opts)
	credential, err := f.auth.requireCredential(ctx)
	if err != nil {
		return nil, err
	}
	var envelope managerEnvelope[struct {
		Teams []*model.TeamSummary `json:"teams"`
	}]
	if err := f.auth.managerJSON(
		ctx,
		http.MethodGet,
		credential.ManagerURL,
		userTeamPath(credential.UserID, "", ""),
		credential.APIKey,
		nil,
		&envelope,
	); err != nil {
		return nil, err
	}
	return &ListTeamsResponse{Teams: envelope.Data.Teams, UserID: credential.UserID}, nil
}

func userTeamPath(userID string, teamID string, sub string) string {
	path := "/api/v1/user/" + url.PathEscape(userID) + "/teams"
	if teamID != "" {
		path += "/" + url.PathEscape(teamID)
	}
	if sub != "" {
		path += "/" + sub
	}
	return path
}

// ensure strings is used by later tasks in this file.
var _ = strings.TrimSpace
```

> Note: remove the `var _ = strings.TrimSpace` line in Task 3 once `strings` is genuinely used. It exists only so this file compiles in isolation.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/facade/ -run 'TestTeamFacadeSuite/TestListTeamsGetsSummaries' -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/facade/team.go internal/facade/team_test.go
git commit -m "$(printf 'Add TeamFacade.ListTeams\n\nCo-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>')"
```

---

### Task 3: TeamFacade.GetTeam and ListAgents

**Files:**
- Modify: `internal/facade/team.go`
- Modify: `internal/facade/team_test.go`

- [ ] **Step 1: Write the failing tests**

Append to `internal/facade/team_test.go`:

```go
func (s *TeamFacadeSuite) TestGetTeamReturnsTeam() {
	client := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		s.Equal(http.MethodGet, req.Method)
		s.Equal("/api/v1/user/usr_1/teams/team_1", req.URL.Path)
		return jsonResponse(`{
			"data":{"team":{"team_id":"team_1","name":"Core","status":"active"}},
			"code":200,"message":"ok"
		}`), nil
	})
	teamFacade := NewTeamFacade(client, s.store)

	resp, err := teamFacade.GetTeam(s.ctx, &GetTeamRequest{TeamID: "team_1"})

	s.Require().NoError(err)
	s.Equal("Core", resp.Team.Name)
	s.Equal("usr_1", resp.UserID)
}

func (s *TeamFacadeSuite) TestGetTeamRequiresTeamID() {
	teamFacade := NewTeamFacade(roundTripFunc(func(*http.Request) (*http.Response, error) {
		s.Fail("manager should not be called")
		return nil, nil
	}), s.store)

	_, err := teamFacade.GetTeam(s.ctx, &GetTeamRequest{})

	s.Require().Error(err)
	s.Contains(err.Error(), "team id is required")
}

func (s *TeamFacadeSuite) TestListAgentsGetsTeamAgents() {
	client := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		s.Equal(http.MethodGet, req.Method)
		s.Equal("/api/v1/user/usr_1/teams/team_1/agents", req.URL.Path)
		return jsonResponse(`{
			"data":{"agents":[
				{"team_id":"team_1","agent_id":"agent_9","agent_owner_user_id":"usr_mate",
				 "agent":{"agent_id":"agent_9","name":"codex-laptop","online":true}}
			]},
			"code":200,"message":"ok"
		}`), nil
	})
	teamFacade := NewTeamFacade(client, s.store)

	resp, err := teamFacade.ListAgents(s.ctx, &ListTeamAgentsRequest{TeamID: "team_1"})

	s.Require().NoError(err)
	s.Require().Len(resp.Agents, 1)
	s.Equal("agent_9", resp.Agents[0].AgentID)
	s.Equal("team_1", resp.TeamID)
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/facade/ -run 'TestTeamFacadeSuite/TestGetTeam|TestTeamFacadeSuite/TestListAgents' -v`
Expected: FAIL — `undefined: GetTeamRequest` / `undefined: ListTeamAgentsRequest`.

- [ ] **Step 3: Write minimal implementation**

In `internal/facade/team.go`, delete the `var _ = strings.TrimSpace` placeholder line, and add:

```go
type GetTeamRequest struct {
	TeamID string
}

type GetTeamResponse struct {
	Team   *model.Team
	UserID string
}

func (f *TeamFacade) GetTeam(
	ctx context.Context,
	req *GetTeamRequest,
	opts ...func(*Option),
) (*GetTeamResponse, error) {
	_ = applyOptions(opts)
	if req == nil || strings.TrimSpace(req.TeamID) == "" {
		return nil, fmt.Errorf("get team: team id is required")
	}
	credential, err := f.auth.requireCredential(ctx)
	if err != nil {
		return nil, err
	}
	var envelope managerEnvelope[struct {
		Team model.Team `json:"team"`
	}]
	if err := f.auth.managerJSON(
		ctx,
		http.MethodGet,
		credential.ManagerURL,
		userTeamPath(credential.UserID, strings.TrimSpace(req.TeamID), ""),
		credential.APIKey,
		nil,
		&envelope,
	); err != nil {
		return nil, err
	}
	return &GetTeamResponse{Team: &envelope.Data.Team, UserID: credential.UserID}, nil
}

type ListTeamAgentsRequest struct {
	TeamID string
}

type ListTeamAgentsResponse struct {
	Agents []*model.TeamAgent
	TeamID string
	UserID string
}

func (f *TeamFacade) ListAgents(
	ctx context.Context,
	req *ListTeamAgentsRequest,
	opts ...func(*Option),
) (*ListTeamAgentsResponse, error) {
	_ = applyOptions(opts)
	if req == nil || strings.TrimSpace(req.TeamID) == "" {
		return nil, fmt.Errorf("list team agents: team id is required")
	}
	credential, err := f.auth.requireCredential(ctx)
	if err != nil {
		return nil, err
	}
	teamID := strings.TrimSpace(req.TeamID)
	var envelope managerEnvelope[struct {
		Agents []*model.TeamAgent `json:"agents"`
	}]
	if err := f.auth.managerJSON(
		ctx,
		http.MethodGet,
		credential.ManagerURL,
		userTeamPath(credential.UserID, teamID, "agents"),
		credential.APIKey,
		nil,
		&envelope,
	); err != nil {
		return nil, err
	}
	return &ListTeamAgentsResponse{
		Agents: envelope.Data.Agents,
		TeamID: teamID,
		UserID: credential.UserID,
	}, nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/facade/ -run 'TestTeamFacadeSuite' -v`
Expected: PASS (all team facade tests so far).

- [ ] **Step 5: Commit**

```bash
git add internal/facade/team.go internal/facade/team_test.go
git commit -m "$(printf 'Add TeamFacade.GetTeam and ListAgents\n\nCo-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>')"
```

---

### Task 4: TeamFacade.ListAllAgents aggregation

**Files:**
- Modify: `internal/facade/team.go`
- Modify: `internal/facade/team_test.go`

- [ ] **Step 1: Write the failing tests**

Append to `internal/facade/team_test.go`:

```go
func (s *TeamFacadeSuite) teamGraphClient() roundTripFunc {
	return roundTripFunc(func(req *http.Request) (*http.Response, error) {
		switch req.URL.Path {
		case "/api/v1/user/usr_1/teams":
			return jsonResponse(`{"data":{"teams":[
				{"team_id":"team_a","name":"Alpha"},
				{"team_id":"team_b","name":"Beta"}
			]},"code":200,"message":"ok"}`), nil
		case "/api/v1/user/usr_1/teams/team_a/agents":
			return jsonResponse(`{"data":{"agents":[
				{"team_id":"team_a","agent_id":"agent_self","agent_owner_user_id":"usr_1",
				 "agent":{"agent_id":"agent_self","name":"my-codex","online":true}},
				{"team_id":"team_a","agent_id":"agent_mate","agent_owner_user_id":"usr_mate",
				 "agent":{"agent_id":"agent_mate","name":"mate-claude","online":false}}
			]},"code":200,"message":"ok"}`), nil
		case "/api/v1/user/usr_1/teams/team_b/agents":
			return jsonResponse(`{"data":{"agents":[
				{"team_id":"team_b","agent_id":"agent_mate","agent_owner_user_id":"usr_mate",
				 "agent":{"agent_id":"agent_mate","name":"mate-claude","online":false}}
			]},"code":200,"message":"ok"}`), nil
		default:
			s.Failf("unexpected path", "path=%s", req.URL.Path)
			return nil, nil
		}
	})
}

func (s *TeamFacadeSuite) TestListAllAgentsExcludesSelfAndDedupes() {
	teamFacade := NewTeamFacade(s.teamGraphClient(), s.store)

	resp, err := teamFacade.ListAllAgents(s.ctx, &ListAllTeamAgentsRequest{})

	s.Require().NoError(err)
	s.Require().Len(resp.Agents, 1) // self excluded, mate de-duped across two teams
	agg := resp.Agents[0]
	s.Equal("agent_mate", agg.Agent.AgentID)
	s.Require().Len(agg.Teams, 2)
	s.Equal("team_a", agg.Teams[0].TeamID)
	s.Equal("team_b", agg.Teams[1].TeamID)
}

func (s *TeamFacadeSuite) TestListAllAgentsIncludeSelf() {
	teamFacade := NewTeamFacade(s.teamGraphClient(), s.store)

	resp, err := teamFacade.ListAllAgents(s.ctx, &ListAllTeamAgentsRequest{IncludeSelf: true})

	s.Require().NoError(err)
	s.Len(resp.Agents, 2)
}

func (s *TeamFacadeSuite) TestListAllAgentsFiltersByAgentID() {
	teamFacade := NewTeamFacade(s.teamGraphClient(), s.store)

	resp, err := teamFacade.ListAllAgents(s.ctx, &ListAllTeamAgentsRequest{
		IncludeSelf: true,
		AgentID:     "agent_self",
	})

	s.Require().NoError(err)
	s.Require().Len(resp.Agents, 1)
	s.Equal("agent_self", resp.Agents[0].Agent.AgentID)
}

func (s *TeamFacadeSuite) TestListAllAgentsOnlineOnly() {
	teamFacade := NewTeamFacade(s.teamGraphClient(), s.store)

	resp, err := teamFacade.ListAllAgents(s.ctx, &ListAllTeamAgentsRequest{
		IncludeSelf: true,
		OnlineOnly:  true,
	})

	s.Require().NoError(err)
	s.Require().Len(resp.Agents, 1)
	s.Equal("agent_self", resp.Agents[0].Agent.AgentID)
}

func (s *TeamFacadeSuite) TestListAllAgentsStrictOnTeamFailure() {
	client := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		switch req.URL.Path {
		case "/api/v1/user/usr_1/teams":
			return jsonResponse(`{"data":{"teams":[{"team_id":"team_a","name":"Alpha"}]},"code":200,"message":"ok"}`), nil
		default:
			return &http.Response{StatusCode: http.StatusInternalServerError, Body: http.NoBody}, nil
		}
	})
	teamFacade := NewTeamFacade(client, s.store)

	_, err := teamFacade.ListAllAgents(s.ctx, &ListAllTeamAgentsRequest{})

	s.Require().Error(err)
	s.Contains(err.Error(), "team_a")
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/facade/ -run 'TestTeamFacadeSuite/TestListAllAgents' -v`
Expected: FAIL — `undefined: ListAllTeamAgentsRequest`.

- [ ] **Step 3: Write minimal implementation**

Append to `internal/facade/team.go`:

```go
type ListAllTeamAgentsRequest struct {
	AgentID     string
	IncludeSelf bool
	OnlineOnly  bool
}

type TeamRef struct {
	TeamID string
	Name   string
}

type AggregatedTeamAgent struct {
	Agent *model.TeamAgent
	Teams []TeamRef
}

type ListAllTeamAgentsResponse struct {
	Agents []*AggregatedTeamAgent
	UserID string
}

func (f *TeamFacade) ListAllAgents(
	ctx context.Context,
	req *ListAllTeamAgentsRequest,
	opts ...func(*Option),
) (*ListAllTeamAgentsResponse, error) {
	_ = applyOptions(opts)
	if req == nil {
		req = &ListAllTeamAgentsRequest{}
	}
	teamsResp, err := f.ListTeams(ctx, &ListTeamsRequest{}, opts...)
	if err != nil {
		return nil, err
	}
	agentID := strings.TrimSpace(req.AgentID)
	index := make(map[string]*AggregatedTeamAgent)
	order := make([]string, 0)
	for _, team := range teamsResp.Teams {
		agentsResp, err := f.ListAgents(
			ctx,
			&ListTeamAgentsRequest{TeamID: team.TeamID},
			opts...,
		)
		if err != nil {
			return nil, fmt.Errorf("list agents for team %s: %w", team.TeamID, err)
		}
		for _, teamAgent := range agentsResp.Agents {
			if !req.IncludeSelf && teamAgent.AgentOwnerUserID == teamsResp.UserID {
				continue
			}
			if agentID != "" && teamAgent.AgentID != agentID {
				continue
			}
			if req.OnlineOnly && (teamAgent.Agent == nil || !teamAgent.Agent.Online) {
				continue
			}
			aggregated, ok := index[teamAgent.AgentID]
			if !ok {
				aggregated = &AggregatedTeamAgent{Agent: teamAgent}
				index[teamAgent.AgentID] = aggregated
				order = append(order, teamAgent.AgentID)
			}
			aggregated.Teams = append(aggregated.Teams, TeamRef{
				TeamID: team.TeamID,
				Name:   team.Name,
			})
		}
	}
	out := make([]*AggregatedTeamAgent, 0, len(order))
	for _, id := range order {
		out = append(out, index[id])
	}
	return &ListAllTeamAgentsResponse{Agents: out, UserID: teamsResp.UserID}, nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/facade/ -run 'TestTeamFacadeSuite' -v`
Expected: PASS (all team facade tests).

- [ ] **Step 5: Commit**

```bash
git add internal/facade/team.go internal/facade/team_test.go
git commit -m "$(printf 'Add TeamFacade.ListAllAgents aggregation\n\nCo-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>')"
```

---

### Task 5: CLI render helpers

**Files:**
- Modify: `cmd/paxl/main.go` (add render helpers near the other `render*` funcs, e.g. after `friendView` ~line 3328)
- Modify: `cmd/paxl/main_test.go`

- [ ] **Step 1: Write the failing tests**

Append to `cmd/paxl/main_test.go`:

```go
func TestRenderTeamListTable(t *testing.T) {
	var buf bytes.Buffer
	resp := &facade.ListTeamsResponse{
		UserID: "usr_1",
		Teams: []*model.TeamSummary{
			{Team: model.Team{TeamID: "team_1", Name: "Core", Status: "active"},
				MyRole: "owner", MemberCount: 2, AgentCount: 3},
		},
	}
	if err := renderTeamList(&buf, resp, "table"); err != nil {
		t.Fatalf("render team list: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "team_1") || !strings.Contains(out, "Core") ||
		!strings.Contains(out, "owner") {
		t.Errorf("unexpected table output:\n%s", out)
	}
}

func TestRenderTeamAgentsAggregatedShowsTeamsColumn(t *testing.T) {
	var buf bytes.Buffer
	resp := &facade.ListAllTeamAgentsResponse{
		UserID: "usr_1",
		Agents: []*facade.AggregatedTeamAgent{
			{
				Agent: &model.TeamAgent{
					AgentID:          "agent_mate",
					AgentOwnerUserID: "usr_mate",
					Agent:            &model.NodeAgent{AgentID: "agent_mate", Name: "mate-claude"},
				},
				Teams: []facade.TeamRef{{TeamID: "team_a", Name: "Alpha"}, {TeamID: "team_b", Name: "Beta"}},
			},
		},
	}
	if err := renderAggregatedTeamAgents(&buf, resp, "table"); err != nil {
		t.Fatalf("render aggregated team agents: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "mate-claude") || !strings.Contains(out, "Alpha") ||
		!strings.Contains(out, "Beta") {
		t.Errorf("unexpected aggregated output:\n%s", out)
	}
}

func TestTeamAgentDisplayFallsBackToAgentID(t *testing.T) {
	withName := &model.TeamAgent{AgentID: "a1", Agent: &model.NodeAgent{Name: "named"}}
	if got := teamAgentDisplay(withName); got != "named" {
		t.Errorf("display = %q, want named", got)
	}
	noName := &model.TeamAgent{AgentID: "a2"}
	if got := teamAgentDisplay(noName); got != "a2" {
		t.Errorf("display = %q, want a2", got)
	}
}
```

> If `bytes`, `strings`, `facade`, or `model` are not already imported in `main_test.go`, add them. (`strings` and `facade`/`model` are already used there; `bytes` may need adding.)

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./cmd/paxl/ -run 'TestRenderTeam|TestRenderAggregated|TestTeamAgentDisplay' -v`
Expected: FAIL — `undefined: renderTeamList` etc.

- [ ] **Step 3: Write minimal implementation**

Add to `cmd/paxl/main.go` (after `friendView`, around line 3328):

```go
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
		if _, err := fmt.Fprintln(writer, "AGENT\tAGENT_ID\tOWNER\tONLINE\tADDED"); err != nil {
			return fmt.Errorf("write team agents header: %w", err)
		}
		for _, teamAgent := range resp.Agents {
			if _, err := fmt.Fprintf(
				writer,
				"%s\t%s\t%s\t%s\t%s\n",
				teamAgentDisplay(teamAgent),
				teamAgent.AgentID,
				firstNonEmpty(teamAgent.AgentOwnerUserID, "-"),
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
		if _, err := fmt.Fprintln(writer, "AGENT\tAGENT_ID\tOWNER\tONLINE\tTEAMS"); err != nil {
			return fmt.Errorf("write aggregated agents header: %w", err)
		}
		for _, aggregated := range resp.Agents {
			if _, err := fmt.Fprintf(
				writer,
				"%s\t%s\t%s\t%s\t%s\n",
				teamAgentDisplay(aggregated.Agent),
				aggregated.Agent.AgentID,
				firstNonEmpty(aggregated.Agent.AgentOwnerUserID, "-"),
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

func encodeTeamAgentJSONL(stdout io.Writer, teamAgent *model.TeamAgent, refs []facade.TeamRef) error {
	teams := make([]map[string]string, 0, len(refs))
	for _, ref := range refs {
		teams = append(teams, map[string]string{"teamId": ref.TeamID, "name": ref.Name})
	}
	payload := map[string]any{
		"schemaVersion":    "paxl.teamAgent.v1",
		"agentId":          teamAgent.AgentID,
		"agentOwnerUserId": teamAgent.AgentOwnerUserID,
		"display":          teamAgentDisplay(teamAgent),
		"online":           teamAgent.Agent != nil && teamAgent.Agent.Online,
		"addedAt":          teamAgent.AddedAt,
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
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./cmd/paxl/ -run 'TestRenderTeam|TestRenderAggregated|TestTeamAgentDisplay' -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add cmd/paxl/main.go cmd/paxl/main_test.go
git commit -m "$(printf 'Add team CLI render helpers\n\nCo-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>')"
```

---

### Task 6: CLI command wiring (parse + actions + registration)

**Files:**
- Modify: `cmd/paxl/main.go` (add `newTeamCommand`, register it, add `teamList`/`teamGet`/`teamAgents` actions and `parse*` helpers)
- Modify: `cmd/paxl/main_test.go`

- [ ] **Step 1: Write the failing tests**

Append to `cmd/paxl/main_test.go`:

```go
func TestParseListAllTeamAgentsRejectsBothTeamIDAndAll(t *testing.T) {
	cmd := newTeamAgentsTestCommand(t, []string{"team_1", "--all"})
	if _, _, err := parseTeamAgentsRequest(cmd); err == nil {
		t.Fatal("expected error when both team id and --all are set")
	}
}

func TestParseListAllTeamAgentsRejectsNeither(t *testing.T) {
	cmd := newTeamAgentsTestCommand(t, []string{})
	if _, _, err := parseTeamAgentsRequest(cmd); err == nil {
		t.Fatal("expected error when neither team id nor --all is set")
	}
}

func TestParseTeamAgentsAllRequest(t *testing.T) {
	cmd := newTeamAgentsTestCommand(t, []string{"--all", "--include-self", "--agent", "agent_x"})
	single, all, err := parseTeamAgentsRequest(cmd)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if single != nil {
		t.Fatalf("expected no single-team request, got %+v", single)
	}
	if all == nil || !all.IncludeSelf || all.AgentID != "agent_x" {
		t.Fatalf("unexpected aggregate request: %+v", all)
	}
}

func newTeamAgentsTestCommand(t *testing.T, args []string) *cli.Command {
	t.Helper()
	cmd := &cli.Command{
		Name: "agents",
		Flags: []cli.Flag{
			&cli.BoolFlag{Name: "all"},
			&cli.BoolFlag{Name: "include-self"},
			&cli.BoolFlag{Name: "online"},
			&cli.StringFlag{Name: "agent"},
			&cli.StringFlag{Name: "format", Value: "table"},
		},
		Action: func(context.Context, *cli.Command) error { return nil },
	}
	if err := cmd.Run(context.Background(), append([]string{"agents"}, args...)); err != nil {
		t.Fatalf("run test command: %v", err)
	}
	return cmd
}
```

> Ensure `cli` ("github.com/urfave/cli/v3") and `context` are imported in `main_test.go` (they are already used elsewhere in that file).

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./cmd/paxl/ -run 'TestParseTeam|TestParseListAllTeam' -v`
Expected: FAIL — `undefined: parseTeamAgentsRequest`.

- [ ] **Step 3: Write minimal implementation**

Add to `cmd/paxl/main.go`. First, the command constructor (place near `newFriendCommand`, ~line 807):

```go
func newTeamCommand(stdout io.Writer) *cli.Command {
	formatFlag := func() cli.Flag {
		return &cli.StringFlag{Name: "format", Value: "table", Usage: "Output format: table or jsonl"}
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
				ArgsUsage: "[<team-id>]",
				Flags: []cli.Flag{
					&cli.BoolFlag{Name: "all", Usage: "Aggregate agents across all your teams"},
					&cli.BoolFlag{Name: "include-self", Usage: "Include agents you own (excluded by default)"},
					&cli.BoolFlag{Name: "online", Usage: "Only agents reporting online"},
					&cli.StringFlag{Name: "agent", Usage: "Filter to a single agent id (with --all)"},
					formatFlag(),
				},
				Action: func(ctx context.Context, cmd *cli.Command) error { return teamAgents(ctx, cmd, stdout) },
			},
		},
	}
}
```

Register it in the root command list (around line 91, next to `newFriendCommand(stdout)`):

```go
			newFriendCommand(stdout),
			newTeamCommand(stdout),
```

Add the parse helpers (near `parseListFriendsRequest`, ~line 2489):

```go
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
	if all {
		return nil, &facade.ListAllTeamAgentsRequest{
			AgentID:     strings.TrimSpace(cmd.String("agent")),
			IncludeSelf: cmd.Bool("include-self"),
			OnlineOnly:  cmd.Bool("online"),
		}, nil
	}
	return &facade.ListTeamAgentsRequest{TeamID: teamID}, nil, nil
}
```

Add the action helpers (near `friendList`, ~line 2011):

```go
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
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./cmd/paxl/ -run 'TestParseTeam|TestParseListAllTeam' -v`
Expected: PASS.

- [ ] **Step 5: Build and run the full package test**

Run: `go build ./... && go test ./cmd/paxl/ ./internal/facade/ ./internal/model/`
Expected: build succeeds; all tests PASS.

- [ ] **Step 6: Commit**

```bash
git add cmd/paxl/main.go cmd/paxl/main_test.go
git commit -m "$(printf 'Wire paxl team list/get/agents commands\n\nCo-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>')"
```

---

### Task 7: Lint, full test gate, and docs

**Files:**
- Modify: `README.md` (Command Reference section)
- Modify: `doc/ARCHITECTURE.md`

- [ ] **Step 1: Run format and lint**

Run: `make format && make lint`
Expected: no formatting changes left unstaged; lint passes. Fix any reported issues (e.g. `golines`, cyclomatic complexity) before continuing.

- [ ] **Step 2: Run the coverage gate**

Run: `make test-cover`
Expected: PASS, total coverage ≥ 80%. If team code dips coverage, add focused facade/render tests until the gate passes.

- [ ] **Step 3: Document the commands in README.md**

Add a new subsection under the "Command Reference" section of `README.md`:

```markdown
### List Teams and Team Agents (Read-Only)

`paxl team` reads the team graph from PAX Manager so the local node knows which
teammate agents it could deliver to. These commands are read-only.

List the teams you belong to:

```sh
paxl team list
```

Show one team:

```sh
paxl team get <team-id>
```

List a team's agents:

```sh
paxl team agents <team-id>
```

Aggregate delivery-candidate agents across all your teams. Agents you own are
excluded by default; each agent shows which teams it belongs to:

```sh
paxl team agents --all
paxl team agents --all --include-self
paxl team agents --all --online
paxl team agents --all --agent <agent-id>
```

All `team` commands support `--format table|jsonl`.
```

- [ ] **Step 4: Document the data flow in doc/ARCHITECTURE.md**

Add a short paragraph to `doc/ARCHITECTURE.md` near the friend/envelope description noting that `TeamFacade` (`internal/facade/team.go`) performs read-only GETs against `/api/v1/user/:user_id/teams[...]`, never caches in SQLite, and that `ListAllAgents` aggregates per-team agents client-side (de-duped by `agent_id`, self excluded by default) to feed future delivery routing.

- [ ] **Step 5: Commit**

```bash
git add README.md doc/ARCHITECTURE.md
git commit -m "$(printf 'Document paxl team read-only commands\n\nCo-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>')"
```

---

## Self-Review Notes

- **Spec coverage:** `team list` (Task 6 + 2), `team get` (Task 6 + 3), `team agents <id>` (Task 6 + 3), `team agents --all` aggregation with default self-exclusion / `--include-self` / `--online` / `--agent` reverse lookup (Task 6 + 4), strict aggregation error (Task 4), table+jsonl rendering (Task 5), live reads / no cache (facade design, Tasks 2-4), docs (Task 7). No members/invites/mutations — matches spec out-of-scope.
- **Type consistency:** `ListTeamsRequest/Response`, `GetTeamRequest/Response`, `ListTeamAgentsRequest/Response`, `ListAllTeamAgentsRequest`, `AggregatedTeamAgent`, `TeamRef`, `ListAllTeamAgentsResponse` are defined in Tasks 2-4 and used identically in Tasks 5-6. Render funcs `renderTeamList`/`renderTeamDetail`/`renderTeamAgents`/`renderAggregatedTeamAgents`, helpers `teamAgentDisplay`/`teamAgentOnline`/`teamRefsLabel`/`encodeTeamAgentJSONL`, and CLI `parseTeamAgentsRequest` returning `(*ListTeamAgentsRequest, *ListAllTeamAgentsRequest, error)` are consistent across tasks.
- **Reused existing helpers:** `validateFormat`, `firstNonEmpty`, `closeStore`, `store.Open`, `applyOptions`, `managerEnvelope`, `requireCredential`, `roundTripFunc`, `jsonResponse` — all already exist in the codebase.
```
