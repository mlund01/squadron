package docs

import "embed"

//go:embed pages/**/*.md pages/*.md
var DocsFS embed.FS
