# Codex Subagent Session Listing

## Feature: Keep the default Codex session list user-facing

Codex writes separate rollout files for internal subagents such as the
Auto-review guardian. These rollouts are useful for diagnostics, but they are
not top-level user sessions and should not appear in the default session list.

### Scenario: Hide Codex subagent rollouts by default

Given a Codex user thread and a rollout whose `thread_source` is `subagent`

When the user runs:

```text
paxl session list --agent codex
```

Then paxl lists the user thread

And paxl does not list the subagent rollout

### Scenario: Include Codex subagent rollouts for diagnostics

Given a Codex user thread and a rollout whose `thread_source` is `subagent`

When the user runs:

```text
paxl session list --agent codex --include-subagents
```

Then paxl lists both the user thread and the subagent rollout

And the subagent keeps its native rollout ID

### Scenario: Preserve older Codex rollouts

Given a Codex rollout whose metadata does not contain `thread_source`

When the user lists Codex sessions without `--include-subagents`

Then paxl continues to list the rollout
