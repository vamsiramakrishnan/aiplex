// Command sidecar is a reference request-hook sidecar implementing the
// AIPlex hook contract (aiplex.hooks.v1, see docs/request-hooks.md) with the
// Go standard library only.
//
// It masks employee IDs (EMP- and six digits) as [EMPLOYEE_ID] and denies
// any text containing the word CONFIDENTIAL. It never logs request content.
//
//	go run ./examples/hooks/sidecar -addr 127.0.0.1:5005
//
// With SIDECAR_TOKEN set, requests must carry "Authorization: Bearer <token>"
// (configure the hook with auth_secret_ref naming a secret with that value).
package main

import (
	"crypto/subtle"
	"encoding/json"
	"flag"
	"io"
	"log"
	"net/http"
	"os"
	"regexp"
	"strings"
)

type part struct {
	Index   int    `json:"index"`
	Locator string `json:"locator"`
	Role    string `json:"role"`
	Kind    string `json:"kind"`
	Text    string `json:"text"`
}

type request struct {
	Version string `json:"version"`
	Phase   string `json:"phase"`
	Plane   string `json:"plane"`
	Parts   []part `json:"parts"`
}

type replacement struct {
	Index int    `json:"index"`
	Text  string `json:"text"`
}

type ruleCount struct {
	ID    string `json:"id"`
	Count int    `json:"count"`
}

type answer struct {
	Decision string        `json:"decision"`
	Reason   string        `json:"reason,omitempty"`
	Parts    []replacement `json:"parts,omitempty"`
	Rules    []ruleCount   `json:"rules,omitempty"`
}

var employeeID = regexp.MustCompile(`\bEMP-\d{6}\b`)

// Decide is the sidecar's policy, separate from HTTP so it can be tested.
func Decide(in request) answer {
	masked := 0
	var parts []replacement
	for _, p := range in.Parts {
		if strings.Contains(p.Text, "CONFIDENTIAL") {
			return answer{Decision: "deny", Reason: "confidential", Rules: []ruleCount{{ID: "confidential", Count: 1}}}
		}
		if n := len(employeeID.FindAllStringIndex(p.Text, -1)); n > 0 {
			masked += n
			parts = append(parts, replacement{Index: p.Index, Text: employeeID.ReplaceAllString(p.Text, "[EMPLOYEE_ID]")})
		}
	}
	if len(parts) == 0 {
		return answer{Decision: "allow"}
	}
	return answer{Decision: "transform", Parts: parts, Rules: []ruleCount{{ID: "employee_id", Count: masked}}}
}

func handler(token string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "POST required", http.StatusMethodNotAllowed)
			return
		}
		if token != "" && subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte("Bearer "+token)) != 1 {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		var in request
		if err := json.NewDecoder(io.LimitReader(r.Body, 8<<20)).Decode(&in); err != nil || in.Version != "aiplex.hooks.v1" {
			http.Error(w, "expected an aiplex.hooks.v1 request", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(Decide(in))
	})
}

func main() {
	addr := flag.String("addr", "127.0.0.1:5005", "listen address (keep it on loopback or a private network)")
	flag.Parse()
	log.Printf("hook sidecar listening on http://%s", *addr)
	log.Fatal(http.ListenAndServe(*addr, handler(os.Getenv("SIDECAR_TOKEN"))))
}
