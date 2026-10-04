package cli

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/mdchaney/streamingchasers-api/internal/output"
	"github.com/spf13/cobra"
)

func writersResource() *resource {
	return &resource{
		name:     "writers",
		aliases:  []string{"writer"},
		singular: "writer",
		label:    "writer",
		segment:  "writers",
		short:    "Manage a company's writers",
		long: `Manage a company's writers: the composers and authors credited on works.

Writers are addressed by their external IDs, which you choose when you
create them: letters, numbers, hyphens and underscores.`,
		scope:      scopeCompany,
		idArg:      "EXTERNAL_ID",
		idKey:      "external_id",
		idUsage:    "your ID for the writer",
		importable: true,
		columns: []output.Column{
			{Header: "ID", Key: "id"},
			{Header: "NAME", Key: "full_name", Max: 40},
			{Header: "PRO", Key: "pro_name", Max: 30},
			{Header: "CONTROLLED", Value: yesNo("controlled")},
		},
		fields: []field{
			{flag: "first-name", key: "first_name", usage: "first name"},
			{flag: "last-name", key: "last_name", usage: "last name (required)"},
			{flag: "pseudonym", key: "pseudonym", usage: "pseudonym"},
			{flag: "pro", key: "pro_id", kind: kindInt, usage: "ID of the writer's PRO (see 'streamingchasers pros list')"},
			{flag: "ipi-name", key: "ipi_name_number", kind: kindInt, usage: "IPI name number"},
			{flag: "ipi-base", key: "ipi_base_number", usage: "IPI base number, as A-999999999-9"},
			{flag: "controlled", key: "controlled", kind: kindBool, usage: "the writer is controlled by the company"},
		},
		filters: []filter{
			{flag: "name", param: "q[name]", usage: "list the writers with this in their name"},
			{flag: "external-id", param: "q[external_id]", usage: "list the writer with this external ID"},
		},
		createExample: `  streamingchasers writers create --id W-125 --first-name Johann --last-name Bach --pro 10
  streamingchasers writers create --data @writer.json`,
		updateExample: `  streamingchasers writers update W-125 --ipi-name 116 --controlled`,
	}
}

func publishersResource() *resource {
	return &resource{
		name:     "publishers",
		aliases:  []string{"publisher"},
		singular: "publisher",
		label:    "publisher",
		segment:  "publishers",
		short:    "Manage a company's publishers",
		long: `Manage a company's publishers: its own, and the sub-publishers and
administrators it has agreements with.

Publishers are addressed by their external IDs, which you choose when you
create them. A publisher needs a PRO. Creating a controlled publisher, or a
non-controlled one, also makes the agreements the server derives from it.`,
		scope:      scopeCompany,
		idArg:      "EXTERNAL_ID",
		idKey:      "external_id",
		idUsage:    "your ID for the publisher",
		importable: true,
		columns: []output.Column{
			{Header: "ID", Key: "id"},
			{Header: "LEGAL NAME", Key: "legal_name", Max: 40},
			{Header: "TRADE NAME", Key: "trade_name", Max: 30},
			{Header: "PRO", Key: "pro_name", Max: 30},
			{Header: "CONTACT", Key: "contact_name", Max: 25},
			{Header: "TERRITORIES", Key: "territories", Max: 30},
		},
		fields: []field{
			{flag: "legal-name", key: "legal_name", usage: "legal name (required)"},
			{flag: "trade-name", key: "trade_name", usage: "trade name"},
			{flag: "pro", key: "pro_id", kind: kindInt, usage: "ID of the publisher's PRO (required; see 'streamingchasers pros list')"},
			{flag: "mech-pro", key: "mech_pro_id", kind: kindInt, usage: "ID of the PRO that collects its mechanicals, when that is another"},
			{flag: "ipi-name", key: "ipi_name", usage: "IPI name number"},
			{flag: "ipi-base", key: "ipi_base_number", usage: "IPI base number, as A-999999999-9"},
			{flag: "contact-name", key: "contact_name", usage: "contact's name"},
			{flag: "contact-email", key: "contact_email", usage: "contact's email address"},
			{flag: "controlled", key: "controlled", kind: kindBool, usage: "the publisher is controlled by the company"},
			{flag: "active", key: "active", kind: kindBool, usage: "the publisher is active"},
			{flag: "notes", key: "notes", kind: kindText, usage: "notes"},
			{flag: "territory", key: "tis_territory_ids", kind: kindStrings, usage: "ID of a TIS territory the publisher covers; the list replaces the publisher's"},
		},
		filters: []filter{
			{flag: "name", param: "q[name]", usage: "list the publishers with this in their name"},
			{flag: "external-id", param: "q[external_id]", usage: "list the publisher with this external ID"},
		},
		createExample: `  streamingchasers publishers create --id P-12 --legal-name "Mike's Awesome Music" --pro 21 --controlled
  streamingchasers publishers create --data @publisher.json`,
		updateExample: `  streamingchasers publishers update P-12 --contact-email mike@example.com`,
	}
}

func catalogsResource() *resource {
	return &resource{
		name:      "catalogs",
		aliases:   []string{"catalog"},
		singular:  "catalog",
		label:     "catalog",
		segment:   "catalogs",
		unwrapKey: "catalog",
		short:     "Manage a company's catalogs",
		long: `Manage a company's catalogs: named groups of works.

Catalogs are addressed by their external IDs.`,
		scope:   scopeCompany,
		idArg:   "EXTERNAL_ID",
		idKey:   "external_id",
		idUsage: "your ID for the catalog",
		columns: []output.Column{
			{Header: "ID", Key: "external_id"},
			{Header: "NAME", Key: "name", Max: 50},
			{Header: "WORKS", Key: "works_count"},
			{Header: "CREATED", Value: timestamp("created_at")},
		},
		fields: []field{
			{flag: "name", key: "name", usage: "name of the catalog (required)"},
		},
		createExample: `  streamingchasers catalogs create --id C-1 --name "Film scores"`,
		updateExample: `  streamingchasers catalogs update C-1 --name "Film and television scores"`,
	}
}

func worksResource() *resource {
	return &resource{
		name:     "works",
		aliases:  []string{"work"},
		singular: "work",
		label:    "work",
		segment:  "works",
		short:    "Manage a company's works",
		long: `Manage a company's works: the compositions, with their alternative titles,
registration codes, writers and publishers.

Works are addressed by their external IDs, which you choose when you
create them: letters, numbers, hyphens and underscores.

Alternative titles and registration codes can be given with flags:

  --alt-title TITLE_TYPE_ID:TITLE     'streamingchasers title-types list' shows the types
  --code REGISTRATION_TYPE_ID:CODE    'streamingchasers registration-types list' shows them

On an update they are added to what the work has. Writers and publishers
are set with --data or --set, as works_writers_attributes and
works_publishers_attributes, with the fields the API documents.

To load a whole catalog from CSV, see 'streamingchasers works-uploads'.`,
		scope:      scopeCompany,
		idArg:      "EXTERNAL_ID",
		idKey:      "external_id",
		idUsage:    "your ID for the work",
		importable: true,
		columns: []output.Column{
			{Header: "ID", Key: "id"},
			{Header: "TITLE", Key: "title", Max: 60},
			{Header: "LANGUAGE", Key: "language", Max: 20},
		},
		fields: []field{
			{flag: "title", key: "title", usage: "title of the work (required)"},
			{flag: "language", key: "cis_language_id", kind: kindInt, usage: "ID of the work's language (see 'streamingchasers cis-languages list')"},
		},
		filters: []filter{
			{flag: "title", param: "q[title]", usage: "list the works with this in their title or an alternative title"},
			{flag: "external-id", param: "q[external_id]", usage: "list the work with this external ID"},
		},
		flags: func(cmd *cobra.Command) {
			cmd.Flags().StringArray("alt-title", nil, "an alternative title, as TITLE_TYPE_ID:TITLE (may be repeated)")
			cmd.Flags().StringArray("code", nil, "a registration code, as REGISTRATION_TYPE_ID:CODE (may be repeated)")
		},
		apply: func(a *App, cmd *cobra.Command, body *output.Record) error {
			if cmd.Flags().Changed("alt-title") {
				values, _ := cmd.Flags().GetStringArray("alt-title")
				list, err := parsePairs("alt-title", values, "title_type_id", "title")
				if err != nil {
					return err
				}
				body.Set("works_alt_titles_attributes", list)
			}
			if cmd.Flags().Changed("code") {
				values, _ := cmd.Flags().GetStringArray("code")
				list, err := parsePairs("code", values, "registration_type_id", "code")
				if err != nil {
					return err
				}
				body.Set("registration_codes_attributes", list)
			}
			return nil
		},
		createExample: `  streamingchasers works create --id W-1001 --title "Highway Windows Down" --code 3:123456789
  streamingchasers works create --data @work.json`,
		updateExample: `  streamingchasers works update W-1001 --title "Highway Windows Down (Reprise)"
  streamingchasers works update W-1001 --alt-title 2:"Windows Down"`,
	}
}

// parsePairs reads values of the form ID:TEXT into records with idKey a
// number and textKey the rest.
func parsePairs(flag string, values []string, idKey, textKey string) ([]any, error) {
	list := make([]any, 0, len(values))
	for _, value := range values {
		id, text, ok := strings.Cut(value, ":")
		id, text = strings.TrimSpace(id), strings.TrimSpace(text)
		if _, err := strconv.ParseInt(id, 10, 64); !ok || err != nil || text == "" {
			return nil, usagef("--%s takes ID:%s, not %q", flag, strings.ToUpper(textKey), value)
		}
		entry := output.NewRecord()
		entry.Set(idKey, json.Number(id))
		entry.Set(textKey, text)
		list = append(list, entry)
	}
	return list, nil
}

func salesResource() *resource {
	return &resource{
		name:     "sales",
		aliases:  []string{"sale", "placements"},
		singular: "sale",
		label:    "sale",
		segment:  "sales",
		short:    "Manage a company's sales: the placements of its works",
		long: `Manage a company's sales: the placements of its works in films, series and
episodes, which the royalty statements are matched against.

Sales are addressed by the IDs the server gives them. Most are loaded in
bulk with 'streamingchasers sales-uploads create'; this is for one at a
time, and for the exports.`,
		scope: scopeCompany,
		idArg: "ID",
		columns: []output.Column{
			{Header: "ID", Key: "id"},
			{Header: "WORK", Key: "work_external_id"},
			{Header: "WORK TITLE", Key: "work_title", Max: 35},
			{Header: "FILM", Key: "original_film_title", Max: 30},
			{Header: "SERIES", Key: "original_series_title", Max: 30},
			{Header: "EPISODE", Key: "original_episode_title", Max: 30},
			{Header: "RELEASED", Key: "release_date"},
		},
		fields: []field{
			{flag: "work-id", key: "work_id", kind: kindInt, usage: "the server's ID of the work (the API does not yet take an external ID here)"},
			{flag: "film-title", key: "original_film_title", usage: "title of the film"},
			{flag: "film-imdb", key: "film_imdb_id", usage: "IMDB ID of the film, as tt1234567"},
			{flag: "series-title", key: "original_series_title", usage: "title of the series"},
			{flag: "series-imdb", key: "series_imdb_id", usage: "IMDB ID of the series"},
			{flag: "episode-title", key: "original_episode_title", usage: "title of the episode"},
			{flag: "episode-imdb", key: "episode_imdb_id", usage: "IMDB ID of the episode"},
			{flag: "episode-number", key: "original_full_episode_number", kind: kindInt, usage: "full episode number, such as 304 for season 3 episode 4"},
			{flag: "external-work-id", key: "external_work_id", usage: "the work's ID as the supplier reported it"},
			{flag: "original-title", key: "original_work_title", usage: "the work's title as the supplier reported it"},
			{flag: "release-date", key: "release_date", kind: kindDate, usage: "release date"},
			{flag: "production-company", key: "original_production_company", usage: "production company as reported"},
		},
		filters: []filter{
			{flag: "work", param: "q[work_external_id]", usage: "sales of the work with this external ID"},
			{flag: "work-title", param: "q[work_title]", usage: "sales of works with this in their title"},
			{flag: "original-title", param: "q[original_work_title]", usage: "sales with this in the title as reported"},
			{flag: "code", param: "q[registration_code]", usage: "sales of works with this registration code"},
			{flag: "production", param: "q[film_or_series_title]", usage: "sales with this in the film or series title"},
			{flag: "episode", param: "q[episode_title]", usage: "sales with this in the episode title"},
		},
		createExample: `  streamingchasers sales create --data @sale.json`,
		updateExample: `  streamingchasers sales update 4411 --episode-title "Pilot" --episode-imdb tt0959621`,
	}
}

func subpublishingAgreementsResource() *resource {
	return &resource{
		name:     "subpublishing-agreements",
		aliases:  []string{"subpublishing", "subpub"},
		singular: "subpublishing_agreement",
		label:    "subpublishing agreement",
		segment:  "subpublishing_agreements",
		short:    "Manage subpublishing agreements between publishers",
		long:     agreementsLong("subpublishing"),
		scope:    scopeCompany,
		idArg:    "ID",
		columns:  agreementColumns(),
		fields:   agreementFields("subpublishing"),
		filters:  agreementFilters(),
		createExample: `  streamingchasers subpublishing-agreements create --assignor P-12 --assignee P-40 \
      --number SP-2024-1 --starts-on 2024-01-01 --pr-share 50 --mr-share 50`,
		updateExample: `  streamingchasers subpublishing-agreements update 7 --ends-on 2026-12-31`,
	}
}

func adminAgreementsResource() *resource {
	return &resource{
		name:     "admin-agreements",
		aliases:  []string{"administration-agreements"},
		singular: "admin_agreement",
		label:    "administration agreement",
		segment:  "admin_agreements",
		short:    "Manage administration agreements between publishers",
		long:     agreementsLong("administration"),
		scope:    scopeCompany,
		idArg:    "ID",
		columns:  agreementColumns(),
		fields:   agreementFields("admin"),
		filters:  agreementFilters(),
		createExample: `  streamingchasers admin-agreements create --assignor P-12 --assignee P-41 \
      --number AD-2024-1 --starts-on 2024-01-01 --fee-share 15`,
		updateExample: `  streamingchasers admin-agreements update 3 --licensing-authority "Assignee"`,
	}
}

func agreementsLong(kind string) string {
	return fmt.Sprintf(`Manage %s agreements: an assignor publisher's rights in some
territories, assigned to another publisher.

Publishers are named by their external IDs. The assignor and the assignee
are set when an agreement is created and cannot be changed afterwards.
Territories go with --territories, as JSON for the API's
*_tis_territories_attributes, or with --set.`, kind)
}

func agreementColumns() []output.Column {
	return []output.Column{
		{Header: "ID", Key: "id"},
		{Header: "ASSIGNOR", Value: nested("assignor", "name"), Max: 30},
		{Header: "ASSIGNEE", Value: nested("assignee", "name"), Max: 30},
		{Header: "NUMBER", Key: "agreement_number"},
		{Header: "STARTS", Key: "starts_on"},
		{Header: "ENDS", Key: "ends_on"},
	}
}

func agreementFields(kind string) []field {
	fields := []field{
		{flag: "assignor", key: "assignor_external_id", usage: "external ID of the publisher assigning its rights (create only)"},
		{flag: "assignee", key: "assignee_external_id", usage: "external ID of the publisher the rights are assigned to (create only)"},
		{flag: "number", key: "agreement_number", usage: "agreement number"},
	}
	if kind == "subpublishing" {
		fields = append(fields, field{flag: "mech-number", key: "mech_agreement_number", usage: "mechanical agreement number"})
	}
	fields = append(fields,
		field{flag: "starts-on", key: "starts_on", kind: kindDate, usage: "first day"},
		field{flag: "ends-on", key: "ends_on", kind: kindDate, usage: "last day"},
	)
	if kind == "subpublishing" {
		fields = append(fields,
			field{flag: "pr-share", key: "pr_royalty_share", kind: kindNumber, usage: "performance royalty share, in percent"},
			field{flag: "mr-share", key: "mr_royalty_share", kind: kindNumber, usage: "mechanical royalty share, in percent"},
			field{flag: "territories", key: "subpublishing_agreements_tis_territories_attributes", kind: kindJSON, usage: "the territories, as a JSON list of {tis_territory_id, inclusion_status}"},
		)
	} else {
		fields = append(fields,
			field{flag: "fee-share", key: "admin_fee_share", kind: kindNumber, usage: "administration fee, in percent"},
			field{flag: "licensing-authority", key: "licensing_authority", usage: "who may license"},
			field{flag: "territories", key: "admin_agreements_tis_territories_attributes", kind: kindJSON, usage: "the territories, as a JSON list of {tis_territory_id, inclusion_status}"},
		)
	}
	return fields
}

func agreementFilters() []filter {
	return []filter{
		{flag: "assignor", param: "assignor_external_id", usage: "agreements of this assignor, by external ID"},
		{flag: "assignee", param: "assignee_external_id", usage: "agreements of this assignee, by external ID"},
	}
}

func royaltyRecordsResource() *resource {
	return &resource{
		name:     "royalty-records",
		aliases:  []string{"raw-royalty-records", "royalty-record"},
		singular: "raw_royalty_record",
		label:    "royalty record",
		segment:  "raw_royalty_records",
		short:    "Browse the lines of every royalty statement",
		long: `Browse the lines of a company's royalty statements, across statements.
Each is a line as the PRO reported it, with the work and production it was
matched to, if any.

Narrow the list with the filters; --matched takes matched_work,
unmatched_work, matched_production or unmatched_production. For one
statement's lines, see 'streamingchasers royalty-statements records'.`,
		scope:    scopeCompany,
		idArg:    "ID",
		readOnly: true,
		columns:  royaltyRecordColumns(),
		filters:  royaltyRecordFilters(),
	}
}

func royaltyRecordColumns() []output.Column {
	return []output.Column{
		{Header: "ID", Key: "id"},
		{Header: "STATEMENT", Key: "royalty_statement_id"},
		{Header: "LINE", Key: "line_number"},
		{Header: "TITLE", Key: "work_title", Max: 35},
		{Header: "CODE", Key: "work_code"},
		{Header: "STREAMER", Key: "streamer_name", Max: 20},
		{Header: "FROM", Key: "usage_start_date"},
		{Header: "AMOUNT", Key: "gross_amount"},
		{Header: "CUR", Key: "currency"},
		{Header: "ISSUES", Value: yesNo("has_issues")},
	}
}

func royaltyRecordFilters() []filter {
	return []filter{
		{flag: "title", param: "q[title]", usage: "lines with this in the work title"},
		{flag: "iswc", param: "q[iswc]", usage: "lines with this ISWC"},
		{flag: "work-code", param: "q[work_code]", usage: "lines with this work code"},
		{flag: "work", param: "q[work_external_id]", usage: "lines matched to the work with this external ID"},
		{flag: "streamer", param: "q[streamer_name]", usage: "lines with this in the streamer's name"},
		{flag: "channel", param: "q[channel_name]", usage: "lines with this in the channel's name"},
		{flag: "customer", param: "q[customer]", usage: "lines with this customer"},
		{flag: "usage-type", param: "q[usage_type_name]", usage: "lines with this usage type"},
		{flag: "composer", param: "q[composer_name]", usage: "lines with this in the composer's name"},
		{flag: "publisher", param: "q[publisher_name]", usage: "lines with this in the publisher's name"},
		{flag: "territory", param: "q[territory_code]", usage: "lines for this territory code"},
		{flag: "from", param: "q[usage_date_from]", usage: "lines used on or after this date"},
		{flag: "to", param: "q[usage_date_to]", usage: "lines used on or before this date"},
		{flag: "line", param: "q[line_number]", usage: "the line with this number"},
		{flag: "matched", param: "q[matched]", usage: "matched_work, unmatched_work, matched_production or unmatched_production"},
		{flag: "issues", param: "q[has_issues]", usage: "1 for lines with issues"},
	}
}

func prosResource() *resource {
	return &resource{
		name:     "pros",
		aliases:  []string{"pro"},
		singular: "pro",
		label:    "PRO",
		segment:  "pros",
		short:    "Look up performing rights organizations",
		long: `Look up performing rights organizations (PROs). The list is shared by
every company.

A PRO can be named by its ID or its abbreviation, here and wherever --pro
is taken: 'streamingchasers pros get ASCAP'. Showing one includes its
instructions for submitting a claims sheet, when it has any.`,
		scope:    scopeGlobal,
		idArg:    "ID_OR_ABBREVIATION",
		readOnly: true,
		columns: []output.Column{
			{Header: "ID", Key: "id"},
			{Header: "ABBREVIATION", Key: "abbreviation"},
			{Header: "NAME", Key: "name", Max: 60},
		},
	}
}

// referenceResources are the read-only lists shared by every company.
func referenceResources() []*resource {
	simple := func(name, segment, label, plural, short string, columns []output.Column) *resource {
		return &resource{
			name: name, singular: strings.TrimSuffix(segment, "s"), label: label, pluralLabel: plural,
			segment: segment, short: short, scope: scopeGlobal, idArg: "ID", readOnly: true, columns: columns,
		}
	}
	id := output.Column{Header: "ID", Key: "id"}
	name := output.Column{Header: "NAME", Key: "name", Max: 50}
	code := output.Column{Header: "CODE", Key: "code"}
	description := output.Column{Header: "DESCRIPTION", Key: "description", Max: 60}
	return []*resource{
		simple("royalty-sources", "royalty_sources", "royalty source", "", "The PRO statement sources, each with its file formats",
			[]output.Column{id, name, {Header: "PRO", Key: "pro"}, {Header: "FORMATS", Value: func(r *output.Record) string {
				formats, _ := r.Value("royalty_file_formats").([]any)
				var names []string
				for _, f := range formats {
					if format, ok := f.(*output.Record); ok {
						names = append(names, fmt.Sprintf("%s (%s)", format.String("name"), format.String("id")))
					}
				}
				return strings.Join(names, ", ")
			}, Max: 70}}),
		simple("sales-file-formats", "sales_file_formats", "sales file format", "", "The formats a sales CSV may be in",
			[]output.Column{id, name, {Header: "PRO", Key: "pro"}, {Header: "TYPE", Key: "format_type"}, {Header: "EXTENSION", Key: "file_extension"}, {Header: "ACTIVE", Value: yesNo("active")}}),
		simple("works-file-formats", "works_file_formats", "works file format", "", "The formats a works CSV may be in",
			[]output.Column{id, name, {Header: "TYPE", Key: "format_type"}, {Header: "NEEDS PRO", Value: yesNo("requires_pro")}, {Header: "EXTENSION", Key: "file_extension"}, description}),
		simple("registration-types", "registration_types", "registration type", "", "The kinds of registration code a work can carry, one per PRO",
			[]output.Column{id, name, {Header: "PRO", Key: "pro_id"}, description}),
		simple("writer-designations", "writer_designations", "writer designation", "", "The CWR writer designations", []output.Column{id, code, description}),
		simple("publisher-types", "publisher_types", "publisher type", "", "The CWR publisher types", []output.Column{id, code, description}),
		simple("title-types", "title_types", "title type", "", "The CWR title types, for alternative titles", []output.Column{id, code, description}),
		simple("cis-languages", "cis_languages", "CIS language", "", "The languages a work can be in", []output.Column{id, code, name}),
		simple("tis-territories", "tis_territories", "TIS territory", "TIS territories", "The territories", []output.Column{id, {Header: "TIS", Key: "tis_a"}, {Header: "EXT", Key: "tis_a_ext"}, name}),
		simple("tis-territory-types", "tis_territory_types", "TIS territory type", "", "The kinds of territory", []output.Column{id, name}),
		simple("production-companies", "production_companies", "production company", "production companies", "The production companies", []output.Column{id, name}),
		simple("streamers", "streamers", "streamer", "", "The streaming services and channels", []output.Column{id, name}),
	}
}

// productionResources are movies, series and episodes, which can also be
// searched by title.
func productionResources() []*resource {
	title := output.Column{Header: "TITLE", Key: "title", Max: 60}
	imdb := output.Column{Header: "IMDB", Key: "imdb_id"}
	return []*resource{
		{name: "movies", singular: "movie", label: "movie", segment: "movies", scope: scopeGlobal, idArg: "ID_OR_IMDB_ID", readOnly: true,
			short:   "Look up movies",
			columns: []output.Column{{Header: "ID", Key: "id"}, title, imdb}},
		{name: "series", singular: "series", label: "series", pluralLabel: "series", segment: "series", scope: scopeGlobal, idArg: "ID_OR_IMDB_ID", readOnly: true,
			short:   "Look up series, and their episodes",
			columns: []output.Column{{Header: "ID", Key: "id"}, title, imdb}},
		{name: "episodes", singular: "episode", label: "episode", segment: "episodes", scope: scopeGlobal, idArg: "ID_OR_IMDB_ID", readOnly: true,
			short:   "Look up episodes",
			columns: []output.Column{{Header: "ID", Key: "id"}, title, {Header: "SEASON", Key: "season_number"}, {Header: "EPISODE", Key: "episode_number"}, imdb}},
	}
}
