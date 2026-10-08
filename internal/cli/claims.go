package cli

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/mdchaney/streamingchasers-cli/internal/api"
	"github.com/mdchaney/streamingchasers-cli/internal/output"
	"github.com/spf13/cobra"
)

// The claims workflow: what a PRO has not paid, which payment periods a
// claims batch can still be made for, the batches themselves and their
// sheets, and the two views that say where to chase first.

const claimsLong = `The chase-and-claim loop, for one PRO at a time (--pro, by ID or
abbreviation):

  streamingchasers chase list --pro ASCAP            where the money is
  streamingchasers unpaid list --pro ASCAP           what is claimable
  streamingchasers batches missing-works --pro ASCAP works that need a code first
  streamingchasers periods list --pro ASCAP --available
  streamingchasers batches preview --pro ASCAP --period 12
  streamingchasers batches create --pro ASCAP --period 12
  streamingchasers batches csv 34 --save            the claims sheet

A batch locks out its payment period and every earlier one for that PRO.
Productions the PRO pays as a rolled-up bundle are never claimed; see
'streamingchasers rollups'.`

func addProFlag(cmd *cobra.Command, pro *string) {
	cmd.Flags().StringVar(pro, "pro", "", "the PRO, by ID or abbreviation (required)")
}

var unpaidColumns = []output.Column{
	{Header: "ID", Key: "id"},
	{Header: "WORK", Value: nested("work", "id")},
	{Header: "TITLE", Value: nested("work", "title"), Max: 30},
	{Header: "CODE", Key: "registration_code"},
	{Header: "PRODUCTION", Key: "production_title", Max: 35},
	{Header: "EPISODE", Key: "episode_title", Max: 25},
	{Header: "AIRED", Key: "air_date"},
	{Header: "STREAMER", Key: "streamer", Max: 20},
	{Header: "TERRITORY", Key: "territory", Max: 20},
}

// proList lists a collection nested under a PRO.
func (a *App) proList(cmd *cobra.Command, pro string, opts *listOptions, key string, columns []output.Column, what string, segments ...any) (*api.Page, error) {
	if err := opts.validate(cmd); err != nil {
		return nil, err
	}
	s, err := a.session()
	if err != nil {
		return nil, err
	}
	path, err := pathWithPro(s, cmd, pro, segments...)
	if err != nil {
		return nil, err
	}
	page, err := a.fetchList(cmd.Context(), s, path, key, opts)
	if err != nil {
		return nil, err
	}
	return page, a.renderList(page, columns, what, opts)
}

func (a *App) newPeriodsCmd() *cobra.Command {
	var pro string
	var available bool
	opts := &listOptions{all: true, page: 1}
	list := &cobra.Command{
		Use:   "list --pro PRO",
		Short: "List a PRO's payment periods and whether a batch can still be made for each",
		Long: `List a PRO's payment periods. A period is available when no batch exists
for it or for a later one; --available lists only those. A period that
has a batch shows its ID.`,
		Args: noArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if available {
				opts.extra = url.Values{"available": {"1"}}
			}
			columns := []output.Column{
				{Header: "ID", Key: "id"},
				{Header: "NAME", Key: "name", Max: 30},
				{Header: "REPORT DATE", Key: "report_date"},
				{Header: "FROM", Key: "start_date"},
				{Header: "TO", Key: "end_date"},
				{Header: "AVAILABLE", Value: yesNo("available")},
				{Header: "BATCH", Key: "batch_id"},
			}
			_, err := a.proList(cmd, pro, opts, "payment_periods", columns, "payment periods", "payment_periods")
			return err
		},
	}
	addProFlag(list, &pro)
	list.Flags().BoolVar(&available, "available", false, "only the periods a batch can still be made for")
	return group("periods", "A PRO's payment periods", claimsLong, list)
}

func (a *App) newUnpaidCmd() *cobra.Command {
	var pro string
	opts := &listOptions{}
	list := &cobra.Command{
		Use:   "list --pro PRO",
		Short: "List the placements a PRO has not paid for and that can be claimed",
		Long: `List the unpaid broadcast placements that can be claimed from a PRO: those
whose work carries a registration code for it. For MCPS, the rows PRS has
paid and MCPS has not.`,
		Args: noArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			_, err := a.proList(cmd, pro, opts, "unpaid_broadcasts", unpaidColumns, "unpaid placements", "unpaid_broadcasts")
			return err
		},
	}
	addProFlag(list, &pro)
	addListFlags(list, opts)
	return group("unpaid", "The placements a PRO has not paid for", claimsLong, list)
}

var batchColumns = []output.Column{
	{Header: "ID", Key: "id"},
	{Header: "PRO", Key: "pro"},
	{Header: "PERIOD", Value: nested("payment_period", "name"), Max: 30},
	{Header: "ROWS", Key: "broadcasts_count"},
	{Header: "SENT", Value: timestamp("sent_at")},
	{Header: "CREATED", Value: timestamp("created_at")},
	{Header: "NOTES", Key: "notes", Max: 30},
}

func (a *App) newBatchesCmd() *cobra.Command {
	cmd := group("batches", "Claims batches: the sheets of unpaid placements sent to a PRO", `Claims batches. A batch gathers every claimable unpaid placement up to a
payment period into a sheet to send to the PRO, and marks the period and
every earlier one as claimed. Preview one first; what the preview shows is
exactly what create will take.

`+claimsLong)

	var pro string
	opts := &listOptions{}
	list := &cobra.Command{
		Use:   "list",
		Short: "List the company's batches",
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := opts.validate(cmd); err != nil {
				return err
			}
			s, err := a.session()
			if err != nil {
				return err
			}
			if pro != "" {
				id, err := s.proID(cmd.Context(), pro)
				if err != nil {
					return err
				}
				opts.extra = url.Values{"pro_id": {fmt.Sprint(id)}}
			}
			path, err := s.companyPath(cmd.Context(), "broadcast_delivery_batches")
			if err != nil {
				return err
			}
			page, err := a.fetchList(cmd.Context(), s, path, "broadcast_delivery_batches", opts)
			if err != nil {
				return err
			}
			return a.renderList(page, batchColumns, "batches", opts)
		},
	}
	list.Flags().StringVar(&pro, "pro", "", "only one PRO's batches, by ID or abbreviation")
	addListFlags(list, opts)

	get := &cobra.Command{
		Use:     "get ID",
		Aliases: []string{"show"},
		Short:   "Show a batch",
		Args:    exactArgs("ID"),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := a.session()
			if err != nil {
				return err
			}
			path, err := s.companyPath(cmd.Context(), "broadcast_delivery_batches", args[0])
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

	cmd.AddCommand(list, get, a.newBatchPreviewCmd(), a.newBatchCreateCmd(), a.newBatchCSVCmd(), a.newPaidReportCmd(), a.newMissingWorksCmd(), a.newMissingCodesCmd())
	return cmd
}

func (a *App) newPaidReportCmd() *cobra.Command {
	var pro string
	var sheet int64
	opts := &listOptions{}
	cmd := &cobra.Command{
		Use:   "paid-report",
		Short: "What got paid that was on a sent claims sheet",
		Long: `What came of the claims sheets that were sent: per sheet, how many
placements it claimed and how many have been paid since, at all and
after the sheet went out; then the paid placements themselves, most
recently paid first, each with the sheets that claimed it and whether
the payment came after the claim (no means it was already on file when
the sheet went out).

A placement claimed on two sheets is one row. --sheet narrows the rows
to those one sheet claimed, reading every page to do it; --pro narrows
everything to one PRO.`,
		Example: `  streamingchasers batches paid-report --pro ASCAP
  streamingchasers batches paid-report --sheet 34 -o csv`,
		Args: noArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if sheet > 0 && !cmd.Flags().Changed("all") {
				opts.all = true
			}
			if err := opts.validate(cmd); err != nil {
				return err
			}
			if pro != "" {
				segment, err := proSegment(pro)
				if err != nil {
					return err
				}
				opts.extra = url.Values{"pro_id": {segment}}
			}
			s, err := a.session()
			if err != nil {
				return err
			}
			path, err := s.companyPath(cmd.Context(), "broadcast_delivery_batches", "paid_report")
			if err != nil {
				return err
			}
			page, err := a.fetchList(cmd.Context(), s, path, "paid_placements", opts)
			if err != nil {
				return err
			}
			sheets, _ := listOf(mustJSON(extras(page)), "sheets")
			if sheet > 0 {
				page.Items = onSheet(page.Items, sheet)
				page.Paginated = false
				sheets = withID(sheets, sheet)
			}
			// As JSON the report is the whole of what the server said:
			// the sheets and the placements together.
			if a.globals.output == output.JSON {
				report := output.NewRecord()
				report.Set("sheets", json.RawMessage(mustJSONList(sheets)))
				report.Set("paid_placements", json.RawMessage(mustJSONList(page.Items)))
				return output.WriteJSON(a.Out, mustJSON(report))
			}
			if a.globals.output == output.Table {
				sheetColumns := []output.Column{
					{Header: "SHEET", Key: "id"},
					{Header: "PRO", Key: "pro"},
					{Header: "PERIOD", Value: nested("payment_period", "name"), Max: 30},
					{Header: "SENT", Value: timestamp("sent_at")},
					{Header: "CLAIMED", Key: "claimed"},
					{Header: "PAID", Key: "paid"},
					{Header: "PAID AFTER SEND", Key: "paid_after_send"},
				}
				if err := a.renderItems(sheets, sheetColumns, "sent sheets"); err != nil {
					return err
				}
				fmt.Fprintln(a.Out)
			}
			columns := []output.Column{
				{Header: "WORK", Value: nested("work", "id")},
				{Header: "TITLE", Value: nested("work", "title"), Max: 30},
				{Header: "PRODUCTION", Value: nested("production", "title"), Max: 35},
				{Header: "STREAMER", Key: "streamer", Max: 20},
				{Header: "PRO", Key: "pro"},
				{Header: "AIRED", Key: "initial_air_date"},
				{Header: "PAID", Value: timestamp("paid_at")},
				{Header: "AFTER CLAIM", Value: yesNo("paid_after_claim")},
				{Header: "SHEETS", Key: "sheet_ids"},
			}
			return a.renderList(page, columns, "paid placements", opts)
		},
	}
	cmd.Flags().StringVar(&pro, "pro", "", "one PRO's sheets and placements, by ID or abbreviation")
	cmd.Flags().Int64Var(&sheet, "sheet", 0, "only the placements this sheet claimed")
	addListFlags(cmd, opts)
	return cmd
}

// onSheet keeps the paid placements that the sheet claimed.
func onSheet(items []json.RawMessage, sheet int64) []json.RawMessage {
	var kept []json.RawMessage
	for _, item := range items {
		record, err := output.ParseRecord(item)
		if err != nil {
			continue
		}
		for _, id := range listOfIDs(record.Value("sheet_ids")) {
			if id == sheet {
				kept = append(kept, item)
				break
			}
		}
	}
	return kept
}

// withID keeps the record with the id.
func withID(items []json.RawMessage, id int64) []json.RawMessage {
	for _, item := range items {
		if record, err := output.ParseRecord(item); err == nil && record.String("id") == strconv.FormatInt(id, 10) {
			return []json.RawMessage{item}
		}
	}
	return nil
}

// mustJSONList joins raw JSON records into one array.
func mustJSONList(items []json.RawMessage) []byte {
	var b strings.Builder
	b.WriteByte('[')
	for i, item := range items {
		if i > 0 {
			b.WriteByte(',')
		}
		b.Write(item)
	}
	b.WriteByte(']')
	return []byte(b.String())
}

func listOfIDs(v any) []int64 {
	var ids []int64
	if list, ok := v.([]any); ok {
		for _, item := range list {
			if n, err := strconv.ParseInt(output.Format(item), 10, 64); err == nil {
				ids = append(ids, n)
			}
		}
	}
	return ids
}

// thresholds are the chase-score floors a batch can be narrowed by.
type thresholds struct {
	minMoney, minViews string
	minUnpaid          int64
}

func addThresholdFlags(cmd *cobra.Command, t *thresholds) {
	cmd.Flags().StringVar(&t.minMoney, "min-money", "", "leave out series whose chase score shows less money than this")
	cmd.Flags().StringVar(&t.minViews, "min-views", "", "leave out series with fewer estimated views than this")
	cmd.Flags().Int64Var(&t.minUnpaid, "min-unpaid", 0, "leave out series with fewer unpaid placements than this")
}

func (a *App) newBatchPreviewCmd() *cobra.Command {
	var pro string
	var period int64
	var t thresholds
	opts := &listOptions{}
	cmd := &cobra.Command{
		Use:   "preview --pro PRO --period ID",
		Short: "Show what a batch for a payment period would hold, without making it",
		Long: `Show what a batch for a payment period would hold: how many rows, how
many are left out for want of a registration code or because the PRO
rolls their production up, and the rows themselves, a page at a time.

The same thresholds that create takes narrow the preview the same way.`,
		Args: noArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if period <= 0 {
				return usagef("--period must name a payment period; 'streamingchasers periods list --pro %s --available' shows them", pro)
			}
			opts.extra = url.Values{"payment_period_id": {fmt.Sprint(period)}}
			if t.minMoney != "" {
				opts.extra.Set("min_money", t.minMoney)
			}
			if t.minViews != "" {
				opts.extra.Set("min_views", t.minViews)
			}
			if t.minUnpaid > 0 {
				opts.extra.Set("min_unpaid", fmt.Sprint(t.minUnpaid))
			}
			if err := opts.validate(cmd); err != nil {
				return err
			}
			s, err := a.session()
			if err != nil {
				return err
			}
			path, err := pathWithPro(s, cmd, pro, "broadcast_delivery_batches", "preview")
			if err != nil {
				return err
			}
			page, err := a.fetchList(cmd.Context(), s, path, "rows", opts)
			if err != nil {
				return err
			}
			if a.globals.output == output.Table {
				summary := extras(page)
				first := ""
				if summary.String("is_first_batch") == "true" {
					first = " This would be the first batch for this PRO, so it takes every unpaid placement to date."
				}
				fmt.Fprintf(a.Err, "A batch for %s would hold %s rows. Left out: %s for missing codes, %s rolled up.%s\n",
					nested("payment_period", "name")(summary), summary.String("total_includable"),
					summary.String("excluded_missing_codes"), summary.String("excluded_rolled_up"), first)
				if rolled, ok := summary.Value("rolled_up_productions").([]any); ok && len(rolled) > 0 {
					for _, entry := range rolled {
						if r, ok := entry.(*output.Record); ok {
							fmt.Fprintf(a.Err, "  rolled up: %s %q, %s rows\n", r.String("production_type"), r.String("title"), r.String("excluded_count"))
						}
					}
				}
				fmt.Fprintln(a.Err)
			}
			return a.renderList(page, unpaidColumns, "rows", opts)
		},
	}
	addProFlag(cmd, &pro)
	cmd.Flags().Int64Var(&period, "period", 0, "ID of the payment period (required)")
	addThresholdFlags(cmd, &t)
	addListFlags(cmd, opts)
	return cmd
}

// extras returns the top-level fields of a list response, besides the
// list, as a record.
func extras(page *api.Page) *output.Record {
	record := output.NewRecord()
	for key, raw := range page.Extra {
		value, err := output.Parse(raw)
		if err == nil {
			record.Set(key, value)
		}
	}
	return record
}

func (a *App) newBatchCreateCmd() *cobra.Command {
	var pro, notes string
	var period int64
	var t thresholds
	var exclude []int64
	var yes bool
	cmd := &cobra.Command{
		Use:   "create --pro PRO --period ID",
		Short: "Make a claims batch for a payment period",
		Long: `Make a claims batch for a payment period. The server takes every row the
preview showed, less any named with --exclude, and the batch marks the
period and every earlier one as claimed for this PRO: no batch can be
made for them afterwards.

You are asked to confirm. A script passes --yes.`,
		Example: `  streamingchasers batches create --pro ASCAP --period 12 --notes "Q1 2026"
  streamingchasers batches create --pro BMI --period 9 --min-money 50 --exclude 4411 --exclude 4412 --yes`,
		Args: noArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if period <= 0 {
				return usagef("--period must name a payment period; 'streamingchasers periods list --pro %s --available' shows them", pro)
			}
			s, err := a.session()
			if err != nil {
				return err
			}
			path, err := pathWithPro(s, cmd, pro, "broadcast_delivery_batches")
			if err != nil {
				return err
			}
			if err := a.confirmDestructive(yes, "create a batch", fmt.Sprintf("Make a %s batch for payment period %d? This closes the period and every earlier one to further batches.", strings.ToUpper(pro), period), "No batch was made."); err != nil {
				return err
			}
			batch := map[string]any{"broadcast_payment_period_id": period}
			if notes != "" {
				batch["notes"] = notes
			}
			if t.minMoney != "" {
				batch["min_money_observed"] = t.minMoney
			}
			if t.minViews != "" {
				batch["min_views_total_est"] = t.minViews
			}
			if t.minUnpaid > 0 {
				batch["min_unpaid_placements"] = t.minUnpaid
			}
			body := map[string]any{"broadcast_delivery_batch": batch}
			if len(exclude) > 0 {
				body["excluded_broadcasts_pros_sale_ids"] = exclude
			}
			resp, err := s.client.Post(cmd.Context(), path, body)
			if err != nil {
				return err
			}
			record, _ := output.ParseRecord(resp.Body)
			fmt.Fprintf(a.Err, "Made batch %s with %s rows. Left out: %s excluded by you, %s for missing codes, %s rolled up.\n",
				record.String("id"), record.String("added_count"), record.String("excluded_by_client"),
				record.String("excluded_missing_codes"), record.String("excluded_rolled_up"))
			fmt.Fprintf(a.Err, "Download the sheet with 'streamingchasers batches csv %s --save'.\n", record.String("id"))
			return a.renderRecord(resp.Body, "")
		},
	}
	addProFlag(cmd, &pro)
	cmd.Flags().Int64Var(&period, "period", 0, "ID of the payment period (required)")
	cmd.Flags().StringVar(&notes, "notes", "", "notes on the batch")
	addThresholdFlags(cmd, &t)
	cmd.Flags().Int64SliceVar(&exclude, "exclude", nil, "ID of a row from the preview to leave out (may be repeated)")
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "make the batch without asking")
	return cmd
}

func (a *App) newBatchCSVCmd() *cobra.Command {
	var variant, sort string
	var noNotes, noMarkSent bool
	save := &saveOptions{}
	cmd := &cobra.Command{
		Use:   "csv ID",
		Short: "Download a batch's claims sheet, which marks the batch as sent",
		Long: `Download a batch's claims sheet, as the PRO wants it (GEMA batches come
as a spreadsheet). Downloading the sheet marks the batch as sent, unless
--no-mark-sent says to download it for checking first.

--variant missing-codes downloads instead the companion sheet of rows that
were left out for want of a work code, which does not mark the batch
sent. --no-notes leaves out the publisher-notes column, which is slow to
build; --sort chase-money or --sort chase-views reorders the rows.

The sheet goes to standard output unless --save or --save-as says
otherwise.`,
		Args: exactArgs("ID"),
		RunE: func(cmd *cobra.Command, args []string) error {
			query := url.Values{}
			switch variant {
			case "":
			case "missing-codes", "missing_codes":
				query.Set("variant", "missing_codes")
			default:
				return usagef("--variant must be missing-codes")
			}
			switch sort {
			case "":
			case "chase-money", "chase_money":
				query.Set("sort", "chase_money")
			case "chase-views", "chase_views":
				query.Set("sort", "chase_views")
			default:
				return usagef("--sort must be chase-money or chase-views")
			}
			if noNotes {
				query.Set("with_notes", "0")
			}
			if noMarkSent {
				query.Set("mark_sent", "0")
			}
			s, err := a.session()
			if err != nil {
				return err
			}
			path, err := s.companyPath(cmd.Context(), "broadcast_delivery_batches", args[0], "csv")
			if err != nil {
				return err
			}
			resp, err := s.client.Download(cmd.Context(), path, query)
			if err != nil {
				return err
			}
			if err := a.deliver(resp, save, "batch-"+args[0]+".csv"); err != nil {
				return err
			}
			if variant == "" && !noMarkSent {
				fmt.Fprintf(a.Err, "Batch %s is now marked as sent.\n", args[0])
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&variant, "variant", "", "missing-codes for the sheet of rows left out for want of a code")
	cmd.Flags().StringVar(&sort, "sort", "", "chase-money or chase-views")
	cmd.Flags().BoolVar(&noNotes, "no-notes", false, "leave out the publisher-notes column")
	cmd.Flags().BoolVar(&noMarkSent, "no-mark-sent", false, "download without marking the batch as sent")
	addSaveFlags(cmd, save)
	return cmd
}

func (a *App) newMissingWorksCmd() *cobra.Command {
	var pro string
	cmd := &cobra.Command{
		Use:   "missing-works --pro PRO",
		Short: "List the works with unpaid placements but no registration code for the PRO",
		Long: `List the works that have unpaid placements but no registration code for
the PRO, which keeps their placements out of every batch. Add a code with
'streamingchasers works update EXTERNAL_ID --code TYPE_ID:CODE'; the type
to use is named first.`,
		Args: noArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := a.session()
			if err != nil {
				return err
			}
			path, err := pathWithPro(s, cmd, pro, "broadcast_delivery_batches", "missing_work_ids")
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
			page, err := api.ParsePage(resp.Body, "missing_works")
			if err != nil {
				return err
			}
			if a.globals.output == output.Table {
				summary := extras(page)
				if kind, ok := summary.Value("registration_type").(*output.Record); ok {
					fmt.Fprintf(a.Err, "Codes for %s are registration type %s, %s", summary.String("pro"), kind.String("id"), kind.String("name"))
					if what := kind.String("work_id_description"); what != "" {
						fmt.Fprintf(a.Err, " (%s)", what)
					}
					fmt.Fprintln(a.Err, ".")
				}
			}
			columns := []output.Column{
				{Header: "ID", Key: "id"},
				{Header: "TITLE", Key: "title", Max: 60},
				{Header: "CATALOG", Key: "catalog", Max: 30},
			}
			return a.renderItems(page.Items, columns, "works missing a code")
		},
	}
	addProFlag(cmd, &pro)
	return cmd
}

func (a *App) newMissingCodesCmd() *cobra.Command {
	var pro string
	var period int64
	save := &saveOptions{}
	cmd := &cobra.Command{
		Use:   "missing-codes --pro PRO --period ID",
		Short: "Download the sheet of rows a batch would leave out for want of a code",
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if period <= 0 {
				return usagef("--period must name a payment period")
			}
			s, err := a.session()
			if err != nil {
				return err
			}
			path, err := pathWithPro(s, cmd, pro, "broadcast_delivery_batches", "missing_codes_csv")
			if err != nil {
				return err
			}
			resp, err := s.client.Download(cmd.Context(), path, url.Values{"payment_period_id": {fmt.Sprint(period)}})
			if err != nil {
				return err
			}
			return a.deliver(resp, save, "missing-codes.csv")
		},
	}
	addProFlag(cmd, &pro)
	cmd.Flags().Int64Var(&period, "period", 0, "ID of the payment period (required)")
	addSaveFlags(cmd, save)
	return cmd
}

func (a *App) newChaseCmd() *cobra.Command {
	cmd := group("chase", "Chase scores: which series to chase a PRO about first", `Chase scores. For each series a company has placements in, and each PRO,
the money and views observed and how many placements are paid and unpaid.
Left out of the list, with counts, are series that are fully paid and
series whose every unpaid placement the PRO has rolled up; a series with a
rollup but other claimable episodes still lists, as a batch would take
them.

The scores are computed in the background; 'recompute' refreshes them.`)

	var pro, sort string
	opts := &listOptions{}
	list := &cobra.Command{
		Use:   "list --pro PRO",
		Short: "List the chase scores for a PRO",
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if sort != "" {
				opts.extra = url.Values{"sort": {sort}}
			}
			columns := []output.Column{
				{Header: "SERIES", Value: nested("series", "id")},
				{Header: "TITLE", Value: nested("series", "title"), Max: 40},
				{Header: "MONEY", Key: "money_observed"},
				{Header: "VIEWS", Key: "views_observed"},
				{Header: "EST. VIEWS", Key: "views_total_est"},
				{Header: "PAID", Key: "paid_broadcasts_count"},
				{Header: "UNPAID", Key: "unpaid_broadcasts_count"},
			}
			if err := opts.validate(cmd); err != nil {
				return err
			}
			s, err := a.session()
			if err != nil {
				return err
			}
			path, err := pathWithPro(s, cmd, pro, "series_chase_scores")
			if err != nil {
				return err
			}
			page, err := a.fetchList(cmd.Context(), s, path, "series_chase_scores", opts)
			if err != nil {
				return err
			}
			if a.globals.output == output.Table {
				summary := extras(page)
				fmt.Fprintf(a.Err, "Sorted by %s. Hidden: %s rolled up, %s fully paid. Last computed %s.\n\n",
					summary.String("sort"), summary.String("rolled_up_hidden_count"), summary.String("fully_paid_hidden_count"), orNever(summary.String("last_computed_at")))
			}
			return a.renderList(page, columns, "chase scores", opts)
		},
	}
	addProFlag(list, &pro)
	list.Flags().StringVar(&sort, "sort", "", "money, views_observed, views, paid, unpaid or updated (default money)")
	addListFlags(list, opts)

	var getPro string
	get := &cobra.Command{
		Use:     "get SERIES_ID --pro PRO",
		Aliases: []string{"show"},
		Short:   "Show one series' chase score for a PRO",
		Args:    exactArgs("SERIES_ID"),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := a.session()
			if err != nil {
				return err
			}
			path, err := pathWithPro(s, cmd, getPro, "series_chase_scores", args[0])
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
	addProFlag(get, &getPro)

	var placementsPro string
	placementsOpts := &listOptions{}
	placements := &cobra.Command{
		Use:   "placements SERIES_ID --pro PRO",
		Short: "List every placement in a series, paid and unpaid, for a PRO",
		Args:  exactArgs("SERIES_ID"),
		RunE: func(cmd *cobra.Command, args []string) error {
			columns := []output.Column{
				{Header: "ID", Key: "id"},
				{Header: "EPISODE", Key: "episode_title", Max: 30},
				{Header: "S", Key: "season_number"},
				{Header: "E", Key: "episode_number"},
				{Header: "WORK", Key: "work_id"},
				{Header: "TITLE", Key: "work_title", Max: 30},
				{Header: "STREAMER", Key: "streamer", Max: 20},
				{Header: "AIRED", Key: "air_date"},
				{Header: "PAID", Value: timestamp("paid_at")},
				{Header: "MONEY", Key: "money_observed"},
				{Header: "VIEWS", Key: "views_observed"},
			}
			_, err := a.proList(cmd, placementsPro, placementsOpts, "placements", columns, "placements", "series_chase_scores", args[0], "placements")
			return err
		},
	}
	addProFlag(placements, &placementsPro)
	addListFlags(placements, placementsOpts)

	var recomputePro string
	recompute := &cobra.Command{
		Use:   "recompute --pro PRO",
		Short: "Recompute the company's chase scores, for every PRO, in the background",
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := a.session()
			if err != nil {
				return err
			}
			path, err := pathWithPro(s, cmd, recomputePro, "series_chase_scores", "recompute")
			if err != nil {
				return err
			}
			resp, err := s.client.Post(cmd.Context(), path, nil)
			if err != nil {
				return err
			}
			record, _ := output.ParseRecord(resp.Body)
			if message := record.String("message"); message != "" {
				fmt.Fprintln(a.Err, message+".")
			} else {
				fmt.Fprintln(a.Err, "The scores are being recomputed.")
			}
			return nil
		},
	}
	addProFlag(recompute, &recomputePro)

	cmd.AddCommand(list, get, placements, recompute)
	return cmd
}

func orNever(s string) string {
	if s == "" {
		return "never"
	}
	return s
}

func (a *App) newRollupsCmd() *cobra.Command {
	cmd := group("rollups", "Productions a PRO pays as a bundle, which claims sheets leave out", `Rolled-up productions: those a PRO reports as one composite royalty line
rather than by work. Their placements are never put in a claims sheet, so
this is where to look for money a batch cannot reach.`)

	var pro, sort string
	opts := &listOptions{}
	list := &cobra.Command{
		Use:   "list --pro PRO",
		Short: "List the productions a PRO rolls up, with what each blocks",
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if sort != "" {
				opts.extra = url.Values{"sort": {sort}}
			}
			columns := []output.Column{
				{Header: "TYPE", Key: "production_type"},
				{Header: "ID", Key: "production_id"},
				{Header: "TITLE", Key: "title", Max: 40},
				{Header: "LINES", Key: "rollup_records"},
				{Header: "AMOUNT", Key: "total_amount"},
				{Header: "CUR", Key: "currencies"},
				{Header: "FIRST", Key: "first_usage_on"},
				{Header: "LAST", Key: "last_usage_on"},
				{Header: "UNPAID BLOCKED", Key: "unpaid_broadcasts_blocked"},
			}
			if err := opts.validate(cmd); err != nil {
				return err
			}
			s, err := a.session()
			if err != nil {
				return err
			}
			path, err := pathWithPro(s, cmd, pro, "rolled_up_productions")
			if err != nil {
				return err
			}
			page, err := a.fetchList(cmd.Context(), s, path, "rolled_up_productions", opts)
			if err != nil {
				return err
			}
			if a.globals.output == output.Table {
				summary := extras(page)
				fmt.Fprintf(a.Err, "%s productions rolled up by %s, blocking %s unpaid placements across %s rollup lines.\n\n",
					summary.String("total_productions"), summary.String("pro"), summary.String("total_unpaid_blocked"), summary.String("total_rollup_records"))
			}
			return a.renderList(page, columns, "rolled-up productions", opts)
		},
	}
	addProFlag(list, &pro)
	list.Flags().StringVar(&sort, "sort", "", "unpaid, title, records, amount, first or last")
	addListFlags(list, opts)

	var getPro string
	get := &cobra.Command{
		Use:     "get TYPE ID --pro PRO",
		Aliases: []string{"show"},
		Short:   "Show a rolled-up production: its rollup lines and its works",
		Long: `Show a rolled-up production, named by its type (Movie, Series or Episode)
and ID: the composite lines the PRO paid, and each work in it with its
placements and what was paid by line.`,
		Args: exactArgs("TYPE", "ID"),
		RunE: func(cmd *cobra.Command, args []string) error {
			kind := strings.ToUpper(args[0][:1]) + strings.ToLower(args[0][1:])
			switch kind {
			case "Movie", "Series", "Episode":
			default:
				return usagef("TYPE must be Movie, Series or Episode, not %q", args[0])
			}
			s, err := a.session()
			if err != nil {
				return err
			}
			path, err := pathWithPro(s, cmd, getPro, "rolled_up_productions", kind, args[1])
			if err != nil {
				return err
			}
			resp, err := s.client.Get(cmd.Context(), path, nil)
			if err != nil {
				return err
			}
			if a.globals.output != output.Table {
				return a.renderRecord(resp.Body, "")
			}
			record, err := output.ParseRecord(resp.Body)
			if err != nil {
				return err
			}
			production, _ := record.Value("production").(*output.Record)
			fmt.Fprintf(a.Out, "%s %s: %s\nRolled up by %s: %s lines, %s %s\n\n", production.String("type"), production.String("id"), production.String("title"),
				record.String("pro"), countOf(record.Value("rollup_records")), record.String("rollup_total"), record.String("currencies"))
			works, _ := listOf(resp.Body, "works")
			columns := []output.Column{
				{Header: "WORK", Key: "id"},
				{Header: "TITLE", Key: "title", Max: 40},
				{Header: "PLACEMENTS", Key: "placements"},
				{Header: "PAID", Key: "paid"},
				{Header: "UNPAID", Key: "unpaid"},
				{Header: "ITEMIZED LINES", Key: "itemized_lines"},
				{Header: "ITEMIZED AMOUNT", Key: "itemized_amount"},
			}
			return a.renderItems(works, columns, "works")
		},
	}
	addProFlag(get, &getPro)

	cmd.AddCommand(list, get)
	return cmd
}

func countOf(v any) string {
	if list, ok := v.([]any); ok {
		return fmt.Sprint(len(list))
	}
	return "0"
}
