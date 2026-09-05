# AGENTS.md

## Cursor Cloud specific instructions

Cellar is a Go control plane for isolated microsandbox VMs (Linux and macOS hosts).
The repo builds three Go binaries (`cellard`, `cellar`, `cellar-gateway`) and a
Fumadocs docs site (`docs/`). Clients use the **official microsandbox SDKs** in
cloud mode against `cellar-gateway` — there is no first-party Cellar SDK in this
repo. Standard commands live in the `Makefile` (`make build`, `make test`,
`make install`) and in `docs/package.json` — refer to those rather than
duplicating them here.

### Toolchain (already present in the VM snapshot)
- Go 1.26.x is selected automatically via `GOTOOLCHAIN=auto` (declared in `go.mod`).
- Node 22 and `bun` (installed at `~/.bun/bin`, added to `PATH` via `~/.bashrc`) —
  the docs site uses `bun`, not npm/pnpm.
- Sandboxes run as **microsandbox** VMs over **KVM** (Linux) / Virtualization.framework
  (macOS). Docker is **not** used. `cellard` is built with `CGO_ENABLED=1` because the
  microsandbox Go SDK dlopens FFI libraries.

### Running the services (non-obvious caveats)
- **KVM required on Linux.** Ensure `/dev/kvm` is available and the `cellar` service
  user can access it (typically membership in the `kvm` group). On first sandbox
  work, `cellard` calls `runtime.EnsureInstalled`, which downloads the microsandbox
  CLI / firmware if needed.
- **Linux bring-up (this Cloud VM):** run as root (`sudo`). Defaults are
  `/var/run/cellar/cellar.sock` and `/var/lib/cellar`. Typical bring-up:
  `sudo bin/cellard` → `sudo bin/cellar init --advertise-addr 127.0.0.1:17946` →
  `sudo bin/cellar status` (expect `is_leader: true`) →
  `sudo bin/cellar api-key create --name demo` → optionally
  `sudo bin/cellar-gateway --listen 127.0.0.1:8080 --data-dir /var/lib/cellar`
  (health at `/healthz`, `/readyz`).
- **macOS:** defaults are `~/.cellar` (data + socket). Prefer running `cellard` /
  `cellar` without sudo so the data dir stays under `/Users/...` (when sudo is
  used, defaults follow `SUDO_USER`’s home).

### Install
- `curl … | sh` (`install.sh`) and `make install` support **Linux and Darwin**.
- Linux: binaries → `$PREFIX/bin` (default `/usr/local`), plus systemd units +
  sysusers from `contrib/`.
- Darwin: host tools → `$PREFIX/bin` (default `~/.local/bin`, no sudo); LaunchAgents
  under `~/Library/LaunchAgents`. Override with `CELLAR_PREFIX` / `CELLAR_DATA_DIR`.

### Clients
Point the official microsandbox SDK at the Cellar gateway as the cloud backend URL
(and pass a Cellar API key). Do not use a vendored Cellar SDK.

### Tests
- Prefer package-scoped tests during migration, e.g.
  `go test ./internal/sandbox ./internal/raftstore ./internal/scheduler
  ./internal/grpcapi ./internal/gateway ./internal/runtime`.
- `make test` (`go test ./...`) exercises the full tree; some packages may need
  CGO for anything that imports the microsandbox driver.
- Docs (from `docs/`): `bun run types:check`, `bun run build`, `bun run dev`
  (serves on `:3000`).
