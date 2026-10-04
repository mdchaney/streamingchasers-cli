package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/mdchaney/streamingchasers-api/internal/api"
	"github.com/mdchaney/streamingchasers-api/internal/config"
	"github.com/mdchaney/streamingchasers-api/internal/oauth"
	"github.com/mdchaney/streamingchasers-api/internal/output"
	"github.com/spf13/cobra"
)

// wantedScopes are the scopes the CLI asks for, of those the server
// offers: reading the catalog and changing it.
var wantedScopes = []string{"catalog", "catalog_write"}

// adminScope is asked for only when the user asks for it.  It lets a
// token change a company's settings and ownership, the user's own
// account and password, and the CWR connections that hold PRO server
// credentials: more than most of what this program is used for needs.
const adminScope = "catalog_admin"

// fallbackScopes are asked for when the server does not say what it offers.
var fallbackScopes = []string{"catalog"}

// apiResource is the REST API as an OAuth protected resource.  Tokens
// are issued for it and for nothing else.
func apiResource(host string) string { return host + "/api/v1" }

func (a *App) newAuthCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "auth",
		Short: "Sign in and out",
		Args:  subcommandsOnly,
		RunE:  func(cmd *cobra.Command, args []string) error { return cmd.Help() },
	}
	cmd.AddCommand(a.newLoginCmd(), a.newLogoutCmd(), a.newStatusCmd(), a.newTokenCmd())
	return cmd
}

type loginOptions struct {
	withToken bool
	noBrowser bool
	newClient bool
	admin     bool
	scopes    []string
}

func (a *App) newLoginCmd() *cobra.Command {
	opts := &loginOptions{}
	cmd := &cobra.Command{
		Use:   "login",
		Short: "Sign in to a Streaming Chasers server",
		Long: `Sign in to a Streaming Chasers server.

By default this opens your browser, where you sign in to Streaming Chasers
and approve the CLI. Nothing secret is typed into the terminal, the CLI gets
only the permissions you approve, and you can disconnect it at any time
from your account page.

It asks to read your catalog and to change it. With --admin it also asks
to administer: to change a company's settings and ownership, your own
account and password, and the CWR connections that hold PRO server
credentials. Leave that out unless you mean to do those things.

With --with-token, sign in with the API token from your account page
instead. The token is read from standard input, so that it stays out of
your shell history:

  streamingchasers auth login --with-token < token.txt

On a machine without a browser, use --no-browser: open the address that is
printed in a browser on another machine, and once you have approved the
request paste the address that browser ends up at back into the terminal.`,
		Example: `  streamingchasers auth login
  streamingchasers auth login --admin
  streamingchasers auth login --host test.streamingchasers.com
  streamingchasers auth login --with-token`,
		Args: noArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.login(cmd.Context(), opts)
		},
	}
	cmd.Flags().BoolVar(&opts.withToken, "with-token", false, "sign in with an API token read from standard input")
	cmd.Flags().BoolVar(&opts.noBrowser, "no-browser", false, "print the sign-in address instead of opening a browser")
	cmd.Flags().BoolVar(&opts.admin, "admin", false, "also ask for permission to administer: company settings and ownership, your own account, and CWR connections")
	cmd.Flags().BoolVar(&opts.newClient, "new-client", false, "register the CLI with the server again instead of reusing its registration")
	cmd.Flags().StringSliceVar(&opts.scopes, "scopes", nil, "permissions to ask for (default: everything the server offers of "+strings.Join(wantedScopes, ", ")+")")
	return cmd
}

func (a *App) login(ctx context.Context, opts *loginOptions) error {
	if a.getenv("STREAMINGCHASERS_TOKEN") != "" {
		return usagef("STREAMINGCHASERS_TOKEN is set and takes the place of signing in; unset it first")
	}
	store, err := a.store()
	if err != nil {
		return err
	}
	cfg, err := store.LoadConfig()
	if err != nil {
		return err
	}
	host, err := a.resolveHost(cfg)
	if err != nil {
		return err
	}
	a.globals.host = host
	creds, err := store.LoadCredentials()
	if err != nil {
		return err
	}
	if !strings.HasPrefix(host, "https://") && !config.IsLoopbackURL(host) {
		fmt.Fprintf(a.Err, "Warning: %s is not an https address; your credentials will cross the network unencrypted.\n", host)
	}

	if opts.withToken {
		err = a.loginWithToken(ctx, store, creds, host)
	} else {
		err = a.loginWithOAuth(ctx, store, creds, host, opts)
	}
	if err != nil {
		return err
	}

	// The first server signed in to becomes the default, so that the
	// commands that follow need no --host.
	if cfg.DefaultHost == "" && host != config.DefaultHost {
		cfg.DefaultHost = host
		if err := store.SaveConfig(cfg); err != nil {
			return err
		}
		fmt.Fprintf(a.Err, "%s is now the default host.\n", host)
	}
	return nil
}

func (a *App) loginWithToken(ctx context.Context, store *config.Store, creds *config.Credentials, host string) error {
	var token string
	var err error
	if a.Interactive && a.ReadSecret != nil {
		token, err = a.ReadSecret(fmt.Sprintf("API token (from %s/my/account): ", host))
	} else {
		var data []byte
		data, err = io.ReadAll(io.LimitReader(a.In, 4096))
		token = string(data)
	}
	if err != nil {
		return fmt.Errorf("reading the token failed: %w", err)
	}
	token = strings.TrimSpace(token)
	if token == "" {
		return usagef("no token was given on standard input")
	}
	if strings.ContainsAny(token, " \t\r\n") {
		return usagef("that does not look like a token: it contains white space")
	}

	if err := a.verify(ctx, host, token); err != nil {
		if api.IsUnauthorized(err) {
			return fmt.Errorf("%s did not accept that API token; copy it again from %s/my/account", host, host)
		}
		return err
	}

	_ = a.forget(ctx, creds, host)
	creds.Host(host).APIToken = token
	if err := store.SaveCredentials(creds); err != nil {
		return err
	}
	fmt.Fprintf(a.Err, "Signed in to %s with an API token.\n", host)
	return nil
}

func (a *App) loginWithOAuth(ctx context.Context, store *config.Store, creds *config.Credentials, host string, opts *loginOptions) error {
	hc := a.httpClient()
	metadata, err := oauth.Discover(ctx, hc, host)
	if err != nil {
		return err
	}

	wanted := wantedScopes
	if opts.admin {
		if len(opts.scopes) > 0 {
			return usagef("--admin and --scopes cannot be used together; name %s among the scopes", adminScope)
		}
		if len(metadata.ScopesSupported) > 0 && !containsString(metadata.ScopesSupported, adminScope) {
			return fmt.Errorf("%s does not offer the permission to administer libraries; it may be older than this program", host)
		}
		wanted = append(append([]string{}, wantedScopes...), adminScope)
	}
	scopes := oauth.SelectScopes(metadata.ScopesSupported, wanted, fallbackScopes)
	if len(opts.scopes) > 0 {
		scopes = nil
		for _, scope := range opts.scopes {
			if scope = strings.TrimSpace(scope); scope != "" {
				scopes = append(scopes, scope)
			}
		}
		if len(metadata.ScopesSupported) > 0 {
			for _, scope := range scopes {
				if !containsString(metadata.ScopesSupported, scope) {
					return usagef("%s does not offer the scope %q; it offers %s", host, scope, strings.Join(metadata.ScopesSupported, ", "))
				}
			}
		}
	}
	if len(scopes) == 0 {
		return fmt.Errorf("%s offers none of the permissions the CLI uses (%s)", host, strings.Join(wantedScopes, ", "))
	}

	hostCreds := creds.Host(host)
	// Taken now, while the client that the old sign-in belongs to is
	// still on record.
	previous := grantOf(hostCreds)
	preferredPort := 0
	if hostCreds.Client != nil && !opts.newClient {
		preferredPort = oauth.PortOf(hostCreds.Client.RedirectURI)
	}
	listener, err := oauth.Listen(preferredPort)
	if err != nil {
		return err
	}
	defer listener.Close()

	// The registration is kept from one sign-in to the next so that the
	// account page lists one CLI, not one per sign-in.  It is replaced
	// when it cannot grant what is being asked for.
	if opts.newClient || hostCreds.Client == nil || !coversScopes(hostCreds.Client.Scope, scopes) {
		registered, err := oauth.Register(ctx, hc, metadata, clientName(), oauth.RedirectURI(listener), scopes)
		if err != nil {
			return err
		}
		hostCreds.Client = &config.OAuthClient{
			ClientID:     registered.ClientID,
			RedirectURI:  oauth.RedirectURI(listener),
			Scope:        registered.Scope,
			RegisteredAt: a.now().UTC(),
		}
		if hostCreds.Client.Scope == "" {
			hostCreds.Client.Scope = strings.Join(scopes, " ")
		}
		if err := store.SaveCredentials(creds); err != nil {
			return err
		}
	}

	flow := &oauth.Flow{
		HTTPClient: hc,
		Metadata:   metadata,
		ClientID:   hostCreds.Client.ClientID,
		Scopes:     scopes,
		Resource:   apiResource(host),
		Listener:   listener,
		Out:        a.Err,
		Timeout:    a.LoginTimeout,
	}
	if !opts.noBrowser {
		flow.OpenBrowser = a.OpenBrowser
	}
	if a.Interactive {
		flow.Paste = a.In
	}

	issued := a.now()
	token, err := flow.Run(ctx)
	if err != nil {
		var oauthErr *oauth.Error
		if errors.As(err, &oauthErr) && oauthErr.Code == "invalid_client" {
			return fmt.Errorf("%w\n\nThe server no longer knows this CLI; run 'streamingchasers auth login --new-client'", err)
		}
		return err
	}

	if err := a.verify(ctx, host, token.AccessToken); err != nil {
		// Nothing can use the token, so it is not left behind.
		_ = oauth.Revoke(ctx, hc, metadata.RevocationEndpoint, hostCreds.Client.ClientID, token.AccessToken)
		if api.IsUnauthorized(err) {
			return fmt.Errorf("you signed in, but the REST API at %s did not accept the token; sign in with your API token instead:\n\n  %s --with-token", host, loginCommand(host))
		}
		return err
	}

	granted := token.Scope
	if granted == "" {
		granted = strings.Join(scopes, " ")
	}
	// The sign-in this one replaces is ended rather than left to expire.
	_ = a.revoke(ctx, previous)
	hostCreds.APIToken = ""
	hostCreds.OAuth = &config.OAuthToken{
		AccessToken:        token.AccessToken,
		RefreshToken:       token.RefreshToken,
		Scope:              granted,
		Resource:           apiResource(host),
		ExpiresAt:          token.ExpiresAt(issued).UTC(),
		TokenEndpoint:      metadata.TokenEndpoint,
		RevocationEndpoint: metadata.RevocationEndpoint,
	}
	if err := store.SaveCredentials(creds); err != nil {
		return err
	}

	fmt.Fprintf(a.Err, "Signed in to %s. The CLI may: %s.\n", host, describeScopes(granted))
	if !containsString(strings.Fields(granted), "catalog_write") {
		fmt.Fprintln(a.Err, "It was not given permission to change the catalog, so commands that create, update or delete will be refused.")
	}
	return nil
}

// verify checks that the server accepts token.
func (a *App) verify(ctx context.Context, host, token string) error {
	client := a.newClient(host, api.StaticToken(token))
	_, err := client.Get(ctx, api.Path("users", "me"), nil)
	return err
}

// grant is an OAuth sign-in as the server knows it: enough to revoke it.
type grant struct {
	endpoint string
	clientID string
	token    string
}

// grantOf returns the OAuth sign-in stored for a host, or nil.
func grantOf(hostCreds *config.HostCredentials) *grant {
	if hostCreds == nil || hostCreds.OAuth == nil || hostCreds.Client == nil || hostCreds.OAuth.AccessToken == "" {
		return nil
	}
	// An access token and its refresh token are one grant to the
	// server, so revoking either ends both.
	token := hostCreds.OAuth.RefreshToken
	if token == "" {
		token = hostCreds.OAuth.AccessToken
	}
	return &grant{endpoint: hostCreds.OAuth.RevocationEndpoint, clientID: hostCreds.Client.ClientID, token: token}
}

// revoke ends a sign-in on the server.
func (a *App) revoke(ctx context.Context, g *grant) error {
	if g == nil {
		return nil
	}
	return oauth.Revoke(ctx, a.httpClient(), g.endpoint, g.clientID, g.token)
}

// forget removes the stored credentials for host, revoking an OAuth
// sign-in with the server first.  The client registration stays.
func (a *App) forget(ctx context.Context, creds *config.Credentials, host string) error {
	hostCreds := creds.Lookup(host)
	if hostCreds == nil {
		return nil
	}
	err := a.revoke(ctx, grantOf(hostCreds))
	hostCreds.OAuth = nil
	hostCreds.APIToken = ""
	if hostCreds.Client == nil {
		delete(creds.Hosts, host)
	}
	return err
}

func clientName() string {
	name, err := os.Hostname()
	if err != nil || name == "" {
		return "Streaming Chasers CLI"
	}
	return fmt.Sprintf("Streaming Chasers CLI (%s)", strings.TrimSuffix(name, ".local"))
}

func coversScopes(granted string, wanted []string) bool {
	have := strings.Fields(granted)
	for _, scope := range wanted {
		if !containsString(have, scope) {
			return false
		}
	}
	return true
}

func containsString(list []string, s string) bool {
	for _, item := range list {
		if item == s {
			return true
		}
	}
	return false
}

var scopeDescriptions = map[string]string{
	"catalog":       "read your catalog",
	"catalog_write": "change your catalog",
	"catalog_admin": "administer companies, CWR connections and your account",
}

func describeScopes(scopes string) string {
	var parts []string
	for _, scope := range strings.Fields(scopes) {
		if description, ok := scopeDescriptions[scope]; ok {
			parts = append(parts, description)
		} else {
			parts = append(parts, scope)
		}
	}
	switch len(parts) {
	case 0:
		return "nothing"
	case 1:
		return parts[0]
	}
	return strings.Join(parts[:len(parts)-1], ", ") + " and " + parts[len(parts)-1]
}

func (a *App) newLogoutCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "logout",
		Short: "Sign out of a Streaming Chasers server",
		Long: `Sign out of a Streaming Chasers server.

A browser sign-in is also disconnected on the server, so the tokens the
CLI held stop working. An API token is only forgotten: it keeps working
until you reset it on your account page.`,
		Args: noArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			store, err := a.store()
			if err != nil {
				return err
			}
			cfg, err := store.LoadConfig()
			if err != nil {
				return err
			}
			host, err := a.resolveHost(cfg)
			if err != nil {
				return err
			}
			creds, err := store.LoadCredentials()
			if err != nil {
				return err
			}
			hostCreds := creds.Lookup(host)
			if hostCreds == nil || (hostCreds.OAuth == nil && hostCreds.APIToken == "") {
				fmt.Fprintf(a.Err, "Not signed in to %s.\n", host)
				return nil
			}

			wasOAuth := hostCreds.OAuth != nil
			revokeErr := a.forget(cmd.Context(), creds, host)
			if err := store.SaveCredentials(creds); err != nil {
				return err
			}
			switch {
			case revokeErr != nil:
				fmt.Fprintf(a.Err, "Signed out of %s, but the server could not be told (%s). Disconnect the CLI at %s/my/account to be sure.\n", host, revokeErr, host)
			case wasOAuth:
				fmt.Fprintf(a.Err, "Signed out of %s and disconnected the CLI from your account.\n", host)
			default:
				fmt.Fprintf(a.Err, "Signed out of %s. The API token itself still works; reset it at %s/my/account to retire it.\n", host, host)
			}
			if a.getenv("STREAMINGCHASERS_TOKEN") != "" {
				fmt.Fprintln(a.Err, "STREAMINGCHASERS_TOKEN is still set and will still be used.")
			}
			return nil
		},
	}
}

func (a *App) newStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show who is signed in and whether the server accepts it",
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := a.session()
			if err != nil {
				return err
			}
			fmt.Fprintln(a.Out, s.host)

			// A token that has run out is renewed before it is
			// described, so that what is said of it is so.  If it cannot
			// be, the request below says why.
			_, _ = s.auth.Token(cmd.Context())

			switch {
			case a.getenv("STREAMINGCHASERS_TOKEN") != "":
				fmt.Fprintln(a.Out, "  Signed in with:   the token in STREAMINGCHASERS_TOKEN")
			case s.auth.oauth != nil:
				fmt.Fprintf(a.Out, "  Signed in with:   your browser (OAuth)\n")
				fmt.Fprintf(a.Out, "  The CLI may:      %s\n", describeScopes(s.auth.oauth.Scope))
				fmt.Fprintf(a.Out, "  Token:            %s\n", a.describeExpiry(s.auth.oauth))
			case s.auth.static != "":
				fmt.Fprintln(a.Out, "  Signed in with:   an API token")
			default:
				fmt.Fprintln(a.Out, "  Not signed in")
				return &errNotSignedIn{host: s.host}
			}
			if host, ok := s.cfg.Hosts[s.host]; ok && host.DefaultCompany != 0 {
				fmt.Fprintf(a.Out, "  Default company:  %d\n", host.DefaultCompany)
			}

			resp, err := s.client.Get(cmd.Context(), api.Path("users", "me"), nil)
			if err != nil {
				if api.IsUnauthorized(err) {
					fmt.Fprintln(a.Out, "  Server:           up, but it did not accept these credentials")
				} else {
					fmt.Fprintln(a.Out, "  Server:           could not be reached")
				}
				return err
			}
			if me, err := output.ParseRecord(resp.Body); err == nil && me.String("email_address") != "" {
				fmt.Fprintf(a.Out, "  Signed in as:     %s <%s>\n", me.String("name"), me.String("email_address"))
			}
			fmt.Fprintln(a.Out, "  Server:           up, and it accepts these credentials")
			return nil
		},
	}
}

func (a *App) describeExpiry(token *config.OAuthToken) string {
	renewal := "sign in again when it does"
	if token.RefreshToken != "" {
		renewal = "renewed automatically"
	}
	if token.ExpiresAt.IsZero() {
		return "does not expire"
	}
	left := token.ExpiresAt.Sub(a.now()).Round(time.Minute)
	if left <= 0 {
		return "expired; " + renewal
	}
	return fmt.Sprintf("expires in %s; %s", left.String(), renewal)
}

func (a *App) newTokenCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "token",
		Short: "Print the token the CLI sends, for use with other tools",
		Long: `Print the token the CLI sends, renewing it first if it is about to expire.

  curl -H "Authorization: Bearer $(streamingchasers auth token)" https://app.streamingchasers.com/api/v1/users/me

A token from a browser sign-in lasts an hour. Treat the output as a password.`,
		Args: noArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := a.session()
			if err != nil {
				return err
			}
			token, err := s.auth.Token(cmd.Context())
			if err != nil {
				return err
			}
			_, err = fmt.Fprintln(a.Out, token)
			return err
		},
	}
}

// confirm asks a yes or no question.  It is only called when a person
// is at the terminal.
func (a *App) confirm(question string) (bool, error) {
	fmt.Fprintf(a.Err, "%s [y/N] ", question)
	line, err := bufio.NewReader(a.In).ReadString('\n')
	if err != nil && line == "" {
		return false, nil
	}
	answer := strings.ToLower(strings.TrimSpace(line))
	return answer == "y" || answer == "yes", nil
}
