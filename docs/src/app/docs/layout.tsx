import { source } from '@/lib/source'
import { DocsLayout } from 'fumadocs-ui/layouts/docs'
import { ProdiosCredit } from '@/components/footer'
import { baseOptions } from '@/lib/layout.shared'

export default function Layout({ children }: LayoutProps<'/docs'>) {
  return (
    <DocsLayout
      tree={source.getPageTree()}
      {...baseOptions()}
      sidebar={{
        footer: <ProdiosCredit className="mt-2 text-xs text-fd-muted-foreground" />,
      }}
    >
      {children}
    </DocsLayout>
  )
}
