# streamingchasers

The command line for [Streaming Chasers](https://app.streamingchasers.com):
load a catalog of works, writers and publishers, upload sales and royalty
statements, find what a PRO has not paid and build the claims sheets, and
register works by CWR.

```console
$ streamingchasers chase list --pro ASCAP
Sorted by money. Hidden: 1 rolled up, 3 fully paid. Last computed 2026-09-30T04:12:00Z.

SERIES  TITLE                 MONEY    VIEWS   EST. VIEWS  PAID  UNPAID
500     Big Series            1204.50  170000  200000      12    7
...
```

- [Install](#install)
- [Sign in](#sign-in)
- [Choose a company](#choose-a-company)
- [Commands](#commands)
- [Loading a catalog](#loading-a-catalog)
- [Sales and royalty statements](#sales-and-royalty-statements)
- [Chasing and claiming](#chasing-and-claiming)
- [CWR registration](#cwr-registration)
- [Output](#output)
- [Scripts](#scripts)
- [Settings](#settings)
- [Development](#development)
- [License](#license)

## Install

With Go 1.22 or later:

```console
$ go install github.com/mdchaney/streamingchasers-cli/cmd/streamingchasers@latest
```

Or from a checkout:

```console
$ make install        # into $(go env GOPATH)/bin
$ make build          # or just build ./bin/streamingchasers
```

For completion in your shell, see `streamingchasers completion --help`.

## Sign in

There are two ways to sign in.

### With your browser

```console
$ streamingchasers auth login
Opening your browser to sign in. If it does not open, visit:

  https://app.streamingchasers.com/oauth/authorize?...

Waiting for you to approve the request...
Signed in to https://app.streamingchasers.com. The CLI may: read your catalog and change your catalog.
```

You sign in to Streaming Chasers in the browser and approve the CLI
there. Nothing secret is typed into the terminal. The CLI gets only the
permissions you approve, its access lasts an hour at a time and is renewed
as needed, and you can disconnect it at any time from your account page or
with `streamingchasers auth logout`.

It asks to read your catalog and to change it. **It does not ask to
administer unless you pass `--admin`.** That permission lets it change a
company's settings and ownership, your own account and password, and the
CWR connections that hold PRO server credentials: more than loading a
catalog and chasing claims needs. A command that needs it and has not got
it says so, and how to sign in again with it.

| Flag | |
|---|---|
| `--admin` | Also ask for permission to administer companies, CWR connections and your account |
| `--no-browser` | Print the address instead of opening a browser. On a machine without one, open the address on another machine, approve, and paste the address that browser ends up at (it starts `http://127.0.0.1` and will not load) back into the terminal. |
| `--scopes catalog` | Ask for less than everything. Without `catalog_write` the CLI can read but not change anything. |
| `--new-client` | Register the CLI with the server again. For when the server has forgotten it. |

### With your API token

Your API token is on your [account page](https://app.streamingchasers.com/my/account).
The CLI reads it from standard input so that it stays out of your shell
history:

```console
$ streamingchasers auth login --with-token
API token (from https://app.streamingchasers.com/my/account):
Signed in to https://app.streamingchasers.com with an API token.
```

An API token may do anything you may, and works until you reset it with
`streamingchasers account reset-token` or on your account page. Because it
is unscoped, a browser sign-in without `--admin` cannot read it:
`streamingchasers account token` needs the token itself or `--admin`.

### Other servers

```console
$ streamingchasers auth login --host test.streamingchasers.com
$ streamingchasers auth login --with-token --host localhost:3000
```

The first server you sign in to becomes the default. Credentials are kept
for each server separately; `--host`, or `STREAMINGCHASERS_HOST`, chooses
between them. For a server on your own machine, plain `http` is assumed.

### Checking and signing out

```console
$ streamingchasers auth status
$ streamingchasers auth logout
```

## Choose a company

Almost everything happens in a company. Choose one once:

```console
$ streamingchasers companies list
ID  NAME             OWNER         DESCRIPTION
2   Frivolous Music  Floyd Lawson  Production music for film and television

$ streamingchasers companies use "Frivolous Music"
Now working in company 2, Frivolous Music.
```

Or name one each time with `--company` (`-c`), by ID or by name, or with
`STREAMINGCHASERS_COMPANY`.

Your role in a company is what you may do there. A viewer can read; an
editor can also change the catalog, load files and build claims; an admin
can also manage CWR connections; the owner can also change the company's
settings and hand it to another collaborator.

Companies are made, collaborators invited and removed, subscriptions
bought, and sessions and connected applications managed on the web site,
and only there: the API leaves them out on purpose, and so does this
program.

## Commands

| | |
|---|---|
| `auth login`, `logout`, `status`, `token` | Sign in and out |
| `companies list`, `get`, `update`, `use`, `transfer-ownership` | Your companies |
| `account show`, `update`, `password`, `token`, `reset-token` | Your own account |
| `config list`, `get`, `set`, `unset`, `path` | Settings |
| `ping` | Check the server and your credentials |
| `works`, `writers`, `publishers`, `catalogs` | The catalog: `list`, `get`, `create`, `update`, `delete`, and `import` for all but catalogs |
| `publishers alt-names add`, `remove`; `publishers import-sheet` | A publisher's other names; the publisher sheet import |
| `subpublishing-agreements`, `admin-agreements` | Agreements between publishers |
| `works-uploads` | Load a catalog from CSV, with the match report |
| `sales`, `sales-uploads` | Placements, one at a time or from CSV, with the paid-sales exports and lookups |
| `royalty-statements`, `royalty-records` | PRO statements and their lines |
| `pro-data-dumps` | A PRO's export of work codes |
| `periods`, `unpaid`, `batches`, `chase`, `rollups` | Chasing and claiming |
| `cwr destinations`, `connections`, `tickets`, `files`, `acks` | CWR registration |
| `pros`, `royalty-sources`, `sales-file-formats`, `works-file-formats`, `registration-types`, ... | Reference data, `list` and `get` |
| `api` | Any request to the API |

Every command has `--help`, with examples.

### Records

Works, writers, publishers and catalogs are addressed by **your** external
IDs, which you choose when you create them: letters, numbers, hyphens and
underscores. Sales, uploads, statements, batches and agreements have IDs
the server gave them. PROs can be named by their abbreviation wherever one
is taken.

```console
$ streamingchasers writers create --id W-125 --first-name Johann --last-name Bach --pro 10
$ streamingchasers writers update W-125 --ipi-name 116 --controlled
$ streamingchasers writers get W-125
$ streamingchasers writers delete W-125
```

`list` takes filters particular to each kind of record: `--name` and
`--external-id` for writers and publishers, `--title` for works, and six
for sales. `update` changes only what you name. To remove a value, set it
to nothing: an empty string for text, or `null` with `--set`.

Anything can also be given as JSON, with the fields the API documents, and
`--set key=value` sets any field; the value is JSON when it parses as
JSON, and text otherwise:

```console
$ streamingchasers works create --data @work.json
$ streamingchasers works update W-1001 --set 'works_writers_attributes=[{"writer_id": 7, "share": 100}]'
```

`delete` asks first. A script must pass `--yes`.

## Loading a catalog

The quick way is a CSV in one of the formats the server knows, which it
processes in the background:

```console
$ streamingchasers works-file-formats list
$ streamingchasers works-uploads create --file catalog.csv --format 1 --wait
Uploaded catalog.csv as works upload 12. The server is processing it.
Processing completed after 40s.
```

`--wait` keeps looking until the server is done and exits with 1 if the
processing failed, printing the errors. Without it, poll with `get`; the
`status` field is `pending`, `completed` or `failed`.

Two flags change what a load does. `--match-by-title` matches rows to the
works already there, by title, instead of creating works; `--codes-only`
adds registration codes to the works it matches and changes nothing else.
Together they are how a PRO's codes are brought in for a catalog that is
already loaded, and the server writes a match report to hand-work:

```console
$ streamingchasers works-uploads create --file sacem-codes.csv --format 4 --pro SACEM \
    --match-by-title --codes-only --wait
$ streamingchasers works-uploads report 13 --save-as sacem-matches.csv
```

Every upload carries an `Idempotency-Key`, so a request repeated after a
network failure cannot load the file twice. Pass `--idempotency-key` to
choose it and repeat a run safely.

Records can also go one at a time, from JSON:

```console
$ streamingchasers writers import writers.json
$ streamingchasers publishers import publishers.jsonl
$ streamingchasers works import works.json
$ streamingchasers publishers import-sheet publishers.csv
```

`import` reads a JSON list or JSON Lines, `-` for standard input, and what
`list -o jsonl` writes it reads back. A record that exists is updated and
one that does not is created (`--mode upsert`); `--mode create` and
`--mode update` do only the one. A record that fails does not stop the
rest; the failures are listed at the end and the exit code is 1.

A work's credits, codes and alternative titles have flags, each a few
fields joined by colons:

```console
$ streamingchasers works create --id W-1001 --title "Highway Windows Down" \
    --writer W-186:50 --writer W-187:50:1 --publisher P-2 --code 3:123456789
$ streamingchasers works update W-1001 --alt-title 1:"Windows Down"
```

| | |
|---|---|
| `--writer EXT`, `--writer EXT:SHARE`, `--writer EXT:SHARE:DESIGNATION_ID` | A writer, by external ID. Shares left out are split evenly; `writer-designations list` shows the designations. |
| `--publisher EXT[:SHARE[:TYPE_ID]]` | A publisher, likewise; `publisher-types list` |
| `--code TYPE_ID:CODE` | A registration code; `registration-types list` shows the types, one per PRO |
| `--alt-title TYPE_ID:TITLE` | An alternative title; `title-types list` |

On an update they add to what the work has. Credits carry their own ids
in `works get`, so to change or remove one send it back by id, with
`--data` or `--set`; what `get` shows can be sent straight back:

```console
$ streamingchasers works get W-1001 -o json > work.json      # edit, then
$ streamingchasers works update W-1001 --data @work.json
$ streamingchasers works update W-1001 --set 'works_writers_attributes=[{"id": 7, "_destroy": true}]'
```

## Sales and royalty statements

Sales are the placements of works in films, series and episodes; royalty
statements are what the PROs paid, matched against them.

```console
$ streamingchasers sales-uploads create --file placements.csv --wait
$ streamingchasers sales create --work W-999 --film-title "Big Movie" --film-imdb tt0000300
$ streamingchasers sales list --production "Big Series"
$ streamingchasers sales paid-csv --save                       # every paid sale, a column per PRO
$ streamingchasers sales paid-csv --pro BMI --save-as bmi.csv  # one PRO's, a column per streamer
```

```console
$ streamingchasers royalty-sources list
$ streamingchasers royalty-statements create --file ascap-2026q1.csv --source "ASCAP Domestic" --wait
$ streamingchasers royalty-statements streamers 31    # the names it reported, and which were recognised
$ streamingchasers royalty-statements records 31 --issues 1
$ streamingchasers royalty-statements reprocess 31 --wait
$ streamingchasers royalty-records list --matched unmatched_work --from 2026-01-01
```

`royalty-statements streamers` is the match-quality check: the streamer
and channel names the statement used, with the streamer each resolved
to, unrecognised names first. `reprocess` runs a failed import again.

## Chasing and claiming

For one PRO at a time, named by ID or abbreviation:

```console
$ streamingchasers chase list --pro ASCAP               # where the money is, by series
$ streamingchasers chase placements 500 --pro ASCAP     # every placement in one series
$ streamingchasers unpaid list --pro ASCAP              # what is claimable
$ streamingchasers batches missing-works --pro ASCAP    # works that need a code first
$ streamingchasers periods list --pro ASCAP --available
$ streamingchasers batches preview --pro ASCAP --period 12
$ streamingchasers batches create --pro ASCAP --period 12 --notes "Q1 2026"
$ streamingchasers batches csv 34 --save                # the claims sheet
```

A batch gathers every claimable unpaid placement up to a payment period
into a sheet to send to the PRO. **It locks out its period and every
earlier one for that PRO**, so preview first: what the preview shows is
exactly what `create` takes, less any rows you name with `--exclude`, and
`--min-money`, `--min-views` and `--min-unpaid` narrow both the same way.
`create` asks before it goes ahead; a script passes `--yes`. A period that
is no longer available is a conflict, exit code 7.

Downloading the sheet with `batches csv` marks the batch as sent;
`--no-mark-sent` downloads it to check first, and a later plain download
marks it. `--variant missing-codes` downloads instead the companion sheet
of rows left out for want of a work code, and never marks the batch. Add a code with
`works update EXTERNAL_ID --code TYPE:CODE`; `batches missing-works`
names the type.

Productions a PRO pays as one rolled-up line are never claimed.
`streamingchasers rollups list --pro ASCAP` shows them and what they
block; `rollups get Series 600 --pro ASCAP` the lines and the works.

`chase recompute --pro ASCAP` refreshes the scores in the background.

## CWR registration

```console
$ streamingchasers cwr destinations list                          # the PRO servers
$ streamingchasers cwr connections list
$ streamingchasers cwr tickets candidates --connection 3          # works never sent
$ streamingchasers cwr tickets queue --connection 3 --reason "New catalog" W-1 W-2
$ streamingchasers cwr files candidates --connection 3            # what a new file could hold
$ streamingchasers cwr files create --connection 3 W-1 W-2
$ streamingchasers cwr files download 17 --connection 3 --save
$ streamingchasers cwr files send 17 --connection 3
$ streamingchasers cwr connections download-acks 3
$ streamingchasers cwr acks list --connection 3
```

A connection is the company's link to one PRO's delivery server;
`--destination` names the server by ID, name or PRO. Making
or changing one takes a company admin and, with a browser sign-in,
`streamingchasers auth login --admin`. Its password for the PRO server is
accepted and never shown again; give it as `--password @file` to keep it
out of your shell history.

## Output

`--output` (`-o`) chooses the format:

| | |
|---|---|
| `table` | The default. For reading. Long values are cut short. |
| `json` | What the server sent. A list is a JSON array of records. |
| `jsonl` | A record to a line |
| `csv` | With a header row, and every field rather than only the table's columns |

Data goes to standard output, and everything said about it (what was
created, how many more pages there are, progress) to standard error, so
output can be piped as it is:

```console
$ streamingchasers works list --all -o json | jq '.[] | .title'
$ streamingchasers unpaid list --pro ASCAP --all -o csv > unpaid.csv
```

Lists show a page, usually of 25. `--page` and `--per-page` (200 at most)
choose another; `--all` reads every page; `--limit N` reads the first N.

Files the server makes, such as claims sheets and the paid-sales export,
go to standard output unless `--save` (under the server's name) or
`--save-as FILE` says otherwise.

## Scripts

```console
$ export STREAMINGCHASERS_TOKEN=...          # instead of signing in
$ export STREAMINGCHASERS_HOST=https://app.streamingchasers.com
$ export STREAMINGCHASERS_COMPANY=2
$ streamingchasers works get W-1001 -o json
```

| Exit code | |
|---|---|
| 0 | Success |
| 1 | An error, including an import in which some records failed or an upload the server could not process |
| 2 | The command was misused |
| 3 | Not signed in, or the server refused the credentials |
| 4 | Not allowed: your role in the company, or the token's permissions |
| 5 | Not found |
| 6 | The server rejected the data as invalid |
| 7 | A conflict: a payment period already claimed, a CWR file already sent |
| 130 | Interrupted |

For what the commands do not cover, `streamingchasers api` makes any
request with your credentials:

```console
$ streamingchasers api companies/2/works --query 'q[title]=daisy'
$ streamingchasers api companies/2/writers --data '{"writer": {"external_id": "W-9", "last_name": "Raw"}}'
$ streamingchasers api companies/2/sales/paid --raw > paid.csv
$ curl -H "Authorization: Bearer $(streamingchasers auth token)" https://app.streamingchasers.com/api/v1/users/me
```

`--debug` logs every request and response to standard error. It does not
log your token, and passwords and tokens in what is sent and received are
left out of it.

Requests that are safe to repeat are tried three times when the server is
unreachable or answers 502, 503 or 504: reads, updates, deletes, and the
uploads, which carry an idempotency key. Other creates are never repeated.
The server allows 600 requests a minute; a 429 says how long to wait.

### API versions

The API has a version, and this program is built for one. It declares it
with every request, and the server says how the two stand:

```console
$ streamingchasers version
streamingchasers 1.0.0 (API 1.5.0)
https://app.streamingchasers.com speaks API 1.5.0: the same as this program
```

When the server is newer, every command ends with a note to upgrade. When
the server is older than this program, nothing is said while things work;
when something fails, the error comes with a note that the server may not
have it yet, which is what a 404 from a server awaiting a deploy means.
`version` asks without credentials, so it works before signing in, and
`auth status` and `--debug` show both versions too.

## Settings

| Environment | Setting | Flag | |
|---|---|---|---|
| `STREAMINGCHASERS_HOST` | `host` | `--host` | The server. `https://app.streamingchasers.com` unless you say. |
| `STREAMINGCHASERS_COMPANY` | `company` | `--company`, `-c` | The company, kept for each server |
| `STREAMINGCHASERS_TOKEN` | | | A token to use instead of the stored credentials |
| `STREAMINGCHASERS_CONFIG_DIR` | | | Where the files are kept |
| | | `--timeout` | How long to wait for an answer. `2m` unless you say. |

A flag overrides the environment, which overrides the setting.

Settings are in `config.json` and credentials in `credentials.json`, in
`$STREAMINGCHASERS_CONFIG_DIR`, else `$XDG_CONFIG_HOME/streamingchasers`,
else `~/.config/streamingchasers`. `credentials.json` is readable only by
you. It holds your tokens as they are, not encrypted: anyone who can read
your files can use them, as with `~/.netrc`. On a shared machine, prefer
the browser sign-in, whose tokens can be disconnected from your account
page, or keep the token in `STREAMINGCHASERS_TOKEN`.

## Development

```console
$ make test       # the tests, with the race detector
$ make lint       # gofmt, go vet, staticcheck
$ make cover      # coverage, as HTML
$ make build      # ./bin/streamingchasers
$ make release    # binaries for macOS, Linux and Windows in ./dist
```

| | |
|---|---|
| `cmd/streamingchasers` | `main`: the terminal, the environment, signals |
| `internal/cli` | The commands |
| `internal/api` | The REST client: requests, multipart uploads, errors, pagination, retries, the version handshake |
| `internal/oauth` | Discovery, registration, PKCE, the loopback redirect, refresh, revocation |
| `internal/config` | Settings and credentials |
| `internal/output` | Tables, CSV and JSON |
| `internal/fakeserver` | A stand-in for the Rails application, for the tests |

The CLI is built against the server's OpenAPI description, `docs/openapi.yaml`
in its repository and `/api/v1/openapi.yaml` on any server, which a test
there keeps truthful, and against its jbuilder views for the shape of each
record. The description's `info.version` is
the API version this program declares: `make build` reads it from the
description when the server's repository is beside this one, and
`internal/api/version.go` holds it otherwise. A test fails when the two
differ, which is the prompt to follow what changed in the API and bump it.

### The tests

The tests run the commands from end to end against `internal/fakeserver`,
an in-memory stand-in for the Rails application's `/api/v1` and OAuth
endpoints. It gives the same status codes, error bodies and pagination,
and has the same rules about who may do what. Nothing reaches the network
and nothing of yours is read: each test has its own server, its own
configuration directory and its own environment.

**The Rails application is the specification. Where the fake and the
application disagree, the fake is wrong.** The CLI has not yet been run
against the application itself; [docs/rails-handoff.md](docs/rails-handoff.md)
lists what building it found in the API,
[docs/rails-handoff-response.md](docs/rails-handoff-response.md) how the
application answered, and what to try first.

Adding a command for a new kind of record is mostly a matter of describing
it: see `writersResource` in `internal/cli/definitions.go`.

## License

MIT. See [LICENSE](LICENSE).

The license is of this program. Streaming Chasers, the service it talks
to, has terms of its own.
