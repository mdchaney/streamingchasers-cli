package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mdchaney/streamingchasers-api/internal/config"
	"github.com/mdchaney/streamingchasers-api/internal/fakeserver"
)

// Tokens of the fake server's users.
const (
	ownerToken  = "floyd-api-token"
	viewerToken = "crump-api-token"
	editorToken = "barney-api-token"
)

// harness runs the command against a fake server, with a terminal, an
// environment and a configuration directory of its own.
type harness struct {
	t      *testing.T
	server *fakeserver.Server
	store  *config.Store
	env    map[string]string

	// stdin is what the command reads.
	stdin io.Reader
	// interactive says a person is at the terminal.
	interactive bool
	// progress says standard error is a terminal to draw progress on.
	progress bool
	// browser stands in for the user's browser.
	browser func(url string) error
	// secrets are what the user types when asked for one, in order.
	secrets []string
	// now, if set, is the clock.
	now func() time.Time
}

// result is what a run of the command produced.
type result struct {
	stdout string
	stderr string
	code   int
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	server := fakeserver.New()
	t.Cleanup(server.Close)
	h := &harness{
		t:      t,
		server: server,
		store:  &config.Store{Dir: filepath.Join(t.TempDir(), "streamingchasers")},
		env:    map[string]string{"STREAMINGCHASERS_HOST": server.URL},
	}
	h.browser = h.approvingBrowser
	return h
}

// approvingBrowser is a browser whose user is signed in to the server:
// it opens the authorization page and follows the redirect back.
func (h *harness) approvingBrowser(url string) error {
	go func() {
		resp, err := http.Get(url)
		if err != nil {
			h.t.Errorf("the browser could not open %s: %v", url, err)
			return
		}
		resp.Body.Close()
	}()
	return nil
}

func (h *harness) app() *App {
	stdin := h.stdin
	if stdin == nil {
		stdin = strings.NewReader("")
	}
	return &App{
		In:          stdin,
		Getenv:      func(key string) string { return h.env[key] },
		Store:       h.store,
		HTTPClient:  h.server.Client(),
		OpenBrowser: h.browser,
		Interactive: h.interactive,
		Progress:    h.progress,
		ReadSecret: func(string) (string, error) {
			if len(h.secrets) == 0 {
				return "", nil
			}
			secret := h.secrets[0]
			h.secrets = h.secrets[1:]
			return secret, nil
		},
		Version:      "test",
		LoginTimeout: 5 * time.Second,
		RetryDelay:   time.Millisecond,
		Now:          h.now,
		Sleep:        func(context.Context, time.Duration) error { return nil },
	}
}

// run runs the command.
func (h *harness) run(args ...string) result {
	h.t.Helper()
	return h.runContext(context.Background(), args...)
}

func (h *harness) runContext(ctx context.Context, args ...string) result {
	h.t.Helper()
	var stdout, stderr bytes.Buffer
	app := h.app()
	app.Out, app.Err = &stdout, &stderr
	code := app.Run(ctx, args)
	// What was typed is read once.
	h.stdin = nil
	return result{stdout: stdout.String(), stderr: stderr.String(), code: code}
}

// ok runs the command and fails the test unless it succeeds.
func (h *harness) ok(args ...string) result {
	h.t.Helper()
	r := h.run(args...)
	if r.code != ExitOK {
		h.t.Fatalf("streamingchasers %s: exit %d\nstdout:\n%s\nstderr:\n%s", strings.Join(args, " "), r.code, r.stdout, r.stderr)
	}
	return r
}

// fails runs the command and fails the test unless it exits with code
// and says what is wanted on standard error.
func (h *harness) fails(code int, want string, args ...string) result {
	h.t.Helper()
	r := h.run(args...)
	if r.code != code {
		h.t.Fatalf("streamingchasers %s: exit %d, want %d\nstdout:\n%s\nstderr:\n%s", strings.Join(args, " "), r.code, code, r.stdout, r.stderr)
	}
	if !strings.Contains(r.stderr, want) {
		h.t.Fatalf("streamingchasers %s: standard error does not say %q:\n%s", strings.Join(args, " "), want, r.stderr)
	}
	return r
}

// signIn stores an API token, as 'auth login --with-token' does.
func (h *harness) signIn(token string) {
	h.t.Helper()
	creds, err := h.store.LoadCredentials()
	if err != nil {
		h.t.Fatal(err)
	}
	creds.Host(h.server.URL).APIToken = token
	creds.Host(h.server.URL).OAuth = nil
	if err := h.store.SaveCredentials(creds); err != nil {
		h.t.Fatal(err)
	}
}

// signedIn returns a harness whose user owns the company and works in it.
func signedIn(t *testing.T) *harness {
	t.Helper()
	h := newHarness(t)
	h.signIn(ownerToken)
	h.env["STREAMINGCHASERS_COMPANY"] = "2"
	return h
}

// as returns a harness signed in as another of the company's users.
func as(t *testing.T, token string) *harness {
	t.Helper()
	h := newHarness(t)
	h.signIn(token)
	h.env["STREAMINGCHASERS_COMPANY"] = "2"
	return h
}

// credentials returns what is stored for the server.
func (h *harness) credentials() *config.HostCredentials {
	h.t.Helper()
	creds, err := h.store.LoadCredentials()
	if err != nil {
		h.t.Fatal(err)
	}
	return creds.Lookup(h.server.URL)
}

// write writes a file and returns its path.
func (h *harness) write(name, content string) string {
	h.t.Helper()
	path := filepath.Join(h.t.TempDir(), name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		h.t.Fatal(err)
	}
	return path
}

// lastRequest returns the last request to method and path.
func (h *harness) lastRequest(method, path string) fakeserver.Request {
	h.t.Helper()
	requests := h.server.Requests()
	for i := len(requests) - 1; i >= 0; i-- {
		if requests[i].Method == method && requests[i].Path == path {
			return requests[i]
		}
	}
	h.t.Fatalf("no %s %s was made; the requests were:\n%s", method, path, describe(requests))
	return fakeserver.Request{}
}

// lastBody returns the JSON body of the last request to method and path.
func (h *harness) lastBody(method, path string) map[string]any {
	h.t.Helper()
	request := h.lastRequest(method, path)
	var body map[string]any
	if err := json.Unmarshal(request.Body, &body); err != nil {
		h.t.Fatalf("the body of %s %s is not JSON: %v\n%s", method, path, err, request.Body)
	}
	return body
}

func describe(requests []fakeserver.Request) string {
	var b strings.Builder
	for _, r := range requests {
		fmt.Fprintf(&b, "  %s %s", r.Method, r.Path)
		if r.Query != "" {
			fmt.Fprintf(&b, "?%s", r.Query)
		}
		b.WriteByte('\n')
	}
	return b.String()
}

// lines splits output into its lines.
func lines(s string) []string {
	s = strings.TrimRight(s, "\n")
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

// decode reads standard output as JSON.
func decode[T any](t *testing.T, r result) T {
	t.Helper()
	var v T
	if err := json.Unmarshal([]byte(r.stdout), &v); err != nil {
		t.Fatalf("standard output is not the JSON expected: %v\n%s", err, r.stdout)
	}
	return v
}

// want fails the test unless s contains every one of parts.
func want(t *testing.T, s string, parts ...string) {
	t.Helper()
	for _, part := range parts {
		if !strings.Contains(s, part) {
			t.Errorf("missing %q in:\n%s", part, s)
		}
	}
}

// unwanted fails the test if s contains any of parts.
func unwanted(t *testing.T, s string, parts ...string) {
	t.Helper()
	for _, part := range parts {
		if strings.Contains(s, part) {
			t.Errorf("unexpected %q in:\n%s", part, s)
		}
	}
}
