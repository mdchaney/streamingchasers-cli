package cli

import (
	"strings"
	"testing"
)

func TestWritersCRUD(t *testing.T) {
	h := signedIn(t)
	r := h.ok("writers", "list")
	want(t, r.stdout, "ID", "NAME", "PRO", "CONTROLLED", "W-186", "Ilze Platais", "American Society", "yes", "W-187", "Neal Busby")

	r = h.ok("writers", "create", "--id", "W-300", "--first-name", "Dolly", "--last-name", "Parton", "--pro", "21", "--controlled")
	want(t, r.stderr, "Created writer W-300.")
	want(t, r.stdout, "full_name:", "Dolly Parton")
	body := h.lastBody("POST", "/api/v1/companies/2/writers")
	writer := body["writer"].(map[string]any)
	if writer["external_id"] != "W-300" || writer["pro_id"] != float64(21) || writer["controlled"] != true {
		t.Errorf("unexpected body: %v", body)
	}

	r = h.ok("writers", "update", "W-300", "--ipi-name", "116")
	want(t, r.stderr, "Updated writer W-300.")
	if req := h.lastRequest("PATCH", "/api/v1/companies/2/writers/W-300"); !strings.Contains(string(req.Body), `"ipi_name_number":116`) {
		t.Errorf("unexpected body: %s", req.Body)
	}

	want(t, h.ok("writers", "list", "--name", "parton").stdout, "Dolly Parton")
	unwanted(t, h.ok("writers", "list", "--name", "parton").stdout, "Busby")
	want(t, h.ok("writers", "list", "--external-id", "W-186").stdout, "Platais")

	h.fails(ExitUsage, "pass --yes", "writers", "delete", "W-300")
	want(t, h.ok("writers", "delete", "W-300", "--yes").stderr, "Deleted writer W-300.")
	h.fails(ExitNotFound, "Not found", "writers", "get", "W-300")
}

func TestValidationErrorsAreListed(t *testing.T) {
	h := signedIn(t)
	r := h.fails(ExitInvalid, "Validation failed", "writers", "create", "--id", "W 1", "--first-name", "No")
	want(t, r.stderr, "External can only contain letters, numbers, hyphens, and underscores", "Last name can't be blank")
	h.fails(ExitInvalid, "External has already been taken", "writers", "create", "--id", "W-186", "--last-name", "Again")
	h.fails(ExitUsage, "nothing to create", "writers", "create")
}

func TestRolesAreEnforced(t *testing.T) {
	h := as(t, viewerToken)
	want(t, h.ok("writers", "list").stdout, "Platais")
	r := h.fails(ExitForbidden, "Forbidden", "writers", "create", "--id", "W-1", "--last-name", "X")
	want(t, r.stderr, "Your role in the company does not allow this.")

	h = as(t, editorToken)
	h.ok("writers", "create", "--id", "W-1", "--last-name", "X")
	h.fails(ExitForbidden, "Forbidden", "cwr", "connections", "update", "3", "--active=false")
}

func TestPublishers(t *testing.T) {
	h := signedIn(t)
	want(t, h.ok("publishers", "list").stdout, "LEGAL NAME", "Mike's Awesome Music", "Broadcast Music", "World")
	r := h.ok("publishers", "create", "--id", "P-12", "--legal-name", "New Pub", "--pro", "10", "--territory", "1", "--territory", "2", "--notes", "@"+h.write("notes.txt", "From a file"))
	want(t, r.stdout, "legal_name:", "New Pub", "From a file", "United States")
	h.fails(ExitInvalid, "Pro must exist", "publishers", "create", "--id", "P-13", "--legal-name", "No PRO")

	r = h.ok("publishers", "alt-names", "add", "P-12", "--name", "NEW PUB LTD")
	want(t, r.stderr, `Added "NEW PUB LTD" to publisher P-12.`)
	id := decode[map[string]any](t, h.ok("publishers", "alt-names", "add", "P-12", "--name", "N.P.", "-o", "json"))["id"]
	want(t, h.ok("publishers", "get", "P-12").stdout, "NEW PUB LTD", "N.P.")
	h.ok("publishers", "alt-names", "remove", "P-12", strings.TrimSuffix(strings.TrimSuffix(jsonNumber(id), ".0"), ""), "--yes")
	unwanted(t, h.ok("publishers", "get", "P-12").stdout, "N.P.")

	sheet := h.write("publishers.csv", "name,pro,ipi,controlled\nSheet Pub,BMI,,true\nBad Pub,NOPE,,\n")
	r = h.fails(ExitInvalid, "Some rows could not be imported", "publishers", "import-sheet", sheet)
	want(t, r.stdout, `"created": 1`, "unknown PRO")
	want(t, h.ok("publishers", "list").stdout, "Sheet Pub")
	want(t, h.ok("publishers", "import-sheet", h.write("ok.csv", "name,pro\nAnother,ASCAP\n")).stderr, "The sheet was imported.")
}

func jsonNumber(v any) string {
	switch n := v.(type) {
	case float64:
		return strings.TrimSuffix(strings.TrimSuffix(strings.TrimRight(fmtFloat(n), "0"), "."), "")
	}
	return ""
}

func fmtFloat(n float64) string {
	return strings.TrimRight(strings.TrimRight(strconvFormat(n), "0"), ".")
}

func TestCatalogs(t *testing.T) {
	h := signedIn(t)
	want(t, h.ok("catalogs", "list").stdout, "C-1", "Film scores", "WORKS", "1")
	// The show endpoint wraps the record.
	r := h.ok("catalogs", "create", "--id", "C-2", "--name", "Jingles")
	want(t, r.stderr, "Created catalog C-2.")
	want(t, r.stdout, "external_id:", "C-2", "name:", "Jingles")
	unwanted(t, r.stdout, "catalog:")
	catalog := decode[map[string]any](t, h.ok("catalogs", "get", "C-2", "-o", "json"))
	if catalog["name"] != "Jingles" {
		t.Errorf("unexpected catalog: %v", catalog)
	}
	// Its validation errors come without a message.
	h.fails(ExitInvalid, "Name can't be blank", "catalogs", "update", "C-2", "--name", "")
	h.ok("catalogs", "delete", "C-2", "--yes")
}

func TestWorks(t *testing.T) {
	h := signedIn(t)
	want(t, h.ok("works", "list").stdout, "TITLE", "LANGUAGE", "W-999", "Drunken Daisy", "English", "W-1001", "Highway Windows Down")
	want(t, h.ok("works", "list", "--title", "daisy").stdout, "Drunken Daisy")
	unwanted(t, h.ok("works", "list", "--title", "daisy").stdout, "Highway")

	r := h.ok("works", "get", "W-999")
	want(t, r.stdout, "title:", "Drunken Daisy", "registration_codes:", "123456789", "ASCAP Work ID", "publishers:", "Mike's Awesome Music", "writers:", "Ilze Platais")

	r = h.ok("works", "create", "--id", "W-2000", "--title", "New Work", "--language", "2", "--code", "3:987654321", "--alt-title", "1:A New Work")
	want(t, r.stderr, "Created work W-2000.")
	want(t, r.stdout, "French", "987654321", "A New Work")
	body := h.lastBody("POST", "/api/v1/companies/2/works")["work"].(map[string]any)
	codes := body["registration_codes_attributes"].([]any)
	if len(codes) != 1 || codes[0].(map[string]any)["registration_type_id"] != float64(3) || codes[0].(map[string]any)["code"] != "987654321" {
		t.Errorf("unexpected codes: %v", codes)
	}
	h.fails(ExitUsage, "--code takes ID:CODE", "works", "create", "--id", "W-3", "--title", "T", "--code", "nope")
	h.fails(ExitInvalid, "Title can't be blank", "works", "create", "--id", "W-3")

	h.ok("works", "update", "W-1001", "--code", "3:111")
	want(t, h.ok("works", "get", "W-1001").stdout, "111")
	h.ok("works", "update", "W-2000", "--data", `{"work": {"title": "Renamed"}}`)
	want(t, h.ok("works", "get", "W-2000").stdout, "Renamed")
}

func TestImport(t *testing.T) {
	h := signedIn(t)
	file := h.write("writers.json", `[
	  {"external_id": "W-186", "first_name": "Ilze E.", "last_name": "Platais"},
	  {"id": "W-500", "last_name": "New"},
	  {"writer": {"external_id": "W-501", "last_name": "Wrapped"}}
	]`)
	r := h.ok("writers", "import", file)
	want(t, r.stderr, "Loaded 3 of 3 writers: 2 created, 1 updated, 0 failed.")
	want(t, h.ok("writers", "list").stdout, "Ilze E. Platais", "New", "Wrapped")

	r = h.fails(ExitError, "record 1 (writer W-186): External has already been taken", "writers", "import", "--mode", "create", file)
	want(t, r.stderr, "Loaded 0 of 3 writers: 0 created, 0 updated, 3 failed.")

	h.stdin = strings.NewReader(`{"external_id": "W-600", "last_name": "Lines"}` + "\n" + `{"external_id": "W-601", "last_name": "More"}` + "\n")
	h.fails(ExitError, "0 created, 0 updated, 2 failed", "writers", "import", "-", "--mode", "update")

	h.fails(ExitError, "record 2 has no external_id", "writers", "import", h.write("bad.json", `[{"external_id": "W-1", "last_name": "A"}, {"last_name": "B"}]`))
	want(t, h.ok("writers", "import", "--dry-run", file).stderr, "3 writers are ready to load. Nothing was sent.")
	h.fails(ExitUsage, "--mode must be", "writers", "import", "--mode", "replace", file)

	// What list writes, import reads.
	h.stdin = strings.NewReader(h.ok("works", "list", "--all", "-o", "jsonl").stdout)
	want(t, h.ok("works", "import", "-").stderr, "0 created, 2 updated, 0 failed")
}

func TestAgreements(t *testing.T) {
	h := signedIn(t)
	want(t, h.ok("subpublishing-agreements", "list").stderr, "No subpublishing agreements.")
	r := h.ok("subpublishing-agreements", "create", "--assignor", "P-2", "--assignee", "P-40", "--number", "SP-1", "--starts-on", "2024-01-01", "--pr-share", "50", "--mr-share", "50", "--territories", `[{"tis_territory_id": 2, "inclusion_status": "include"}]`)
	want(t, r.stdout, "SP-1", "Mike's Awesome Music", "Euro Sub Publishing", "United States")
	h.fails(ExitUsage, "--starts-on must be a date", "subpublishing-agreements", "create", "--assignor", "P-2", "--assignee", "P-40", "--starts-on", "yesterday")
	h.fails(ExitNotFound, "Not found", "subpublishing-agreements", "create", "--assignor", "P-2", "--assignee", "P-99", "--starts-on", "2024-01-01")
	want(t, h.ok("subpublishing-agreements", "list", "--assignor", "P-2").stdout, "SP-1")
	want(t, h.ok("subpublishing-agreements", "list", "--assignor", "P-40").stderr, "No subpublishing agreements match.")

	r = h.ok("admin-agreements", "create", "--assignor", "P-2", "--assignee", "P-40", "--number", "AD-1", "--starts-on", "2024-01-01", "--fee-share", "15")
	want(t, r.stdout, "AD-1", "admin_fee_share:", "15")
	id := strings.TrimSpace(h.ok("admin-agreements", "list", "-o", "jsonl").stdout)
	_ = id
	h.fails(ExitUsage, "--fee-share must be a number", "admin-agreements", "create", "--assignor", "P-2", "--assignee", "P-40", "--starts-on", "2024-01-01", "--fee-share", "lots")
}
