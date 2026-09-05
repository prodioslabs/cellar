import Link from 'next/link'
import { AnimatedLogo } from './animated-logo'
import { InstallCommand } from './install-command'

export function Hero() {
  return (
    <section className="px-4 pt-4 pb-12 sm:px-6 sm:pt-10 sm:pb-24">
      <div className="mx-auto grid w-full min-w-0 max-w-(--fd-layout-width) items-center gap-8 sm:gap-10 lg:grid-cols-[minmax(0,1.05fr)_minmax(0,0.95fr)] lg:gap-12">
        <div className="min-w-0 text-center lg:text-left">
          <p className="mb-3 text-sm font-medium text-fd-muted-foreground">
            Self-host{' '}
            <a
              href="https://microsandbox.dev"
              target="_blank"
              rel="noreferrer"
              className="text-fd-primary underline-offset-4 hover:underline"
            >
              microsandbox
            </a>{' '}
            today
          </p>
          <h1 className="mb-4 text-3xl font-bold tracking-tight text-balance sm:text-4xl lg:text-5xl">
            Deploy microsandbox on your own cloud infra
          </h1>
          <p className="mb-6 text-base text-pretty text-fd-muted-foreground sm:text-lg">
            Cellar is the control plane that runs the open-source{' '}
            <a
              href="https://microsandbox.dev"
              target="_blank"
              rel="noreferrer"
              className="font-medium text-fd-foreground underline-offset-4 hover:underline"
            >
              microsandbox
            </a>{' '}
            runtime on AWS, GCP, or hardware you already own. No waiting for
            managed cloud access — stand up KVM-backed sandboxes on your infra
            now.
          </p>
          <InstallCommand className="mx-auto mb-8 lg:mx-0" />
          <div className="flex flex-col items-stretch justify-center gap-3 sm:flex-row sm:flex-wrap sm:items-center lg:justify-start">
            <Link
              href="/docs/quick-start"
              className="rounded-full bg-fd-primary px-5 py-2.5 text-center text-sm font-medium text-fd-primary-foreground transition-opacity hover:opacity-90"
            >
              Quick start
            </Link>
            <Link
              href="/docs"
              className="rounded-full border px-5 py-2.5 text-center text-sm font-medium transition-colors hover:bg-fd-accent"
            >
              Read the docs
            </Link>
          </div>
        </div>
        <div className="min-w-0 lg:block hidden">
          <AnimatedLogo />
        </div>
      </div>
    </section>
  )
}
