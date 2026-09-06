import { Star } from 'lucide-react'
import { Suspense, use } from 'react'
import { fetchRepositoryInfo } from 'fumadocs-ui/components/github-info'
import { cn } from '@/lib/cn'
import { gitConfig } from '@/lib/shared'

export const githubRepoUrl = `https://github.com/${gitConfig.user}/${gitConfig.repo}`

const starFormatter = new Intl.NumberFormat('en', {
  notation: 'compact',
  maximumFractionDigits: 1,
})

const repoInfoPromise = fetchRepositoryInfo({
  owner: gitConfig.user,
  repo: gitConfig.repo,
  token: process.env.GITHUB_TOKEN,
}).then(
  (info) => info.stars,
  () => null,
)

type GitHubStarLinkProps = {
  className?: string
  variant?: 'nav' | 'hero'
}

export function GitHubStarLink(props: GitHubStarLinkProps) {
  return (
    <Suspense fallback={<GitHubStarAnchor stars={null} {...props} />}>
      <GitHubStarLinkInner {...props} />
    </Suspense>
  )
}

function GitHubStarLinkInner(props: GitHubStarLinkProps) {
  return <GitHubStarAnchor stars={use(repoInfoPromise)} {...props} />
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
