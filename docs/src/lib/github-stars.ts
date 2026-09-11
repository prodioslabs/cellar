import { cache } from 'react'
import { fetchRepositoryInfo } from 'fumadocs-ui/components/github-info'
import { gitConfig } from '@/lib/shared'

export const getGitHubStars = cache(async (): Promise<number | null> => {
  try {
    const info = await fetchRepositoryInfo({
      owner: gitConfig.user,
      repo: gitConfig.repo,
      token: process.env.GITHUB_TOKEN,
      fetchOptions: { next: { revalidate: 60 } },
    })
    return info.stars
  } catch {
    return null
  }
})
