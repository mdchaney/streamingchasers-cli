package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/mdchaney/streamingchasers-api/internal/api"
	"github.com/mdchaney/streamingchasers-api/internal/output"
	"github.com/spf13/cobra"
)

// scope is where a resource lives.
type scope int

const (
	// scopeGlobal resources, such as PROs, are shared by every company.
	scopeGlobal scope = iota
	// scopeCompany resources belong to a company.
	scopeCompany
)

// fieldKind is the type of a field that can be set with a flag.
type fieldKind int

const (
	kindString fieldKind = iota
	// kindText is a string that may be read from a file with @path.
	kindText
	kindInt
	kindNumber
	kindBool
	kindDate
	// kindStrings is a list of strings, from a flag given more than once.
	kindStrings
	// kindJSON is any JSON value, which may be read from a file with @path.
	kindJSON
)

// field is a field of a record that a flag of create and update sets.
type field struct {
	flag  string
	key   string
	kind  fieldKind
	usage string
}

// filter is a flag of list that narrows it, by a query parameter.
type filter struct {
	flag string
	// param is the query parameter, such as "q[title]".
	param string
	usage string
}

// resource describes a kind of record in terms the generic list, get,
// create, update, delete and import commands work from.
type resource struct {
	// name is the command: "writers".
	name    string
	aliases []string
	// singular names one record and is the key request bodies are
	// wrapped in: "writer".
	singular string
	// label is how a record is called in messages: "writer".
	label string
	// pluralLabel is the plural of label when adding an s does not make it.
	pluralLabel string
	// segment is the collection's path segment and the key of its list
	// in a response: "writers".
	segment string
	// listKey is the key of the list in a response when it is not
	// segment.
	listKey string
	// unwrapKey is the key a single record comes wrapped under, for the
	// endpoints that wrap one: {"catalog": {...}}.
	unwrapKey string
	short     string
	long      string
	scope     scope
	// idArg names the argument that addresses a record.
	idArg string
	// idKey is the key a request body carries the record's own
	// identifier under, or "" when the server assigns it.
	idKey string
	// idUsage describes --id.
	idUsage string
	columns []output.Column
	fields  []field
	filters []filter
	// readOnly resources have no create, update or delete.  The others
	// say which of the usual commands a resource does without.
	readOnly bool
	noGet    bool
	noCreate bool
	noUpdate bool
	noDelete bool
	// importable resources have an import command.
	importable bool
	// flags adds flags particular to the resource to create and update.
	flags func(cmd *cobra.Command)
	// apply puts the values of those flags into the body.
	apply func(a *App, cmd *cobra.Command, body *output.Record) error
	// nameKeys are the fields a record can be named by, for byName;
	// "name" when there are none.
	nameKeys []string
	// byName lets a record be named instead of numbered, for records
	// the server addresses by an ID of its own.
	byName bool
	// createExample and updateExample are shown in the help.
	createExample string
	updateExample string
	// adminScope says that changing the resource takes catalog_admin.
	adminScope bool
	// prepare, if set, turns a record as the API serves it into one it
	// takes, after the read-only keys are dropped.
	prepare func(record *output.Record)
}

func (r *resource) plural() string {
	if r.pluralLabel != "" {
		return r.pluralLabel
	}
	return r.label + "s"
}

func (r *resource) key() string {
	if r.listKey != "" {
		return r.listKey
	}
	return r.segment
}

// collection returns the path of the resource's collection.
func (a *App) collection(ctx context.Context, s *session, r *resource) (string, error) {
	if r.scope == scopeGlobal {
		return api.Path(r.segment), nil
	}
	return s.companyPath(ctx, r.segment)
}

// member returns the path of the record that arg addresses.
func (a *App) member(ctx context.Context, s *session, r *resource, arg string) (string, error) {
	collection, err := a.collection(ctx, s, r)
	if err != nil {
		return "", err
	}
	if _, err := strconv.ParseInt(arg, 10, 64); err != nil && r.byName {
		record, err := s.findByName(ctx, collection, r.key(), r.label, arg, r.nameKeys...)
		if err != nil {
			return "", err
		}
		arg = record.String("id")
	}
	return collection + "/" + url.PathEscape(arg), nil
}

// newResourceCmd builds the command for a resource and its subcommands.
func (a *App) newResourceCmd(r *resource) *cobra.Command {
	cmd := &cobra.Command{
		Use:     r.name,
		Aliases: r.aliases,
		Short:   r.short,
		Long:    r.long,
		Args:    subcommandsOnly,
		RunE:    func(cmd *cobra.Command, args []string) error { return cmd.Help() },
	}
	cmd.AddCommand(a.newListCmd(r))
	if !r.noGet {
		cmd.AddCommand(a.newGetCmd(r))
	}
	if !r.readOnly && !r.noCreate {
		cmd.AddCommand(a.newCreateCmd(r))
	}
	if !r.readOnly && !r.noUpdate {
		cmd.AddCommand(a.newUpdateCmd(r))
	}
	if !r.readOnly && !r.noDelete {
		cmd.AddCommand(a.newDeleteCmd(r))
	}
	if r.importable {
		cmd.AddCommand(a.newImportCmd(r))
	}
	return cmd
}

// listOptions are the flags of a list command.
type listOptions struct {
	page    int
	perPage int
	all     bool
	limit   int
	// extra is what narrows the list, from a resource's filters.
	extra url.Values
}

// filter is what narrows the list, for the request's query.
func (o *listOptions) filter() url.Values {
	if len(o.extra) == 0 {
		return nil
	}
	filter := url.Values{}
	for key, values := range o.extra {
		filter[key] = values
	}
	return filter
}

func addListFlags(cmd *cobra.Command, opts *listOptions) {
	cmd.Flags().IntVar(&opts.page, "page", 1, "page to show")
	cmd.Flags().IntVar(&opts.perPage, "per-page", 0, fmt.Sprintf("records per page, up to %d (default: the server's, usually 25)", api.MaxPerPage))
	cmd.Flags().BoolVar(&opts.all, "all", false, "show every record, reading as many pages as it takes")
	cmd.Flags().IntVar(&opts.limit, "limit", 0, "show the first N records, reading as many pages as it takes")
}

// addFilterFlags adds a flag for each filter and returns what reads them
// into opts.extra.
func addFilterFlags(cmd *cobra.Command, filters []filter, opts *listOptions) func() {
	for _, f := range filters {
		cmd.Flags().String(f.flag, "", f.usage)
	}
	return func() {
		opts.extra = url.Values{}
		for _, f := range filters {
			if cmd.Flags().Changed(f.flag) {
				value, _ := cmd.Flags().GetString(f.flag)
				opts.extra.Set(f.param, value)
			}
		}
	}
}

func (o *listOptions) validate(cmd *cobra.Command) error {
	switch {
	case o.page < 1:
		return usagef("--page must be 1 or more")
	case o.perPage < 0 || o.perPage > api.MaxPerPage:
		return usagef("--per-page must be between 1 and %d", api.MaxPerPage)
	case o.limit < 0:
		return usagef("--limit must be 1 or more")
	case o.all && o.limit > 0:
		return usagef("--all and --limit cannot be used together")
	case (o.all || o.limit > 0) && (cmd.Flags().Changed("page") || cmd.Flags().Changed("per-page")):
		return usagef("--page and --per-page cannot be used with --all or --limit")
	}
	return nil
}

// fetchList reads the page, or pages, that opts asks for.
func (a *App) fetchList(ctx context.Context, s *session, path, key string, opts *listOptions) (*api.Page, error) {
	if opts.all || opts.limit > 0 {
		var progress func(read, total int)
		if a.Progress {
			progress = func(read, total int) {
				if total > api.MaxPerPage {
					fmt.Fprintf(a.Err, "\rRead %d of %d", read, total)
					if read >= total || (opts.limit > 0 && read >= opts.limit) {
						fmt.Fprint(a.Err, "\r\033[K")
					}
				}
			}
		}
		return s.client.ListAll(ctx, path, key, opts.filter(), opts.limit, progress)
	}
	return s.client.List(ctx, path, key, api.ListOptions{Page: opts.page, PerPage: opts.perPage, Query: opts.filter()})
}

// renderList writes a list in the chosen format.  what names the
// records in the plural, for the line under a table.
func (a *App) renderList(page *api.Page, columns []output.Column, what string, opts *listOptions) error {
	switch a.globals.output {
	case output.JSON:
		return output.WriteJSONList(a.Out, page.Items)
	case output.JSONL:
		return output.WriteJSONLines(a.Out, page.Items)
	}

	records, err := output.ParseRecords(page.Items)
	if err != nil {
		return err
	}
	if a.globals.output == output.CSV {
		return output.WriteCSV(a.Out, output.AllColumns(records), records)
	}

	if len(records) == 0 {
		switch {
		case page.Total() > 0:
			fmt.Fprintf(a.Err, "No %s on page %d; there are %d pages.\n", what, page.Pagination.CurrentPage, page.Pagination.TotalPages)
		case opts.filter() != nil:
			fmt.Fprintf(a.Err, "No %s match.\n", what)
		default:
			fmt.Fprintf(a.Err, "No %s.\n", what)
		}
		return nil
	}
	if err := output.WriteTable(a.Out, columns, records); err != nil {
		return err
	}
	if !opts.all && len(records) < page.Total() {
		p := page.Pagination
		hint := "Use --all for everything."
		if opts.limit == 0 && p.NextPage() > 0 {
			hint = fmt.Sprintf("Use --page %d for the next page or --all for everything.", p.NextPage())
		}
		if opts.limit > 0 {
			fmt.Fprintf(a.Err, "\nShowing %d of %d %s. %s\n", len(records), page.Total(), what, hint)
		} else {
			fmt.Fprintf(a.Err, "\nShowing %d of %d %s (page %d of %d). %s\n", len(records), page.Total(), what, p.CurrentPage, p.TotalPages, hint)
		}
	}
	return nil
}

// renderRecord writes one record in the chosen format.  unwrapKey, if
// not "", is the key the record comes wrapped under.
func (a *App) renderRecord(raw []byte, unwrapKey string) error {
	if unwrapKey != "" {
		if record, err := output.ParseRecord(raw); err == nil {
			if inner, ok := record.Value(unwrapKey).(*output.Record); ok {
				raw, _ = json.Marshal(inner)
			}
		}
	}
	switch a.globals.output {
	case output.JSON:
		return output.WriteJSON(a.Out, raw)
	case output.JSONL:
		return output.WriteJSONLines(a.Out, []json.RawMessage{raw})
	}
	record, err := output.ParseRecord(raw)
	if err != nil {
		return fmt.Errorf("the server sent a record that cannot be read: %w", err)
	}
	if a.globals.output == output.CSV {
		records := []*output.Record{record}
		return output.WriteCSV(a.Out, output.AllColumns(records), records)
	}
	return output.WriteFields(a.Out, record)
}

// renderItems writes a list that was not fetched as a page: the rows
// of a report, say.
func (a *App) renderItems(items []json.RawMessage, columns []output.Column, what string) error {
	return a.renderList(&api.Page{Items: items}, columns, what, &listOptions{all: true})
}

// listOf returns the list under key in a response.
func listOf(body []byte, key string) ([]json.RawMessage, error) {
	page, err := api.ParsePage(body, key)
	if err != nil {
		return nil, err
	}
	return page.Items, nil
}

func (a *App) newListCmd(r *resource) *cobra.Command {
	opts := &listOptions{}
	var readFilters func()
	cmd := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List " + r.plural(),
		Args:    noArgs,
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
			collection, err := a.collection(cmd.Context(), s, r)
			if err != nil {
				return err
			}
			page, err := a.fetchList(cmd.Context(), s, collection, r.key(), opts)
			if err != nil {
				return err
			}
			return a.renderList(page, r.columns, r.plural(), opts)
		},
	}
	addListFlags(cmd, opts)
	if len(r.filters) > 0 {
		readFilters = addFilterFlags(cmd, r.filters, opts)
	}
	return cmd
}

func (a *App) newGetCmd(r *resource) *cobra.Command {
	return &cobra.Command{
		Use:     "get " + r.idArg,
		Aliases: []string{"show"},
		Short:   "Show " + article(r.label) + " " + r.label,
		Args:    exactArgs(r.idArg),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := a.session()
			if err != nil {
				return err
			}
			path, err := a.member(cmd.Context(), s, r, args[0])
			if err != nil {
				return err
			}
			resp, err := s.client.Get(cmd.Context(), path, nil)
			if err != nil {
				return err
			}
			return a.renderRecord(resp.Body, r.unwrapKey)
		},
	}
}

// writeOptions are the flags create and update share.
type writeOptions struct {
	data string
	sets []string
}

func (a *App) addWriteFlags(cmd *cobra.Command, r *resource, opts *writeOptions) {
	flags := cmd.Flags()
	if r.idKey != "" {
		flags.String("id", "", r.idUsage)
	}
	for _, f := range r.fields {
		switch f.kind {
		case kindInt:
			flags.Int64(f.flag, 0, f.usage)
		case kindNumber:
			flags.String(f.flag, "", f.usage+", a number")
		case kindBool:
			flags.Bool(f.flag, false, f.usage)
		case kindStrings:
			flags.StringArray(f.flag, nil, f.usage+" (may be repeated)")
		case kindText:
			flags.String(f.flag, "", f.usage+"; @FILE reads a file")
		case kindDate:
			flags.String(f.flag, "", f.usage+", as YYYY-MM-DD")
		case kindJSON:
			flags.String(f.flag, "", f.usage+", as JSON; @FILE reads a file")
		case kindString:
			flags.String(f.flag, "", f.usage)
		}
	}
	if r.flags != nil {
		r.flags(cmd)
	}
	flags.StringVarP(&opts.data, "data", "d", "", "the "+r.label+" as JSON; @FILE reads a file and - reads standard input")
	flags.StringArrayVar(&opts.sets, "set", nil, "set any field, as key=value; the value is JSON if it parses as JSON, else text (may be repeated)")
	flags.SortFlags = false
}

var dateFormat = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)

// buildBody assembles the record to send from --data, then the field
// flags, then --set, each overriding what came before.
func (a *App) buildBody(cmd *cobra.Command, r *resource, opts *writeOptions) (*output.Record, error) {
	body := output.NewRecord()
	if opts.data != "" {
		parsed, err := a.parseObject("data", opts.data)
		if err != nil {
			return nil, err
		}
		body = prepareRecord(unwrap(parsed, r.singular), r)
	}

	flags := cmd.Flags()
	if r.idKey != "" && flags.Changed("id") {
		value, _ := flags.GetString("id")
		body.Set(r.idKey, value)
	}
	for _, f := range r.fields {
		if !flags.Changed(f.flag) {
			continue
		}
		switch f.kind {
		case kindInt:
			value, _ := flags.GetInt64(f.flag)
			body.Set(f.key, json.Number(strconv.FormatInt(value, 10)))
		case kindNumber:
			value, _ := flags.GetString(f.flag)
			if _, err := strconv.ParseFloat(value, 64); err != nil {
				return nil, usagef("--%s must be a number, not %q", f.flag, value)
			}
			body.Set(f.key, json.Number(value))
		case kindBool:
			value, _ := flags.GetBool(f.flag)
			body.Set(f.key, value)
		case kindStrings:
			values, _ := flags.GetStringArray(f.flag)
			list := make([]any, 0, len(values))
			for _, value := range values {
				if value != "" {
					list = append(list, value)
				}
			}
			body.Set(f.key, list)
		case kindText:
			value, _ := flags.GetString(f.flag)
			if strings.HasPrefix(value, "@") {
				data, err := a.readSource(value)
				if err != nil {
					return nil, usagef("--%s: %s", f.flag, err)
				}
				value = string(data)
			}
			body.Set(f.key, value)
		case kindJSON:
			value, _ := flags.GetString(f.flag)
			parsed, err := a.parseJSON(f.flag, value)
			if err != nil {
				return nil, err
			}
			body.Set(f.key, parsed)
		case kindDate:
			value, _ := flags.GetString(f.flag)
			if value != "" && !dateFormat.MatchString(value) {
				return nil, usagef("--%s must be a date as YYYY-MM-DD, not %q", f.flag, value)
			}
			body.Set(f.key, value)
		case kindString:
			value, _ := flags.GetString(f.flag)
			body.Set(f.key, value)
		}
	}
	if r.apply != nil {
		if err := r.apply(a, cmd, body); err != nil {
			return nil, err
		}
	}
	for _, set := range opts.sets {
		key, value, err := parseAssignment(set)
		if err != nil {
			return nil, err
		}
		body.Set(key, value)
	}
	return body, nil
}

// prepareRecord makes a record that was read from the API fit to send
// back: the fields the server computes are dropped, and a record that
// carries its external ID as "id" has it moved to "external_id".
func prepareRecord(record *output.Record, r *resource) *output.Record {
	prepared := output.NewRecord()
	for _, key := range record.Keys() {
		value, _ := record.Get(key)
		prepared.Set(key, value)
	}
	for _, key := range readOnlyKeys {
		prepared.Delete(key)
	}
	switch r.idKey {
	case "":
		prepared.Delete("id")
	case "external_id":
		if value, ok := prepared.Get("id"); ok {
			if _, has := prepared.Get("external_id"); !has {
				prepared.Set("external_id", value)
			}
			prepared.Delete("id")
		}
	}
	if r.prepare != nil {
		r.prepare(prepared)
	}
	return prepared
}

// wrap puts a record under its wrapper key.
func wrap(r *resource, body *output.Record) *output.Record {
	wrapped := output.NewRecord()
	wrapped.Set(r.singular, body)
	return wrapped
}

func (a *App) newCreateCmd(r *resource) *cobra.Command {
	opts := &writeOptions{}
	cmd := &cobra.Command{
		Use:     "create",
		Aliases: []string{"add", "new"},
		Short:   "Create " + article(r.label) + " " + r.label,
		Example: r.createExample,
		Args:    noArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			body, err := a.buildBody(cmd, r, opts)
			if err != nil {
				return err
			}
			if len(body.Keys()) == 0 {
				return usagef("nothing to create; describe the %s with flags or --data", r.label)
			}
			s, err := a.session()
			if err != nil {
				return err
			}
			collection, err := a.collection(cmd.Context(), s, r)
			if err != nil {
				return err
			}
			resp, err := s.client.Post(cmd.Context(), collection, wrap(r, body))
			if err != nil {
				return err
			}
			fmt.Fprintf(a.Err, "Created %s %s.\n", r.label, idOf(resp.Body, r.unwrapKey))
			return a.renderRecord(resp.Body, r.unwrapKey)
		},
	}
	a.addWriteFlags(cmd, r, opts)
	return cmd
}

func (a *App) newUpdateCmd(r *resource) *cobra.Command {
	opts := &writeOptions{}
	cmd := &cobra.Command{
		Use:     "update " + r.idArg,
		Aliases: []string{"edit", "set"},
		Short:   "Change " + article(r.label) + " " + r.label,
		Long: fmt.Sprintf(`Change a %s. Only the fields you name change.

To remove a value, set it to nothing: an empty string for text, or null
with --set, as in --set notes=null.`, r.label),
		Example: r.updateExample,
		Args:    exactArgs(r.idArg),
		RunE: func(cmd *cobra.Command, args []string) error {
			body, err := a.buildBody(cmd, r, opts)
			if err != nil {
				return err
			}
			if len(body.Keys()) == 0 {
				return usagef("nothing to change; name the fields to change with flags or --data")
			}
			s, err := a.session()
			if err != nil {
				return err
			}
			path, err := a.member(cmd.Context(), s, r, args[0])
			if err != nil {
				return err
			}
			resp, err := s.client.Patch(cmd.Context(), path, wrap(r, body))
			if err != nil {
				return err
			}
			fmt.Fprintf(a.Err, "Updated %s %s.\n", r.label, idOf(resp.Body, r.unwrapKey))
			return a.renderRecord(resp.Body, r.unwrapKey)
		},
	}
	a.addWriteFlags(cmd, r, opts)
	return cmd
}

func (a *App) newDeleteCmd(r *resource) *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:     "delete " + r.idArg + "...",
		Aliases: []string{"rm", "remove"},
		Short:   "Delete " + article(r.label) + " " + r.label,
		Long: fmt.Sprintf(`Delete one or more %s. This cannot be undone.

You are asked to confirm first. Pass --yes to skip the question, which a
script must do.`, r.plural()),
		Args: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return usagef("missing %s", r.idArg)
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			what := fmt.Sprintf("%s %s", r.label, args[0])
			if len(args) > 1 {
				what = fmt.Sprintf("%d %s (%s)", len(args), r.plural(), strings.Join(args, ", "))
			}
			if err := a.confirmDestructive(yes, "delete", fmt.Sprintf("Delete %s? This cannot be undone.", what), "Nothing was deleted."); err != nil {
				return err
			}
			s, err := a.session()
			if err != nil {
				return err
			}
			for _, id := range args {
				path, err := a.member(cmd.Context(), s, r, id)
				if err != nil {
					return err
				}
				if _, err := s.client.Delete(cmd.Context(), path); err != nil {
					return err
				}
				fmt.Fprintf(a.Err, "Deleted %s %s.\n", r.label, id)
			}
			return nil
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "delete without asking")
	return cmd
}

// confirmDestructive asks before something that cannot be undone, unless
// yes was passed.  Away from a terminal, nothing is asked and nothing is
// done.
func (a *App) confirmDestructive(yes bool, verb, question, refused string) error {
	if yes {
		return nil
	}
	if !a.Interactive {
		return usagef("refusing to %s without being asked to; pass --yes", verb)
	}
	ok, err := a.confirm(question)
	if err != nil {
		return err
	}
	if !ok {
		fmt.Fprintln(a.Err, refused)
		return &errReported{code: ExitError}
	}
	return nil
}

// article is "a" or "an", as the word that follows wants.
func article(word string) string {
	if strings.ContainsAny(word[:1], "aeiouAEIOU") {
		return "an"
	}
	return "a"
}

// idOf returns the id of a record for a message, or "" if it has none.
func idOf(raw []byte, unwrapKey string) string {
	record, err := output.ParseRecord(raw)
	if err != nil {
		return ""
	}
	if unwrapKey != "" {
		if inner, ok := record.Value(unwrapKey).(*output.Record); ok {
			record = inner
		}
	}
	if id := record.String("id"); id != "" {
		return id
	}
	return record.String("external_id")
}

// timestamp shortens a timestamp to the minute for a table.
func timestamp(key string) func(*output.Record) string {
	return func(r *output.Record) string {
		value := r.String(key)
		if len(value) >= 16 && value[10] == 'T' {
			return value[:10] + " " + value[11:16]
		}
		return value
	}
}

// nested returns the value of key inside the object under parent.
func nested(parent, key string) func(*output.Record) string {
	return func(r *output.Record) string {
		if inner, ok := r.Value(parent).(*output.Record); ok {
			return inner.String(key)
		}
		return ""
	}
}

// yesNo shows a boolean as yes, no or nothing.
func yesNo(key string) func(*output.Record) string {
	return func(r *output.Record) string {
		switch r.String(key) {
		case "true":
			return "yes"
		case "false":
			return "no"
		}
		return ""
	}
}
