package api

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/vamsiramakrishnan/aiplex/pkg/errorcode"
)

// Guards for the refusal inventory (docs/access-points.md). Every place that
// writes an error of AIPlex's own under /ap/{id}/ passes its code to
// pkg/errorcode, and every code is one the header can carry. A new raw
// http.Error in code the access point reaches fails here, so a refusal is
// never added without a code.

func sourceFiles(t *testing.T, dir string) map[string][]string {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(dir, "*.go"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no sources in %s: %v", dir, err)
	}
	out := map[string][]string{}
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		out[f] = strings.Split(string(raw), "\n")
	}
	return out
}

// A raw http.Error or http.NotFound is allowed only when the code was set on
// one of the lines just above it.
func TestRefusalWritersCarryTheirCode(t *testing.T) {
	// http.Error, http.NotFound, and WriteHeader with a constant status of 400 or
	// more (a refusal written as WriteHeader then Write would otherwise pass).
	raw := regexp.MustCompile(`\bhttp\.(Error|NotFound)\(|WriteHeader\((?:[45][0-9][0-9]|http\.Status(?:Bad|Unauthorized|Payment|Forbidden|NotFound|Method|NotAcceptable|Proxy|Request|Conflict|Gone|Length|Precondition|Unsupported|Unprocessable|TooMany|Internal|NotImplemented|ServiceUnavailable|GatewayTimeout|HTTPVersion)\w*)\)`)
	for _, dir := range []string{".", "../../pkg/modelapi", "../../pkg/toolexec"} {
		for file, lines := range sourceFiles(t, dir) {
			base := filepath.Base(file)
			if dir == "." && !(strings.HasPrefix(base, "access_point") || strings.HasPrefix(base, "client_gateway") || base == "native_key_auth.go" || base == "middleware.go" || base == "respond.go") {
				continue
			}
			for i, line := range lines {
				if !raw.MatchString(line) || strings.HasPrefix(strings.TrimSpace(line), "//") {
					continue
				}
				marked := false
				for j := max(0, i-3); j < i; j++ {
					marked = marked || strings.Contains(lines[j], "errorcode.Set(")
				}
				if !marked {
					t.Errorf("%s:%d writes a refusal without errorcode: %s", file, i+1, strings.TrimSpace(line))
				}
			}
		}
	}
}

// Every code written as a string literal at a refusal site fits the header.
func TestRefusalCodesFitTheHeader(t *testing.T) {
	patterns := []*regexp.Regexp{
		regexp.MustCompile(`\bErrors?\([^,]+, [^,]+, [^,]+, "([^"]+)"`),
		regexp.MustCompile(`errorcode\.(?:Refuse|Set)\([^,]+, (?:[^,"]+, )?"([^"]+)"`),
		regexp.MustCompile(`\b(?:refuseNative|writeRPCError)\(.*?"([A-Z][A-Z0-9_]+)", `),
		regexp.MustCompile(`quotaStatus\([^,]+, "([^"]+)"`),
		regexp.MustCompile(`StatusError\{[^}]*Code: "([^"]+)"`),
		regexp.MustCompile(`backendFailure\{[^,]+, "([^"]+)"`),
		regexp.MustCompile(`\bfail\(w, r, [^,]+, "([^"]+)"`),
		regexp.MustCompile(`\bdenial\("([^"]+)"`),
	}
	seen := 0
	for _, dir := range []string{".", "../../pkg/modelapi", "../../pkg/toolexec"} {
		for file, lines := range sourceFiles(t, dir) {
			for i, line := range lines {
				for _, p := range patterns {
					for _, m := range p.FindAllStringSubmatch(line, -1) {
						seen++
						code := strings.ToUpper(m[1]) // a denial's reason is upper-cased by its writer
						if !errorcode.Valid(code) {
							t.Errorf("%s:%d: %q does not fit %s", file, i+1, m[1], errorcode.Header)
						}
					}
				}
			}
		}
	}
	if seen < 150 {
		t.Fatalf("only %d code literals found: the patterns no longer match the producers", seen)
	}
}

// A quota refusal's code is built from the engine's reason ("QUOTA_" plus the
// upper-cased reason), so every reason the engine can give must fit.
func TestQuotaReasonsFitTheHeader(t *testing.T) {
	reason := regexp.MustCompile(`policyDenied\("([^"]+)"|Reason: *"([^"]+)"`)
	seen := 0
	for file, lines := range sourceFiles(t, "../../pkg/quotaplex") {
		for i, line := range lines {
			for _, m := range reason.FindAllStringSubmatch(line, -1) {
				value := m[1] + m[2]
				if strings.ContainsAny(value, " :") {
					continue // prose, not a reason token
				}
				seen++
				if code := "QUOTA_" + strings.ToUpper(value); !errorcode.Valid(code) {
					t.Errorf("%s:%d: reason %q gives code %q", file, i+1, value, code)
				}
			}
		}
	}
	if seen == 0 {
		t.Fatal("no quota reasons found")
	}
}

// Only pkg/errorcode writes the header; every other file reads or names it.
func TestOnlyErrorcodeSetsTheHeader(t *testing.T) {
	writes := regexp.MustCompile(`Header(\(\))?\.(Set|Add)\((errorcode\.Header|"X-Aiplex-Error-Code")|\[(errorcode\.Header|"X-Aiplex-Error-Code")\]\s*=`)
	for _, dir := range []string{".", "../../pkg/modelapi", "../../pkg/toolexec", "../../cmd/aiplex-api"} {
		for file, lines := range sourceFiles(t, dir) {
			for i, line := range lines {
				if writes.MatchString(line) {
					t.Errorf("%s:%d sets the header directly: %s", file, i+1, strings.TrimSpace(line))
				}
			}
		}
	}
}
