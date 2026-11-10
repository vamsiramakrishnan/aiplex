package toolcredential

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func validAuth() map[string]Auth {
	return map[string]Auth{
		None:                    {Type: None},
		APIKey:                  {Type: APIKey, In: InHeader, Name: "X-Api-Key", SecretRef: "search-key"},
		"api_key_query":         {Type: APIKey, In: InQuery, Name: "key", SecretRef: "search-key"},
		"api_key_prefix":        {Type: APIKey, In: InHeader, Name: "Authorization", Prefix: "Token", SecretRef: "search-key"},
		Bearer:                  {Type: Bearer, SecretRef: "static-token"},
		Basic:                   {Type: Basic, Username: "svc", SecretRef: "svc-password"},
		OAuthUser:               {Type: OAuthUser},
		OAuthClientCredentials:  {Type: OAuthClientCredentials, TokenEndpoint: "https://login.example/token", ClientID: "svc", ClientSecretRef: "svc-secret", ClientAuthMethod: ClientSecretBasic, Scopes: []string{"tools.read"}, Resource: "https://tools.example/mcp"},
		TokenExchange:           {Type: TokenExchange, TokenEndpoint: "https://sts.example/token", Audience: "tools", Scopes: []string{"read"}},
		"token_exchange_client": {Type: TokenExchange, TokenEndpoint: "https://sts.example/token", Resource: "https://tools.example/mcp", ClientID: "gw", ClientSecretRef: "gw-secret", ClientAuthMethod: ClientSecretPost, SubjectTokenType: TokenTypeAccessToken},
		GoogleIDToken:           {Type: GoogleIDToken, Audience: "https://tools-abc.a.run.app", IdentityRef: "workload"},
	}
}

func TestAuthValidation(t *testing.T) {
	for name, auth := range validAuth() {
		if err := auth.Validate(); err != nil {
			t.Errorf("%s: valid block rejected: %v", name, err)
		}
	}
	invalid := map[string]Auth{
		"unknown type":               {Type: "magic"},
		"empty type":                 {},
		"none with secret":           {Type: None, SecretRef: "x"},
		"oauth_user with secret":     {Type: OAuthUser, SecretRef: "x"},
		"api key missing in":         {Type: APIKey, Name: "X-Key", SecretRef: "k"},
		"api key bad in":             {Type: APIKey, In: "cookie", Name: "X-Key", SecretRef: "k"},
		"api key missing secret":     {Type: APIKey, In: InHeader, Name: "X-Key"},
		"api key host header":        {Type: APIKey, In: InHeader, Name: "Host", SecretRef: "k"},
		"api key connection header":  {Type: APIKey, In: InHeader, Name: "connection", SecretRef: "k"},
		"api key transfer-encoding":  {Type: APIKey, In: InHeader, Name: "Transfer-Encoding", SecretRef: "k"},
		"api key forwarded":          {Type: APIKey, In: InHeader, Name: "X-Forwarded-For", SecretRef: "k"},
		"api key proxy":              {Type: APIKey, In: InHeader, Name: "Proxy-Authorization", SecretRef: "k"},
		"api key mcp session":        {Type: APIKey, In: InHeader, Name: "Mcp-Session-Id", SecretRef: "k"},
		"api key gateway header":     {Type: APIKey, In: InHeader, Name: "X-Aiplex-Funding-Account", SecretRef: "k"},
		"api key header with space":  {Type: APIKey, In: InHeader, Name: "X Key", SecretRef: "k"},
		"api key header with colon":  {Type: APIKey, In: InHeader, Name: "X-Key:", SecretRef: "k"},
		"api key query prefix":       {Type: APIKey, In: InQuery, Name: "key", Prefix: "Token", SecretRef: "k"},
		"api key query bad name":     {Type: APIKey, In: InQuery, Name: "a&b=c", SecretRef: "k"},
		"api key inline secret":      {Type: APIKey, In: InHeader, Name: "X-Key", SecretRef: "sk live/abc=="},
		"bearer inline token":        {Type: Bearer, SecretRef: "eyJhbGciOi.eyJzdWIi.sig"},
		"bearer with username":       {Type: Bearer, SecretRef: "t", Username: "u"},
		"basic colon username":       {Type: Basic, Username: "a:b", SecretRef: "p"},
		"basic missing username":     {Type: Basic, SecretRef: "p"},
		"cc missing method":          {Type: OAuthClientCredentials, TokenEndpoint: "https://l.example/token", ClientID: "c", ClientSecretRef: "s"},
		"cc bad method":              {Type: OAuthClientCredentials, TokenEndpoint: "https://l.example/token", ClientID: "c", ClientSecretRef: "s", ClientAuthMethod: "private_key_jwt"},
		"cc missing secret":          {Type: OAuthClientCredentials, TokenEndpoint: "https://l.example/token", ClientID: "c", ClientAuthMethod: ClientSecretPost},
		"cc credentials in endpoint": {Type: OAuthClientCredentials, TokenEndpoint: "https://u:p@l.example/token", ClientID: "c", ClientSecretRef: "s", ClientAuthMethod: ClientSecretPost},
		"cc empty scopes":            {Type: OAuthClientCredentials, TokenEndpoint: "https://l.example/token", ClientID: "c", ClientSecretRef: "s", ClientAuthMethod: ClientSecretPost, Scopes: []string{}},
		"cc duplicate scopes":        {Type: OAuthClientCredentials, TokenEndpoint: "https://l.example/token", ClientID: "c", ClientSecretRef: "s", ClientAuthMethod: ClientSecretPost, Scopes: []string{"a", "a"}},
		"cc audience":                {Type: OAuthClientCredentials, TokenEndpoint: "https://l.example/token", ClientID: "c", ClientSecretRef: "s", ClientAuthMethod: ClientSecretPost, Audience: "x"},
		"te missing audience":        {Type: TokenExchange, TokenEndpoint: "https://sts.example/token"},
		"te secret without method":   {Type: TokenExchange, TokenEndpoint: "https://sts.example/token", Audience: "a", ClientID: "c", ClientSecretRef: "s"},
		"te bad subject type":        {Type: TokenExchange, TokenEndpoint: "https://sts.example/token", Audience: "a", SubjectTokenType: "urn:x"},
		"te identity ref":            {Type: TokenExchange, TokenEndpoint: "https://sts.example/token", Audience: "a", IdentityRef: "w"},
		"google missing identity":    {Type: GoogleIDToken, Audience: "https://a.example"},
		"google missing audience":    {Type: GoogleIDToken, IdentityRef: "w"},
		"google secret":              {Type: GoogleIDToken, Audience: "https://a.example", IdentityRef: "w", SecretRef: "s"},
	}
	for name, auth := range invalid {
		if err := auth.Validate(); err == nil {
			t.Errorf("%s: invalid block accepted", name)
		}
	}
}

func TestAuthUnknownFieldsRejectedByStrictDecoding(t *testing.T) {
	decoder := json.NewDecoder(bytes.NewReader([]byte(`{"type":"bearer","secret_ref":"t","secret":"inline"}`)))
	decoder.DisallowUnknownFields()
	var auth Auth
	if decoder.Decode(&auth) == nil {
		t.Fatal("unknown auth field accepted by strict decoding")
	}
}

func TestCatalogDescribesEveryTypeAndField(t *testing.T) {
	tags := map[string]bool{}
	typ := reflect.TypeOf(Auth{})
	for i := 0; i < typ.NumField(); i++ {
		tags[strings.Split(typ.Field(i).Tag.Get("json"), ",")[0]] = true
	}
	seen := map[string]bool{}
	for _, spec := range Catalog() {
		seen[spec.Type] = true
		if spec.Description == "" || spec.Title == "" || spec.Fields == nil {
			t.Errorf("%s: incomplete description", spec.Type)
		}
		for _, field := range spec.Fields {
			if !tags[field.Name] || field.Description == "" {
				t.Errorf("%s.%s: not an auth block member", spec.Type, field.Name)
			}
			if field.SecretRef != strings.HasSuffix(field.Name, "secret_ref") {
				t.Errorf("%s.%s: secret reference flag mismatch", spec.Type, field.Name)
			}
		}
	}
	for _, name := range []string{None, APIKey, Bearer, Basic, OAuthUser, OAuthClientCredentials, TokenExchange, GoogleIDToken} {
		if !seen[name] {
			t.Errorf("catalog omits %s", name)
		}
	}
	if len(seen) != 8 {
		t.Fatalf("catalog has %d types", len(seen))
	}
}
