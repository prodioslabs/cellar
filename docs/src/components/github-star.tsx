'use client'

import { Star } from 'lucide-react'
import { useEffect, useState } from 'react'
import { cn } from '@/lib/cn'
import { gitConfig } from '@/lib/shared'

export const githubRepoUrl = `https://github.com/${gitConfig.user}/${gitConfig.repo}`

const starFormatter = new Intl.NumberFormat('en', {
  notation: 'compact',
  maximumFractionDigits: 1,
})

const STARS_REFRESH_MS = 60_000

let cachedStars: number | null = null
let inflight: Promise<number | null> | null = null

async function fetchStars(): Promise<number | null> {
  const response = await fetch('/api/github-stars', { cache: 'no-store' })
  if (!response.ok) return cachedStars
  const data: unknown = await response.json()
  if (typeof data !== 'object' || data === null || !('stars' in data)) {
    return cachedStars
  }
  return typeof data.stars === 'number' ? data.stars : null
}

function refreshStars(): Promise<number | null> {
  if (inflight) return inflight
  inflight = fetchStars()
    .catch(() => cachedStars)
    .then((stars) => {
      cachedStars = stars
      inflight = null
      return stars
    })
  return inflight
}

function useGitHubStars() {
  const [stars, setStars] = useState<number | null>(cachedStars)

  useEffect(() => {
    let cancelled = false

    const tick = () => {
      void refreshStars().then((value) => {
        if (!cancelled) setStars(value)
      })
    }

    tick()
    const interval = window.setInterval(tick, STARS_REFRESH_MS)
    const onVisible = () => {
      if (document.visibilityState === 'visible') tick()
    }
    document.addEventListener('visibilitychange', onVisible)

    return () => {
      cancelled = true
      window.clearInterval(interval)
      document.removeEventListener('visibilitychange', onVisible)
    }
  }, [])

  return stars
}

type GitHubStarLinkProps = {
  className?: string
  variant?: 'nav' | 'hero'
}

export function GitHubStarLink(props: GitHubStarLinkProps) {
  return <GitHubStarAnchor stars={useGitHubStars()} {...props} />
}

function GitHubStarAnchor({
  stars,
  className,
  variant = 'nav',
}: GitHubStarLinkProps & { stars: number | null }) {
  const isHero = variant === 'hero'

  return (
    <a
      href={githubRepoUrl}
      target="_blank"
      rel="noreferrer noopener"
      aria-label={
        stars != null
          ? `Star Cellar on GitHub (${starFormatter.format(stars)} stars)`
          : 'Star Cellar on GitHub'
      }
      className={cn(
        'inline-flex items-center justify-center gap-1.5 text-sm font-medium transition-colors',
        isHero
          ? 'rounded-full border px-5 py-2.5 hover:bg-fd-accent'
          : 'rounded-lg px-2 py-1.5 text-fd-muted-foreground hover:bg-fd-accent hover:text-fd-accent-foreground',
        className,
      )}
    >
      <Star className="size-3.5" />
      <span>{isHero ? 'Star on GitHub' : 'Star'}</span>
      {stars != null && (
        <span
          className={cn(
            'tabular-nums',
            isHero
              ? 'rounded-full bg-fd-accent px-2 py-0.5 text-xs text-fd-accent-foreground'
              : 'text-fd-foreground',
          )}
        >
          {starFormatter.format(stars)}
        </span>
      )}
    </a>
  )
}
