import { status as grpcStatus } from '@grpc/grpc-js'
import { describe, expect, it } from 'vitest'
import { isRetryable, pickEndpointOrder, splitEndpoints } from './client.js'

describe('splitEndpoints', () => {
  it('splits and trims comma-separated addresses', () => {
    expect(splitEndpoints('a:1, b:2 , ,c:3')).toEqual(['a:1', 'b:2', 'c:3'])
  })

  it('returns empty for blank', () => {
    expect(splitEndpoints('')).toEqual([])
    expect(splitEndpoints('  , , ')).toEqual([])
  })
})

describe('isRetryable', () => {
  it('retries unavailable / deadline / resource exhausted', () => {
    expect(isRetryable({ code: grpcStatus.UNAVAILABLE })).toBe(true)
    expect(isRetryable({ code: grpcStatus.DEADLINE_EXCEEDED })).toBe(true)
    expect(isRetryable({ code: grpcStatus.RESOURCE_EXHAUSTED })).toBe(true)
  })

  it('does not retry permission denied / not found', () => {
    expect(isRetryable({ code: grpcStatus.PERMISSION_DENIED })).toBe(false)
    expect(isRetryable({ code: grpcStatus.NOT_FOUND })).toBe(false)
    expect(isRetryable({ code: grpcStatus.INVALID_ARGUMENT })).toBe(false)
  })

  it('retries dial/transport errors without a gRPC code', () => {
    expect(isRetryable(new Error('connect ECONNREFUSED'))).toBe(true)
  })

  it('returns false for null', () => {
    expect(isRetryable(null)).toBe(false)
  })
})

describe('pickEndpointOrder', () => {
  const endpoints = ['a:1', 'b:2', 'c:3']

  it('prefers lastOK when healthy', () => {
    const order = pickEndpointOrder(endpoints, 'b:2', new Map(), 0)
    expect(order[0]).toBe('b:2')
    expect(order).toContain('a:1')
    expect(order).toContain('c:3')
    expect(order).toHaveLength(3)
  })

  it('skips endpoints marked bad', () => {
    const badUntil = new Map<string, number>([['a:1', Date.now() + 60_000]])
    const order = pickEndpointOrder(endpoints, '', badUntil, 0)
    expect(order).not.toContain('a:1')
    expect(order).toEqual(['b:2', 'c:3'])
  })

  it('falls back to all endpoints when everything is bad', () => {
    const now = 1_000
    const badUntil = new Map<string, number>([
      ['a:1', now + 60_000],
      ['b:2', now + 60_000],
      ['c:3', now + 60_000],
    ])
    const order = pickEndpointOrder(endpoints, '', badUntil, 0, now)
    expect(order).toEqual(endpoints)
  })

  it('round-robins starting index', () => {
    const order0 = pickEndpointOrder(endpoints, '', new Map(), 0)
    const order1 = pickEndpointOrder(endpoints, '', new Map(), 1)
    expect(order0[0]).toBe('a:1')
    expect(order1[0]).toBe('b:2')
  })
})
