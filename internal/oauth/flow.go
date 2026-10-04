package oauth

import (
	"bufio"
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"html"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// CallbackPath is the path of the loopback redirect URI.
const CallbackPath = "/callback"

// Listen opens the loopback listener the browser is redirected to.  It
// tries preferredPort first, so that a redirect URI registered on an
// earlier sign-in matches exactly, and falls back to any free port.
func Listen(preferredPort int) (net.Listener, error) {
	if preferredPort > 0 {
		if l, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(preferredPort))); err == nil {
			return l, nil
		}
	}
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("cannot listen on the loopback interface for the sign-in redirect: %w", err)
	}
	return l, nil
}

// RedirectURI is the redirect URI served by listener.
func RedirectURI(listener net.Listener) string {
	return "http://" + listener.Addr().String() + CallbackPath
}

// PortOf returns the port of a loopback redirect URI, or 0.
func PortOf(redirectURI string) int {
	u, err := url.Parse(redirectURI)
	if err != nil {
		return 0
	}
	port, _ := strconv.Atoi(u.Port())
	return port
}

// Flow is one run of the authorization code grant.
type Flow struct {
	HTTPClient *http.Client
	Metadata   *Metadata
	ClientID   string
	Scopes     []string
	// Resource is the protected resource the token is for (RFC 8707).
	Resource string
	// Listener receives the redirect; see Listen.
	Listener net.Listener
	// OpenBrowser opens the authorization URL.  Nil means the user opens
	// it themselves.
	OpenBrowser func(url string) error
	// Out receives the instructions for the user.
	Out io.Writer
	// Paste, if set, is read for a redirected URL pasted by a user whose
	// browser cannot reach this machine's loopback interface.
	Paste io.Reader
	// Timeout is how long to wait for the user; zero means five minutes.
	Timeout time.Duration
}

// messages writes what the user is told, from whichever goroutine has
// something to say, until it is closed.
type messages struct {
	mu     sync.Mutex
	w      io.Writer
	closed bool
}

func (m *messages) Write(p []byte) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return len(p), nil
	}
	return m.w.Write(p)
}

func (m *messages) close() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.closed = true
}

type callbackResult struct {
	code string
	err  error
}

// Run sends the user to the authorization server, waits for them to
// come back with a code, and exchanges it for a token.
func (f *Flow) Run(ctx context.Context) (*Token, error) {
	pkce, err := NewPKCE()
	if err != nil {
		return nil, err
	}
	state, err := randomString(24)
	if err != nil {
		return nil, err
	}
	redirectURI := RedirectURI(f.Listener)

	// The reader of pasted addresses outlives the sign-in when nothing
	// is pasted, and must not write once the sign-in is over.
	out := &messages{w: f.Out}
	defer out.close()

	timeout := f.Timeout
	if timeout == 0 {
		timeout = 5 * time.Minute
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	results := make(chan callbackResult, 2)
	server := &http.Server{
		Handler:           f.callbackHandler(state, results),
		ReadHeaderTimeout: 10 * time.Second,
	}
	go func() { _ = server.Serve(f.Listener) }()
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
	}()

	authURL := f.AuthorizationURL(redirectURI, state, pkce.Challenge)
	opened := false
	if f.OpenBrowser != nil {
		opened = f.OpenBrowser(authURL) == nil
	}
	if opened {
		fmt.Fprintf(out, "Opening your browser to sign in. If it does not open, visit:\n\n  %s\n\n", authURL)
	} else {
		fmt.Fprintf(out, "Open this URL in a browser to sign in:\n\n  %s\n\n", authURL)
	}
	if f.Paste != nil {
		fmt.Fprintf(out, "Waiting for you to approve the request. If the browser is on another machine and ends\non a page that cannot load, paste that page's address here and press Enter.\n")
		go readPasted(f.Paste, out, state, results)
	} else {
		fmt.Fprintln(out, "Waiting for you to approve the request...")
	}

	var code string
	select {
	case result := <-results:
		if result.err != nil {
			return nil, result.err
		}
		code = result.code
	case <-ctx.Done():
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return nil, fmt.Errorf("timed out after %s waiting for the sign-in to finish", timeout)
		}
		return nil, ctx.Err()
	}

	token, err := Exchange(ctx, f.HTTPClient, f.Metadata.TokenEndpoint, f.ClientID, code, redirectURI, pkce.Verifier, f.Resource)
	if err != nil {
		return nil, fmt.Errorf("exchanging the authorization code failed: %w", err)
	}
	return token, nil
}

// AuthorizationURL is the URL the user approves the request at.
func (f *Flow) AuthorizationURL(redirectURI, state, challenge string) string {
	query := url.Values{
		"response_type":         {"code"},
		"client_id":             {f.ClientID},
		"redirect_uri":          {redirectURI},
		"state":                 {state},
		"code_challenge":        {challenge},
		"code_challenge_method": {"S256"},
	}
	if len(f.Scopes) > 0 {
		query.Set("scope", strings.Join(f.Scopes, " "))
	}
	if f.Resource != "" {
		query.Set("resource", f.Resource)
	}
	separator := "?"
	if strings.Contains(f.Metadata.AuthorizationEndpoint, "?") {
		separator = "&"
	}
	return f.Metadata.AuthorizationEndpoint + separator + query.Encode()
}

func (f *Flow) callbackHandler(state string, results chan<- callbackResult) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != CallbackPath {
			http.NotFound(w, r)
			return
		}
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		code, err := parseCallback(r.URL.Query(), state)
		if err != nil {
			writePage(w, http.StatusBadRequest, "Sign-in failed", sentence(err.Error())+" Return to the terminal and try again.")
		} else {
			writePage(w, http.StatusOK, "You're signed in", "The Streaming Chasers CLI is connected. You can close this window and return to the terminal.")
		}
		// A request without the state did not come from the sign-in in
		// progress.  It is turned away, and the sign-in goes on waiting
		// for the one that does, so that nothing else on the machine can
		// end it by calling this address.
		if errors.Is(err, errState) {
			return
		}
		select {
		case results <- callbackResult{code: code, err: err}:
		default:
		}
	})
}

// errState is returned for a callback that does not carry the state of
// the sign-in in progress.
var errState = errors.New("the sign-in response does not belong to this sign-in attempt (state mismatch)")

// parseCallback checks the redirect's query against the state that was
// sent and returns the authorization code.
func parseCallback(query url.Values, state string) (string, error) {
	// State is checked first: an error or a code that arrives without
	// the state this process generated did not come from its request.
	if subtle.ConstantTimeCompare([]byte(query.Get("state")), []byte(state)) != 1 {
		return "", errState
	}
	if code := query.Get("error"); code != "" {
		if code == "access_denied" {
			return "", errors.New("the request was not approved")
		}
		return "", &Error{Code: code, Description: query.Get("error_description")}
	}
	code := query.Get("code")
	if code == "" {
		return "", errors.New("the sign-in response carried no authorization code")
	}
	return code, nil
}

// readPasted waits for the user to paste the URL their browser was
// redirected to.  Lines that are not such a URL are ignored.
func readPasted(in io.Reader, out io.Writer, state string, results chan<- callbackResult) {
	scanner := bufio.NewScanner(in)
	scanner.Buffer(make([]byte, 0, 4096), 64*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		u, err := url.Parse(line)
		if err != nil || u.Path != CallbackPath || len(u.Query()) == 0 {
			fmt.Fprintf(out, "That is not the address of the page the sign-in ended on; it starts with http://127.0.0.1 and has %s in it.\n", CallbackPath)
			continue
		}
		code, err := parseCallback(u.Query(), state)
		if errors.Is(err, errState) {
			fmt.Fprintln(out, "That address is from another sign-in attempt. Open the address printed above and paste where that ends.")
			continue
		}
		select {
		case results <- callbackResult{code: code, err: err}:
		default:
		}
		return
	}
}

// sentence turns an error message into a sentence for the browser page.
func sentence(message string) string {
	if message == "" {
		return ""
	}
	return strings.ToUpper(message[:1]) + message[1:] + "."
}

func writePage(w http.ResponseWriter, status int, title, message string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.WriteHeader(status)
	fmt.Fprintf(w, `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>%[1]s · Streaming Chasers CLI</title>
<style>
  body { font-family: system-ui, -apple-system, "Segoe UI", sans-serif; background: #f6f7f9; color: #1d2330; margin: 0; }
  main { max-width: 28rem; margin: 18vh auto 0; padding: 2rem; background: #fff; border-radius: 12px; box-shadow: 0 1px 3px rgba(0,0,0,.12); }
  h1 { font-size: 1.35rem; margin: 0 0 .75rem; }
  p { line-height: 1.5; margin: 0; color: #4a5263; }
  @media (prefers-color-scheme: dark) {
    body { background: #12151c; color: #e8eaf0; }
    main { background: #1c212c; }
    p { color: #aab1c2; }
  }
</style>
</head>
<body><main><h1>%[1]s</h1><p>%[2]s</p></main></body>
</html>
`, html.EscapeString(title), html.EscapeString(message))
}
