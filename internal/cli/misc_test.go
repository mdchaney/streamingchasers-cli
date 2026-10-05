package cli

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/mdchaney/streamingchasers-cli/internal/api"
)

func TestOutputFormats(t *testing.T) {
	h := signedIn(t)
	works := decode[[]map[string]any](t, h.ok("works", "list", "-o", "json"))
	if len(works) != 2 || works[0]["title"] != "Drunken Daisy" {
		t.Errorf("unexpected works: %v", works)
	}
	if got := lines(h.ok("works", "list", "-o", "jsonl").stdout); len(got) != 2 {
		t.Errorf("jsonl: %v", got)
	}
	csv := h.ok("works", "list", "-o", "csv").stdout
	want(t, csv, "external_id", "title", "url", "W-999,W-999,English,EN,Drunken Daisy")
	if n := len(lines(csv)); n != 3 {
		t.Errorf("csv has %d lines", n)
	}
	work := decode[map[string]any](t, h.ok("works", "get", "W-999", "-o", "json"))
	if work["title"] != "Drunken Daisy" {
		t.Errorf("unexpected work: %v", work)
	}
	h.fails(ExitUsage, "unknown output format", "works", "list", "-o", "xml")
}

func TestPagination(t *testing.T) {
	h := signedIn(t)
	h.server.Lock()
	company := h.server.Company(2)
	for i := 0; i < 60; i++ {
		company.Writers = append(company.Writers, map[string]any{"external_id": "X-" + strings.Repeat("0", 2-len(itoa(i))) + itoa(i), "first_name": "W", "last_name": "Writer " + itoa(i), "controlled": false})
	}
	h.server.Unlock()

	r := h.ok("writers", "list")
	if n := len(lines(r.stdout)); n != 26 {
		t.Errorf("%d lines, want a header and 25", n)
	}
	want(t, r.stderr, "Showing 25 of 62 writers (page 1 of 3). Use --page 2 for the next page or --all for everything.")
	r = h.ok("writers", "list", "--page", "3")
	if n := len(lines(r.stdout)); n != 13 {
		t.Errorf("%d lines on the last page", n)
	}
	r = h.ok("writers", "list", "--all", "-o", "jsonl")
	if n := len(lines(r.stdout)); n != 62 {
		t.Errorf("--all read %d", n)
	}
	if n := h.server.CountRequests("GET", "/api/v1/companies/2/writers"); n != 3 {
		t.Errorf("%d requests for --all", n)
	}
	r = h.ok("writers", "list", "--limit", "30", "-o", "jsonl")
	if n := len(lines(r.stdout)); n != 30 {
		t.Errorf("--limit read %d", n)
	}
	want(t, h.ok("writers", "list", "--per-page", "5").stderr, "Showing 5 of 62 writers (page 1 of 13)")
	want(t, h.ok("writers", "list", "--page", "99").stderr, "No writers on page 99; there are 3 pages.")
	h.fails(ExitUsage, "--per-page must be between 1 and 200", "writers", "list", "--per-page", "500")
	h.fails(ExitUsage, "cannot be used together", "writers", "list", "--all", "--limit", "3")
}

func itoa(i int) string {
	return strings.TrimSpace(strings.Repeat(" ", 0) + fmtInt(i))
}

func fmtInt(i int) string {
	if i == 0 {
		return "0"
	}
	s := ""
	for i > 0 {
		s = string(rune('0'+i%10)) + s
		i /= 10
	}
	return s
}

func TestAPICommand(t *testing.T) {
	h := signedIn(t)
	r := h.ok("api", "companies/2/works", "--query", "per_page=1")
	want(t, r.stdout, `"works": [`, `"pagination"`)
	if req := h.lastRequest("GET", "/api/v1/companies/2/works"); req.Query != "per_page=1" {
		t.Errorf("query = %q", req.Query)
	}
	r = h.ok("api", "companies/2/writers", "--data", `{"writer": {"external_id": "W-9", "last_name": "Raw"}}`, "-i")
	want(t, r.stdout, "201 Created", "Content-Type: application/json", `"last_name": "Raw"`)
	r = h.ok("api", "/api/v1/companies/2/writers/W-9", "-X", "DELETE")
	if r.stdout != "" {
		t.Errorf("stdout = %q", r.stdout)
	}
	want(t, h.ok("api", "companies/2/sales/paid", "--raw").stdout, "work,title")
	h.fails(ExitUsage, "--method must be", "api", "x", "-X", "FETCH")
	h.fails(ExitUsage, "--data must be JSON", "api", "x", "--data", "nope")
	h.fails(ExitNotFound, "the server has no such endpoint", "api", "/nowhere")
}

func TestDebugLogsRequestsWithoutSecrets(t *testing.T) {
	h := signedIn(t)
	h.stdin = strings.NewReader("pw\n")
	r := h.ok("account", "password", "--debug")
	want(t, r.stderr, "> PATCH "+h.server.URL+"/api/v1/users/me", `"password":"(not shown)"`, "< 200 OK")
	unwanted(t, r.stderr, `"pw"`, ownerToken)
}

func TestRetries(t *testing.T) {
	h := signedIn(t)
	failures := 0
	h.server.Fail = func(r *http.Request) int {
		if failures < 2 && r.Method == http.MethodGet {
			failures++
			return http.StatusBadGateway
		}
		return 0
	}
	want(t, h.ok("works", "list").stdout, "Drunken Daisy")

	// A create is not repeated, unless it carries an idempotency key.
	failures = 0
	h.server.Fail = func(r *http.Request) int {
		if failures < 1 && r.Method == http.MethodPost {
			failures++
			return http.StatusServiceUnavailable
		}
		return 0
	}
	h.fails(ExitError, "503", "writers", "create", "--id", "W-5", "--last-name", "X")
	failures = 0
	want(t, h.ok("works-uploads", "create", "--file", h.write("c.csv", "a\n"), "--format", "1").stderr, "Uploaded")
}

func TestRateLimit(t *testing.T) {
	h := signedIn(t)
	h.server.Fail = func(r *http.Request) int { return 0 }
	h.server.Lock()
	h.server.Fail = nil
	h.server.Unlock()
	// The fake has no limiter; the message shape is checked at the client.
	h.server.Fail = func(r *http.Request) int { return http.StatusTooManyRequests }
	h.fails(ExitError, "429", "works", "list")
}

func TestInterrupt(t *testing.T) {
	h := signedIn(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r := h.runContext(ctx, "works", "list")
	if r.code != ExitInterrupted || !strings.Contains(r.stderr, "Interrupted.") {
		t.Errorf("exit %d: %s", r.code, r.stderr)
	}
}

func TestUsageErrors(t *testing.T) {
	h := signedIn(t)
	r := h.fails(ExitUsage, `unknown command "wrks"`, "wrks")
	want(t, r.stderr, "Did you mean this?", "works")
	h.fails(ExitUsage, "unknown flag", "works", "list", "--nope")
	h.fails(ExitUsage, "missing EXTERNAL_ID", "works", "get")
	want(t, h.ok("version").stdout, "streamingchasers test (API "+api.SpecVersion+")")
	want(t, h.ok("config", "list").stdout, "host", "company")
	h.fails(ExitUsage, "there is no setting called", "config", "get", "nope")
}

func TestDeleteAsksAtTheTerminal(t *testing.T) {
	h := signedIn(t)
	h.interactive = true
	h.stdin = strings.NewReader("n\n")
	r := h.fails(ExitError, "Nothing was deleted.", "works", "delete", "W-999")
	want(t, r.stderr, "Delete work W-999? This cannot be undone. [y/N]")
	want(t, h.ok("works", "get", "W-999").stdout, "Drunken Daisy")
	h.stdin = strings.NewReader("y\n")
	want(t, h.ok("works", "delete", "W-999").stderr, "Deleted work W-999.")
}
