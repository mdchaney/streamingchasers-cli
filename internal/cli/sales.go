package cli

import (
	"fmt"
	"net/url"

	"github.com/mdchaney/streamingchasers-api/internal/output"
	"github.com/spf13/cobra"
)

// newSalesCmd is the sales command: the usual ones, the paid-sales
// exports and the lookups.
func (a *App) newSalesCmd() *cobra.Command {
	r := salesResource()
	cmd := a.newResourceCmd(r)

	var pro string
	paid := a.newDownloadCmd("paid-csv", "Download the CSV of every paid sale, or of one PRO's",
		`Download the paid-sales sheet: every sale that a royalty statement has
paid, with a column per tracked PRO. With --pro, one PRO's paid sales on
tracked streamers, with a column per streamer.

The sheet goes to standard output unless --save or --save-as says
otherwise.`, "",
		func(s *session, cmd *cobra.Command, _ string) (string, error) {
			if pro == "" {
				return s.companyPath(cmd.Context(), "sales", "paid")
			}
			id, err := s.proID(cmd.Context(), pro)
			if err != nil {
				return "", err
			}
			return s.companyPath(cmd.Context(), "sales", "paid", "pro", id)
		},
		func(string) string { return "paid_sales.csv" })
	paid.Args = noArgs
	paid.Flags().StringVar(&pro, "pro", "", "one PRO's paid sales, by ID or abbreviation")

	lookupProduction := &cobra.Command{
		Use:   "lookup-production IMDB_ID",
		Short: "Find the production an IMDB ID is",
		Args:  exactArgs("IMDB_ID"),
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.lookup(cmd, "lookup_production", url.Values{"imdb_id": {args[0]}}, "no production is known by IMDB ID %q", args[0])
		},
	}
	lookupWork := &cobra.Command{
		Use:   "lookup-work EXTERNAL_ID",
		Short: "Find the title of a work by its external ID",
		Args:  exactArgs("EXTERNAL_ID"),
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.lookup(cmd, "lookup_work", url.Values{"external_id": {args[0]}}, "no work has the external ID %q", args[0])
		},
	}

	cmd.AddCommand(paid, lookupProduction, lookupWork)
	return cmd
}

// lookup runs one of the sales lookups, which answer {found: false}
// rather than 404 when there is nothing.
func (a *App) lookup(cmd *cobra.Command, action string, query url.Values, missing string, arg string) error {
	s, err := a.session()
	if err != nil {
		return err
	}
	path, err := s.companyPath(cmd.Context(), "sales", action)
	if err != nil {
		return err
	}
	resp, err := s.client.Get(cmd.Context(), path, query)
	if err != nil {
		return err
	}
	record, err := output.ParseRecord(resp.Body)
	if err != nil {
		return err
	}
	if record.String("found") != "true" {
		fmt.Fprintf(a.Err, missing+"\n", arg)
		return &errReported{code: ExitNotFound}
	}
	return a.renderRecord(resp.Body, "")
}

func (a *App) newRoyaltyRecordsCmd() *cobra.Command {
	return a.newResourceCmd(royaltyRecordsResource())
}

// uploadColumns are the columns the lists of uploads share.
var statusColumns = []output.Column{
	{Header: "STATUS", Key: "status"},
	{Header: "PROCESSED", Value: timestamp("processed_at")},
	{Header: "CREATED", Value: timestamp("created_at")},
}

// pathWithPro resolves --pro and builds a company path with its ID.
func pathWithPro(s *session, cmd *cobra.Command, pro string, segments ...any) (string, error) {
	id, err := s.proID(cmd.Context(), pro)
	if err != nil {
		return "", err
	}
	return s.companyPath(cmd.Context(), append([]any{"pros", id}, segments...)...)
}

// recordPath is the path of a record given its ID: for downloads.
func recordPath(segments ...any) func(s *session, cmd *cobra.Command, id string) (string, error) {
	return func(s *session, cmd *cobra.Command, id string) (string, error) {
		head := append([]any{}, segments[:1]...)
		return s.companyPath(cmd.Context(), append(append(head, id), segments[1:]...)...)
	}
}
