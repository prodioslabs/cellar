import Image from 'next/image'
import Link from 'next/link'
import { InstallCommand } from './install-command'

export function Hero() {
  return (
    <section className="flex flex-col items-center px-6 py-16 text-center sm:py-24">
      <Image
        src="/cellar-logo.png"
        alt="Cellar — isometric C of slate-blue cubes with a single orange cube in the center"
        width={128}
        height={128}
        priority
        className="mb-8"
      />
      <h1 className="mb-4 max-w-3xl text-4xl font-bold tracking-tight sm:text-5xl">
        Isolated sandboxes in one command
      </h1>
      <p className="mb-8 max-w-2xl text-lg text-fd-muted-foreground">
        Install Cellar, create a hardened container, and run untrusted code with userspace egress
        policy.
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
