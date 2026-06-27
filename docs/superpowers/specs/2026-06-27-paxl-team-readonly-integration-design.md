# paxl Team Read-Only Integration — Design

Date: 2026-06-27
Status: Approved (design)
Repository: paxl
Branch: feat/team-readonly-integration

## Goal

Give paxl read-only visibility into the pax-manager Team graph so the local node
knows which agents it could deliver to. paxl does **not** manage teams in this
iteration. The CLI only reads:

1. The teams the current user belongs to.
2. The team(s) a given agent belongs to.
3. All (teammate) agents across the user's teams — the delivery candidate set.

Delivery routing that consumes this discovery is explicitly out of scope here;
this iteration only exposes the reads.

## Source of Truth

The pax-manager Team API contract comes from knowledge capsule
`kcap_b38f0f6a992ccaaf` (keyword `team接入`), which reflects pax-manager
`origin/main` after PR #55 (merge `afe68a5965d62dc46692d8ea5b963100f4018fa6`).

Note: the local pax-manager working tree may be behind `origin/main` and may not
contain the team files yet. paxl integration depends only on the REST contract,
not on local manager source.

### Routes consumed (all GET, under `/api/v1/user/:user_id`)

- `GET /teams` → `{ "teams": TeamSummary[] }`
- `GET /teams/:team_id` → `{ "team": Team }`
- `GET /teams/:team_id/members` → `{ "members": TeamMember[] }`
- `GET /teams/:team_id/agents` → `{ "agents": TeamAgent[] }` (`TeamAgent.agent`
  included when available)

No mutation routes (create/invite/accept/decline/leave/remove/agent add/remove)
are called in this iteration.

### Relevant domain JSON (subset used)

- `Team`: `team_id, owner_user_id, name, status, created_at, archived_at`
- `TeamSummary`: all `Team` fields plus `my_role, member_count, agent_count`
- `TeamMember`: `team_id, user_id, email, role, status, invited_by_user_id,
  joined_at, removed_at, removed_by_user_id`
- `TeamAgent`: `team_id, agent_id, agent_owner_user_id, added_by_user_id,
  added_at, removed_at, removed_by_user_id, agent`

Enums: role = `owner|operator|member`; team status = `active|archived`; member
status = `active|removed`.

Naming rule (from the contract): node/agent have no alias. Bind by stable IDs
(`node_id`, `agent_id`). For display use `name → hostname → agent_id` fallback.
`name` is not user-editable yet; do not rely on name uniqueness.

## CLI Surface (Approach C — resource-oriented + aggregation flag)

All commands are read-only and support `--format table|jsonl` (matching the
existing `paxl friend` / `paxl node` convention).

| Command | Route | Purpose |
|---|---|---|
| `paxl team list` | `GET /teams` | Teams the user belongs to (`TeamSummary`) |
| `paxl team get <team_id>` | `GET /teams/:id` | Single team detail |
| `paxl team members <team_id>` | `GET /teams/:id/members` | Members + roles (permission visibility) |
| `paxl team agents [<team_id>] [--all] [--agent <id>] [--exclude-self] [--online]` | `GET /teams/:id/agents` (+ client-side aggregation when `--all`) | Delivery-candidate agents; reverse-lookup an agent's teams |

`team agents` behavior:

- `team agents <team_id>` — agents of one team.
- `team agents --all` — iterate the user's teams and aggregate, de-duped by
  `agent_id`, with a `TEAMS` column listing each agent's team membership (covers
  read #3: all agents across the user's teams).
- `team agents --all --agent <id>` — filter the aggregate to one agent and show
  its `TEAMS` (covers read #2: which teams an agent is in).
- `--exclude-self` — drop agents whose `agent_owner_user_id` equals the current
  user (i.e. show only teammates' agents — "其他 agents").
- `--online` — keep only agents whose embedded agent reports `online`.

`<team_id>` and `--all` are mutually exclusive; require exactly one for
`team agents`. Display uses `name → hostname → agent_id` fallback. Since
`TeamAgent.agent` mirrors a registered node agent (no hostname field on the agent
itself), `hostname` fallback applies only when present; otherwise fall back to
`agent_id`.

## Architecture

Follows the established `FriendFacade` pattern exactly. Manager data is fetched
live on each call; nothing is cached in local SQLite.

### Model — `internal/model/team.go`

New types: `Team`, `TeamSummary`, `TeamMember`, `TeamAgent`. `TeamAgent` embeds
the registered agent via `Agent *NodeAgent` to reuse existing agent fields
(`agent_id, node_id, owner_user_id, name, agent_type, status, online`). Roles and
statuses are string enums with an unknown-first value, per Go style rules.

Out of scope: `TeamInvite` and any write-oriented types.

### Facade — `internal/facade/team.go`

New `TeamFacade` mirroring `FriendFacade`:

```go
func NewTeamFacade(client AuthHTTPClient, sessionStore *store.Store) *TeamFacade
```

Methods (all read-only, GET via `auth.managerJSON`, request/response structs,
`opts ...func(*Option)`):

- `ListTeams(ctx, req) (*ListTeamsResponse, error)` → `TeamSummary[]`
- `GetTeam(ctx, req) (*GetTeamResponse, error)` → `Team`
- `ListMembers(ctx, req) (*ListTeamMembersResponse, error)` → `TeamMember[]`
- `ListAgents(ctx, req) (*ListTeamAgentsResponse, error)` → `TeamAgent[]` for one team
- `ListAllAgents(ctx, req) (*ListAllTeamAgentsResponse, error)` → aggregation:
  calls `ListTeams`, then `ListAgents` per team, merges by `agent_id` collecting
  the set of teams each agent belongs to. Applies `--agent`, `--exclude-self`,
  `--online` filters. Carries `UserID` for self-detection.

Path helper `userTeamPath(userID, teamID, sub string)` analogous to
`userFriendPath`.

### CLI — `cmd/paxl/main.go`

Add `newTeamCommand(stdout)` to the root command list (sibling of
`newFriendCommand`). Each subcommand action calls a `teamXxx(ctx, cmd, stdout)`
helper that opens the store, builds `facade.NewTeamFacade(nil, store)`, parses a
request struct, calls the facade, and renders. Add `parseXxx` and `renderXxx`
helpers mirroring the friend equivalents (table + jsonl encoders).

## Data Flow

```
paxl team agents --all --exclude-self
  → teamAgents (cmd) → parseListAllTeamAgentsRequest
  → TeamFacade.ListAllAgents
      → ListTeams (GET /teams)
      → for each team: ListAgents (GET /teams/:id/agents)
      → merge by agent_id, collect teams, filter exclude-self/online/agent
  → renderTeamAgents (table|jsonl)
```

## Error Handling

- Reuse `auth.requireCredential`; surface `not logged in` when no credential.
- `managerJSON` already maps non-2xx to `manager returned HTTP <code>`; wrap with
  command context using `%w`.
- `team agents` with neither `<team_id>` nor `--all`, or with both, is a
  validation error returned before any manager call.
- Aggregation is best-effort-strict: if any per-team agent fetch fails, return the
  error (do not silently drop a team), wrapped with the failing `team_id`.

## Testing (TDD)

Behavior tests through public interfaces, table-driven where it fits, using a
fake `AuthHTTPClient` like the existing friend/auth tests:

- Facade: `ListTeams`/`GetTeam`/`ListMembers`/`ListAgents` decode envelopes and
  build correct paths; `ListAllAgents` aggregates and de-dupes by `agent_id`,
  collects multiple teams per agent, and applies `--exclude-self`/`--online`/
  `--agent` filters; `not logged in` propagates.
- CLI: `team agents` rejects both/neither `<team_id>`/`--all`; table and jsonl
  renderers; `name → hostname → agent_id` display fallback.

Coverage gate remains 80% (`make test-cover`).

## Out of Scope (this iteration)

- Any team mutation: create, invite, accept/decline, leave, remove, agent
  add/remove.
- `TeamInvite` model and invite read routes (`/team-invites`).
- `capsule send --to #team` group delivery.
- Local SQLite caching of the team graph.
- Wiring discovery into the actual delivery/routing path.

## Documentation

After implementation, update paxl docs (`README.md` command reference,
`doc/ARCHITECTURE.md`) to describe the read-only `paxl team` commands.
