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

## Build

```bash
make build          # → bin/cellard, bin/cellar, bin/cellar-agent
# or: make cellard / make cellar / make cellar-agent
```

Requires Go 1.26+. `cellar-agent` is built with `CGO_ENABLED=0` for gVisor. Keep it next to `cellard` (or set `CELLAR_AGENT_BINARY`) so the daemon can bind-mount it into sandboxes.

## Quick start

```bash
# Host A — start daemon (idle until init)
./bin/cellard --data-dir ./data-a --socket ./cellar-a.sock \
  --listen 127.0.0.1:17946 --raft-addr 127.0.0.1:17947

# Initialize cluster (first manager)
./bin/cellar --socket ./cellar-a.sock init --advertise-addr 127.0.0.1:17946 \
  --listen-addr 127.0.0.1:17946 --raft-addr 127.0.0.1:17947

# Print ready-to-run join command
./bin/cellar --socket ./cellar-a.sock join-token worker
# → cellar join --token CLLRN-1-… 127.0.0.1:17946

# Host B — worker
./bin/cellard --data-dir ./data-b --socket ./cellar-b.sock
./bin/cellar --socket ./cellar-b.sock join --token CLLRN-1-… 127.0.0.1:17946
```

Managers join the same way with the **manager** token (and should pass `--advertise-addr` / `--raft-addr`).

## Sandboxes

Requires Docker with the gVisor [`runsc`](https://gvisor.dev/docs/user_guide/install/) runtime registered (`sudo runsc install && sudo systemctl restart docker`).

On Arch Linux–based systems:

```bash
yay -Sy gvisor-bin
sudo runsc install && sudo systemctl restart docker
```

Each sandbox runs with **`cellar-agent` as the container entrypoint** (PID 1). There is no create-time `--entrypoint` / `command`; the sandbox stays up until `stop`/`rm`. Workloads run via `sandbox exec`, which talks to the agent over a bind-mounted Unix socket authenticated with a per-sandbox bearer token.

```bash
# Create an isolated sandbox (no external network)
cellar sandbox create --image alpine
# or from YAML:
cellar sandbox create -f examples/sandbox.yaml

# Allowlisted egress (enforced by cellard's userspace proxy + iptables REDIRECT)
cellar sandbox create --image curlimages/curl --network allowlist \
  --allow-host example.com --allow-port 443
# or: cellar sandbox create -f examples/sandbox-allowlist.yaml

cellar sandbox ls
cellar sandbox inspect <id>
cellar sandbox logs -f <id>
cellar sandbox exec <id> -- uname -a
cellar sandbox stop <id>
cellar sandbox rm <id>
```

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
