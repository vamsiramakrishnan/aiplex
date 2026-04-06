package console

import "embed"

// Dist contains the built React SPA.
// Requires: cd console && npm run build
//
//go:embed all:dist
var Dist embed.FS
