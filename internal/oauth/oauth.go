// Package oauth implements the client side of the Streaming Chasers authorization
// server: discovery (RFC 8414), dynamic client registration (RFC 7591),
// the authorization code grant with PKCE (RFC 7636) over a loopback
// redirect (RFC 8252), resource indicators (RFC 8707), refresh and
// revocation (RFC 7009).
package oauth

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// maxBody caps how much of a response is read; nothing the authorization
// server sends is anywhere near this large.
const maxBody = 1 << 20

// Metadata is the authorization server's discovery document.
type Metadata struct {
	Issuer                        string   `json:"issuer"`
	AuthorizationEndpoint         string   `json:"authorization_endpoint"`
	TokenEndpoint                 string   `json:"token_endpoint"`
	RevocationEndpoint            string   `json:"revocation_endpoint"`
	RegistrationEndpoint          string   `json:"registration_endpoint"`
	ScopesSupported               []string `json:"scopes_supported"`
	CodeChallengeMethodsSupported []string `json:"code_challenge_methods_supported"`
}

// Error is an error response from the authorization server.
type Error struct {
	Status      int
	Code        string `json:"error"`
	Description string `json:"error_description"`
}

func (e *Error) Error() string {
	switch {
	case e.Code != "" && e.Description != "":
		return fmt.Sprintf("%s: %s", e.Code, e.Description)
	case e.Code != "":
		return e.Code
	case e.Description != "":
		return e.Description
	default:
		return fmt.Sprintf("the authorization server answered %d", e.Status)
	}
}

// Discover fetches the authorization server metadata for base.  A server
// without a discovery document gets Doorkeeper's conventional endpoints.
func Discover(ctx context.Context, hc *http.Client, base string) (*Metadata, error) {
	base = strings.TrimRight(base, "/")
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/.well-known/oauth-authorization-server", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("cannot reach %s: %w", base, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return &Metadata{
			Issuer:                base,
			AuthorizationEndpoint: base + "/oauth/authorize",
			TokenEndpoint:         base + "/oauth/token",
			RevocationEndpoint:    base + "/oauth/revoke",
			RegistrationEndpoint:  base + "/oauth/register",
		}, nil
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s answered %d to the OAuth discovery request", base, resp.StatusCode)
	}

	md := &Metadata{}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxBody)).Decode(md); err != nil {
		return nil, fmt.Errorf("%s sent an OAuth discovery document that is not JSON: %w", base, err)
	}
	if md.AuthorizationEndpoint == "" || md.TokenEndpoint == "" {
		return nil, fmt.Errorf("%s sent an OAuth discovery document without its endpoints", base)
	}
	if len(md.CodeChallengeMethodsSupported) > 0 && !contains(md.CodeChallengeMethodsSupported, "S256") {
		return nil, fmt.Errorf("%s does not support PKCE with S256", base)
	}
	// The endpoints receive the authorization code and the tokens, so a
	// document that points them at another origin is refused.
	for name, endpoint := range map[string]string{
		"authorization_endpoint": md.AuthorizationEndpoint,
		"token_endpoint":         md.TokenEndpoint,
		"revocation_endpoint":    md.RevocationEndpoint,
		"registration_endpoint":  md.RegistrationEndpoint,
	} {
		if endpoint != "" && !sameOrigin(base, endpoint) {
			return nil, fmt.Errorf("%s names a %s on another origin (%s)", base, name, endpoint)
		}
	}
	return md, nil
}

func sameOrigin(a, b string) bool {
	ua, err := url.Parse(a)
	if err != nil {
		return false
	}
	ub, err := url.Parse(b)
	if err != nil {
		return false
	}
	return strings.EqualFold(ua.Scheme, ub.Scheme) && strings.EqualFold(ua.Host, ub.Host)
}

// SelectScopes returns the scopes in wanted that the server supports, in
// wanted's order.  A server that does not list its scopes gets fallback.
func SelectScopes(supported, wanted, fallback []string) []string {
	if len(supported) == 0 {
		return fallback
	}
	var scopes []string
	for _, scope := range wanted {
		if contains(supported, scope) {
			scopes = append(scopes, scope)
		}
	}
	return scopes
}

func contains(list []string, s string) bool {
	for _, item := range list {
		if item == s {
			return true
		}
	}
	return false
}

// Client is a registered application.
type Client struct {
	ClientID     string   `json:"client_id"`
	RedirectURIs []string `json:"redirect_uris"`
	Scope        string   `json:"scope"`
}

// Register registers a public client with the server.
func Register(ctx context.Context, hc *http.Client, md *Metadata, name, redirectURI string, scopes []string) (*Client, error) {
	if md.RegistrationEndpoint == "" {
		return nil, errors.New("the server does not support registering OAuth clients")
	}
	body, err := json.Marshal(map[string]any{
		"client_name":                name,
		"redirect_uris":              []string{redirectURI},
		"grant_types":                []string{"authorization_code", "refresh_token"},
		"response_types":             []string{"code"},
		"token_endpoint_auth_method": "none",
		"scope":                      strings.Join(scopes, " "),
	})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, md.RegistrationEndpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	client := &Client{}
	if err := do(hc, req, client); err != nil {
		return nil, fmt.Errorf("registering the CLI with the server failed: %w", err)
	}
	if client.ClientID == "" {
		return nil, errors.New("registering the CLI with the server failed: no client_id in the response")
	}
	return client, nil
}

// Token is a token endpoint response.
type Token struct {
	AccessToken  string `json:"access_token"`
	TokenType    string `json:"token_type"`
	RefreshToken string `json:"refresh_token"`
	Scope        string `json:"scope"`
	ExpiresIn    int64  `json:"expires_in"`
}

// ExpiresAt is when the token expires, or the zero time if it does not.
func (t *Token) ExpiresAt(issued time.Time) time.Time {
	if t.ExpiresIn <= 0 {
		return time.Time{}
	}
	return issued.Add(time.Duration(t.ExpiresIn) * time.Second)
}

// Exchange trades an authorization code for a token.
func Exchange(ctx context.Context, hc *http.Client, tokenEndpoint, clientID, code, redirectURI, verifier, resource string) (*Token, error) {
	form := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {redirectURI},
		"client_id":     {clientID},
		"code_verifier": {verifier},
	}
	if resource != "" {
		form.Set("resource", resource)
	}
	return requestToken(ctx, hc, tokenEndpoint, form)
}

// Refresh trades a refresh token for a new access token.  The server
// rotates refresh tokens, so the response's refresh token replaces the
// one that was sent.
func Refresh(ctx context.Context, hc *http.Client, tokenEndpoint, clientID, refreshToken string) (*Token, error) {
	form := url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {refreshToken},
		"client_id":     {clientID},
	}
	return requestToken(ctx, hc, tokenEndpoint, form)
}

func requestToken(ctx context.Context, hc *http.Client, tokenEndpoint string, form url.Values) (*Token, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenEndpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	token := &Token{}
	if err := do(hc, req, token); err != nil {
		return nil, err
	}
	if token.AccessToken == "" {
		return nil, errors.New("the authorization server sent no access token")
	}
	if token.TokenType != "" && !strings.EqualFold(token.TokenType, "bearer") {
		return nil, fmt.Errorf("the authorization server sent a %q token; only bearer tokens are supported", token.TokenType)
	}
	return token, nil
}

// Revoke revokes an access or refresh token.
func Revoke(ctx context.Context, hc *http.Client, revocationEndpoint, clientID, token string) error {
	if revocationEndpoint == "" {
		return errors.New("the server does not support revoking tokens")
	}
	form := url.Values{"token": {token}, "client_id": {clientID}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, revocationEndpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	return do(hc, req, nil)
}

// do sends req and decodes a successful JSON response into out.  Any
// other response becomes an *Error.
func do(hc *http.Client, req *http.Request, out any) error {
	resp, err := hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return err
	}

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		oauthErr := &Error{Status: resp.StatusCode}
		// The body is an OAuth error when it is JSON and anything at all
		// (an HTML error page, say) otherwise.
		_ = json.Unmarshal(body, oauthErr)
		return oauthErr
	}
	if out == nil || len(bytes.TrimSpace(body)) == 0 {
		return nil
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("the authorization server sent a response that is not JSON: %w", err)
	}
	return nil
}

// PKCE is a code verifier and its S256 challenge.
type PKCE struct {
	Verifier  string
	Challenge string
}

// NewPKCE generates a verifier with 256 bits of entropy.
func NewPKCE() (*PKCE, error) {
	verifier, err := randomString(32)
	if err != nil {
		return nil, err
	}
	return &PKCE{Verifier: verifier, Challenge: Challenge(verifier)}, nil
}

// Challenge is the S256 code challenge for verifier.
func Challenge(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

func randomString(bytes int) (string, error) {
	buf := make([]byte, bytes)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}
