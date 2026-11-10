package agentfederation

import (
	"fmt"
	"time"
)

// The helpers below expose the token parsing and strict-value rules of the
// agent identity engine so sibling federation packages (user identity
// providers) apply exactly the same rules instead of re-implementing them.

// ParseCompactJWT parses an unverified compact JWS (RS256/ES256 with a key ID)
// and returns its payload claims with numbers as json.Number. problem is
// non-empty, and safe to show, when the token is not acceptable.
func ParseCompactJWT(token string) (claims map[string]any, alg, problem string) {
	parsed, problem := parseToken(token)
	return parsed.claims, parsed.alg, problem
}

// Audiences returns the string audiences of an aud claim (string or array).
func Audiences(raw any) []string { return audiences(raw) }

// NumericDate reads a numeric date claim.
func NumericDate(raw any) (int64, bool) { return numeric(raw) }

// Clip bounds echoed identity values.
func Clip(s string) string { return clip(s) }

// Contains reports whether values includes v.
func Contains(values []string, v string) bool { return contains(values, v) }

// ExactValue rejects empty, padded, over-long or wildcard values.
func ExactValue(label, value string) error { return exactValue(label, value) }

// ValidAudience requires an explicit, wildcard-free audience.
func ValidAudience(aud string) error { return validAudience(aud) }

// ValidHTTPSURL requires an exact https URL without credentials, query or fragment.
func ValidHTTPSURL(raw, label string, issuer bool) error { return validHTTPSURL(raw, label, issuer) }

// ValidClaimName reports whether name is an acceptable, non-reserved claim name.
func ValidClaimName(name string) bool { return claimName.MatchString(name) && !reservedClaims[name] }

// ShellQuote single-quotes a value for POSIX shells.
func ShellQuote(s string) string { return shellQuote(s) }

// JSONString quotes a value for embedding in rendered JSON or YAML.
func JSONString(s string) string { return jsonString(s) }

// LifetimeProblem applies the exp/nbf/iat policy shared by federation engines.
// It returns a non-empty problem when the token is outside its validity window,
// otherwise the expiry.
func LifetimeProblem(claims map[string]any, now time.Time, skew time.Duration) (problem string, exp int64) {
	exp, hasExp := numeric(claims["exp"])
	switch {
	case !hasExp:
		return "token has no numeric exp claim", 0
	case now.Unix() >= exp:
		return fmt.Sprintf("token expired %s ago (exp %s)", now.Sub(time.Unix(exp, 0)).Round(time.Second), time.Unix(exp, 0).UTC().Format(time.RFC3339)), exp
	}
	if raw, ok := claims["nbf"]; ok {
		nbf, valid := numeric(raw)
		if !valid {
			return "nbf claim is not a number", exp
		} else if now.Add(skew).Unix() < nbf {
			return fmt.Sprintf("token is not valid until %s", time.Unix(nbf, 0).UTC().Format(time.RFC3339)), exp
		}
	}
	if raw, ok := claims["iat"]; ok {
		iat, valid := numeric(raw)
		if !valid {
			return "iat claim is not a number", exp
		} else if now.Add(skew).Unix() < iat {
			return "token was issued in the future (check the workload's clock)", exp
		}
	}
	return "", exp
}
