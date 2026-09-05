'use client'

import { Check, Copy } from 'lucide-react'
import { useState } from 'react'
import { cn } from '@/lib/cn'

export const INSTALL_COMMAND = 'curl -fsSL https://cellar.prodioslabs.in/install.sh | sh'

export function InstallCommand({
  command = INSTALL_COMMAND,
  className,
}: {
  command?: string
  className?: string
}) {
  const [copied, setCopied] = useState(false)

  async function copy() {
    try {
      await navigator.clipboard.writeText(command)
      setCopied(true)
      window.setTimeout(() => setCopied(false), 1600)
    } catch {
      setCopied(false)
    }
  }

  return (
    <div
      className={cn(
        'flex w-full min-w-0 max-w-full items-center gap-2 overflow-hidden rounded-xl border border-fd-border bg-fd-card px-3 py-2 shadow-sm sm:max-w-2xl sm:px-4 sm:py-2.5',
        className,
      )}
    >
      <span className="shrink-0 font-mono text-sm text-fd-muted-foreground select-none" aria-hidden>
        $
      </span>
      <code className="min-w-0 flex-1 truncate text-left font-mono text-[12px] text-fd-foreground sm:text-[13px] md:text-sm">
        {command}
      </code>
      <button
        type="button"
        onClick={copy}
        className="inline-flex shrink-0 items-center gap-1.5 rounded-lg px-2 py-1.5 text-xs font-medium text-fd-muted-foreground transition-colors hover:bg-fd-accent hover:text-fd-accent-foreground"
        aria-label={copied ? 'Copied install command' : 'Copy install command'}
      >
        {copied ? <Check className="size-3.5" /> : <Copy className="size-3.5" />}
        <span className="hidden sm:inline">{copied ? 'Copied' : 'Copy'}</span>
      </button>
    </div>
  )
}
