'use client'

import { DynamicCodeBlock } from 'fumadocs-ui/components/dynamic-codeblock'
import { Tab, Tabs } from 'fumadocs-ui/components/tabs'
import Link from 'next/link'

const CLI = `# Point the official microsandbox CLI / SDKs at cellar-gateway.
# Same cloud-mode surface as microsandbox cloud — your cluster, your key.

export MSB_BACKEND=cloud
export MSB_API_URL=https://cellar.example.com
export MSB_API_KEY=cellar_…   # from: cellar api-key create --name app

msb context
msb run python -- python -V
`

const TYPESCRIPT = `import { Sandbox, setDefaultBackend } from "microsandbox";

setDefaultBackend({
  kind: "cloud",
  url: "https://cellar.example.com",
  apiKey: process.env.MSB_API_KEY!,
});

const sandbox = await Sandbox.builder("hello")
  .image("python")
  .create();

const output = await sandbox.exec("python", ["-c", "print('hello from cellar')"]);
console.log(output.stdout());
`

const RUST = `use microsandbox::{set_default_backend, CloudBackend, Sandbox};

#[tokio::main]
async fn main() -> Result<(), Box<dyn std::error::Error>> {
    set_default_backend(CloudBackend::new(
        "https://cellar.example.com",
        std::env::var("MSB_API_KEY")?,
    )?);

    let sandbox = Sandbox::builder("hello")
        .image("python")
        .create()
        .await?;

    let output = sandbox
        .exec("python", ["-c", "print('hello from cellar')"])
        .await?;
    println!("{}", output.stdout()?);

    sandbox.stop().await?;
    Ok(())
}
`

const PYTHON = `import asyncio
import os
from microsandbox import BackendKind, Sandbox, set_default_backend

set_default_backend(
    BackendKind.CLOUD,
    url="https://cellar.example.com",
    api_key=os.environ["MSB_API_KEY"],
)

async def main():
    sandbox = await Sandbox.create("hello", image="python")
    output = await sandbox.exec("python", ["-c", "print('hello from cellar')"])
    print(output.stdout_text)
    await sandbox.stop()

asyncio.run(main())
`

const RUBY = `require "microsandbox"

Microsandbox.use_cloud_backend!(
  ENV.fetch("MSB_API_KEY"),
  url: "https://cellar.example.com",
)

Microsandbox::Sandbox.with("hello", image: "python") do |sandbox|
  output = sandbox.exec("python", ["-c", "print('hello from cellar')"])
  puts output.stdout
end
`

const EXAMPLES = [
  { label: 'TypeScript', lang: 'ts', title: 'client.ts', code: TYPESCRIPT },
  { label: 'Rust', lang: 'rust', title: 'main.rs', code: RUST },
  { label: 'Python', lang: 'python', title: 'main.py', code: PYTHON },
  { label: 'Ruby', lang: 'ruby', title: 'main.rb', code: RUBY },
  { label: 'CLI', lang: 'bash', title: 'Configure microsandbox backends', code: CLI },
] as const

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
          <code className="font-mono text-[13px]">cellar-gateway</code> in cloud mode, pass a Cellar
          API key, and you&apos;re talking to sandboxes you run yourself. There&apos;s no separate
          Cellar SDK — you use theirs.
        </p>
        <Tabs items={EXAMPLES.map((example) => example.label)} className="my-0 max-w-full min-w-0">
          {EXAMPLES.map((example) => (
            <Tab
              key={example.label}
              value={example.label}
              className="max-w-full min-w-0 overflow-x-auto"
            >
              <DynamicCodeBlock
                lang={example.lang}
                code={example.code}
                codeblock={{ title: example.title }}
              />
            </Tab>
          ))}
        </Tabs>
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
