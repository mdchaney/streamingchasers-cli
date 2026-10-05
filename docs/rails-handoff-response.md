# The Rails application's answer to rails-handoff.md

Everything in the note is addressed on the `mdc` branch of
`test.streamingchasers.com` (commits `b871f06`, `8c14ccf`, `f496920`),
suite green. The spec at `/api/v1/openapi.yaml` reflects all of it.
Where the fake differs from what's below, the fake should change.

## 1. Credits and sales by external id — FIXED (as you preferred)

`writer_id` / `publisher_id` in `works_writers_attributes` /
`works_publishers_attributes` and `work_id` on sales **are now the
external ids** the API serves; `writer_external_id` /
`publisher_external_id` also work. They resolve within the company, so
an unknown or cross-tenant id answers 404. So `works show` output can be
sent straight back.

Also, while in there:

- The permitted `:role` on credits was bogus (no such column); the real
  columns `writer_designation_id` / `publisher_type_id` are now
  permitted and served.
- A work's show response now carries each credit row's `id` (the join
  row), plus `writer_designation_id` / `publisher_type_id`, so credits
  round-trip through nested updates and `_destroy`. The CLI can now do
  full credit editing, not just create.

## 2. The four missing controllers — BUILT

`registration_types` (with `pro_id`, `work_id_description`,
`validation_regexp`), `streamers` (`id`, `name`, `tracked`),
`production_companies` (`id`, `name`) and `tis_territory_types` (`id`,
`name`, `abbreviation`) all exist now, in the standard
`{ "<segment>": [...] }` envelope with a `pagination` block. Note for
the fake: `registration_types` has **no `description` field** — the
descriptive text is `work_id_description`.

Your diagnosis of why the coverage test missed this was right; there is
now a test asserting every `/api` route resolves to a real controller
action, so this class of gap can't recur.

## 3. Catalogs — NORMALIZED

Standard `CompanyScoped` controller with `authorize_view!` /
`authorize_edit!` (a viewer can no longer create or delete), paginated
index, bare show (no `{"catalog": ...}` wrapper; `id` is the external
id like every other resource), standard validation envelope. Drop the
four special cases.

## 4. Spec vs. code

- **ValidationErrors**: the view now emits what the spec said - `errors`
  is a map of attribute => full messages (e.g.
  `{"name": ["Name can't be blank"]}`), with `"message": "Validation
  failed"`. The flat-array form is gone.
- **`works_file_formats`** is now `{ "works_file_formats": [...] }` with
  a pagination block.
- **Agreement filters** take `q[assignor_external_id]` /
  `q[assignee_external_id]`; the bare top-level params still work, but
  the spec documents the `q[...]` form.
- **Sales** documents all six `q[...]` filters.
- Other unpaginated reference lists are unchanged (small, spec-OK).

## 5. PRO by abbreviation — DONE

All the claims endpoints (`payment_periods`, `unpaid_broadcasts`,
`broadcast_delivery_batches`, `series_chase_scores`,
`rolled_up_productions`) resolve `:pro_id` through the same
`find_pro` as `/pros/:id`, so `.../pros/ASCAP/unpaid_broadcasts` works
(case-insensitive). The CLI can drop its extra resolution request.

## 6. CWR destinations — ADDED

`GET /api/v1/cwr_destinations` serves `id`, `name`, `pro`
(abbreviation). Identity only - no server details.

## 7. Small things

- **`auth_token` scoping**: taken - `/users/me` serves `auth_token` only
  to the account token itself or an OAuth token carrying
  `catalog_admin`. A `catalog`-scoped token gets no `auth_token` key.
  If `account token` runs under OAuth, it needs `catalog_admin` (or the
  account token); plain reads keep working without it.
- **`mark_sent=0`** on the batch CSV: added. Download with
  `?mark_sent=0` to check the sheet; a later plain download marks it
  sent. Spec'd.
- **`recompute`** nested-but-global: as designed (scores recompute as a
  set per company); the spec summary says so.
- **`sales/paid` CSVs**: CSV-only is intentional - they are hand-off
  sheets, not data endpoints.
- **`royalty_statements` without destroy**: intentional - statements
  anchor imported royalty records; removal stays an admin operation.
- Plain-string `?q=` on works stays (documented as the legacy form);
  prefer `q[title]` as you do.

## One request back

Nothing in the fake needed server changes beyond the above. When the
CLI first runs against a real server, start with the item-1 flows
(works create with credits, sales create) and `registration-types list`,
since those are the shapes that changed; the `works_file_formats` and
catalogs envelopes and the validation-error map also changed shape, so
`api.ParsePage`'s special cases for them can be deleted rather than
kept.
