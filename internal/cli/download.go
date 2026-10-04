package cli

import (
	"fmt"
	"os"

	"github.com/mdchaney/streamingchasers-api/internal/api"
	"github.com/spf13/cobra"
)

// saveOptions say where a downloaded file goes: to a named file, to a
// file named by the server, or to standard output.
type saveOptions struct {
	saveAs string
	save   bool
}

func addSaveFlags(cmd *cobra.Command, opts *saveOptions) {
	cmd.Flags().StringVar(&opts.saveAs, "save-as", "", "write the file here instead of to standard output")
	cmd.Flags().BoolVar(&opts.save, "save", false, "write the file under the name the server gives it, in the current directory")
}

// deliver writes a downloaded file where opts say.  fallback names the
// file when the server does not.
func (a *App) deliver(resp *api.Response, opts *saveOptions, fallback string) error {
	name := opts.saveAs
	if name == "" && opts.save {
		name = resp.Filename()
		if name == "" || name == "." {
			name = fallback
		}
	}
	if name == "" {
		_, err := a.Out.Write(resp.Body)
		return err
	}
	if err := os.WriteFile(name, resp.Body, 0o644); err != nil {
		return err
	}
	fmt.Fprintf(a.Err, "Saved %s (%d bytes).\n", name, len(resp.Body))
	return nil
}

// newDownloadCmd makes a command that downloads a member file: the CSV
// an upload was made from, say.  pathOf gives the file's path from the
// record's argument.
func (a *App) newDownloadCmd(use, short, long, idArg string, pathOf func(s *session, cmd *cobra.Command, id string) (string, error), fallback func(id string) string) *cobra.Command {
	opts := &saveOptions{}
	cmd := &cobra.Command{
		Use:   use,
		Short: short,
		Long:  long,
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			id := ""
			if len(args) > 0 {
				id = args[0]
			}
			s, err := a.session()
			if err != nil {
				return err
			}
			path, err := pathOf(s, cmd, id)
			if err != nil {
				return err
			}
			resp, err := s.client.Download(cmd.Context(), path, nil)
			if err != nil {
				return err
			}
			return a.deliver(resp, opts, fallback(id))
		},
	}
	if idArg != "" {
		cmd.Args = exactArgs(idArg)
	}
	addSaveFlags(cmd, opts)
	return cmd
}
