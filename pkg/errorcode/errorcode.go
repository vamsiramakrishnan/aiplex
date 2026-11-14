// Package errorcode carries AIPlex's own code for a refusal to the place that
// writes the response.
//
// A reverse proxy in front of an access point must tell a refusal AIPlex
// produced from a provider's error that AIPlex relayed. AIPlex marks its own
// refusals with the X-Aiplex-Error-Code response header and never marks a
// relayed one. The producer of a refusal passes the code as a value (Set,
// Refuse); nothing parses a code back out of a message.
package errorcode

import (
	"net/http"
	"strings"

	"github.com/rs/zerolog/log"
)

// Header is the response header that carries the code of a refusal that AIPlex
// itself produced.
const Header = "X-Aiplex-Error-Code"

// Unspecified is written when a producer passes a code that does not fit the
// header's format. The header must never carry a malformed value, and must
// never be left off a refusal AIPlex produced, so a proxy still sees AIPlex's
// decision. A producer that causes it has a bug: tests check the codes the
// producers use.
const Unspecified = "UNSPECIFIED"

// MaxLength is the longest code the header carries.
const MaxLength = 64

// Valid reports whether code is 1 to 64 characters of A-Z, 0-9 and underscore.
func Valid(code string) bool {
	if code == "" || len(code) > MaxLength {
		return false
	}
	for i := 0; i < len(code); i++ {
		c := code[i]
		if !(c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_') {
			return false
		}
	}
	return true
}

// Set is the only place that decides the header's value. It marks a response as
// a refusal AIPlex produced, with its code in upper case. A code that is empty,
// longer than 64 characters or outside A-Z 0-9 _ after upper-casing is never
// sent: the header carries Unspecified instead (a refusal AIPlex produced is
// never left without the header) and an error is logged, because the producer
// has a bug to fix.
func Set(h http.Header, code string) {
	upper := strings.ToUpper(code)
	if !Valid(upper) {
		log.Error().Int("length", len(code)).Msg("refusal code does not fit X-Aiplex-Error-Code; sent as UNSPECIFIED")
		upper = Unspecified
	}
	h.Set(Header, upper)
}

// Refuse writes a plain-text refusal, exactly as http.Error does, and marks
// it with its code. The body is message alone; the code travels in the header
// and, where a caller renders one, in its own structured field.
func Refuse(w http.ResponseWriter, status int, code, message string) {
	Set(w.Header(), code)
	http.Error(w, message, status)
}
