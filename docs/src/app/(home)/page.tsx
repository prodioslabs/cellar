import { CliShowcase } from '@/components/home/cli-terminal'
import { Hero } from '@/components/home/hero'
import { SdkShowcase } from '@/components/home/sdk-showcase'

export default function HomePage() {
  return (
    <div className="flex min-w-0 flex-1 flex-col overflow-x-clip">
      <Hero />
      <CliShowcase />
      <SdkShowcase />
    </div>
  )
}
