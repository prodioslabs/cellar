import Link from 'next/link'
import { InstallCommand } from './install-command'
import { MetallicCube } from './metallic-cube'

export function Hero() {
  return (
    <section className="flex flex-col items-center px-6 pb-16 pt-6 text-center sm:pb-24 sm:pt-8">
      <MetallicCube />
      <h1 className="mb-4 max-w-3xl text-4xl font-bold tracking-tight sm:text-5xl">
        Isolated sandboxes in one command
      </h1>
      <p className="mb-8 max-w-2xl text-lg text-fd-muted-foreground">
        Install Cellar, create a KVM-backed microsandbox, and run untrusted code with cluster
        scheduling and an HTTP gateway for official microsandbox SDKs.
      </p>
      <InstallCommand className="mb-8" />
      <div className="flex flex-wrap items-center justify-center gap-3">
        <Link
          href="/docs/quick-start"
          className="rounded-full bg-fd-primary px-5 py-2.5 text-sm font-medium text-fd-primary-foreground transition-opacity hover:opacity-90"
        >
          Quick start
        </Link>
        <Link
          href="/docs"
          className="rounded-full border px-5 py-2.5 text-sm font-medium transition-colors hover:bg-fd-accent"
        >
          Read the docs
        </Link>
      </div>
    </section>
  )
}
