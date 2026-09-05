export const PRODIOS_LABS_URL = 'https://prodioslabs.com?utm_source=cellar'

export function ProdiosCredit({ className }: { className?: string }) {
  return (
    <p className={className}>
      Made with care by{' '}
      <a
        href={PRODIOS_LABS_URL}
        target="_blank"
        rel="noreferrer"
        className="font-medium text-fd-foreground underline-offset-4 hover:underline"
      >
        Prodios Labs
      </a>
    </p>
  )
}

export function Footer() {
  return (
    <footer className="border-t border-fd-border px-6 py-8">
      <ProdiosCredit className="mx-auto w-full max-w-(--fd-layout-width) text-center text-sm text-fd-muted-foreground" />
    </footer>
  )
}
