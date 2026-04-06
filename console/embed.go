package console

import "embed"

// Dist contains the built React SPA files.
// In development, this contains a placeholder.
// For production, build first: cd console && npm run build
//
//go:embed all:dist
var Dist embed.FS
