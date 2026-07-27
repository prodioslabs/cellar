# Cellar

Cellar is a Docker Swarm–style container orchestrator control plane for isolated sandboxes.
This repository implements the **cluster identity layer** (mTLS gRPC, Raft-replicated CA) and
**sandbox lifecycle** (desired state in Raft, Docker + gVisor `runsc` on every node, userspace egress policy).

## Binaries

| Binary | Role |
|--------|------|
| **`cellard`** | Always-on node daemon (manager or worker). Local control over a unix socket; remote gRPC on `:17946` after `init`/`join`. Runs sandboxes via host Docker + `runsc`. |
| **`cellar`** | CLI client (`init`, `join`, `join-token`, `status`, `sandbox …`) talking to local `cellard`. |
| **`cellar-agent`** | In-sandbox PID 1. Bound into each container; serves authenticated gRPC (`Health`, `RunCommand`) on a per-sandbox Unix socket. |

## Roles

| Role | Join token | Raft | Holds RootCA key | Control plane |
|------|------------|------|------------------|---------------|
| **Manager** | manager token | Voter | Yes (via Raft `Cluster.RootCA`) | Yes |
| **Worker** | worker token | No | No | No |

The CA private key and join secrets live on the raft-backed **`Cluster.RootCA`** object (SwarmKit-style).
Local disk stores only this node’s leaf cert/key and the public CA cert.

## Ports / sockets

Defaults avoid Docker Swarm’s control-plane ports (`7946` gossip, `2377` manager).

| Listener | Default | Auth | Purpose |
|----------|---------|------|---------|
| Unix socket | `/var/run/cellar/cellar.sock` | Local FS permissions | `Init`, `Join`, `JoinToken`, `Status` |
| Remote gRPC | `:17946` | Bootstrap insecure TLS + token digest; else mTLS | CA issue/renew, raft membership |
| Raft TCP | `127.0.0.1:17947` | Manager network | Consensus / CA key replication |

## Build & install

```bash
make build          # → bin/cellard, bin/cellar, bin/cellar-agent
# or: make cellard / make cellar / make cellar-agent

sudo make install   # Linux: binaries → /usr/local/bin, plus systemd unit + sysusers from contrib/
```

Requires Go 1.26+. `cellar-agent` is built with `CGO_ENABLED=0` for gVisor. `make install` places it next to `cellard` under `/usr/local/bin` (override with `CELLAR_AGENT_BINARY` if needed). It also installs:

| Source | Destination |
|--------|-------------|
| `contrib/systemd/cellard.service` | `/usr/lib/systemd/system/cellard.service` |
| `contrib/systemd/cellar.sysusers` | `/usr/lib/sysusers.d/cellar.conf` |

`make install` does not enable or start the service. Use `make uninstall` to remove the installed files.

## Quick start

```bash
# On each host — install, create the cellar system user, start the daemon
sudo make install
sudo systemd-sysusers
sudo systemctl daemon-reload
sudo systemctl enable --now cellard

# Host A — initialize cluster (first manager)
# Use this host’s reachable address (defaults: data /var/lib/cellar, socket /var/run/cellar/cellar.sock)
sudo cellar init --advertise-addr 192.0.2.10:17946

# Print ready-to-run join command
sudo cellar join-token worker
# → cellar join --token CLLRN-1-… 192.0.2.10:17946

# Host B — worker (after install + enable --now cellard as above)
sudo cellar join --token CLLRN-1-… 192.0.2.10:17946
```

The control socket is mode `0660` and owned by `cellar:cellar`, so local CLI calls need root (or membership in the `cellar` group). Managers join the same way with the **manager** token (and should pass `--advertise-addr` / `--raft-addr`).
## Sandboxes

Requires Docker with the gVisor [`runsc`](https://gvisor.dev/docs/user_guide/install/) runtime registered. After `sudo runsc install`, enable host Unix-domain sockets so `cellar-agent` can expose its control socket on the bind-mounted sandbox dir (gVisor blocks this by default):

```json
{
  "runtimes": {
    "runsc": {
      "path": "/usr/bin/runsc",
      "runtimeArgs": ["--host-uds=all"]
    }
  }
}
```

Put that in `/etc/docker/daemon.json` (merge with any existing config), then `sudo systemctl restart docker`. Adjust `"path"` if `runsc` lives elsewhere (`which runsc`).

On Arch Linux–based systems:

```bash
yay -Sy gvisor-bin
sudo runsc install
# then add runtimeArgs as above and restart docker
sudo systemctl restart docker
```

Each sandbox runs with **`cellar-agent` as the container entrypoint** (PID 1). There is no create-time `--entrypoint` / `command`; the sandbox stays up until `stop`/`rm`. Workloads run via `sandbox exec`, which talks to the agent over a bind-mounted Unix socket authenticated with a per-sandbox bearer token.

```bash
# Create an isolated sandbox (no external network)
sudo cellar sandbox create --image alpine
# or a language runtime preset (resolves to an Alpine image):
sudo cellar sandbox create --runtime node-26
# or from YAML:
sudo cellar sandbox create -f examples/sandbox.yaml

# Allowlisted egress (enforced by cellard's userspace proxy + iptables REDIRECT)
# See internal/egress/README.md for proxy, DNS bait mount, and policy details.
sudo cellar sandbox create --image curlimages/curl --network allowlist \
  --allow-host example.com --allow-port 443
# or: sudo cellar sandbox create -f examples/sandbox-allowlist.yaml

# Tighten (or loosen) the policy of a running sandbox. Takes effect immediately
# and closes connections the new policy no longer allows.
sudo cellar sandbox network <id> --mode allowlist --allow-host api.example.com --allow-port 443

sudo cellar sandbox ls
sudo cellar sandbox inspect <id>
sudo cellar sandbox logs -f <id>
sudo cellar sandbox exec <id> -- uname -a
sudo cellar sandbox stop <id>
sudo cellar sandbox rm <id>
```

Runtime presets and their images:

| Runtime | Image |
|---------|-------|
| `node-26` | `node:26-alpine` |
| `bun-1.3` | `oven/bun:1.3-alpine` |
| `python-3.13` | `astral/uv:python3.13-alpine` |
| `go-1.26` | `golang:1.26-alpine` |

Managers and workers both run sandboxes. Desired state lives in Raft; the leader schedules onto the least-loaded live node.

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

See repository license if present.
