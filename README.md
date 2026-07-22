# Cellar

Cellar is a Docker Swarm–style container orchestrator control plane for isolated sandboxes.
This repository currently implements the **cluster identity layer**: an always-on daemon (`cellard`),
a CLI (`cellar`), mTLS gRPC between nodes, and a HashiCorp Raft–replicated cluster CA.

## Binaries

| Binary | Role |
|--------|------|
| **`cellard`** | Always-on node daemon (manager or worker). Local control over a unix socket; remote gRPC on `:7946` after `init`/`join`. |
| **`cellar`** | CLI client (`init`, `join`, `join-token`, `status`) talking to local `cellard`. |

## Roles

| Role | Join token | Raft | Holds RootCA key | Control plane |
|------|------------|------|------------------|---------------|
| **Manager** | manager token | Voter | Yes (via Raft `Cluster.RootCA`) | Yes |
| **Worker** | worker token | No | No | No |

The CA private key and join secrets live on the raft-backed **`Cluster.RootCA`** object (SwarmKit-style).
Local disk stores only this node’s leaf cert/key and the public CA cert.

## Ports / sockets

| Listener | Default | Auth | Purpose |
|----------|---------|------|---------|
| Unix socket | `/var/run/cellar/cellar.sock` | Local FS permissions | `Init`, `Join`, `JoinToken`, `Status` |
| Remote gRPC | `:7946` | Bootstrap insecure TLS + token digest; else mTLS | CA issue/renew, raft membership |
| Raft TCP | `127.0.0.1:7947` | Manager network | Consensus / CA key replication |

## Build

```bash
make build          # → bin/cellard and bin/cellar
# or: make cellard / make cellar
```

Requires Go 1.26+.

## Quick start

```bash
# Host A — start daemon (idle until init)
./bin/cellard --data-dir ./data-a --socket ./cellar-a.sock \
  --listen 127.0.0.1:7946 --raft-addr 127.0.0.1:7947

# Initialize cluster (first manager)
./bin/cellar --socket ./cellar-a.sock init --advertise-addr 127.0.0.1:7946 \
  --listen-addr 127.0.0.1:7946 --raft-addr 127.0.0.1:7947

# Print ready-to-run join command
./bin/cellar --socket ./cellar-a.sock join-token worker
# → cellar join --token CLLRN-1-… 127.0.0.1:7946

# Host B — worker
./bin/cellard --data-dir ./data-b --socket ./cellar-b.sock
./bin/cellar --socket ./cellar-b.sock join --token CLLRN-1-… 127.0.0.1:7946
```

Managers join the same way with the **manager** token (and should pass `--advertise-addr` / `--raft-addr`).

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
