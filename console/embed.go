package console

import (
	"embed"
	"io/fs"
	"strings"
)

// The Console build (`cd console && npm run build`) writes to console/built,
// which is git-ignored, so building never modifies a tracked file. dist holds
// the placeholder page committed to the repository and is never written.
//
//go:embed all:dist
var placeholder embed.FS

//go:embed all:built
var built embed.FS

type overlay struct{}

// Open serves the compiled Console under the "dist" prefix when a build is
// embedded, and the committed placeholder page otherwise.
func (overlay) Open(name string) (fs.File, error) {
	if name == "dist" || strings.HasPrefix(name, "dist/") {
		if _, err := fs.Stat(built, "built/index.html"); err == nil {
			return built.Open("built" + strings.TrimPrefix(name, "dist"))
		}
	}
	return placeholder.Open(name)
}

// Dist contains the built React SPA files under "dist". With no build
// embedded it contains a placeholder page (see consoleBuilt in the CLI).
var Dist fs.FS = overlay{}
