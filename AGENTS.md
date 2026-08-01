# AGENTS.md

## Cursor Cloud specific instructions

Cellar is a Go control plane for isolated sandboxes (Linux and macOS hosts). The repo
builds five Go binaries (`cellard`, `cellar`, `cellar-gateway`, `cellar-agent`,
`cellar-egress-gateway`), a TypeScript SDK (`sdk/node`), and a Fumadocs docs site
(`docs/`). Standard commands live in the `Makefile` (`make build`, `make test`,
`make install`) and in the `scripts` of `sdk/node/package.json` and `docs/package.json`
— refer to those rather than duplicating them here.

### Toolchain (already present in the VM snapshot)
- Go 1.26.x is selected automatically via `GOTOOLCHAIN=auto` (declared in `go.mod`).
- Node 22 and `bun` (installed at `~/.bun/bin`, added to `PATH` via `~/.bashrc`) — the
  Node SDK and docs site use `bun`, not npm/pnpm.
- Docker Engine is installed. Sandboxes use Docker’s default OCI runtime (`runc`) with
  hardened host config (`CapDrop: ALL`, `no-new-privileges`, pids limit). gVisor/`runsc`
  is **not** used by the current sandbox path.

### Running the services (non-obvious caveats)
- **Docker is not managed by systemd here.** Start it manually before any sandbox work:
  `sudo dockerd` (leave it running; it logs to your terminal). `make`/`go test` do not
  need it, but `cellar sandbox …` does.
- **Linux bring-up (this Cloud VM):** run as root (`sudo`). Defaults are
  `/var/run/cellar/cellar.sock` and `/var/lib/cellar`. `cellard` resolves `cellar-agent`
  next to its own executable (and stages a copy under the data dir before bind-mounting
  into containers), so run the freshly built `bin/cellard` beside `bin/cellar-agent`.
  Typical bring-up: `sudo bin/cellard` → `sudo bin/cellar init --advertise-addr
  127.0.0.1:17946` → `sudo bin/cellar status` (expect `is_leader: true`) →
  `sudo bin/cellar api-key create --name demo` → optionally `sudo bin/cellar-gateway
  --listen 127.0.0.1:8080 --data-dir /var/lib/cellar` (health at `/healthz`, `/readyz`).
- **macOS / Docker Desktop:** defaults are `~/.cellar` (data + socket). Do **not** rely
  on bind-mounting `/usr/local/bin` or Homebrew prefixes — Docker Desktop only shares
  paths under the user home (and similar). `install.sh` and `make install` stage
  `cellar-agent` to `~/.cellar/cellar-agent`; at runtime `StageAgentBinary` also copies
  the resolved agent into the data dir before `ContainerCreate`. Prefer running
  `cellard` / `cellar` without sudo so the data dir stays under `/Users/...` (when
  sudo is used, defaults follow `SUDO_USER`’s home). Use plain `docker` (no sudo) with
  Docker Desktop. Networked sandboxes still need the `cellar/egress-gateway` image
  (release tarball via the curl installer / `docker load`, or `make egress-gateway-image`
  from a source checkout).

### Install
- `curl … | sh` (`install.sh`) and `make install` support **Linux and Darwin**.
- Linux: binaries → `$PREFIX/bin` (default `/usr/local`), plus systemd units + sysusers from `contrib/`.
- Darwin: host tools → `$PREFIX/bin` (default `~/.local/bin`, no sudo); LaunchAgents under
  `~/Library/LaunchAgents`; Linux `cellar-agent` is also staged under
  `$CELLAR_DATA_DIR/cellar-agent` (default `~/.cellar`). Override with `CELLAR_PREFIX`
  / `CELLAR_DATA_DIR`.

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
