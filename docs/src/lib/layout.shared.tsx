import { GitHubStarLink } from '@/components/github-star'
import type { BaseLayoutProps } from 'fumadocs-ui/layouts/shared'
import Image from 'next/image'
import { appName } from './shared'

export function baseOptions(): BaseLayoutProps {
  return {
    nav: {
      title: (
        <>
          <Image
            src="/cellar-logo.png"
            alt="Cellar"
            width={28}
            height={28}
            className="rounded-sm"
          />
          {appName}
        </>
      ),
    },
    links: [
      {
        text: 'Documentation',
        url: '/docs',
        active: 'nested-url',
      },
      {
        type: 'custom',
        secondary: true,
        children: <GitHubStarLink />,
      },
    ],
  }
}
