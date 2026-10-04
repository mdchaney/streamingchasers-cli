package cli

import (
	"strings"
	"testing"
)

func TestCwrConnections(t *testing.T) {
	h := signedIn(t)
	r := h.ok("cwr", "connections", "list")
	want(t, r.stdout, "DESTINATION", "PRO", "QUEUED", "3", "ASCAP Delivery", "ASCAP", "frivolous", "FRIVOLOUS MUSIC", "yes", "0")
	r = h.ok("cwr", "connections", "get", "3")
	want(t, r.stdout, "server_username:", "frivolous")
	unwanted(t, r.stdout, "hunter2", "password")

	r = h.ok("cwr", "connections", "create", "--destination", "2", "--username", "u", "--password", "@"+h.write("pw.txt", "s3cret"), "--inbound", "/in", "--sender-name", "FM", "--sender-ipi", "1")
	want(t, r.stderr, "Created CWR connection")
	if body := string(h.lastRequest("POST", "/api/v1/companies/2/cwr_connections").Body); !strings.Contains(body, `"server_password":"s3cret"`) {
		t.Errorf("body = %s", body)
	}
	h.fails(ExitInvalid, "Server username can't be blank", "cwr", "connections", "create", "--destination", "2", "--password", "x")
	id := idFrom(t, h.ok("cwr", "connections", "list", "-o", "jsonl").stdout)
	_ = id
	h.ok("cwr", "connections", "update", "3", "--active=false")
	want(t, h.ok("cwr", "connections", "get", "3").stdout, "active:", "false")
	want(t, h.ok("cwr", "connections", "download-acks", "3").stderr, "The acknowledgments for connection 3 are being fetched.")

	// A viewer may look, an editor may not change.
	want(t, as(t, viewerToken).ok("cwr", "connections", "list").stdout, "ASCAP Delivery")
	as(t, editorToken).fails(ExitForbidden, "Forbidden", "cwr", "connections", "delete", "3", "--yes")
}

func TestCwrWorkflow(t *testing.T) {
	h := signedIn(t)
	r := h.ok("cwr", "tickets", "candidates", "--connection", "3")
	want(t, r.stdout, "HAS PRO CODE", "W-999", "yes", "W-1001", "no")
	h.fails(ExitUsage, "--connection must name a CWR connection", "cwr", "tickets", "candidates")
	h.fails(ExitUsage, "--reason is required", "cwr", "tickets", "queue", "--connection", "3", "W-999")
	r = h.ok("cwr", "tickets", "queue", "--connection", "3", "--reason", "New catalog", "W-999", "W-1001")
	want(t, r.stderr, "Queued 2 of 2 works on connection 3.")
	want(t, h.ok("cwr", "connections", "list").stdout, "2")
	h.fails(ExitInvalid, "Name at least one unsent work", "cwr", "tickets", "queue", "--connection", "3", "--reason", "Again", "W-999")

	r = h.ok("cwr", "files", "candidates", "--connection", "3")
	want(t, r.stdout, "W-999", "Drunken Daisy", "New catalog", "W-1001")
	r = h.ok("cwr", "files", "create", "--connection", "3", "W-999", "W-1001")
	want(t, r.stderr, "Made CWR file", "with 2 works. Send it with 'streamingchasers cwr files send")
	want(t, r.stdout, "cwr_file_name:", "WORK", "Drunken Daisy", "Highway Windows Down")
	id := idFrom(t, h.ok("cwr", "files", "list", "--connection", "3", "-o", "jsonl").stdout)
	want(t, h.ok("cwr", "files", "list", "--connection", "3").stdout, "FILE", "WORKS", "2", "no")
	want(t, h.ok("cwr", "files", "download", id, "--connection", "3").stdout, "HDRPBFRIVOLOUS MUSIC")
	want(t, h.ok("cwr", "files", "candidates", "--connection", "3").stderr, "No works ready for a file.")

	want(t, h.ok("cwr", "files", "send", id, "--connection", "3").stderr, "Sent CWR file "+id+".")
	h.fails(ExitConflict, "This file was already sent", "cwr", "files", "send", id, "--connection", "3")
	h.fails(ExitInvalid, "Cannot delete a file that was sent", "cwr", "files", "delete", id, "--connection", "3", "--yes")
	h.fails(ExitConflict, "still has files", "cwr", "connections", "delete", "3", "--yes")

	h.server.SendFails = true
	other := h.ok("cwr", "files", "create", "--connection", "3", "-o", "json", "W-999")
	otherID := strings.TrimSuffix(strings.TrimRight(strconvFormat(decode[map[string]any](t, other)["id"].(float64)), "0"), ".")
	h.fails(ExitError, "Send failed", "cwr", "files", "send", otherID, "--connection", "3")
	h.ok("cwr", "files", "delete", otherID, "--connection", "3", "--yes")

	want(t, h.ok("cwr", "acks", "list", "--connection", "3").stderr, "No acknowledgment files.")
}

func TestCwrSingleTickets(t *testing.T) {
	h := signedIn(t)
	r := h.ok("cwr", "tickets", "add", "W-999", "--connection", "3", "--reason", "Fix")
	want(t, r.stderr, "Queued work W-999 on connection 3 as ticket")
	h.fails(ExitConflict, "already queued", "cwr", "tickets", "add", "W-999", "--connection", "3")
	ticket := decode[map[string]any](t, h.ok("cwr", "tickets", "add", "W-1001", "--connection", "3", "-o", "json"))
	id := strings.TrimSuffix(strings.TrimRight(strconvFormat(ticket["id"].(float64)), "0"), ".")
	want(t, h.ok("cwr", "tickets", "remove", "W-1001", id, "--yes").stderr, "Withdrew ticket")
	h.fails(ExitNotFound, "Not found", "cwr", "tickets", "remove", "W-1001", id, "--yes")
}
