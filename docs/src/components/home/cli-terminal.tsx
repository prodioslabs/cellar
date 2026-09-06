'use client'

import { Box, Server, Shield } from 'lucide-react'
import { type ReactNode, useEffect, useRef, useState } from 'react'
import { cn } from '@/lib/cn'

function AwsIcon({ className }: { className?: string }) {
  return (
    <svg
      xmlns="http://www.w3.org/2000/svg"
      version="1.1"
      id="Layer_1"
      x="0px"
      y="0px"
      viewBox="0 0 304 182"
      className={className}
    >
      <g>
        <path
          d="M86.4,66.4c0,3.7,0.4,6.7,1.1,8.9c0.8,2.2,1.8,4.6,3.2,7.2c0.5,0.8,0.7,1.6,0.7,2.3c0,1-0.6,2-1.9,3l-6.3,4.2   c-0.9,0.6-1.8,0.9-2.6,0.9c-1,0-2-0.5-3-1.4C76.2,90,75,88.4,74,86.8c-1-1.7-2-3.6-3.1-5.9c-7.8,9.2-17.6,13.8-29.4,13.8   c-8.4,0-15.1-2.4-20-7.2c-4.9-4.8-7.4-11.2-7.4-19.2c0-8.5,3-15.4,9.1-20.6c6.1-5.2,14.2-7.8,24.5-7.8c3.4,0,6.9,0.3,10.6,0.8   c3.7,0.5,7.5,1.3,11.5,2.2v-7.3c0-7.6-1.6-12.9-4.7-16c-3.2-3.1-8.6-4.6-16.3-4.6c-3.5,0-7.1,0.4-10.8,1.3c-3.7,0.9-7.3,2-10.8,3.4   c-1.6,0.7-2.8,1.1-3.5,1.3c-0.7,0.2-1.2,0.3-1.6,0.3c-1.4,0-2.1-1-2.1-3.1v-4.9c0-1.6,0.2-2.8,0.7-3.5c0.5-0.7,1.4-1.4,2.8-2.1   c3.5-1.8,7.7-3.3,12.6-4.5c4.9-1.3,10.1-1.9,15.6-1.9c11.9,0,20.6,2.7,26.2,8.1c5.5,5.4,8.3,13.6,8.3,24.6V66.4z M45.8,81.6   c3.3,0,6.7-0.6,10.3-1.8c3.6-1.2,6.8-3.4,9.5-6.4c1.6-1.9,2.8-4,3.4-6.4c0.6-2.4,1-5.3,1-8.7v-4.2c-2.9-0.7-6-1.3-9.2-1.7   c-3.2-0.4-6.3-0.6-9.4-0.6c-6.7,0-11.6,1.3-14.9,4c-3.3,2.7-4.9,6.5-4.9,11.5c0,4.7,1.2,8.2,3.7,10.6   C37.7,80.4,41.2,81.6,45.8,81.6z M126.1,92.4c-1.8,0-3-0.3-3.8-1c-0.8-0.6-1.5-2-2.1-3.9L96.7,10.2c-0.6-2-0.9-3.3-0.9-4   c0-1.6,0.8-2.5,2.4-2.5h9.8c1.9,0,3.2,0.3,3.9,1c0.8,0.6,1.4,2,2,3.9l16.8,66.2l15.6-66.2c0.5-2,1.1-3.3,1.9-3.9c0.8-0.6,2.2-1,4-1   h8c1.9,0,3.2,0.3,4,1c0.8,0.6,1.5,2,1.9,3.9l15.8,67l17.3-67c0.6-2,1.3-3.3,2-3.9c0.8-0.6,2.1-1,3.9-1h9.3c1.6,0,2.5,0.8,2.5,2.5   c0,0.5-0.1,1-0.2,1.6c-0.1,0.6-0.3,1.4-0.7,2.5l-24.1,77.3c-0.6,2-1.3,3.3-2.1,3.9c-0.8,0.6-2.1,1-3.8,1h-8.6c-1.9,0-3.2-0.3-4-1   c-0.8-0.7-1.5-2-1.9-4L156,23l-15.4,64.4c-0.5,2-1.1,3.3-1.9,4c-0.8,0.7-2.2,1-4,1H126.1z M254.6,95.1c-5.2,0-10.4-0.6-15.4-1.8   c-5-1.2-8.9-2.5-11.5-4c-1.6-0.9-2.7-1.9-3.1-2.8c-0.4-0.9-0.6-1.9-0.6-2.8v-5.1c0-2.1,0.8-3.1,2.3-3.1c0.6,0,1.2,0.1,1.8,0.3   c0.6,0.2,1.5,0.6,2.5,1c3.4,1.5,7.1,2.7,11,3.5c4,0.8,7.9,1.2,11.9,1.2c6.3,0,11.2-1.1,14.6-3.3c3.4-2.2,5.2-5.4,5.2-9.5   c0-2.8-0.9-5.1-2.7-7c-1.8-1.9-5.2-3.6-10.1-5.2L246,52c-7.3-2.3-12.7-5.7-16-10.2c-3.3-4.4-5-9.3-5-14.5c0-4.2,0.9-7.9,2.7-11.1   c1.8-3.2,4.2-6,7.2-8.2c3-2.3,6.4-4,10.4-5.2c4-1.2,8.2-1.7,12.6-1.7c2.2,0,4.5,0.1,6.7,0.4c2.3,0.3,4.4,0.7,6.5,1.1   c2,0.5,3.9,1,5.7,1.6c1.8,0.6,3.2,1.2,4.2,1.8c1.4,0.8,2.4,1.6,3,2.5c0.6,0.8,0.9,1.9,0.9,3.3v4.7c0,2.1-0.8,3.2-2.3,3.2   c-0.8,0-2.1-0.4-3.8-1.2c-5.7-2.6-12.1-3.9-19.2-3.9c-5.7,0-10.2,0.9-13.3,2.8c-3.1,1.9-4.7,4.8-4.7,8.9c0,2.8,1,5.2,3,7.1   c2,1.9,5.7,3.8,11,5.5l14.2,4.5c7.2,2.3,12.4,5.5,15.5,9.6c3.1,4.1,4.6,8.8,4.6,14c0,4.3-0.9,8.2-2.6,11.6   c-1.8,3.4-4.2,6.4-7.3,8.8c-3.1,2.5-6.8,4.3-11.1,5.6C264.4,94.4,259.7,95.1,254.6,95.1z"
          fill="currentColor"
        />
        <g>
          <path
            d="M273.5,143.7c-32.9,24.3-80.7,37.2-121.8,37.2c-57.6,0-109.5-21.3-148.7-56.7c-3.1-2.8-0.3-6.6,3.4-4.4    c42.4,24.6,94.7,39.5,148.8,39.5c36.5,0,76.6-7.6,113.5-23.2C274.2,133.6,278.9,139.7,273.5,143.7z"
            fill="#FF9900"
          />
          <path
            d="M287.2,128.1c-4.2-5.4-27.8-2.6-38.5-1.3c-3.2,0.4-3.7-2.4-0.8-4.5c18.8-13.2,49.7-9.4,53.3-5    c3.6,4.5-1,35.4-18.6,50.2c-2.7,2.3-5.3,1.1-4.1-1.9C282.5,155.7,291.4,133.4,287.2,128.1z"
            fill="#FF9900"
          />
        </g>
      </g>
    </svg>
  )
}

function GcpIcon({ className }: { className?: string }) {
  return (
    <svg
      width="33"
      height="27"
      viewBox="0 0 33 27"
      fill="none"
      xmlns="http://www.w3.org/2000/svg"
      className={className}
    >
      <path
        d="M20.8051 7.26592H21.8051L24.6551 4.41592L24.7951 3.20592C23.1624 1.76488 21.1893 0.763836 19.0622 0.297356C16.9351 -0.169124 14.7242 -0.0856401 12.6383 0.539922C10.5525 1.16548 8.66049 2.31247 7.14115 3.87254C5.62181 5.4326 4.52526 7.35424 3.95508 9.45592C4.27258 9.32579 4.62429 9.30468 4.95508 9.39592L10.6551 8.45592C10.6551 8.45592 10.9451 7.97592 11.0951 8.00592C12.3164 6.66459 14.0043 5.84082 15.8131 5.70325C17.6219 5.56568 19.4149 6.12472 20.8251 7.26592H20.8051Z"
        fill="#EA4335"
      />
      <path
        d="M28.715 9.45582C28.0599 7.04345 26.7149 4.87476 24.845 3.21582L20.845 7.21582C21.6786 7.89698 22.3467 8.75856 22.7988 9.73556C23.251 10.7126 23.4753 11.7795 23.455 12.8558V13.5658C23.9225 13.5658 24.3854 13.6579 24.8174 13.8368C25.2493 14.0157 25.6417 14.2779 25.9723 14.6085C26.3029 14.9391 26.5651 15.3315 26.744 15.7635C26.9229 16.1954 27.015 16.6583 27.015 17.1258C27.015 17.5933 26.9229 18.0563 26.744 18.4882C26.5651 18.9201 26.3029 19.3125 25.9723 19.6431C25.6417 19.9737 25.2493 20.2359 24.8174 20.4148C24.3854 20.5937 23.9225 20.6858 23.455 20.6858H16.335L15.625 21.4058V25.6758L16.335 26.3858H23.455C25.4432 26.4013 27.3837 25.7764 28.9892 24.6036C30.5948 23.4308 31.7802 21.7723 32.3701 19.8736C32.96 17.9748 32.9232 15.9366 32.2649 14.0604C31.6066 12.1842 30.362 10.5698 28.715 9.45582Z"
        fill="#4285F4"
      />
      <path
        d="M9.20523 26.3462H16.3252V20.6462H9.20523C8.69797 20.6461 8.19665 20.5369 7.73523 20.3262L6.73523 20.6362L3.86523 23.4862L3.61523 24.4862C5.22467 25.7015 7.18851 26.3549 9.20523 26.3462Z"
        fill="#34A853"
      />
      <path
        d="M9.20469 7.85602C7.27551 7.86754 5.3981 8.48132 3.83468 9.61162C2.27126 10.7419 1.09991 12.3323 0.484221 14.1606C-0.131464 15.989 -0.160733 17.9639 0.400502 19.8097C0.961737 21.6555 2.08545 23.2799 3.61469 24.456L7.74469 20.326C7.21985 20.0889 6.76038 19.728 6.40569 19.2743C6.051 18.8205 5.81169 18.2875 5.70828 17.7209C5.60488 17.1544 5.64047 16.5712 5.81201 16.0214C5.98354 15.4716 6.2859 14.9717 6.69313 14.5645C7.10036 14.1572 7.60032 13.8549 8.1501 13.6833C8.69987 13.5118 9.28306 13.4762 9.84962 13.5796C10.4162 13.683 10.9492 13.9223 11.4029 14.277C11.8567 14.6317 12.2176 15.0912 12.4547 15.616L16.5847 11.486C15.7178 10.3528 14.6006 9.43542 13.3203 8.80569C12.04 8.17596 10.6314 7.85093 9.20469 7.85602Z"
        fill="#FBBC05"
      />
    </svg>
  )
}

type Step = {
  host: string
  command: string
  output: string[]
  /** Stage of the cluster diagram after this step completes */
  stage: number
}

const SCRIPT: Step[] = [
  {
    host: 'manager-a',
    command: 'cellar init --advertise-addr 192.0.2.10:17946',
    output: [
      'Cluster initialized: 7f3a2c91 (node 1a2b3c4d)',
      '',
      'To add a worker to this cluster, run:',
      '    cellar join --token CLLRN-1-wk-… 192.0.2.10:17946',
      '',
      'To add a manager to this cluster, run:',
      '    cellar join --token CLLRN-1-mgr-… 192.0.2.10:17946',
    ],
    stage: 1,
  },
  {
    host: 'manager-a',
    command: 'cellar join-token manager',
    output: [
      'To add a manager to this cluster, run:',
      '    cellar join --token CLLRN-1-mgr-7f3a2c91ab…ef01 192.0.2.10:17946',
    ],
    stage: 1,
  },
  {
    host: 'manager-b',
    command:
      'cellar join --token CLLRN-1-mgr-7f3a2c91ab…ef01 --advertise-addr 192.0.2.11:17946 192.0.2.10:17946',
    output: ['This node joined as a manager (2b3c4d5e).'],
    stage: 2,
  },
  {
    host: 'manager-a',
    command: 'cellar join-token worker',
    output: [
      'To add a worker to this cluster, run:',
      '    cellar join --token CLLRN-1-wk-7f3a2c91ab…ef01 192.0.2.10:17946',
    ],
    stage: 2,
  },
  {
    host: 'worker-c',
    command: 'cellar join --token CLLRN-1-wk-7f3a2c91ab…ef01 192.0.2.10:17946',
    output: ['This node joined as a worker (9e8d7c6b).'],
    stage: 3,
  },
  {
    host: 'worker-d',
    command: 'cellar join --token CLLRN-1-wk-7f3a2c91ab…ef01 192.0.2.10:17946',
    output: ['This node joined as a worker (8d7c6b5a).'],
    stage: 4,
  },
  {
    host: 'manager-a',
    command: 'cellar node ls',
    output: [
      'ID        TYPE     STATUS  AVAILABILITY  MANAGER STATUS  SANDBOXES',
      '1a2b3c4d  leader   ready   active        leader          0',
      '2b3c4d5e  manager  ready   active        reachable       0',
      '9e8d7c6b  worker   ready   active                        0',
      '8d7c6b5a  worker   ready   active                        0',
    ],
    stage: 4,
  },
  {
    host: 'manager-a',
    command: 'cellar sandbox create --name demo --image alpine:3.20 --start',
    output: ['sandbox abc123de created (name=demo node=9e8d7c6b phase=pending)'],
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

function NodeCard({
  name,
  addr,
  on,
  kind,
  subtitle,
  footer,
  footerActive,
}: {
  name: string
  addr: string
  on: boolean
  kind: 'manager' | 'worker'
  subtitle: string
  footer: string
  footerActive?: boolean
}) {
  const live =
    kind === 'manager'
      ? 'border-emerald-500/40 bg-emerald-500/5 shadow-[0_0_0_1px_rgba(16,185,129,0.08)]'
      : 'border-sky-500/40 bg-sky-500/5'
  const dot = kind === 'manager' ? 'bg-emerald-500' : 'bg-sky-500'
  const Icon = kind === 'manager' ? Shield : Box
  const iconTone =
    kind === 'manager'
      ? 'bg-emerald-500/15 text-emerald-600 dark:text-emerald-400'
      : 'bg-sky-500/15 text-sky-600 dark:text-sky-400'

  return (
    <div
      className={cn(
        'min-w-0 rounded-lg border p-3 transition-all duration-500',
        on ? live : 'border-dashed border-fd-border opacity-45',
      )}
    >
      <div className="mb-1.5 flex items-center justify-between gap-2">
        <span className="font-mono text-[11px] text-fd-muted-foreground">{addr}</span>
        <span
          className={cn(
            'size-2 rounded-full transition-colors',
            on ? dot : 'bg-fd-muted-foreground/40',
          )}
        />
      </div>
      <div className="flex min-w-0 items-center gap-2">
        <span
          className={cn(
            'inline-flex size-6 shrink-0 items-center justify-center rounded-md transition-colors',
            on ? iconTone : 'bg-fd-muted text-fd-muted-foreground',
          )}
          title={kind === 'manager' ? 'Manager' : 'Worker'}
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

function ClusterDiagram({ stage }: { stage: number }) {
  const managerA = stage >= 1
  const managerB = stage >= 2
  const workerC = stage >= 3
  const workerD = stage >= 4
  const raftOn = stage >= 2
  const sandboxOn = stage >= 5
  const managerCount = Number(managerA) + Number(managerB)
  const workerCount = Number(workerC) + Number(workerD)

  return (
    <div
      className="min-w-0 overflow-hidden rounded-xl border border-fd-border bg-fd-card p-4 shadow-sm sm:p-5"
      aria-hidden="true"
    >
      <div className="mb-4 flex min-w-0 items-center justify-between gap-3">
        <p className="text-xs font-medium tracking-wide text-fd-muted-foreground uppercase">
          Raft cluster
        </p>
        <span
          className={cn(
            'shrink-0 rounded-full px-2 py-0.5 font-mono text-[11px] transition-colors',
            raftOn
              ? 'bg-emerald-500/15 text-emerald-600 dark:text-emerald-400'
              : 'bg-fd-muted text-fd-muted-foreground',
          )}
        >
          {raftOn ? 'replicated' : 'bootstrapping'}
        </span>
      </div>

      <p className="mb-2 text-[11px] font-medium tracking-wide text-fd-muted-foreground uppercase">
        Managers
        <span className="ml-1.5 font-mono font-normal normal-case">{managerCount}/2</span>
      </p>
      <div className="grid gap-3 sm:grid-cols-2">
        <NodeCard
          kind="manager"
          name="manager-a"
          addr="192.0.2.10"
          on={managerA}
          subtitle={managerA ? 'Raft leader · gRPC :17946' : 'waiting to init'}
          footer="raftstore · desired state"
        />
        <NodeCard
          kind="manager"
          name="manager-b"
          addr="192.0.2.11"
          on={managerB}
          subtitle={managerB ? 'Raft follower · gRPC :17946' : 'waiting for join token'}
          footer="raftstore · replica"
        />
      </div>

      <p className="mt-4 mb-2 text-[11px] font-medium tracking-wide text-fd-muted-foreground uppercase">
        Workers
        <span className="ml-1.5 font-mono font-normal normal-case">{workerCount}/2</span>
      </p>
      <div className="grid gap-3 sm:grid-cols-2">
        <NodeCard
          kind="worker"
          name="worker-c"
          addr="192.0.2.20"
          on={workerC}
          subtitle={workerC ? 'joined · schedulable' : 'waiting for join token'}
          footer={sandboxOn ? 'sandbox demo · alpine:3.20' : 'no sandboxes yet'}
          footerActive={sandboxOn}
        />
        <NodeCard
          kind="worker"
          name="worker-d"
          addr="192.0.2.21"
          on={workerD}
          subtitle={workerD ? 'joined · schedulable' : 'waiting for join token'}
          footer="no sandboxes yet"
        />
      </div>

      <p className="mt-4 text-xs leading-relaxed text-fd-muted-foreground">
        Managers replicate cluster state with Raft. Workers run microsandbox VMs scheduled from that
        shared desired state.
      </p>
    </div>
  )
}

export function CliTerminal({
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
      aria-label="Animated demo of bootstrapping a Cellar Raft cluster across managers and workers"
    >
      <div className="flex items-center gap-2 border-b border-neutral-200 px-4 py-2.5 dark:border-neutral-800">
        <span className="size-2.5 rounded-full bg-red-400/80" aria-hidden />
        <span className="size-2.5 rounded-full bg-amber-400/80" aria-hidden />
        <span className="size-2.5 rounded-full bg-emerald-400/80" aria-hidden />
        <span className="ml-2 font-mono text-xs text-neutral-500 dark:text-neutral-400">
          cellar · multi-node
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

export function CliShowcase() {
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
          <p className="mb-2 text-sm font-medium text-fd-muted-foreground">Cluster</p>
          <h2 className="mb-4 text-xl font-bold tracking-tight text-balance sm:text-2xl lg:text-3xl">
            Start a cluster. Add machines when you need them.
          </h2>
          <p className="text-pretty text-fd-muted-foreground">
            Bootstrap a manager, join more nodes, and create sandboxes. Cellar keeps cluster state
            in <code className="font-mono text-[13px]">Raft</code>, then places hardware-isolated
            VMs on workers that are ready. You don&apos;t write YAML for this.
          </p>
        </div>

        <div className="grid grid-cols-1 items-start gap-6 sm:gap-8 lg:grid-cols-[minmax(0,1.15fr)_minmax(0,0.85fr)] lg:gap-10">
          <CliTerminal
            stepIndex={stepIndex}
            typed={typed}
            showOutput={showOutput}
            reducedMotion={reducedMotion}
            onPauseChange={(paused) => {
              pausedRef.current = paused
            }}
          />
          <div className="min-w-0 space-y-4">
            <ClusterDiagram stage={stage} />
            <ol className="space-y-3 text-sm">
              <li className="flex gap-3">
                <span className="mt-0.5 font-mono text-fd-muted-foreground">1</span>
                <span>
                  <code className="font-mono text-[13px]">cellar init</code> stands up the first
                  manager — Raft leader and cluster CA
                </span>
              </li>
              <li className="flex gap-3">
                <span className="mt-0.5 font-mono text-fd-muted-foreground">2</span>
                <span>
                  Mint a <code className="font-mono text-[13px]">join-token</code>, then{' '}
                  <code className="font-mono text-[13px]">cellar join</code> to add more managers
                  and workers
                </span>
              </li>
              <li className="flex gap-3">
                <span className="mt-0.5 font-mono text-fd-muted-foreground">3</span>
                <span>
                  <code className="font-mono text-[13px]">sandbox create</code> puts a microsandbox
                  VM on a live node
                </span>
              </li>
            </ol>
          </div>
        </div>

        <div className="mt-12">
          <p className="mb-3 text-sm font-medium text-fd-muted-foreground">
            Run it wherever you already have machines
          </p>
          <div className="grid grid-cols-1 gap-3 sm:grid-cols-2 lg:grid-cols-3">
            {(
              [
                {
                  title: 'AWS',
                  body: 'EC2 or bare metal with KVM. Same Cellar install as anywhere else.',
                  icon: <AwsIcon className="size-5" />,
                },
                {
                  title: 'GCP',
                  body: 'Compute Engine with nested virtualization. Point Cellar at VMs you already run.',
                  icon: <GcpIcon className="size-5" />,
                },
                {
                  title: 'Your own infra',
                  body: 'A Linux box with /dev/kvm, or a Mac. A laptop is enough to start.',
                  icon: <Server className="size-5" />,
                },
              ] as const satisfies ReadonlyArray<{
                title: string
                body: string
                icon: ReactNode
              }>
            ).map((card) => (
              <div
                key={card.title}
                className="min-w-0 rounded-xl border border-fd-border bg-fd-card px-4 py-4"
              >
                <div className="mb-2 flex min-w-0 items-center gap-2.5">
                  <span className="inline-flex size-8 shrink-0 items-center justify-center rounded-lg border border-fd-border bg-fd-background text-fd-foreground">
                    {card.icon}
                  </span>
                  <h3 className="truncate text-sm font-semibold">{card.title}</h3>
                </div>
                <p className="text-sm text-fd-muted-foreground">{card.body}</p>
              </div>
            ))}
          </div>
        </div>
      </div>
    </section>
  )
}
