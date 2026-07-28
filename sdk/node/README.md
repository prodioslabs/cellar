# `@cellar/node`

TypeScript SDK for Cellar’s public **SandboxAPI**. Works on **Node.js 18+** and **Bun**.

Talks to managers over gRPC + TLS (default port `17946`) with an API key — same model as the Go client in `pkg/client`.

## Install

```bash
npm install @cellar/node
# or: bun add @cellar/node
```

## Configure

| Variable           | Required | Meaning                                                                     |
| ------------------ | -------- | --------------------------------------------------------------------------- |
| `CELLAR_API_KEY`   | yes      | Raw key from `cellar api-key create` (`cellar_…`)                           |
| `CELLAR_ENDPOINTS` | yes      | Comma-separated manager gRPC addrs (`host:17946`)                           |
| `CELLAR_CA_CERT`   | yes      | File path, `\n`-escaped PEM (from `cellar ca-cert --env`), or base64 of PEM |

```bash
export CELLAR_API_KEY='cellar_…'
export CELLAR_ENDPOINTS='192.0.2.10:17946,192.0.2.11:17946'
export CELLAR_CA_CERT=/var/lib/cellar/ca.crt
```

## Usage

```ts
import { Client } from '@cellar/node'

const c = Client.fromEnv()
// or: Client.create({ endpoints: ["192.0.2.10:17946"], apiKey: "cellar_…", caCertFile: "./ca.crt" })

const sb = await c.create({
  spec: { image: 'alpine:3.20' },
})
console.log('created', sb.id)

const res = await c.exec(sb.id, ['uname', '-a'])
console.log(`exit=${res.exitCode} stdout=${res.stdout.toString()}`)

await c.remove(sb.id)
```

Supported ops: `create`, `stop`, `remove`, `get`, `list`, `updateNetwork`, `exec`.

Auth is sent as `Authorization: Bearer …` and `x-api-key`. The client round-robins endpoints and retries on dial / `UNAVAILABLE` / `DEADLINE_EXCEEDED` / `RESOURCE_EXHAUSTED`. TLS verifies managers with the cluster CA using SNI `cellar-manager`.

## Develop

From this directory (requires [Bun](https://bun.sh)):

```bash
bun install
bun run proto    # or: make sdk-node-proto (from repo root)
bun run test
bun run build
```
