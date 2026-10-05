# What building the CLI found in the API

This is a request from the command-line client to whoever is working on
the Rails application (`test.streamingchasers.com`). It is meant to be
read on its own. **It has been answered**: see
[rails-handoff-response.md](rails-handoff-response.md). Everything below
was addressed on the application's `mdc` branch (commits `b871f06`,
`8c14ccf`, `f496920`), and the CLI and its fake were changed to match on
5 October 2026:

| # | Asked for | The CLI now |
|---|---|---|
| 1 | Credits and sales by external id | `works create/update --writer EXT[:SHARE[:DESIGNATION]]`, `--publisher`; credits round-trip by id through `--data`; `sales create --work EXT` |
| 2 | The four missing controllers | `registration-types`, `streamers`, `production-companies`, `tis-territory-types` read the new fields |
| 3 | Catalogs normalized | The wrapper, the bare `errors` list and the missing pagination are no longer special-cased |
| 4 | Spec vs. code | `errors` read as a map of attribute to messages; `works_file_formats` as an envelope; agreement filters sent as `q[...]` |
| 5 | PRO by abbreviation | `--pro ASCAP` goes straight into the path; the lookup request is gone |
| 6 | CWR destinations | `streamingchasers cwr destinations list` |
| 7 | `auth_token` scoping, `mark_sent=0` | `account token` says when it needs `--admin`; `batches csv --no-mark-sent` |
| + | The version handshake (new, from [rails-to-cli-instructions.md](rails-to-cli-instructions.md)) | Sends `X-Client-API-Version`, reads `X-API-Version-Status`; `version` shows both; errors against an older server say so |

The CLI has still **not been run against the application itself**; the
section at the end says what to try first. What follows is the request
as it was made, kept for the record.

Ordered by how much they matter to a CLI user.

## 1. Works cannot name their writers and publishers (blocking)

`work_params` permits `works_writers_attributes: [:writer_id, ...]` and
`works_publishers_attributes: [:publisher_id, ...]`, and those are
**database IDs** (`WorksWriter belongs_to :writer`). Nothing in the API
ever exposes a writer's or publisher's database ID: the views serve
`id: writer.external_id`. So a client working to the API's own rule
("addressed by `external_id`, never database ids") cannot create a work
with its credits, and `works show` returns credits as `writer_id:
external_id`, which cannot be sent back.

Asked for: accept `writer_external_id` / `publisher_external_id` in the
nested attributes (or resolve `writer_id` as the external ID, since the
spec says that is what IDs are in this API). Until then the CLI's `works
create` and `import` can carry titles, codes and alt titles only, and
documents that the credits go in with `--set` by whatever the server
takes.

The same applies to **sales**: `sale_params` permits `work_id`, a
database ID. The CLI's `sales create --work-id` passes it through and says
so in its help; it should take the work's external ID.

## 2. Four reference controllers are missing

The spec and `config/routes.rb` list `registration_types`, `streamers`,
`production_companies` and `tis_territory_types` under `/api/v1`, but
there is no `app/controllers/api/v1/*_controller.rb` and no view for any
of them (only `admin/` and `my/` versions). A request will raise
`ActionController::RoutingError` / uninitialized constant. The OpenAPI
coverage test checks routes against the spec, not controllers against
routes, so it does not catch this.

`registration_types` matters most: it is how a client finds the
registration type to add a work code under (`works update --code
TYPE_ID:CODE`), and it needs `pro_id` in the response so the type for a
PRO can be found without hard-coding. The CLI's commands for all four are
written against `{ "<segment>": [...] }` with `id`, `name` (`code` /
`description` where the `my/` views have them), and the fake serves
`registration_types` with `id, name, pro_id, description,
work_id_description`.

## 3. Catalogs are the odd one out

`Api::V1::CatalogsController` differs from every other company resource:

- no `authorize_view!` / `authorize_edit!` (a viewer can create and
  delete catalogs; `CompanyScoped` is reimplemented inline);
- the index has no `add_pagination` and no `pagination` block;
- `show` wraps the record as `{"catalog": {...}}` where every other show
  is the bare record;
- validation failures render `{"errors": [...]}` without the
  `"message": "Validation failed"` envelope.

The CLI copes with all four, but they are the kind of drift the spec
cannot express. Suggest making it a normal `CompanyScoped` controller.

## 4. Things the spec says that the code does not

- **`ValidationErrors` schema** says `errors` is an object mapping each
  field to its messages. The view (`api/shared/validation_errors`) emits
  `@record.errors.full_messages`, a flat array. The CLI reads both.
- **`Pagination` is absent** from `catalogs`, `cwr_connections`,
  `royalty_sources`, `sales_file_formats`, `payment_periods`,
  `missing_work_ids` and `candidates`, and `works_file_formats` is a bare
  JSON array rather than `{"works_file_formats": [...]}`. All are listed
  as `OK` responses, so none of this is wrong by the spec, but a client
  has to special-case each. Wrapping `works_file_formats` like the rest
  would be a one-line change.
- `subpublishing_agreements` / `admin_agreements` `index` take
  `assignor_external_id` / `assignee_external_id` as **top-level** query
  parameters, not `q[...]`, unlike the other filtered indexes.
- `sales` `index` documents three `q[...]` filters; the controller takes
  six (`registration_code`, `film_or_series_title`, `episode_title` too).
  The CLI exposes all six.

## 5. The claims endpoints take only a numeric `pro_id`

`Pro.find(params[:pro_id])` in `broadcast_payment_periods`,
`unpaid_broadcasts`, `broadcast_delivery_batches`, `series_chase_scores`
and `rolled_up_productions`, while `/api/v1/pros/:id` uses
`FlexibleFinder` and takes an abbreviation. The CLI resolves `--pro
ASCAP` with one extra request to `/pros/ASCAP`; using `find_pro` in the
nested controllers would make that unnecessary and let
`.../pros/ASCAP/unpaid_broadcasts` work for curl users too.

## 6. No way to list CWR destinations

`company_cwr_connection` create needs `cwr_destination_id`, and
`cwr_destinations` are admin-only (`Admin::CwrDestinationsController`).
A client cannot discover the ID. Asked for: a read-only
`GET /api/v1/cwr_destinations` (id, name, PRO), like the other reference
lists. The CLI's `cwr connections create --destination` says "the web site
lists them" for now.

## 7. Small things

- **Users**: `PATCH /api/v1/users/me` and `reset_auth_token` serve
  `auth_token` in every `me` response, including plain `GET /users/me`.
  Fine for the CLI (it is how `account token` works), but note it is
  served to any `catalog`-scoped OAuth token, which otherwise could not
  write. Consider serving it only on `reset_auth_token`.
- **Works index** `q[title]` searches alt titles too (good); `q` as a
  plain string also works. The spec should drop the plain `q` or keep
  it; the CLI uses `q[title]`.
- **`sales/paid`** and **`paid/pro/:pro_id`** are the only list-shaped
  endpoints that are CSV only; the CLI sends `Accept: */*` for them.
- **`series_chase_scores/recompute`** is nested under a PRO but
  recomputes every PRO. Harmless; the CLI says so in its help.
- **Batch CSV marks the batch sent** on download, including a download
  that fails half way on the client. The CLI warns in its help; a
  `?mark_sent=0` or a separate `POST .../mark_sent` would let a client
  download, check, then mark.
- `royalty_statements` has no `destroy`; the spec agrees. Noted in case
  that is not intentional.

## What to try first against the application

The shapes that changed in answer to this note come first: credits and
sales by external id, the four new reference lists, catalogs, and the
validation-error map.

```console
$ streamingchasers version --host localhost:3000               # if the server is older than this program, stop: it is not deployed yet
$ streamingchasers auth login --host localhost:3000            # the OAuth flow end to end
$ streamingchasers companies list && streamingchasers companies use <name>
$ streamingchasers works list --title <word> ; writers list ; publishers list ; catalogs list
$ streamingchasers works-file-formats list ; registration-types list ; cwr destinations list
$ streamingchasers works create --id CLI-1 --title "CLI test" --writer <ext>:100 --publisher <ext> --code <type>:<code>
$ streamingchasers works get CLI-1 -o json | streamingchasers works update CLI-1 --data -   # credits round-trip
$ streamingchasers sales create --work CLI-1 --film-title "CLI test film"
$ streamingchasers writers create --id CLI-1                    # the validation-error map
$ streamingchasers works-uploads create --file <csv> --format <id> --wait ; works-uploads report <id>
$ streamingchasers royalty-statements create --file <csv> --source <name> --wait ; royalty-statements streamers <id>
$ streamingchasers chase list --pro ASCAP ; unpaid list --pro ASCAP ; periods list --pro ASCAP
$ streamingchasers batches preview --pro ASCAP --period <id>       # do not create against real data
$ streamingchasers cwr connections list ; cwr tickets candidates --connection <id>
```

Where the application and the fake (`internal/fakeserver`) disagree, the
fake is wrong: fix it there, and the CLI test that then fails says what
the CLI must change.
