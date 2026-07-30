<p align="center">
  <img src="../../assets/cellar-logo.png" alt="Cellar — isometric C of slate-blue cubes with a single orange cube in the center" width="160" />
</p>

# `@cellar/node`

TypeScript SDK for Cellar’s public HTTP gateway. Works on **Node.js 18+** and **Bun**.

Talks to `cellar-gateway` over HTTPS with an API key — same model as the Go client in `sdk/go`.

## Install

```bash
npm install @cellar/node
# or: bun add @cellar/node
```

## Configure

| Variable          | Required | Meaning                                           |
| ----------------- | -------- | ------------------------------------------------- |
| `CELLAR_API_KEY`  | yes      | Raw key from `cellar api-key create` (`cellar_…`) |
| `CELLAR_ENDPOINT` | yes      | Gateway base URL (`https://cellar.example.com`)   |

```bash
export CELLAR_API_KEY='cellar_…'
export CELLAR_ENDPOINT='https://cellar.example.com'
```

## Usage

```ts
import { Client } from '@cellar/node'

const c = Client.fromEnv()
// or: Client.create({ endpoint: 'https://cellar.example.com', apiKey: 'cellar_…' })

const sb = await c.create({
  spec: { image: 'alpine:3.20' },
})
console.log('created', sb.id)

// Creation returns immediately (often pending). Wait until the container is running.
await sb.waitUntilReady()

const res = await sb.exec(['uname', '-a'])
console.log(`exit=${res.exitCode} stdout=${res.stdout.toString()}`)

for await (const chunk of sb.logs({ tail: 100 })) {
  process.stdout.write(chunk.data)
}

await sb.remove()
```

`Client` ops: `create`, `get`, `list`.

`Sandbox` ops: `waitUntilReady`, `getStatus`, `exec`, `logs`, `stop`, `remove`, `updateNetwork`.

Creation is asynchronous with respect to runtime readiness. Prefer `await sb.waitUntilReady()` before `exec`. Status is refreshed from cellar-gateway via `GET /v1/sandboxes/:id`.

Auth is sent as `Authorization: Bearer …` and `X-Api-Key`.

## Develop

From this directory (requires [Bun](https://bun.sh)):

```bash
bun install
bun run test
bun run build    # or: make sdk-node (from repo root)
```

## License

MIT — see [LICENSE](../../LICENSE).
