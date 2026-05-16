#!/usr/bin/env node
/**
 * Generates AEO assets from docs/content/:
 *   - public/llms.txt        — curated index (llmstxt.org format)
 *   - public/llms-full.txt   — every doc concatenated as plain markdown
 *   - public/<path>.md       — raw markdown mirror of each MDX page
 *
 * Page order and titles come from each directory's _meta.js. Page titles
 * fall back to frontmatter `title:` then the meta value then the filename.
 *
 * Run automatically before `next build` (see package.json prebuild script).
 */

import fs from 'node:fs/promises'
import path from 'node:path'
import { pathToFileURL } from 'node:url'

const SITE = 'https://docs.squadron.sh'
const CONTENT = path.resolve('content')
const PUBLIC = path.resolve('public')

// Strip frontmatter and return { title, body }. Title preference: frontmatter
// `title:` → first `# ` heading → null.
function parseMdx(src) {
  let body = src
  let frontmatterTitle = null
  const fmMatch = src.match(/^---\n([\s\S]*?)\n---\n?/)
  if (fmMatch) {
    body = src.slice(fmMatch[0].length)
    const t = fmMatch[1].match(/^title:\s*(.+)$/m)
    if (t) frontmatterTitle = t[1].replace(/^['"]|['"]$/g, '').trim()
  }
  let h1Title = null
  const h1 = body.match(/^#\s+(.+)$/m)
  if (h1) h1Title = h1[1].trim()
  return { title: frontmatterTitle || h1Title, body: body.trim() }
}

// First non-empty paragraph after the H1, stripped of markdown link syntax.
function firstParagraph(body) {
  const afterH1 = body.replace(/^#\s+.+\n+/, '')
  const para = afterH1.split(/\n\s*\n/).find((p) => p.trim() && !p.startsWith('```') && !p.startsWith('#'))
  if (!para) return ''
  return para
    .replace(/\[([^\]]+)\]\([^)]+\)/g, '$1')
    .replace(/\s+/g, ' ')
    .trim()
}

async function loadMeta(dir) {
  const metaPath = path.join(dir, '_meta.js')
  try {
    await fs.access(metaPath)
  } catch {
    return null
  }
  const mod = await import(pathToFileURL(metaPath).href + `?t=${Date.now()}`)
  return mod.default || mod
}

// Walk the content tree in _meta.js order. Returns a flat list of pages:
//   { slug, urlPath, mdPath, title, sectionTitle, src }
async function collectPages() {
  const pages = []

  async function visit(dir, urlPrefix, sectionTitle) {
    const meta = await loadMeta(dir)
    const entries = await fs.readdir(dir, { withFileTypes: true })

    // Order: meta keys first, then anything leftover alphabetically.
    const order = meta ? Object.keys(meta) : []
    const seen = new Set(order)
    for (const e of entries) {
      const base = e.isDirectory() ? e.name : e.name.replace(/\.mdx?$/, '')
      if (!seen.has(base)) order.push(base)
    }

    for (const key of order) {
      const metaValue = meta ? meta[key] : null
      // Skip non-page meta entries (separators, links objects).
      if (metaValue && typeof metaValue === 'object' && metaValue.type && metaValue.type !== 'page') continue

      const childDir = path.join(dir, key)
      const childMdx = path.join(dir, `${key}.mdx`)
      const childMd = path.join(dir, `${key}.md`)

      const isDir = await fs.stat(childDir).then((s) => s.isDirectory()).catch(() => false)
      const mdxExists = await fs.access(childMdx).then(() => true).catch(() => false)
      const mdExists = await fs.access(childMd).then(() => true).catch(() => false)

      const labelFromMeta =
        typeof metaValue === 'string' ? metaValue : metaValue && metaValue.title ? metaValue.title : null

      if (isDir) {
        await visit(childDir, `${urlPrefix}/${key}`, labelFromMeta || key)
        continue
      }

      if (!mdxExists && !mdExists) continue
      const src = await fs.readFile(mdxExists ? childMdx : childMd, 'utf8')
      const parsed = parseMdx(src)
      const title = parsed.title || labelFromMeta || key
      const urlPath = key === 'index' ? urlPrefix || '/' : `${urlPrefix}/${key}`
      const mdPath = key === 'index' ? `${urlPrefix || ''}/index.md` : `${urlPrefix}/${key}.md`
      pages.push({
        slug: key,
        urlPath: urlPath || '/',
        mdPath,
        title,
        sectionTitle,
        body: parsed.body,
        summary: firstParagraph(parsed.body),
      })
    }
  }

  await visit(CONTENT, '', null)
  return pages
}

// Group pages by sectionTitle in encounter order.
function groupBySection(pages) {
  const groups = []
  const byTitle = new Map()
  for (const p of pages) {
    const key = p.sectionTitle || 'Overview'
    if (!byTitle.has(key)) {
      const g = { title: key, pages: [] }
      byTitle.set(key, g)
      groups.push(g)
    }
    byTitle.get(key).pages.push(p)
  }
  return groups
}

async function writeRawMd(pages) {
  for (const p of pages) {
    const out = path.join(PUBLIC, p.mdPath.replace(/^\//, ''))
    await fs.mkdir(path.dirname(out), { recursive: true })
    await fs.writeFile(out, p.body + '\n')
  }
}

async function buildSitemap(pages) {
  // Use the mtime of each page's source .mdx as the lastmod hint.
  const entries = await Promise.all(
    pages.map(async (p) => {
      const candidates = [
        path.join(CONTENT, p.urlPath === '/' ? 'index.mdx' : p.urlPath.replace(/^\//, '') + '.mdx'),
        path.join(CONTENT, p.urlPath.replace(/^\//, '') + '.md'),
      ]
      let lastmod = new Date().toISOString()
      for (const c of candidates) {
        try {
          const st = await fs.stat(c)
          lastmod = st.mtime.toISOString()
          break
        } catch {}
      }
      return { loc: `${SITE}${p.urlPath}`, lastmod }
    }),
  )
  const xml = [
    '<?xml version="1.0" encoding="UTF-8"?>',
    '<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">',
    ...entries.map(
      (e) => `  <url><loc>${e.loc}</loc><lastmod>${e.lastmod}</lastmod></url>`,
    ),
    '</urlset>',
    '',
  ].join('\n')
  return xml
}

function buildLlmsTxt(pages) {
  const groups = groupBySection(pages)
  const lines = [
    '# Squadron',
    '',
    '> Squadron is a declarative framework for building and running multi-agent AI workflows. Agents, tools, models, and missions are defined entirely in HCL configuration — no code required. Squadron handles orchestration, state, dependency resolution, conditional routing, persistence, and resume.',
    '',
    'Squadron supports Anthropic, OpenAI, Gemini, and Ollama models, speaks MCP in both directions (consumes external MCP servers and exposes its own tools as an MCP server), and ships with a built-in plugin system (Go + Python) for extending tool capability.',
    '',
    '## Docs',
    '',
  ]
  for (const g of groups) {
    if (g.title && g.title !== 'Overview') {
      lines.push(`### ${g.title}`)
      lines.push('')
    }
    for (const p of g.pages) {
      const summary = p.summary ? `: ${p.summary}` : ''
      lines.push(`- [${p.title}](${SITE}${p.mdPath})${summary}`)
    }
    lines.push('')
  }
  lines.push('## Source')
  lines.push('')
  lines.push('- [GitHub repository](https://github.com/mlund01/squadron)')
  lines.push('- [Full docs (concatenated)](' + SITE + '/llms-full.txt)')
  lines.push('')
  return lines.join('\n')
}

function buildLlmsFullTxt(pages) {
  const parts = [
    '# Squadron — Full Documentation',
    '',
    `Source: ${SITE}`,
    `Generated: ${new Date().toISOString()}`,
    '',
    'This file contains the complete Squadron documentation as plain markdown,',
    'concatenated for ingestion by LLMs and answer engines.',
    '',
    '---',
    '',
  ]
  for (const p of pages) {
    parts.push(`# ${p.title}`)
    parts.push('')
    parts.push(`Source: ${SITE}${p.urlPath}`)
    parts.push('')
    parts.push(p.body)
    parts.push('')
    parts.push('---')
    parts.push('')
  }
  return parts.join('\n')
}

async function main() {
  const pages = await collectPages()
  console.log(`generate-llms: ${pages.length} pages discovered`)
  await fs.mkdir(PUBLIC, { recursive: true })
  await writeRawMd(pages)
  await fs.writeFile(path.join(PUBLIC, 'llms.txt'), buildLlmsTxt(pages))
  await fs.writeFile(path.join(PUBLIC, 'llms-full.txt'), buildLlmsFullTxt(pages))
  await fs.writeFile(path.join(PUBLIC, 'sitemap.xml'), await buildSitemap(pages))
  console.log('generate-llms: wrote llms.txt, llms-full.txt, sitemap.xml, and raw .md mirrors')
}

main().catch((err) => {
  console.error(err)
  process.exit(1)
})
