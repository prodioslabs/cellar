import { DynamicCodeBlock } from 'fumadocs-ui/components/dynamic-codeblock'
import Link from 'next/link'

const NOTE = `# Use the official microsandbox CLI / SDKs against Cellar.
# Point cloud mode at your cellar-gateway — same surface as microsandbox cloud.

export MSB_BACKEND=cloud
export MSB_API_URL=https://cellar.example.com
export MSB_API_KEY=cellar_…   # from: cellar api-key create --name app

# Then use microsandbox as usual (SDK setDefaultBackend / CLI msb …).
# See: https://docs.microsandbox.dev/operations/backends
`

export function SdkShowcase() {
  return (
    <section className="border-t border-fd-border px-4 py-12 sm:px-6 sm:py-16 lg:py-20">
      <div className="mx-auto w-full max-w-(--fd-layout-width) min-w-0">
        <p className="mb-2 text-sm font-medium text-fd-muted-foreground">Clients</p>
        <h2 className="mb-3 text-xl font-bold tracking-tight text-balance sm:text-2xl lg:text-3xl">
          Use the microsandbox SDKs you already know
        </h2>
        <p className="mb-8 max-w-2xl text-pretty text-fd-muted-foreground">
          Once the cluster is up, point the official microsandbox SDKs at your{' '}
          <code className="font-mono text-[13px]">cellar-gateway</code>, pass a Cellar API key, and
          you&apos;re talking to sandboxes you run yourself. There&apos;s no separate Cellar SDK —
          you use theirs, in cloud mode.
        </p>
        <div className="max-w-full min-w-0 overflow-x-auto">
          <DynamicCodeBlock
            lang="bash"
            code={NOTE}
            codeblock={{ title: 'Configure microsandbox backends' }}
          />
        </div>
        <div className="mt-4 flex flex-col gap-2 sm:flex-row sm:flex-wrap sm:gap-x-5 sm:gap-y-2">
          <a
            href="https://docs.microsandbox.dev/operations/backends"
            target="_blank"
            rel="noreferrer"
            className="inline-flex text-sm font-medium text-fd-primary underline-offset-4 hover:underline"
          >
            microsandbox backends docs →
          </a>
          <Link
            href="/docs/api"
            className="inline-flex text-sm font-medium text-fd-primary underline-offset-4 hover:underline"
          >
            Cellar client API docs →
          </Link>
        </div>
      </div>
    </section>
  )
}
