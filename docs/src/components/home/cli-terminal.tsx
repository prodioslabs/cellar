'use client'

import { useEffect, useRef, useState } from 'react'

type Step = {
  command: string
  output: string[]
}

const SCRIPT: Step[] = [
  {
    command: 'cellar init --advertise-addr 127.0.0.1:17946',
    output: ['Cluster initialized: 7f3a2c91 (node 1a2b3c4d)'],
  },
  {
    command: 'cellar sandbox create --id demo --runtime node-26',
    output: ['sandbox demo created (node=1a2b3c4d phase=pending)'],
  },
  {
    command: `cellar sandbox exec demo -- node -e 'console.log("hello from cellar")'`,
    output: ['hello from cellar'],
  },
]

const TYPE_MS = 28
const AFTER_COMMAND_MS = 380
const AFTER_OUTPUT_MS = 900
const LOOP_HOLD_MS = 2200

function fullTranscript() {
  return SCRIPT.flatMap((step, i) => [
    { kind: 'command' as const, text: step.command, key: `c-${i}` },
    ...step.output.map((line, j) => ({
      kind: 'output' as const,
      text: line,
      key: `o-${i}-${j}`,
    })),
  ])
}

export function CliTerminal() {
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

  const lines = reducedMotion
    ? fullTranscript()
    : [
        ...SCRIPT.slice(0, stepIndex).flatMap((step, i) => [
          { kind: 'command' as const, text: step.command, key: `c-${i}` },
          ...step.output.map((line, j) => ({
            kind: 'output' as const,
            text: line,
            key: `o-${i}-${j}`,
          })),
        ]),
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

  return (
    <div
      className="overflow-hidden rounded-xl border border-neutral-800 bg-neutral-950 text-left shadow-lg"
      onMouseEnter={() => {
        pausedRef.current = true
      }}
      onMouseLeave={() => {
        pausedRef.current = false
      }}
      role="img"
      aria-label="Animated demo of initializing Cellar and creating a sandbox from the CLI"
    >
      <div className="flex items-center gap-2 border-b border-neutral-800 px-4 py-2.5">
        <span className="size-2.5 rounded-full bg-red-400/80" aria-hidden />
        <span className="size-2.5 rounded-full bg-amber-400/80" aria-hidden />
        <span className="size-2.5 rounded-full bg-emerald-400/80" aria-hidden />
        <span className="ml-2 font-mono text-xs text-neutral-400">cellar</span>
      </div>
      <pre className="min-h-68 overflow-x-hidden p-4 font-mono text-[13px] leading-relaxed wrap-break-word whitespace-pre-wrap text-neutral-100 sm:min-h-72 sm:text-sm">
        {lines.map((line) =>
          line.kind === 'output' ? (
            <div key={line.key} className="text-neutral-400">
              {line.text || '\u00a0'}
            </div>
          ) : (
            <div key={line.key}>
              <span className="text-emerald-400">$</span> <span>{line.text}</span>
              {line.kind === 'typing' && !reducedMotion ? (
                <span className="ml-px inline-block w-1.5 animate-pulse bg-emerald-300 align-[-2px]">
                  &nbsp;
                </span>
              ) : null}
            </div>
          ),
        )}
      </pre>
    </div>
  )
}

export function CliShowcase() {
  return (
    <section className="border-t border-fd-border px-6 py-16 sm:py-20">
      <div className="mx-auto grid w-full max-w-5xl items-center gap-10 lg:grid-cols-[minmax(0,0.9fr)_minmax(0,1.1fr)] lg:gap-14">
        <div>
          <p className="mb-2 text-sm font-medium text-fd-muted-foreground">CLI</p>
          <h2 className="mb-4 text-2xl font-bold tracking-tight sm:text-3xl">
            From zero to a running sandbox
          </h2>
          <p className="mb-6 text-fd-muted-foreground">
            Initialize a cluster, create a hardened container with a language runtime, and exec
            inside it. Three commands — no YAML required.
          </p>
          <ol className="space-y-3 text-sm">
            <li className="flex gap-3">
              <span className="mt-0.5 font-mono text-fd-muted-foreground">1</span>
              <span>
                <code className="font-mono text-[13px]">cellar init</code> stands up the control
                plane
              </span>
            </li>
            <li className="flex gap-3">
              <span className="mt-0.5 font-mono text-fd-muted-foreground">2</span>
              <span>
                <code className="font-mono text-[13px]">sandbox create</code> schedules a container
                with <code className="font-mono text-[13px]">--runtime node-26</code>
              </span>
            </li>
            <li className="flex gap-3">
              <span className="mt-0.5 font-mono text-fd-muted-foreground">3</span>
              <span>
                <code className="font-mono text-[13px]">sandbox exec</code> runs your command in
                isolation
              </span>
            </li>
          </ol>
        </div>
        <CliTerminal />
      </div>
    </section>
  )
}
