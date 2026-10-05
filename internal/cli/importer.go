package cli

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/mdchaney/streamingchasers-cli/internal/api"
	"github.com/mdchaney/streamingchasers-cli/internal/output"
	"github.com/spf13/cobra"
)

// Import modes.
const (
	modeUpsert = "upsert"
	modeCreate = "create"
	modeUpdate = "update"
)

type importOptions struct {
	mode     string
	failFast bool
	dryRun   bool
}

// importResult is what became of one record.
type importResult struct {
	index int
	id    string
	err   error
}

func (a *App) newImportCmd(r *resource) *cobra.Command {
	opts := &importOptions{}
	cmd := &cobra.Command{
		Use:   "import FILE",
		Short: "Load " + r.plural() + " from a file",
		Long: fmt.Sprintf(`Load %[1]s from a file of JSON: either a list of records or one record
after another (JSON Lines). FILE may be - for standard input.

Records have the fields the API documents, and what 'streamingchasers %[2]s
list --output jsonl' writes can be read back in. Each record needs its
external ID, as "external_id" (or "id").

By default a record that exists is updated and one that does not is
created (--mode upsert). --mode create makes only new records and reports
one that already exists as a failure; --mode update changes only records
that exist.

Records go one to a request. A record that fails does not stop the rest:
the failures are listed at the end and the exit code is 1. Loading the
file again is safe.

For a whole catalog in CSV, see 'streamingchasers works-uploads create',
which the server processes in the background.`, r.plural(), r.name),
		Example: fmt.Sprintf(`  streamingchasers %[1]s import %[1]s.json
  streamingchasers %[1]s import --mode create %[1]s.jsonl
  streamingchasers %[1]s list --all -o jsonl --company 2 | streamingchasers %[1]s import - --company 5`, r.name),
		Args: exactArgs("FILE"),
		RunE: func(cmd *cobra.Command, args []string) error {
			switch opts.mode {
			case modeUpsert, modeCreate, modeUpdate:
			default:
				return usagef("--mode must be upsert, create or update, not %q", opts.mode)
			}

			data, err := a.readFile(args[0])
			if err != nil {
				return err
			}
			records, err := readRecords(data)
			if err != nil {
				return err
			}
			if len(records) == 0 {
				return fmt.Errorf("the input has no records")
			}

			// Every record is checked before any is sent, so that a file
			// with a mistake in it changes nothing.
			ids := make([]string, len(records))
			seen := map[string]int{}
			var problems []string
			for i, record := range records {
				records[i] = prepareRecord(unwrap(record, r.singular), r)
				ids[i] = records[i].String(r.idKey)
				switch {
				case ids[i] == "":
					problems = append(problems, fmt.Sprintf("record %d has no %s", i+1, r.idKey))
				case seen[ids[i]] > 0:
					problems = append(problems, fmt.Sprintf("record %d has the same %s (%s) as record %d", i+1, r.idKey, ids[i], seen[ids[i]]))
				default:
					seen[ids[i]] = i + 1
				}
			}
			if len(problems) > 0 {
				const most = 10
				if len(problems) > most {
					problems = append(problems[:most], fmt.Sprintf("and %d more", len(problems)-most))
				}
				return fmt.Errorf("the input cannot be loaded:\n  %s", strings.Join(problems, "\n  "))
			}
			if opts.dryRun {
				fmt.Fprintf(a.Err, "%d %s are ready to load. Nothing was sent.\n", len(records), r.plural())
				return nil
			}

			s, err := a.session()
			if err != nil {
				return err
			}
			collection, err := a.collection(cmd.Context(), s, r)
			if err != nil {
				return err
			}
			return a.runImport(cmd.Context(), s, r, collection, records, ids, opts)
		},
	}
	cmd.Flags().StringVar(&opts.mode, "mode", modeUpsert, "what to do with each record: upsert, create or update")
	cmd.Flags().BoolVar(&opts.failFast, "fail-fast", false, "stop at the first record that fails")
	cmd.Flags().BoolVar(&opts.dryRun, "dry-run", false, "check the file and send nothing")
	return cmd
}

func (a *App) runImport(ctx context.Context, s *session, r *resource, collection string, records []*output.Record, ids []string, opts *importOptions) error {
	created, updated, done := 0, 0, 0
	var failures []importResult
	var fatal error
	for index := range records {
		if ctx.Err() != nil {
			break
		}
		wasCreated, err := a.importRecord(ctx, s, r, collection, records[index], ids[index], opts.mode)
		if errors.Is(err, context.Canceled) {
			break
		}
		done++
		switch {
		case err == nil && wasCreated:
			created++
		case err == nil:
			updated++
		default:
			failures = append(failures, importResult{index: index, id: ids[index], err: err})
			// Credentials that are refused will be refused for every
			// record, as will a role that may not write.
			if status := api.StatusOf(err); status == 401 || status == 403 || opts.failFast || isNotSignedIn(err) {
				fatal = err
			}
		}
		if a.Progress {
			fmt.Fprintf(a.Err, "\r%d of %d", done, len(records))
		}
		if fatal != nil {
			break
		}
	}
	if a.Progress {
		fmt.Fprint(a.Err, "\r\033[K")
	}

	if fatal != nil && !opts.failFast {
		fmt.Fprintf(a.Err, "Stopped after %d of %d %s: %d created, %d updated.\n", done-len(failures), len(records), r.plural(), created, updated)
		return fatal
	}

	const most = 50
	for i, failure := range failures {
		if i == most {
			fmt.Fprintf(a.Err, "... and %d more failures\n", len(failures)-most)
			break
		}
		fmt.Fprintf(a.Err, "record %d (%s %s): %s\n", failure.index+1, r.label, failure.id, describeFailure(failure.err))
	}

	summary := fmt.Sprintf("%d created, %d updated, %d failed", created, updated, len(failures))
	if remaining := len(records) - done; remaining > 0 {
		summary += fmt.Sprintf(", %d not attempted", remaining)
	}
	fmt.Fprintf(a.Err, "Loaded %d of %d %s: %s.\n", created+updated, len(records), r.plural(), summary)

	// Interrupting an import is reported as an interruption.
	if fatal == nil && ctx.Err() != nil && len(records) > done {
		return context.Canceled
	}
	if len(failures) > 0 {
		return &errReported{code: ExitError}
	}
	return nil
}

func isNotSignedIn(err error) bool {
	var notSignedIn *errNotSignedIn
	return errors.As(err, &notSignedIn)
}

// importRecord sends one record and reports whether it was created.
func (a *App) importRecord(ctx context.Context, s *session, r *resource, collection string, record *output.Record, id, mode string) (bool, error) {
	member := collection + "/" + escapeSegment(id)
	body := wrap(r, record)

	if mode == modeCreate {
		_, err := s.client.Post(ctx, collection, body)
		return true, err
	}
	resp, err := s.client.Patch(ctx, member, body)
	if err == nil {
		return resp.StatusCode == http.StatusCreated, nil
	}
	if mode == modeUpdate || !api.IsNotFound(err) {
		return false, err
	}
	// Not there to update, so it is created.  If the company itself is
	// what was not found, this fails the same way.
	_, err = s.client.Post(ctx, collection, body)
	return true, err
}

// describeFailure puts a failure on one line.
func describeFailure(err error) string {
	var apiErr *api.Error
	if errors.As(err, &apiErr) && len(apiErr.Problems) > 0 {
		return strings.Join(apiErr.Problems, "; ")
	}
	return strings.ReplaceAll(err.Error(), "\n", "; ")
}
