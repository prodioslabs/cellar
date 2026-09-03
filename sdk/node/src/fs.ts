import type { Client } from './client.js'

export type FsEntryKind = 'file' | 'directory' | 'symlink' | 'other'

export interface FsEntry {
  path: string
  kind: FsEntryKind
  size: number
  mode: number
  modified: Date | null
}

export interface FsMetadata {
  kind: FsEntryKind
  size: number
  mode: number
  readonly: boolean
  modified: Date | null
  created: Date | null
}

function parseTime(v: unknown): Date | null {
  if (v == null || v === '') return null
  if (typeof v === 'string') {
    const d = new Date(v)
    return Number.isNaN(d.getTime()) ? null : d
  }
  return null
}

function queryPath(sandboxId: string, op: string, path: string): string {
  const q = new URLSearchParams({ path })
  return `/v1/sandboxes/${encodeURIComponent(sandboxId)}/fs/${op}?${q}`
}

/**
 * Streaming reader for a guest file. Implements AsyncIterable and AsyncDisposable.
 */
export class FsReadStream implements AsyncIterable<Uint8Array>, AsyncDisposable {
  private readonly reader: ReadableStreamDefaultReader<Uint8Array>
  private closed = false

  /** @internal */
  constructor(body: ReadableStream<Uint8Array>) {
    this.reader = body.getReader()
  }

  /** Receive the next chunk, or null at EOF. */
  async recv(): Promise<Uint8Array | null> {
    if (this.closed) return null
    const { done, value } = await this.reader.read()
    if (done) {
      this.closed = true
      return null
    }
    return value ?? new Uint8Array()
  }

  /** Drain the stream into a single buffer. */
  async collect(): Promise<Uint8Array> {
    const parts: Uint8Array[] = []
    let total = 0
    for (;;) {
      const chunk = await this.recv()
      if (chunk == null) break
      parts.push(chunk)
      total += chunk.byteLength
    }
    const out = new Uint8Array(total)
    let offset = 0
    for (const p of parts) {
      out.set(p, offset)
      offset += p.byteLength
    }
    return out
  }

  async *[Symbol.asyncIterator](): AsyncIterator<Uint8Array> {
    for (;;) {
      const chunk = await this.recv()
      if (chunk == null) return
      yield chunk
    }
  }

  async [Symbol.asyncDispose](): Promise<void> {
    if (this.closed) return
    this.closed = true
    try {
      await this.reader.cancel()
    } catch {
      // ignore
    }
  }
}

/**
 * Streaming writer for a guest file. Implements AsyncDisposable.
 */
export class FsWriteSink implements AsyncDisposable {
  private readonly writer: WritableStreamDefaultWriter<Uint8Array>
  private readonly done: Promise<void>
  private closed = false

  /** @internal */
  constructor(writer: WritableStreamDefaultWriter<Uint8Array>, done: Promise<void>) {
    this.writer = writer
    this.done = done
  }

  /** Append a chunk. Strings are encoded as UTF-8. */
  async write(data: Uint8Array | string): Promise<void> {
    if (this.closed) throw new Error('write stream is closed')
    const chunk = typeof data === 'string' ? new TextEncoder().encode(data) : data
    await this.writer.write(chunk)
  }

  /** Flush and close. Idempotent. */
  async close(): Promise<void> {
    if (this.closed) return
    this.closed = true
    await this.writer.close()
    await this.done
  }

  async [Symbol.asyncDispose](): Promise<void> {
    await this.close()
  }
}

/** In-sandbox filesystem operations for a single sandbox. */
export class SandboxFs {
  private readonly client: Client
  private readonly sandboxId: () => string

  /** @internal */
  constructor(client: Client, sandboxId: () => string) {
    this.client = client
    this.sandboxId = sandboxId
  }

  /** Read the entire contents of a file as raw bytes. */
  async read(path: string): Promise<Uint8Array> {
    const stream = await this.readStream(path)
    try {
      return await stream.collect()
    } finally {
      await stream[Symbol.asyncDispose]()
    }
  }

  /** Read the entire contents of a file and decode as UTF-8. */
  async readToString(path: string): Promise<string> {
    const bytes = await this.read(path)
    return new TextDecoder().decode(bytes)
  }

  /** Write content to a file (create or overwrite). Parents must exist. */
  async write(path: string, data: Uint8Array | string): Promise<void> {
    const body = typeof data === 'string' ? new TextEncoder().encode(data) : data
    await this.client.fsPutContent(this.sandboxId(), path, body)
  }

  /** Open a streaming reader for a file. */
  async readStream(path: string): Promise<FsReadStream> {
    const body = await this.client.fsGetContentStream(this.sandboxId(), path)
    return new FsReadStream(body)
  }

  /**
   * Open a streaming writer for a file.
   * Prefer `await using sink = await fs.writeStream(path)`.
   */
  async writeStream(path: string): Promise<FsWriteSink> {
    return this.client.fsPutContentStream(this.sandboxId(), path)
  }

  /** List directory entries. */
  async list(path: string): Promise<FsEntry[]> {
    const out = (await this.client.fsJSON(
      'GET',
      queryPath(this.sandboxId(), 'list', path),
    )) as { entries?: Array<Record<string, unknown>> }
    return (out?.entries ?? []).map((e) => ({
      path: String(e.path ?? ''),
      kind: (e.kind as FsEntryKind) ?? 'other',
      size: Number(e.size ?? 0),
      mode: Number(e.mode ?? 0),
      modified: parseTime(e.modified),
    }))
  }

  /** Create a directory. Parents must exist. */
  async mkdir(path: string): Promise<void> {
    await this.client.fsJSON('POST', `/v1/sandboxes/${encodeURIComponent(this.sandboxId())}/fs/mkdir`, {
      path,
    })
  }

  /** Remove an empty directory. */
  async removeDir(path: string): Promise<void> {
    await this.client.fsJSON(
      'POST',
      `/v1/sandboxes/${encodeURIComponent(this.sandboxId())}/fs/remove-dir`,
      { path },
    )
  }

  /** Remove a file. */
  async remove(path: string): Promise<void> {
    await this.client.fsJSON('POST', `/v1/sandboxes/${encodeURIComponent(this.sandboxId())}/fs/remove`, {
      path,
    })
  }

  /** Get file or directory metadata. */
  async stat(path: string): Promise<FsMetadata> {
    const e = (await this.client.fsJSON(
      'GET',
      queryPath(this.sandboxId(), 'stat', path),
    )) as Record<string, unknown>
    return {
      kind: (e.kind as FsEntryKind) ?? 'other',
      size: Number(e.size ?? 0),
      mode: Number(e.mode ?? 0),
      readonly: Boolean(e.readonly),
      modified: parseTime(e.modified),
      created: parseTime(e.created),
    }
  }

  /** Check whether a path exists. */
  async exists(path: string): Promise<boolean> {
    const out = (await this.client.fsJSON(
      'GET',
      queryPath(this.sandboxId(), 'exists', path),
    )) as { exists?: boolean }
    return Boolean(out?.exists)
  }

  /** Copy a file within the sandbox. */
  async copy(from: string, to: string): Promise<void> {
    await this.client.fsJSON('POST', `/v1/sandboxes/${encodeURIComponent(this.sandboxId())}/fs/copy`, {
      from,
      to,
    })
  }

  /** Rename or move a path within the sandbox. */
  async rename(from: string, to: string): Promise<void> {
    await this.client.fsJSON('POST', `/v1/sandboxes/${encodeURIComponent(this.sandboxId())}/fs/rename`, {
      from,
      to,
    })
  }
}
