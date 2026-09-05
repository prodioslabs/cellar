import { DynamicCodeBlock } from 'fumadocs-ui/components/dynamic-codeblock'
import Link from 'next/link'

const NOTE = `# Point the official microsandbox SDK at Cellar:
#   cloud backend URL = https://cellar.example.com  (cellar-gateway)
#   API key           = cellar_…  (from: cellar api-key create)
#
# See microsandbox docs for language SDKs (TypeScript, Go, Python, …).
`

export function SdkShowcase() {
  return (
    <section className="border-t border-fd-border px-6 py-16 sm:py-20">
      <div className="mx-auto w-full max-w-5xl">
        <p className="mb-2 text-sm font-medium text-fd-muted-foreground">Clients</p>
        <h2 className="mb-3 text-2xl font-bold tracking-tight sm:text-3xl">
          Official microsandbox SDKs
        </h2>
        <p className="mb-8 max-w-2xl text-fd-muted-foreground">
          Apps use the official microsandbox SDKs in cloud mode against{' '}
          <code className="font-mono text-[13px]">cellar-gateway</code>. Mint an API key with the
          Cellar CLI, then set the SDK cloud backend URL to your gateway.
        </p>
        <DynamicCodeBlock lang="bash" code={NOTE} codeblock={{ title: 'Configure' }} />
        <p className="mt-4">
          <Link
            href="/docs/api"
            className="inline-flex text-sm font-medium text-fd-primary underline-offset-4 hover:underline"
          >
            Client API docs →
          </Link>
        </p>
      </div>
    </section>
  )
}
