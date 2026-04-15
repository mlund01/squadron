import { Footer, Layout, Navbar } from 'nextra-theme-docs'
import { Head } from 'nextra/components'
import { getPageMap } from 'nextra/page-map'
import 'nextra-theme-docs/style.css'
import './globals.css'

export const metadata = {
  title: 'Squadron',
  description: 'AI agent workflows as configuration',
}

export default async function RootLayout({ children }) {
  const pageMap = await getPageMap()

  return (
    <html lang="en" dir="ltr" suppressHydrationWarning>
      <Head
        color={{
          hue: 142,
          saturation: 70,
          lightness: { dark: 55, light: 40 },
        }}
        backgroundColor={{
          dark: '#0a1f0a',
          light: '#f0fdf4',
        }}
        faviconGlyph="▶"
      />
      <body>
        <Layout
          navbar={<Navbar logo={<b className="defcon5-logo">SQUADRON</b>} />}
          pageMap={pageMap}
          docsRepositoryBase="https://github.com/mlund01/squadron/tree/main/docs"
          footer={
            <Footer>
              <span style={{ fontFamily: "'JetBrains Mono', monospace", color: 'var(--defcon5-fg-dim)' }}>
                [MIT] {new Date().getFullYear()} SQUADRON // DEFCON 5
              </span>
            </Footer>
          }
          nextThemes={{
            defaultTheme: 'dark',
            forcedTheme: 'dark',
          }}
          themeSwitch={{ dark: 'DEFCON 5', light: 'DEFCON 5', system: 'DEFCON 5' }}
        >
          {children}
        </Layout>
      </body>
    </html>
  )
}
