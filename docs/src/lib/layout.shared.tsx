import type { BaseLayoutProps } from 'fumadocs-ui/layouts/shared'
import Image from 'next/image'
import { appName, gitConfig } from './shared'

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
    ],
    githubUrl: `https://github.com/${gitConfig.user}/${gitConfig.repo}`,
  }
}
