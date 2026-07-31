# AGENTS.md

## Cursor Cloud specific instructions

Cellar is a Linux-only Go control plane for isolated sandboxes. The repo builds four
Go binaries (`cellard`, `cellar`, `cellar-gateway`, `cellar-agent`), a TypeScript SDK
(`sdk/node`), and a Fumadocs docs site (`docs/`). Standard commands live in the
`Makefile` (`make build`, `make test`) and in the `scripts` of `sdk/node/package.json`
and `docs/package.json` — refer to those rather than duplicating them here.

### Toolchain (already present in the VM snapshot)
- Go 1.26.x is selected automatically via `GOTOOLCHAIN=auto` (declared in `go.mod`).
- Node 22 and `bun` (installed at `~/.bun/bin`, added to `PATH` via `~/.bashrc`) — the
  Node SDK and docs site use `bun`, not npm/pnpm.
- Docker Engine + gVisor `runsc` are installed. `runsc` is registered in
  `/etc/docker/daemon.json` and is required for the sandbox lifecycle.

### Running the services (non-obvious caveats)
- **Docker is not managed by systemd here.** Start it manually before any sandbox work:
  `sudo dockerd` (leave it running; it logs to your terminal). `make`/`go test` do not
  need it, but `cellar sandbox …` does.
- **gVisor needs two runtime args in this nested VM.** `/etc/docker/daemon.json`
  registers `runsc` with `--host-uds=create` (so `cellar-agent` can bind its control
  socket) **and** `--ignore-cgroups`. Without `--ignore-cgroups`, `runsc` containers
  fail at startup with `configuring cgroup: write .../cgroup.subtree_control: operation
  not supported`. After editing that file, reload with `sudo kill -HUP $(pgrep -x dockerd)`.
- **Run the daemon/CLI/gateway as root** (`sudo`). The control socket is
  `/var/run/cellar/cellar.sock` (owned `root:root`, mode 0660) and `cellard` needs the
  Docker socket. `cellard` resolves the `cellar-agent` binary next to its own executable,
  so run the freshly built `bin/cellard` (which sits beside `bin/cellar-agent`).
- Typical bring-up: `sudo bin/cellard` → `sudo bin/cellar init --advertise-addr
  127.0.0.1:17946` → `sudo bin/cellar status` (expect `is_leader: true`) →
  `sudo bin/cellar api-key create --name demo` → optionally `sudo bin/cellar-gateway
  --listen 127.0.0.1:8080 --data-dir /var/lib/cellar` (health at `/healthz`, `/readyz`).
  Sandboxes really boot under gVisor — `sandbox exec … -- uname -a` reports a
  `*-gvisor` kernel.

### Tests
- `make test` (`go test ./...`) passes except one **pre-existing** failure:
  `internal/daemon` `TestSandboxAPIClientViaGatewayFailover`. It fails because the JSON
  API encodes `int64` fields (e.g. `updatedAtUnixNano`) as strings (standard protojson
  behavior) while that test's client decodes into `int64`. It is unrelated to
  environment setup — do not treat it as an env regression.
- SDK checks (from `sdk/node`): `bun run test`, `bun run typecheck`, `bun run build`.
  Note `bun run lint` runs `oxlint --fix` and will modify files; use `bunx oxlint`
  (no `--fix`) for a read-only check.
- Docs (from `docs/`): `bun run types:check`, `bun run build`, `bun run dev` (serves on
  `:3000`).
