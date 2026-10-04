package oauth

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/mdchaney/streamingchasers-api/internal/fakeserver"
)

func TestChallengeMatchesRFC7636(t *testing.T) {
	// The example of RFC 7636, appendix B.
	const verifier = "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk"
	const challenge = "E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM"
	if got := Challenge(verifier); got != challenge {
		t.Errorf("Challenge = %q, want %q", got, challenge)
	}
}

func TestNewPKCE(t *testing.T) {
	a, err := NewPKCE()
	if err != nil {
		t.Fatal(err)
	}
	b, err := NewPKCE()
	if err != nil {
		t.Fatal(err)
	}
	if a.Verifier == b.Verifier {
		t.Error("two verifiers were the same")
	}
	// RFC 7636 section 4.1: 43 to 128 characters.
	if len(a.Verifier) < 43 || len(a.Verifier) > 128 {
		t.Errorf("the verifier has %d characters", len(a.Verifier))
	}
	if a.Challenge != Challenge(a.Verifier) {
		t.Error("the challenge does not belong to the verifier")
	}
}

func TestDiscover(t *testing.T) {
	server := fakeserver.New()
	defer server.Close()

	md, err := Discover(context.Background(), server.Client(), server.URL+"/")
	if err != nil {
		t.Fatal(err)
	}
	if md.TokenEndpoint != server.URL+"/oauth/token" || md.RegistrationEndpoint != server.URL+"/oauth/register" {
		t.Errorf("unexpected endpoints: %+v", md)
	}
	if strings.Join(md.ScopesSupported, " ") != "catalog catalog_write catalog_admin" {
		t.Errorf("unexpected scopes: %v", md.ScopesSupported)
	}
}

func TestDiscoverFallsBackToConventionalEndpoints(t *testing.T) {
	server := httptest.NewServer(http.NotFoundHandler())
	defer server.Close()

	md, err := Discover(context.Background(), server.Client(), server.URL)
	if err != nil {
		t.Fatal(err)
	}
	if md.AuthorizationEndpoint != server.URL+"/oauth/authorize" || md.RevocationEndpoint != server.URL+"/oauth/revoke" {
		t.Errorf("unexpected endpoints: %+v", md)
	}
}

func TestDiscoverRefusesBadDocuments(t *testing.T) {
	tests := []struct {
		name   string
		status int
		body   string
		want   string
	}{
		{"server error", 500, "", "answered 500"},
		{"not JSON", 200, "<html>", "not JSON"},
		{"no endpoints", 200, `{"issuer": "x"}`, "without its endpoints"},
		{"no S256", 200, `{"authorization_endpoint": "BASE/a", "token_endpoint": "BASE/t", "code_challenge_methods_supported": ["plain"]}`, "S256"},
		{"token endpoint elsewhere", 200, `{"authorization_endpoint": "BASE/a", "token_endpoint": "https://evil.example/t"}`, "another origin"},
		{"authorization endpoint elsewhere", 200, `{"authorization_endpoint": "https://evil.example/a", "token_endpoint": "BASE/t"}`, "another origin"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var server *httptest.Server
			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tt.status)
				io.WriteString(w, strings.ReplaceAll(tt.body, "BASE", server.URL))
			}))
			defer server.Close()

			_, err := Discover(context.Background(), server.Client(), server.URL)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("got %v, want an error mentioning %q", err, tt.want)
			}
		})
	}
}

func TestDiscoverUnreachable(t *testing.T) {
	server := httptest.NewServer(http.NotFoundHandler())
	server.Close()
	_, err := Discover(context.Background(), http.DefaultClient, server.URL)
	if err == nil || !strings.Contains(err.Error(), "cannot reach") {
		t.Errorf("got %v", err)
	}
}

func TestSelectScopes(t *testing.T) {
	wanted := []string{"catalog", "catalog_write"}
	fallback := []string{"catalog"}

	tests := []struct {
		supported []string
		want      string
	}{
		{[]string{"catalog", "catalog_write"}, "catalog catalog_write"},
		{[]string{"catalog_write", "catalog", "admin"}, "catalog catalog_write"},
		{nil, "catalog"},
		{[]string{"admin"}, ""},
	}
	for _, tt := range tests {
		if got := strings.Join(SelectScopes(tt.supported, wanted, fallback), " "); got != tt.want {
			t.Errorf("SelectScopes(%v) = %q, want %q", tt.supported, got, tt.want)
		}
	}
}

func TestRegister(t *testing.T) {
	server := fakeserver.New()
	defer server.Close()
	md, err := Discover(context.Background(), server.Client(), server.URL)
	if err != nil {
		t.Fatal(err)
	}

	client, err := Register(context.Background(), server.Client(), md, "Streaming Chasers CLI", "http://127.0.0.1:4567/callback", []string{"catalog", "catalog_write"})
	if err != nil {
		t.Fatal(err)
	}
	if client.ClientID == "" || client.Scope != "catalog catalog_write" {
		t.Errorf("unexpected client: %+v", client)
	}

	var sent map[string]any
	for _, r := range server.Requests() {
		if r.Path == "/oauth/register" {
			if err := json.Unmarshal(r.Body, &sent); err != nil {
				t.Fatal(err)
			}
		}
	}
	if sent["token_endpoint_auth_method"] != "none" {
		t.Errorf("the CLI must register as a public client, sent %v", sent["token_endpoint_auth_method"])
	}
	if sent["client_name"] != "Streaming Chasers CLI" {
		t.Errorf("client_name = %v", sent["client_name"])
	}
}

func TestRegisterFailure(t *testing.T) {
	server := fakeserver.New()
	defer server.Close()
	md, _ := Discover(context.Background(), server.Client(), server.URL)

	_, err := Register(context.Background(), server.Client(), md, "x", "http://example.org/callback", nil)
	var oauthErr *Error
	if !errors.As(err, &oauthErr) || oauthErr.Code != "invalid_redirect_uri" || oauthErr.Status != 400 {
		t.Errorf("got %v", err)
	}

	md.RegistrationEndpoint = ""
	if _, err := Register(context.Background(), server.Client(), md, "x", "http://127.0.0.1/callback", nil); err == nil {
		t.Error("expected an error when the server has no registration endpoint")
	}
}

func TestErrorMessages(t *testing.T) {
	tests := []struct {
		err  Error
		want string
	}{
		{Error{Code: "invalid_grant", Description: "It expired."}, "invalid_grant: It expired."},
		{Error{Code: "invalid_grant"}, "invalid_grant"},
		{Error{Description: "It expired."}, "It expired."},
		{Error{Status: 502}, "the authorization server answered 502"},
	}
	for _, tt := range tests {
		if got := tt.err.Error(); got != tt.want {
			t.Errorf("got %q, want %q", got, tt.want)
		}
	}
}

func TestTokenExpiresAt(t *testing.T) {
	issued := time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)
	if got := (&Token{ExpiresIn: 3600}).ExpiresAt(issued); !got.Equal(issued.Add(time.Hour)) {
		t.Errorf("got %v", got)
	}
	if got := (&Token{}).ExpiresAt(issued); !got.IsZero() {
		t.Errorf("a token without expires_in should not expire, got %v", got)
	}
}

func TestTokenEndpointErrors(t *testing.T) {
	respond := func(status int, body string) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(status)
			io.WriteString(w, body)
		}))
	}

	t.Run("an OAuth error", func(t *testing.T) {
		server := respond(400, `{"error": "invalid_grant", "error_description": "Nope."}`)
		defer server.Close()
		_, err := Refresh(context.Background(), server.Client(), server.URL, "cid", "r")
		var oauthErr *Error
		if !errors.As(err, &oauthErr) || oauthErr.Code != "invalid_grant" {
			t.Errorf("got %v", err)
		}
	})
	t.Run("an HTML error page", func(t *testing.T) {
		server := respond(502, `<html>Bad Gateway</html>`)
		defer server.Close()
		_, err := Refresh(context.Background(), server.Client(), server.URL, "cid", "r")
		var oauthErr *Error
		if !errors.As(err, &oauthErr) || oauthErr.Status != 502 {
			t.Errorf("got %v", err)
		}
	})
	t.Run("no access token", func(t *testing.T) {
		server := respond(200, `{"token_type": "Bearer"}`)
		defer server.Close()
		if _, err := Refresh(context.Background(), server.Client(), server.URL, "cid", "r"); err == nil || !strings.Contains(err.Error(), "no access token") {
			t.Errorf("got %v", err)
		}
	})
	t.Run("a token that is not a bearer token", func(t *testing.T) {
		server := respond(200, `{"access_token": "a", "token_type": "mac"}`)
		defer server.Close()
		if _, err := Refresh(context.Background(), server.Client(), server.URL, "cid", "r"); err == nil || !strings.Contains(err.Error(), "bearer") {
			t.Errorf("got %v", err)
		}
	})
	t.Run("a body that is not JSON", func(t *testing.T) {
		server := respond(200, `welcome`)
		defer server.Close()
		if _, err := Refresh(context.Background(), server.Client(), server.URL, "cid", "r"); err == nil || !strings.Contains(err.Error(), "not JSON") {
			t.Errorf("got %v", err)
		}
	})
}

func TestRevokeWithoutEndpoint(t *testing.T) {
	if err := Revoke(context.Background(), http.DefaultClient, "", "cid", "token"); err == nil {
		t.Error("expected an error")
	}
}

// signIn sets up a flow against a fake server, registered and listening.
func signIn(t *testing.T, server *fakeserver.Server) *Flow {
	t.Helper()
	md, err := Discover(context.Background(), server.Client(), server.URL)
	if err != nil {
		t.Fatal(err)
	}
	listener, err := Listen(0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { listener.Close() })
	client, err := Register(context.Background(), server.Client(), md, "Test", RedirectURI(listener), []string{"catalog", "catalog_write"})
	if err != nil {
		t.Fatal(err)
	}
	return &Flow{
		HTTPClient: server.Client(),
		Metadata:   md,
		ClientID:   client.ClientID,
		Scopes:     []string{"catalog", "catalog_write"},
		Resource:   server.URL + "/api/v1",
		Listener:   listener,
		Out:        io.Discard,
		Timeout:    5 * time.Second,
		// The browser: it follows the authorization server's redirect
		// to the loopback listener.
		OpenBrowser: func(url string) error {
			go func() {
				if resp, err := http.Get(url); err == nil {
					resp.Body.Close()
				}
			}()
			return nil
		},
	}
}

func TestFlow(t *testing.T) {
	server := fakeserver.New()
	defer server.Close()
	flow := signIn(t, server)

	var out bytes.Buffer
	flow.Out = &out
	token, err := flow.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if token.AccessToken == "" || token.RefreshToken == "" || token.Scope != "catalog catalog_write" || token.ExpiresIn != 3600 {
		t.Errorf("unexpected token: %+v", token)
	}
	if !strings.Contains(out.String(), "Opening your browser") || !strings.Contains(out.String(), "/oauth/authorize?") {
		t.Errorf("the user was not told what is happening:\n%s", out.String())
	}

	issued := server.Tokens()
	if len(issued) != 1 || issued[0].Resource != server.URL+"/api/v1" || issued[0].UserID != 1 {
		t.Errorf("unexpected tokens on the server: %+v", issued)
	}

	// What the browser was sent to carries PKCE, state and the resource.
	var authorize url.Values
	for _, r := range server.Requests() {
		if r.Path == "/oauth/authorize" {
			authorize, _ = url.ParseQuery(r.Query)
		}
	}
	for _, key := range []string{"client_id", "redirect_uri", "state", "code_challenge", "scope", "resource"} {
		if authorize.Get(key) == "" {
			t.Errorf("the authorization request has no %s", key)
		}
	}
	if authorize.Get("code_challenge_method") != "S256" || authorize.Get("response_type") != "code" {
		t.Errorf("unexpected authorization request: %v", authorize)
	}
}

func TestFlowRefreshAndRevoke(t *testing.T) {
	server := fakeserver.New()
	defer server.Close()
	flow := signIn(t, server)
	token, err := flow.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	refreshed, err := Refresh(context.Background(), server.Client(), flow.Metadata.TokenEndpoint, flow.ClientID, token.RefreshToken)
	if err != nil {
		t.Fatal(err)
	}
	if refreshed.AccessToken == token.AccessToken || refreshed.RefreshToken == token.RefreshToken {
		t.Error("refreshing should issue a new access token and rotate the refresh token")
	}
	if issued := server.Tokens(); issued[1].Resource != server.URL+"/api/v1" {
		t.Errorf("the refreshed token lost its resource: %+v", issued[1])
	}

	// The refresh token that was used is spent.
	if _, err := Refresh(context.Background(), server.Client(), flow.Metadata.TokenEndpoint, flow.ClientID, token.RefreshToken); err == nil {
		t.Error("a used refresh token should be refused")
	}

	if err := Revoke(context.Background(), server.Client(), flow.Metadata.RevocationEndpoint, flow.ClientID, refreshed.RefreshToken); err != nil {
		t.Fatal(err)
	}
	for _, issued := range server.Tokens() {
		if !issued.Revoked {
			t.Errorf("a token survived: %+v", issued)
		}
	}
}

func TestFlowWithoutBrowser(t *testing.T) {
	server := fakeserver.New()
	defer server.Close()
	flow := signIn(t, server)
	flow.OpenBrowser = nil
	flow.Timeout = 50 * time.Millisecond

	var out bytes.Buffer
	flow.Out = &out
	_, err := flow.Run(context.Background())
	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Errorf("got %v", err)
	}
	if !strings.Contains(out.String(), "Open this URL in a browser") {
		t.Errorf("the user was not given the URL:\n%s", out.String())
	}
}

func TestFlowBrowserFailsToOpen(t *testing.T) {
	server := fakeserver.New()
	defer server.Close()
	flow := signIn(t, server)
	flow.OpenBrowser = func(string) error { return errors.New("no browser") }
	flow.Timeout = 50 * time.Millisecond

	var out bytes.Buffer
	flow.Out = &out
	flow.Run(context.Background())
	if !strings.Contains(out.String(), "Open this URL in a browser") {
		t.Errorf("the user was not given the URL:\n%s", out.String())
	}
}

func TestFlowDenied(t *testing.T) {
	server := fakeserver.New()
	defer server.Close()
	server.Deny = true
	flow := signIn(t, server)

	_, err := flow.Run(context.Background())
	if err == nil || !strings.Contains(err.Error(), "not approved") {
		t.Errorf("got %v", err)
	}
	if len(server.Tokens()) != 0 {
		t.Error("a token was issued for a request that was refused")
	}
}

func TestFlowCancelled(t *testing.T) {
	server := fakeserver.New()
	defer server.Close()
	flow := signIn(t, server)
	flow.OpenBrowser = nil

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()
	if _, err := flow.Run(ctx); !errors.Is(err, context.Canceled) {
		t.Errorf("got %v", err)
	}
}

// get visits the loopback listener as a browser would.  It is called
// from the goroutine that plays the browser, so it reports a failure
// without stopping the test.
func get(t *testing.T, target string) (int, string) {
	t.Helper()
	resp, err := http.Get(target)
	if err != nil {
		t.Error(err)
		return 0, ""
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(body)
}

func TestFlowIgnoresAForgedCallback(t *testing.T) {
	server := fakeserver.New()
	defer server.Close()
	flow := signIn(t, server)

	redirect := RedirectURI(flow.Listener)
	flow.OpenBrowser = func(authURL string) error {
		go func() {
			// Something other than the authorization server calls back.
			// It is turned away and the sign-in goes on.
			status, body := get(t, redirect+"?code=stolen&state=guessed")
			if status != http.StatusBadRequest || !strings.Contains(body, "Sign-in failed") {
				t.Errorf("a forged callback got %d:\n%s", status, body)
			}
			get(t, authURL)
		}()
		return nil
	}

	token, err := flow.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if token.AccessToken == "" {
		t.Error("no token")
	}
	for _, r := range server.Requests() {
		if r.Path == "/oauth/token" && strings.Contains(string(r.Body), "stolen") {
			t.Error("the forged code was sent to the token endpoint")
		}
	}
}

func TestFlowForgedCallbackAloneTimesOut(t *testing.T) {
	server := fakeserver.New()
	defer server.Close()
	flow := signIn(t, server)
	flow.Timeout = 200 * time.Millisecond

	redirect := RedirectURI(flow.Listener)
	flow.OpenBrowser = func(string) error {
		go get(t, redirect+"?error=access_denied&state=guessed")
		return nil
	}
	if _, err := flow.Run(context.Background()); err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Errorf("got %v", err)
	}
	if n := server.CountRequests("POST", "/oauth/token"); n != 0 {
		t.Errorf("the token endpoint was called %d times", n)
	}
}

func TestCallbackServer(t *testing.T) {
	server := fakeserver.New()
	defer server.Close()
	flow := signIn(t, server)
	flow.Timeout = 2 * time.Second

	base := "http://" + flow.Listener.Addr().String()
	flow.OpenBrowser = func(authURL string) error {
		go func() {
			// Browsers ask for a favicon; that is not the callback.
			if status, _ := get(t, base+"/favicon.ico"); status != http.StatusNotFound {
				t.Errorf("/favicon.ico got %d", status)
			}
			resp, err := http.Post(base+CallbackPath, "text/plain", nil)
			if err != nil {
				t.Error(err)
				return
			}
			resp.Body.Close()
			if resp.StatusCode != http.StatusMethodNotAllowed {
				t.Errorf("POST to the callback got %d", resp.StatusCode)
			}
			// Then the real thing.
			status, body := get(t, authURL)
			if status != http.StatusOK || !strings.Contains(body, "signed in") {
				t.Errorf("the callback got %d:\n%s", status, body)
			}
		}()
		return nil
	}
	if _, err := flow.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestFlowPaste(t *testing.T) {
	server := fakeserver.New()
	defer server.Close()
	flow := signIn(t, server)

	// The browser is on another machine: it is redirected to a loopback
	// address it cannot load, and the user pastes that address.
	reader, writer := io.Pipe()
	flow.Paste = reader
	flow.OpenBrowser = func(authURL string) error {
		go func() {
			client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
			resp, err := client.Get(authURL)
			if err != nil {
				t.Error(err)
				return
			}
			resp.Body.Close()
			io.WriteString(writer, "\nwhat do I do\n"+resp.Header.Get("Location")+"\n")
		}()
		return nil
	}

	var out bytes.Buffer
	flow.Out = &out
	token, err := flow.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if token.AccessToken == "" {
		t.Error("no token")
	}
	if !strings.Contains(out.String(), "paste") {
		t.Errorf("the user was not told they can paste:\n%s", out.String())
	}
}

func TestFlowPasteWithTheWrongState(t *testing.T) {
	server := fakeserver.New()
	defer server.Close()
	flow := signIn(t, server)
	flow.OpenBrowser = nil
	flow.Timeout = 200 * time.Millisecond
	flow.Paste = strings.NewReader("http://127.0.0.1:1/callback?code=abc&state=wrong\nhello\n")

	var out bytes.Buffer
	flow.Out = &out
	if _, err := flow.Run(context.Background()); err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Errorf("got %v", err)
	}
	if !strings.Contains(out.String(), "another sign-in attempt") || !strings.Contains(out.String(), "not the address") {
		t.Errorf("the user was not told what was wrong with what they pasted:\n%s", out.String())
	}
	if n := server.CountRequests("POST", "/oauth/token"); n != 0 {
		t.Errorf("the token endpoint was called %d times", n)
	}
}

func TestFlowExchangeFailure(t *testing.T) {
	server := fakeserver.New()
	defer server.Close()
	flow := signIn(t, server)
	server.Lock()
	server.Fail = func(r *http.Request) int {
		if r.URL.Path == "/oauth/token" {
			return http.StatusBadGateway
		}
		return 0
	}
	server.Unlock()

	_, err := flow.Run(context.Background())
	if err == nil || !strings.Contains(err.Error(), "exchanging the authorization code failed") {
		t.Errorf("got %v", err)
	}
}

func TestListenPrefersThePortAsked(t *testing.T) {
	first, err := Listen(0)
	if err != nil {
		t.Fatal(err)
	}
	port := PortOf(RedirectURI(first))
	if port == 0 {
		t.Fatalf("no port in %s", RedirectURI(first))
	}

	// The port is taken, so another is found.
	second, err := Listen(port)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	if PortOf(RedirectURI(second)) == port {
		t.Error("two listeners share a port")
	}

	// Once it is free it is used again.
	first.Close()
	third, err := Listen(port)
	if err != nil {
		t.Fatal(err)
	}
	defer third.Close()
	if got := PortOf(RedirectURI(third)); got != port {
		t.Errorf("listening on %d, want %d", got, port)
	}
}

func TestRedirectURIIsLoopback(t *testing.T) {
	listener, err := Listen(0)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	uri := RedirectURI(listener)
	if !strings.HasPrefix(uri, "http://127.0.0.1:") || !strings.HasSuffix(uri, "/callback") {
		t.Errorf("unexpected redirect URI %q", uri)
	}
	if PortOf("not a url at all://") != 0 || PortOf("http://127.0.0.1/callback") != 0 {
		t.Error("PortOf should be 0 when there is no port")
	}
}

func TestAuthorizationURLKeepsAnExistingQuery(t *testing.T) {
	flow := &Flow{Metadata: &Metadata{AuthorizationEndpoint: "https://example.com/authorize?tenant=a"}, ClientID: "cid"}
	got := flow.AuthorizationURL("http://127.0.0.1:1/callback", "s", "c")
	if !strings.HasPrefix(got, "https://example.com/authorize?tenant=a&") {
		t.Errorf("got %q", got)
	}
	parsed, err := url.Parse(got)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Query().Get("scope") != "" || parsed.Query().Get("resource") != "" {
		t.Errorf("scope and resource should be left out when they are empty: %q", got)
	}
}
