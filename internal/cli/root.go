package cli

import (
	"fmt"
	"strings"
	"time"

	"github.com/mdchaney/streamingchasers-api/internal/api"
	"github.com/mdchaney/streamingchasers-api/internal/output"
	"github.com/spf13/cobra"
)

const rootLong = `Work with Streaming Chasers from the command line: load a catalog of works,
writers and publishers, upload sales and royalty statements, find what is
owed and build the claims sheets, and register works by CWR.

Sign in first:

  streamingchasers auth login

Then pick the company to work in, so that --company can be left off:

  streamingchasers companies list
  streamingchasers companies use "Frivolous Music"

Environment:

  STREAMINGCHASERS_HOST        the server to talk to (default https://app.streamingchasers.com)
  STREAMINGCHASERS_TOKEN       a token to use instead of the stored credentials
  STREAMINGCHASERS_COMPANY     the company to work in
  STREAMINGCHASERS_CONFIG_DIR  where settings and credentials are kept

Exit codes:

  0 success                  4 not allowed (your role or the token's scopes)
  1 error                    5 not found
  2 the command was misused  6 the server rejected the data as invalid
  3 not signed in            7 conflict (a payment period taken, a file already sent)
                             130 interrupted`

func (a *App) newRootCmd() *cobra.Command {
	a.globals = globals{}

	root := &cobra.Command{
		Use:           "streamingchasers",
		Short:         "The command line for Streaming Chasers",
		Long:          rootLong,
		Version:       a.Version,
		SilenceUsage:  true,
		SilenceErrors: true,
		Args:          subcommandsOnly,
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			if !output.ValidFormat(a.globals.output) {
				return usagef("unknown output format %q; use one of %s", a.globals.output, strings.Join(output.Formats, ", "))
			}
			if a.globals.timeout <= 0 {
				return usagef("--timeout must be positive")
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmd.Help()
		},
	}
	root.SetVersionTemplate("streamingchasers {{.Version}}\n")
	root.SetFlagErrorFunc(func(cmd *cobra.Command, err error) error {
		return &usageError{message: err.Error()}
	})
	root.CompletionOptions.HiddenDefaultCmd = false

	flags := root.PersistentFlags()
	flags.StringVar(&a.globals.host, "host", "", "server to talk to (default: $STREAMINGCHASERS_HOST, the configured host, or https://app.streamingchasers.com)")
	flags.StringVarP(&a.globals.company, "company", "c", "", "company to work in, by ID or name (default: $STREAMINGCHASERS_COMPANY or the configured company)")
	flags.StringVarP(&a.globals.output, "output", "o", output.Table, "output format: "+strings.Join(output.Formats, ", "))
	flags.BoolVar(&a.globals.debug, "debug", false, "log requests and responses to standard error")
	flags.DurationVar(&a.globals.timeout, "timeout", 2*time.Minute, "how long to wait for the server to answer a request")

	root.AddGroup(
		&cobra.Group{ID: "start", Title: "Getting started:"},
		&cobra.Group{ID: "catalog", Title: "Catalog:"},
		&cobra.Group{ID: "sales", Title: "Sales and royalties:"},
		&cobra.Group{ID: "claims", Title: "Claims:"},
		&cobra.Group{ID: "cwr", Title: "CWR registration:"},
		&cobra.Group{ID: "reference", Title: "Reference data:"},
		&cobra.Group{ID: "other", Title: "Other:"},
	)
	root.SetHelpCommandGroupID("other")
	root.SetCompletionCommandGroupID("other")

	add := func(group string, cmds ...*cobra.Command) {
		for _, cmd := range cmds {
			cmd.GroupID = group
			root.AddCommand(cmd)
		}
	}
	add("start", a.newAuthCmd(), a.newCompaniesCmd(), a.newAccountCmd(), a.newConfigCmd(), a.newPingCmd())
	add("catalog",
		a.newWorksCmd(),
		a.newResourceCmd(writersResource()),
		a.newPublishersCmd(),
		a.newResourceCmd(catalogsResource()),
		a.newResourceCmd(subpublishingAgreementsResource()),
		a.newResourceCmd(adminAgreementsResource()),
		a.newWorksUploadsCmd(),
	)
	add("sales",
		a.newSalesCmd(),
		a.newSalesUploadsCmd(),
		a.newRoyaltyStatementsCmd(),
		a.newRoyaltyRecordsCmd(),
		a.newProDataDumpsCmd(),
	)
	add("claims", a.newPeriodsCmd(), a.newUnpaidCmd(), a.newBatchesCmd(), a.newChaseCmd(), a.newRollupsCmd())
	add("cwr", a.newCwrCmd())
	add("reference", append([]*cobra.Command{a.newProsCmd()}, a.newReferenceCmds()...)...)
	add("other", a.newAPICmd(), a.newVersionCmd())

	return root
}

func (a *App) newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Show the version",
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			_, err := fmt.Fprintf(a.Out, "streamingchasers %s\n", a.Version)
			return err
		},
	}
}

func (a *App) newPingCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "ping",
		Short: "Check that the server is up and your credentials work",
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := a.session()
			if err != nil {
				return err
			}
			started := a.now()
			resp, err := s.client.Get(cmd.Context(), api.Path("users", "me"), nil)
			if err != nil {
				return err
			}
			who := ""
			if me, err := output.ParseRecord(resp.Body); err == nil && me.String("email_address") != "" {
				who = fmt.Sprintf(" as %s <%s>", me.String("name"), me.String("email_address"))
			}
			_, err = fmt.Fprintf(a.Out, "%s is up and accepted your credentials%s (%s)\n", s.host, who, a.now().Sub(started).Round(time.Millisecond))
			return err
		},
	}
}
