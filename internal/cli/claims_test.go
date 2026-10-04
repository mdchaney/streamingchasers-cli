package cli

import (
	"strings"
	"testing"
)

func TestPeriodsAndUnpaid(t *testing.T) {
	h := signedIn(t)
	h.fails(ExitUsage, "no PRO selected", "periods", "list")
	r := h.ok("periods", "list", "--pro", "ascap")
	want(t, r.stdout, "NAME", "AVAILABLE", "12", "2026 Q1", "yes", "11", "2025 Q4", "9")
	r = h.ok("unpaid", "list", "--pro", "ASCAP")
	want(t, r.stdout, "7001", "W-999", "Drunken Daisy", "123456789", "Big Movie", "Netflix", "7002", "7004")
	// A work without a code for the PRO is not claimable.
	unwanted(t, r.stdout, "7003")
	want(t, h.ok("unpaid", "list", "--pro", "BMI").stderr, "No unpaid placements.")
}

func TestClaimsLoop(t *testing.T) {
	h := signedIn(t)
	r := h.ok("batches", "missing-works", "--pro", "ASCAP")
	want(t, r.stderr, "Codes for ASCAP are registration type 3, ASCAP Work ID (the ASCAP Work ID).")
	want(t, r.stdout, "W-1001", "Highway Windows Down")

	h.fails(ExitUsage, "--period must name a payment period", "batches", "preview", "--pro", "ASCAP")
	r = h.ok("batches", "preview", "--pro", "ASCAP", "--period", "12")
	want(t, r.stderr, "A batch for 2026 Q1 would hold 2 rows. Left out: 1 for missing codes, 1 rolled up.", "This would be the first batch", `rolled up: Series "Bundle Show", 1 rows`)
	want(t, r.stdout, "7001", "7002")
	unwanted(t, r.stdout, "7003", "7004")

	// Thresholds narrow the preview the same way they narrow a batch.
	r = h.ok("batches", "preview", "--pro", "ASCAP", "--period", "12", "--min-money", "500")
	want(t, r.stderr, "would hold 1 rows")

	h.fails(ExitUsage, "pass --yes", "batches", "create", "--pro", "ASCAP", "--period", "12")
	r = h.ok("batches", "create", "--pro", "ASCAP", "--period", "12", "--notes", "Q1", "--exclude", "7002", "--yes")
	want(t, r.stderr, "Made batch", "with 1 rows. Left out: 1 excluded by you, 1 for missing codes, 1 rolled up.", "streamingchasers batches csv")
	want(t, r.stdout, "added_count:", "1")
	body := h.lastBody("POST", "/api/v1/companies/2/pros/10/broadcast_delivery_batches")
	if excluded := body["excluded_broadcasts_pros_sale_ids"].([]any); len(excluded) != 1 || excluded[0] != float64(7002) {
		t.Errorf("unexpected body: %v", body)
	}

	// The period and every earlier one are now taken.
	r = h.ok("periods", "list", "--pro", "ASCAP", "--available")
	want(t, r.stderr, "No payment periods match.")
	r = h.fails(ExitConflict, "2025 Q4 is no longer available - a batch already exists for this or a later ASCAP payment period.", "batches", "create", "--pro", "ASCAP", "--period", "11", "--yes")
	h.fails(ExitConflict, "no longer available", "batches", "preview", "--pro", "ASCAP", "--period", "11")

	r = h.ok("batches", "list")
	want(t, r.stdout, "PRO", "PERIOD", "ROWS", "ASCAP", "2026 Q1", "1", "Q1")
	id := idFrom(t, h.ok("batches", "list", "-o", "jsonl").stdout)
	want(t, h.ok("batches", "get", id).stdout, "payment_period:", "2026 Q1", "broadcasts_count:")
	want(t, h.ok("batches", "list", "--pro", "BMI").stderr, "No batches match.")

	r = h.ok("batches", "csv", id)
	want(t, r.stdout, "work,code,production,aired,notes", "W-999,123456789,Big Movie")
	want(t, r.stderr, "Batch "+id+" is now marked as sent.")
	want(t, h.ok("batches", "get", id, "-o", "json").stdout, `"sent_at": "20`)
	r = h.ok("batches", "csv", id, "--no-notes", "--sort", "chase-money")
	want(t, r.stdout, "work,code,production,aired\n")
	if req := h.lastRequest("GET", "/api/v1/companies/2/broadcast_delivery_batches/"+id+"/csv"); !strings.Contains(req.Query, "with_notes=0") || !strings.Contains(req.Query, "sort=chase_money") {
		t.Errorf("query = %q", req.Query)
	}
	r = h.ok("batches", "csv", id, "--variant", "missing-codes")
	want(t, r.stdout, "W-1001")
	unwanted(t, r.stderr, "marked as sent")
	h.fails(ExitUsage, "--variant must be missing-codes", "batches", "csv", id, "--variant", "x")

	want(t, h.ok("batches", "missing-codes", "--pro", "ASCAP", "--period", "12").stdout, "work,title,production", "W-1001")
}

func TestChaseScores(t *testing.T) {
	h := signedIn(t)
	r := h.ok("chase", "list", "--pro", "ASCAP")
	want(t, r.stderr, "Sorted by money. Hidden: 1 rolled up, 1 fully paid.")
	want(t, r.stdout, "SERIES", "TITLE", "MONEY", "UNPAID", "500", "Big Series", "120.5", "2")
	unwanted(t, r.stdout, "Bundle Show", "Paid Off")
	h.ok("chase", "list", "--pro", "ASCAP", "--sort", "unpaid")
	if req := h.lastRequest("GET", "/api/v1/companies/2/pros/10/series_chase_scores"); !strings.Contains(req.Query, "sort=unpaid") {
		t.Errorf("query = %q", req.Query)
	}
	want(t, h.ok("chase", "get", "500", "--pro", "ASCAP").stdout, "series:", "Big Series", "money_observed:")
	h.fails(ExitNotFound, "Not found", "chase", "get", "999", "--pro", "ASCAP")
	want(t, h.ok("chase", "placements", "500", "--pro", "ASCAP").stdout, "EPISODE", "WORK", "Pilot", "Drunken Daisy", "Hulu")
	want(t, h.ok("chase", "recompute", "--pro", "ASCAP").stderr, "Recompute enqueued for Frivolous Music.")
}

func TestRollups(t *testing.T) {
	h := signedIn(t)
	r := h.ok("rollups", "list", "--pro", "ASCAP")
	want(t, r.stderr, "1 productions rolled up by ASCAP, blocking 1 unpaid placements across 2 rollup lines.")
	want(t, r.stdout, "TYPE", "TITLE", "UNPAID BLOCKED", "Series", "600", "Bundle Show", "55.25")
	r = h.ok("rollups", "get", "series", "600", "--pro", "ASCAP")
	want(t, r.stdout, "Series 600: Bundle Show", "Rolled up by ASCAP: 2 lines, 55.25 USD", "WORK", "W-999", "Drunken Daisy")
	h.fails(ExitUsage, "TYPE must be Movie, Series or Episode", "rollups", "get", "Album", "1", "--pro", "ASCAP")
	h.fails(ExitNotFound, "Not found", "rollups", "get", "Movie", "1", "--pro", "ASCAP")
	want(t, h.ok("rollups", "list", "--pro", "BMI").stderr, "No rolled-up productions.")
}
