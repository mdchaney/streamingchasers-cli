package cli

import (
	"strings"
	"testing"
	"time"
)

func TestLoginWithToken(t *testing.T) {
	h := newHarness(t)
	h.stdin = strings.NewReader(ownerToken + "\n")

	r := h.ok("auth", "login", "--with-token")
	want(t, r.stderr, "Signed in to "+h.server.URL+" with an API token.")
	if r.stdout != "" {
		t.Errorf("standard output should be empty, got %q", r.stdout)
	}
	if creds := h.credentials(); creds == nil || creds.APIToken != ownerToken || creds.OAuth != nil {
		t.Errorf("unexpected credentials: %+v", creds)
	}
	want(t, h.ok("ping").stdout, "is up and accepted your credentials as Floyd Lawson <floyd@example.com>")
}

func TestLoginWithABadToken(t *testing.T) {
	h := newHarness(t)
	h.stdin = strings.NewReader("not-a-token\n")
	h.fails(ExitError, "did not accept that API token", "auth", "login", "--with-token")
	if creds := h.credentials(); creds != nil {
		t.Errorf("credentials were stored: %+v", creds)
	}
}

func TestLoginWithOAuth(t *testing.T) {
	h := newHarness(t)
	r := h.ok("auth", "login")
	want(t, r.stderr, "Opening your browser to sign in", h.server.URL+"/oauth/authorize?",
		"Signed in to "+h.server.URL+". The CLI may: read your catalog and change your catalog.")

	creds := h.credentials()
	if creds == nil || creds.OAuth == nil || creds.Client == nil || creds.APIToken != "" {
		t.Fatalf("unexpected credentials: %+v", creds)
	}
	if creds.OAuth.Scope != "catalog catalog_write" {
		t.Errorf("scope = %q", creds.OAuth.Scope)
	}
	if creds.OAuth.Resource != h.server.URL+"/api/v1" {
		t.Errorf("resource = %q", creds.OAuth.Resource)
	}
	if left := time.Until(creds.OAuth.ExpiresAt); left < 59*time.Minute || left > 61*time.Minute {
		t.Errorf("the token expires in %s, want an hour", left)
	}
	tokens := h.server.Tokens()
	if len(tokens) != 1 || tokens[0].Resource != h.server.URL+"/api/v1" {
		t.Errorf("unexpected tokens on the server: %+v", tokens)
	}

	// It works for reading and writing, but not for administering.
	h.env["STREAMINGCHASERS_COMPANY"] = "2"
	want(t, h.ok("works", "list").stdout, "Drunken Daisy")
	h.ok("writers", "create", "--id", "W-300", "--last-name", "Parton")
	r = h.fails(ExitForbidden, "does not carry the catalog_admin scope", "companies", "update", "2", "--name", "X")
	want(t, r.stderr, "streamingchasers auth login --host "+h.server.URL+" --admin")
	// Nor may a read-only sign-in read the unscoped account token.
	h.fails(ExitError, "only to the token itself or to a sign-in with --admin", "account", "token")
	unwanted(t, h.ok("account", "show", "-o", "json").stdout, "auth_token")
}

func TestLoginWithAdmin(t *testing.T) {
	h := newHarness(t)
	h.ok("auth", "login", "--admin")
	if creds := h.credentials(); creds.OAuth.Scope != "catalog catalog_write catalog_admin" {
		t.Errorf("scope = %q", creds.OAuth.Scope)
	}
	h.env["STREAMINGCHASERS_COMPANY"] = "2"
	want(t, h.ok("companies", "update", "2", "--description", "Now with admin").stdout, "Now with admin")

	// A registration is kept from one sign-in to the next.
	h.ok("auth", "login")
	if n := h.server.ClientCount(); n != 1 {
		t.Errorf("%d clients registered, want 1", n)
	}
}

func TestOAuthTokenIsRefreshed(t *testing.T) {
	h := newHarness(t)
	h.ok("auth", "login")
	before := h.credentials().OAuth.AccessToken
	h.server.ExpireAccessTokens()
	h.env["STREAMINGCHASERS_COMPANY"] = "2"
	want(t, h.ok("works", "list").stdout, "Drunken Daisy")
	if after := h.credentials().OAuth.AccessToken; after == before {
		t.Error("the access token was not refreshed")
	}
}

func TestRevokedSignInIsReported(t *testing.T) {
	h := newHarness(t)
	h.ok("auth", "login")
	h.server.RevokeTokens()
	h.server.ExpireAccessTokens()
	h.env["STREAMINGCHASERS_COMPANY"] = "2"
	h.fails(ExitAuth, "has expired or been disconnected", "works", "list")
}

func TestLoginDenied(t *testing.T) {
	h := newHarness(t)
	h.server.Deny = true
	h.fails(ExitError, "the request was not approved", "auth", "login")
}

func TestLogout(t *testing.T) {
	h := newHarness(t)
	h.ok("auth", "login")
	r := h.ok("auth", "logout")
	want(t, r.stderr, "Signed out of "+h.server.URL+" and disconnected the CLI from your account.")
	for _, token := range h.server.Tokens() {
		if !token.Revoked {
			t.Errorf("a token was left working: %+v", token)
		}
	}
	h.fails(ExitAuth, "not signed in", "companies", "list")

	h.signIn(ownerToken)
	want(t, h.ok("auth", "logout").stderr, "The API token itself still works")
	want(t, h.ok("auth", "logout").stderr, "Not signed in")
}

func TestStatus(t *testing.T) {
	h := signedIn(t)
	h.ok("companies", "use", "Frivolous Music")
	r := h.ok("auth", "status")
	want(t, r.stdout, h.server.URL, "Signed in with:   an API token", "Default company:  2", "Signed in as:     Floyd Lawson <floyd@example.com>", "up, and it accepts these credentials")

	h = newHarness(t)
	h.fails(ExitAuth, "not signed in", "auth", "status")

	h.env["STREAMINGCHASERS_TOKEN"] = ownerToken
	want(t, h.ok("auth", "status").stdout, "the token in STREAMINGCHASERS_TOKEN")
	h.fails(ExitUsage, "STREAMINGCHASERS_TOKEN is set", "auth", "login")
}

func TestTokenCommand(t *testing.T) {
	h := signedIn(t)
	if got := h.ok("auth", "token").stdout; got != ownerToken+"\n" {
		t.Errorf("got %q", got)
	}
}

func TestBadCredentialsExitCode(t *testing.T) {
	h := newHarness(t)
	h.signIn("stale")
	r := h.fails(ExitAuth, "Bad credentials", "companies", "list")
	want(t, r.stderr, "did not accept your credentials")
}

func TestHostSelection(t *testing.T) {
	h := newHarness(t)
	delete(h.env, "STREAMINGCHASERS_HOST")
	h.fails(ExitUsage, "invalid host", "companies", "list", "--host", "ftp://nope")
	h.ok("config", "set", "host", h.server.URL)
	want(t, h.ok("config", "get", "host").stdout, h.server.URL)
	h.signIn(ownerToken)
	want(t, h.ok("companies", "list").stdout, "Frivolous Music")
}
