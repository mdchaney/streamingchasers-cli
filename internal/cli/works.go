package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/mdchaney/streamingchasers-cli/internal/api"
	"github.com/mdchaney/streamingchasers-cli/internal/output"
	"github.com/spf13/cobra"
)

func (a *App) newWorksCmd() *cobra.Command {
	return a.newResourceCmd(worksResource())
}

// newPublishersCmd is the publishers command: the usual ones, and the
// alternative names and the sheet import.
func (a *App) newPublishersCmd() *cobra.Command {
	r := publishersResource()
	cmd := a.newResourceCmd(r)

	altNames := group("alt-names", "Add and remove a publisher's alternative names", `Add and remove the alternative names a publisher is known by on royalty
statements. 'streamingchasers publishers get EXTERNAL_ID' lists them, with
their IDs.`)
	var name string
	add := &cobra.Command{
		Use:   "add EXTERNAL_ID --name NAME",
		Short: "Add an alternative name",
		Args:  exactArgs("EXTERNAL_ID"),
		RunE: func(cmd *cobra.Command, args []string) error {
			if name == "" {
				return usagef("--name is required")
			}
			s, err := a.session()
			if err != nil {
				return err
			}
			path, err := s.companyPath(cmd.Context(), "publishers", args[0], "alt_names")
			if err != nil {
				return err
			}
			body := map[string]any{"publishers_alt_name": map[string]any{"name": name}}
			resp, err := s.client.Post(cmd.Context(), path, body)
			if err != nil {
				return err
			}
			fmt.Fprintf(a.Err, "Added %q to publisher %s.\n", name, args[0])
			return a.renderRecord(resp.Body, "")
		},
	}
	add.Flags().StringVar(&name, "name", "", "the alternative name")

	var yes bool
	remove := &cobra.Command{
		Use:     "remove EXTERNAL_ID ALT_NAME_ID",
		Aliases: []string{"delete", "rm"},
		Short:   "Remove an alternative name",
		Args:    exactArgs("EXTERNAL_ID", "ALT_NAME_ID"),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := a.confirmDestructive(yes, "remove", fmt.Sprintf("Remove alternative name %s from publisher %s?", args[1], args[0]), "Nothing was removed."); err != nil {
				return err
			}
			s, err := a.session()
			if err != nil {
				return err
			}
			path, err := s.companyPath(cmd.Context(), "publishers", args[0], "alt_names", args[1])
			if err != nil {
				return err
			}
			if _, err := s.client.Delete(cmd.Context(), path); err != nil {
				return err
			}
			fmt.Fprintf(a.Err, "Removed alternative name %s from publisher %s.\n", args[1], args[0])
			return nil
		},
	}
	remove.Flags().BoolVarP(&yes, "yes", "y", false, "remove without asking")
	altNames.AddCommand(add, remove)

	sheet := &cobra.Command{
		Use:   "import-sheet FILE",
		Short: "Import a publisher sheet, a CSV with one publisher to a row",
		Long: `Import a publisher sheet: a CSV with one publisher to a row (name, PRO,
IPI, controlled), each optionally with a sub-publisher, which gets a
subpublishing agreement made for it.

Rows stand or fall alone. The server's report of what it did, with any
rows it could not take, is printed; when any row failed the exit code is 6.`,
		Args: exactArgs("FILE"),
		RunE: func(cmd *cobra.Command, args []string) error {
			data, err := a.readFile(args[0])
			if err != nil {
				return err
			}
			s, err := a.session()
			if err != nil {
				return err
			}
			path, err := s.companyPath(cmd.Context(), "publishers", "process_sheet")
			if err != nil {
				return err
			}
			form := &api.Form{FileField: "csv_file", Filename: filepath.Base(args[0]), File: data}
			resp, err := s.client.Do(cmd.Context(), api.Request{Method: "POST", Path: path, Form: form})
			if err != nil {
				// The report comes back with 422 when any row failed; it
				// is still the report.
				var apiErr *api.Error
				if errors.As(err, &apiErr) && apiErr.StatusCode == 422 && apiErr.Message == "" && len(apiErr.Body) > 0 {
					fmt.Fprintln(a.Err, "Some rows could not be imported.")
					_ = output.WriteJSON(a.Out, apiErr.Body)
					return &errReported{code: ExitInvalid}
				}
				return err
			}
			fmt.Fprintln(a.Err, "The sheet was imported.")
			return output.WriteJSON(a.Out, resp.Body)
		},
	}

	cmd.AddCommand(altNames, sheet)
	return cmd
}

// readFileArg reads a file to upload, which cannot be standard input:
// the server wants a name for it.
func readFileArg(name string) ([]byte, string, error) {
	if name == "" {
		return nil, "", usagef("--file is required")
	}
	data, err := os.ReadFile(name)
	if err != nil {
		return nil, "", err
	}
	return data, filepath.Base(name), nil
}
