package cli

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/mdchaney/streamingchasers-cli/internal/api"
	"github.com/mdchaney/streamingchasers-cli/internal/output"
	"github.com/spf13/cobra"
)

// upload describes a kind of file the server takes and processes in the
// background: works uploads, sales uploads, royalty statements and PRO
// data dumps.
type upload struct {
	name, singular, label, segment, short, long string
	columns                                     []output.Column
	// fields are the flags of create, sent as form fields under the
	// singular: works_upload[notes].
	fields []field
	// proField, if set, is the field --pro fills, by ID or abbreviation.
	proField string
	// sourceFlag, if set, is the field --source fills with a royalty
	// source, by ID or name.
	sourceField string
	noDelete    bool
	// downloads are the member files: subcommand name to path segment.
	downloads []download
	example   string
}

type download struct {
	use, short, segment, fallback string
}

func worksUploads() *upload {
	return &upload{
		name: "works-uploads", singular: "works_upload", label: "works upload", segment: "works_uploads",
		short: "Load a catalog of works from a CSV",
		long: `Load a catalog of works from a CSV, which the server processes in the
background. 'streamingchasers works-file-formats list' shows the formats
a file may be in; a format that needs a PRO takes --pro.

Two flags change what a load does. --match-by-title matches rows to the
works already there, by title, instead of creating works; --codes-only
adds registration codes to the works it matches and changes nothing else.
Together they are how a PRO's codes are brought in for a catalog that is
already loaded. Such a load writes a match report, which 'report'
downloads.

The status is pending until the server is done, then completed or
failed. --wait keeps looking until it is done.`,
		columns: append([]output.Column{
			{Header: "ID", Key: "id"},
			{Header: "FORMAT", Value: nested("works_file_format", "name"), Max: 25},
			{Header: "PRO", Value: nested("pro", "abbreviation")},
			{Header: "ROWS", Key: "total_rows"},
			{Header: "DONE", Key: "processed_rows"},
			{Header: "BY TITLE", Value: yesNo("match_by_title")},
			{Header: "CODES ONLY", Value: yesNo("codes_only")},
		}, statusColumns...),
		fields: []field{
			{flag: "format", key: "works_file_format_id", kind: kindInt, usage: "ID of the file's format (required; see 'streamingchasers works-file-formats list')"},
			{flag: "notes", key: "notes", kind: kindText, usage: "notes"},
			{flag: "match-by-title", key: "match_by_title", kind: kindBool, usage: "match rows to existing works by title instead of creating works"},
			{flag: "codes-only", key: "codes_only", kind: kindBool, usage: "only add registration codes to the works matched"},
		},
		proField:  "pro_id",
		downloads: []download{{"download", "Download the CSV that was uploaded", "csv_file", "works.csv"}, {"report", "Download the match report of a codes-only or match-by-title load", "report_file", "report.csv"}},
		example: `  streamingchasers works-uploads create --file catalog.csv --format 1 --wait
  streamingchasers works-uploads create --file sacem-codes.csv --format 4 --pro SACEM --match-by-title --codes-only --wait
  streamingchasers works-uploads report 12 --save`,
	}
}

func salesUploads() *upload {
	return &upload{
		name: "sales-uploads", singular: "sales_upload", label: "sales upload", segment: "sales_uploads",
		short: "Load sales from a CSV",
		long: `Load sales, the placements of works in productions, from a CSV, which the
server processes in the background. 'streamingchasers sales-file-formats
list' shows the formats a file may be in; without --format the generic
format is assumed.

The status is pending until the server is done, then completed or
failed. --wait keeps looking until it is done.`,
		columns: append([]output.Column{
			{Header: "ID", Key: "id"},
			{Header: "FILE", Key: "filename", Max: 30},
			{Header: "SALES", Key: "sales_count"},
			{Header: "NOTES", Key: "notes", Max: 30},
		}, statusColumns...),
		fields: []field{
			{flag: "format", key: "sales_file_format_id", kind: kindInt, usage: "ID of the file's format (see 'streamingchasers sales-file-formats list')"},
			{flag: "notes", key: "notes", kind: kindText, usage: "notes"},
		},
		downloads: []download{{"download", "Download the CSV that was uploaded", "csv_file", "sales.csv"}},
		example: `  streamingchasers sales-uploads create --file placements.csv --wait
  streamingchasers sales-uploads sales 7`,
	}
}

func royaltyStatements() *upload {
	return &upload{
		name: "royalty-statements", singular: "royalty_statement", label: "royalty statement", segment: "royalty_statements",
		short: "Load the royalty statements PROs send",
		long: `Load a royalty statement from a PRO, which the server processes in the
background, matching each line to a work and a production.

Name the source with --source, by ID or by name: 'streamingchasers
royalty-sources list' shows them, each with the file formats it comes in,
for --format.

The status is pending until the server is done, then completed or
failed. --wait keeps looking until it is done. A statement that failed
can be run again with 'reprocess'. 'streamers' shows the streamer and
channel names a statement reported and which were recognised, and
'records' its lines.`,
		columns: append([]output.Column{
			{Header: "ID", Key: "id"},
			{Header: "SOURCE", Key: "source_name", Max: 25},
			{Header: "FORMAT", Key: "format_name", Max: 20},
			{Header: "FILE", Key: "filename", Max: 30},
			{Header: "FROM", Key: "starts_at"},
			{Header: "TO", Key: "ends_at"},
			{Header: "LINES", Key: "records_count"},
		}, statusColumns...),
		fields: []field{
			{flag: "format", key: "royalty_file_format_id", kind: kindInt, usage: "ID of the file's format, one of the source's"},
			{flag: "notes", key: "notes", kind: kindText, usage: "notes"},
		},
		sourceField: "royalty_source_id",
		noDelete:    true,
		downloads:   []download{{"download", "Download the statement as it was uploaded", "csv_file", "statement.csv"}},
		example: `  streamingchasers royalty-statements create --file ascap-2026q1.csv --source ASCAP --wait
  streamingchasers royalty-statements streamers 31
  streamingchasers royalty-statements reprocess 31 --wait`,
	}
}

func proDataDumps() *upload {
	return &upload{
		name: "pro-data-dumps", singular: "pro_data_dump", label: "PRO data dump", segment: "pro_data_dumps",
		short: "Load a PRO's data dump of work codes",
		long: `Load a PRO's data dump: its export of the works it knows and their codes,
from which the server adds registration codes to the company's works.
The server processes it in the background.

The status is pending until the server is done, then completed or
failed. --wait keeps looking until it is done.`,
		columns: append([]output.Column{
			{Header: "ID", Key: "id"},
			{Header: "PRO", Key: "pro"},
			{Header: "RECORDS", Key: "total_records"},
			{Header: "NEW CODES", Key: "new_codes_count"},
			{Header: "NOTES", Key: "notes", Max: 30},
		}, statusColumns...),
		fields: []field{
			{flag: "notes", key: "notes", kind: kindText, usage: "notes"},
		},
		proField:  "pro_id",
		downloads: []download{{"download", "Download the file that was uploaded", "csv_file", "dump.csv"}},
		example:   `  streamingchasers pro-data-dumps create --file bmi-works.csv --pro BMI --wait`,
	}
}

func (a *App) newWorksUploadsCmd() *cobra.Command      { return a.newUploadCmd(worksUploads()) }
func (a *App) newSalesUploadsCmd() *cobra.Command      { return a.newUploadCmd(salesUploads()) }
func (a *App) newProDataDumpsCmd() *cobra.Command      { return a.newUploadCmd(proDataDumps()) }
func (a *App) newRoyaltyStatementsCmd() *cobra.Command { return a.newUploadCmd(royaltyStatements()) }

// asResource is the upload as a resource, for the generic list, get and
// delete.
func (u *upload) asResource() *resource {
	return &resource{
		name: u.name, singular: u.singular, label: u.label, segment: u.segment, scope: scopeCompany,
		idArg: "ID", columns: u.columns, readOnly: true,
	}
}

func (a *App) newUploadCmd(u *upload) *cobra.Command {
	r := u.asResource()
	cmd := &cobra.Command{
		Use:     u.name,
		Short:   u.short,
		Long:    u.long,
		Example: u.example,
		Args:    subcommandsOnly,
		RunE:    func(cmd *cobra.Command, args []string) error { return cmd.Help() },
	}
	cmd.AddCommand(a.newListCmd(r), a.newGetCmd(r), a.newUploadCreateCmd(u, r))
	if !u.noDelete {
		cmd.AddCommand(a.newDeleteCmd(r))
	}
	for _, d := range u.downloads {
		d := d
		cmd.AddCommand(a.newDownloadCmd(d.use+" ID", d.short, "", "ID", recordPath(u.segment, d.segment), func(id string) string {
			return strings.TrimSuffix(d.fallback, ".csv") + "-" + id + ".csv"
		}))
	}
	switch u.name {
	case "sales-uploads":
		cmd.AddCommand(a.newUploadSubListCmd("sales ID", "List the sales an upload created", u.segment, "sales", "sales", salesResource().columns))
	case "royalty-statements":
		cmd.AddCommand(a.newReprocessCmd(u, r), a.newStreamersCmd(u),
			a.newUploadSubListCmd("records ID", "List the lines of a statement", u.segment, "raw_royalty_records", "lines", royaltyRecordColumns(), royaltyRecordFilters()...))
	}
	return cmd
}

// uploadCreateOptions are the flags of create.
type uploadCreateOptions struct {
	file   string
	pro    string
	source string
	key    string
	wait   bool
	every  time.Duration
}

func (a *App) newUploadCreateCmd(u *upload, r *resource) *cobra.Command {
	opts := &uploadCreateOptions{}
	cmd := &cobra.Command{
		Use:     "create --file FILE",
		Aliases: []string{"upload", "add"},
		Short:   "Upload a file for the server to process",
		Long: fmt.Sprintf(`Upload a file, which the server processes in the background.

The new %s is printed, with its status. With --wait, the command keeps
looking until the server is done, prints the result, and exits with 1 if
the processing failed.

Every upload carries an Idempotency-Key, made up for the occasion unless
--idempotency-key gives one, so that a request repeated after a network
failure cannot load the file twice: the server answers a repeat with the
%s the first request made. Pass the same key to repeat a run safely.`, u.label, u.label),
		Example: u.example,
		Args:    noArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if opts.every < time.Second {
				return usagef("--interval must be a second or more")
			}
			data, filename, err := readFileArg(opts.file)
			if err != nil {
				return err
			}
			fields := map[string]string{}
			flags := cmd.Flags()
			for _, f := range u.fields {
				if !flags.Changed(f.flag) {
					continue
				}
				var value string
				switch f.kind {
				case kindInt:
					n, _ := flags.GetInt64(f.flag)
					value = strconv.FormatInt(n, 10)
				case kindBool:
					b, _ := flags.GetBool(f.flag)
					value = strconv.FormatBool(b)
				default:
					value, _ = flags.GetString(f.flag)
					if f.kind == kindText && strings.HasPrefix(value, "@") {
						text, err := a.readSource(value)
						if err != nil {
							return usagef("--%s: %s", f.flag, err)
						}
						value = string(text)
					}
				}
				fields[u.singular+"["+f.key+"]"] = value
			}

			s, err := a.session()
			if err != nil {
				return err
			}
			if u.proField != "" && opts.pro != "" {
				id, err := s.proID(cmd.Context(), opts.pro)
				if err != nil {
					return err
				}
				fields[u.singular+"["+u.proField+"]"] = strconv.FormatInt(id, 10)
			}
			if u.sourceField != "" {
				if opts.source == "" {
					return usagef("--source is required; 'streamingchasers royalty-sources list' shows them")
				}
				id, err := s.royaltySourceID(cmd.Context(), opts.source)
				if err != nil {
					return err
				}
				fields[u.singular+"["+u.sourceField+"]"] = strconv.FormatInt(id, 10)
			}
			collection, err := a.collection(cmd.Context(), s, r)
			if err != nil {
				return err
			}
			key := opts.key
			if key == "" {
				key = newIdempotencyKey()
			}
			form := &api.Form{Fields: fields, FileField: u.singular + "[csv_file]", Filename: filename, File: data}
			headers := map[string][]string{"Idempotency-Key": {key}}
			resp, err := s.client.Do(cmd.Context(), api.Request{Method: "POST", Path: collection, Form: form, Headers: headers})
			if err != nil {
				return err
			}
			record, err := output.ParseRecord(resp.Body)
			if err != nil {
				return err
			}
			id := record.String("id")
			if resp.Replayed() {
				fmt.Fprintf(a.Err, "The server had this upload already, as %s %s; nothing new was loaded.\n", u.label, id)
			} else {
				fmt.Fprintf(a.Err, "Uploaded %s as %s %s. The server is processing it.\n", filename, u.label, id)
			}
			if !opts.wait {
				return a.renderRecord(resp.Body, "")
			}
			return a.waitForUpload(cmd.Context(), s, u, collection+"/"+escapeSegment(id), opts.every)
		},
	}
	flags := cmd.Flags()
	flags.StringVarP(&opts.file, "file", "f", "", "the file to upload (required)")
	for _, f := range u.fields {
		switch f.kind {
		case kindInt:
			flags.Int64(f.flag, 0, f.usage)
		case kindBool:
			flags.Bool(f.flag, false, f.usage)
		case kindText:
			flags.String(f.flag, "", f.usage+"; @FILE reads a file")
		default:
			flags.String(f.flag, "", f.usage)
		}
	}
	if u.proField != "" {
		usage := "the PRO, by ID or abbreviation"
		if u.name == "pro-data-dumps" {
			usage += " (required)"
		}
		flags.StringVar(&opts.pro, "pro", "", usage)
	}
	if u.sourceField != "" {
		flags.StringVar(&opts.source, "source", "", "the royalty source, by ID or name (required)")
	}
	flags.StringVar(&opts.key, "idempotency-key", "", "the key that makes a repeat of this request safe (default: a new one)")
	flags.BoolVar(&opts.wait, "wait", false, "keep looking until the server has processed the file")
	flags.DurationVar(&opts.every, "interval", 5*time.Second, "how long to leave between looks, with --wait")
	flags.SortFlags = false
	return cmd
}

func newIdempotencyKey() string {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return strconv.FormatInt(time.Now().UnixNano(), 36)
	}
	return hex.EncodeToString(buf)
}

// waitForUpload polls the record at path until its status is no longer
// pending, prints it, and fails if the processing did.
func (a *App) waitForUpload(ctx context.Context, s *session, u *upload, path string, every time.Duration) error {
	started := a.now()
	for {
		resp, err := s.client.Get(ctx, path, nil)
		if err != nil {
			return err
		}
		record, err := output.ParseRecord(resp.Body)
		if err != nil {
			return err
		}
		status := record.String("status")
		if status != "pending" {
			if a.globals.output == output.Table {
				fmt.Fprintf(a.Err, "Processing %s after %s.\n", status, a.now().Sub(started).Round(time.Second))
			}
			if err := a.renderRecord(resp.Body, ""); err != nil {
				return err
			}
			if status == "failed" {
				return &errReported{code: ExitError}
			}
			return nil
		}
		if a.Progress {
			progress := ""
			if total := record.String("total_rows"); total != "" && total != "0" {
				progress = fmt.Sprintf(", %s of %s rows", record.String("processed_rows"), total)
			}
			fmt.Fprintf(a.Err, "\rStill processing%s (%s)...", progress, a.now().Sub(started).Round(time.Second))
		}
		if err := a.sleep(ctx, every); err != nil {
			return err
		}
	}
}

// royaltySourceID turns a royalty source's ID or name into its ID.
func (s *session) royaltySourceID(ctx context.Context, selected string) (int64, error) {
	if id, err := strconv.ParseInt(selected, 10, 64); err == nil {
		return id, nil
	}
	record, err := s.findByName(ctx, api.Path("royalty_sources"), "royalty_sources", "royalty source", selected, "name", "pro")
	if err != nil {
		return 0, err
	}
	return recordID(record)
}

func (a *App) newReprocessCmd(u *upload, r *resource) *cobra.Command {
	var wait bool
	var every time.Duration
	cmd := &cobra.Command{
		Use:   "reprocess ID",
		Short: "Run a statement's import again, after a failure",
		Long: `Clear what came of a statement's import and run it again. The statement
goes back to pending; --wait keeps looking until it is done.`,
		Args: exactArgs("ID"),
		RunE: func(cmd *cobra.Command, args []string) error {
			if every < time.Second {
				return usagef("--interval must be a second or more")
			}
			s, err := a.session()
			if err != nil {
				return err
			}
			path, err := s.companyPath(cmd.Context(), u.segment, args[0])
			if err != nil {
				return err
			}
			resp, err := s.client.Post(cmd.Context(), path+"/reprocess", nil)
			if err != nil {
				return err
			}
			fmt.Fprintf(a.Err, "%s %s is being processed again.\n", strings.ToUpper(u.label[:1])+u.label[1:], args[0])
			if !wait {
				return a.renderRecord(resp.Body, "")
			}
			return a.waitForUpload(cmd.Context(), s, u, path, every)
		},
	}
	cmd.Flags().BoolVar(&wait, "wait", false, "keep looking until the server has processed the statement")
	cmd.Flags().DurationVar(&every, "interval", 5*time.Second, "how long to leave between looks, with --wait")
	return cmd
}

func (a *App) newStreamersCmd(u *upload) *cobra.Command {
	return &cobra.Command{
		Use:   "streamers ID",
		Short: "Show the streamer and channel names a statement reported, and which were recognised",
		Long: `Show every distinct streamer and channel name a statement reported, with
the streamer each was matched to, and how many lines and how much money
ride on each. Names that were not recognised come first.`,
		Args: exactArgs("ID"),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := a.session()
			if err != nil {
				return err
			}
			path, err := s.companyPath(cmd.Context(), u.segment, args[0], "streamers")
			if err != nil {
				return err
			}
			resp, err := s.client.Get(cmd.Context(), path, nil)
			if err != nil {
				return err
			}
			if a.globals.output == output.JSON {
				return output.WriteJSON(a.Out, resp.Body)
			}
			page, err := api.ParsePage(resp.Body, "streamers")
			if err != nil {
				return err
			}
			if a.globals.output == output.Table {
				summary, _ := output.ParseRecord(resp.Body)
				fmt.Fprintf(a.Err, "%s of %s lines matched a streamer; %s of %s in money.\n",
					summary.String("matched_rows"), summary.String("total_rows"), summary.String("matched_amount"), summary.String("total_amount"))
			}
			columns := []output.Column{
				{Header: "STREAMER", Key: "streamer_name", Max: 30},
				{Header: "CHANNEL", Key: "channel_name", Max: 30},
				{Header: "MATCHED", Value: nested("matched_streamer", "name"), Max: 30},
				{Header: "LINES", Key: "rows"},
				{Header: "AMOUNT", Key: "amount"},
				{Header: "CUR", Key: "currency"},
				{Header: "USES", Key: "uses"},
			}
			return a.renderItems(page.Items, columns, "streamers")
		},
	}
}

// newUploadSubListCmd lists a collection under one upload: the sales of
// a sales upload, the lines of a statement.
func (a *App) newUploadSubListCmd(use, short, segment, sub, what string, columns []output.Column, filters ...filter) *cobra.Command {
	opts := &listOptions{}
	var readFilters func()
	cmd := &cobra.Command{
		Use:   use,
		Short: short,
		Args:  exactArgs("ID"),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := opts.validate(cmd); err != nil {
				return err
			}
			if readFilters != nil {
				readFilters()
			}
			s, err := a.session()
			if err != nil {
				return err
			}
			path, err := s.companyPath(cmd.Context(), segment, args[0], sub)
			if err != nil {
				return err
			}
			page, err := a.fetchList(cmd.Context(), s, path, sub, opts)
			if err != nil {
				return err
			}
			return a.renderList(page, columns, what, opts)
		},
	}
	addListFlags(cmd, opts)
	if len(filters) > 0 {
		readFilters = addFilterFlags(cmd, filters, opts)
	}
	return cmd
}
