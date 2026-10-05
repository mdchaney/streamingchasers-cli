package cli

import (
	"fmt"
	"strconv"

	"github.com/mdchaney/streamingchasers-cli/internal/config"
	"github.com/spf13/cobra"
)

const configLong = `See and change the CLI's settings.

  host     the server commands talk to when --host is not given
  company  the company commands work in when --company is not given,
           kept for each server

Settings are kept in config.json and credentials in credentials.json, in
the directory 'streamingchasers config path' prints.`

func (a *App) newConfigCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "See and change the CLI's settings",
		Long:  configLong,
		Args:  subcommandsOnly,
		RunE:  func(cmd *cobra.Command, args []string) error { return cmd.Help() },
	}

	// load reads the settings and works out the host in use.
	load := func() (*config.Store, *config.Config, string, error) {
		store, err := a.store()
		if err != nil {
			return nil, nil, "", err
		}
		cfg, err := store.LoadConfig()
		if err != nil {
			return nil, nil, "", err
		}
		host, err := a.resolveHost(cfg)
		return store, cfg, host, err
	}
	company := func(cfg *config.Config, host string) string {
		if settings, ok := cfg.Hosts[host]; ok && settings.DefaultCompany != 0 {
			return strconv.FormatInt(settings.DefaultCompany, 10)
		}
		return ""
	}

	list := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "Show the settings",
		Args:    noArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			_, cfg, host, err := load()
			if err != nil {
				return err
			}
			fmt.Fprintf(a.Out, "host     %s\n", host)
			fmt.Fprintf(a.Out, "company  %s\n", company(cfg, host))
			return nil
		},
	}

	get := &cobra.Command{
		Use:   "get KEY",
		Short: "Show a setting",
		Args:  exactArgs("KEY"),
		RunE: func(cmd *cobra.Command, args []string) error {
			_, cfg, host, err := load()
			if err != nil {
				return err
			}
			switch args[0] {
			case "host":
				fmt.Fprintln(a.Out, host)
			case "company":
				fmt.Fprintln(a.Out, company(cfg, host))
			default:
				return usagef("there is no setting called %q; the settings are host and company", args[0])
			}
			return nil
		},
	}

	set := &cobra.Command{
		Use:   "set KEY VALUE",
		Short: "Change a setting",
		Example: `  streamingchasers config set host staging.example.com
  streamingchasers config set company 2`,
		Args: exactArgs("KEY", "VALUE"),
		RunE: func(cmd *cobra.Command, args []string) error {
			store, cfg, host, err := load()
			if err != nil {
				return err
			}
			switch args[0] {
			case "host":
				normalized, err := config.NormalizeHost(args[1])
				if err != nil {
					return usagef("%s", err)
				}
				cfg.DefaultHost = normalized
				fmt.Fprintf(a.Err, "The host is now %s.\n", normalized)
			case "company":
				// The company is not looked up: a setting can be made
				// before signing in.  'libraries use' checks it.
				id, err := strconv.ParseInt(args[1], 10, 64)
				if err != nil || id < 1 {
					return usagef("the company is set by its ID, a number; 'streamingchasers companies use' takes a name")
				}
				cfg.Host(host).DefaultCompany = id
				fmt.Fprintf(a.Err, "The company for %s is now %d.\n", host, id)
			default:
				return usagef("there is no setting called %q; the settings are host and company", args[0])
			}
			return store.SaveConfig(cfg)
		},
	}

	unset := &cobra.Command{
		Use:   "unset KEY",
		Short: "Put a setting back to its default",
		Args:  exactArgs("KEY"),
		RunE: func(cmd *cobra.Command, args []string) error {
			store, cfg, host, err := load()
			if err != nil {
				return err
			}
			switch args[0] {
			case "host":
				cfg.DefaultHost = ""
			case "company":
				if settings, ok := cfg.Hosts[host]; ok {
					settings.DefaultCompany = 0
				}
			default:
				return usagef("there is no setting called %q; the settings are host and company", args[0])
			}
			return store.SaveConfig(cfg)
		},
	}

	path := &cobra.Command{
		Use:   "path",
		Short: "Show where the settings and credentials are kept",
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			store, err := a.store()
			if err != nil {
				return err
			}
			_, err = fmt.Fprintln(a.Out, store.Dir)
			return err
		},
	}

	cmd.AddCommand(list, get, set, unset, path)
	return cmd
}
