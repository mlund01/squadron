import { Footer, Layout, Navbar } from 'nextra-theme-docs'
import { Head } from 'nextra/components'
import { getPageMap } from 'nextra/page-map'
import 'nextra-theme-docs/style.css'

export const metadata = {
  title: 'Squadron',
  description: 'AI agent workflows as configuration',
}

export default async function RootLayout({ children }) {
  const pageMap = await getPageMap()

  return (
    <html lang="en" dir="ltr" suppressHydrationWarning>
      <Head />
      <body>
        <Layout
          navbar={<Navbar logo={<b>Squadron</b>} />}
          pageMap={pageMap}
          docsRepositoryBase="https://github.com/mlund01/squadron/tree/main/docs"
          footer={<Footer>MIT {new Date().getFullYear()} Squadron</Footer>}
        >
          {children}
        </Layout>
      </body>
    </html>
  )
}
