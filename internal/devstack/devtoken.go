package devstack

import (
	"crypto/ecdsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/vamsiramakrishnan/aiplex/internal/auth"
)

// DevTokenPath is the loopback-only endpoint on the local token issuer that
// mints a token for another caller. It lives on the issuer (whose key is in the
// `aiplex dev` process), never on the API.
const DevTokenPath = "/dev/token"

// CallerTokenTTL is how long tokens minted for other callers last.
const CallerTokenTTL = time.Hour

// DevDeployerGroup is bound to the deployer role by the local role bindings.
const DevDeployerGroup = "aiplex-local-deployers"

// DevTokenRequest asks the issuer for a token for another local caller.
type DevTokenRequest struct {
	User   string   `json:"user"`
	Role   string   `json:"role,omitempty"` // none (default), deployer or admin
	Groups []string `json:"groups,omitempty"`
}

// DevTokenResponse is the issuer's answer. The token is the only secret.
type DevTokenResponse struct {
	Token       string   `json:"token"`
	Subject     string   `json:"subject"`
	Email       string   `json:"email"`
	PrincipalID string   `json:"principal_id"`
	Role        string   `json:"role"`
	Groups      []string `json:"groups"`
	ExpiresIn   int64    `json:"expires_in"`
}

var (
	devUserPattern  = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,62}$`)
	devGroupPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)
)

// MintCallerToken builds the claims for another local caller and signs them.
// The role is expressed as membership of the group the local role bindings
// map to it, exactly as a real identity provider would.
func (s *Stack) MintCallerToken(req DevTokenRequest) (*DevTokenResponse, error) {
	user := strings.TrimSpace(req.User)
	if !devUserPattern.MatchString(user) {
		return nil, fmt.Errorf("user must be 1 to 63 lowercase letters, digits, dots, dashes or underscores, starting with a letter or digit")
	}
	if user == DevSubject {
		return nil, fmt.Errorf("%q is the developer you already are; choose another name", DevSubject)
	}
	role := req.Role
	if role == "" {
		role = "none"
	}
	groups := []string{}
	switch role {
	case "none":
	case "deployer":
		groups = append(groups, DevDeployerGroup)
	case "admin":
		groups = append(groups, DevGroup)
	default:
		return nil, fmt.Errorf("role must be none, deployer or admin")
	}
	for _, g := range req.Groups {
		if g = strings.TrimSpace(g); g == "" {
			continue
		}
		if !devGroupPattern.MatchString(g) {
			return nil, fmt.Errorf("invalid group %q", g)
		}
		if g == DevGroup || g == DevDeployerGroup {
			return nil, fmt.Errorf("group %q is reserved; use --role instead", g)
		}
		groups = append(groups, g)
	}
	if len(groups) > 20 {
		return nil, fmt.Errorf("at most 20 groups")
	}
	email := user + "@localhost"
	token, err := s.Issuer.Mint(map[string]any{"sub": user, "email": email, "name": user, "groups": groups}, CallerTokenTTL)
	if err != nil {
		return nil, err
	}
	return &DevTokenResponse{Token: token, Subject: user, Email: email, PrincipalID: auth.CanonicalPrincipalID(s.IssuerURL, DevPoolID, user), Role: role, Groups: groups, ExpiresIn: int64(CallerTokenTTL.Seconds())}, nil
}

// issuerHandler is the issuer's JWKS and discovery handler plus DevTokenPath.
func (s *Stack) issuerHandler() http.Handler {
	base := s.Issuer.Handler()
	mux := http.NewServeMux()
	mux.Handle("/", base)
	mux.HandleFunc(DevTokenPath, s.serveDevToken)
	return mux
}

func (s *Stack) serveDevToken(w http.ResponseWriter, r *http.Request) {
	fail := func(status int, msg string) {
		writeJSON(w, status, map[string]any{"error": msg})
	}
	if r.Method != http.MethodPost {
		fail(http.StatusMethodNotAllowed, "POST only")
		return
	}
	// Loopback peers only, and nothing that was forwarded from elsewhere.
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if ip := net.ParseIP(host); err != nil || ip == nil || !ip.IsLoopback() || r.Header.Get("X-Forwarded-For") != "" || r.Header.Get("Forwarded") != "" {
		fail(http.StatusForbidden, "the token endpoint answers loopback clients only")
		return
	}
	// The caller must hold the developer's own token: a token for another
	// caller cannot mint more tokens.
	bearer := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	claims, err := s.Issuer.verifyOwn(bearer)
	if err != nil || claims["sub"] != DevSubject || !containsString(claims["groups"], DevGroup) {
		fail(http.StatusUnauthorized, "send the developer token (aiplex dev env) as a bearer token")
		return
	}
	var req DevTokenRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&req); err != nil {
		fail(http.StatusBadRequest, "invalid request body")
		return
	}
	resp, err := s.MintCallerToken(req)
	if err != nil {
		fail(http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func containsString(v any, want string) bool {
	list, _ := v.([]any)
	for _, e := range list {
		if s, _ := e.(string); s == want {
			return true
		}
	}
	return false
}

// verifyOwn checks a compact JWT against this issuer's own key and returns its
// claims. It accepts only tokens this issuer signed, still within their lifetime.
func (i *Issuer) verifyOwn(token string) (map[string]any, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil, errors.New("not a token")
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil || len(sig) != 64 {
		return nil, errors.New("bad signature")
	}
	digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if !ecdsa.Verify(&i.key.PublicKey, digest[:], new(big.Int).SetBytes(sig[:32]), new(big.Int).SetBytes(sig[32:])) {
		return nil, errors.New("bad signature")
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, err
	}
	var claims map[string]any
	if err := json.Unmarshal(raw, &claims); err != nil {
		return nil, err
	}
	if exp, _ := claims["exp"].(float64); int64(exp) <= time.Now().Unix() {
		return nil, errors.New("expired")
	}
	if claims["iss"] != i.issuer || claims["aud"] != i.audience {
		return nil, errors.New("wrong issuer or audience")
	}
	return claims, nil
}
