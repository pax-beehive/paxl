# DeepSeek Harness through paxl daemon

Install DSH on the same computer that runs paxd, then discover and create it:

```sh
paxl daemon harness install dsh --dry-run
paxl daemon harness install dsh
paxl daemon harness discover dsh
paxl daemon agent create --harness dsh --name deepseek
paxl daemon agent list
```

The installer explicitly runs `npm install -g @deepseek-ai/dsh@latest` locally
as the invoking user. It does not install on a remote node or restart paxd.
Node 22.19+ on the 22.x line or Node 24+ is required. Configure
`DEEPSEEK_API_KEY` in the paxd service environment (or DSH's own credential
configuration), and ensure the daemon's PATH can resolve both DSH and Node.

Both paxl and paxd must include the DSH integration: paxl owns the installation
command; paxd owns the `dsh` discovery entry and runtime. Discovery and creation
reuse existing local-control APIs. An available executable is not proof of
working model credentials. An unknown DSH harness usually means paxd still
needs updating; a missing one means its process cannot find `dsh`.

Creation resolves `dsh --profile acp` from the daemon inventory, registers a
cloud agent of type `dsh`, and persists the connection. Supply `--remote <id>`
when multiple remotes are configured. An explicit `--command` remains available
for nonstandard installations.

DSH accepts per-session stdio/HTTP MCP declarations on new/resume, but requires
absolute stdio commands and explicit credential env values. It does not expose
slash-command catalogs or transcript replay over ACP. Existing paxd MCP injection
and cold-route recovery remain shared with other compatible harnesses.

## Local session titles and history

`paxl session list --agent dsh` reads local DSH logs directly. Titles use the
latest persisted `session/title`, then the first user prompt, then the workspace
name or session ID. No model request or credential loading is needed.
`paxl session get dsh:<native-id>` reads the settled transcript, including tool
calls/results, model provenance and usage. Compaction replacements and streaming
chunks are not duplicated as messages; original event JSON is retained.

The default log root is `~/.dsh/sessions`. `DSH_HOME` changes the DSH home;
`PAXL_DSH_SESSIONS_DIR` overrides the session directory directly. Formats v0,
v1 and v2, plain JSONL and concatenated Zstandard frames are supported. The
newest log generation is authoritative. Incomplete trailing writes are ignored;
committed corruption and unknown formats produce errors. Logs are never migrated
or modified. Decoded logs are limited to 256 MiB and individual lines to 16 MiB.

This adapter supports local listing and reading, not native prompt delivery,
new-session creation or resume. Those runtime operations remain available through
`paxl daemon` and paxd's ACP integration. Update paxl on the daemon host to enable
titles/history reporting; paxd forwards the connection's DSH home overrides and
preserves workspace roots. ACP fallback alone does not provide titles/history.
