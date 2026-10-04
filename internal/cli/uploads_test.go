package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWorksUpload(t *testing.T) {
	h := signedIn(t)
	want(t, h.ok("works-uploads", "list").stderr, "No works uploads.")
	csv := h.write("catalog.csv", "title,writer\nOne,A\nTwo,B\n")

	r := h.ok("works-uploads", "create", "--file", csv, "--format", "1", "--notes", "First load")
	want(t, r.stderr, "Uploaded catalog.csv as works upload", "The server is processing it.")
	want(t, r.stdout, "status:", "pending", "First load", "Standard")
	req := h.lastRequest("POST", "/api/v1/companies/2/works_uploads")
	if !strings.HasPrefix(req.ContentType, "multipart/form-data") || req.Headers.Get("Idempotency-Key") == "" {
		t.Errorf("content type %q, key %q", req.ContentType, req.Headers.Get("Idempotency-Key"))
	}
	for _, part := range []string{`name="works_upload[notes]"`, `name="works_upload[works_file_format_id]"`, `name="works_upload[csv_file]"; filename="catalog.csv"`} {
		if !strings.Contains(string(req.Body), part) {
			t.Errorf("multipart body lacks %s:\n%s", part, req.Body)
		}
	}

	r = h.ok("works-uploads", "list")
	want(t, r.stdout, "FORMAT", "STATUS", "Standard", "pending")
	// The server finishes in the background; the next look sees it.
	want(t, h.ok("works-uploads", "get", idFrom(t, h.ok("works-uploads", "list", "-o", "jsonl").stdout)).stdout, "completed")

	h.fails(ExitError, `no PRO is abbreviated "sacem"`, "works-uploads", "create", "--file", csv, "--format", "4", "--pro", "sacem", "--match-by-title", "--codes-only", "--wait")
	r = h.ok("works-uploads", "create", "--file", csv, "--format", "4", "--pro", "ascap", "--match-by-title", "--codes-only", "--wait", "-o", "json")
	upload := decode[map[string]any](t, r)
	if upload["status"] != "completed" || upload["match_by_title"] != "true" || upload["report_file_available"] != true {
		t.Errorf("unexpected upload: %v", upload)
	}
	id := strings.TrimSuffix(strings.TrimRight(strconvFormat(upload["id"].(float64)), "0"), ".")
	r = h.ok("works-uploads", "report", id)
	want(t, r.stdout, "row,title,matched")
	r = h.ok("works-uploads", "download", id)
	want(t, r.stdout, "title,writer")

	dir := t.TempDir()
	cwd, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(cwd)
	r = h.ok("works-uploads", "download", id, "--save")
	want(t, r.stderr, "Saved catalog.csv")
	if _, err := os.Stat(filepath.Join(dir, "catalog.csv")); err != nil {
		t.Error(err)
	}
	h.ok("works-uploads", "delete", id, "--yes")
}

func TestUploadIsIdempotent(t *testing.T) {
	h := signedIn(t)
	csv := h.write("catalog.csv", "title\nOne\n")
	first := decode[map[string]any](t, h.ok("works-uploads", "create", "--file", csv, "--format", "1", "--idempotency-key", "abc", "-o", "json"))
	r := h.ok("works-uploads", "create", "--file", csv, "--format", "1", "--idempotency-key", "abc", "-o", "json")
	want(t, r.stderr, "The server had this upload already")
	if second := decode[map[string]any](t, r); second["id"] != first["id"] {
		t.Errorf("a second upload was made: %v and %v", first["id"], second["id"])
	}
	if n := len(h.server.Company(2).WorksUploads); n != 1 {
		t.Errorf("%d uploads", n)
	}
}

func TestUploadFailureIsReported(t *testing.T) {
	h := signedIn(t)
	h.server.FailUploads = true
	csv := h.write("catalog.csv", "title\nOne\n")
	r := h.fails(ExitError, "Processing failed", "works-uploads", "create", "--file", csv, "--format", "1", "--wait")
	want(t, r.stdout, "status:", "failed", "error_list:", "Row 2")

	h.server.FailUploads = false
	h.fails(ExitInvalid, "Works file format id must exist", "works-uploads", "create", "--file", csv)
	h.fails(ExitUsage, "--file is required", "works-uploads", "create")
	h.fails(ExitError, "no such file", "works-uploads", "create", "--file", "/nope.csv", "--format", "1")
}

func TestSalesUpload(t *testing.T) {
	h := signedIn(t)
	csv := h.write("sales.csv", "work,film\nW-999,Big Movie\n")
	r := h.ok("sales-uploads", "create", "--file", csv, "--wait")
	want(t, r.stdout, "status:", "completed", "sales.csv")
	id := idFrom(t, h.ok("sales-uploads", "list", "-o", "jsonl").stdout)
	want(t, h.ok("sales-uploads", "sales", id).stderr, "No sales.")
	want(t, h.ok("sales-uploads", "download", id).stdout, "work,film")
}

func TestRoyaltyStatements(t *testing.T) {
	h := signedIn(t)
	r := h.ok("royalty-statements", "list")
	want(t, r.stdout, "SOURCE", "LINES", "31", "ASCAP Domestic", "ASCAP CSV", "2", "completed")
	h.fails(ExitUsage, "--source is required", "royalty-statements", "create", "--file", h.write("s.csv", "a\n1\n"))

	r = h.ok("royalty-statements", "create", "--file", h.write("bmi.csv", "line,amount\n1,2\n"), "--source", "BMI", "--format", "2", "--wait")
	want(t, r.stdout, "status:", "completed", "royalty_source:", "BMI")
	body := string(h.lastRequest("POST", "/api/v1/companies/2/royalty_statements").Body)
	want(t, body, `name="royalty_statement[royalty_source_id]"`, "\r\n\r\n2\r\n")
	h.fails(ExitError, `no royalty source is named "SACEM"`, "royalty-statements", "create", "--file", h.write("s.csv", "a\n"), "--source", "SACEM")

	r = h.ok("royalty-statements", "streamers", "31")
	want(t, r.stderr, "1 of 2 lines matched a streamer")
	want(t, r.stdout, "STREAMER", "MATCHED", "Hulu Plus", "Netflix")
	if got := lines(r.stdout); !strings.Contains(got[1], "Hulu Plus") {
		t.Errorf("unmatched names should come first:\n%s", r.stdout)
	}

	want(t, h.ok("royalty-statements", "records", "31").stdout, "9001", "9002")
	want(t, h.ok("royalty-statements", "records", "31", "--issues", "1").stdout, "9002")
	want(t, h.ok("royalty-statements", "download", "31").stdout, "line,amount")

	r = h.ok("royalty-statements", "reprocess", "31", "--wait")
	want(t, r.stderr, "Royalty statement 31 is being processed again.", "Processing completed")
	h.fails(ExitUsage, `unknown command "delete"`, "royalty-statements", "delete", "31")
}

func TestProDataDumps(t *testing.T) {
	h := signedIn(t)
	h.fails(ExitInvalid, "Pro must exist", "pro-data-dumps", "create", "--file", h.write("d.csv", "a\n"))
	r := h.ok("pro-data-dumps", "create", "--file", h.write("bmi.csv", "work,code\nW-999,1\n"), "--pro", "BMI", "--wait")
	want(t, r.stdout, "pro:", "BMI", "completed")
	want(t, h.ok("pro-data-dumps", "list").stdout, "PRO", "BMI")
}

// idFrom reads the id of the first record of JSON Lines output.
func idFrom(t *testing.T, jsonl string) string {
	t.Helper()
	first := lines(jsonl)[0]
	record := decode[map[string]any](t, result{stdout: first})
	return strings.TrimSuffix(strings.TrimRight(strconvFormat(record["id"].(float64)), "0"), ".")
}
