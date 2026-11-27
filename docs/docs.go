package docs

import "embed"

//go:embed index.html openapi.json
var Files embed.FS
