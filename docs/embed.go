// Package docs embeds the Oasis framework documentation for use by the
// MCP docs server and other tools that need access to documentation at runtime.
package docs

import (
	"embed"
	"io/fs"
)

//go:embed external
var embedded embed.FS

// FS contains all documentation files, rooted at the topic level: each topic
// folder holds three files (index.md = concept, api.md = reference,
// examples.md = recipes). The landing page is at index.md.
//
// On disk the files live under docs/external/; FS is a sub-filesystem rooted
// there, so consumer paths are unchanged by the external/internal split.
// Internal contributor-only docs (PHILOSOPHY.md, ENGINEERING.md, benchmarks)
// live under internal/ and are intentionally excluded from the embed.
var FS, _ = fs.Sub(embedded, "external")
