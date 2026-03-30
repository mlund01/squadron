package docs

import "embed"

//go:embed content/**/*.mdx content/*.mdx
var DocsFS embed.FS
