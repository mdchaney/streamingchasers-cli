package fakeserver

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type oauthClient struct {
	ID           string
	Name         string
	RedirectURIs []string
	Scopes       []string
}

type oauthGrant struct {
	ClientID    string
	RedirectURI string
	Challenge   string
	Resource    string
	Scopes      []string
	UserID      int64
	Expires     time.Time
}

type oauthToken struct {
	Access   string
	Refresh  string
	ClientID string
	Resource string
	Scopes   []string
	UserID   int64
	Expires  time.Time
	Revoked  bool
}

// Token describes an issued access token, for tests to inspect.
type Token struct {
	Access   string
	Refresh  string
	ClientID string
	Resource string
	Scopes   []string
	UserID   int64
	Revoked  bool
	Expired  bool
}

// Tokens returns every token issued so far, oldest first.
func (s *Server) Tokens() []Token {
	s.mu.Lock()
	defer s.mu.Unlock()
	tokens := make([]Token, 0, len(s.tokens))
	for _, t := range s.tokens {
		tokens = append(tokens, Token{
			Access: t.Access, Refresh: t.Refresh, ClientID: t.ClientID, Resource: t.Resource,
			Scopes: t.Scopes, UserID: t.UserID, Revoked: t.Revoked, Expired: time.Now().After(t.Expires),
		})
	}
	return tokens
}

// ExpireAccessTokens makes every access token expire; refresh tokens
// keep working.
func (s *Server) ExpireAccessTokens() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, t := range s.tokens {
		t.Expires = time.Now().Add(-time.Minute)
	}
}

// RevokeTokens revokes every token, as disconnecting the application on
// the account page does.
func (s *Server) RevokeTokens() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, t := range s.tokens {
		t.Revoked = true
	}
}

// ClientCount returns how many clients have registered.
func (s *Server) ClientCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.clients)
}

func secret() string {
	buf := make([]byte, 24)
	if _, err := rand.Read(buf); err != nil {
		panic(err)
	}
	return hex.EncodeToString(buf)
}

func (s *Server) authorizationServerMetadata(w http.ResponseWriter) {
	writeJSON(w, http.StatusOK, record{
		"issuer":                                s.URL,
		"authorization_endpoint":                s.URL + "/oauth/authorize",
		"token_endpoint":                        s.URL + "/oauth/token",
		"revocation_endpoint":                   s.URL + "/oauth/revoke",
		"registration_endpoint":                 s.URL + "/oauth/register",
		"response_types_supported":              []string{"code"},
		"grant_types_supported":                 []string{"authorization_code", "refresh_token"},
		"code_challenge_methods_supported":      []string{"S256"},
		"token_endpoint_auth_methods_supported": []string{"none", "client_secret_basic", "client_secret_post"},
		"scopes_supported":                      s.Scopes,
		"service_documentation":                 s.URL + "/api/docs.html",
	})
}

func (s *Server) protectedResourceMetadata(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, record{
		"resource":                 s.URL + APIResource,
		"resource_name":            "Streaming Chasers API",
		"authorization_servers":    []string{s.URL},
		"bearer_methods_supported": []string{"header"},
		"scopes_supported":         s.Scopes,
	})
}

func oauthError(w http.ResponseWriter, status int, code, description string) {
	writeJSON(w, status, record{"error": code, "error_description": description})
}

func isLoopbackIP(host string) bool {
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// redirectMatches follows Doorkeeper: an exact match, except that the
// port of a loopback IP address is ignored (RFC 8252 section 7.3).
func redirectMatches(requested, registered string) bool {
	a, err := url.Parse(requested)
	if err != nil {
		return false
	}
	b, err := url.Parse(registered)
	if err != nil {
		return false
	}
	if isLoopbackIP(a.Hostname()) && isLoopbackIP(b.Hostname()) {
		return a.Scheme == b.Scheme && a.Hostname() == b.Hostname() && a.Path == b.Path
	}
	return requested == registered
}

func (s *Server) intersectScopes(requested string) []string {
	var scopes []string
	for _, scope := range strings.Fields(requested) {
		for _, supported := range s.Scopes {
			if scope == supported {
				scopes = append(scopes, scope)
			}
		}
	}
	return scopes
}

func (s *Server) register(w http.ResponseWriter, r *http.Request, body []byte) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	var params struct {
		ClientName   string   `json:"client_name"`
		RedirectURIs []string `json:"redirect_uris"`
		Scope        string   `json:"scope"`
		AuthMethod   string   `json:"token_endpoint_auth_method"`
	}
	_ = json.Unmarshal(body, &params)

	if len(params.RedirectURIs) == 0 {
		oauthError(w, http.StatusBadRequest, "invalid_redirect_uri", "redirect_uris is required")
		return
	}
	for _, uri := range params.RedirectURIs {
		u, err := url.Parse(uri)
		if err != nil || u.Host == "" {
			oauthError(w, http.StatusBadRequest, "invalid_redirect_uri", "Redirect URI must be an absolute URI.")
			return
		}
		loopback := u.Hostname() == "localhost" || isLoopbackIP(u.Hostname())
		if u.Scheme != "https" && !loopback {
			oauthError(w, http.StatusBadRequest, "invalid_redirect_uri", "Redirect URI must be an HTTPS/SSL URI.")
			return
		}
	}

	scopes := s.intersectScopes(params.Scope)
	if len(scopes) == 0 {
		scopes = s.Scopes
	}
	name := params.ClientName
	if name == "" {
		name = "API client"
	}
	client := &oauthClient{ID: secret(), Name: name, RedirectURIs: params.RedirectURIs, Scopes: scopes}
	s.clients[client.ID] = client
	s.clientOrder = append(s.clientOrder, client.ID)

	writeJSON(w, http.StatusCreated, record{
		"client_id":                  client.ID,
		"client_id_issued_at":        time.Now().Unix(),
		"client_name":                client.Name,
		"redirect_uris":              client.RedirectURIs,
		"grant_types":                []string{"authorization_code", "refresh_token"},
		"response_types":             []string{"code"},
		"scope":                      strings.Join(scopes, " "),
		"token_endpoint_auth_method": "none",
	})
}

// authorize stands in for the consent page: the signed-in user approves
// (or, with Deny, refuses) at once and the browser is redirected.
func (s *Server) authorize(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()

	// Doorkeeper shows an error page, and does not redirect, when it
	// cannot trust the client or the redirect URI.
	client := s.clients[query.Get("client_id")]
	if client == nil {
		http.Error(w, "Client authentication failed due to unknown client.", http.StatusUnauthorized)
		return
	}
	redirectURI := query.Get("redirect_uri")
	matched := false
	for _, registered := range client.RedirectURIs {
		matched = matched || redirectMatches(redirectURI, registered)
	}
	if !matched {
		http.Error(w, "The requested redirect uri is malformed or doesn't match client redirect URI.", http.StatusBadRequest)
		return
	}

	redirect := func(params url.Values) {
		if state := query.Get("state"); state != "" {
			params.Set("state", state)
		}
		http.Redirect(w, r, redirectURI+"?"+params.Encode(), http.StatusFound)
	}
	fail := func(code, description string) {
		redirect(url.Values{"error": {code}, "error_description": {description}})
	}

	if query.Get("response_type") != "code" {
		fail("unsupported_response_type", "The authorization server does not support this response type.")
		return
	}
	if query.Get("code_challenge") == "" {
		fail("invalid_request", "Code challenge is required.")
		return
	}
	if query.Get("code_challenge_method") != "S256" {
		fail("invalid_request", "The code challenge method must be S256.")
		return
	}
	requested := strings.Fields(query.Get("scope"))
	if len(requested) == 0 {
		requested = []string{"catalog"}
	}
	for _, scope := range requested {
		allowed := false
		for _, granted := range client.Scopes {
			allowed = allowed || scope == granted
		}
		if !allowed || len(s.intersectScopes(scope)) == 0 {
			fail("invalid_scope", "The requested scope is invalid, unknown, or malformed.")
			return
		}
	}
	if s.Deny || s.SignedIn == nil {
		fail("access_denied", "The resource owner or authorization server denied the request.")
		return
	}

	code := secret()
	s.grants[code] = &oauthGrant{
		ClientID: client.ID, RedirectURI: redirectURI, Challenge: query.Get("code_challenge"),
		Resource: query.Get("resource"), Scopes: requested, UserID: s.SignedIn.ID,
		Expires: time.Now().Add(10 * time.Minute),
	}
	redirect(url.Values{"code": {code}})
}

func (s *Server) token(w http.ResponseWriter, r *http.Request, body []byte) {
	form, err := url.ParseQuery(string(body))
	if err != nil || r.Method != http.MethodPost {
		oauthError(w, http.StatusBadRequest, "invalid_request", "The request is malformed.")
		return
	}
	invalidGrant := func() {
		oauthError(w, http.StatusBadRequest, "invalid_grant", "The provided authorization grant is invalid, expired, revoked, does not match the redirection URI used in the authorization request, or was issued to another client.")
	}

	switch form.Get("grant_type") {
	case "authorization_code":
		grant := s.grants[form.Get("code")]
		if grant == nil {
			invalidGrant()
			return
		}
		// A code works once, whether or not the exchange succeeds.
		delete(s.grants, form.Get("code"))
		if time.Now().After(grant.Expires) || grant.ClientID != form.Get("client_id") || grant.RedirectURI != form.Get("redirect_uri") {
			invalidGrant()
			return
		}
		sum := sha256.Sum256([]byte(form.Get("code_verifier")))
		if base64.RawURLEncoding.EncodeToString(sum[:]) != grant.Challenge {
			invalidGrant()
			return
		}
		resource := grant.Resource
		if resource == "" {
			resource = form.Get("resource")
		}
		s.issue(w, grant.ClientID, resource, grant.Scopes, grant.UserID)

	case "refresh_token":
		var old *oauthToken
		for _, t := range s.tokens {
			if t.Refresh != "" && t.Refresh == form.Get("refresh_token") {
				old = t
			}
		}
		if old == nil || old.Revoked || old.ClientID != form.Get("client_id") {
			invalidGrant()
			return
		}
		old.Revoked = true
		s.issue(w, old.ClientID, old.Resource, old.Scopes, old.UserID)

	default:
		oauthError(w, http.StatusBadRequest, "unsupported_grant_type", "The authorization grant type is not supported by the authorization server.")
	}
}

func (s *Server) issue(w http.ResponseWriter, clientID, resource string, scopes []string, userID int64) {
	t := &oauthToken{
		Access: secret(), Refresh: secret(), ClientID: clientID, Resource: resource,
		Scopes: scopes, UserID: userID, Expires: time.Now().Add(s.AccessTokenTTL),
	}
	s.tokens = append(s.tokens, t)
	writeJSON(w, http.StatusOK, record{
		"access_token":  t.Access,
		"token_type":    "Bearer",
		"expires_in":    int64(s.AccessTokenTTL / time.Second),
		"refresh_token": t.Refresh,
		"scope":         strings.Join(scopes, " "),
		"created_at":    time.Now().Unix(),
	})
}

func (s *Server) revoke(w http.ResponseWriter, r *http.Request, body []byte) {
	form, _ := url.ParseQuery(string(body))
	for _, t := range s.tokens {
		if (t.Access == form.Get("token") || t.Refresh == form.Get("token")) && t.ClientID == form.Get("client_id") {
			t.Revoked = true
		}
	}
	// RFC 7009: the answer is the same whether or not the token existed.
	writeJSON(w, http.StatusOK, record{})
}

// authenticate returns the user a request's bearer token belongs to and
// the scopes it carries.  An API token carries no scopes and may do
// anything its user may.
func (s *Server) authenticate(r *http.Request) (*User, []string, bool) {
	header := r.Header.Get("Authorization")
	presented, ok := strings.CutPrefix(header, "Bearer ")
	if !ok || presented == "" {
		return nil, nil, false
	}
	for _, user := range s.Users {
		if user.APIToken == presented {
			return user, nil, true
		}
	}
	for _, t := range s.tokens {
		if t.Access != presented {
			continue
		}
		if t.Revoked || time.Now().After(t.Expires) || t.Resource != fmt.Sprintf("http://%s%s", r.Host, APIResource) {
			return nil, nil, false
		}
		for _, user := range s.Users {
			if user.ID == t.UserID {
				return user, t.Scopes, true
			}
		}
	}
	return nil, nil, false
}
