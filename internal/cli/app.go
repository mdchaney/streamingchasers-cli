// Package cli implements the streamingchasers command.
package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/mdchaney/streamingchasers-api/internal/api"
	"github.com/mdchaney/streamingchasers-api/internal/config"
	"github.com/mdchaney/streamingchasers-api/internal/oauth"
	"github.com/spf13/cobra"
)

// Exit codes.
const (
	ExitOK          = 0
	ExitError       = 1
	ExitUsage       = 2
	ExitAuth        = 3
	ExitForbidden   = 4
	ExitNotFound    = 5
	ExitInvalid     = 6
	ExitConflict    = 7
	ExitInterrupted = 130
)

// App is the command's connection to the outside world.  Everything the
// command reads or writes goes through it, so tests can stand in for the
// terminal, the environment, the browser and the network.
type App struct {
	In  io.Reader
	Out io.Writer
	Err io.Writer

	// Getenv reads an environment variable.
	Getenv func(string) string
	// Store holds the settings and credentials.  Nil means the default
	// directory for the environment.
	Store *config.Store
	// HTTPClient makes every request.  Nil means a client with the
	// --timeout the user asked for.
	HTTPClient *http.Client
	// OpenBrowser opens a URL in the user's browser.
	OpenBrowser func(url string) error
	// Interactive reports whether a person is at the terminal to answer
	// a question.
	Interactive bool
	// Progress reports whether to draw progress on Err.
	Progress bool
	// ReadSecret prompts for a line without echoing it.
	ReadSecret func(prompt string) (string, error)
	// Version is the version of the build.
	Version string
	// LoginTimeout is how long a sign-in waits for the user; zero means
	// five minutes.
	LoginTimeout time.Duration
	// RetryDelay is the pause before a request is retried.
	RetryDelay time.Duration
	// Now is the clock; nil means time.Now.
	Now func() time.Time
	// Sleep waits, for polling; nil means time.Sleep with the context.
	Sleep func(ctx context.Context, d time.Duration) error

	globals globals
	// versions is what the server said of its API version in this run.
	versions api.Versions
}

// globals are the flags every command accepts.
type globals struct {
	host    string
	company string
	output  string
	debug   bool
	timeout time.Duration
}

// usageError is a mistake in how the command was called.
type usageError struct{ message string }

func (e *usageError) Error() string { return e.message }

func usagef(format string, args ...any) error {
	return &usageError{message: fmt.Sprintf(format, args...)}
}

// errNotSignedIn is returned when a command needs credentials and there
// are none for the host, or none that still work.
type errNotSignedIn struct {
	host string
	// reason says why, when it is not simply that nobody signed in.
	reason string
}

func (e *errNotSignedIn) Error() string {
	if e.reason != "" {
		return e.reason
	}
	return fmt.Sprintf("not signed in to %s", e.host)
}

// errReported is returned by a command that has already explained what
// went wrong and only needs the exit code set.
type errReported struct{ code int }

func (e *errReported) Error() string { return "" }

func (a *App) now() time.Time {
	if a.Now != nil {
		return a.Now()
	}
	return time.Now()
}

func (a *App) sleep(ctx context.Context, d time.Duration) error {
	if a.Sleep != nil {
		return a.Sleep(ctx, d)
	}
	select {
	case <-time.After(d):
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (a *App) getenv(key string) string {
	if a.Getenv == nil {
		return os.Getenv(key)
	}
	return a.Getenv(key)
}

// Run runs the command with args (without the program name) and returns
// the exit code.
func (a *App) Run(ctx context.Context, args []string) int {
	root := a.newRootCmd()
	root.SetArgs(args)
	root.SetIn(a.In)
	root.SetOut(a.Out)
	root.SetErr(a.Err)

	cmd, err := root.ExecuteContextC(ctx)
	if err == nil {
		// A server that has moved on still answers; the user should
		// know that this program is behind it.
		if a.versions.Status() == api.StatusOutdated {
			fmt.Fprintf(a.Err, "\nNote: %s\n", a.versionNote())
		}
		return ExitOK
	}
	return a.report(cmd, err)
}

// versionNote says how the server's API version stands to the one this
// program was built for, or "" when they are the same or unknown.
func (a *App) versionNote() string {
	switch a.versions.Status() {
	case api.StatusOutdated:
		return fmt.Sprintf("the server speaks API %s and this program was built for %s; upgrade streamingchasers.", a.versions.Server(), api.SpecVersion)
	case api.StatusAhead:
		return fmt.Sprintf("this server speaks API %s, older than this program (built for %s). What failed may be something the server has not got yet.", a.versions.Server(), api.SpecVersion)
	}
	return ""
}

// report explains err to the user and returns the exit code for it.
func (a *App) report(cmd *cobra.Command, err error) int {
	var reported *errReported
	if errors.As(err, &reported) {
		return reported.code
	}
	if errors.Is(err, context.Canceled) {
		fmt.Fprintln(a.Err, "Interrupted.")
		return ExitInterrupted
	}

	fmt.Fprintf(a.Err, "Error: %s\n", err)
	// Said with whatever went wrong: a server older than this program
	// answers 404 for what it has not got, and one newer may have
	// changed what this program sends.
	defer func() {
		if note := a.versionNote(); note != "" {
			fmt.Fprintf(a.Err, "\nNote: %s\n", note)
		}
	}()

	var usage *usageError
	var notSignedIn *errNotSignedIn
	var apiErr *api.Error
	var oauthErr *oauth.Error
	switch {
	case errors.As(err, &usage) || isCobraUsageError(err):
		fmt.Fprintf(a.Err, "\nRun '%s --help' for usage.\n", cmd.CommandPath())
		return ExitUsage
	case errors.As(err, &notSignedIn):
		fmt.Fprintf(a.Err, "\nRun '%s' to sign in.\n", loginCommand(notSignedIn.host))
		return ExitAuth
	case errors.As(err, &apiErr):
		switch apiErr.StatusCode {
		case http.StatusUnauthorized:
			fmt.Fprintf(a.Err, "\nThe server did not accept your credentials. Run '%s' to sign in again.\n", loginCommand(a.globals.host))
			return ExitAuth
		case http.StatusForbidden:
			// A token that lacks a scope can be had again with it; a
			// role that falls short cannot be helped from here.
			if strings.Contains(apiErr.Message, "does not carry the "+adminScope) {
				fmt.Fprintf(a.Err, "\nThis program was not given permission to administer companies and your account. Run '%s --admin' to sign in again with it.\n", loginCommand(a.globals.host))
			} else if strings.Contains(apiErr.Message, "does not carry the ") {
				fmt.Fprintf(a.Err, "\nThis program was not given that permission when you signed in. Run '%s' to sign in again with it.\n", loginCommand(a.globals.host))
			} else {
				fmt.Fprintln(a.Err, "\nYour role in the company does not allow this.")
			}
			return ExitForbidden
		case http.StatusNotFound:
			return ExitNotFound
		case http.StatusUnprocessableEntity:
			return ExitInvalid
		case http.StatusConflict:
			return ExitConflict
		case http.StatusTooManyRequests:
			if apiErr.RetryAfter != "" {
				fmt.Fprintf(a.Err, "\nThe server asks you to wait %s seconds before trying again.\n", apiErr.RetryAfter)
			}
			return ExitError
		}
	case errors.As(err, &oauthErr):
		return ExitAuth
	}
	return ExitError
}

// isCobraUsageError recognises the errors cobra itself produces for an
// unknown command or a bad flag, which carry no type of their own.
func isCobraUsageError(err error) bool {
	message := err.Error()
	for _, prefix := range []string{"unknown command", "unknown flag", "unknown shorthand flag", "flag needs an argument", "invalid argument", "required flag", "accepts "} {
		if strings.HasPrefix(message, prefix) {
			return true
		}
	}
	return false
}

func loginCommand(host string) string {
	if host == "" || host == config.DefaultHost {
		return "streamingchasers auth login"
	}
	return "streamingchasers auth login --host " + host
}

// exactArgs requires the named arguments, no more and no fewer.
func exactArgs(names ...string) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		if len(args) < len(names) {
			return usagef("missing %s", strings.Join(names[len(args):], " and "))
		}
		if len(args) > len(names) {
			return usagef("unexpected argument %q", args[len(names)])
		}
		return nil
	}
}

// subcommandsOnly is for a command that only groups others: whatever
// follows it that is not one of them is a mistake.
func subcommandsOnly(cmd *cobra.Command, args []string) error {
	if len(args) == 0 {
		return nil
	}
	message := fmt.Sprintf("unknown command %q for %q", args[0], cmd.CommandPath())
	// Cobra sets the distance only on the path that this function takes
	// the place of.
	if cmd.SuggestionsMinimumDistance <= 0 {
		cmd.SuggestionsMinimumDistance = 2
	}
	if suggestions := cmd.SuggestionsFor(args[0]); len(suggestions) > 0 {
		message += "\n\nDid you mean this?\n  " + strings.Join(suggestions, "\n  ")
	}
	return &usageError{message: message}
}

// noArgs rejects any argument.
func noArgs(cmd *cobra.Command, args []string) error {
	if len(args) > 0 {
		return usagef("unexpected argument %q", args[0])
	}
	return nil
}

// group makes a command that only holds others.
func group(use, short, long string, cmds ...*cobra.Command) *cobra.Command {
	cmd := &cobra.Command{
		Use:   use,
		Short: short,
		Long:  long,
		Args:  subcommandsOnly,
		RunE:  func(cmd *cobra.Command, args []string) error { return cmd.Help() },
	}
	cmd.AddCommand(cmds...)
	return cmd
}
