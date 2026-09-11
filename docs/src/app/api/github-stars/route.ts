import { githubStarsRevalidateSeconds, getGitHubStars } from '@/lib/github-stars'

export const revalidate = githubStarsRevalidateSeconds

export async function GET() {
  const stars = await getGitHubStars()
  return Response.json(
    { stars },
    {
      headers: {
        'Cache-Control': `public, s-maxage=${githubStarsRevalidateSeconds}, stale-while-revalidate=${githubStarsRevalidateSeconds * 2}`,
      },
    },
  )
}
