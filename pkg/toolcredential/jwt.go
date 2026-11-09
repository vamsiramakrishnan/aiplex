package toolcredential

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"time"
)

// jwtExpiry reads the unverified exp claim of a token this process obtained
// itself, only to bound how long it is cached. It is never an authorization
// decision; an unreadable token is simply not cached.
func jwtExpiry(token string) (time.Time, bool) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return time.Time{}, false
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return time.Time{}, false
	}
	var claims struct {
		Exp *int64 `json:"exp"`
	}
	if json.Unmarshal(payload, &claims) != nil || claims.Exp == nil || *claims.Exp <= 0 {
		return time.Time{}, false
	}
	return time.Unix(*claims.Exp, 0), true
}
