'use client'

import { Globe, KeyRound, Laptop, Shield } from 'lucide-react'
import Link from 'next/link'
import { useEffect, useRef, useState } from 'react'
import { cn } from '@/lib/cn'

type Step = {
  host: string
  command: string
  output: string[]
  /** Stage of the request-path diagram after this step completes */
  stage: number
}

const SCRIPT: Step[] = [
  {
    host: 'manager-a',
    command: 'cellar status',
    output: [
      'initialized: true',
      'node_id:     1a2b3c4d',
      'role:        manager',
      'cluster_id:  7f3a2c91',
      'is_leader:   true',
      'advertise:   192.0.2.10:17946',
      'listen:      :17946',
      'raft:        :17947',
    ],
    stage: 1,
  },
  {
    host: 'manager-a',
    command: 'cellar api-key create --name app',
    output: [
      'API key created: a1b2c3d4 (app)',
      '',
      'Store this secret now; it will not be shown again:',
      '',
      '    cellar_7f3a2c91ab01cd23ef45…',
      '',
      'Export for the Go client:',
      '',
      '    export CELLAR_API_KEY=cellar_7f3a2c91ab01cd23ef45…',
    ],
    stage: 2,
  },
  {
    host: 'manager-a',
    command: 'cellar-gateway --listen :8080 --data-dir /var/lib/cellar',
    output: ['cellar-gateway listening on :8080'],
    stage: 3,
  },
  {
    host: 'laptop',
    command: 'curl -s http://192.0.2.10:8080/healthz',
    output: ['{"status":"ok"}'],
    stage: 4,
  },
  {
    host: 'laptop',
    command: 'curl -s http://192.0.2.10:8080/readyz',
    output: ['{"status":"ready"}'],
    stage: 4,
  },
  {
    host: 'laptop',
    command:
      'curl -s -H "Authorization: Bearer cellar_7f3a2c91ab01cd23ef45…" http://192.0.2.10:8080/v1/sandboxes',
    output: ['{"data":[]}'],
    stage: 5,
  },
]

const TYPE_MS = 22
const AFTER_COMMAND_MS = 320
const AFTER_OUTPUT_MS = 1100
const LOOP_HOLD_MS = 3200

function fullTranscript() {
  return SCRIPT.flatMap((step, i) => [
    { kind: 'host' as const, text: step.host, key: `h-${i}` },
    { kind: 'command' as const, text: step.command, key: `c-${i}` },
    ...step.output.map((line, j) => ({
      kind: 'output' as const,
      text: line,
      key: `o-${i}-${j}`,
    })),
  ])
}

function PathNode({
  name,
  addr,
  on,
  icon: Icon,
  subtitle,
  footer,
  footerActive,
}: {
  name: string
  addr: string
  on: boolean
  icon: typeof Shield
  subtitle: string
  footer: string
  footerActive?: boolean
}) {
  return (
    <div
      className={cn(
        'min-w-0 rounded-lg border p-3 transition-all duration-500',
        on
          ? 'border-emerald-500/40 bg-emerald-500/5 shadow-[0_0_0_1px_rgba(16,185,129,0.08)]'
          : 'border-dashed border-fd-border opacity-45',
      )}
    >
      <div className="mb-1.5 flex items-center justify-between gap-2">
        <span className="font-mono text-[11px] text-fd-muted-foreground">{addr}</span>
        <span
          className={cn(
            'size-2 rounded-full transition-colors',
            on ? 'bg-emerald-500' : 'bg-fd-muted-foreground/40',
          )}
        />
      </div>
      <div className="flex min-w-0 items-center gap-2">
        <span
          className={cn(
            'inline-flex size-6 shrink-0 items-center justify-center rounded-md transition-colors',
            on
              ? 'bg-emerald-500/15 text-emerald-600 dark:text-emerald-400'
              : 'bg-fd-muted text-fd-muted-foreground',
          )}
        >
          <Icon className="size-3.5" aria-hidden />
        </span>
        <p className="truncate text-sm font-semibold">{name}</p>
      </div>
      <p className="mt-0.5 text-xs text-pretty text-fd-muted-foreground">{subtitle}</p>
      <div
        className={cn(
          'mt-2.5 rounded-md px-2 py-1.5 font-mono text-[11px] break-words transition-all duration-500',
          footerActive
            ? 'bg-amber-500/15 text-amber-700 dark:text-amber-300'
            : 'bg-fd-background/80 text-fd-muted-foreground',
        )}
      >
        {footer}
      </div>
    </div>
  )
}

function PathArrow({ label, on }: { label: string; on: boolean }) {
  return (
    <div className="flex items-center gap-2 py-1.5 pl-2" aria-hidden="true">
      <span
        className={cn(
          'h-6 w-px transition-colors',
          on ? 'bg-emerald-500/50' : 'bg-fd-border',
        )}
      />
      <span
        className={cn(
          'font-mono text-[11px] transition-colors',
          on ? 'text-emerald-700 dark:text-emerald-400' : 'text-fd-muted-foreground',
        )}
      >
        {label}
      </span>
    </div>
  )
}

function RequestPath({ stage }: { stage: number }) {
  const leaderOn = stage >= 1
  const keyOn = stage >= 2
  const gatewayOn = stage >= 3
  const readyOn = stage >= 4
  const requestOn = stage >= 5

  return (
    <div
      className="min-w-0 overflow-hidden rounded-xl border border-fd-border bg-fd-card p-4 shadow-sm sm:p-5"
      aria-hidden="true"
    >
      <div className="mb-4 flex min-w-0 items-center justify-between gap-3">
        <p className="text-xs font-medium tracking-wide text-fd-muted-foreground uppercase">
          Request path
        </p>
        <span
          className={cn(
            'shrink-0 rounded-full px-2 py-0.5 font-mono text-[11px] transition-colors',
            requestOn
              ? 'bg-emerald-500/15 text-emerald-600 dark:text-emerald-400'
              : readyOn
                ? 'bg-sky-500/15 text-sky-700 dark:text-sky-400'
                : 'bg-fd-muted text-fd-muted-foreground',
          )}
        >
          {requestOn ? 'authenticated' : readyOn ? 'ready' : gatewayOn ? 'listening' : 'offline'}
        </span>
      </div>

      <PathNode
        name="App / SDK"
        addr="laptop"
        icon={Laptop}
        on={requestOn}
        subtitle={requestOn ? 'cloud backend · Bearer cellar_…' : 'waiting for a key'}
        footer={requestOn ? 'GET /v1/sandboxes' : 'no request yet'}
        footerActive={requestOn}
      />
      <PathArrow label="HTTPS + API key" on={requestOn} />
      <PathNode
        name="cellar-gateway"
        addr=":8080"
        icon={Globe}
        on={gatewayOn}
        subtitle={
          readyOn
            ? 'HTTP JSON · /healthz · /readyz'
            : gatewayOn
              ? 'listening · loading cluster CA'
              : 'waiting to start'
        }
        footer={readyOn ? 'status: ready' : gatewayOn ? 'status: ok' : 'not listening'}
        footerActive={readyOn}
      />
      <PathArrow label="mTLS + API key" on={gatewayOn && leaderOn} />
      <PathNode
        name="cellard manager"
        addr="192.0.2.10:17946"
        icon={Shield}
        on={leaderOn}
        subtitle={leaderOn ? 'Raft leader · SandboxAPI' : 'waiting for status'}
        footer={keyOn ? 'api-key app · cellar_7f3a…' : 'no API keys yet'}
        footerActive={keyOn}
      />

      <p className="mt-4 text-xs leading-relaxed text-fd-muted-foreground">
        The gateway is the public HTTP surface. Terminate TLS at a proxy or ALB — the process itself
        speaks HTTP.
      </p>
    </div>
  )
}

function GatewayTerminal({
  stepIndex,
  typed,
  showOutput,
  reducedMotion,
  onPauseChange,
}: {
  stepIndex: number
  typed: number
  showOutput: boolean
  reducedMotion: boolean
  onPauseChange: (paused: boolean) => void
}) {
  const preRef = useRef<HTMLPreElement>(null)
  const lines = reducedMotion
    ? fullTranscript()
    : [
        ...SCRIPT.slice(0, stepIndex).flatMap((step, i) => [
          { kind: 'host' as const, text: step.host, key: `h-${i}` },
          { kind: 'command' as const, text: step.command, key: `c-${i}` },
          ...step.output.map((line, j) => ({
            kind: 'output' as const,
            text: line,
            key: `o-${i}-${j}`,
          })),
        ]),
        {
          kind: 'host' as const,
          text: SCRIPT[stepIndex].host,
          key: `h-${stepIndex}`,
        },
        {
          kind: 'typing' as const,
          text: SCRIPT[stepIndex].command.slice(0, typed),
          key: `t-${stepIndex}`,
        },
        ...(showOutput
          ? SCRIPT[stepIndex].output.map((line, j) => ({
              kind: 'output' as const,
              text: line,
              key: `o-${stepIndex}-${j}`,
            }))
          : []),
      ]

  useEffect(() => {
    const el = preRef.current
    if (!el) return
    el.scrollTop = el.scrollHeight
  }, [stepIndex, typed, showOutput, reducedMotion])

  return (
    <div
      className="min-w-0 overflow-hidden rounded-xl border border-neutral-200 bg-neutral-50 text-left shadow-sm dark:border-neutral-800 dark:bg-neutral-950 dark:shadow-lg"
      onMouseEnter={() => onPauseChange(true)}
      onMouseLeave={() => onPauseChange(false)}
      aria-label="Animated demo of creating a Cellar API key and starting cellar-gateway"
    >
      <div className="flex items-center gap-2 border-b border-neutral-200 px-4 py-2.5 dark:border-neutral-800">
        <span className="size-2.5 rounded-full bg-red-400/80" aria-hidden />
        <span className="size-2.5 rounded-full bg-amber-400/80" aria-hidden />
        <span className="size-2.5 rounded-full bg-emerald-400/80" aria-hidden />
        <span className="ml-2 font-mono text-xs text-neutral-500 dark:text-neutral-400">
          cellar · gateway
        </span>
      </div>
      <pre
        ref={preRef}
        className="h-[18rem] overflow-x-auto overflow-y-auto p-3 font-mono text-[12px] leading-relaxed break-all whitespace-pre-wrap text-neutral-800 sm:h-[22rem] sm:p-4 sm:text-[13px] sm:break-words md:h-[24rem] md:text-sm dark:text-neutral-100"
      >
        {lines.map((line) => {
          if (line.kind === 'host') {
            return (
              <div key={line.key} className="mt-2 text-sky-700 first:mt-0 dark:text-sky-400/90">
                # {line.text}
              </div>
            )
          }
          if (line.kind === 'output') {
            return (
              <div key={line.key} className="text-neutral-500 dark:text-neutral-400">
                {line.text || '\u00a0'}
              </div>
            )
          }
          return (
            <div key={line.key}>
              <span className="text-emerald-600 dark:text-emerald-400">$</span>{' '}
              <span>{line.text}</span>
              {line.kind === 'typing' && !reducedMotion ? (
                <span className="ml-px inline-block w-1.5 animate-pulse bg-emerald-600 align-[-2px] dark:bg-emerald-300">
                  &nbsp;
                </span>
              ) : null}
            </div>
          )
        })}
      </pre>
    </div>
  )
}

export function GatewayShowcase() {
  const [reducedMotion, setReducedMotion] = useState(false)
  const [stepIndex, setStepIndex] = useState(0)
  const [typed, setTyped] = useState(0)
  const [showOutput, setShowOutput] = useState(false)
  const pausedRef = useRef(false)
  const mountedRef = useRef(false)

  useEffect(() => {
    mountedRef.current = true
    const mq = window.matchMedia('(prefers-reduced-motion: reduce)')
    const apply = () => setReducedMotion(mq.matches)
    apply()
    mq.addEventListener('change', apply)
    return () => {
      mountedRef.current = false
      mq.removeEventListener('change', apply)
    }
  }, [])

  useEffect(() => {
    if (reducedMotion) return

    const step = SCRIPT[stepIndex]
    let timer: number

    const schedule = (fn: () => void, ms: number) => {
      timer = window.setTimeout(() => {
        if (!mountedRef.current) return
        if (pausedRef.current) {
          schedule(fn, 120)
          return
        }
        fn()
      }, ms)
    }

    if (!showOutput) {
      if (typed < step.command.length) {
        schedule(() => setTyped((n) => n + 1), TYPE_MS)
      } else {
        schedule(() => setShowOutput(true), AFTER_COMMAND_MS)
      }
    } else if (stepIndex < SCRIPT.length - 1) {
      schedule(() => {
        setStepIndex((i) => i + 1)
        setTyped(0)
        setShowOutput(false)
      }, AFTER_OUTPUT_MS)
    } else {
      schedule(() => {
        setStepIndex(0)
        setTyped(0)
        setShowOutput(false)
      }, LOOP_HOLD_MS)
    }

    return () => window.clearTimeout(timer)
  }, [reducedMotion, stepIndex, typed, showOutput])

  const stage = reducedMotion
    ? 5
    : showOutput
      ? SCRIPT[stepIndex].stage
      : stepIndex === 0
        ? 0
        : SCRIPT[stepIndex - 1].stage

  return (
    <section className="border-t border-fd-border px-4 py-12 sm:px-6 sm:py-16 lg:py-20">
      <div className="mx-auto w-full max-w-(--fd-layout-width) min-w-0">
        <div className="mb-8 max-w-3xl sm:mb-10">
          <p className="mb-2 text-sm font-medium text-fd-muted-foreground">Gateway</p>
          <h2 className="mb-4 text-xl font-bold tracking-tight text-balance sm:text-2xl lg:text-3xl">
            Open the HTTP front door. Mint a key.
          </h2>
          <p className="text-pretty text-fd-muted-foreground">
            Apps talk to <code className="font-mono text-[13px]">cellar-gateway</code> over HTTP, not
            the unix-socket CLI. Create a key on the Raft leader with{' '}
            <code className="font-mono text-[13px]">cellar api-key create</code>. The gateway loads
            the cluster CA from <code className="font-mono text-[13px]">--data-dir</code>, dials
            manager <code className="font-mono text-[13px]">SandboxAPI</code>, and authenticates
            callers with{' '}
            <code className="font-mono text-[13px]">Authorization: Bearer cellar_…</code>.
          </p>
        </div>

        <div className="grid grid-cols-1 items-start gap-6 sm:gap-8 lg:grid-cols-[minmax(0,1.15fr)_minmax(0,0.85fr)] lg:gap-10">
          <GatewayTerminal
            stepIndex={stepIndex}
            typed={typed}
            showOutput={showOutput}
            reducedMotion={reducedMotion}
            onPauseChange={(paused) => {
              pausedRef.current = paused
            }}
          />
          <div className="min-w-0 space-y-4">
            <RequestPath stage={stage} />
            <ol className="space-y-3 text-sm">
              <li className="flex gap-3">
                <span className="mt-0.5 font-mono text-fd-muted-foreground">1</span>
                <span>
                  Confirm the leader with <code className="font-mono text-[13px]">cellar status</code>
                  , then mint a key with{' '}
                  <code className="font-mono text-[13px]">api-key create</code>
                </span>
              </li>
              <li className="flex gap-3">
                <span className="mt-0.5 font-mono text-fd-muted-foreground">2</span>
                <span>
                  Start <code className="font-mono text-[13px]">cellar-gateway</code> on{' '}
                  <code className="font-mono text-[13px]">:8080</code> — it loads the cluster CA and
                  reaches a manager
                </span>
              </li>
              <li className="flex gap-3">
                <span className="mt-0.5 font-mono text-fd-muted-foreground">3</span>
                <span>
                  Probe <code className="font-mono text-[13px]">/healthz</code> and{' '}
                  <code className="font-mono text-[13px]">/readyz</code>, then call the HTTP API with{' '}
                  <code className="font-mono text-[13px]">Bearer cellar_…</code>
                </span>
              </li>
            </ol>
          </div>
        </div>

        <div className="mt-8 flex flex-col gap-2 sm:flex-row sm:flex-wrap sm:gap-x-5 sm:gap-y-2">
          <Link
            href="/docs/api/gateway"
            className="inline-flex items-center gap-1.5 text-sm font-medium text-fd-primary underline-offset-4 hover:underline"
          >
            <Globe className="size-3.5" aria-hidden />
            Run the gateway →
          </Link>
          <Link
            href="/docs/api/api-keys"
            className="inline-flex items-center gap-1.5 text-sm font-medium text-fd-primary underline-offset-4 hover:underline"
          >
            <KeyRound className="size-3.5" aria-hidden />
            API keys →
          </Link>
        </div>
      </div>
    </section>
  )
}
