// Package fakeserver is an in-memory stand-in for the Streaming Chasers
// Rails application, for tests.  It mirrors the behavior of /api/v1 and
// of the OAuth endpoints closely enough that the CLI cannot tell the
// difference: the same status codes, the same error bodies, the same
// pagination and the same rules about who may do what.
//
// The Rails application is the specification.  When the two disagree the
// fake is wrong.
package fakeserver

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Roles, weakest first.
const (
	Viewer = "viewer"
	Editor = "editor"
	Admin  = "admin"
	Owner  = "owner"
)

// APIResource is the path of the REST API as an OAuth protected resource.
const APIResource = "/api/v1"

type record = map[string]any

// User is an account.
type User struct {
	ID       int64
	Name     string
	Email    string
	APIToken string
	Password string
}

// Upload is a file the server took to process in the background: a works
// upload, a sales upload, a royalty statement or a PRO data dump.
type Upload struct {
	Fields record
	File   []byte
	Name   string
	// Report is the match report of a works upload, if it has one.
	Report []byte
	// Polls is how many times the record has been shown since it was
	// made; it is processed once that reaches the server's PollsToFinish.
	Polls int
	Key   string
}

// Company is a tenant.
type Company struct {
	ID          int64
	Name        string
	Description string
	Owner       int64
	// Roles maps a user ID to the user's role in the company.
	Roles           map[int64]string
	Writers         []record
	Publishers      []record
	Catalogs        []record
	Works           []record
	Sales           []record
	AltNames        []record
	SubAgreements   []record
	AdminAgreements []record

	WorksUploads      []*Upload
	SalesUploads      []*Upload
	RoyaltyStatements []*Upload
	ProDataDumps      []*Upload
	RawRoyaltyRecords []record

	// Unpaid are the claimable broadcasts_pros_sales rows, by PRO ID.
	Unpaid      map[int64][]record
	Batches     []record
	ChaseScores map[int64][]record
	Rollups     map[int64][]record
	Connections []record
	CwrFiles    []record
	CwrAcks     []record
	CwrTickets  []record
}

// Request is a request the server received.
type Request struct {
	Method        string
	Path          string
	Query         string
	Authorization string
	UserAgent     string
	ContentType   string
	Headers       http.Header
	Body          []byte
}

// Server is the fake.  Its exported fields may be set before requests
// are made; use Lock to change them, or the data, while it is serving.
type Server struct {
	*httptest.Server

	mu sync.Mutex

	// Scopes are the scopes the authorization server supports.
	Scopes []string
	// SignedIn is the user whose browser visits the authorization page.
	SignedIn *User
	// Deny makes the user refuse the authorization request.
	Deny bool
	// AccessTokenTTL is how long access tokens last.
	AccessTokenTTL time.Duration
	// Fail, if set, is consulted before every request; a non-zero status
	// is sent instead of handling the request.
	Fail func(r *http.Request) int
	// PollsToFinish is how many times an upload is shown before the
	// background processing is done; FailUploads makes it fail.
	PollsToFinish int
	FailUploads   bool
	// SendFails makes sending a CWR file fail at the PRO server.
	SendFails bool

	Users     []*User
	Companies []*Company
	Pros      []record
	// PaymentPeriods are the payment periods, by PRO ID.
	PaymentPeriods map[int64][]record
	Reference      map[string][]record

	clients map[string]*oauthClient
	// clientOrder is the clients' IDs in the order they registered.
	clientOrder []string
	grants      map[string]*oauthGrant
	tokens      []*oauthToken

	requests []Request
	nextID   int64
}

// New starts a server with a small catalog: three users, the reference
// data, and a company the first user owns, the second may only view and
// the third may edit.
func New() *Server {
	s := &Server{
		Scopes:         []string{"catalog", "catalog_write", "catalog_admin"},
		AccessTokenTTL: time.Hour,
		PollsToFinish:  1,
		clients:        map[string]*oauthClient{},
		grants:         map[string]*oauthGrant{},
		nextID:         1000,
	}
	s.seed()
	s.Server = httptest.NewServer(http.HandlerFunc(s.serve))
	return s
}

// Lock locks the server's state for a test to read or change it.
func (s *Server) Lock() { s.mu.Lock() }

// Unlock releases Lock.
func (s *Server) Unlock() { s.mu.Unlock() }

// Requests returns the requests received so far.
func (s *Server) Requests() []Request {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Request(nil), s.requests...)
}

// ResetRequests forgets the requests received so far.
func (s *Server) ResetRequests() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.requests = nil
}

// CountRequests returns how many requests matched method and path prefix.
func (s *Server) CountRequests(method, prefix string) int {
	n := 0
	for _, r := range s.Requests() {
		if r.Method == method && strings.HasPrefix(r.Path, prefix) {
			n++
		}
	}
	return n
}

// Company returns the company with the given ID, or nil.
func (s *Server) Company(id int64) *Company {
	for _, company := range s.Companies {
		if company.ID == id {
			return company
		}
	}
	return nil
}

func (s *Server) id() int64 {
	s.nextID++
	return s.nextID
}

const seedTime = "2024-08-19T13:30:26.166-05:00"

func (s *Server) seed() {
	owner := &User{ID: 1, Name: "Floyd Lawson", Email: "floyd@example.com", APIToken: "floyd-api-token", Password: "secret"}
	viewer := &User{ID: 2, Name: "Old Man Crump", Email: "crump@example.com", APIToken: "crump-api-token"}
	editor := &User{ID: 3, Name: "Barney Fife", Email: "barney@example.com", APIToken: "barney-api-token"}
	s.Users = []*User{owner, viewer, editor}
	s.SignedIn = owner

	s.Pros = []record{
		{"id": 10, "abbreviation": "ASCAP", "name": "American Society of Composers, Authors and Publishers", "instructions": "Email the sheet to claims@ascap.example.", "created_at": seedTime, "updated_at": seedTime},
		{"id": 21, "abbreviation": "BMI", "name": "Broadcast Music, Inc.", "instructions": nil, "created_at": seedTime, "updated_at": seedTime},
		{"id": 52, "abbreviation": "PRS", "name": "PRS for Music", "instructions": nil, "created_at": seedTime, "updated_at": seedTime},
		{"id": 35, "abbreviation": "GEMA", "name": "GEMA", "instructions": nil, "created_at": seedTime, "updated_at": seedTime},
	}
	s.PaymentPeriods = map[int64][]record{
		10: {
			{"id": 12, "name": "2026 Q1", "report_date": "2026-03-31", "start_date": "2026-01-01", "end_date": "2026-03-31"},
			{"id": 11, "name": "2025 Q4", "report_date": "2025-12-31", "start_date": "2025-10-01", "end_date": "2025-12-31"},
			{"id": 9, "name": "2025 Q3", "report_date": "2025-09-30", "start_date": "2025-07-01", "end_date": "2025-09-30"},
		},
		21: {{"id": 20, "name": "2026 Q1", "report_date": "2026-03-31", "start_date": "2026-01-01", "end_date": "2026-03-31"}},
	}
	s.Reference = map[string][]record{
		"royalty_sources": {
			{"id": 1, "name": "ASCAP Domestic", "pro": "ASCAP", "pro_name": "American Society of Composers, Authors and Publishers", "royalty_file_formats": []any{record{"id": 1, "name": "ASCAP CSV"}}},
			{"id": 2, "name": "BMI", "pro": "BMI", "pro_name": "Broadcast Music, Inc.", "royalty_file_formats": []any{record{"id": 2, "name": "BMI Standard"}}},
		},
		"sales_file_formats": {
			{"id": 1, "name": "Generic", "description": "The standard layout", "format_type": "generic", "file_extension": "csv", "pro": nil, "active": true},
			{"id": 2, "name": "ASCAP Cue Sheet", "description": nil, "format_type": "ascap", "file_extension": "csv", "pro": "ASCAP", "active": true},
		},
		"works_file_formats": {
			{"id": 1, "name": "Standard", "description": "Title, writers, publishers", "requires_pro": false, "file_extension": "csv", "format_type": "standard"},
			{"id": 4, "name": "SACEM codes", "description": "Work codes from SACEM", "requires_pro": true, "file_extension": "csv", "format_type": "codes"},
		},
		"registration_types": {
			{"id": 3, "name": "ASCAP Work ID", "pro_id": 10, "work_id_description": "the ASCAP Work ID", "validation_regexp": "^\\d{9,}$"},
			{"id": 4, "name": "BMI Work ID", "pro_id": 21, "work_id_description": "the BMI Work #", "validation_regexp": nil},
		},
		"writer_designations":  {{"id": 1, "code": "C", "description": "Composer"}, {"id": 2, "code": "A", "description": "Author"}},
		"publisher_types":      {{"id": 1, "code": "E", "description": "Original publisher"}},
		"title_types":          {{"id": 1, "code": "AT", "description": "Alternative title", "definition": "Another title"}},
		"cis_languages":        {{"id": 1, "code": "EN", "name": "English"}, {"id": 2, "code": "FR", "name": "French"}},
		"tis_territories":      {{"id": 1, "tis_a": 2136, "tis_a_ext": nil, "name": "World"}, {"id": 2, "tis_a": 840, "tis_a_ext": nil, "name": "United States"}},
		"tis_territory_types":  {{"id": 1, "name": "Country", "abbreviation": "C"}},
		"production_companies": {{"id": 1, "name": "Big Studio"}},
		"streamers":            {{"id": 1, "name": "Netflix", "tracked": true}, {"id": 2, "name": "Hulu", "tracked": false}},
		"cwr_destinations":     {{"id": 2, "name": "ASCAP Delivery", "pro": "ASCAP"}},
		"movies":               {{"id": 300, "title": "Big Movie", "imdb_id": "tt0000300", "reelgood_id": nil}},
		"series":               {{"id": 500, "title": "Big Series", "imdb_id": "tt0000500", "reelgood_id": nil}, {"id": 600, "title": "Bundle Show", "imdb_id": "tt0000600", "reelgood_id": nil}},
		"episodes":             {{"id": 5001, "title": "Pilot", "imdb_id": "tt0005001", "reelgood_id": nil, "season_number": 1, "episode_number": 1, "series_id": 500}},
	}

	frivolous := &Company{
		ID: 2, Name: "Frivolous Music", Description: "Production music for film and television", Owner: owner.ID,
		Roles: map[int64]string{owner.ID: Owner, viewer.ID: Viewer, editor.ID: Editor},
		Writers: []record{
			{"external_id": "W-186", "first_name": "Ilze", "last_name": "Platais", "pseudonym": nil, "pro_id": 10, "ipi_name_number": 49448942, "ipi_base_number": nil, "controlled": true},
			{"external_id": "W-187", "first_name": "Neal", "last_name": "Busby", "pseudonym": nil, "pro_id": 21, "ipi_name_number": nil, "ipi_base_number": nil, "controlled": false},
		},
		Publishers: []record{
			{"external_id": "P-2", "legal_name": "Mike's Awesome Music", "trade_name": "MAM", "pro_id": 21, "mech_pro_id": nil, "ipi_name": "2389239892", "ipi_base_number": nil, "contact_name": "Mike", "contact_email": "mike@example.com", "controlled": true, "active": true, "notes": nil, "tis_territory_ids": []any{1}},
			{"external_id": "P-40", "legal_name": "Euro Sub Publishing", "trade_name": nil, "pro_id": 52, "mech_pro_id": nil, "ipi_name": nil, "ipi_base_number": nil, "contact_name": nil, "contact_email": nil, "controlled": false, "active": true, "notes": nil, "tis_territory_ids": []any{}},
		},
		Catalogs: []record{{"external_id": "C-1", "name": "Film scores"}},
		Works: []record{
			{"external_id": "W-999", "title": "Drunken Daisy", "cis_language_id": 1, "catalog": "Film scores",
				"works_alt_titles": []any{}, "registration_codes": []any{record{"id": 1, "registration_type_id": 3, "code": "123456789"}},
				"works_publishers": []any{record{"id": 1, "publisher_external_id": "P-2", "share": "100.0"}}, "works_writers": []any{record{"id": 1, "writer_external_id": "W-186", "share": "100.0"}}, "include_in_cwr": true},
			{"external_id": "W-1001", "title": "Highway Windows Down", "cis_language_id": nil, "catalog": nil,
				"works_alt_titles": []any{record{"id": 2, "title_type_id": 1, "title": "Windows Down"}}, "registration_codes": []any{},
				"works_publishers": []any{}, "works_writers": []any{record{"id": 2, "writer_external_id": "W-187", "share": "100.0"}}, "include_in_cwr": false},
		},
		Sales: []record{
			{"id": 4411, "line_number": 1, "work_external_id": "W-999", "original_film_title": "Big Movie", "film_imdb_id": "tt0000300", "original_series_title": nil, "series_imdb_id": nil, "original_episode_title": nil, "episode_imdb_id": nil, "original_full_episode_number": nil, "external_work_id": "DD-1", "original_work_title": "Drunken Daisy", "release_date": "2024-01-15", "original_production_company": "Big Studio", "production_type": "Movie", "production_id": 300, "production_title": "Big Movie", "sales_upload_id": nil},
			{"id": 4412, "line_number": 2, "work_external_id": "W-1001", "original_film_title": nil, "film_imdb_id": nil, "original_series_title": "Big Series", "series_imdb_id": "tt0000500", "original_episode_title": "Pilot", "episode_imdb_id": "tt0005001", "original_full_episode_number": 101, "external_work_id": nil, "original_work_title": nil, "release_date": nil, "original_production_company": nil, "production_type": "Episode", "production_id": 5001, "production_title": "Big Series: Pilot", "sales_upload_id": nil},
		},
		Unpaid: map[int64][]record{
			10: {
				{"id": 7001, "work_external_id": "W-999", "production_title": "Big Movie", "film_title": "Big Movie", "series_title": nil, "episode_title": nil, "episode_number": nil, "air_date": "2025-11-02", "streamer": "Netflix", "territory": "United States", "money": "80.5", "views": 12000, "series_id": nil},
				{"id": 7002, "work_external_id": "W-999", "production_title": "Big Series: Pilot", "film_title": nil, "series_title": "Big Series", "episode_title": "Pilot", "episode_number": 101, "air_date": "2026-01-10", "streamer": "Hulu", "territory": "United States", "money": "40.0", "views": 5000, "series_id": 500},
				{"id": 7003, "work_external_id": "W-1001", "production_title": "Big Series: Pilot", "film_title": nil, "series_title": "Big Series", "episode_title": "Pilot", "episode_number": 101, "air_date": "2026-01-10", "streamer": "Hulu", "territory": "United States", "money": "0", "views": 5000, "series_id": 500},
				{"id": 7004, "work_external_id": "W-999", "production_title": "Bundle Show: Ep 1", "film_title": nil, "series_title": "Bundle Show", "episode_title": "Ep 1", "episode_number": 101, "air_date": "2026-02-01", "streamer": "Netflix", "territory": "United States", "money": "0", "views": 100, "series_id": 600},
			},
		},
		ChaseScores: map[int64][]record{
			10: {
				{"series_id": 500, "series_title": "Big Series", "money_observed": "120.5", "views_observed": 17000, "views_estimated_missing": 3000, "views_total_est": 20000, "paid_broadcasts_count": 2, "unpaid_broadcasts_count": 2, "total_broadcasts_count": 4, "computed_at": seedTime},
				{"series_id": 600, "series_title": "Bundle Show", "money_observed": "10.0", "views_observed": 100, "views_estimated_missing": 0, "views_total_est": 100, "paid_broadcasts_count": 0, "unpaid_broadcasts_count": 1, "total_broadcasts_count": 1, "computed_at": seedTime},
				{"series_id": 601, "series_title": "Paid Off", "money_observed": "900.0", "views_observed": 90000, "views_estimated_missing": 0, "views_total_est": 90000, "paid_broadcasts_count": 9, "unpaid_broadcasts_count": 0, "total_broadcasts_count": 9, "computed_at": seedTime},
			},
		},
		Rollups: map[int64][]record{
			10: {{"production_type": "Series", "production_id": 600, "title": "Bundle Show", "rollup_records": 2, "total_amount": "55.25", "currencies": "USD", "first_usage_on": "2025-01-01", "last_usage_on": "2025-12-31", "unpaid_broadcasts_blocked": 1}},
		},
		Connections: []record{
			{"id": 3, "cwr_destination_id": 2, "destination_name": "ASCAP Delivery", "destination_pro": "ASCAP", "pro_id": 10, "server_username": "frivolous", "server_password": "hunter2", "outbound_directory": "/out", "inbound_directory": "/in", "cwr_sender_name": "FRIVOLOUS MUSIC", "cwr_sender_ipi_number": "123456789", "cwr_sender_filename_code": "FRV", "minimum_work_count": 1, "filename_serial_offset": 0, "active": true, "created_at": seedTime},
		},
		RoyaltyStatements: []*Upload{{
			Fields: record{"id": 31, "royalty_source_id": 1, "royalty_file_format_id": 1, "notes": "Q4 2025", "processed_at": seedTime, "success": true, "error_list": nil, "starts_at": "2025-10-01", "ends_at": "2025-12-31", "created_at": seedTime, "updated_at": seedTime, "user_id": 1},
			File:   []byte("line,amount\n1,12.50\n"), Name: "ascap-2025q4.csv", Polls: 99,
		}},
		RawRoyaltyRecords: []record{
			{"id": 9001, "royalty_statement_id": 31, "line_number": 1, "work_title": "Drunken Daisy", "iswc": "T-123456789-0", "work_code": "123456789", "series_title": nil, "episode_title": nil, "usage_start_date": "2025-10-01", "usage_end_date": "2025-12-31", "gross_amount": "12.50", "currency": "USD", "territory_code": "US", "streamer_name": "Netflix", "channel_name": nil, "is_ignored": false, "has_issues": false, "streamer_id": 1, "uses": 3},
			{"id": 9002, "royalty_statement_id": 31, "line_number": 2, "work_title": "Unknown Tune", "iswc": nil, "work_code": "999", "series_title": "Big Series", "episode_title": "Pilot", "usage_start_date": "2025-11-01", "usage_end_date": "2025-11-30", "gross_amount": "3.00", "currency": "USD", "territory_code": "US", "streamer_name": "Hulu Plus", "channel_name": nil, "is_ignored": false, "has_issues": true, "streamer_id": nil, "uses": 1},
		},
	}
	private := &Company{
		ID: 3, Name: "Soundmenders", Description: "A company the second user cannot see", Owner: owner.ID,
		Roles:  map[int64]string{owner.ID: Owner},
		Unpaid: map[int64][]record{}, ChaseScores: map[int64][]record{}, Rollups: map[int64][]record{},
	}
	s.Companies = []*Company{frivolous, private}
	for _, company := range s.Companies {
		for _, list := range [][]record{company.Writers, company.Publishers, company.Catalogs, company.Works, company.Sales} {
			for _, item := range list {
				stamp(item, true)
			}
		}
	}
}

// stamp sets the timestamps the way Active Record does.
func stamp(r record, created bool) {
	now := time.Now().Format("2006-01-02T15:04:05.000-07:00")
	if created {
		if _, ok := r["created_at"]; !ok {
			r["created_at"] = seedTime
			r["updated_at"] = seedTime
			return
		}
	}
	if _, ok := r["created_at"]; !ok {
		r["created_at"] = now
	}
	r["updated_at"] = now
}

func (s *Server) serve(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)

	s.mu.Lock()
	defer s.mu.Unlock()
	s.requests = append(s.requests, Request{
		Method: r.Method, Path: r.URL.EscapedPath(), Query: r.URL.RawQuery,
		Authorization: r.Header.Get("Authorization"), UserAgent: r.Header.Get("User-Agent"),
		ContentType: r.Header.Get("Content-Type"), Headers: r.Header.Clone(), Body: body,
	})

	if s.Fail != nil {
		if status := s.Fail(r); status != 0 {
			http.Error(w, http.StatusText(status), status)
			return
		}
	}

	path := r.URL.EscapedPath()
	switch {
	case path == "/.well-known/oauth-authorization-server":
		s.authorizationServerMetadata(w)
	case strings.HasPrefix(path, "/.well-known/oauth-protected-resource"):
		s.protectedResourceMetadata(w, r)
	case path == "/oauth/register":
		s.register(w, r, body)
	case path == "/oauth/authorize":
		s.authorize(w, r)
	case path == "/oauth/token":
		s.token(w, r, body)
	case path == "/oauth/revoke":
		s.revoke(w, r, body)
	case path == APIResource || strings.HasPrefix(path, APIResource+"/"):
		s.api(w, r, body)
	default:
		routingError(w)
	}
}

// routingError is how Rails answers a path that matches no route: with
// a page of HTML.
func routingError(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/html; charset=UTF-8")
	w.WriteHeader(http.StatusNotFound)
	fmt.Fprint(w, "<!DOCTYPE html><html><body><h1>Routing Error</h1></body></html>")
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v)
}

func message(w http.ResponseWriter, status int, text string) {
	writeJSON(w, status, record{"message": text})
}

func notFound(w http.ResponseWriter) {
	message(w, http.StatusNotFound, "Not found")
}

func forbidden(w http.ResponseWriter) {
	message(w, http.StatusForbidden, "Forbidden")
}

// invalid is api/shared/validation_errors: errors.to_hash(true), a map
// from attribute to its full messages.  The attribute is taken to be
// the first word of the message, in lower case; a message such as
// "Last name can't be blank" lands under "last", which is near enough
// for a client that reads the messages.
func invalid(w http.ResponseWriter, problems ...string) {
	errors := map[string][]string{}
	for _, problem := range problems {
		attribute := strings.ToLower(strings.SplitN(problem, " ", 2)[0])
		errors[attribute] = append(errors[attribute], problem)
	}
	writeJSON(w, http.StatusUnprocessableEntity, record{"message": "Validation failed", "errors": errors})
}

// toInt reads an integer the way Rails casts a parameter: from a JSON
// number or from a string of digits.
func toInt(v any) (int64, bool) {
	switch n := v.(type) {
	case float64:
		if n == float64(int64(n)) {
			return int64(n), true
		}
	case int:
		return int64(n), true
	case int64:
		return n, true
	case json.Number:
		i, err := n.Int64()
		return i, err == nil
	case string:
		i, err := strconv.ParseInt(strings.TrimSpace(n), 10, 64)
		return i, err == nil
	}
	return 0, false
}

func blank(v any) bool {
	switch value := v.(type) {
	case nil:
		return true
	case string:
		return strings.TrimSpace(value) == ""
	case []any:
		return len(value) == 0
	case record:
		return len(value) == 0
	}
	return false
}

func str(v any) string {
	if v == nil {
		return ""
	}
	return fmt.Sprint(v)
}

// paginate cuts list to the requested page and describes it the way the
// jbuilder views do.
func paginate(r *http.Request, list []record, defaultPerPage int) ([]record, record) {
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page < 1 {
		page = 1
	}
	perPage := defaultPerPage
	if perPage <= 0 {
		perPage = 25
	}
	if requested, _ := strconv.Atoi(r.URL.Query().Get("per_page")); requested > 0 {
		perPage = min(requested, 200)
	}

	total := len(list)
	pages := (total + perPage - 1) / perPage
	if pages < 1 {
		pages = 1
	}
	start := min((page-1)*perPage, total)
	end := min(start+perPage, total)
	pagination := record{"current_page": page, "total_pages": pages, "total_count": total, "per_page": perPage}
	out := list[start:end]
	if out == nil {
		out = []record{}
	}
	return out, pagination
}

func sortBy(list []record, key string) {
	sort.SliceStable(list, func(i, j int) bool {
		a, aok := toInt(list[i][key])
		b, bok := toInt(list[j][key])
		if aok && bok {
			return a < b
		}
		return strings.ToLower(str(list[i][key])) < strings.ToLower(str(list[j][key]))
	})
}

func copyRecord(r record) record {
	out := make(record, len(r))
	for k, v := range r {
		out[k] = v
	}
	return out
}

func contains(haystack, needle string) bool {
	return needle == "" || strings.Contains(strings.ToLower(haystack), strings.ToLower(needle))
}
