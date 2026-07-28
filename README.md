# Cellar

Cellar is a Docker Swarm–style container orchestrator control plane for isolated sandboxes.
This repository implements the **cluster identity layer** (mTLS gRPC, Raft-replicated CA) and
**sandbox lifecycle** (desired state in Raft, Docker + gVisor `runsc` on every node, userspace egress policy).

## Binaries

| Binary         | Role                                                                                                                                                                |
| -------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `cellard`      | Always-on node daemon (manager or worker). Local control over a unix socket; remote gRPC on `:17946` after `init`/`join`. Runs sandboxes via host Docker + `runsc`. |
| `cellar`       | CLI client (`init`, `join`, `join-token`, `status`, `ca-cert`, `api-key …`, `sandbox …`) talking to local `cellard`.                                                           |
| `cellar-agent` | In-sandbox PID 1. Bound into each container; serves authenticated gRPC (`Health`, `RunCommand`) on a per-sandbox Unix socket.                                       |

## Roles

| Role        | Join token    | Raft  | Holds RootCA key                | Control plane |
| ----------- | ------------- | ----- | ------------------------------- | ------------- |
| **Manager** | manager token | Voter | Yes (via Raft `Cluster.RootCA`) | Yes           |
| **Worker**  | worker token  | No    | No                              | No            |

The CA private key and join secrets live on the raft-backed `Cluster.RootCA` object (SwarmKit-style).
Local disk stores only this node’s leaf cert/key and the public CA cert.

## Ports / sockets

Defaults avoid Docker Swarm’s control-plane ports (`7946` gossip, `2377` manager).

| Listener    | Default                       | Auth                                                                           | Purpose                                                               |
| ----------- | ----------------------------- | ------------------------------------------------------------------------------ | --------------------------------------------------------------------- |
| Unix socket | `/var/run/cellar/cellar.sock` | Local FS permissions                                                           | `Init`, `Join`, `JoinToken`, `Status`, `api-key …`, local sandbox ops |
| Remote gRPC | `:17946`                      | Bootstrap insecure TLS + token digest; else mTLS **or** API key (`SandboxAPI`) | CA issue/renew, raft membership, public sandbox client API            |
| Raft TCP    | `127.0.0.1:17947`             | Manager network                                                                | Consensus / CA key replication                                        |

## Build & install

```bash
make build          # → bin/cellard, bin/cellar, bin/cellar-agent
# or: make cellard / make cellar / make cellar-agent

sudo make install   # Linux: binaries → /usr/local/bin, plus systemd unit + sysusers from contrib/
```

Requires Go 1.26+. `cellar-agent` is built with `CGO_ENABLED=0` for gVisor. `make install` places it next to `cellard` under `/usr/local/bin` (override with `CELLAR_AGENT_BINARY` if needed). It also installs:

| Source                            | Destination                               |
| --------------------------------- | ----------------------------------------- |
| `contrib/systemd/cellard.service` | `/usr/lib/systemd/system/cellard.service` |
| `contrib/systemd/cellar.sysusers` | `/usr/lib/sysusers.d/cellar.conf`         |

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

Requires Docker with the gVisor `[runsc](https://gvisor.dev/docs/user_guide/install/)` runtime registered. After `sudo runsc install`, enable host Unix-domain sockets so `cellar-agent` can expose its control socket on the bind-mounted sandbox dir (gVisor blocks this by default):

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

Each sandbox runs with `cellar-agent` **as the container entrypoint** (PID 1). There is no create-time `--entrypoint` / `command`; the sandbox stays up until `stop`/`rm`. Workloads run via `sandbox exec`, which talks to the agent over a bind-mounted Unix socket authenticated with a per-sandbox bearer token.

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

| Runtime       | Image                         |
| ------------- | ----------------------------- |
| `node-26`     | `node:26-alpine`              |
| `bun-1.3`     | `oven/bun:1.3-alpine`         |
| `python-3.13` | `astral/uv:python3.13-alpine` |
| `go-1.26`     | `golang:1.26-alpine`          |

Managers and workers both run sandboxes. Desired state lives in Raft; the leader schedules onto the least-loaded live node.

## Client API (remote apps)

Apps talk to **managers** over gRPC (`SandboxAPI` on `:17946`) with a long-lived API key.
Mint the key once with the CLI; do **not** run `cellar sandbox …` from application code.

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
# ID  NAME  MASK              CREATED
# …   ci    cellar_ab…wxyz    …

sudo cellar api-key rm <id>   # revoke; also must run on the leader
```

### Export the cluster CA

Clients need the **public** cluster CA cert (not the CA private key) to verify managers over TLS.

```bash
# PEM to stdout (any joined manager or worker)
sudo cellar ca-cert

# Write PEM file
sudo cellar ca-cert --out ca.crt

# Base64 one-liner (for secret stores)
sudo cellar ca-cert --format base64

# Ready-to-paste .env line (\\n-escaped PEM)
sudo cellar ca-cert --env
# → CELLAR_CA_CERT="-----BEGIN CERTIFICATE-----\n...\n-----END CERTIFICATE-----\n"

sudo cellar ca-cert --env --out cellar.ca.env
```

You can still copy `/var/lib/cellar/ca.crt` from a node’s data dir directly.

### Configure the client

| Variable | Required | Meaning |
|----------|----------|---------|
| `CELLAR_API_KEY` | yes | Raw key from `api-key create` (`cellar_…`) |
| `CELLAR_ENDPOINTS` | yes | Comma-separated manager gRPC addrs (`host:17946`) |
| `CELLAR_CA_CERT` | yes | File path, `\n`-escaped PEM (from `ca-cert --env`), or base64 of PEM |

```bash
export CELLAR_API_KEY='cellar_…'
export CELLAR_ENDPOINTS='192.0.2.10:17946,192.0.2.11:17946,192.0.2.12:17946'

# Option A: path to PEM file
export CELLAR_CA_CERT=/var/lib/cellar/ca.crt
# Option B: paste output of `cellar ca-cert --env` into your .env
```

List every manager you want the client to fail over across. A single endpoint is fine; there is no in-tree network load balancer.

### Use the Go client

Package: `[pkg/client](pkg/client)`. Auth is sent as `Authorization: Bearer …` and `x-api-key`. The client round-robins endpoints and retries on dial / `Unavailable` / `DeadlineExceeded`. Non-leader managers forward writes to the Raft leader; Exec/Logs are proxied to the owning node.

```go
package main

import (
	"context"
	"fmt"
	"log"

	cellarv1 "github.com/prodioslabs/cellar/api/gen"
	"github.com/prodioslabs/cellar/pkg/client"
)

func main() {
	c, err := client.NewFromEnv() // or client.New(client.Config{…})
	if err != nil {
		log.Fatal(err)
	}
	ctx := context.Background()

	sb, err := c.Create(ctx, &cellarv1.SandboxCreateRequest{
		Spec: &cellarv1.SandboxSpec{Image: "alpine:3.20"},
	})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("created", sb.Id)

	res, err := c.Exec(ctx, sb.Id, []string{"uname", "-a"})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("exit=%d stdout=%s\n", res.ExitCode, res.Stdout)

	if err := c.Remove(ctx, sb.Id); err != nil {
		log.Fatal(err)
	}
}
```

Supported client ops: `Create`, `Stop`, `Remove`, `Get`, `List`, `UpdateNetwork`, `Exec` (and streaming Logs via the generated `SandboxAPI` stub if you dial directly).

### Use the TypeScript client

Package: [`@cellar/node`](sdk/node) (Node.js 18+ and Bun). Same env vars and auth as the Go client.

```bash
npm install @cellar/node
```

```ts
import { Client } from "@cellar/node";

const c = Client.fromEnv();

const sb = await c.create({
  spec: { image: "alpine:3.20" },
});
console.log("created", sb.id);

const res = await c.exec(sb.id, ["uname", "-a"]);
console.log(`exit=${res.exitCode} stdout=${res.stdout.toString()}`);

await c.remove(sb.id);
```

See [`sdk/node/README.md`](sdk/node/README.md) for details. Regenerate stubs with `make sdk-node-proto`.

### Rotation

Create a new key, update `CELLAR_API_KEY` in your apps/secrets, then `cellar api-key rm <old-id>` on the leader.

## Cluster CA (HA)

1. `cellar init` generates a RootCA in memory, issues a local manager leaf, bootstraps Raft, and proposes `CreateCluster` with `CAKey` + `CACert` + join tokens.
2. Every manager receives the same `Cluster.RootCA` through the raft log/snapshots.
3. Only the **leader** runs the CA signer (`UpdateRootCA` from the store). On failover, the new leader loads signing material from raft — it does not re-seed from disk.
4. External APIs never return `CAKey` (`GetRootCACertificate` is cert-only; `Cluster.Redact()` strips the key).

## Development

```bash
make tools   # install protoc-gen-go and protoc-gen-go-grpc (needs protoc on PATH)
make proto   # regenerate gRPC stubs under api/gen/
make sdk-node-proto  # regenerate TypeScript stubs under sdk/node/src/gen/ (needs bun install in sdk/node)
make test
make clean
```

## License

See repository license if present.
