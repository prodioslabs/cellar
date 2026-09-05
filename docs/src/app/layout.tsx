import { RootProvider } from 'fumadocs-ui/provider/next'
import './global.css'
import { Inter } from 'next/font/google'
import type { Metadata } from 'next'

const inter = Inter({
  subsets: ['latin'],
})

export const metadata: Metadata = {
  metadataBase: new URL(process.env.NEXT_PUBLIC_SITE_URL ?? 'http://localhost:3000'),
  title: {
    default: 'Cellar',
    template: '%s | Cellar',
  },
  description:
    'Container orchestrator control plane for isolated sandboxes with mTLS identity and userspace egress policy.',
  icons: {
    icon: [{ url: '/cellar-logo.png', type: 'image/png' }],
  },
}

export default function Layout({ children }: LayoutProps<'/'>) {
  return (
    <html lang="en" className={inter.className} suppressHydrationWarning>
      <body className="flex min-h-screen flex-col">
        <RootProvider>{children}</RootProvider>
      </body>
    </html>
  )
}
