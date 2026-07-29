import { describe, expect, it, vi } from 'vitest'
import { APIError, Client, EnvAPIKey, EnvEndpoint } from './client.js'

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
  it('sends auth headers and maps CRUD', async () => {
    const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input)
      const headers = new Headers(init?.headers)
      expect(headers.get('Authorization')).toBe('Bearer cellar_secret')
      expect(headers.get('X-Api-Key')).toBe('cellar_secret')
      if (url.endsWith('/v1/sandboxes') && init?.method === 'POST') {
        return jsonResponse(200, { id: 'sb1' })
      }
      if (url.endsWith('/v1/sandboxes/sb1') && init?.method === 'GET') {
        return jsonResponse(200, { id: 'sb1' })
      }
      if (url.endsWith('/v1/sandboxes') && (!init?.method || init.method === 'GET')) {
        return jsonResponse(200, { sandboxes: [{ id: 'sb1' }] })
      }
      if (url.endsWith('/stop')) {
        return jsonResponse(200, { id: 'sb1' })
      }
      if (url.endsWith('/network')) {
        return jsonResponse(200, { id: 'sb1' })
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
    expect(created.id).toBe('sb1')
    expect((await c.get('sb1')).id).toBe('sb1')
    expect((await c.list()).map((s) => s.id)).toEqual(['sb1'])
    expect((await c.stop('sb1')).id).toBe('sb1')
    expect((await c.updateNetwork({ sandboxId: 'sb1', network: { mode: 'none' } })).id).toBe('sb1')
    await c.remove('sb1')
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

  it('exec collects JSON output', async () => {
    const fetchMock = vi.fn(async (_input: RequestInfo | URL, init?: RequestInit) => {
      const body = JSON.parse(String(init?.body)) as { command: string[] }
      expect(body.command).toEqual(['echo', 'hi'])
      return jsonResponse(200, { stdout: 'hi\n', stderr: '', exitCode: 0 })
    })
    const c = Client.create({
      endpoint: 'https://gw.example',
      apiKey: 'k',
      fetch: fetchMock as unknown as typeof fetch,
    })
    const res = await c.exec('sb1', ['echo', 'hi'])
    expect(res.stdout.toString()).toBe('hi\n')
    expect(res.exitCode).toBe(0)
  })

  it('logs streams NDJSON', async () => {
    const line1 = JSON.stringify({ data: Buffer.from('a\n').toString('base64') })
    const line2 = JSON.stringify({ data: Buffer.from('b\n').toString('base64') })
    const stream = new ReadableStream({
      start(controller) {
        controller.enqueue(new TextEncoder().encode(`${line1}\n${line2}\n`))
        controller.close()
      },
    })
    const fetchMock = vi.fn(async () => new Response(stream, {
      status: 200,
      headers: { 'Content-Type': 'application/x-ndjson' },
    }))
    const c = Client.create({
      endpoint: 'https://gw.example',
      apiKey: 'k',
      fetch: fetchMock as unknown as typeof fetch,
    })
    const chunks: string[] = []
    for await (const ch of c.logs('sb1', { tail: 10 })) {
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
