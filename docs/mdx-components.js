import { useMDXComponents as getNextraMDXComponents } from 'nextra-theme-docs'
import LLMToolbar from './components/LLMToolbar.jsx'

const SITE = 'https://docs.squadron.sh'

function PageJsonLd({ metadata }) {
  const title = metadata?.title || 'Squadron'
  const description = metadata?.description || 'AI agent workflows as configuration'
  const data = {
    '@context': 'https://schema.org',
    '@type': 'TechArticle',
    headline: title,
    description,
    inLanguage: 'en',
    isPartOf: {
      '@type': 'WebSite',
      name: 'Squadron Documentation',
      url: SITE,
    },
    publisher: {
      '@type': 'Organization',
      name: 'Squadron',
      url: SITE,
    },
  }
  return (
    <script
      type="application/ld+json"
      dangerouslySetInnerHTML={{ __html: JSON.stringify(data) }}
    />
  )
}

export function useMDXComponents(components) {
  const base = getNextraMDXComponents(components)
  const BaseWrapper = base.wrapper
  function Wrapper(props) {
    return (
      <BaseWrapper {...props}>
        <PageJsonLd metadata={props.metadata} />
        <LLMToolbar />
        {props.children}
      </BaseWrapper>
    )
  }
  return { ...base, wrapper: Wrapper }
}
