package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSales(t *testing.T) {
	h := signedIn(t)
	r := h.ok("sales", "list")
	want(t, r.stdout, "WORK", "FILM", "SERIES", "EPISODE", "4411", "W-999", "Big Movie", "4412", "Big Series", "Pilot")
	want(t, h.ok("sales", "list", "--production", "series").stdout, "4412")
	unwanted(t, h.ok("sales", "list", "--production", "series").stdout, "4411")
	want(t, h.ok("sales", "get", "4411").stdout, "work:", "Drunken Daisy", "production:", "Big Movie")

	r = h.ok("sales", "create", "--work", "W-999", "--film-title", "Another Movie", "--film-imdb", "tt0000301", "--release-date", "2025-02-02")
	want(t, r.stderr, "Created sale")
	want(t, r.stdout, "Another Movie", "Drunken Daisy")
	h.fails(ExitNotFound, "Not found", "sales", "create", "--work", "W-99", "--film-title", "X")
	want(t, h.ok("sales", "create", "--work-id", "W-1001", "--film-title", "Spelled the other way").stdout, "Highway Windows Down")
	h.fails(ExitInvalid, "Work must exist", "sales", "create", "--film-title", "X")
	h.ok("sales", "update", "4411", "--episode-number", "7")
	want(t, h.ok("sales", "get", "4411").stdout, "original_full_episode_number:", "7")
	h.ok("sales", "delete", "4411", "--yes")
	h.fails(ExitNotFound, "Not found", "sales", "get", "4411")

	want(t, h.ok("sales", "lookup-production", "tt0000300").stdout, "Big Movie", "Movie")
	h.fails(ExitNotFound, `no production is known by IMDB ID "tt9"`, "sales", "lookup-production", "tt9")
	want(t, h.ok("sales", "lookup-work", "W-999").stdout, "Drunken Daisy")
}

func TestPaidSalesCSV(t *testing.T) {
	h := signedIn(t)
	r := h.ok("sales", "paid-csv")
	want(t, r.stdout, "work,title,ASCAP,BMI", "W-999")
	r = h.ok("sales", "paid-csv", "--pro", "ascap")
	want(t, r.stdout, "Netflix,Hulu")
	if req := h.lastRequest("GET", "/api/v1/companies/2/sales/paid/pro/10"); req.Headers.Get("Accept") != "*/*" {
		t.Errorf("Accept = %q", req.Headers.Get("Accept"))
	}

	dir := t.TempDir()
	out := filepath.Join(dir, "paid.csv")
	r = h.ok("sales", "paid-csv", "--save-as", out)
	want(t, r.stderr, "Saved "+out)
	if data, err := os.ReadFile(out); err != nil || !strings.Contains(string(data), "W-999") {
		t.Errorf("file: %v %q", err, data)
	}
	h.fails(ExitError, `no PRO is abbreviated "sacem"`, "sales", "paid-csv", "--pro", "sacem")
}

func TestRoyaltyRecords(t *testing.T) {
	h := signedIn(t)
	r := h.ok("royalty-records", "list")
	want(t, r.stdout, "STATEMENT", "LINE", "NET", "CATEGORY", "9001", "Drunken Daisy", "Netflix", "12.50", "streaming_av", "9002", "Unknown Tune", "tv, flow-through", "yes")
	unwanted(t, r.stdout, "13.00")
	want(t, h.ok("royalty-records", "list", "--issues", "1").stdout, "9002")
	unwanted(t, h.ok("royalty-records", "list", "--issues", "1").stdout, "9001")
	want(t, h.ok("royalty-records", "list", "--streamer", "hulu").stdout, "9002")
	if req := h.lastRequest("GET", "/api/v1/companies/2/raw_royalty_records"); !strings.Contains(req.Query, "q%5Bstreamer_name%5D=hulu") {
		t.Errorf("query = %q", req.Query)
	}

	// The list carries the net and gross amounts; the rest of the money,
	// the distribution period and the interested parties are in get.
	r = h.ok("royalty-records", "list", "-o", "json")
	want(t, r.stdout, `"net_amount": "12.50"`, `"gross_amount": "13.00"`, `"record_category": "streaming_av"`, `"flow_through": true`)
	unwanted(t, r.stdout, "deduction_amount", "owner_name", "payment_year")
	want(t, h.ok("royalty-records", "get", "9001").stdout, "iswc:", "T-123456789-0", "net_amount:", "deduction_amount:", "0.50", "payment_year:", "2025", "owner_name:", "Frivolous Music")
}
