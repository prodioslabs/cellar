import { readFileSync, statSync } from 'node:fs'

export const EnvAPIKey = 'CELLAR_API_KEY'
export const EnvEndpoints = 'CELLAR_ENDPOINTS'
export const EnvCACert = 'CELLAR_CA_CERT'

/** Returns a single .env line: CELLAR_CA_CERT="-----BEGIN…\\n…\\n-----END…\\n" */
export function formatCACertEnv(pem: Uint8Array | string): string {
  const text = typeof pem === 'string' ? pem : new TextDecoder().decode(pem)
  return `${EnvCACert}="${escapePEMForEnv(text)}"`
}

function escapePEMForEnv(pem: string): string {
  let s = pem.replace(/\r\n/g, '\n').replace(/\r/g, '\n')
  s = s.replace(/\\/g, '\\\\')
  s = s.replace(/"/g, '\\"')
  s = s.replace(/\n/g, '\\n')
  return s
}

function unescapePEMFromEnv(s: string): string {
  let out = ''
  for (let i = 0; i < s.length; i++) {
    if (s[i] === '\\' && i + 1 < s.length) {
      switch (s[i + 1]) {
        case 'n':
          out += '\n'
          i++
          continue
        case '\\':
          out += '\\'
          i++
          continue
        case '"':
          out += '"'
          i++
          continue
      }
    }
    out += s[i]
  }
  return out
}

/**
 * Turns a CELLAR_CA_CERT-style value into PEM bytes.
 * Accepts: raw PEM, \\n-escaped PEM, filesystem path, or std base64 of PEM.
 */
export function resolveCACert(raw: string): Buffer {
  if (raw.trim() === '') {
    throw new Error('empty CA certificate value')
  }

  const unescaped = unescapePEMFromEnv(raw)
  if (unescaped.includes('BEGIN CERTIFICATE')) {
    return Buffer.from(unescaped)
  }

  const trimmed = raw.trim()

  try {
    const st = statSync(trimmed)
    if (!st.isDirectory()) {
      const b = readFileSync(trimmed)
      if (!b.toString('utf8').includes('BEGIN CERTIFICATE')) {
        throw new Error('CA cert file does not contain a PEM certificate')
      }
      return b
    }
  } catch (err) {
    if (err instanceof Error && err.message === 'CA cert file does not contain a PEM certificate') {
      throw err
    }
    // Not a readable file — fall through to base64.
  }

  try {
    const decoded = Buffer.from(trimmed, 'base64')
    if (decoded.toString('utf8').includes('BEGIN CERTIFICATE')) {
      return decoded
    }
  } catch {
    // ignore
  }

  throw new Error('CA certificate must be PEM, \\n-escaped PEM, a file path, or base64')
}
