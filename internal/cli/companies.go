package cli

import (
	"fmt"

	"github.com/mdchaney/streamingchasers-api/internal/api"
	"github.com/mdchaney/streamingchasers-api/internal/output"
	"github.com/spf13/cobra"
)

func companiesResource() *resource {
	return &resource{
		name:        "companies",
		aliases:     []string{"company"},
		singular:    "company",
		label:       "company",
		pluralLabel: "companies",
		segment:     "companies",
		short:       "See your companies and choose the one to work in",
		long: `See the companies you own or collaborate on, and choose the one to work
in. Almost everything else happens in a company.

Your role in a company is what you may do there: a viewer can read,
an editor can change the catalog and load files, an admin can also manage
CWR connections, and the owner can also change the company's settings and
hand it to another collaborator.

Companies are made, and collaborators invited, on the web site. With a
browser sign-in, changing a company's settings or ownership takes
'streamingchasers auth login --admin'.`,
		scope:      scopeGlobal,
		idArg:      "ID_OR_NAME",
		byName:     true,
		noGet:      true,
		noCreate:   true,
		noDelete:   true,
		adminScope: true,
		fields: []field{
			{flag: "name", key: "name", usage: "name of the company"},
			{flag: "description", key: "description", kind: kindText, usage: "what the company is"},
		},
		updateExample: `  streamingchasers companies update "Frivolous Music" --description "Production music"`,
		columns: []output.Column{
			{Header: "ID", Key: "id"},
			{Header: "NAME", Key: "name", Max: 40},
			{Header: "OWNER", Key: "owner_name", Max: 30},
			{Header: "DESCRIPTION", Key: "description", Max: 50},
		},
	}
}

func (a *App) newCompaniesCmd() *cobra.Command {
	r := companiesResource()
	cmd := &cobra.Command{
		Use:     r.name,
		Aliases: r.aliases,
		Short:   r.short,
		Long:    r.long,
		Args:    subcommandsOnly,
		RunE:    func(cmd *cobra.Command, args []string) error { return cmd.Help() },
	}

	get := &cobra.Command{
		Use:     "get [ID_OR_NAME]",
		Aliases: []string{"show"},
		Short:   "Show a company; without an argument, the one you are working in",
		Args:    cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := a.session()
			if err != nil {
				return err
			}
			var id int64
			if len(args) == 1 {
				id, err = s.findCompany(cmd.Context(), args[0])
			} else {
				id, err = s.companyID(cmd.Context())
			}
			if err != nil {
				return err
			}
			resp, err := s.client.Get(cmd.Context(), api.Path("companies", id), nil)
			if err != nil {
				return err
			}
			return a.renderRecord(resp.Body, "")
		},
	}

	use := &cobra.Command{
		Use:   "use ID_OR_NAME",
		Short: "Choose the company that commands work in",
		Long: `Choose the company that commands work in when --company is not given.

The choice is kept for the server you are signed in to.`,
		Example: `  streamingchasers companies use 2
  streamingchasers companies use "Frivolous Music"`,
		Args: exactArgs("ID_OR_NAME"),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := a.session()
			if err != nil {
				return err
			}
			id, err := s.findCompany(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			// Asking for the company checks that it exists and that the
			// user may see it.
			resp, err := s.client.Get(cmd.Context(), api.Path("companies", id), nil)
			if err != nil {
				return err
			}
			record, err := output.ParseRecord(resp.Body)
			if err != nil {
				return err
			}
			s.cfg.Host(s.host).DefaultCompany = id
			if err := s.store.SaveConfig(s.cfg); err != nil {
				return err
			}
			fmt.Fprintf(a.Err, "Now working in company %d, %s.\n", id, record.String("name"))
			return nil
		},
	}

	var newOwner int64
	var yes bool
	transfer := &cobra.Command{
		Use:   "transfer-ownership [ID_OR_NAME] --to USER_ID",
		Short: "Hand a company to another collaborator",
		Long: `Hand a company you own to another of its collaborators, by their user ID.
You become an admin of it. This cannot be undone from here.

Without an argument, the company you are working in.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if newOwner <= 0 {
				return usagef("--to must name the new owner's user ID")
			}
			s, err := a.session()
			if err != nil {
				return err
			}
			var id int64
			if len(args) == 1 {
				id, err = s.findCompany(cmd.Context(), args[0])
			} else {
				id, err = s.companyID(cmd.Context())
			}
			if err != nil {
				return err
			}
			if err := a.confirmDestructive(yes, "transfer ownership", fmt.Sprintf("Make user %d the owner of company %d? You will become an admin.", newOwner, id), "Nothing was changed."); err != nil {
				return err
			}
			body := map[string]any{"new_owner_id": newOwner}
			resp, err := s.client.Post(cmd.Context(), api.Path("companies", id, "transfer_ownership"), body)
			if err != nil {
				return err
			}
			fmt.Fprintf(a.Err, "Company %d now belongs to user %d.\n", id, newOwner)
			return a.renderRecord(resp.Body, "")
		},
	}
	transfer.Flags().Int64Var(&newOwner, "to", 0, "user ID of the new owner, a collaborator of the company")
	transfer.Flags().BoolVarP(&yes, "yes", "y", false, "transfer without asking")

	cmd.AddCommand(a.newListCmd(r), get, a.newUpdateCmd(r), use, transfer)
	return cmd
}
