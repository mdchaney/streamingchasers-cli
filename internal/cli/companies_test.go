package cli

import (
	"strings"
	"testing"
)

func TestCompaniesList(t *testing.T) {
	h := signedIn(t)
	r := h.ok("companies", "list")
	got := lines(r.stdout)
	if len(got) != 3 {
		t.Fatalf("unexpected output:\n%s", r.stdout)
	}
	want(t, got[0], "ID", "NAME", "OWNER", "DESCRIPTION")
	want(t, got[1], "2", "Frivolous Music", "Floyd Lawson")
	want(t, got[2], "3", "Soundmenders")
	if r.stderr != "" {
		t.Errorf("standard error should be empty when everything is shown, got %q", r.stderr)
	}
}

func TestCompaniesAreThoseTheUserCanSee(t *testing.T) {
	h := newHarness(t)
	h.signIn(viewerToken)
	r := h.ok("companies", "list")
	want(t, r.stdout, "Frivolous Music")
	unwanted(t, r.stdout, "Soundmenders")
	h.fails(ExitNotFound, "Not found", "companies", "get", "3")
}

func TestCompaniesGetAndUse(t *testing.T) {
	h := newHarness(t)
	h.signIn(ownerToken)
	h.fails(ExitUsage, "no company selected", "works", "list")

	r := h.ok("companies", "use", "frivolous music")
	want(t, r.stderr, "Now working in company 2, Frivolous Music.")
	want(t, h.ok("works", "list").stdout, "Drunken Daisy")
	want(t, h.ok("config", "get", "company").stdout, "2")

	want(t, h.ok("companies", "get").stdout, "name:", "Frivolous Music", "counts:", "subscription:")
	want(t, h.ok("companies", "get", "Soundmenders").stdout, "Soundmenders")
	want(t, h.ok("works", "list", "-c", "3").stderr, "No works.")
	h.fails(ExitNotFound, "Not found", "companies", "use", "99")
	r = h.fails(ExitError, `no company is named "Nope"`, "companies", "get", "Nope")
	want(t, r.stderr, `"Frivolous Music"`, `"Soundmenders"`)
}

func TestCompaniesUpdateTakesTheOwner(t *testing.T) {
	h := signedIn(t)
	r := h.ok("companies", "update", "2", "--description", "Film and TV")
	want(t, r.stderr, "Updated company 2.")
	want(t, r.stdout, "Film and TV")
	h.fails(ExitInvalid, "Name can't be blank", "companies", "update", "2", "--name", "")

	h = as(t, editorToken)
	r = h.fails(ExitForbidden, "Forbidden", "companies", "update", "2", "--name", "Mine")
	want(t, r.stderr, "Your role in the company does not allow this.")
}

func TestTransferOwnership(t *testing.T) {
	h := signedIn(t)
	h.fails(ExitUsage, "pass --yes", "companies", "transfer-ownership", "--to", "3")
	h.fails(ExitNotFound, "New owner not found", "companies", "transfer-ownership", "--to", "99", "--yes")
	r := h.ok("companies", "transfer-ownership", "--to", "3", "--yes")
	want(t, r.stderr, "Company 2 now belongs to user 3.")
	want(t, r.stdout, "owner_name:", "Barney Fife")
	h.fails(ExitForbidden, "Forbidden", "companies", "update", "2", "--name", "Mine")
}

func TestAccount(t *testing.T) {
	h := signedIn(t)
	r := h.ok("account", "show")
	want(t, r.stdout, "Floyd Lawson", "floyd@example.com", "companies:")
	unwanted(t, r.stdout, ownerToken)
	if got := h.ok("account", "token").stdout; got != ownerToken+"\n" {
		t.Errorf("got %q", got)
	}

	want(t, h.ok("account", "update", "--name", "Floyd T. Lawson").stdout, "Floyd T. Lawson")
	h.fails(ExitUsage, "nothing to change", "account", "update")
	h.fails(ExitInvalid, "Name can't be blank", "account", "update", "--name", "")

	h.stdin = strings.NewReader("hunter2\n")
	want(t, h.ok("account", "password").stderr, "Your password was changed.")
	h.interactive = true
	h.secrets = []string{"one", "two"}
	h.fails(ExitError, "do not match", "account", "password")

	// A reset replaces the stored token when that is what is in use.
	r = h.ok("account", "reset-token", "--yes")
	want(t, r.stderr, "the CLI now uses the new one")
	token := strings.TrimSpace(r.stdout)
	if token == ownerToken || h.credentials().APIToken != token {
		t.Errorf("token = %q, stored %q", token, h.credentials().APIToken)
	}
	want(t, h.ok("ping").stdout, "accepted your credentials")
}

func TestPros(t *testing.T) {
	h := signedIn(t)
	want(t, h.ok("pros", "list").stdout, "ABBREVIATION", "ASCAP", "BMI", "GEMA")
	want(t, h.ok("pros", "get", "ascap").stdout, "abbreviation:", "ASCAP", "instructions:", "claims@ascap.example")
	h.fails(ExitNotFound, "Not found", "pros", "get", "SACEM")
	h.fails(ExitUsage, `unknown command "create"`, "pros", "create")
}

func TestReferenceData(t *testing.T) {
	h := signedIn(t)
	want(t, h.ok("royalty-sources", "list").stdout, "ASCAP Domestic", "ASCAP CSV (1)")
	want(t, h.ok("works-file-formats", "list").stdout, "SACEM codes", "yes")
	want(t, h.ok("sales-file-formats", "get", "1").stdout, "Generic")
	want(t, h.ok("registration-types", "list").stdout, "ASCAP Work ID", "WORK ID IS", "the ASCAP Work ID")
	want(t, h.ok("streamers", "list").stdout, "TRACKED", "Netflix", "yes")
	want(t, h.ok("tis-territory-types", "list").stdout, "ABBREVIATION", "Country")
	want(t, h.ok("cwr", "destinations", "list").stdout, "ASCAP Delivery", "ASCAP")
	want(t, h.ok("cis-languages", "get", "en").stdout, "English")
	want(t, h.ok("tis-territories", "list").stdout, "2136", "World")
	want(t, h.ok("movies", "search", "big").stdout, "Big Movie", "tt0000300")
	want(t, h.ok("series", "episodes", "500").stdout, "Pilot")
	want(t, h.ok("series", "episodes", "500", "pil").stdout, "Pilot")
	want(t, h.ok("episodes", "get", "tt0005001").stdout, "Pilot")
	want(t, h.ok("movies", "search", "nothing").stderr, "No movies match.")
}
