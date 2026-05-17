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

// Remove MDX-only syntax that's noise in raw markdown: `{/* ... */}` comments
// and any inline `<script>...</script>` or self-closing `<script ... />` blocks
// (typically JSON-LD payloads). The self-closing case is parsed by counting
// braces so we don't truncate at a `/` that lives inside a string literal.
function stripScriptTags(body) {
  let out = ''
  let i = 0
  while (i < body.length) {
    const open = body.indexOf('<script', i)
    if (open === -1) {
      out += body.slice(i)
      break
    }
    out += body.slice(i, open)
    // Walk forward tracking brace depth so we don't bail at a `/` inside a
    // JSX expression like `JSON.stringify({...})`.
    let depth = 0
    let j = open + '<script'.length
    while (j < body.length) {
      const ch = body[j]
      if (ch === '{') depth++
      else if (ch === '}') depth--
      else if (depth === 0 && ch === '/' && body[j + 1] === '>') {
        j += 2
        break
      } else if (depth === 0 && ch === '<' && body.slice(j, j + 9).toLowerCase() === '</script>') {
        j += '</script>'.length
        break
      }
      j++
    }
    i = j
  }
  return out
}

function stripMdxisms(body) {
  return stripScriptTags(body.replace(/\{\/\*[\s\S]*?\*\/\}/g, '')).replace(/^\n+/, '')
}

// Strip frontmatter and return { title, body }. Title preference: frontmatter
// `title:` → first `# ` heading → null. Body is cleaned of MDX-only syntax so
// it reads as plain markdown.
function parseMdx(src) {
  let body = src
  let frontmatterTitle = null
  const fmMatch = src.match(/^---\n([\s\S]*?)\n---\n?/)
  if (fmMatch) {
    body = src.slice(fmMatch[0].length)
    const t = fmMatch[1].match(/^title:\s*(.+)$/m)
    if (t) frontmatterTitle = t[1].replace(/^['"]|['"]$/g, '').trim()
  }
  body = stripMdxisms(body)
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

    // Track the current section title as we walk this directory. A
    // `{ type: 'separator', title: 'Learn' }` entry shifts the section so
    // subsequent pages in the same directory get that grouping in llms.txt.
    let currentSection = sectionTitle

    for (const key of order) {
      const metaValue = meta ? meta[key] : null
      if (metaValue && typeof metaValue === 'object' && metaValue.type === 'separator') {
        if (metaValue.title) currentSection = metaValue.title
        continue
      }
      // Skip other non-page meta entries (links, menus).
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
        // For directories, the child's section is the directory's label (e.g.
        // "Missions"), but for directories that sit under a separator group
        // (e.g. a Guides directory under the Build separator), the directory
        // label is more specific and wins.
        await visit(childDir, `${urlPrefix}/${key}`, labelFromMeta || currentSection || key)
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
        sectionTitle: currentSection,
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

// Remove .md mirrors from PUBLIC that no longer correspond to a current page.
// Without this, renaming or moving a content file leaves a stale mirror behind
// that ships to production until someone notices.
async function pruneStaleMirrors(pages) {
  const valid = new Set(pages.map((p) => path.join(PUBLIC, p.mdPath.replace(/^\//, ''))))
  async function walk(dir) {
    let entries
    try {
      entries = await fs.readdir(dir, { withFileTypes: true })
    } catch {
      return
    }
    for (const e of entries) {
      const full = path.join(dir, e.name)
      if (e.isDirectory()) {
        await walk(full)
        // Best-effort: remove the dir if it's empty after pruning.
        try {
          await fs.rmdir(full)
        } catch {}
      } else if (e.name.endsWith('.md') && !valid.has(full)) {
        await fs.unlink(full)
      }
    }
  }
  await walk(PUBLIC)
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
  await pruneStaleMirrors(pages)
  await fs.writeFile(path.join(PUBLIC, 'llms.txt'), buildLlmsTxt(pages))
  await fs.writeFile(path.join(PUBLIC, 'llms-full.txt'), buildLlmsFullTxt(pages))
  await fs.writeFile(path.join(PUBLIC, 'sitemap.xml'), await buildSitemap(pages))
  console.log('generate-llms: wrote llms.txt, llms-full.txt, sitemap.xml, and raw .md mirrors')
}

main().catch((err) => {
  console.error(err)
  process.exit(1)
})
