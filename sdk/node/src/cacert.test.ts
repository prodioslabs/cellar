import { mkdirSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { describe, expect, it } from 'vitest'
import { EnvCACert, formatCACertEnv, resolveCACert } from './cacert.js'

const samplePEM = `-----BEGIN CERTIFICATE-----
MIIBIjANBgkqhkiG9w0BAQEFAAOCAQ8AMIIBCgKCAQEA0Z3VS5JJcds3xfn/ygWy
-----END CERTIFICATE-----
`

describe('formatCACertEnv / resolveCACert', () => {
  it('round-trips escaped PEM', () => {
    const line = formatCACertEnv(samplePEM)
    expect(line.startsWith(`${EnvCACert}="`)).toBe(true)
    expect(line.includes('\n') && !line.includes('\\n')).toBe(false)

    const val = line.slice(`${EnvCACert}="`.length, -1)
    const got = resolveCACert(val)
    expect(got.toString('utf8')).toBe(samplePEM)
  })

  it('resolves a PEM file path', () => {
    const dir = join(tmpdir(), `cellar-ca-${Date.now()}`)
    mkdirSync(dir, { recursive: true })
    const path = join(dir, 'ca.crt')
    writeFileSync(path, samplePEM)
    const got = resolveCACert(path)
    expect(got.toString('utf8')).toBe(samplePEM)
  })

  it('resolves base64 of PEM', () => {
    const b64 = Buffer.from(samplePEM).toString('base64')
    const got = resolveCACert(b64)
    expect(got.toString('utf8')).toBe(samplePEM)
  })

  it('resolves raw PEM', () => {
    const got = resolveCACert(samplePEM)
    expect(got.toString('utf8')).toBe(samplePEM)
  })

  it('rejects empty input', () => {
    expect(() => resolveCACert('   ')).toThrow(/empty/)
  })
})
