/**
 * Client talks to the Cellar HTTP gateway.
 * Authenticate with CELLAR_API_KEY (or Config.apiKey). Point CELLAR_ENDPOINT /
 * Config.endpoint at the gateway base URL.
 */
import type {
  Sandbox,
  SandboxCreateRequest,
  SandboxUpdateNetworkRequest,
} from './types.js'

/** Nested partial request shape. */
export type DeepPartial<T> = {
  [P in keyof T]?: T[P] extends (infer U)[]
    ? DeepPartial<U>[]
    : T[P] extends object | undefined
      ? DeepPartial<NonNullable<T[P]>>
      : T[P]
}

export const EnvAPIKey = 'CELLAR_API_KEY'
export const EnvEndpoint = 'CELLAR_ENDPOINT'

export interface Config {
  /** Gateway base URL (e.g. https://cellar.example.com). Required. */
  endpoint: string
  /** cellar_… secret. Required. */
  apiKey: string
  /** Optional fetch implementation (defaults to global fetch). */
  fetch?: typeof fetch
}

export interface ExecResult {
  stdout: Buffer
  stderr: Buffer
  exitCode: number
  error: string
}

export interface LogsOptions {
  follow?: boolean
  tail?: number
  timestamps?: boolean
}

export interface LogsChunk {
  data: Buffer
}

export class APIError extends Error {
  readonly status: number
  readonly code: string

  constructor(status: number, message: string, code = '') {
    super(code ? `${message} (${code})` : `${message} (HTTP ${status})`)
    this.name = 'APIError'
    this.status = status
    this.code = code
  }
}

function normalizeEndpoint(endpoint: string): string {
  const trimmed = endpoint.trim().replace(/\/+$/, '')
  let u: URL
  try {
    u = new URL(trimmed)
  } catch {
    throw new Error(
      `invalid endpoint ${JSON.stringify(endpoint)}: need absolute URL with scheme and host`,
    )
  }
  if (!u.protocol || !u.host) {
    throw new Error(
      `invalid endpoint ${JSON.stringify(endpoint)}: need absolute URL with scheme and host`,
    )
  }
  return trimmed
}

export class Client {
  private readonly endpoint: string
  private readonly apiKey: string
  private readonly fetchImpl: typeof fetch

  private constructor(endpoint: string, apiKey: string, fetchImpl: typeof fetch) {
    this.endpoint = endpoint
    this.apiKey = apiKey
    this.fetchImpl = fetchImpl
  }

  /** Builds a client from CELLAR_* environment variables. */
  static fromEnv(fetchImpl: typeof fetch = fetch): Client {
    return Client.create({
      endpoint: process.env[EnvEndpoint] ?? '',
      apiKey: process.env[EnvAPIKey] ?? '',
      fetch: fetchImpl,
    })
  }

  static create(cfg: Config): Client {
    if (!cfg.apiKey) {
      throw new Error(`API key is required (set ${EnvAPIKey})`)
    }
    if (!cfg.endpoint) {
      throw new Error(`endpoint is required (set ${EnvEndpoint})`)
    }
    return new Client(normalizeEndpoint(cfg.endpoint), cfg.apiKey, cfg.fetch ?? fetch)
  }

  private authHeaders(extra?: Record<string, string>): Headers {
    const h = new Headers(extra)
    h.set('Authorization', `Bearer ${this.apiKey}`)
    h.set('X-Api-Key', this.apiKey)
    return h
  }

  private async parseError(res: Response): Promise<never> {
    let message = res.statusText
    let code = ''
    try {
      const body = (await res.json()) as { error?: string; code?: string }
      if (body.error) message = body.error
      if (body.code) code = body.code
    } catch {
      // ignore
    }
    throw new APIError(res.status, message, code)
  }

  private async requestJSON(method: string, path: string, body?: unknown): Promise<unknown> {
    const headers = this.authHeaders({ Accept: 'application/json' })
    let payload: string | undefined
    if (body !== undefined) {
      headers.set('Content-Type', 'application/json')
      payload = JSON.stringify(body)
    }
    const res = await this.fetchImpl(`${this.endpoint}${path}`, {
      method,
      headers,
      body: payload,
    })
    if (res.status === 204) {
      return undefined
    }
    if (!res.ok) {
      await this.parseError(res)
    }
    if (method === 'DELETE') {
      return undefined
    }
    const text = await res.text()
    if (!text) {
      return undefined
    }
    return JSON.parse(text)
  }

  /** Creates a sandbox. */
  async create(req: DeepPartial<SandboxCreateRequest>): Promise<Sandbox> {
    const out = await this.requestJSON('POST', '/v1/sandboxes', req)
    return out as Sandbox
  }

  /** Stops a sandbox. */
  async stop(id: string): Promise<Sandbox> {
    const out = await this.requestJSON('POST', `/v1/sandboxes/${encodeURIComponent(id)}/stop`)
    return out as Sandbox
  }

  /** Deletes a sandbox. */
  async remove(id: string): Promise<void> {
    await this.requestJSON('DELETE', `/v1/sandboxes/${encodeURIComponent(id)}`)
  }

  /** Returns a sandbox. */
  async get(id: string): Promise<Sandbox> {
    const out = await this.requestJSON('GET', `/v1/sandboxes/${encodeURIComponent(id)}`)
    return out as Sandbox
  }

  /** Returns all sandboxes. */
  async list(): Promise<Sandbox[]> {
    const out = (await this.requestJSON('GET', '/v1/sandboxes')) as
      | { sandboxes?: Sandbox[] }
      | undefined
    return out?.sandboxes ?? []
  }

  /** Replaces a sandbox network policy. */
  async updateNetwork(req: DeepPartial<SandboxUpdateNetworkRequest>): Promise<Sandbox> {
    if (!req.sandboxId) {
      throw new Error('sandboxId is required')
    }
    const out = await this.requestJSON(
      'PUT',
      `/v1/sandboxes/${encodeURIComponent(req.sandboxId)}/network`,
      req,
    )
    return out as Sandbox
  }

  /** Streams sandbox logs as NDJSON chunks. */
  async *logs(sandboxId: string, opt: LogsOptions = {}): AsyncGenerator<LogsChunk> {
    const q = new URLSearchParams()
    if (opt.follow) q.set('follow', 'true')
    if (opt.timestamps) q.set('timestamps', 'true')
    if (opt.tail != null && opt.tail !== 0) q.set('tail', String(opt.tail))
    const qs = q.toString()
    const path = `/v1/sandboxes/${encodeURIComponent(sandboxId)}/logs${qs ? `?${qs}` : ''}`
    const res = await this.fetchImpl(`${this.endpoint}${path}`, {
      method: 'GET',
      headers: this.authHeaders({ Accept: 'application/x-ndjson' }),
    })
    if (!res.ok) {
      await this.parseError(res)
    }
    if (!res.body) {
      return
    }
    const reader = res.body.getReader()
    const decoder = new TextDecoder()
    let buf = ''
    for (;;) {
      const { done, value } = await reader.read()
      if (done) break
      buf += decoder.decode(value, { stream: true })
      let idx: number
      while ((idx = buf.indexOf('\n')) >= 0) {
        const line = buf.slice(0, idx).trim()
        buf = buf.slice(idx + 1)
        if (!line) continue
        const row = JSON.parse(line) as { data?: string }
        yield { data: Buffer.from(row.data ?? '', 'base64') }
      }
    }
    const rest = buf.trim()
    if (rest) {
      const row = JSON.parse(rest) as { data?: string }
      yield { data: Buffer.from(row.data ?? '', 'base64') }
    }
  }

  /** Runs a command in a sandbox and collects output until exit. */
  async exec(sandboxId: string, command: string[]): Promise<ExecResult> {
    const out = (await this.requestJSON(
      'POST',
      `/v1/sandboxes/${encodeURIComponent(sandboxId)}/exec`,
      {
        command,
      },
    )) as {
      stdout?: string
      stderr?: string
      exitCode?: number
      error?: string
    }
    return {
      stdout: Buffer.from(out?.stdout ?? '', 'utf8'),
      stderr: Buffer.from(out?.stderr ?? '', 'utf8'),
      exitCode: out?.exitCode ?? 0,
      error: out?.error ?? '',
    }
  }
}
