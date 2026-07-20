# Cellar

Cellar is a cluster certificate authority inspired by Docker Swarm-style join tokens. Managers form a [HashiCorp Raft](https://github.com/hashicorp/raft) cluster that replicates the root CA (including the private key). Workers join with a worker token, receive leaf certificates, and never hold the CA private key.

## Roles

| Role | Join token | Raft | Holds RootCA key | Control plane |
|------|------------|------|------------------|---------------|
| **Manager** | `tokens.manager` | Voter | Yes (via Raft FSM) | Yes |
| **Worker** | `tokens.worker` | No | No | No |

Both roles use the agent library for join, renew, and peer mTLS. Only managers may call control-plane APIs when presenting a client certificate.

## Ports

Cellar uses **two listeners** on each manager:

| Flag | Default | Protocol | Audience |
|------|---------|----------|----------|
| `-listen` | `:7946` | HTTP CA / control-plane API | Agents, operators |
| `-raft-addr` | `127.0.0.1:7947` | Raft peer RPC | Managers only |

Do not expose the Raft port to workers or untrusted networks.

## Build

```bash
go build -o cellar ./cmd/cellar
```

Requires Go 1.26+.

## Quick start (single manager)

```bash
./cellar -data-dir ./data-a -listen :7946 -raft-addr 127.0.0.1:7947 -node-id a -bootstrap

curl -s -X POST http://127.0.0.1:7946/api/v1/cluster/init -d '{}' | jq
# → cluster_id, tokens.worker, tokens.manager
```

## Multi-manager cluster

```bash
# Manager A (bootstrap)
./cellar -data-dir ./data-a -listen :7946 -raft-addr 10.0.0.1:7947 \
  -node-id a -http-advertise http://10.0.0.1:7946 -bootstrap

curl -X POST http://10.0.0.1:7946/api/v1/cluster/init -d '{}'

# Manager B (join Raft; receives RootCA via log/snapshot replication)
./cellar -data-dir ./data-b -listen :7946 -raft-addr 10.0.0.2:7947 \
  -node-id b -http-advertise http://10.0.0.2:7946 \
  -join http://10.0.0.1:7946
```

After join, every manager has the same RootCA. Mutating APIs are leader-only; followers return `503` with `X-Cellar-Leader` pointing at the leader’s advertised HTTP URL.

## Workers

Workers do not run the `cellar` binary as a Raft member. Use the agent library (`pkg/agent`) with the **worker** join token:

```go
a := agent.New("http://10.0.0.1:7946", "./worker-data")
id, err := a.Join(ctx, workerToken) // RoleWorker leaf cert + public ca.crt
```

Agent data directory layout: `node.crt`, `node.key`, `ca.crt`, `identity.json`.

## CLI flags

| Flag | Default | Description |
|------|---------|-------------|
| `-data-dir` | `./cellar-data` | Persistent state (`raft/` under this dir) |
| `-listen` | `:7946` | HTTP API listen address |
| `-raft-addr` | `127.0.0.1:7947` | Raft TCP listen/advertise (`host:port`) |
| `-node-id` | basename of `-data-dir` | Stable Raft server ID |
| `-http-advertise` | `http://<listen>` | HTTP base URL peers use for redirects |
| `-bootstrap` | `false` | Form a new single-voter cluster (first manager only) |
| `-join` | empty | Leader HTTP base URL to join as an additional manager |

`-bootstrap` and `-join` are mutually exclusive.

## HTTP API

| Method | Path | Notes |
|--------|------|-------|
| `POST` | `/api/v1/cluster/init` | Bootstrap CA + join tokens (leader only) |
| `GET` | `/api/v1/ca/certificate` | Public CA cert PEM |
| `POST` | `/api/v1/ca/issue` | Issue/renew node cert (`token` or `node_id`) |
| `GET` | `/api/v1/ca/status/{node_id}` | Node membership / cert metadata |
| `GET` | `/api/v1/cluster/tokens` | Current worker + manager tokens |
| `POST` | `/api/v1/cluster/rotate-tokens` | Rotate join secrets (leader + control plane) |
| `GET` | `/api/v1/cluster/leader` | Leadership / leader HTTP advertise |
| `POST` | `/api/v1/cluster/managers` | `{node_id,raft_addr,http_addr}` → `AddVoter` |
| `DELETE` | `/api/v1/cluster/managers/{node_id}` | Remove Raft voter |

Control-plane routes allow plain HTTP for bootstrap; when a client TLS certificate is present, only **manager** certs are accepted.

## Data layout (managers)

```text
<data-dir>/
  raft/
    raft.db       # BoltDB log + stable store (includes RootCA PEMs in log/snapshots)
    snapshots/
```

Treat manager data directories as secret: they contain the cluster root private key.

## Development

```bash
go test ./...
```

`internal/store.FileStore` remains available for unit tests. Production `cmd/cellar` uses `internal/raftstore`.

## License

See repository license file if present.
