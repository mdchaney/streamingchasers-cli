package cli

import (
	"net/url"

	"github.com/mdchaney/streamingchasers-api/internal/api"
	"github.com/spf13/cobra"
)

func escapeSegment(s string) string { return url.PathEscape(s) }

func (a *App) newProsCmd() *cobra.Command {
	return a.newResourceCmd(prosResource())
}

// newReferenceCmds builds the commands for the read-only reference data.
func (a *App) newReferenceCmds() []*cobra.Command {
	var cmds []*cobra.Command
	for _, r := range referenceResources() {
		cmds = append(cmds, a.newResourceCmd(r))
	}
	for _, r := range productionResources() {
		cmd := a.newResourceCmd(r)
		cmd.AddCommand(a.newProductionSearchCmd(r, api.Path(r.segment, "search"), "TITLE"))
		if r.name == "series" {
			episodes := group("episodes SERIES_ID", "List or search a series' episodes", "", nil...)
			episodes.Args = cobra.MinimumNArgs(1)
			episodes.RunE = func(cmd *cobra.Command, args []string) error {
				s, err := a.session()
				if err != nil {
					return err
				}
				path := api.Path("series", args[0], "episodes")
				query := url.Values{}
				if len(args) > 1 {
					path += "/search"
					query.Set("q", args[1])
				}
				page, err := s.client.List(cmd.Context(), path, "episodes", api.ListOptions{Query: query})
				if err != nil {
					return err
				}
				return a.renderList(page, productionResources()[2].columns, "episodes", &listOptions{all: true})
			}
			episodes.Use = "episodes SERIES_ID [TITLE]"
			episodes.Short = "List a series' episodes, or search them by title"
			cmd.AddCommand(episodes)
		}
		cmds = append(cmds, cmd)
	}
	return cmds
}

// newProductionSearchCmd searches movies, series or episodes by title.
func (a *App) newProductionSearchCmd(r *resource, path, arg string) *cobra.Command {
	opts := &listOptions{}
	cmd := &cobra.Command{
		Use:   "search " + arg,
		Short: "Search " + r.plural() + " by title",
		Args:  exactArgs(arg),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := opts.validate(cmd); err != nil {
				return err
			}
			s, err := a.session()
			if err != nil {
				return err
			}
			opts.extra = url.Values{"q": {args[0]}}
			page, err := a.fetchList(cmd.Context(), s, path, r.segment, opts)
			if err != nil {
				return err
			}
			return a.renderList(page, r.columns, r.plural(), opts)
		},
	}
	addListFlags(cmd, opts)
	return cmd
}
