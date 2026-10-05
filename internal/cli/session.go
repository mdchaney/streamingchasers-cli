package cli

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mdchaney/streamingchasers-cli/internal/api"
	"github.com/mdchaney/streamingchasers-cli/internal/config"
	"github.com/mdchaney/streamingchasers-cli/internal/oauth"
	"github.com/mdchaney/streamingchasers-cli/internal/output"
)

// refreshLeeway is how long before its expiry an access token is
// replaced, so that a request is not sent with a token that expires on
// the way.
const refreshLeeway = 30 * time.Second

// session is what a command needs to talk to a server.
type session struct {
	app    *App
	store  *config.Store
	host   string
	cfg    *config.Config
	client *api.Client
	auth   *authorizer
}

func (a *App) store() (*config.Store, error) {
	if a.Store != nil {
		return a.Store, nil
	}
	dir, err := config.DefaultDir(a.getenv)
	if err != nil {
		return nil, err
	}
	return &config.Store{Dir: dir}, nil
}

func (a *App) httpClient() *http.Client {
	if a.HTTPClient != nil {
		return a.HTTPClient
	}
	return &http.Client{Timeout: a.globals.timeout}
}

func (a *App) userAgent() string {
	return fmt.Sprintf("streamingchasers-cli/%s (%s; %s)", a.Version, runtime.GOOS, runtime.GOARCH)
}

// resolveHost picks the server: --host, then $STREAMINGCHASERS_HOST,
// then the configured default, then the production service.
func (a *App) resolveHost(cfg *config.Config) (string, error) {
	for _, candidate := range []string{a.globals.host, a.getenv("STREAMINGCHASERS_HOST"), cfg.DefaultHost} {
		if candidate != "" {
			host, err := config.NormalizeHost(candidate)
			if err != nil {
				return "", usagef("%s", err)
			}
			return host, nil
		}
	}
	return config.DefaultHost, nil
}

// session loads the settings and credentials for the host in use.  It
// does not fail for want of credentials; the first request does.
func (a *App) session() (*session, error) {
	store, err := a.store()
	if err != nil {
		return nil, err
	}
	cfg, err := store.LoadConfig()
	if err != nil {
		return nil, err
	}
	host, err := a.resolveHost(cfg)
	if err != nil {
		return nil, err
	}
	// Remembered for the hint printed when the server answers 401.
	a.globals.host = host

	auth := &authorizer{app: a, store: store, host: host, static: a.getenv("STREAMINGCHASERS_TOKEN")}
	if auth.static == "" {
		creds, err := store.LoadCredentials()
		if err != nil {
			return nil, err
		}
		auth.load(creds)
	}

	s := &session{app: a, store: store, host: host, cfg: cfg, auth: auth}
	s.client = a.newClient(host, auth)
	return s, nil
}

func (a *App) newClient(host string, auth api.Authorizer) *api.Client {
	client := &api.Client{
		BaseURL:    host,
		HTTPClient: a.httpClient(),
		Auth:       auth,
		UserAgent:  a.userAgent(),
		Retries:    2,
		RetryDelay: a.RetryDelay,
		APIVersion: api.SpecVersion,
		Versions:   &a.versions,
	}
	if client.RetryDelay == 0 {
		client.RetryDelay = 500 * time.Millisecond
	}
	if a.globals.debug {
		client.Debug = a.Err
	}
	return client
}

// authorizer supplies the token for a host: $STREAMINGCHASERS_TOKEN, a
// stored API token, or a stored OAuth token that it keeps fresh.
type authorizer struct {
	app   *App
	store *config.Store
	host  string

	mu       sync.Mutex
	static   string
	oauth    *config.OAuthToken
	clientID string
}

func (z *authorizer) load(creds *config.Credentials) {
	z.static, z.oauth, z.clientID = "", nil, ""
	host := creds.Lookup(z.host)
	if host == nil {
		return
	}
	switch {
	case host.OAuth != nil && host.OAuth.AccessToken != "":
		token := *host.OAuth
		z.oauth = &token
		if host.Client != nil {
			z.clientID = host.Client.ClientID
		}
	case host.APIToken != "":
		z.static = host.APIToken
	}
}

// Token implements api.Authorizer.
func (z *authorizer) Token(ctx context.Context) (string, error) {
	z.mu.Lock()
	defer z.mu.Unlock()

	if z.static != "" {
		return z.static, nil
	}
	if z.oauth == nil {
		return "", &errNotSignedIn{host: z.host}
	}
	if z.oauth.RefreshToken != "" && z.oauth.Expired(z.app.now(), refreshLeeway) {
		if err := z.refresh(ctx); err != nil {
			return "", err
		}
	}
	return z.oauth.AccessToken, nil
}

// Refresh implements api.Authorizer.
func (z *authorizer) Refresh(ctx context.Context, rejected string) (string, bool, error) {
	z.mu.Lock()
	defer z.mu.Unlock()

	if z.static != "" || z.oauth == nil || z.oauth.RefreshToken == "" {
		return "", false, nil
	}
	// Another request may have replaced the token while this one was
	// on its way.
	if z.oauth.AccessToken != rejected {
		return z.oauth.AccessToken, true, nil
	}
	if err := z.refresh(ctx); err != nil {
		return "", false, err
	}
	return z.oauth.AccessToken, true, nil
}

// refresh replaces the access token and stores the result.  The server
// rotates refresh tokens, so the stored one is read again first: another
// process may have used the one in memory already.
func (z *authorizer) refresh(ctx context.Context) error {
	creds, err := z.store.LoadCredentials()
	if err != nil {
		return err
	}
	if stored := creds.Lookup(z.host); stored != nil && stored.OAuth != nil {
		if stored.OAuth.AccessToken != z.oauth.AccessToken && !stored.OAuth.Expired(z.app.now(), refreshLeeway) {
			token := *stored.OAuth
			z.oauth = &token
			return nil
		}
		if stored.OAuth.RefreshToken != "" {
			z.oauth.RefreshToken = stored.OAuth.RefreshToken
		}
	}

	issued := z.app.now()
	token, err := oauth.Refresh(ctx, z.app.httpClient(), z.oauth.TokenEndpoint, z.clientID, z.oauth.RefreshToken)
	if err != nil {
		var oauthErr *oauth.Error
		if errors.As(err, &oauthErr) {
			return &errNotSignedIn{host: z.host, reason: fmt.Sprintf("your sign-in to %s has expired or been disconnected (%s)", z.host, oauthErr.Code)}
		}
		return fmt.Errorf("refreshing the sign-in to %s failed: %w", z.host, err)
	}

	z.oauth.AccessToken = token.AccessToken
	z.oauth.ExpiresAt = token.ExpiresAt(issued).UTC()
	if token.RefreshToken != "" {
		z.oauth.RefreshToken = token.RefreshToken
	}
	if token.Scope != "" {
		z.oauth.Scope = token.Scope
	}

	stored := *z.oauth
	creds.Host(z.host).OAuth = &stored
	creds.Host(z.host).APIToken = ""
	return z.store.SaveCredentials(creds)
}

// companyID returns the company to work in: --company, then
// $STREAMINGCHASERS_COMPANY, then the configured default.  A value that
// is not a number is taken as a company's name.
func (s *session) companyID(ctx context.Context) (int64, error) {
	selected := s.app.globals.company
	if selected == "" {
		selected = s.app.getenv("STREAMINGCHASERS_COMPANY")
	}
	if selected == "" {
		if host, ok := s.cfg.Hosts[s.host]; ok && host.DefaultCompany != 0 {
			return host.DefaultCompany, nil
		}
		return 0, usagef("no company selected; pass --company or choose one with 'streamingchasers companies use'")
	}
	return s.findCompany(ctx, selected)
}

// findCompany turns an ID or a name into a company ID.
func (s *session) findCompany(ctx context.Context, selected string) (int64, error) {
	if id, err := strconv.ParseInt(selected, 10, 64); err == nil {
		return id, nil
	}
	record, err := s.findByName(ctx, api.Path("companies"), "companies", "company", selected)
	if err != nil {
		return 0, err
	}
	return recordID(record)
}

// companyPath returns the path of the company's collection named by
// segments: companyPath(ctx, "works") is /api/v1/companies/N/works.
func (s *session) companyPath(ctx context.Context, segments ...any) (string, error) {
	company, err := s.companyID(ctx)
	if err != nil {
		return "", err
	}
	return api.Path(append([]any{"companies", company}, segments...)...), nil
}

// proSegment is a PRO as it goes into a path: its ID or its abbreviation,
// both of which the server takes.
func proSegment(selected string) (string, error) {
	selected = strings.TrimSpace(selected)
	if selected == "" {
		return "", usagef("no PRO selected; pass --pro with its ID or abbreviation, such as --pro ASCAP")
	}
	return strings.ToUpper(selected), nil
}

// proID turns a PRO's ID or abbreviation into its ID, for the places
// that take only the number: a query parameter or an upload's field.
func (s *session) proID(ctx context.Context, selected string) (int64, error) {
	selected = strings.TrimSpace(selected)
	if selected == "" {
		return 0, usagef("no PRO selected; pass --pro with its ID or abbreviation, such as --pro ASCAP")
	}
	if id, err := strconv.ParseInt(selected, 10, 64); err == nil {
		return id, nil
	}
	resp, err := s.client.Get(ctx, api.Path("pros", strings.ToUpper(selected)), nil)
	if err != nil {
		if api.IsNotFound(err) {
			return 0, fmt.Errorf("no PRO is abbreviated %q; 'streamingchasers pros list' shows them", selected)
		}
		return 0, err
	}
	record, err := output.ParseRecord(resp.Body)
	if err != nil {
		return 0, err
	}
	return recordID(record)
}

// findByName finds the record in a list that wanted names, ignoring
// case.  A record is named by its name, or by any of keys when there
// are some.
func (s *session) findByName(ctx context.Context, path, key, what, wanted string, keys ...string) (*output.Record, error) {
	if len(keys) == 0 {
		keys = []string{"name"}
	}
	page, err := s.client.ListAll(ctx, path, key, nil, 0, nil)
	if err != nil {
		return nil, err
	}
	records, err := output.ParseRecords(page.Items)
	if err != nil {
		return nil, err
	}
	var matches []*output.Record
	var names []string
	for _, record := range records {
		names = append(names, record.String(keys[0]))
		for _, key := range keys {
			if strings.EqualFold(record.String(key), wanted) {
				matches = append(matches, record)
				break
			}
		}
	}
	switch len(matches) {
	case 1:
		return matches[0], nil
	case 0:
		if len(names) == 0 {
			return nil, fmt.Errorf("no %s is named %q; there are none to choose from", what, wanted)
		}
		return nil, fmt.Errorf("no %s is named %q; the choices are %s", what, wanted, quoteList(names))
	default:
		return nil, fmt.Errorf("more than one %s is named %q; use its ID instead", what, wanted)
	}
}

func quoteList(names []string) string {
	const most = 12
	quoted := make([]string, 0, len(names))
	for i, name := range names {
		if i == most {
			quoted = append(quoted, fmt.Sprintf("and %d more", len(names)-most))
			break
		}
		quoted = append(quoted, strconv.Quote(name))
	}
	return strings.Join(quoted, ", ")
}

func recordID(record *output.Record) (int64, error) {
	id, err := strconv.ParseInt(record.String("id"), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("the server sent a record without a numeric id")
	}
	return id, nil
}
