<p align="center">
  <img src="assets/cellar-logo.png" alt="Cellar — isometric C of slate-blue cubes with a single orange cube in the center" width="220" />
</p>

# Cellar

Cellar is a control plane for isolated [microsandbox](https://github.com/superradcompany/microsandbox) VMs.
This repository implements the **cluster identity layer** (mTLS gRPC, Raft-replicated CA) and
**sandbox lifecycle** (desired state in Raft, local microsandbox driver on every node via the official Go SDK).

Clients use the **official microsandbox SDKs** in cloud mode with the backend URL set to
**`cellar-gateway`**. There is no first-party Cellar client SDK in this repo.

## Binaries

| Binary           | Role                                                                                                                                                                      |
| ---------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `cellard`        | Always-on node daemon (manager or worker). Local control over a unix socket; remote gRPC on `:17946` after `init`/`join`. Runs sandboxes via microsandbox (needs CGO/KVM). |
| `cellar`         | CLI client (`init`, `join`, `join-token`, `status`, `api-key …`, `node …`, `sandbox …`) talking to local `cellard`.                                                       |
| `cellar-gateway` | HTTP/JSON front door (Gin). Runs beside `cellard` on manager or worker hosts; proxies to manager `SandboxAPI` with the caller’s API key. Default listen `:8080`.          |

## Roles

| Role        | Join token    | Raft  | Holds RootCA key                | Control plane |
| ----------- | ------------- | ----- | ------------------------------- | ------------- |
| **Manager** | manager token | Voter | Yes (via Raft `Cluster.RootCA`) | Yes           |
| **Worker**  | worker token  | No    | No                              | No            |

The CA private key and join secrets live on the raft-backed `Cluster.RootCA` object.
Local disk stores only this node’s leaf cert/key and the public CA cert.

## Ports / sockets

| Listener     | Default                       | Auth                                                                           | Purpose                                                                         |
| ------------ | ----------------------------- | ------------------------------------------------------------------------------ | ------------------------------------------------------------------------------- |
| Unix socket  | `/var/run/cellar/cellar.sock` | Local FS permissions                                                           | `Init`, `Join`, `JoinToken`, `Status`, `api-key …`, `node …`, local sandbox ops |
| Remote gRPC  | `:17946`                      | Bootstrap insecure TLS + token digest; else mTLS **or** API key (`SandboxAPI`) | CA issue/renew, raft membership, public sandbox client API                      |
| Gateway HTTP | `:8080`                       | API key (`Authorization: Bearer` / `X-Api-Key`); terminate TLS at ALB/proxy    | Public JSON API for apps (`cellar-gateway`)                                     |
| Raft TCP     | `127.0.0.1:17947`             | Manager network                                                                | Consensus / CA key replication                                                  |

## Build & install

```bash
make build                  # → bin/cellard, bin/cellar, bin/cellar-gateway
# or: make cellard / make cellar / make cellar-gateway

sudo make install           # Linux: binaries → /usr/local/bin, plus systemd units + sysusers from contrib/
make install                # macOS: binaries → ~/.local/bin (no sudo), LaunchAgents under ~/Library/LaunchAgents
```

Requires Go 1.26+. `cellard` is built with `CGO_ENABLED=1` (microsandbox FFI). `make install` places the three host binaries next to each other. It also installs:

| Source                                   | Destination (Linux)                              |
| ---------------------------------------- | ------------------------------------------------ |
| `contrib/systemd/cellard.service`        | `/usr/lib/systemd/system/cellard.service`        |
| `contrib/systemd/cellar-gateway.service` | `/usr/lib/systemd/system/cellar-gateway.service` |
| `contrib/systemd/cellar.sysusers`        | `/usr/lib/sysusers.d/cellar.conf`                |

| Source                                                 | Destination (macOS)                                           |
| ------------------------------------------------------ | ------------------------------------------------------------- |
| host binaries                                          | `~/.local/bin/`                                               |
| `contrib/launchd/com.prodioslabs.cellard.plist`        | `~/Library/LaunchAgents/com.prodioslabs.cellard.plist`        |
| `contrib/launchd/com.prodioslabs.cellar-gateway.plist` | `~/Library/LaunchAgents/com.prodioslabs.cellar-gateway.plist` |

`make install` does not enable or start the services. On macOS, load agents with `launchctl bootstrap gui/$(id -u) …`. Use `make uninstall` to remove the installed files.

All three binaries share one SemVer. Local builds stamp version/commit/date via ldflags (`VERSION`, `COMMIT`, `BUILD_DATE` overrides). Check with:

```bash
cellar --version
cellard -version
cellar-gateway -version
```

## Releases

Push a SemVer tag (`vX.Y.Z` or `vX.Y.Z-rc.1`) to publish a [GitHub Release](https://github.com/prodioslabs/cellar/releases) with Linux and macOS **amd64** / **arm64** archives.

Each archive is named `cellar_<version>_<os>_<arch>.tar.gz` and contains:

```text
cellar
cellard
cellar-gateway
LICENSE
README.md
contrib/systemd/…
contrib/launchd/…   # included on all platforms; used on Darwin
```

Install the current release on Linux or macOS with:

```bash
curl -fsSL https://cellar.prodioslabs.in/install.sh | sh
```

The installer detects the OS (**linux** / **darwin**) and arch (**amd64** / **arm64**), verifies archives against `checksums.txt`, and installs binaries. On Linux it also installs systemd units and the sysusers definition (same as `sudo make install`). On macOS it defaults to `~/.local` (no sudo) and stages LaunchAgents under `~/Library/LaunchAgents`. It uses `sudo` when needed, but does not enable or start the services.

To install a specific version or prefix:

```bash
curl -fsSL https://cellar.prodioslabs.in/install.sh | CELLAR_VERSION=v0.1.0 CELLAR_PREFIX=/usr/local sh
```

## Quick start

```bash
# On each host — install, create the cellar system user, start the daemon
sudo make install
sudo systemd-sysusers
sudo usermod -aG kvm cellar   # Linux: KVM access for microsandbox
sudo systemctl daemon-reload
sudo systemctl enable --now cellard
sudo systemctl enable --now cellar-gateway   # optional: HTTP front door for apps

# Host A — initialize cluster (first manager)
# Use this host’s reachable address (defaults: data /var/lib/cellar, socket /var/run/cellar/cellar.sock)
sudo cellar init --advertise-addr 192.0.2.10:17946

# Print ready-to-run join command
sudo cellar join-token worker
# → cellar join --token CLLRN-1-… 192.0.2.10:17946

# Host B — worker (after install + enable --now cellard as above)
sudo cellar join --token CLLRN-1-… 192.0.2.10:17946
```

Managers join the same way with the **manager** token (and should pass `--advertise-addr` / `--raft-addr`).

## Manage Cellar as a non-root user

The control socket is mode `0660` and owned by `cellar:cellar`. By default only root can talk to it; to run `cellar` without `sudo`, add your user to the `cellar` group (created by `systemd-sysusers` from `contrib/systemd/cellar.sysusers`).

> **Warning:** The `cellar` group grants local control-plane access equivalent to root for CLI operations against the socket.

1. Add your user to the `cellar` group.

   ```bash
   sudo usermod -aG cellar $USER
   ```

2. Log out and log back in so that your group membership is re-evaluated. You can also activate the group in the current shell:

   ```bash
   newgrp cellar
   ```

3. Verify that you can run `cellar` without `sudo` (with `cellard` already running):

   ```bash
   cellar status
   ```

## Sandboxes

Requires **KVM** (Linux `/dev/kvm`) or macOS Virtualization.framework. `cellard` drives VMs through the official microsandbox Go SDK (`EnsureInstalled` on first use). Isolation is hardware virtualization — not shared-kernel containers.

```bash
# Create an isolated sandbox (does not start unless --start)
sudo cellar sandbox create --name demo --image alpine:3.20
# or a language runtime preset:
sudo cellar sandbox create --name demo --runtime node-26 --memory-mib 1024 --vcpus 2 --start

sudo cellar sandbox ls
sudo cellar sandbox ls --all
sudo cellar sandbox inspect <id>
sudo cellar sandbox start <id>
sudo cellar sandbox logs -f <id>
sudo cellar sandbox stop <id>
sudo cellar sandbox rm <id>
```

Runtime presets and their images:

| Runtime       | Image                         |
| ------------- | ----------------------------- |
| `node-26`     | `node:26-alpine`              |
| `bun-1.3`     | `oven/bun:1.3-alpine`         |
| `python-3.13` | `astral/uv:python3.13-alpine` |
| `go-1.26`     | `golang:1.26-alpine`          |

Managers and workers both run sandboxes. Desired state lives in Raft; the leader schedules onto the least-loaded live node
(nodes with availability `pause` or `drain` are skipped).

**How create works.** `sandbox create` writes desired state to Raft and returns immediately. Each node's runtime agent pulls sandboxes assigned to it via the Raft leader / heartbeat path and reconciles with the local microsandbox driver.

## Manage nodes

Node writes (`promote`, `demote`, `rm`, `update`) must run on the **Raft leader**. Reads (`ls`, `inspect`) work on any manager.

```bash
sudo cellar node ls
sudo cellar node inspect <id>

sudo cellar node promote <worker-id>
sudo cellar node demote <manager-id>

sudo cellar node update <id> --availability drain
sudo cellar node update <id> --label-add zone=a --label-rm zone
sudo cellar node update <id> --role manager   # same as promote

sudo cellar node rm <id>
sudo cellar node rm --force <id>
```

Availability:

- `pause` — cordon only: no new placements; existing sandboxes stay.
- `drain` — cordon and reschedule running sandboxes onto other live nodes. Bind-mounted sandboxes stay until they stop or you remove them.

## Client API (remote apps)

Apps talk to **`cellar-gateway`** over HTTPS (JSON, msb-cloud compatible shapes). The gateway runs on manager or worker hosts, dials manager `SandboxAPI` with the cluster CA, and forwards each caller’s API key. Mint the key once with the CLI; do **not** run `cellar sandbox …` from application code.

### Create an API key

`api-key create` and `api-key rm` must run on the **Raft leader** (local unix socket).
Check with `cellar status` (`is_leader: true`).

```bash
sudo cellar status
# is_leader:   true

sudo cellar api-key create --name ci
```

Example output:

```text
API key created: <id> (ci)

Store this secret now; it will not be shown again:

    cellar_<40 hex chars>

Export for clients:

    export CELLAR_API_KEY=cellar_…
```

The raw `cellar_…` secret is shown **once**. Only a hash is stored in Raft; `ls` returns a mask.

```bash
sudo cellar api-key ls
sudo cellar api-key rm <id>   # revoke; also must run on the leader
```

### Run the gateway

```bash
sudo systemctl enable --now cellar-gateway
# or manually:
cellar-gateway --listen :8080 --data-dir /var/lib/cellar
# optional: --upstreams 192.0.2.10:17946,192.0.2.11:17946
```

Health endpoints (no auth): `/healthz`, `/readyz`.

### Use the official microsandbox SDK

Configure the microsandbox SDK’s **cloud** backend URL to your Cellar gateway and pass the API key
(for example `CELLAR_API_KEY` / gateway base URL). Lifecycle and agent protocols follow the
microsandbox cloud API that `cellar-gateway` implements.

### Rotation

Create a new key, update the API key in your apps/secrets, then `cellar api-key rm <old-id>` on the leader.

## Cluster CA (HA)

1. `cellar init` generates a RootCA in memory, issues a local manager leaf, bootstraps Raft, and proposes `CreateCluster` with `CAKey` + `CACert` + join tokens.
2. Every manager receives the same `Cluster.RootCA` through the raft log/snapshots.
3. Only the **leader** runs the CA signer (`UpdateRootCA` from the store). On failover, the new leader loads signing material from raft — it does not re-seed from disk.
4. External APIs never return `CAKey` (`GetRootCACertificate` is cert-only; `Cluster.Redact()` strips the key).

## Development

```bash
make tools   # install protoc-gen-go and protoc-gen-go-grpc (needs protoc on PATH)
make proto   # regenerate gRPC stubs under api/gen/
make test
make clean
```

## License

MIT — see [LICENSE](LICENSE).
