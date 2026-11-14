package toolauth

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/vamsiramakrishnan/aiplex/pkg/endpointpolicy"
)

type registrationGate struct {
	gate       chan struct{}
	references int
}

type metadataCacheEntry struct {
	metadata ClientMetadata
	expires  time.Time
}
type Service struct {
	registrations          map[CredentialKey]*registrationGate
	client                 *http.Client
	credentials            CredentialStore
	registrationAuthorizer RegistrationAuthorizer
	preRegistered          map[CredentialKey]Registration
	documents              map[string]ClientMetadata
	network                *endpointpolicy.Policy
	allowLoopback          bool
	cacheTTL               time.Duration
	mu                     sync.Mutex
	cache                  map[string]metadataCacheEntry
}

func NewService(cfg Config) (*Service, error) {
	network, e := endpointpolicy.New(cfg.Network)
	if e != nil {
		return nil, e
	}
	if cfg.MetadataCacheTTL == 0 {
		cfg.MetadataCacheTTL = 5 * time.Minute
	}
	if cfg.MetadataCacheTTL < 0 || cfg.MetadataCacheTTL > time.Hour {
		return nil, fmt.Errorf("metadata cache TTL must be between zero and one hour")
	}
	s := &Service{client: network.Client(), network: network, credentials: cfg.Credentials, registrationAuthorizer: cfg.RegistrationAuthorizer, preRegistered: map[CredentialKey]Registration{}, documents: map[string]ClientMetadata{}, allowLoopback: cfg.Network.AllowLoopback, cacheTTL: cfg.MetadataCacheTTL, cache: map[string]metadataCacheEntry{}, registrations: map[CredentialKey]*registrationGate{}}
	for _, r := range cfg.PreRegistered {
		if r.TenantID == "" || r.ProfileID == "" || r.ClientID == "" {
			return nil, fmt.Errorf("pre-registration requires tenant, profile and client ID")
		}
		if e = s.allowedURL(r.Issuer); e != nil {
			return nil, e
		}
		for _, uri := range r.RedirectURIs {
			if e = ValidateRedirectURI(uri); e != nil {
				return nil, e
			}
		}
		key := CredentialKey{r.TenantID, r.Issuer, r.ProfileID}
		if _, ok := s.preRegistered[key]; ok {
			return nil, fmt.Errorf("duplicate issuer-bound pre-registration")
		}
		r.Mode = PreRegistered
		r.RedirectURIs = append([]string(nil), r.RedirectURIs...)
		s.preRegistered[key] = r
	}
	for _, m := range cfg.MetadataDocuments {
		if e = validateMetadata(m); e != nil {
			return nil, e
		}
		if _, ok := s.documents[m.ClientID]; ok {
			return nil, fmt.Errorf("duplicate CIMD client_id")
		}
		if m.TokenEndpointAuthMethod == "" {
			m.TokenEndpointAuthMethod = "none"
		}
		if len(m.GrantTypes) == 0 {
			m.GrantTypes = []string{"authorization_code"}
		}
		if len(m.ResponseTypes) == 0 {
			m.ResponseTypes = []string{"code"}
		}
		s.documents[m.ClientID] = cloneMetadata(m)
	}
	return s, nil
}
func originOf(u *url.URL) string               { return strings.ToLower(u.Scheme) + "://" + strings.ToLower(u.Host) }
func (s *Service) allowedURL(raw string) error { return s.network.ValidateURL(raw) }
func (s *Service) fetchJSON(ctx context.Context, raw string, target any) (*http.Response, error) {
	if e := s.allowedURL(raw); e != nil {
		return nil, e
	}
	req, e := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
	if e != nil {
		return nil, e
	}
	req.Header.Set("Accept", "application/json")
	resp, e := s.client.Do(req)
	if e != nil {
		return nil, fmt.Errorf("OAuth metadata fetch failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return resp, fmt.Errorf("OAuth metadata returned HTTP %d", resp.StatusCode)
	}
	if e = decodeBounded(resp.Body, target); e != nil {
		return resp, e
	}
	return resp, nil
}
func decodeBounded(reader io.Reader, target any) error {
	payload, e := io.ReadAll(io.LimitReader(reader, (1<<20)+1))
	if e != nil {
		return fmt.Errorf("OAuth response read failed")
	}
	if len(payload) > 1<<20 {
		return fmt.Errorf("OAuth response exceeds 1MiB")
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	if e = decoder.Decode(target); e != nil {
		return fmt.Errorf("OAuth response is not valid JSON")
	}
	if e = decoder.Decode(new(any)); e != io.EOF {
		return fmt.Errorf("OAuth response contains trailing data")
	}
	return nil
}

// Prepare obtains an issuer-bound OAuth client registration. It does not issue
// access tokens, authenticate a user or implement the authorization-code flow.
// The returned endpoints require PKCE S256 and the resource parameter.
func (s *Service) Prepare(ctx context.Context, connection Connection, profile ClientProfile) (Registration, error) {
	if connection.TenantID == "" || connection.ID == "" {
		return Registration{}, fmt.Errorf("tool connection requires tenant and id")
	}
	if e := validateProfile(profile); e != nil {
		return Registration{}, e
	}
	prm, e := s.DiscoverResource(ctx, connection.ResourceURL, connection.Challenge)
	if e != nil {
		return Registration{}, e
	}
	issuer := connection.Issuer
	if issuer == "" {
		if len(prm.AuthorizationServers) != 1 {
			return Registration{}, fmt.Errorf("select an approved authorization server explicitly")
		}
		issuer = prm.AuthorizationServers[0]
	}
	found := false
	for _, v := range prm.AuthorizationServers {
		if v == issuer {
			found = true
		}
	}
	if !found {
		return Registration{}, fmt.Errorf("selected issuer is not advertised by the resource")
	}
	metadata, e := s.DiscoverAuthorizationServer(ctx, issuer)
	if e != nil {
		return Registration{}, e
	}
	if !slices.Contains(metadata.CodeChallengeMethodsSupported, "S256") {
		return Registration{}, fmt.Errorf("authorization server does not advertise PKCE S256")
	}
	result := Registration{TenantID: connection.TenantID, ProfileID: profile.ID, Issuer: issuer, Resource: connection.ResourceURL, RedirectURIs: append([]string(nil), profile.RedirectURIs...), AuthorizationEndpoint: metadata.AuthorizationEndpoint, TokenEndpoint: metadata.TokenEndpoint, ResourceParameter: connection.ResourceURL, PKCEMethod: "S256", RequireIssuer: metadata.AuthorizationResponseIssuerSupported, AllowRefresh: profile.RefreshTokens}
	key := CredentialKey{connection.TenantID, issuer, profile.ID}
	if existing, ok := s.preRegistered[key]; ok {
		if !sameStrings(existing.RedirectURIs, profile.RedirectURIs) {
			return Registration{}, fmt.Errorf("pre-registered redirect URI mismatch")
		}
		result.Mode = PreRegistered
		result.ClientID = existing.ClientID
		result.CredentialRef = existing.CredentialRef
		return result, nil
	}
	if metadata.CIMDSupported && profile.MetadataURL != "" {
		document, ok := s.documents[profile.MetadataURL]
		if !ok {
			return Registration{}, fmt.Errorf("CIMD profile must reference a configured hosted metadata document")
		}
		if !sameStrings(document.RedirectURIs, profile.RedirectURIs) || document.ClientName != profile.Name {
			return Registration{}, fmt.Errorf("CIMD metadata does not match client profile")
		}
		if profile.RefreshTokens && !slices.Contains(document.GrantTypes, "refresh_token") {
			return Registration{}, fmt.Errorf("refresh-token use requires matching CIMD grant_types")
		}
		result.Mode = CIMD
		result.ClientID = document.ClientID
		return result, nil
	}
	if metadata.RegistrationEndpoint == "" {
		return Registration{}, &RegistrationError{Code: "registration_required", Description: "authorization server supports no configured registration mechanism"}
	}
	if s.credentials == nil {
		return Registration{}, fmt.Errorf("DCR requires a secure credential store")
	}
	release, err := s.lockRegistration(ctx, key)
	if err != nil {
		return Registration{}, err
	}
	defer release()
	if credential, ref, e := s.credentials.Get(ctx, key); e == nil {
		if credential.ClientID == "" || ref == "" {
			return Registration{}, fmt.Errorf("stored registration is incomplete")
		}
		if !sameStrings(credential.RedirectURIs, profile.RedirectURIs) {
			return Registration{}, fmt.Errorf("stored registration redirect URI mismatch")
		}
		if profile.RefreshTokens && !slices.Contains(credential.GrantTypes, "refresh_token") {
			return Registration{}, fmt.Errorf("stored DCR registration lacks refresh_token grant; configure a newly registered client profile")
		}
		if credential.SecretExpiresAt == 0 || credential.SecretExpiresAt > time.Now().Unix() {
			result.Mode = DCR
			result.ClientID = credential.ClientID
			result.CredentialRef = ref
			return result, nil
		}
	} else if !errors.Is(e, ErrCredentialsNotFound) {
		return Registration{}, fmt.Errorf("registration credential lookup failed")
	}
	clientMetadata := ClientMetadata{ClientName: profile.Name, RedirectURIs: append([]string(nil), profile.RedirectURIs...), GrantTypes: []string{"authorization_code"}, ResponseTypes: []string{"code"}, TokenEndpointAuthMethod: "none", ApplicationType: profile.ApplicationType}
	if profile.RefreshTokens {
		clientMetadata.GrantTypes = append(clientMetadata.GrantTypes, "refresh_token")
	}
	if len(metadata.TokenEndpointAuthMethodsSupported) > 0 && !slices.Contains(metadata.TokenEndpointAuthMethodsSupported, "none") {
		return Registration{}, fmt.Errorf("authorization server does not support public-client registration")
	}
	payload, _ := json.Marshal(clientMetadata)
	req, e := http.NewRequestWithContext(ctx, http.MethodPost, metadata.RegistrationEndpoint, bytes.NewReader(payload))
	if e != nil {
		return Registration{}, e
	}
	if s.registrationAuthorizer != nil {
		token, err := s.registrationAuthorizer.InitialAccessToken(ctx, issuer)
		if err != nil || strings.ContainsAny(token, "\r\n") {
			return Registration{}, fmt.Errorf("dynamic registration authorization unavailable")
		}
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	response, e := s.client.Do(req)
	if e != nil {
		return Registration{}, fmt.Errorf("dynamic registration request failed")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusCreated {
		return Registration{}, fmt.Errorf("dynamic registration returned HTTP %d", response.StatusCode)
	}
	var registered registrationResponse
	if e = decodeBounded(response.Body, &registered); e != nil {
		return Registration{}, e
	}
	if registered.ClientID == "" || !sameStrings(registered.RedirectURIs, profile.RedirectURIs) || registered.TokenEndpointAuthMethod != "none" || !sameStrings(registered.GrantTypes, clientMetadata.GrantTypes) || !sameStrings(registered.ResponseTypes, clientMetadata.ResponseTypes) {
		return Registration{}, fmt.Errorf("dynamic registration response changed approved client metadata")
	}
	if registered.ClientSecretExpiresAt < 0 || (registered.ClientSecretExpiresAt > 0 && registered.ClientSecretExpiresAt <= time.Now().Unix()) {
		return Registration{}, fmt.Errorf("invalid registration credential expiry")
	}
	credential := Credential{ClientID: registered.ClientID, ClientSecret: registered.ClientSecret, RegistrationAccessToken: registered.RegistrationAccessToken, SecretExpiresAt: registered.ClientSecretExpiresAt, TokenEndpointAuthMethod: registered.TokenEndpointAuthMethod, RedirectURIs: append([]string(nil), registered.RedirectURIs...), GrantTypes: append([]string(nil), registered.GrantTypes...)}
	ref, e := s.credentials.Put(ctx, key, credential)
	if e != nil || ref == "" {
		return Registration{}, fmt.Errorf("dynamic registration credential storage failed")
	}
	result.Mode = DCR
	result.ClientID = registered.ClientID
	result.CredentialRef = ref
	return result, nil
}

// FetchClientMetadata lets an authorization-server adapter validate an external
// CIMD. This method alone does not make Aiplex an OAuth authorization server.
func (s *Service) FetchClientMetadata(ctx context.Context, clientID string) (ClientMetadata, error) {
	if e := validateClientIDURL(clientID); e != nil {
		return ClientMetadata{}, e
	}
	s.mu.Lock()
	cached, ok := s.cache[clientID]
	s.mu.Unlock()
	if ok && time.Now().Before(cached.expires) {
		return cloneMetadata(cached.metadata), nil
	}
	var document ClientMetadata
	response, e := s.fetchJSON(ctx, clientID, &document)
	if e != nil {
		return ClientMetadata{}, e
	}
	if e = validateMetadata(document); e != nil {
		return ClientMetadata{}, e
	}
	if document.ClientID != clientID {
		return ClientMetadata{}, fmt.Errorf("CIMD client_id does not exactly match document URL")
	}
	ttl := s.cacheTTL
	for _, directive := range strings.Split(response.Header.Get("Cache-Control"), ",") {
		directive = strings.TrimSpace(strings.ToLower(directive))
		if directive == "no-store" || directive == "no-cache" {
			ttl = 0
			break
		}
		if strings.HasPrefix(directive, "max-age=") {
			seconds, e := strconv.ParseInt(strings.TrimPrefix(directive, "max-age="), 10, 64)
			if e != nil || seconds <= 0 {
				ttl = 0
			} else if seconds < int64(ttl/time.Second) {
				ttl = time.Duration(seconds) * time.Second
			}
		}
	}
	s.mu.Lock()
	delete(s.cache, clientID)
	if ttl > 0 {
		if len(s.cache) >= 1024 {
			for key := range s.cache {
				delete(s.cache, key)
				break
			}
		}
		s.cache[clientID] = metadataCacheEntry{cloneMetadata(document), time.Now().Add(ttl)}
	}
	s.mu.Unlock()
	return cloneMetadata(document), nil
}

// Coalesce simultaneous registration attempts within a host. A shared durable
// credential-store implementation should also provide cross-host serialization
// if several hosts register the same profile concurrently.
func (s *Service) lockRegistration(ctx context.Context, key CredentialKey) (func(), error) {
	s.mu.Lock()
	entry := s.registrations[key]
	if entry == nil {
		entry = &registrationGate{gate: make(chan struct{}, 1)}
		s.registrations[key] = entry
	}
	entry.references++
	s.mu.Unlock()
	drop := func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		entry.references--
		if entry.references == 0 {
			delete(s.registrations, key)
		}
	}
	select {
	case entry.gate <- struct{}{}:
		return func() { <-entry.gate; drop() }, nil
	case <-ctx.Done():
		drop()
		return nil, ctx.Err()
	}
}
