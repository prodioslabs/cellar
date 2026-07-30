import { describe, expect, it, vi } from 'vitest'
import { APIError, Client, EnvAPIKey, EnvEndpoint } from './client.js'
import { Sandbox } from './sandbox.js'

function jsonResponse(status: number, body: unknown): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  })
}

describe('Client.create', () => {
  it('requires api key and endpoint', () => {
    expect(() => Client.create({ endpoint: '', apiKey: '' })).toThrow(/API key/)
    expect(() => Client.create({ endpoint: '', apiKey: 'k' })).toThrow(new RegExp(EnvEndpoint))
    expect(() => Client.create({ endpoint: 'not-a-url', apiKey: 'k' })).toThrow(/invalid endpoint/)
  })

  it('accepts absolute URLs', () => {
    const c = Client.create({ endpoint: 'https://cellar.example.com/', apiKey: 'k' })
    expect(c).toBeInstanceOf(Client)
  })
})

describe('Client HTTP API', () => {
  it('sends auth headers and maps CRUD onto Sandbox instances', async () => {
    const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input)
      const headers = new Headers(init?.headers)
      expect(headers.get('Authorization')).toBe('Bearer cellar_secret')
      expect(headers.get('X-Api-Key')).toBe('cellar_secret')
      if (url.endsWith('/v1/sandboxes') && init?.method === 'POST') {
        return jsonResponse(200, {
          id: 'sb1',
          desiredState: 'running',
          status: { phase: 'pending' },
        })
      }
      if (url.endsWith('/v1/sandboxes/sb1') && init?.method === 'GET') {
        return jsonResponse(200, {
          id: 'sb1',
          desiredState: 'running',
          status: { phase: 'running' },
        })
      }
      if (url.endsWith('/v1/sandboxes') && (!init?.method || init.method === 'GET')) {
        return jsonResponse(200, {
          sandboxes: [{ id: 'sb1', desiredState: 'running', status: { phase: 'running' } }],
        })
      }
      if (url.endsWith('/stop')) {
        return jsonResponse(200, {
          id: 'sb1',
          desiredState: 'stopped',
          status: { phase: 'stopped' },
        })
      }
      if (url.endsWith('/network')) {
        return jsonResponse(200, {
          id: 'sb1',
          desiredState: 'running',
          status: { phase: 'running' },
        })
      }
      if (url.endsWith('/v1/sandboxes/sb1') && init?.method === 'DELETE') {
        return new Response(null, { status: 204 })
      }
      return new Response('missing', { status: 404 })
    })

    const c = Client.create({
      endpoint: 'https://gw.example',
      apiKey: 'cellar_secret',
      fetch: fetchMock as unknown as typeof fetch,
    })

    const created = await c.create({ spec: { image: 'alpine:3.20' } })
    expect(created).toBeInstanceOf(Sandbox)
    expect(created.id).toBe('sb1')
    expect(created.status?.phase).toBe('pending')

    const got = await c.get('sb1')
    expect(got).toBeInstanceOf(Sandbox)
    expect(got.id).toBe('sb1')

    const listed = await c.list()
    expect(listed.map((s) => s.id)).toEqual(['sb1'])
    expect(listed[0]).toBeInstanceOf(Sandbox)

    const stopped = await created.stop()
    expect(stopped.desiredState).toBe('stopped')

    const networked = await created.updateNetwork({ mode: 'none' })
    expect(networked.id).toBe('sb1')

    await created.remove()
  })

  it('maps API errors', async () => {
    const fetchMock = vi.fn(async () => jsonResponse(404, { error: 'missing', code: 'not_found' }))
    const c = Client.create({
      endpoint: 'https://gw.example',
      apiKey: 'k',
      fetch: fetchMock as unknown as typeof fetch,
    })
    await expect(c.get('x')).rejects.toMatchObject({
      name: 'APIError',
      status: 404,
      code: 'not_found',
    } satisfies Partial<APIError>)
  })

  it('Sandbox.exec collects JSON output', async () => {
    const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input)
      if (url.endsWith('/v1/sandboxes') && init?.method === 'POST') {
        return jsonResponse(200, {
          id: 'sb1',
          desiredState: 'running',
          status: { phase: 'running' },
        })
      }
      const body = JSON.parse(String(init?.body)) as { command: string[] }
      expect(body.command).toEqual(['echo', 'hi'])
      return jsonResponse(200, { stdout: 'hi\n', stderr: '', exitCode: 0 })
    })
    const c = Client.create({
      endpoint: 'https://gw.example',
      apiKey: 'k',
      fetch: fetchMock as unknown as typeof fetch,
    })
    const sb = await c.create({ spec: { image: 'alpine:3.20' } })
    const res = await sb.exec(['echo', 'hi'])
    expect(res.stdout.toString()).toBe('hi\n')
    expect(res.exitCode).toBe(0)
  })

  it('Sandbox.logs streams NDJSON', async () => {
    const line1 = JSON.stringify({ data: Buffer.from('a\n').toString('base64') })
    const line2 = JSON.stringify({ data: Buffer.from('b\n').toString('base64') })
    const stream = new ReadableStream({
      start(controller) {
        controller.enqueue(new TextEncoder().encode(`${line1}\n${line2}\n`))
        controller.close()
      },
    })
    const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input)
      if (url.endsWith('/v1/sandboxes') && init?.method === 'POST') {
        return jsonResponse(200, {
          id: 'sb1',
          desiredState: 'running',
          status: { phase: 'running' },
        })
      }
      return new Response(stream, {
        status: 200,
        headers: { 'Content-Type': 'application/x-ndjson' },
      })
    })
    const c = Client.create({
      endpoint: 'https://gw.example',
      apiKey: 'k',
      fetch: fetchMock as unknown as typeof fetch,
    })
    const sb = await c.create({ spec: { image: 'alpine:3.20' } })
    const chunks: string[] = []
    for await (const ch of sb.logs({ tail: 10 })) {
      chunks.push(ch.data.toString())
    }
    expect(chunks.join('')).toBe('a\nb\n')
  })

  it('fromEnv reads CELLAR_ENDPOINT', () => {
    const prevKey = process.env[EnvAPIKey]
    const prevEp = process.env[EnvEndpoint]
    process.env[EnvAPIKey] = 'cellar_x'
    process.env[EnvEndpoint] = 'https://gw.example'
    try {
      const c = Client.fromEnv(async () => new Response('{}'))
      expect(c).toBeInstanceOf(Client)
    } finally {
      if (prevKey === undefined) delete process.env[EnvAPIKey]
      else process.env[EnvAPIKey] = prevKey
      if (prevEp === undefined) delete process.env[EnvEndpoint]
      else process.env[EnvEndpoint] = prevEp
    }
  })
})

describe('Sandbox readiness', () => {
  it('getStatus refreshes from the gateway', async () => {
    let calls = 0
    const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input)
      if (url.endsWith('/v1/sandboxes') && init?.method === 'POST') {
        return jsonResponse(200, {
          id: 'sb1',
          desiredState: 'running',
          status: { phase: 'pending', message: '' },
        })
      }
      calls++
      return jsonResponse(200, {
        id: 'sb1',
        desiredState: 'running',
        status: { phase: 'running', message: 'up', containerId: 'c1' },
      })
    })
    const c = Client.create({
      endpoint: 'https://gw.example',
      apiKey: 'k',
      fetch: fetchMock as unknown as typeof fetch,
    })
    const sb = await c.create({ spec: { image: 'alpine:3.20' } })
    expect(sb.status?.phase).toBe('pending')
    const status = await sb.getStatus()
    expect(status?.phase).toBe('running')
    expect(sb.status?.phase).toBe('running')
    expect(sb.status?.containerId).toBe('c1')
    expect(calls).toBe(1)
  })

  it('waitUntilReady polls until running', async () => {
    const phases = ['pending', 'starting', 'failed', 'running']
    let getCount = 0
    const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input)
      if (url.endsWith('/v1/sandboxes') && init?.method === 'POST') {
        return jsonResponse(200, {
          id: 'sb1',
          desiredState: 'running',
          status: { phase: 'pending' },
        })
      }
      const phase = phases[Math.min(getCount, phases.length - 1)]
      getCount++
      return jsonResponse(200, {
        id: 'sb1',
        desiredState: 'running',
        status: { phase, message: phase === 'failed' ? 'retrying' : '' },
      })
    })
    const c = Client.create({
      endpoint: 'https://gw.example',
      apiKey: 'k',
      fetch: fetchMock as unknown as typeof fetch,
    })
    const sb = await c.create({ spec: { image: 'alpine:3.20' } })
    await sb.waitUntilReady({ timeoutMs: 5_000, pollIntervalMs: 1 })
    expect(sb.status?.phase).toBe('running')
    expect(getCount).toBe(4)
  })

  it('waitUntilReady times out', async () => {
    const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input)
      if (url.endsWith('/v1/sandboxes') && init?.method === 'POST') {
        return jsonResponse(200, {
          id: 'sb1',
          desiredState: 'running',
          status: { phase: 'pending' },
        })
      }
      return jsonResponse(200, {
        id: 'sb1',
        desiredState: 'running',
        status: { phase: 'starting', message: 'booting' },
      })
    })
    const c = Client.create({
      endpoint: 'https://gw.example',
      apiKey: 'k',
      fetch: fetchMock as unknown as typeof fetch,
    })
    const sb = await c.create({ spec: { image: 'alpine:3.20' } })
    await expect(sb.waitUntilReady({ timeoutMs: 20, pollIntervalMs: 5 })).rejects.toThrow(
      /not ready within 20ms/,
    )
  })

  it('waitUntilReady rejects when desiredState is stopped', async () => {
    const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input)
      if (url.endsWith('/v1/sandboxes') && init?.method === 'POST') {
        return jsonResponse(200, {
          id: 'sb1',
          desiredState: 'running',
          status: { phase: 'pending' },
        })
      }
      return jsonResponse(200, {
        id: 'sb1',
        desiredState: 'stopped',
        status: { phase: 'pending', message: 'stopping' },
      })
    })
    const c = Client.create({
      endpoint: 'https://gw.example',
      apiKey: 'k',
      fetch: fetchMock as unknown as typeof fetch,
    })
    const sb = await c.create({ spec: { image: 'alpine:3.20' } })
    await expect(sb.waitUntilReady({ timeoutMs: 1_000, pollIntervalMs: 1 })).rejects.toThrow(
      /desiredState=stopped/,
    )
  })

  it('waitUntilReady rejects when phase is stopped', async () => {
    const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input)
      if (url.endsWith('/v1/sandboxes') && init?.method === 'POST') {
        return jsonResponse(200, {
          id: 'sb1',
          desiredState: 'running',
          status: { phase: 'pending' },
        })
      }
      return jsonResponse(200, {
        id: 'sb1',
        desiredState: 'running',
        status: { phase: 'stopped', message: 'exited' },
      })
    })
    const c = Client.create({
      endpoint: 'https://gw.example',
      apiKey: 'k',
      fetch: fetchMock as unknown as typeof fetch,
    })
    const sb = await c.create({ spec: { image: 'alpine:3.20' } })
    await expect(sb.waitUntilReady({ timeoutMs: 1_000, pollIntervalMs: 1 })).rejects.toThrow(
      /is stopped/,
    )
  })

  it('waitUntilReady respects AbortSignal', async () => {
    const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input)
      if (url.endsWith('/v1/sandboxes') && init?.method === 'POST') {
        return jsonResponse(200, {
          id: 'sb1',
          desiredState: 'running',
          status: { phase: 'pending' },
        })
      }
      return jsonResponse(200, {
        id: 'sb1',
        desiredState: 'running',
        status: { phase: 'starting' },
      })
    })
    const c = Client.create({
      endpoint: 'https://gw.example',
      apiKey: 'k',
      fetch: fetchMock as unknown as typeof fetch,
    })
    const sb = await c.create({ spec: { image: 'alpine:3.20' } })
    const ac = new AbortController()
    ac.abort(new Error('cancelled'))
    await expect(
      sb.waitUntilReady({ timeoutMs: 5_000, pollIntervalMs: 100, signal: ac.signal }),
    ).rejects.toThrow(/cancelled/)
  })
})
