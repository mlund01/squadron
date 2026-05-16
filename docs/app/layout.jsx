import { Footer, Layout, Navbar } from 'nextra-theme-docs'
import { Head } from 'nextra/components'
import { getPageMap } from 'nextra/page-map'
import 'nextra-theme-docs/style.css'
import './globals.css'

export const metadata = {
  title: {
    default: 'Squadron — declarative multi-agent AI workflows in HCL',
    template: '%s | Squadron',
  },
  description:
    'Squadron is a declarative framework for building and running multi-agent AI workflows. Define agents, tools, models, and missions entirely in HCL configuration — no code required.',
  metadataBase: new URL('https://docs.squadron.sh'),
  alternates: { canonical: '/' },
  openGraph: {
    title: 'Squadron — declarative multi-agent AI workflows in HCL',
    description:
      'Build and run multi-agent AI workflows declaratively in HCL. Orchestration, state, routing, persistence, and resume — handled by the runtime.',
    url: 'https://docs.squadron.sh',
    siteName: 'Squadron',
    type: 'website',
  },
}

const SITE_JSONLD = {
  '@context': 'https://schema.org',
  '@type': 'SoftwareApplication',
  name: 'Squadron',
  applicationCategory: 'DeveloperApplication',
  operatingSystem: 'macOS, Linux, Windows',
  description:
    'Squadron is a declarative framework for building and running multi-agent AI workflows. Agents, tools, models, and missions are defined entirely in HCL configuration. Squadron handles orchestration, state, dependency resolution, conditional routing, persistence, and resume. Supports Anthropic, OpenAI, Gemini, and Ollama, and speaks Model Context Protocol (MCP) in both directions.',
  url: 'https://docs.squadron.sh',
  downloadUrl: 'https://github.com/mlund01/squadron/releases',
  softwareVersion: 'latest',
  license: 'https://opensource.org/licenses/MIT',
  offers: { '@type': 'Offer', price: '0', priceCurrency: 'USD' },
  author: { '@type': 'Organization', name: 'Squadron', url: 'https://docs.squadron.sh' },
  sameAs: ['https://github.com/mlund01/squadron'],
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
          light: '#ffffff',
        }}
      />
      <body>
        <script
          type="application/ld+json"
          dangerouslySetInnerHTML={{ __html: JSON.stringify(SITE_JSONLD) }}
        />
        <Layout
          navbar={<Navbar logo={<b className="defcon5-logo">SQUADRON</b>} />}
          pageMap={pageMap}
          docsRepositoryBase="https://github.com/mlund01/squadron/tree/main/docs"
          footer={
            <Footer>
              <span style={{ fontFamily: "'JetBrains Mono', monospace", color: 'var(--defcon5-fg-dim)' }}>
                [MIT] {new Date().getFullYear()} SQUADRON
              </span>
            </Footer>
          }
          nextThemes={{
            defaultTheme: 'system',
          }}
          themeSwitch={{ dark: 'DEFCON 5', light: 'DEFCON 1', system: 'SYSTEM' }}
        >
          {children}
        </Layout>
      </body>
    </html>
  )
}
