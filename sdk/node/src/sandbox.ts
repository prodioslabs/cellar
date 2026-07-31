import type { Client, DeepPartial, ExecResult, JobInfo, LogsChunk, LogsOptions } from './client.js'
import type { NetworkPolicy, SandboxSnapshot, SandboxSpec, SandboxStatus } from './types.js'

export interface WaitUntilReadyOptions {
  /** Max time to wait before rejecting. Defaults to 60_000. */
  timeoutMs?: number
  /** Delay between status polls. Defaults to 500. */
  pollIntervalMs?: number
  /** Optional abort signal to cancel waiting. */
  signal?: AbortSignal
}

const DEFAULT_TIMEOUT_MS = 60_000
const DEFAULT_POLL_INTERVAL_MS = 500

function sleep(ms: number, signal?: AbortSignal): Promise<void> {
  return new Promise((resolve, reject) => {
    if (signal?.aborted) {
      reject(signal.reason instanceof Error ? signal.reason : new Error('aborted'))
      return
    }
    const timer = setTimeout(() => {
      signal?.removeEventListener('abort', onAbort)
      resolve()
    }, ms)
    const onAbort = () => {
      clearTimeout(timer)
      reject(signal?.reason instanceof Error ? signal.reason : new Error('aborted'))
    }
    signal?.addEventListener('abort', onAbort, { once: true })
  })
}

/**
 * Operational sandbox handle returned by {@link Client.create}, {@link Client.get},
 * and {@link Client.list}. Creation is asynchronous — call {@link waitUntilReady}
 * before {@link exec} when you need the container to be running.
 */
export class Sandbox {
  private readonly client: Client
  private data: SandboxSnapshot

  /** @internal */
  constructor(client: Client, data: SandboxSnapshot) {
    this.client = client
    this.data = data
  }

  get id(): string {
    return this.data.id
  }

  get spec(): SandboxSpec | undefined {
    return this.data.spec
  }

  get nodeId(): string {
    return this.data.nodeId
  }

  get desiredState(): string {
    return this.data.desiredState
  }

  get status(): SandboxStatus | undefined {
    return this.data.status
  }

  get createdAtUnixNano(): number {
    return this.data.createdAtUnixNano
  }

  get updatedAtUnixNano(): number {
    return this.data.updatedAtUnixNano
  }

  /** Latest gateway snapshot for this sandbox. */
  snapshot(): SandboxSnapshot {
    return { ...this.data, status: this.data.status ? { ...this.data.status } : undefined }
  }

  /** Refreshes from `GET /v1/sandboxes/:id` and returns the status. */
  async getStatus(): Promise<SandboxStatus | undefined> {
    this.data = await this.client.fetchSnapshot(this.id)
    return this.data.status
  }

  /**
   * Polls the gateway until `status.phase === "running"`.
   * Continues through `pending`, `starting`, and retryable `failed` while
   * `desiredState` remains `running`. Rejects on timeout, abort, or a
   * non-running desired/terminal state.
   */
  async waitUntilReady(options: WaitUntilReadyOptions = {}): Promise<this> {
    const timeoutMs = options.timeoutMs ?? DEFAULT_TIMEOUT_MS
    const pollIntervalMs = options.pollIntervalMs ?? DEFAULT_POLL_INTERVAL_MS
    const signal = options.signal
    const deadline = Date.now() + timeoutMs

    for (;;) {
      if (signal?.aborted) {
        throw signal.reason instanceof Error ? signal.reason : new Error('aborted')
      }

      const status = await this.getStatus()
      const phase = status?.phase ?? ''
      const desired = this.data.desiredState

      if (phase === 'running') {
        return this
      }

      if (desired === 'stopped' || desired === 'removed') {
        throw new Error(
          `sandbox ${this.id} will not become ready: desiredState=${desired}` +
            statusMessage(status),
        )
      }

      if (phase === 'stopped') {
        throw new Error(`sandbox ${this.id} is stopped` + statusMessage(status))
      }

      // pending, starting, failed (retryable while desired=running), or unknown
      const remaining = deadline - Date.now()
      if (remaining <= 0) {
        throw new Error(
          `sandbox ${this.id} not ready within ${timeoutMs}ms (phase=${phase || 'unknown'})` +
            statusMessage(status),
        )
      }

      await sleep(Math.min(pollIntervalMs, remaining), signal)
    }
  }

  /** Runs a command in this sandbox and collects output until exit. */
  async exec(command: string[]): Promise<ExecResult> {
    return this.client.execCommand(this.id, command)
  }

  /** Starts a background job and returns its id. */
  async startJob(command: string[]): Promise<string> {
    return this.client.startJob(this.id, command)
  }

  /** Lists background jobs. */
  async listJobs(): Promise<JobInfo[]> {
    return this.client.listJobs(this.id)
  }

  /** Gets a background job. */
  async getJob(jobId: string): Promise<JobInfo> {
    return this.client.getJob(this.id, jobId)
  }

  /** Stops a background job. */
  async stopJob(jobId: string): Promise<void> {
    await this.client.stopJob(this.id, jobId)
  }

  /** Streams sandbox logs as NDJSON chunks. */
  logs(opt: LogsOptions = {}): AsyncGenerator<LogsChunk> {
    return this.client.streamLogs(this.id, opt)
  }

  /** Stops this sandbox. */
  async stop(): Promise<this> {
    this.data = await this.client.stopSandbox(this.id)
    return this
  }

  /** Deletes this sandbox. */
  async remove(): Promise<void> {
    await this.client.removeSandbox(this.id)
  }

  /** Replaces this sandbox's network policy. */
  async updateNetwork(network: DeepPartial<NetworkPolicy> | undefined): Promise<this> {
    this.data = await this.client.updateSandboxNetwork(this.id, network)
    return this
  }
}

function statusMessage(status: SandboxStatus | undefined): string {
  if (!status?.message) return ''
  return `: ${status.message}`
}
