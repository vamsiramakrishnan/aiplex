package main

import (
	"os/exec"
	"runtime"
)

// openBrowser opens a URL in the user's default browser.
// Returns an error if the browser cannot be opened, but callers
// should treat this as non-fatal (user can open URL manually).
func openBrowser(url string) error {
	switch runtime.GOOS {
	case "darwin":
		return exec.Command("open", url).Start()
	case "windows":
		return exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Start()
	default: // linux, freebsd, etc.
		return exec.Command("xdg-open", url).Start()
	}
}
