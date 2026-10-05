package cli

import (
	"fmt"
	"strings"

	"github.com/mdchaney/streamingchasers-cli/internal/api"
	"github.com/mdchaney/streamingchasers-cli/internal/output"
	"github.com/spf13/cobra"
)

func (a *App) newAccountCmd() *cobra.Command {
	cmd := group("account", "Your own account", `See and change your own account: your name, email address and password,
and the API token that scripts can use instead of signing in.

With a browser sign-in, changing anything here takes
'streamingchasers auth login --admin'.`)

	show := &cobra.Command{
		Use:     "show",
		Aliases: []string{"get", "me"},
		Short:   "Show your account and the companies you are in",
		Args:    noArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := a.session()
			if err != nil {
				return err
			}
			resp, err := s.client.Get(cmd.Context(), api.Path("users", "me"), nil)
			if err != nil {
				return err
			}
			// The token is a secret; it is shown by 'account token'.
			if a.globals.output == output.Table {
				record, err := output.ParseRecord(resp.Body)
				if err != nil {
					return err
				}
				record.Delete("auth_token")
				return a.renderRecord(mustJSON(record), "")
			}
			return a.renderRecord(resp.Body, "")
		},
	}

	var name, email string
	update := &cobra.Command{
		Use:   "update",
		Short: "Change your name or email address",
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			user := map[string]any{}
			if cmd.Flags().Changed("name") {
				user["name"] = name
			}
			if cmd.Flags().Changed("email") {
				user["email_address"] = email
			}
			if len(user) == 0 {
				return usagef("nothing to change; pass --name or --email")
			}
			s, err := a.session()
			if err != nil {
				return err
			}
			resp, err := s.client.Patch(cmd.Context(), api.Path("users", "me"), map[string]any{"user": user})
			if err != nil {
				return err
			}
			fmt.Fprintln(a.Err, "Your account was updated.")
			return a.renderRecord(resp.Body, "")
		},
	}
	update.Flags().StringVar(&name, "name", "", "your name")
	update.Flags().StringVar(&email, "email", "", "your email address")

	password := &cobra.Command{
		Use:   "password",
		Short: "Change your password",
		Long: `Change your password. It is asked for twice at the terminal, without
being shown. Away from a terminal, it is read from standard input, one
line, so that it stays out of your shell history.`,
		Args: noArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			var secret string
			if a.Interactive && a.ReadSecret != nil {
				first, err := a.ReadSecret("New password: ")
				if err != nil {
					return err
				}
				second, err := a.ReadSecret("Again: ")
				if err != nil {
					return err
				}
				if first != second {
					return fmt.Errorf("the passwords do not match")
				}
				secret = first
			} else {
				line, err := a.readFile("-")
				if err != nil {
					return err
				}
				secret = strings.TrimRight(string(line), "\r\n")
			}
			if secret == "" {
				return usagef("no password was given")
			}
			s, err := a.session()
			if err != nil {
				return err
			}
			body := map[string]any{"user": map[string]any{"password": secret, "password_confirmation": secret}}
			if _, err := s.client.Patch(cmd.Context(), api.Path("users", "me"), body); err != nil {
				return err
			}
			fmt.Fprintln(a.Err, "Your password was changed.")
			return nil
		},
	}

	token := &cobra.Command{
		Use:   "token",
		Short: "Print your API token",
		Long: `Print your API token, for scripts: it works as a bearer token, unscoped,
until you reset it. Treat the output as a password.

With a browser sign-in, seeing it takes 'streamingchasers auth login
--admin': a read-only sign-in may not read a token that can do anything.`,
		Args: noArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := a.session()
			if err != nil {
				return err
			}
			resp, err := s.client.Get(cmd.Context(), api.Path("users", "me"), nil)
			if err != nil {
				return err
			}
			record, err := output.ParseRecord(resp.Body)
			if err != nil {
				return err
			}
			if record.String("auth_token") == "" {
				return fmt.Errorf("the server shows the API token only to the token itself or to a sign-in with --admin; run '%s --admin'", loginCommand(s.host))
			}
			_, err = fmt.Fprintln(a.Out, record.String("auth_token"))
			return err
		},
	}

	var yes bool
	reset := &cobra.Command{
		Use:   "reset-token",
		Short: "Replace your API token",
		Long: `Replace your API token. The old one stops working at once, everywhere it
is used. If the CLI is signed in with it, the new one takes its place and
the CLI goes on working. The new token is printed.`,
		Args: noArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := a.confirmDestructive(yes, "reset the token", "Replace your API token? Everything using the old one stops working.", "The token was not changed."); err != nil {
				return err
			}
			s, err := a.session()
			if err != nil {
				return err
			}
			resp, err := s.client.Post(cmd.Context(), api.Path("users", "reset_auth_token"), nil)
			if err != nil {
				return err
			}
			record, err := output.ParseRecord(resp.Body)
			if err != nil {
				return err
			}
			token := record.String("auth_token")
			if token == "" {
				return fmt.Errorf("the server did not send the new token")
			}
			if s.auth.static != "" && a.getenv("STREAMINGCHASERS_TOKEN") == "" {
				creds, err := s.store.LoadCredentials()
				if err != nil {
					return err
				}
				creds.Host(s.host).APIToken = token
				if err := s.store.SaveCredentials(creds); err != nil {
					return err
				}
				fmt.Fprintln(a.Err, "Your API token was replaced, and the CLI now uses the new one.")
			} else {
				fmt.Fprintln(a.Err, "Your API token was replaced.")
			}
			_, err = fmt.Fprintln(a.Out, token)
			return err
		},
	}
	reset.Flags().BoolVarP(&yes, "yes", "y", false, "reset without asking")

	cmd.AddCommand(show, update, password, token, reset)
	return cmd
}

// mustJSON encodes a record that came from JSON.
func mustJSON(record *output.Record) []byte {
	data, err := record.MarshalJSON()
	if err != nil {
		return []byte("{}")
	}
	return data
}
