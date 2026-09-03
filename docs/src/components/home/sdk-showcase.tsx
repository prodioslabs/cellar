import { DynamicCodeBlock } from 'fumadocs-ui/components/dynamic-codeblock'
import { Tab, Tabs } from 'fumadocs-ui/components/tabs'
import Link from 'next/link'

const TS_INSTALL = 'npm install @cellar/node'

const TS_CODE = `import { Client } from '@cellar/node'

const sb = await Client.fromEnv().create({
  spec: { runtime: 'node-26' },
})
await sb.waitUntilReady()
const { stdout } = await sb.exec(['node', '-e', 'console.log("hello")'])
console.log(stdout.toString())
await sb.remove()
`

const GO_INSTALL = 'go get github.com/prodioslabs/cellar/sdk/go'

const GO_CODE = `c, _ := client.NewFromEnv()
sb, _ := c.Create(ctx, &client.SandboxCreateRequest{
  Spec: &client.SandboxSpec{Runtime: "node-26"},
})
_ = sb.WaitUntilReady(ctx, client.WaitUntilReadyOptions{})
res, _ := sb.Exec(ctx, []string{"node", "-e", \`console.log("hello")\`})
`

export function SdkShowcase() {
  return (
    <section className="border-t border-fd-border px-6 py-16 sm:py-20">
      <div className="mx-auto w-full max-w-5xl">
        <p className="mb-2 text-sm font-medium text-fd-muted-foreground">SDKs</p>
        <h2 className="mb-3 text-2xl font-bold tracking-tight sm:text-3xl">
          Same API in TypeScript and Go
        </h2>
        <p className="mb-8 max-w-2xl text-fd-muted-foreground">
          Create a sandbox, wait until it is running, exec, then tear it down. Both clients talk to{' '}
          <code className="font-mono text-[13px]">cellar-gateway</code> with an API key.
        </p>
        <Tabs items={['TypeScript', 'Go']} className="rounded-xl bg-fd-card p-1">
          <Tab value="TypeScript">
            <SdkPanel
              install={TS_INSTALL}
              lang="ts"
              code={TS_CODE}
              title="create-sandbox.ts"
              href="/docs/sdk/node"
              label="TypeScript SDK docs"
            />
          </Tab>
          <Tab value="Go">
            <SdkPanel
              install={GO_INSTALL}
              lang="go"
              code={GO_CODE}
              title="create_sandbox.go"
              href="/docs/sdk/go"
              label="Go SDK docs"
            />
          </Tab>
        </Tabs>
      </div>
    </section>
  )
}

function SdkPanel({
  install,
  lang,
  code,
  title,
  href,
  label,
}: {
  install: string
  lang: string
  code: string
  title: string
  href: string
  label: string
}) {
  return (
    <div className="space-y-3">
      <DynamicCodeBlock lang="bash" code={install} codeblock={{ title: 'Install' }} />
      <DynamicCodeBlock lang={lang} code={code} codeblock={{ title }} />
      <Link
        href={href}
        className="inline-flex text-sm font-medium text-fd-primary underline-offset-4 hover:underline"
      >
        {label} →
      </Link>
    </div>
  )
}
