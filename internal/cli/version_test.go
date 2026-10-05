package cli

import (
	"testing"

	"github.com/mdchaney/streamingchasers-cli/internal/api"
)

func TestVersionHandshake(t *testing.T) {
	h := signedIn(t)
	h.ok("works", "list")
	if got := h.lastRequest("GET", "/api/v1/companies/2/works").Headers.Get("X-Client-API-Version"); got != api.SpecVersion {
		t.Errorf("X-Client-API-Version = %q", got)
	}

	// The server and this program agree: nothing is said of it.
	if r := h.ok("works", "list"); r.stderr != "" {
		t.Errorf("standard error = %q", r.stderr)
	}
	for _, args := range [][]string{{"version"}, {"--version"}} {
		r := h.ok(args...)
		want(t, r.stdout, "streamingchasers test (API "+api.SpecVersion+")", h.server.URL+" speaks API "+api.SpecVersion+": the same as this program")
	}
	want(t, h.ok("auth", "status").stdout, "API version:      "+api.SpecVersion+" on the server, "+api.SpecVersion+" in this program (current)")
	want(t, h.ok("works", "list", "--debug").stderr, "> X-Client-API-Version: "+api.SpecVersion, "< X-API-Version: "+api.SpecVersion+" (current)")
}

func TestAServerOlderThanTheProgram(t *testing.T) {
	h := signedIn(t)
	h.server.APIVersion = "1.0.0"

	// What works is not remarked on.
	if r := h.ok("works", "list"); r.stderr != "" {
		t.Errorf("standard error = %q", r.stderr)
	}
	// What fails is explained: the server may not have it yet.
	r := h.fails(ExitNotFound, "the server has no such endpoint", "api", "companies/2/not_yet")
	want(t, r.stderr, "Note: this server speaks API 1.0.0, older than this program (built for "+api.SpecVersion+"). What failed may be something the server has not got yet.")
	r = h.fails(ExitNotFound, "Not found", "works", "get", "W-404")
	want(t, r.stderr, "older than this program")
	want(t, h.ok("version").stdout, "speaks API 1.0.0: older than this program; some commands may not work until the server is updated")
}

func TestAServerNewerThanTheProgram(t *testing.T) {
	h := signedIn(t)
	h.server.APIVersion = "9.0.0"
	r := h.ok("works", "list")
	want(t, r.stdout, "Drunken Daisy")
	want(t, r.stderr, "Note: the server speaks API 9.0.0 and this program was built for "+api.SpecVersion+"; upgrade streamingchasers.")
	r = h.fails(ExitNotFound, "Not found", "works", "get", "W-404")
	want(t, r.stderr, "upgrade streamingchasers")
	want(t, h.ok("version").stdout, "newer than this program; upgrade streamingchasers")
}

func TestVersionWithoutAServer(t *testing.T) {
	// Before signing in, and of a server from before the handshake.
	h := newHarness(t)
	want(t, h.ok("version").stdout, "speaks API "+api.SpecVersion)
	h.server.APIVersion = ""
	want(t, h.ok("version").stdout, "does not say what API version it speaks")
	h.server.Close()
	want(t, h.ok("version").stdout, "streamingchasers test", "could not be reached")
}
