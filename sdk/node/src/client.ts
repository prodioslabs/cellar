import {
  ChannelCredentials,
  Metadata,
  status as grpcStatus,
  type ServiceError,
} from '@grpc/grpc-js'
import { EnvAPIKey, EnvCACert, EnvEndpoints, resolveCACert } from './cacert.js'
import {
  SandboxAPIClient,
  SandboxCreateRequest,
  SandboxUpdateNetworkRequest,
  type Sandbox,
  type SandboxCreateResponse,
  type SandboxGetResponse,
  type SandboxListResponse,
  type SandboxRemoveResponse,
  type SandboxStopResponse,
  type SandboxUpdateNetworkResponse,
} from './gen/sandbox.js'

/** Nested partial request shape (proto zero-value semantics). */
export type DeepPartial<T> = {
  [P in keyof T]?: T[P] extends (infer U)[]
    ? DeepPartial<U>[]
    : T[P] extends object | undefined
      ? DeepPartial<NonNullable<T[P]>>
      : T[P]
}

export const TLSServerName = 'cellar-manager'

const defaultMaxAttempts = 3
const unhealthyForMs = 5_000

export interface Config {
  /** Manager gRPC addresses (host:port). Required unless using fromEnv. */
  endpoints: string[]
  /** cellar_… secret. Required. */
  apiKey: string
  /** Cluster CA PEM used to verify managers. */
  caCert?: Buffer | Uint8Array | string
  /** Loads caCert from disk when caCert is empty. */
  caCertFile?: string
  /** Caps failover tries per RPC (default 3). */
  maxAttempts?: number
}

export interface ExecResult {
  stdout: Buffer
  stderr: Buffer
  exitCode: number
  error: string
}

/** @internal Exported for tests. */
export function splitEndpoints(s: string): string[] {
  return s
    .split(',')
    .map((p) => p.trim())
    .filter((p) => p !== '')
}

/** @internal Exported for tests. */
export function isRetryable(err: unknown): boolean {
  if (err == null) {
    return false
  }
  if (typeof err === 'object' && err !== null && 'code' in err) {
    const code = (err as ServiceError).code
    switch (code) {
      case grpcStatus.UNAVAILABLE:
      case grpcStatus.DEADLINE_EXCEEDED:
      case grpcStatus.RESOURCE_EXHAUSTED:
        return true
      default:
        // Non-gRPC or other status: only retry dial/transport-like failures.
        // ServiceError always has a code; unknown transport may lack one.
        if (typeof code === 'number') {
          return false
        }
    }
  }
  // Dial / transport errors without a gRPC status.
  return true
}

/** @internal Exported for tests. */
export function pickEndpointOrder(
  endpoints: string[],
  lastOK: string,
  badUntil: Map<string, number>,
  rrIndex: number,
  now = Date.now(),
): string[] {
  const n = endpoints.length
  const order: string[] = []

  if (lastOK !== '') {
    const until = badUntil.get(lastOK)
    if (until === undefined || now >= until) {
      order.push(lastOK)
    }
  }

  const start = ((rrIndex % n) + n) % n
  for (let i = 0; i < n; i++) {
    const addr = endpoints[(start + i) % n]!
    if (addr === lastOK) {
      continue
    }
    const until = badUntil.get(addr)
    if (until !== undefined && now < until) {
      continue
    }
    order.push(addr)
  }

  if (order.length === 0) {
    return [...endpoints]
  }
  return order
}

function unaryCall<Req, Res>(
  fn: (
    request: Req,
    metadata: Metadata,
    callback: (error: ServiceError | null, response: Res) => void,
  ) => unknown,
  request: Req,
  metadata: Metadata,
): Promise<Res> {
  return new Promise((resolve, reject) => {
    fn(request, metadata, (err, res) => {
      if (err) {
        reject(err)
        return
      }
      resolve(res)
    })
  })
}

/**
 * Client talks to SandboxAPI with multi-endpoint failover.
 * Authenticate with CELLAR_API_KEY (or Config.apiKey). Dial one or more manager
 * gRPC addresses via CELLAR_ENDPOINTS / Config.endpoints.
 */
export class Client {
  private readonly endpoints: string[]
  private readonly apiKey: string
  private readonly maxAttempts: number
  private readonly credentials: ChannelCredentials
  private rr = 0
  private lastOK = ''
  private readonly badUntil = new Map<string, number>()

  private constructor(
    endpoints: string[],
    apiKey: string,
    maxAttempts: number,
    credentials: ChannelCredentials,
  ) {
    this.endpoints = endpoints
    this.apiKey = apiKey
    this.maxAttempts = maxAttempts
    this.credentials = credentials
  }

  /** Builds a client from CELLAR_* environment variables. */
  static fromEnv(): Client {
    return Client.create({
      endpoints: splitEndpoints(process.env[EnvEndpoints] ?? ''),
      apiKey: process.env[EnvAPIKey] ?? '',
    })
  }

  /** Creates a Client. */
  static create(cfg: Config): Client {
    if (!cfg.apiKey) {
      throw new Error(`API key is required (set ${EnvAPIKey})`)
    }
    if (!cfg.endpoints || cfg.endpoints.length === 0) {
      throw new Error(`at least one endpoint is required (set ${EnvEndpoints})`)
    }

    const endpoints = cfg.endpoints.map((e) => e.trim()).filter((e) => e !== '')
    if (endpoints.length === 0) {
      throw new Error(`at least one endpoint is required (set ${EnvEndpoints})`)
    }

    let caPEM: Buffer | undefined
    if (cfg.caCert != null) {
      caPEM = typeof cfg.caCert === 'string' ? Buffer.from(cfg.caCert) : Buffer.from(cfg.caCert)
    }
    if ((!caPEM || caPEM.length === 0) && cfg.caCertFile) {
      caPEM = resolveCACert(cfg.caCertFile)
    }
    if ((!caPEM || caPEM.length === 0) && process.env[EnvCACert]) {
      caPEM = resolveCACert(process.env[EnvCACert]!)
    }
    if (!caPEM || caPEM.length === 0) {
      throw new Error(`CA certificate is required (set ${EnvCACert} or Config.caCert)`)
    }
    if (!caPEM.toString('utf8').includes('BEGIN CERTIFICATE')) {
      throw new Error('failed to parse CA certificate PEM')
    }

    let maxAttempts = cfg.maxAttempts ?? defaultMaxAttempts
    if (maxAttempts <= 0) {
      maxAttempts = defaultMaxAttempts
    }
    if (maxAttempts > endpoints.length * 2) {
      maxAttempts = endpoints.length * 2
    }

    const credentials = ChannelCredentials.createSsl(caPEM)
    return new Client(endpoints, cfg.apiKey, maxAttempts, credentials)
  }

  private authMetadata(): Metadata {
    const md = new Metadata()
    md.set('authorization', `Bearer ${this.apiKey}`)
    md.set('x-api-key', this.apiKey)
    return md
  }

  private markBad(addr: string): void {
    this.badUntil.set(addr, Date.now() + unhealthyForMs)
    if (this.lastOK === addr) {
      this.lastOK = ''
    }
  }

  private markOK(addr: string): void {
    this.lastOK = addr
    this.badUntil.delete(addr)
  }

  private pickOrder(): string[] {
    const rrIndex = this.rr++
    return pickEndpointOrder(this.endpoints, this.lastOK, this.badUntil, rrIndex)
  }

  private dial(addr: string): SandboxAPIClient {
    return new SandboxAPIClient(addr, this.credentials, {
      'grpc.ssl_target_name_override': TLSServerName,
      'grpc.default_authority': TLSServerName,
    })
  }

  private async withConn<T>(
    fn: (api: SandboxAPIClient, metadata: Metadata) => Promise<T>,
  ): Promise<T> {
    const metadata = this.authMetadata()
    let lastErr: unknown
    const order = this.pickOrder()
    let attempts = 0

    for (const addr of order) {
      if (attempts >= this.maxAttempts) {
        break
      }
      attempts++

      let api: SandboxAPIClient
      try {
        api = this.dial(addr)
      } catch (err) {
        this.markBad(addr)
        lastErr = err
        continue
      }

      try {
        const result = await fn(api, metadata)
        this.markOK(addr)
        api.close()
        return result
      } catch (err) {
        api.close()
        lastErr = err
        if (isRetryable(err)) {
          this.markBad(addr)
          continue
        }
        throw err
      }
    }

    if (lastErr == null) {
      throw new Error('no endpoints available')
    }
    throw lastErr
  }

  /** Creates a sandbox. */
  async create(req: DeepPartial<SandboxCreateRequest>): Promise<Sandbox> {
    const full = SandboxCreateRequest.fromPartial(req)
    return this.withConn(async (api, md) => {
      const resp = await unaryCall<typeof full, SandboxCreateResponse>(
        (r, m, cb) => api.create(r, m, cb),
        full,
        md,
      )
      if (!resp.sandbox) {
        throw new Error('Create returned empty sandbox')
      }
      return resp.sandbox
    })
  }

  /** Stops a sandbox. */
  async stop(id: string): Promise<Sandbox> {
    return this.withConn(async (api, md) => {
      const resp = await unaryCall<{ sandboxId: string }, SandboxStopResponse>(
        (r, m, cb) => api.stop(r, m, cb),
        { sandboxId: id },
        md,
      )
      if (!resp.sandbox) {
        throw new Error('Stop returned empty sandbox')
      }
      return resp.sandbox
    })
  }

  /** Deletes a sandbox. */
  async remove(id: string): Promise<void> {
    await this.withConn(async (api, md) => {
      await unaryCall<{ sandboxId: string }, SandboxRemoveResponse>(
        (r, m, cb) => api.remove(r, m, cb),
        { sandboxId: id },
        md,
      )
    })
  }

  /** Returns a sandbox. */
  async get(id: string): Promise<Sandbox> {
    return this.withConn(async (api, md) => {
      const resp = await unaryCall<{ sandboxId: string }, SandboxGetResponse>(
        (r, m, cb) => api.get(r, m, cb),
        { sandboxId: id },
        md,
      )
      if (!resp.sandbox) {
        throw new Error('Get returned empty sandbox')
      }
      return resp.sandbox
    })
  }

  /** Returns all sandboxes. */
  async list(): Promise<Sandbox[]> {
    return this.withConn(async (api, md) => {
      const resp = await unaryCall<Record<string, never>, SandboxListResponse>(
        (r, m, cb) => api.list(r, m, cb),
        {},
        md,
      )
      return resp.sandboxes ?? []
    })
  }

  /** Replaces a sandbox network policy. */
  async updateNetwork(req: DeepPartial<SandboxUpdateNetworkRequest>): Promise<Sandbox> {
    const full = SandboxUpdateNetworkRequest.fromPartial(req)
    return this.withConn(async (api, md) => {
      const resp = await unaryCall<typeof full, SandboxUpdateNetworkResponse>(
        (r, m, cb) => api.updateNetwork(r, m, cb),
        full,
        md,
      )
      if (!resp.sandbox) {
        throw new Error('UpdateNetwork returned empty sandbox')
      }
      return resp.sandbox
    })
  }

  /** Runs a command in a sandbox and collects output until exit. */
  async exec(sandboxId: string, command: string[]): Promise<ExecResult> {
    return this.withConn(async (api, md) => {
      const stream = api.exec(md)
      const result: ExecResult = {
        stdout: Buffer.alloc(0),
        stderr: Buffer.alloc(0),
        exitCode: 0,
        error: '',
      }

      const done = new Promise<ExecResult>((resolve, reject) => {
        stream.on(
          'data',
          (msg: {
            stdout?: Buffer
            stderr?: Buffer
            exit?: { exitCode: number; error: string }
          }) => {
            if (msg.stdout) {
              result.stdout = Buffer.concat([result.stdout, msg.stdout])
            }
            if (msg.stderr) {
              result.stderr = Buffer.concat([result.stderr, msg.stderr])
            }
            if (msg.exit) {
              result.exitCode = msg.exit.exitCode
              result.error = msg.exit.error
              resolve(result)
            }
          },
        )
        stream.on('error', reject)
        stream.on('end', () => resolve(result))
      })

      stream.write({
        start: {
          sandboxId,
          command,
          tty: false,
          stdin: false,
        },
      })
      stream.end()

      return done
    })
  }
}
