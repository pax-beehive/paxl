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
