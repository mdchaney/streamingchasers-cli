package cli

import (
	"fmt"
	"strings"

	"github.com/mdchaney/streamingchasers-api/internal/api"
	"github.com/mdchaney/streamingchasers-api/internal/output"
	"github.com/spf13/cobra"
)

const cwrLong = `Register works with PROs by CWR (Common Works Registration).

A connection is the company's link to one PRO's delivery server. Works
are queued for registration on a connection as tickets, a file is made
from the queued works and sent, and the PRO's acknowledgments come back
as ack files.

  streamingchasers cwr connections list
  streamingchasers cwr tickets candidates --connection 3    works never sent
  streamingchasers cwr tickets queue --connection 3 --reason "New catalog" W-1 W-2
  streamingchasers cwr files candidates --connection 3      what a new file could hold
  streamingchasers cwr files create --connection 3 W-1 W-2
  streamingchasers cwr files send 17 --connection 3
  streamingchasers cwr connections download-acks 3
  streamingchasers cwr acks list --connection 3

Making or changing a connection takes a company admin and, with a browser
sign-in, 'streamingchasers auth login --admin'. The connection's password
for the PRO server is accepted and never shown again.`

func cwrConnectionsResource() *resource {
	return &resource{
		name:       "connections",
		aliases:    []string{"connection"},
		singular:   "company_cwr_connection",
		label:      "CWR connection",
		segment:    "cwr_connections",
		short:      "The company's connections to PRO delivery servers",
		long:       cwrLong,
		scope:      scopeCompany,
		idArg:      "ID",
		adminScope: true,
		columns: []output.Column{
			{Header: "ID", Key: "id"},
			{Header: "DESTINATION", Value: nested("destination", "name"), Max: 30},
			{Header: "PRO", Value: nested("destination", "pro")},
			{Header: "USERNAME", Key: "server_username", Max: 20},
			{Header: "SENDER", Key: "cwr_sender_name", Max: 25},
			{Header: "ACTIVE", Value: yesNo("active")},
			{Header: "QUEUED", Key: "queued_tickets"},
		},
		fields: []field{
			{flag: "destination", key: "cwr_destination_id", kind: kindInt, usage: "ID of the PRO delivery server (required; the web site lists them)"},
			{flag: "username", key: "server_username", usage: "user name on the PRO server (required)"},
			{flag: "password", key: "server_password", kind: kindText, usage: "password on the PRO server (required to create); @FILE keeps it out of your shell history"},
			{flag: "outbound", key: "outbound_directory", usage: "directory on the PRO server to put files in"},
			{flag: "inbound", key: "inbound_directory", usage: "directory on the PRO server acknowledgments come from (required)"},
			{flag: "sender-name", key: "cwr_sender_name", usage: "the sender's name, as CWR has it (required)"},
			{flag: "sender-ipi", key: "cwr_sender_ipi_number", usage: "the sender's IPI number (required)"},
			{flag: "sender-code", key: "cwr_sender_filename_code", usage: "the sender's three-letter file name code"},
			{flag: "min-works", key: "minimum_work_count", kind: kindInt, usage: "fewest works a file may hold"},
			{flag: "serial-offset", key: "filename_serial_offset", kind: kindInt, usage: "where file serial numbers start"},
			{flag: "active", key: "active", kind: kindBool, usage: "the connection is in use"},
		},
		createExample: `  streamingchasers cwr connections create --destination 2 --username frivolous --password @pw.txt \
      --inbound /in --outbound /out --sender-name "FRIVOLOUS MUSIC" --sender-ipi 123456789 --sender-code FRV`,
		updateExample: `  streamingchasers cwr connections update 3 --active=false`,
	}
}

func (a *App) newCwrCmd() *cobra.Command {
	cmd := group("cwr", "Register works with PROs by CWR", cwrLong)
	cmd.AddCommand(a.newCwrConnectionsCmd(), a.newCwrTicketsCmd(), a.newCwrFilesCmd(), a.newCwrAcksCmd())
	return cmd
}

func addConnectionFlag(cmd *cobra.Command, connection *int64) {
	cmd.Flags().Int64Var(connection, "connection", 0, "ID of the CWR connection (required)")
}

// connectionPath is the path under a connection.
func connectionPath(s *session, cmd *cobra.Command, connection int64, segments ...any) (string, error) {
	if connection <= 0 {
		return "", usagef("--connection must name a CWR connection; 'streamingchasers cwr connections list' shows them")
	}
	return s.companyPath(cmd.Context(), append([]any{"cwr_connections", connection}, segments...)...)
}

func (a *App) newCwrConnectionsCmd() *cobra.Command {
	r := cwrConnectionsResource()
	cmd := a.newResourceCmd(r)

	acks := &cobra.Command{
		Use:   "download-acks ID",
		Short: "Have the server fetch new acknowledgments from the PRO",
		Long: `Have the server fetch the acknowledgment files waiting on the PRO server,
in the background. 'streamingchasers cwr acks list' shows what came.`,
		Args: exactArgs("ID"),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := a.session()
			if err != nil {
				return err
			}
			path, err := s.companyPath(cmd.Context(), "cwr_connections", args[0], "download_acks")
			if err != nil {
				return err
			}
			if _, err := s.client.Post(cmd.Context(), path, nil); err != nil {
				return err
			}
			fmt.Fprintf(a.Err, "The acknowledgments for connection %s are being fetched.\n", args[0])
			return nil
		},
	}
	cmd.AddCommand(acks)
	return cmd
}

func (a *App) newCwrTicketsCmd() *cobra.Command {
	cmd := group("tickets", "Queue works for registration on a connection", `Tickets queue works for CWR registration on a connection. A work with an
open ticket goes into the next file made on that connection; the ticket
closes when the file is made.`)

	var connection int64
	candidates := &cobra.Command{
		Use:   "candidates --connection ID",
		Short: "List the works never sent on a connection and not yet queued",
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := a.session()
			if err != nil {
				return err
			}
			path, err := connectionPath(s, cmd, connection, "bulk_tickets", "candidates")
			if err != nil {
				return err
			}
			resp, err := s.client.Get(cmd.Context(), path, nil)
			if err != nil {
				return err
			}
			items, err := listOf(resp.Body, "works")
			if err != nil {
				return err
			}
			columns := []output.Column{
				{Header: "ID", Key: "id"},
				{Header: "TITLE", Key: "title", Max: 60},
				{Header: "HAS PRO CODE", Value: yesNo("has_pro_code")},
			}
			return a.renderItems(items, columns, "works to queue")
		},
	}
	addConnectionFlag(candidates, &connection)

	var queueConnection int64
	var reason string
	queue := &cobra.Command{
		Use:   "queue --connection ID --reason WHY EXTERNAL_ID...",
		Short: "Queue works for registration, which also flags them for CWR",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if strings.TrimSpace(reason) == "" {
				return usagef("--reason is required")
			}
			s, err := a.session()
			if err != nil {
				return err
			}
			path, err := connectionPath(s, cmd, queueConnection, "bulk_tickets")
			if err != nil {
				return err
			}
			resp, err := s.client.Post(cmd.Context(), path, map[string]any{"reason": reason, "work_ids": args})
			if err != nil {
				return err
			}
			record, _ := output.ParseRecord(resp.Body)
			fmt.Fprintf(a.Err, "Queued %s of %d works on connection %d.\n", record.String("created"), len(args), queueConnection)
			return nil
		},
	}
	addConnectionFlag(queue, &queueConnection)
	queue.Flags().StringVar(&reason, "reason", "", "why the works are being registered (required)")

	var addConnection int64
	var addReason string
	add := &cobra.Command{
		Use:   "add EXTERNAL_ID --connection ID",
		Short: "Queue one work for registration",
		Args:  exactArgs("EXTERNAL_ID"),
		RunE: func(cmd *cobra.Command, args []string) error {
			if addConnection <= 0 {
				return usagef("--connection must name a CWR connection")
			}
			s, err := a.session()
			if err != nil {
				return err
			}
			path, err := s.companyPath(cmd.Context(), "works", args[0], "cwr_tickets")
			if err != nil {
				return err
			}
			ticket := map[string]any{"company_cwr_connection_id": addConnection}
			if addReason != "" {
				ticket["reason"] = addReason
			}
			resp, err := s.client.Post(cmd.Context(), path, map[string]any{"cwr_ticket": ticket})
			if err != nil {
				return err
			}
			record, _ := output.ParseRecord(resp.Body)
			fmt.Fprintf(a.Err, "Queued work %s on connection %d as ticket %s.\n", args[0], addConnection, record.String("id"))
			return a.renderRecord(resp.Body, "")
		},
	}
	addConnectionFlag(add, &addConnection)
	add.Flags().StringVar(&addReason, "reason", "", "why the work is being registered")

	var yes bool
	remove := &cobra.Command{
		Use:     "remove EXTERNAL_ID TICKET_ID",
		Aliases: []string{"withdraw", "rm"},
		Short:   "Withdraw an open ticket",
		Args:    exactArgs("EXTERNAL_ID", "TICKET_ID"),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := a.confirmDestructive(yes, "withdraw", fmt.Sprintf("Withdraw ticket %s for work %s?", args[1], args[0]), "Nothing was withdrawn."); err != nil {
				return err
			}
			s, err := a.session()
			if err != nil {
				return err
			}
			path, err := s.companyPath(cmd.Context(), "works", args[0], "cwr_tickets", args[1])
			if err != nil {
				return err
			}
			if _, err := s.client.Delete(cmd.Context(), path); err != nil {
				return err
			}
			fmt.Fprintf(a.Err, "Withdrew ticket %s for work %s.\n", args[1], args[0])
			return nil
		},
	}
	remove.Flags().BoolVarP(&yes, "yes", "y", false, "withdraw without asking")

	cmd.AddCommand(candidates, queue, add, remove)
	return cmd
}

var cwrFileColumns = []output.Column{
	{Header: "ID", Key: "id"},
	{Header: "FILE", Key: "cwr_file_name", Max: 30},
	{Header: "WORKS", Key: "works_count"},
	{Header: "NEW", Key: "new_count"},
	{Header: "ACCEPTED", Key: "accepted_count"},
	{Header: "REJECTED", Key: "rejected_count"},
	{Header: "ERRORS", Key: "error_count"},
	{Header: "ACKED", Value: yesNo("fully_acked")},
	{Header: "SENT", Value: timestamp("sent_at")},
	{Header: "CREATED", Value: timestamp("created_at")},
}

func (a *App) newCwrFilesCmd() *cobra.Command {
	cmd := group("files", "The CWR files made on a connection", `The CWR files made on a connection: make one from queued works, download
it, send it to the PRO, and see what the PRO made of each work.`)

	var connection int64
	opts := &listOptions{}
	list := &cobra.Command{
		Use:   "list --connection ID",
		Short: "List the files made on a connection",
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := opts.validate(cmd); err != nil {
				return err
			}
			s, err := a.session()
			if err != nil {
				return err
			}
			path, err := connectionPath(s, cmd, connection, "cwr_files")
			if err != nil {
				return err
			}
			page, err := a.fetchList(cmd.Context(), s, path, "cwr_files", opts)
			if err != nil {
				return err
			}
			return a.renderList(page, cwrFileColumns, "CWR files", opts)
		},
	}
	addConnectionFlag(list, &connection)
	addListFlags(list, opts)

	member := func(segments ...any) func(s *session, cmd *cobra.Command, id string) (string, error) {
		return func(s *session, cmd *cobra.Command, id string) (string, error) {
			c, _ := cmd.Flags().GetInt64("connection")
			return connectionPath(s, cmd, c, append([]any{"cwr_files", id}, segments...)...)
		}
	}

	get := &cobra.Command{
		Use:     "get ID --connection ID",
		Aliases: []string{"show"},
		Short:   "Show a file, with its works",
		Args:    exactArgs("ID"),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := a.session()
			if err != nil {
				return err
			}
			path, err := member()(s, cmd, args[0])
			if err != nil {
				return err
			}
			resp, err := s.client.Get(cmd.Context(), path, nil)
			if err != nil {
				return err
			}
			return a.renderCwrFile(resp.Body)
		},
	}
	addConnectionFlag(get, new(int64))

	candidates := &cobra.Command{
		Use:   "candidates --connection ID",
		Short: "List the queued works a new file could hold",
		Long: `List the works a new file on the connection could hold: flagged for CWR,
with a controlled publisher, at the destination's PROs, with an open
ticket and nothing unacknowledged. new=false means the work was accepted
before and would go out as a revision.`,
		Args: noArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			c, _ := cmd.Flags().GetInt64("connection")
			s, err := a.session()
			if err != nil {
				return err
			}
			path, err := connectionPath(s, cmd, c, "cwr_files", "candidates")
			if err != nil {
				return err
			}
			resp, err := s.client.Get(cmd.Context(), path, nil)
			if err != nil {
				return err
			}
			items, err := listOf(resp.Body, "candidates")
			if err != nil {
				return err
			}
			columns := []output.Column{
				{Header: "ID", Key: "id"},
				{Header: "TITLE", Key: "title", Max: 50},
				{Header: "NEW", Value: yesNo("new")},
				{Header: "REASON", Key: "reason", Max: 40},
			}
			return a.renderItems(items, columns, "works ready for a file")
		},
	}
	addConnectionFlag(candidates, new(int64))

	create := &cobra.Command{
		Use:   "create --connection ID EXTERNAL_ID...",
		Short: "Make a CWR file from queued works",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, _ := cmd.Flags().GetInt64("connection")
			s, err := a.session()
			if err != nil {
				return err
			}
			path, err := connectionPath(s, cmd, c, "cwr_files")
			if err != nil {
				return err
			}
			resp, err := s.client.Post(cmd.Context(), path, map[string]any{"work_ids": args})
			if err != nil {
				return err
			}
			record, _ := output.ParseRecord(resp.Body)
			fmt.Fprintf(a.Err, "Made CWR file %s, %s, with %s works. Send it with 'streamingchasers cwr files send %s --connection %d'.\n",
				record.String("id"), record.String("cwr_file_name"), record.String("works_count"), record.String("id"), c)
			return a.renderCwrFile(resp.Body)
		},
	}
	addConnectionFlag(create, new(int64))

	send := &cobra.Command{
		Use:   "send ID --connection ID",
		Short: "Send a file to the PRO's delivery server",
		Args:  exactArgs("ID"),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := a.session()
			if err != nil {
				return err
			}
			path, err := member("send_to_pro")(s, cmd, args[0])
			if err != nil {
				return err
			}
			resp, err := s.client.Post(cmd.Context(), path, nil)
			if err != nil {
				return err
			}
			fmt.Fprintf(a.Err, "Sent CWR file %s.\n", args[0])
			return a.renderCwrFile(resp.Body)
		},
	}
	addConnectionFlag(send, new(int64))

	download := a.newDownloadCmd("download ID --connection ID", "Download the file as it was made", "", "ID", member("download"), func(id string) string { return "cwr-file-" + id + ".V21" })
	addConnectionFlag(download, new(int64))

	var yes bool
	remove := &cobra.Command{
		Use:     "delete ID --connection ID",
		Aliases: []string{"rm", "remove"},
		Short:   "Delete a file that was not sent",
		Args:    exactArgs("ID"),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := a.confirmDestructive(yes, "delete", fmt.Sprintf("Delete CWR file %s? This cannot be undone.", args[0]), "Nothing was deleted."); err != nil {
				return err
			}
			s, err := a.session()
			if err != nil {
				return err
			}
			path, err := member()(s, cmd, args[0])
			if err != nil {
				return err
			}
			if _, err := s.client.Delete(cmd.Context(), path); err != nil {
				return err
			}
			fmt.Fprintf(a.Err, "Deleted CWR file %s.\n", args[0])
			return nil
		},
	}
	addConnectionFlag(remove, new(int64))
	remove.Flags().BoolVarP(&yes, "yes", "y", false, "delete without asking")

	cmd.AddCommand(list, get, candidates, create, send, download, remove)
	return cmd
}

// renderCwrFile shows a file and, as a table, the works in it.
func (a *App) renderCwrFile(body []byte) error {
	if a.globals.output != output.Table {
		return a.renderRecord(body, "")
	}
	record, err := output.ParseRecord(body)
	if err != nil {
		return err
	}
	works, _ := record.Value("works").([]any)
	record.Delete("works")
	if err := a.renderRecord(mustJSON(record), ""); err != nil {
		return err
	}
	if len(works) == 0 {
		return nil
	}
	fmt.Fprintln(a.Out)
	items, _ := listOf(mustJSON(wrapList("works", works)), "works")
	return a.renderItems(items, []output.Column{{Header: "WORK", Key: "id"}, {Header: "TITLE", Key: "title", Max: 60}}, "works")
}

func wrapList(key string, list []any) *output.Record {
	record := output.NewRecord()
	record.Set(key, list)
	return record
}

func (a *App) newCwrAcksCmd() *cobra.Command {
	cmd := group("acks", "The acknowledgment files a PRO sent back", `The acknowledgment files a PRO sent back on a connection, each saying
which works it accepted and rejected. 'streamingchasers cwr connections
download-acks' fetches new ones.`)

	var connection int64
	opts := &listOptions{}
	list := &cobra.Command{
		Use:   "list --connection ID",
		Short: "List the acknowledgment files received on a connection",
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := opts.validate(cmd); err != nil {
				return err
			}
			s, err := a.session()
			if err != nil {
				return err
			}
			path, err := connectionPath(s, cmd, connection, "cwr_ack_files")
			if err != nil {
				return err
			}
			page, err := a.fetchList(cmd.Context(), s, path, "cwr_ack_files", opts)
			if err != nil {
				return err
			}
			columns := []output.Column{
				{Header: "ID", Key: "id"},
				{Header: "FILE", Key: "ack_file_name", Max: 30},
				{Header: "PRO", Key: "pro"},
				{Header: "RECORDS", Key: "record_count"},
				{Header: "ACCEPTED", Key: "accepted_count"},
				{Header: "REJECTED", Key: "rejected_count"},
				{Header: "ERRORS", Key: "error_count"},
				{Header: "ISWCS", Key: "iswc_count"},
				{Header: "RECEIVED", Value: timestamp("received_at")},
			}
			return a.renderList(page, columns, "acknowledgment files", opts)
		},
	}
	addConnectionFlag(list, &connection)
	addListFlags(list, opts)

	member := func(segments ...any) func(s *session, cmd *cobra.Command, id string) (string, error) {
		return func(s *session, cmd *cobra.Command, id string) (string, error) {
			c, _ := cmd.Flags().GetInt64("connection")
			return connectionPath(s, cmd, c, append([]any{"cwr_ack_files", id}, segments...)...)
		}
	}
	get := &cobra.Command{
		Use:     "get ID --connection ID",
		Aliases: []string{"show"},
		Short:   "Show an acknowledgment file, with its works",
		Args:    exactArgs("ID"),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := a.session()
			if err != nil {
				return err
			}
			path, err := member()(s, cmd, args[0])
			if err != nil {
				return err
			}
			resp, err := s.client.Get(cmd.Context(), path, nil)
			if err != nil {
				return err
			}
			return a.renderRecord(resp.Body, "")
		},
	}
	addConnectionFlag(get, new(int64))

	download := a.newDownloadCmd("download ID --connection ID", "Download the acknowledgment file as it came", "", "ID", member("download"), func(id string) string { return "cwr-ack-" + id + ".V21" })
	addConnectionFlag(download, new(int64))

	cmd.AddCommand(list, get, download)
	return cmd
}

// Keep api imported for callers that build paths directly.
var _ = api.Path
