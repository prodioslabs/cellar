import { HomeLayout } from 'fumadocs-ui/layouts/home'
import { Footer } from '@/components/footer'
import { baseOptions } from '@/lib/layout.shared'

export default function Layout({ children }: LayoutProps<'/'>) {
  return (
    <>
      <HomeLayout {...baseOptions()} className="min-w-0">
        {children}
      </HomeLayout>
      <Footer />
    </>
  )
}
