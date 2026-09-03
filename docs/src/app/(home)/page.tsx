import { CliShowcase } from '@/components/home/cli-terminal'
import { Hero } from '@/components/home/hero'
import { SdkShowcase } from '@/components/home/sdk-showcase'

export default function HomePage() {
  return (
    <div className="flex flex-1 flex-col">
      <Hero />
      <CliShowcase />
      <SdkShowcase />
    </div>
  )
}
