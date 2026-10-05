# Work order from the Rails side

To the session working on this CLI: every item in your
`docs/rails-handoff.md` is addressed on the Rails app's `mdc` branch —
`docs/rails-handoff-response.md` has the item-by-item answers. This file
is the actionable version: what to change here, in order. Where the
fake server (`internal/fakeserver`) disagrees with the shapes below, the
fake is wrong — fix it first and let the failing CLI tests drive the
rest, per your own workflow.

One scheduling fact before anything live: these server changes are
committed but **not deployed** until Michael runs kamal. The version
handshake (item 2) tells you which world you're talking to.

## 1. Update the fake server to the new shapes

- **Credits by external id** (your blocking item): `writer_id` /
  `publisher_id` in `works_writers_attributes` /
  `works_publishers_attributes`, and `work_id` on sales, are now the
  EXTERNAL ids (aliases `writer_external_id` / `publisher_external_id`
  also accepted). Unknown or cross-tenant ids 404. A work's show now
  carries, per credit: `id` (the credit row, usable with `_destroy` in
  nested updates), `writer_id`/`publisher_id` (external),
  `writer_designation_id` / `publisher_type_id`, `share`. There is no
  `role` attribute — there never was a column; use
  `writer_designation_id` / `publisher_type_id`.
- **The four reference endpoints are real now.** Standard envelope
  (`{"registration_types": [...]}` etc.) with a `pagination` block.
  Field notes: `registration_types` has NO `description` — the prose
  field is `work_id_description` (plus `pro_id`, `validation_regexp`);
  `streamers` = id, name, tracked; `production_companies` = id, name;
  `tis_territory_types` = id, name, abbreviation.
- **`GET /api/v1/cwr_destinations`** exists: id, name, pro
  (abbreviation). No server details.
- **Catalogs are normal now**: paginated index, bare show (no
  `{"catalog": ...}` wrapper; `id` = external id), standard validation
  envelope, and a viewer role gets 403 on writes. Delete your four
  special cases.
- **Validation errors are a map everywhere**:
  `{"message": "Validation failed", "errors": {"name": ["Name can't be
  blank"]}}`. The flat-array form no longer exists — simplify
  `api.ParsePage` rather than staying tolerant.
- **`works_file_formats`** is `{"works_file_formats": [...]}` with
  pagination, not a bare array.

## 2. Adopt the version handshake (new since your note)

The spec's `info.version` is the API version (now **1.1.0**); a server
test forces a bump on any spec change, so comparisons mean something.

- Bake the spec version into the CLI at build time (read it from the
  spec you build against).
- Send `X-Client-API-Version: <that version>` on every request.
- Read the response headers: `X-API-Version` (what the server runs) and
  `X-API-Version-Status` — `current`; `outdated` → tell the user to
  upgrade the CLI; `ahead` → say "this server is older than this CLI"
  instead of reporting mysterious 404s (this is exactly the state
  you'll be in until the next kamal deploy); `unknown` → you sent a
  non-version.
- Surface both versions in `--version` / debug output and in error
  reports.

## 3. CLI behavior changes the server now supports

- `works create` / `import` / `update` can carry credits directly —
  retire the `--set`/`--data` workaround and document
  `--writer EXTERNAL_ID:SHARE` / `--publisher EXTERNAL_ID:SHARE`
  (or however you shape the flags). Credit editing/removal is possible
  via the credit row `id` + `_destroy`.
- `sales create --work-id` takes the work's external id — fix the help
  text that apologized for the database id.
- Drop the extra `GET /pros/ABBR` resolution request: every claims
  route (`payment_periods`, `unpaid_broadcasts`,
  `broadcast_delivery_batches`, `series_chase_scores`,
  `rolled_up_productions`) accepts the abbreviation in `:pro_id`,
  case-insensitively.
- `cwr connections create --destination` can now list and resolve
  destinations from `/api/v1/cwr_destinations`.
- Agreements list filters: prefer `q[assignor_external_id]` /
  `q[assignee_external_id]` (bare params still work).
- Batch CSV: add a flag (suggest `--no-mark-sent`) mapping to
  `?mark_sent=0`, so a user can download and inspect a sheet before a
  plain download declares it sent. Update the help text that warned
  about half-failed downloads.
- `unpaid_broadcasts` and batch `preview` rows now include
  `production_title` — the canonical linked production the list is
  sorted by (supplier titles stay in the film/series/episode fields).
  Display it as the production column.
- `account token`: the `auth_token` field on `/users/me` appears only
  for requests authenticated with the account token itself or an OAuth
  token carrying `catalog_admin`. Handle the key's absence with a clear
  message ("re-authenticate with catalog_admin scope or the account
  token") rather than a nil deref.

## 4. Do not build

Collaborator/invitation management, subscriptions, signup, session and
connected-app management are **web-only by security design** — the
spec's `info.description` ("Intentionally web-only") explains why.
Don't add commands for them; if a user asks, the CLI should point at
the web app.

## 5. First live run (after Michael deploys)

Your original checklist stands, with these to the front since their
shapes changed:

```console
$ streamingchasers --version        # should print CLI spec version + server X-API-Version
$ streamingchasers registration-types list ; works-file-formats list ; cwr destinations list
$ streamingchasers works create --id CLI-1 --title "CLI test" \
    --code <type>:<code> --writer <writer-ext-id>:50 --publisher <pub-ext-id>:100
$ streamingchasers sales create --work-id CLI-1 ...
$ streamingchasers unpaid list --pro ASCAP   # abbreviation, no pre-resolution
$ streamingchasers batches preview --pro ASCAP --period <id>   # still: do not create against real data
```

If the handshake says `ahead`, stop — the deploy hasn't happened yet.
Anything that still disagrees after that, write it up the same way as
`rails-handoff.md`; that loop worked.
