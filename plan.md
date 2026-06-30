# paxl-owned paxd installation and paxctl retirement plan

## Assumption

Users install `paxl` first. When they need the fleet daemon, they install and
configure `paxd` through `paxl`. The standalone `paxctl` binary is retired and
is not published in future releases.

## Target shape

- `paxl` is the user-facing CLI.
- `paxd` remains the daemon runtime and owns the background service, local API,
  cloud connection, and agent tunnel loops.
- `paxl` manages `paxd` through the local daemon API and through lifecycle
  commands that install, set up, and control the `paxd` binary.
- `paxctl` behavior is merged into `paxl daemon ...` and `paxl` native commands.

## User experience

Default install stays focused on `paxl`:

```sh
curl -fsSL https://api.paxtech.net/api/v1/public/paxl/install.sh | bash
paxl version
```

Local agent hook setup remains the default `setup` behavior:

```sh
paxl setup
```

Daemon setup is opt-in from the same command:

```sh
paxl setup --with-daemon
```

`paxl setup --with-daemon` should:

1. Install local agent integrations as `paxl setup` already does.
2. Check whether a usable `paxd` binary exists.
3. Install `paxd` if missing or if the user requests an upgrade.
4. Run the daemon pairing/setup flow.
5. Start or restart the background service.
6. Verify the local daemon API is reachable.
7. Print the final daemon status and next useful commands.

Direct daemon lifecycle commands should also exist for explicit control:

```sh
paxl daemon install
paxl daemon update
paxl daemon setup
paxl daemon service status
paxl daemon service start
paxl daemon service stop
paxl daemon status
```

Daemon control commands replace `paxctl`:

```sh
paxl daemon remote list
paxl daemon remote create staging --cloud-url https://api.example.com
paxl daemon agent list
paxl daemon agent create --remote default --harness codex --name work
paxl daemon harness discover --probe codex claude
```

## Command mapping

| Old command | New command |
| --- | --- |
| `paxctl status` | `paxl daemon status` |
| `paxctl remotes list` | `paxl daemon remote list` |
| `paxctl remotes login <remote>` | `paxl daemon setup --remote <remote>` |
| `paxctl agents list` | `paxl daemon agent list` |
| `paxctl agents create ...` | `paxl daemon agent create ...` |
| `paxctl agents restart <id>` | `paxl daemon agent restart <id>` |
| `paxctl agents stop <id>` | `paxl daemon agent stop <id>` |
| `paxctl agents remove <id>` | `paxl daemon agent remove <id>` |
| `paxctl harnesses list` | `paxl daemon harness list` |
| `paxctl harnesses discover ...` | `paxl daemon harness discover ...` |
| `paxctl sessions list` | `paxl session list` or `paxl daemon local session list` |
| `paxctl sessions get <id>` | `paxl session get <id>` |
| `paxctl capsules ...` | `paxl capsule ...` |

## Repository responsibilities

### paxl

- Own the user-facing installation path.
- Add `setup --with-daemon`.
- Add daemon lifecycle facades for install, setup, service control, and status.
- Keep daemon management under `paxl daemon ...`.
- Download `paxd` artifacts through the public paxd resolver.
- Verify downloaded artifacts with sha256 before installation.
- Do not import packages from the `paxd` repository.
- Treat the paxd local API JSON contract as the integration boundary.

### paxd

- Keep publishing the `paxd` binary.
- Stop publishing `paxctl` artifacts.
- Remove `paxctl` from release scripts and installer behavior.
- Keep the local API stable for `paxl`.
- Keep service install/start/stop/status behavior owned by `paxd`.
- Update documentation to say paxd is installed and managed through `paxl`.

## Implementation plan

### Phase 1: paxl command surface

- Add `--with-daemon` to `paxl setup`.
- Add `paxl daemon install`.
- Add `paxl daemon update`.
- Add `paxl daemon setup`.
- Add `paxl daemon service status|start|stop|restart`.
- Reuse the existing `paxl daemon status|remote|agent|harness|local` command
  surface for paxctl replacement behavior.
- Update daemon API guidance to prefer `paxl setup --with-daemon` instead of
  `paxd setup` as the first recovery hint.

### Phase 2: paxd artifact installation from paxl

- Implement platform detection in `paxl`.
- Resolve `paxd` artifacts through `/api/v1/public/paxd/download`.
- Download to a temporary file.
- Verify sha256.
- Install to the chosen bin directory.
- Support explicit install dir and version/tag flags.
- Support explicit daemon self-update through `paxl daemon update`.
- Keep sudo escalation in the shell installer path where possible; inside Go,
  prefer clear errors that tell the user which command needs privilege.

### Phase 3: setup orchestration

- `paxl setup --with-daemon` should run normal hook setup first.
- If daemon setup is requested, call the new daemon setup facade.
- Daemon setup should call the installed `paxd setup` command rather than
  reimplementing pairing internals in `paxl`.
- After setup, verify `~/.paxd/paxd.sock` by calling the local status API.
- Render a compact success summary.

### Phase 4: paxctl retirement in paxd

- Remove `paxctl` from `scripts/release_paxd.sh` default products.
- Remove paxctl artifact upload/publish steps.
- Update the paxd installer to install only `paxd`.
- Remove paxctl checks from installer setup detection.
- Update paxd README examples from `paxctl ...` to `paxl daemon ...`.
- Optionally keep `cmd/paxctl` source for one short transition, but do not
  publish it.

### Phase 5: documentation and migration

- Update paxl README install and first-run sections.
- Add a migration note for old `paxctl` users.
- Update public install endpoint docs so users install `paxl` first.
- Update release notes to state that `paxctl` is retired.

## Testing plan

- Use testify assertions for Go tests.
- Add BDD-style tests for `paxl setup --with-daemon`.
- Mock artifact resolution, download, checksum, install path selection, and
  `paxd setup` execution.
- Add command tests for daemon lifecycle commands.
- Add release script tests or shellcheck-style coverage for removing `paxctl`
  from paxd release outputs.
- Maintain at least 80% unit coverage for new backend code.

## Open decisions

- Whether `paxl setup --with-daemon` should imply daemon install upgrades when
  `paxd` already exists but is old.
- Whether `paxl daemon setup` should expose `--cloud-url`, `--remote`, and
  Cloudflare Access options directly or pass unknown args through to `paxd`.
- Whether to keep `cmd/paxctl` source temporarily after stopping publication.
