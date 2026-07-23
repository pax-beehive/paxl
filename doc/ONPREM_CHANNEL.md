# Team Memory On-Prem Envelope Channel

Paxl can use a single-Team Team Memory installation as a credential-bound
Agent-to-Agent envelope transport. Manager remains the default when
`--channel` is omitted.

## Connect and trust

Create an Agent and one-time enrollment in the Team Memory Portal. Consume the
token once without placing it in shell history:

```sh
read -rs PAXL_ENROLLMENT_TOKEN
export PAXL_ENROLLMENT_TOKEN
paxl channel connect onprem --profile onprem
unset PAXL_ENROLLMENT_TOKEN
```

New self-describing enrollment tokens carry the deployment origin and do not
need `--url`. Legacy two-part tokens remain supported and require
`--url https://memory.internal`. An explicit `--url` always wins. If an embedded
origin differs from the existing profile origin, paxl stops before exchange and
requires rerunning with that origin as an explicit `--url`; this prevents a
modified token from silently sending its one-time secret to another host.

Connect exchanges the enrollment exactly once, stores the returned Agent key,
then verifies `/v1/agent-identity`. If identity verification fails, the token is
already consumed and paxl reports that the saved credential must be checked;
it never replays the enrollment automatically. Normal output and JSON include
only the profile, URL, Agent, user, and permissions, never the token or key.

HTTPS uses system trust by default. For an internal workstation CA:

```sh
paxl channel connect onprem --ca-file /etc/team-memory/ca.pem
```

The CA is added to system roots for that profile. There is no
`insecure-skip-verify` setting. Plain HTTP is accepted only for loopback
origins such as `http://127.0.0.1:8080`, which keeps local acceptance tests
possible without allowing a bearer credential over a remote cleartext link.
The profile name `manager` is reserved for the existing PAX Manager channel.

## Directory and delivery

```sh
paxl channel agents list --channel onprem --query receiver --limit 20
paxl channel agents get receiver-agent --channel onprem
paxl capsule send <capsule-id> --channel onprem --to-agent-id receiver-agent \
  --match project --project paxl --agent codex
paxl inbox list --channel onprem --status pending
paxl inbox accept <envelope-id> --channel onprem
paxl inbox archive <envelope-id> --channel onprem
paxl outbox list --channel onprem --status archived
```

On-prem sends always use the
`paxl.envelope_payload.knowledge_capsule.v2` payload. Paxl validates the 128 KiB
payload limit, 1000-character message limit, route, and target Agent before the
request. A logical send has one stable idempotency key across network and 5xx
retries. HTTP 409 is returned as a conflict instead of silently generating a
new key.

Receive processing writes the remote capsule and any route injection locally
before calling remote accept. The local source key includes the profile
installation namespace. This makes retries after an ambiguous accept response
idempotent and prevents equal envelope ids from different installations from
colliding. A SQLite receipt transaction makes the capsule and route a single
atomic materialization even when multiple paxl processes receive the same
envelope concurrently.

## Permissions and recovery

- HTTP 401 means the Agent credential may be expired or revoked; reconnect with
  a newly issued enrollment.
- HTTP 403 identifies the required `channel_send` or `channel_receive`
  permission and also calls out a suspended Agent or Membership.
- A channel error never falls back to manager. User-prompt auto-receive logs the
  failed profile and continues polling other enabled profiles and local routes.

## Upgrade and migration

Existing manager users do not migrate data or change commands. The manager
credential schema, friend recipient model, default flags, outputs, and legacy
`remote_envelope:<id>` source keys remain valid. On-prem profiles live in a
separate table and can coexist with manager credentials. More named on-prem
profiles can be stored even though `onprem` is the default profile name.

The current Team Memory envelope-list response does not expose `next_cursor`.
Paxl sends cursor values as opaque tokens and preserves a returned cursor when
the server adds that field, but current deployments cannot advance envelope
pagination from a response. Agent Directory pagination already returns
`next_cursor`. Because the sender cannot use the receiver-only direct-get
endpoint, `outbox get` searches the first 100 sent envelopes and reports this
contract limitation explicitly when the id is outside that window.

## Real public-HTTP E2E

The E2E uses the sibling Team Memory repository's isolated Compose topology. It
bootstraps through public HTTP, creates two Agents and enrollments, connects two
separate paxl SQLite stores, performs directory lookup, send, inbox/get, local
materialization and route injection, repeat accept, archive, and sender outbox.
It runs the built `paxl` CLI for all channel operations. Only human bootstrap
and enrollment creation use a small public-HTTP test helper; the test never
queries the Team Memory database or imports Team Memory internals.

```sh
make onprem-channel-e2e
```

Set `TEAM_MEMORY_REPO=/absolute/path/to/team-memory` when the repositories are
not siblings. The script refuses to reuse an existing Compose project and
removes its temporary containers, network, and volume with `down -v`.
`PAXL_ONPREM_E2E_CACHED_PROJECT=<compose-project>` may be used in an offline
environment to retag already-built Team Memory E2E service images; the default
always builds the current sibling checkout.
