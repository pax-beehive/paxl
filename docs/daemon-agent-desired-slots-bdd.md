# Daemon agent desired slots behavior

## Feature: Update an ACP worker pool size from paxl

`paxl daemon agent update` is the user-facing control surface for a local
daemon agent connection. Pool size changes must use the existing paxd update
endpoint and must not require direct access to the daemon database.

### Scenario: Set the desired ACP slot count

Given a local daemon agent connection named `conn_codex`

When the user runs:

```text
paxl daemon agent update conn_codex --desired-slots 2
```

Then paxl sends `desired_slots: 2` in the agent connection update request

And no unrelated optional update field is populated

And the normal daemon command acknowledgement is rendered.

### Scenario: Preserve the existing slot count when the flag is omitted

Given a local daemon agent connection with an existing desired slot count

When the user updates another field without `--desired-slots`

Then paxl omits `desired_slots` from the update request

And paxd preserves the existing desired slot count.

### Scenario: Reject a slot count outside the daemon contract

Given paxd accepts desired slot counts from 1 through 16

When the user supplies a value below 1 or above 16

Then paxl rejects the command before calling the daemon facade

And the error identifies `--desired-slots` and the accepted range.
