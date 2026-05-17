# Docs Reorganization Plan (executed)

**Status:** Shipped on the same branch as Tier 2 (PR #96). All bullets below describe the executed end state — see commits on `claude/aeo-tier-2-content`.

---



The Tier 2 AEO pages were grafted onto the existing nav and the seams show. This plan reorganizes the docs so visitors find a coherent path, without breaking any AEO-critical URLs.

---

## What's wrong now

Current top-level nav (after Tier 2):

```
Introduction
What is Squadron?
Getting Started
CLI Commands
Configuration
Missions
Advanced
Compare
No-Code Guide
Declarative Framework
FAQ
```

Problems:

1. **Introduction and What is Squadron? duplicate.** Both define the product. A first-time visitor doesn't know which to read; both have a definitional opening.
2. **AEO landing pages are interleaved with reference docs.** "No-Code Guide" lives between "Compare" and "Declarative Framework", which sits next to "FAQ". Each is good on its own, but as a list they read as a grab-bag.
3. **"Advanced" is one page.** It currently holds only [Distributing Plugins](docs/content/advanced/distributing-plugins.mdx). A single-page section is noise in the nav.
4. **No funnel-stage groupings.** Someone evaluating Squadron (Compare, FAQ) is reading the same nav as someone deep in build mode (Configuration, Missions). The nav doesn't signal which sections to read in which order.
5. **The Harness page is buried in Missions.** [`missions/harness`](docs/content/missions/harness.mdx) is a conceptual explainer about the runtime model — closer in kind to "What is Squadron?" than to "Tasks" or "Routing". Its current placement makes it easy to miss for evaluators.

## Goals

- Visitors land on a coherent first page. No duplicate "what is this" pages staring at each other in the nav.
- Evaluator content (what-is, comparisons, FAQ, declarative-framework) is grouped together and discoverable, but not interleaved with reference docs.
- Builder content (missions, config, CLI, guides) is grouped together.
- **No AEO-critical URLs change.** Pages already in [llms.txt](docs/public/llms.txt), [sitemap.xml](docs/public/sitemap.xml), and the [Tier 2 PR description](https://github.com/mlund01/squadron/pull/96) must keep their current paths. Any reorg happens through `_meta.js`, not file moves.
- Nav uses **section separators** so groupings are visible without nesting URLs (which would change paths).

## Proposed top-level nav

Nextra v4 supports separator entries in `_meta.js` of the form `{ type: 'separator', title: '...' }`. Use them to create visual section headings in the sidebar without altering URLs.

```
LEARN
├── Introduction              # short docs home — routes to evaluator + builder paths
├── What is Squadron?         # canonical definitional answer page
├── Declarative Framework     # category-defining conceptual page
└── Getting Started/          # install, quickstart, docker

BUILD
├── Missions/                 # overview, harness, tasks, routing, iteration, datasets, folders, schedules, budgets, internal-tools
├── Configuration/            # variables, models, agents, skills, tools, functions, plugins, mcp, gateways, command_center
└── Guides
    ├── No-Code Multi-Agent Workflow   # currently /no-code-multi-agent-workflow
    └── Distributing Plugins           # currently /advanced/distributing-plugins

REFERENCE
└── CLI Reference/            # rename "CLI Commands"

EVALUATE
├── Compare/                  # 4 vs pages
└── FAQ
```

### What changes mechanically

| Concern | Action |
|---|---|
| Section headings | Add `{ type: 'separator', title: 'Learn' }`-style entries to top-level `_meta.js`. |
| Rename "CLI Commands" → "CLI Reference" | Label change in `_meta.js` only. URL `/cli/...` unchanged. |
| Fold "Advanced" into Guides | Move `distributing-plugins.mdx` from `content/advanced/` into a new `content/guides/` directory. **This changes its URL** — see URL-preservation section. |
| Promote "The Harness" | Add a top-level alias to it from the Learn section in nav, or rewrite the Introduction page so it links prominently. Don't move the file. |
| Group `no-code-multi-agent-workflow` and the moved-`distributing-plugins` into a new "Guides" bucket | Either (a) leave file at top level and have nav say "Guides" as a separator that contains the single page link, or (b) move into `content/guides/` directory. Option (b) is cleaner but changes the URL. |
| De-duplicate Introduction and What is Squadron? | Rewrite `index.mdx` to be a short docs home (3 paragraphs + 4 CTA cards). All definitional content lives at `/what-is-squadron`. |

### URL preservation

These URLs must NOT change (they're in `llms.txt`, `sitemap.xml`, the FAQPage JSON-LD, internal cross-links, and the merged PR #95):

- `/`
- `/what-is-squadron`
- `/declarative-ai-agent-framework`
- `/no-code-multi-agent-workflow`
- `/faq`
- `/compare/{langgraph,crewai,autogen,n8n}`
- `/getting-started/{installation,quickstart,docker}`
- `/cli/{quickstart,engage,disengage,verify,chat,mission,vars,upgrade}`
- `/config/*` (all current files)
- `/missions/*` (all current files)

URLs we **could** change (recent, not yet externally shared):

- `/advanced/distributing-plugins` — only one inbound external link risk; safe to move

Recommendation: leave **all** AEO and reference URLs alone. Use `_meta.js` separators for the nav structure. The one optional move is `advanced/distributing-plugins` → `guides/distributing-plugins`, which we can ship with a Next.js redirect to keep the old URL alive.

### Content changes (no URL impact)

1. **Rewrite [`index.mdx`](docs/content/index.mdx) as a short docs home.** Target: ~25 lines. One opening paragraph, four CTA cards (What is Squadron? / Quick Start / Missions / Compare), and a single short code example. Move the long-form "what is / how it works / what's in the box" content into [`what-is-squadron.mdx`](docs/content/what-is-squadron.mdx), which already has most of it.
2. **Promote [`The Harness`](docs/content/missions/harness.mdx) into the Learn section** without moving the file — add it to top-level `_meta.js` (Nextra supports this; a key like `'missions/harness'` in the parent meta references the nested file). If that's not possible, link to it prominently from `what-is-squadron.mdx`.
3. **Add a short paragraph cross-reference** at the top of [`declarative-ai-agent-framework.mdx`](docs/content/declarative-ai-agent-framework.mdx) and [`what-is-squadron.mdx`](docs/content/what-is-squadron.mdx) pointing to each other and to the FAQ, so the three landing pages function as a cohesive set.
4. **Move [`advanced/distributing-plugins.mdx`](docs/content/advanced/distributing-plugins.mdx) to `guides/`** and add a redirect from `/advanced/distributing-plugins` to `/guides/distributing-plugins`. Delete the empty `advanced/` directory.

## Final proposed `docs/content/_meta.js`

```js
export default {
  '__learn': { type: 'separator', title: 'Learn' },
  index: 'Introduction',
  'what-is-squadron': 'What is Squadron?',
  'declarative-ai-agent-framework': 'Declarative Framework',
  'getting-started': 'Getting Started',

  '__build': { type: 'separator', title: 'Build' },
  missions: 'Missions',
  config: 'Configuration',
  guides: 'Guides',

  '__reference': { type: 'separator', title: 'Reference' },
  cli: 'CLI Reference',

  '__evaluate': { type: 'separator', title: 'Evaluate' },
  compare: 'Compare',
  faq: 'FAQ',

  // The 'no-code-multi-agent-workflow' page becomes guides/no-code-multi-agent-workflow.mdx
  // (folded into the Guides directory). Its URL changes; add a redirect from the old path.
  // Alternatively keep top-level and remove from guides/ — see open decision below.
}
```

## Open decisions

1. **Should `no-code-multi-agent-workflow` keep its top-level URL or move under `/guides/`?** Top-level is better for AEO (shorter URL, "guides" prefix is forgettable). But then Guides is a one-item directory (only distributing-plugins). Recommendation: **keep at top level, list in nav under the Guides separator without nesting the URL**. This requires referencing the file from a parent `_meta.js` — Nextra supports cross-directory references in `_meta.js` via the same key syntax used for nested directories.
2. **Do we want a `/concepts/` directory?** Folding what-is-squadron, declarative-ai-agent-framework, and the harness under `/concepts/` would be conceptually clean — but it changes URLs. **Recommendation: no, keep all at top level.** Use the Learn separator for nav grouping only.
3. **How short should the new Introduction page be?** Recommendation: 25–40 lines. Definition + 4 CTA cards + one example. All long-form content lives at `/what-is-squadron`.
4. **Do we keep "Advanced" as an empty bucket for future use?** Recommendation: **no, delete it.** It signals nothing meaningful. Re-introduce later if we have ≥3 advanced pages.

## Migration sequence

1. Verify Nextra v4 separator syntax in this codebase (one quick test in a feature branch).
2. Rewrite `index.mdx` to be a short docs home — move detail into `what-is-squadron.mdx` where it already lives.
3. Move `advanced/distributing-plugins.mdx` → `guides/distributing-plugins.mdx`. Add Next.js redirect for the old URL.
4. Update `docs/content/_meta.js` with the new separator-based structure.
5. Add cross-references between the three Learn-section landing pages.
6. Regenerate `llms.txt` (the generator already picks up the file moves automatically).
7. Build, click through every section heading, verify all URLs in the URL-preservation list still resolve.
8. Open PR with screenshots of before/after nav.

## What this does *not* do

- Doesn't change page content beyond `index.mdx` shortening and small cross-reference links.
- Doesn't add new AEO pages (those continue in Tier 3+).
- Doesn't reorganize per-section `_meta.js` within Configuration or Missions — those are well-ordered already.
- Doesn't break any URL that appears in `llms.txt`, `sitemap.xml`, the FAQPage JSON-LD, or external references.
