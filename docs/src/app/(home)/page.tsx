import Image from 'next/image'
import Link from 'next/link'

export default function HomePage() {
  return (
    <div className="flex flex-1 flex-col items-center justify-center px-6 py-16 text-center">
      <Image
        src="/cellar-logo.png"
        alt="Cellar — isometric C of slate-blue cubes with a single orange cube in the center"
        width={160}
        height={160}
        priority
        className="mb-8"
      />
      <h1 className="mb-4 text-4xl font-bold tracking-tight">Cellar</h1>
      <p className="mb-8 max-w-2xl text-lg text-fd-muted-foreground">
        A container orchestrator control plane for isolated sandboxes — cluster identity over mTLS
        gRPC, Raft-replicated CA, Docker + gVisor{' '}
        <code className="rounded bg-fd-muted px-1.5 py-0.5 text-sm">runsc</code>, and userspace
        egress policy.
      </p>
      <div className="flex flex-wrap items-center justify-center gap-3">
        <Link
          href="/docs"
          className="rounded-full bg-fd-primary px-5 py-2.5 text-sm font-medium text-fd-primary-foreground transition-opacity hover:opacity-90"
        >
          Read the docs
        </Link>
        <Link
          href="/docs/quick-start"
          className="rounded-full border px-5 py-2.5 text-sm font-medium transition-colors hover:bg-fd-accent"
        >
          Quick start
        </Link>
      </div>
    </div>
  )
}
