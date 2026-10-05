package fakeserver

import (
	"bytes"
	"encoding/json"
	"fmt"
	"mime"
	"mime/multipart"
	"net/http"
	"net/url"
	"regexp"
	"strings"
)

// request is an authenticated API request.
type request struct {
	w      http.ResponseWriter
	r      *http.Request
	user   *User
	scopes []string
	// params are the JSON body, or the fields of a multipart body with
	// its file under "csv_file".
	params   record
	file     []byte
	fileName string
	// base is the URL prefix for the url fields of the response.
	base string
	// company is the company the request is about, if it is about one,
	// and role the user's role in it.
	company *Company
	role    string
}

// adminPath is ApiResource::ADMIN_PATH: the changes that take
// catalog_admin rather than catalog_write.
var adminPath = regexp.MustCompile(`^/api/v1/(users/|companies/[^/]+(/transfer_ownership|/cwr_connections(/[^/]+)?)?$)`)

// scopeFor is ApiResource.scope_for.
func scopeFor(r *http.Request) string {
	switch {
	case r.Method == http.MethodGet || r.Method == http.MethodHead:
		return "catalog"
	case adminPath.MatchString(r.URL.Path):
		return "catalog_admin"
	}
	return "catalog_write"
}

func (s *Server) api(w http.ResponseWriter, r *http.Request, body []byte) {
	user, scopes, ok := s.authenticate(r)
	if !ok {
		message(w, http.StatusUnauthorized, "Bad credentials")
		return
	}
	if scopes != nil {
		needed := scopeFor(r)
		granted := false
		for _, scope := range scopes {
			granted = granted || scope == needed
		}
		if !granted {
			w.Header().Set("WWW-Authenticate", fmt.Sprintf(`Bearer error="insufficient_scope", scope="%s"`, needed))
			message(w, http.StatusForbidden, fmt.Sprintf("The token does not carry the %s scope", needed))
			return
		}
	}

	path := strings.TrimPrefix(r.URL.EscapedPath(), APIResource)
	path = strings.Trim(path, "/")
	var segments []string
	for _, segment := range strings.Split(path, "/") {
		unescaped, err := url.PathUnescape(segment)
		if err != nil {
			notFound(w)
			return
		}
		segments = append(segments, unescaped)
	}

	req := &request{w: w, r: r, user: user, scopes: scopes, base: "http://" + r.Host + APIResource, params: record{}}
	if len(body) > 0 {
		mediaType, params, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if mediaType == "multipart/form-data" {
			if !req.parseMultipart(body, params["boundary"]) {
				writeJSON(w, http.StatusBadRequest, record{"status": 400, "error": "Bad Request"})
				return
			}
		} else if err := json.Unmarshal(body, &req.params); err != nil {
			writeJSON(w, http.StatusBadRequest, record{"status": 400, "error": "Bad Request"})
			return
		}
	}

	switch segments[0] {
	case "users":
		s.users(req, segments[1:])
	case "companies":
		s.companies(req, segments[1:])
	case "pros":
		s.pros(req, segments[1:])
	case "movies", "series", "episodes":
		s.productions(req, segments)
	default:
		if list, ok := s.Reference[segments[0]]; ok {
			s.reference(req, segments[0], list, segments[1:])
			return
		}
		routingError(w)
	}
}

// parseMultipart reads a multipart body the way Rails does, nesting
// works_upload[notes] under works_upload.
func (req *request) parseMultipart(body []byte, boundary string) bool {
	reader := multipart.NewReader(bytes.NewReader(body), boundary)
	form, err := reader.ReadForm(32 << 20)
	if err != nil {
		return false
	}
	set := func(name string, value any) {
		if i := strings.Index(name, "["); i > 0 && strings.HasSuffix(name, "]") {
			outer, inner := name[:i], name[i+1:len(name)-1]
			nestedRecord, _ := req.params[outer].(record)
			if nestedRecord == nil {
				nestedRecord = record{}
				req.params[outer] = nestedRecord
			}
			nestedRecord[inner] = value
			return
		}
		req.params[name] = value
	}
	for name, values := range form.Value {
		set(name, values[0])
	}
	for name, files := range form.File {
		f, err := files[0].Open()
		if err != nil {
			return false
		}
		data, _ := readAll(f)
		f.Close()
		req.file, req.fileName = data, files[0].Filename
		set(name, files[0].Filename)
	}
	return true
}

func readAll(f multipart.File) ([]byte, error) {
	var buf bytes.Buffer
	_, err := buf.ReadFrom(f)
	return buf.Bytes(), err
}

// wrapped returns the parameters under the wrapper key, as
// params.require does; a request without them is a 400.
func (req *request) wrapped(key string) (record, bool) {
	inner, ok := req.params[key].(record)
	if !ok || len(inner) == 0 {
		writeJSON(req.w, http.StatusBadRequest, record{"status": 400, "error": fmt.Sprintf("param is missing or the value is empty: %s", key)})
		return nil, false
	}
	return inner, true
}

var roleRank = map[string]int{Viewer: 1, Editor: 2, Admin: 3, Owner: 4}

// authorize is CompanyAuthorization's authorize_*!: a user with no role
// sees a 404, one whose role falls short a 403.
func (req *request) authorize(minimum string) bool {
	if req.role == "" {
		notFound(req.w)
		return false
	}
	if roleRank[req.role] < roleRank[minimum] {
		forbidden(req.w)
		return false
	}
	return true
}

// ---- users ----------------------------------------------------------------

func (s *Server) renderMe(req *request, user *User) record {
	var companies []record
	for _, company := range s.Companies {
		if role, ok := company.Roles[user.ID]; ok {
			companies = append(companies, record{"id": company.ID, "name": company.Name, "role": role, "url": fmt.Sprintf("%s/companies/%d", req.base, company.ID)})
		}
	}
	if companies == nil {
		companies = []record{}
	}
	out := record{
		"id": user.ID, "name": user.Name, "email_address": user.Email, "active": true,
		"created_at": seedTime, "updated_at": seedTime, "companies": companies,
	}
	// The account token is unscoped, so a read-only OAuth token may not
	// read it; the account token itself and catalog_admin tokens may.
	if req.scopes == nil || containsScope(req.scopes, "catalog_admin") {
		out["auth_token"] = user.APIToken
	}
	return out
}

func containsScope(scopes []string, wanted string) bool {
	for _, scope := range scopes {
		if scope == wanted {
			return true
		}
	}
	return false
}

func (s *Server) users(req *request, rest []string) {
	switch {
	case len(rest) == 1 && rest[0] == "me" && req.r.Method == http.MethodGet:
		writeJSON(req.w, http.StatusOK, s.renderMe(req, req.user))
	case len(rest) == 1 && rest[0] == "me" && req.r.Method == http.MethodPatch:
		params, ok := req.wrapped("user")
		if !ok {
			return
		}
		var problems []string
		if name, has := params["name"]; has && blank(name) {
			problems = append(problems, "Name can't be blank")
		}
		if email, has := params["email_address"]; has && blank(email) {
			problems = append(problems, "Email address can't be blank")
		}
		if password, has := params["password"]; has && str(password) != str(params["password_confirmation"]) {
			problems = append(problems, "Password confirmation doesn't match Password")
		}
		if len(problems) > 0 {
			invalid(req.w, problems...)
			return
		}
		if name, has := params["name"]; has {
			req.user.Name = str(name)
		}
		if email, has := params["email_address"]; has {
			req.user.Email = str(email)
		}
		if password, has := params["password"]; has {
			req.user.Password = str(password)
		}
		writeJSON(req.w, http.StatusOK, s.renderMe(req, req.user))
	case len(rest) == 1 && rest[0] == "reset_auth_token" && req.r.Method == http.MethodPost:
		req.user.APIToken = "token-" + secret()[:16]
		writeJSON(req.w, http.StatusOK, s.renderMe(req, req.user))
	default:
		routingError(req.w)
	}
}

// ---- companies ------------------------------------------------------------

func (s *Server) renderCompany(req *request, company *Company, full bool) record {
	owner := s.userByID(company.Owner)
	out := record{
		"id": company.ID, "name": company.Name, "description": company.Description, "owner_name": owner.Name,
		"created_at": seedTime, "updated_at": seedTime,
	}
	if !full {
		out["url"] = fmt.Sprintf("%s/companies/%d", req.base, company.ID)
		return out
	}
	out["publisher_type"] = "music_publisher"
	out["counts"] = record{
		"publishers": len(company.Publishers), "writers": len(company.Writers), "works": len(company.Works),
		"sales": len(company.Sales), "sales_uploads": len(company.SalesUploads), "royalty_statements": len(company.RoyaltyStatements),
	}
	out["subscription"] = record{"active": false}
	return out
}

func (s *Server) userByID(id int64) *User {
	for _, user := range s.Users {
		if user.ID == id {
			return user
		}
	}
	return &User{}
}

func (s *Server) companies(req *request, rest []string) {
	if len(rest) == 0 {
		if req.r.Method != http.MethodGet {
			routingError(req.w)
			return
		}
		var visible []record
		for _, company := range s.Companies {
			if _, ok := company.Roles[req.user.ID]; ok {
				visible = append(visible, s.renderCompany(req, company, false))
			}
		}
		sortBy(visible, "name")
		page, pagination := paginate(req.r, visible, 0)
		writeJSON(req.w, http.StatusOK, record{"companies": page, "pagination": pagination})
		return
	}

	// A company the user does not collaborate on looks like one that
	// does not exist.
	id, _ := toInt(rest[0])
	company := s.Company(id)
	if company == nil || company.Roles[req.user.ID] == "" {
		notFound(req.w)
		return
	}
	req.company, req.role = company, company.Roles[req.user.ID]
	rest = rest[1:]

	if len(rest) == 0 {
		switch req.r.Method {
		case http.MethodGet:
			writeJSON(req.w, http.StatusOK, s.renderCompany(req, company, true))
		case http.MethodPatch, http.MethodPut:
			if !req.authorize(Owner) {
				return
			}
			params, ok := req.wrapped("company")
			if !ok {
				return
			}
			if name, has := params["name"]; has && blank(name) {
				invalid(req.w, "Name can't be blank")
				return
			}
			if name, has := params["name"]; has {
				company.Name = str(name)
			}
			if description, has := params["description"]; has {
				company.Description = str(description)
			}
			writeJSON(req.w, http.StatusOK, s.renderCompany(req, company, true))
		default:
			routingError(req.w)
		}
		return
	}

	switch rest[0] {
	case "transfer_ownership":
		if req.r.Method != http.MethodPost || !req.authorize(Owner) {
			return
		}
		newOwnerID, _ := toInt(req.params["new_owner_id"])
		newOwner := s.userByID(newOwnerID)
		if newOwner.ID == 0 {
			message(req.w, http.StatusNotFound, "New owner not found")
			return
		}
		if company.Roles[newOwner.ID] == "" {
			message(req.w, http.StatusUnprocessableEntity, "New owner must be an existing collaborator")
			return
		}
		company.Owner = newOwner.ID
		company.Roles[newOwner.ID] = Owner
		company.Roles[req.user.ID] = Admin
		writeJSON(req.w, http.StatusOK, s.renderCompany(req, company, true))
	case "publishers":
		s.publishers(req, rest[1:])
	case "writers":
		s.writers(req, rest[1:])
	case "catalogs":
		s.catalogs(req, rest[1:])
	case "works":
		s.works(req, rest[1:])
	case "sales":
		s.sales(req, rest[1:], nil)
	case "subpublishing_agreements":
		s.agreements(req, rest[1:], "subpublishing_agreement", &company.SubAgreements)
	case "admin_agreements":
		s.agreements(req, rest[1:], "admin_agreement", &company.AdminAgreements)
	case "works_uploads":
		s.uploads(req, rest[1:], uploadWorks, &company.WorksUploads)
	case "sales_uploads":
		s.uploads(req, rest[1:], uploadSales, &company.SalesUploads)
	case "royalty_statements":
		s.uploads(req, rest[1:], uploadStatement, &company.RoyaltyStatements)
	case "pro_data_dumps":
		s.uploads(req, rest[1:], uploadDump, &company.ProDataDumps)
	case "raw_royalty_records":
		s.rawRoyaltyRecords(req, rest[1:], nil)
	case "pros":
		s.claimsUnderPro(req, rest[1:])
	case "broadcast_delivery_batches":
		s.batches(req, rest[1:])
	case "cwr_connections":
		s.cwrConnections(req, rest[1:])
	default:
		routingError(req.w)
	}
}

// ---- external-id resources ------------------------------------------------

var externalID = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

// findExternal finds the record in list with the external ID, or -1.
func findExternal(list []record, id string) int {
	for i, item := range list {
		if str(item["external_id"]) == id {
			return i
		}
	}
	return -1
}

// filterQ narrows list by the q[...] parameters the request carries,
// matching each key's value against the fields named.
func filterQ(r *http.Request, list []record, fields map[string][]string) []record {
	query := r.URL.Query()
	out := list
	for param, keys := range fields {
		wanted := query.Get("q[" + param + "]")
		if wanted == "" {
			continue
		}
		var kept []record
		for _, item := range out {
			for _, key := range keys {
				if (param == "external_id" && str(item[key]) == wanted) || (param != "external_id" && contains(str(item[key]), wanted)) {
					kept = append(kept, item)
					break
				}
			}
		}
		out = kept
	}
	return out
}

func (s *Server) renderWriter(req *request, w record, full bool) record {
	out := record{
		"id": w["external_id"], "first_name": w["first_name"], "last_name": w["last_name"],
		"full_name": strings.TrimSpace(str(w["first_name"]) + " " + str(w["last_name"])), "external_id": w["external_id"], "controlled": w["controlled"],
	}
	pro := s.pro(w["pro_id"])
	if !full {
		if pro != nil {
			out["pro_name"] = pro["name"]
		} else {
			out["pro_name"] = nil
		}
		out["url"] = fmt.Sprintf("%s/companies/%d/writers/%s", req.base, req.company.ID, w["external_id"])
		return out
	}
	out["created_at"], out["updated_at"] = w["created_at"], w["updated_at"]
	if pro != nil {
		out["pro"] = record{"id": pro["id"], "name": pro["name"], "url": fmt.Sprintf("%s/pros/%v", req.base, pro["id"])}
	}
	return out
}

func (s *Server) pro(id any) record {
	n, ok := toInt(id)
	if !ok {
		return nil
	}
	for _, pro := range s.Pros {
		if have, _ := toInt(pro["id"]); have == n {
			return pro
		}
	}
	return nil
}

func (s *Server) writers(req *request, rest []string) {
	if !req.authorize(Viewer) {
		return
	}
	company := req.company
	if len(rest) == 0 {
		switch req.r.Method {
		case http.MethodGet:
			list := filterQ(req.r, company.Writers, map[string][]string{"name": {"first_name", "last_name"}, "external_id": {"external_id"}})
			var out []record
			for _, w := range list {
				out = append(out, s.renderWriter(req, w, false))
			}
			sortBy(out, "last_name")
			page, pagination := paginate(req.r, out, 0)
			writeJSON(req.w, http.StatusOK, record{"writers": page, "pagination": pagination})
		case http.MethodPost:
			if !req.authorize(Editor) {
				return
			}
			params, ok := req.wrapped("writer")
			if !ok {
				return
			}
			w := record{"first_name": nil, "last_name": nil, "pseudonym": nil, "pro_id": nil, "ipi_name_number": nil, "ipi_base_number": nil, "controlled": false}
			for key, value := range params {
				w[key] = value
			}
			if problems := s.validateWriter(company, w, -1); len(problems) > 0 {
				invalid(req.w, problems...)
				return
			}
			stamp(w, false)
			company.Writers = append(company.Writers, w)
			writeJSON(req.w, http.StatusCreated, s.renderWriter(req, w, true))
		default:
			routingError(req.w)
		}
		return
	}
	i := findExternal(company.Writers, rest[0])
	if i < 0 || len(rest) > 1 {
		notFound(req.w)
		return
	}
	w := company.Writers[i]
	switch req.r.Method {
	case http.MethodGet:
		writeJSON(req.w, http.StatusOK, s.renderWriter(req, w, true))
	case http.MethodPatch, http.MethodPut:
		if !req.authorize(Editor) {
			return
		}
		params, ok := req.wrapped("writer")
		if !ok {
			return
		}
		updated := copyRecord(w)
		for key, value := range params {
			updated[key] = value
		}
		if problems := s.validateWriter(company, updated, i); len(problems) > 0 {
			invalid(req.w, problems...)
			return
		}
		stamp(updated, false)
		company.Writers[i] = updated
		writeJSON(req.w, http.StatusOK, s.renderWriter(req, updated, true))
	case http.MethodDelete:
		if !req.authorize(Editor) {
			return
		}
		company.Writers = append(company.Writers[:i], company.Writers[i+1:]...)
		req.w.WriteHeader(http.StatusNoContent)
	default:
		routingError(req.w)
	}
}

func (s *Server) validateWriter(company *Company, w record, self int) []string {
	var problems []string
	problems = append(problems, validateExternalID(company.Writers, w, self)...)
	if blank(w["last_name"]) {
		problems = append(problems, "Last name can't be blank")
	}
	return problems
}

func validateExternalID(list []record, r record, self int) []string {
	id := str(r["external_id"])
	if blank(r["external_id"]) {
		return []string{"External can't be blank"}
	}
	if !externalID.MatchString(id) {
		return []string{"External can only contain letters, numbers, hyphens, and underscores"}
	}
	if i := findExternal(list, id); i >= 0 && i != self {
		return []string{"External has already been taken"}
	}
	return nil
}

func (s *Server) renderPublisher(req *request, p record, full bool) record {
	out := record{"id": p["external_id"], "legal_name": p["legal_name"], "trade_name": p["trade_name"], "external_id": p["external_id"], "contact_name": p["contact_name"], "contact_email": p["contact_email"]}
	pro := s.pro(p["pro_id"])
	var territories []record
	ids, _ := p["tis_territory_ids"].([]any)
	for _, id := range ids {
		for _, territory := range s.Reference["tis_territories"] {
			if a, _ := toInt(territory["id"]); a == mustInt(id) {
				territories = append(territories, record{"id": territory["tis_a"], "tis_code": territory["tis_a"], "name": territory["name"]})
			}
		}
	}
	if !full {
		out["pro_name"] = nil
		if pro != nil {
			out["pro_name"] = pro["name"]
		}
		var names []string
		for _, t := range territories {
			names = append(names, str(t["name"]))
		}
		out["territories"] = strings.Join(names, ", ")
		out["url"] = fmt.Sprintf("%s/companies/%d/publishers/%s", req.base, req.company.ID, p["external_id"])
		return out
	}
	out["notes"], out["created_at"], out["updated_at"] = p["notes"], p["created_at"], p["updated_at"]
	if pro != nil {
		out["pro"] = record{"id": pro["id"], "name": pro["name"], "url": fmt.Sprintf("%s/pros/%v", req.base, pro["id"])}
	}
	if territories == nil {
		territories = []record{}
	}
	out["territories"] = territories
	altNames := []record{}
	for _, alt := range req.company.AltNames {
		if alt["publisher_external_id"] == p["external_id"] {
			altNames = append(altNames, record{"id": alt["id"], "name": alt["name"], "manually_entered": alt["manually_entered"]})
		}
	}
	out["alt_names"] = altNames
	return out
}

func mustInt(v any) int64 {
	n, _ := toInt(v)
	return n
}

func (s *Server) publishers(req *request, rest []string) {
	if !req.authorize(Viewer) {
		return
	}
	company := req.company
	if len(rest) == 1 && rest[0] == "process_sheet" {
		s.processSheet(req)
		return
	}
	if len(rest) == 0 {
		switch req.r.Method {
		case http.MethodGet:
			list := filterQ(req.r, company.Publishers, map[string][]string{"name": {"legal_name", "trade_name"}, "external_id": {"external_id"}})
			var out []record
			for _, p := range list {
				out = append(out, s.renderPublisher(req, p, false))
			}
			sortBy(out, "legal_name")
			page, pagination := paginate(req.r, out, 0)
			writeJSON(req.w, http.StatusOK, record{"publishers": page, "pagination": pagination})
		case http.MethodPost:
			if !req.authorize(Editor) {
				return
			}
			params, ok := req.wrapped("publisher")
			if !ok {
				return
			}
			p := record{"legal_name": nil, "trade_name": nil, "pro_id": nil, "mech_pro_id": nil, "ipi_name": nil, "ipi_base_number": nil, "contact_name": nil, "contact_email": nil, "controlled": false, "active": true, "notes": nil, "tis_territory_ids": []any{}}
			for key, value := range params {
				p[key] = value
			}
			if problems := s.validatePublisher(company, p, -1); len(problems) > 0 {
				invalid(req.w, problems...)
				return
			}
			stamp(p, false)
			company.Publishers = append(company.Publishers, p)
			writeJSON(req.w, http.StatusCreated, s.renderPublisher(req, p, true))
		default:
			routingError(req.w)
		}
		return
	}
	i := findExternal(company.Publishers, rest[0])
	if i < 0 {
		notFound(req.w)
		return
	}
	p := company.Publishers[i]
	if len(rest) >= 2 && rest[1] == "alt_names" {
		s.altNames(req, p, rest[2:])
		return
	}
	if len(rest) > 1 {
		notFound(req.w)
		return
	}
	switch req.r.Method {
	case http.MethodGet:
		writeJSON(req.w, http.StatusOK, s.renderPublisher(req, p, true))
	case http.MethodPatch, http.MethodPut:
		if !req.authorize(Editor) {
			return
		}
		params, ok := req.wrapped("publisher")
		if !ok {
			return
		}
		updated := copyRecord(p)
		for key, value := range params {
			updated[key] = value
		}
		if problems := s.validatePublisher(company, updated, i); len(problems) > 0 {
			invalid(req.w, problems...)
			return
		}
		stamp(updated, false)
		company.Publishers[i] = updated
		writeJSON(req.w, http.StatusOK, s.renderPublisher(req, updated, true))
	case http.MethodDelete:
		if !req.authorize(Editor) {
			return
		}
		company.Publishers = append(company.Publishers[:i], company.Publishers[i+1:]...)
		req.w.WriteHeader(http.StatusNoContent)
	default:
		routingError(req.w)
	}
}

func (s *Server) validatePublisher(company *Company, p record, self int) []string {
	var problems []string
	problems = append(problems, validateExternalID(company.Publishers, p, self)...)
	if blank(p["legal_name"]) {
		problems = append(problems, "Legal name can't be blank")
	}
	if s.pro(p["pro_id"]) == nil {
		problems = append(problems, "Pro must exist")
	}
	return problems
}

func (s *Server) altNames(req *request, p record, rest []string) {
	if !req.authorize(Editor) {
		return
	}
	company := req.company
	switch {
	case len(rest) == 0 && req.r.Method == http.MethodPost:
		params, ok := req.wrapped("publishers_alt_name")
		if !ok {
			return
		}
		if blank(params["name"]) {
			invalid(req.w, "Name can't be blank")
			return
		}
		alt := record{"id": s.id(), "publisher_external_id": p["external_id"], "name": params["name"], "manually_entered": true}
		company.AltNames = append(company.AltNames, alt)
		writeJSON(req.w, http.StatusCreated, record{"id": alt["id"], "name": alt["name"], "manually_entered": true})
	case len(rest) == 1 && req.r.Method == http.MethodDelete:
		id, _ := toInt(rest[0])
		for i, alt := range company.AltNames {
			if mustInt(alt["id"]) == id && alt["publisher_external_id"] == p["external_id"] {
				company.AltNames = append(company.AltNames[:i], company.AltNames[i+1:]...)
				req.w.WriteHeader(http.StatusNoContent)
				return
			}
		}
		notFound(req.w)
	default:
		routingError(req.w)
	}
}

// processSheet is the publisher sheet import: name,pro,ipi,controlled
// rows, each standing alone.
func (s *Server) processSheet(req *request) {
	if req.r.Method != http.MethodPost || !req.authorize(Editor) {
		return
	}
	if req.file == nil {
		message(req.w, http.StatusUnprocessableEntity, "csv_file is required")
		return
	}
	created := 0
	var errs []string
	for n, line := range strings.Split(strings.TrimSpace(string(req.file)), "\n")[1:] {
		fields := strings.Split(line, ",")
		if len(fields) < 2 || strings.TrimSpace(fields[0]) == "" {
			errs = append(errs, fmt.Sprintf("Row %d: name is required", n+2))
			continue
		}
		var proID any
		for _, pro := range s.Pros {
			if strings.EqualFold(str(pro["abbreviation"]), strings.TrimSpace(fields[1])) {
				proID = pro["id"]
			}
		}
		if proID == nil {
			errs = append(errs, fmt.Sprintf("Row %d: unknown PRO %q", n+2, strings.TrimSpace(fields[1])))
			continue
		}
		p := record{"external_id": fmt.Sprintf("SHEET-%d", s.id()), "legal_name": strings.TrimSpace(fields[0]), "pro_id": proID, "controlled": len(fields) > 3 && strings.TrimSpace(fields[3]) == "true", "active": true, "tis_territory_ids": []any{}}
		stamp(p, false)
		req.company.Publishers = append(req.company.Publishers, p)
		created++
	}
	status := http.StatusOK
	if len(errs) > 0 {
		status = http.StatusUnprocessableEntity
	}
	if errs == nil {
		errs = []string{}
	}
	writeJSON(req.w, status, record{"created": created, "errors": errs})
}

func (s *Server) catalogs(req *request, rest []string) {
	if !req.authorize(Viewer) {
		return
	}
	company := req.company
	render := func(c record) record {
		count := 0
		for _, w := range company.Works {
			if str(w["catalog"]) == str(c["name"]) {
				count++
			}
		}
		return record{"id": c["external_id"], "external_id": c["external_id"], "name": c["name"], "works_count": count, "created_at": c["created_at"], "updated_at": c["updated_at"]}
	}
	if len(rest) == 0 {
		switch req.r.Method {
		case http.MethodGet:
			var out []record
			for _, c := range company.Catalogs {
				out = append(out, render(c))
			}
			sortBy(out, "name")
			page, pagination := paginate(req.r, out, 0)
			writeJSON(req.w, http.StatusOK, record{"catalogs": page, "pagination": pagination})
		case http.MethodPost:
			if !req.authorize(Editor) {
				return
			}
			params, ok := req.wrapped("catalog")
			if !ok {
				return
			}
			if blank(params["name"]) {
				invalid(req.w, "Name can't be blank")
				return
			}
			c := record{"external_id": params["external_id"], "name": params["name"]}
			stamp(c, false)
			company.Catalogs = append(company.Catalogs, c)
			writeJSON(req.w, http.StatusCreated, render(c))
		default:
			routingError(req.w)
		}
		return
	}
	i := findExternal(company.Catalogs, rest[0])
	if i < 0 || len(rest) > 1 {
		notFound(req.w)
		return
	}
	c := company.Catalogs[i]
	switch req.r.Method {
	case http.MethodGet:
		writeJSON(req.w, http.StatusOK, render(c))
	case http.MethodPatch, http.MethodPut:
		if !req.authorize(Editor) {
			return
		}
		params, ok := req.wrapped("catalog")
		if !ok {
			return
		}
		if name, has := params["name"]; has && blank(name) {
			invalid(req.w, "Name can't be blank")
			return
		}
		for key, value := range params {
			c[key] = value
		}
		stamp(c, false)
		writeJSON(req.w, http.StatusOK, render(c))
	case http.MethodDelete:
		if !req.authorize(Editor) {
			return
		}
		company.Catalogs = append(company.Catalogs[:i], company.Catalogs[i+1:]...)
		req.w.WriteHeader(http.StatusNoContent)
	default:
		routingError(req.w)
	}
}

// ---- works ----------------------------------------------------------------

func (s *Server) language(id any) record {
	for _, language := range s.Reference["cis_languages"] {
		if mustInt(language["id"]) == mustInt(id) && id != nil {
			return language
		}
	}
	return nil
}

func (s *Server) renderWork(req *request, w record, full bool) record {
	out := record{"id": w["external_id"], "external_id": w["external_id"], "title": w["title"]}
	language := s.language(w["cis_language_id"])
	if !full {
		out["language"], out["language_code"] = nil, nil
		if language != nil {
			out["language"], out["language_code"] = language["name"], language["code"]
		}
		out["url"] = fmt.Sprintf("%s/companies/%d/works/%s", req.base, req.company.ID, w["external_id"])
		return out
	}
	out["created_at"], out["updated_at"] = w["created_at"], w["updated_at"]
	if language != nil {
		out["language"] = record{"id": language["code"], "code": language["code"], "name": language["name"]}
	}
	alt := []record{}
	for _, item := range listOfRecords(w["works_alt_titles"]) {
		code := any(nil)
		for _, t := range s.Reference["title_types"] {
			if mustInt(t["id"]) == mustInt(item["title_type_id"]) {
				code = t["code"]
			}
		}
		alt = append(alt, record{"title": item["title"], "title_type": code, "language_code": nil})
	}
	out["alt_titles"] = alt
	codes := []record{}
	for _, item := range listOfRecords(w["registration_codes"]) {
		codes = append(codes, record{"code": item["code"], "registration_type": s.registrationTypeName(item["registration_type_id"])})
	}
	out["registration_codes"] = codes
	publishers := []record{}
	for _, item := range listOfRecords(w["works_publishers"]) {
		name := any(nil)
		if i := findExternal(req.company.Publishers, str(item["publisher_external_id"])); i >= 0 {
			name = req.company.Publishers[i]["legal_name"]
		}
		publishers = append(publishers, record{"id": item["id"], "publisher_id": item["publisher_external_id"], "publisher_name": name, "publisher_type_id": item["publisher_type_id"], "share": item["share"], "publisher_url": fmt.Sprintf("%s/companies/%d/publishers/%v", req.base, req.company.ID, item["publisher_external_id"])})
	}
	out["publishers"] = publishers
	writers := []record{}
	for _, item := range listOfRecords(w["works_writers"]) {
		name := any(nil)
		if i := findExternal(req.company.Writers, str(item["writer_external_id"])); i >= 0 {
			writer := req.company.Writers[i]
			name = strings.TrimSpace(str(writer["first_name"]) + " " + str(writer["last_name"]))
		}
		writers = append(writers, record{"id": item["id"], "writer_id": item["writer_external_id"], "writer_name": name, "writer_designation_id": item["writer_designation_id"], "share": item["share"], "writer_url": fmt.Sprintf("%s/companies/%d/writers/%v", req.base, req.company.ID, item["writer_external_id"])})
	}
	out["writers"] = writers
	return out
}

func (s *Server) registrationTypeName(id any) any {
	for _, t := range s.Reference["registration_types"] {
		if mustInt(t["id"]) == mustInt(id) {
			return t["name"]
		}
	}
	return nil
}

func listOfRecords(v any) []record {
	var out []record
	switch list := v.(type) {
	case []any:
		for _, item := range list {
			if r, ok := item.(map[string]any); ok {
				out = append(out, r)
			}
		}
	case []record:
		out = list
	}
	return out
}

func (s *Server) works(req *request, rest []string) {
	if !req.authorize(Viewer) {
		return
	}
	company := req.company
	if len(rest) == 0 {
		switch req.r.Method {
		case http.MethodGet:
			list := company.Works
			if q := req.r.URL.Query().Get("q"); q != "" {
				list = nil
				for _, w := range company.Works {
					if contains(str(w["title"]), q) {
						list = append(list, w)
					}
				}
			}
			list = filterQ(req.r, list, map[string][]string{"title": {"title", "alt_titles_text"}, "external_id": {"external_id"}})
			var out []record
			for _, w := range list {
				out = append(out, s.renderWork(req, w, false))
			}
			sortBy(out, "title")
			page, pagination := paginate(req.r, out, 0)
			writeJSON(req.w, http.StatusOK, record{"works": page, "pagination": pagination})
		case http.MethodPost:
			if !req.authorize(Editor) {
				return
			}
			params, ok := req.wrapped("work")
			if !ok {
				return
			}
			w := record{"title": nil, "cis_language_id": nil, "catalog": nil, "works_alt_titles": []any{}, "registration_codes": []any{}, "works_publishers": []any{}, "works_writers": []any{}, "include_in_cwr": false}
			if problems := s.applyWork(company, w, params, -1); len(problems) > 0 {
				if problems[0] == notFoundSentinel {
					notFound(req.w)
					return
				}
				invalid(req.w, problems...)
				return
			}
			stamp(w, false)
			company.Works = append(company.Works, w)
			writeJSON(req.w, http.StatusCreated, s.renderWork(req, w, true))
		default:
			routingError(req.w)
		}
		return
	}
	i := findExternal(company.Works, rest[0])
	if i < 0 {
		notFound(req.w)
		return
	}
	w := company.Works[i]
	if len(rest) >= 2 && rest[1] == "cwr_tickets" {
		s.cwrTickets(req, w, rest[2:])
		return
	}
	if len(rest) > 1 {
		notFound(req.w)
		return
	}
	switch req.r.Method {
	case http.MethodGet:
		writeJSON(req.w, http.StatusOK, s.renderWork(req, w, true))
	case http.MethodPatch, http.MethodPut:
		if !req.authorize(Editor) {
			return
		}
		params, ok := req.wrapped("work")
		if !ok {
			return
		}
		updated := copyRecord(w)
		if problems := s.applyWork(company, updated, params, i); len(problems) > 0 {
			if problems[0] == notFoundSentinel {
				notFound(req.w)
				return
			}
			invalid(req.w, problems...)
			return
		}
		stamp(updated, false)
		company.Works[i] = updated
		writeJSON(req.w, http.StatusOK, s.renderWork(req, updated, true))
	case http.MethodDelete:
		if !req.authorize(Editor) {
			return
		}
		company.Works = append(company.Works[:i], company.Works[i+1:]...)
		req.w.WriteHeader(http.StatusNoContent)
	default:
		routingError(req.w)
	}
}

// applyWork puts the permitted parameters into w, nested attributes
// appending to what is there, and reports what is invalid.
func (s *Server) applyWork(company *Company, w record, params record, self int) []string {
	for _, key := range []string{"external_id", "title", "cis_language_id"} {
		if value, has := params[key]; has {
			w[key] = value
		}
	}
	// Credits name writers and publishers by external ID, as writer_id
	// or writer_external_id; an unknown one is a 404 (find_by!).
	for _, kind := range []string{"writer", "publisher"} {
		for _, item := range listOfRecords(params["works_"+kind+"s_attributes"]) {
			externalID := str(item[kind+"_external_id"])
			if externalID == "" {
				externalID = str(item[kind+"_id"])
			}
			if externalID == "" {
				continue
			}
			list := company.Writers
			if kind == "publisher" {
				list = company.Publishers
			}
			if findExternal(list, externalID) < 0 {
				return []string{notFoundSentinel}
			}
			item[kind+"_external_id"] = externalID
			delete(item, kind+"_id")
		}
	}
	nestedKeys := map[string]string{"works_alt_titles_attributes": "works_alt_titles", "registration_codes_attributes": "registration_codes", "works_publishers_attributes": "works_publishers", "works_writers_attributes": "works_writers"}
	var problems []string
	for param, key := range nestedKeys {
		for _, item := range listOfRecords(params[param]) {
			entry := copyRecord(item)
			if key == "registration_codes" && (blank(entry["code"]) || s.registrationTypeName(entry["registration_type_id"]) == nil) {
				problems = append(problems, "Registration codes is invalid")
			}
			if key == "works_alt_titles" && blank(entry["title"]) {
				problems = append(problems, "Works alt titles is invalid")
			}
			// An entry with an id updates (or, with _destroy, removes)
			// the row it names, as accepts_nested_attributes_for does.
			if entry["id"] != nil {
				existing := listOfAny(w[key])
				var kept []any
				for _, row := range existing {
					if r, ok := row.(map[string]any); ok && mustInt(r["id"]) == mustInt(entry["id"]) {
						if entry["_destroy"] == true || str(entry["_destroy"]) == "1" || str(entry["_destroy"]) == "true" {
							continue
						}
						for k, v := range entry {
							if k != "_destroy" {
								r[k] = v
							}
						}
					}
					kept = append(kept, row)
				}
				w[key] = kept
				continue
			}
			entry["id"] = s.id()
			w[key] = append(listOfAny(w[key]), entry)
		}
	}
	problems = append(validateExternalID(company.Works, w, self), problems...)
	if blank(w["title"]) {
		problems = append(problems, "Title can't be blank")
	}
	return problems
}

// notFoundSentinel is what applyWork returns when a credit names a
// writer or publisher that is not there: a 404 rather than a 422.
const notFoundSentinel = "\x00not found"

func listOfAny(v any) []any {
	list, _ := v.([]any)
	return list
}

// workHasCode reports whether the work carries a code of the PRO's
// registration type.
func (s *Server) workHasCode(w record, proID int64) bool {
	for _, code := range listOfRecords(w["registration_codes"]) {
		for _, t := range s.Reference["registration_types"] {
			if mustInt(t["id"]) == mustInt(code["registration_type_id"]) && mustInt(t["pro_id"]) == proID {
				return true
			}
		}
	}
	return false
}

func (s *Server) workCode(w record, proID int64) any {
	for _, code := range listOfRecords(w["registration_codes"]) {
		for _, t := range s.Reference["registration_types"] {
			if mustInt(t["id"]) == mustInt(code["registration_type_id"]) && mustInt(t["pro_id"]) == proID {
				return code["code"]
			}
		}
	}
	return nil
}

// ---- sales ----------------------------------------------------------------

func (s *Server) renderSale(req *request, sale record, full bool) record {
	work := record{}
	if i := findExternal(req.company.Works, str(sale["work_external_id"])); i >= 0 {
		work = req.company.Works[i]
	}
	out := record{"id": sale["id"], "original_film_title": sale["original_film_title"], "original_series_title": sale["original_series_title"], "original_episode_title": sale["original_episode_title"], "external_work_id": sale["external_work_id"], "original_work_title": sale["original_work_title"], "release_date": sale["release_date"], "original_production_company": sale["original_production_company"], "created_at": sale["created_at"]}
	if !full {
		out["work_id"], out["work_title"], out["work_external_id"] = work["external_id"], work["title"], work["external_id"]
		out["url"] = fmt.Sprintf("%s/companies/%d/sales/%v", req.base, req.company.ID, sale["id"])
		return out
	}
	for _, key := range []string{"line_number", "film_imdb_id", "series_imdb_id", "episode_imdb_id", "original_full_episode_number", "updated_at"} {
		out[key] = sale[key]
	}
	out["work"] = record{"id": work["external_id"], "external_id": work["external_id"], "title": work["title"], "url": fmt.Sprintf("%s/companies/%d/works/%v", req.base, req.company.ID, work["external_id"])}
	if sale["production_id"] != nil {
		out["production"] = record{"id": sale["production_id"], "type": sale["production_type"], "title": sale["production_title"]}
	}
	return out
}

func (s *Server) sales(req *request, rest []string, upload *Upload) {
	if !req.authorize(Viewer) {
		return
	}
	company := req.company
	collection := company.Sales
	if upload != nil {
		collection = nil
		for _, sale := range company.Sales {
			if mustInt(sale["sales_upload_id"]) == mustInt(upload.Fields["id"]) {
				collection = append(collection, sale)
			}
		}
	}
	if len(rest) == 0 {
		switch req.r.Method {
		case http.MethodGet:
			list := filterQ(req.r, collection, map[string][]string{"work_external_id": {"work_external_id"}, "work_title": {"work_title"}, "original_work_title": {"original_work_title"}, "registration_code": {"registration_code"}, "film_or_series_title": {"original_film_title", "original_series_title"}, "episode_title": {"original_episode_title"}})
			var out []record
			for _, sale := range list {
				out = append(out, s.renderSale(req, sale, false))
			}
			page, pagination := paginate(req.r, out, 0)
			writeJSON(req.w, http.StatusOK, record{"sales": page, "pagination": pagination})
		case http.MethodPost:
			if !req.authorize(Editor) {
				return
			}
			params, ok := req.wrapped("sale")
			if !ok {
				return
			}
			sale := record{"id": s.id(), "sales_upload_id": nil}
			for key, value := range params {
				sale[key] = value
			}
			// work_id is the work's external ID, found within the company.
			if blank(params["work_id"]) {
				invalid(req.w, "Work must exist")
				return
			}
			if findExternal(company.Works, str(params["work_id"])) < 0 {
				notFound(req.w)
				return
			}
			sale["work_external_id"] = str(params["work_id"])
			delete(sale, "work_id")
			stamp(sale, false)
			company.Sales = append(company.Sales, sale)
			writeJSON(req.w, http.StatusCreated, s.renderSale(req, sale, true))
		default:
			routingError(req.w)
		}
		return
	}
	switch rest[0] {
	case "paid":
		w := req.w
		filename := fmt.Sprintf("paid_sales_company_%d.csv", company.ID)
		body := "work,title,ASCAP,BMI\nW-999,Drunken Daisy,12.50,0\n"
		if len(rest) == 3 && rest[1] == "pro" {
			pro := s.pro(rest[2])
			if pro == nil {
				notFound(w)
				return
			}
			filename = fmt.Sprintf("paid_sales_%s_company_%d.csv", pro["abbreviation"], company.ID)
			body = "work,title,Netflix,Hulu\nW-999,Drunken Daisy,12.50,0\n"
		}
		sendFile(w, filename, "text/csv", []byte(body))
		return
	case "lookup_production":
		for _, kind := range []string{"movies", "series", "episodes"} {
			for _, p := range s.Reference[kind] {
				if str(p["imdb_id"]) == req.r.URL.Query().Get("imdb_id") {
					writeJSON(req.w, http.StatusOK, record{"found": true, "production_type": map[string]string{"movies": "Movie", "series": "Series", "episodes": "Episode"}[kind], "title": p["title"], "release_date": nil, "production_company": nil, "series_title": nil, "episode_number": nil})
					return
				}
			}
		}
		writeJSON(req.w, http.StatusOK, record{"found": false})
		return
	case "lookup_work":
		if i := findExternal(company.Works, req.r.URL.Query().Get("external_id")); i >= 0 {
			writeJSON(req.w, http.StatusOK, record{"found": true, "work_id": company.Works[i]["external_id"], "title": company.Works[i]["title"]})
			return
		}
		writeJSON(req.w, http.StatusOK, record{"found": false})
		return
	}
	id, _ := toInt(rest[0])
	index := -1
	for i, sale := range company.Sales {
		if mustInt(sale["id"]) == id {
			index = i
		}
	}
	if index < 0 || len(rest) > 1 {
		notFound(req.w)
		return
	}
	sale := company.Sales[index]
	switch req.r.Method {
	case http.MethodGet:
		writeJSON(req.w, http.StatusOK, s.renderSale(req, sale, true))
	case http.MethodPatch, http.MethodPut:
		if !req.authorize(Editor) {
			return
		}
		params, ok := req.wrapped("sale")
		if !ok {
			return
		}
		for key, value := range params {
			sale[key] = value
		}
		stamp(sale, false)
		writeJSON(req.w, http.StatusOK, s.renderSale(req, sale, true))
	case http.MethodDelete:
		if !req.authorize(Editor) {
			return
		}
		company.Sales = append(company.Sales[:index], company.Sales[index+1:]...)
		req.w.WriteHeader(http.StatusNoContent)
	default:
		routingError(req.w)
	}
}

func sendFile(w http.ResponseWriter, filename, contentType string, body []byte) {
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, filename))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}

// ---- agreements -----------------------------------------------------------

func (s *Server) renderAgreement(req *request, a record, kind string) record {
	named := func(externalID any) record {
		if i := findExternal(req.company.Publishers, str(externalID)); i >= 0 {
			return record{"id": externalID, "name": req.company.Publishers[i]["legal_name"]}
		}
		return record{"id": externalID, "name": nil}
	}
	out := record{"id": a["id"], "agreement_number": a["agreement_number"], "starts_on": a["starts_on"], "ends_on": a["ends_on"], "assignor": named(a["assignor_external_id"]), "assignee": named(a["assignee_external_id"])}
	territories := []record{}
	for _, t := range listOfRecords(a["territories"]) {
		name := any(nil)
		for _, territory := range s.Reference["tis_territories"] {
			if mustInt(territory["id"]) == mustInt(t["tis_territory_id"]) {
				name = territory["name"]
			}
		}
		territories = append(territories, record{"id": t["id"], "tis_territory_id": t["tis_territory_id"], "name": name, "inclusion_status": t["inclusion_status"]})
	}
	out["territories"] = territories
	if kind == "subpublishing_agreement" {
		out["mech_agreement_number"], out["pr_royalty_share"], out["mr_royalty_share"] = a["mech_agreement_number"], a["pr_royalty_share"], a["mr_royalty_share"]
	} else {
		out["admin_fee_share"], out["licensing_authority"] = a["admin_fee_share"], a["licensing_authority"]
	}
	return out
}

func (s *Server) agreements(req *request, rest []string, kind string, list *[]record) {
	if !req.authorize(Viewer) {
		return
	}
	company := req.company
	territoriesKey := "subpublishing_agreements_tis_territories_attributes"
	if kind == "admin_agreement" {
		territoriesKey = "admin_agreements_tis_territories_attributes"
	}
	apply := func(a record, params record) {
		for key, value := range params {
			if key == territoriesKey {
				for _, t := range listOfRecords(value) {
					entry := copyRecord(t)
					entry["id"] = s.id()
					a["territories"] = append(listOfAny(a["territories"]), entry)
				}
				continue
			}
			a[key] = value
		}
	}
	if len(rest) == 0 {
		switch req.r.Method {
		case http.MethodGet:
			var out []record
			query := req.r.URL.Query()
			filterParam := func(name string) string {
				if v := query.Get("q[" + name + "]"); v != "" {
					return v
				}
				return query.Get(name)
			}
			for _, a := range *list {
				if v := filterParam("assignor_external_id"); v != "" && str(a["assignor_external_id"]) != v {
					continue
				}
				if v := filterParam("assignee_external_id"); v != "" && str(a["assignee_external_id"]) != v {
					continue
				}
				out = append(out, s.renderAgreement(req, a, kind))
			}
			page, pagination := paginate(req.r, out, 0)
			writeJSON(req.w, http.StatusOK, record{kind + "s": page, "pagination": pagination})
		case http.MethodPost:
			if !req.authorize(Editor) {
				return
			}
			params, ok := req.wrapped(kind)
			if !ok {
				return
			}
			for _, key := range []string{"assignor_external_id", "assignee_external_id"} {
				if findExternal(company.Publishers, str(params[key])) < 0 {
					notFound(req.w)
					return
				}
			}
			a := record{"id": s.id(), "territories": []any{}}
			apply(a, params)
			if blank(a["starts_on"]) {
				invalid(req.w, "Starts on can't be blank")
				return
			}
			*list = append(*list, a)
			writeJSON(req.w, http.StatusCreated, s.renderAgreement(req, a, kind))
		default:
			routingError(req.w)
		}
		return
	}
	id, _ := toInt(rest[0])
	index := -1
	for i, a := range *list {
		if mustInt(a["id"]) == id {
			index = i
		}
	}
	if index < 0 || len(rest) > 1 {
		notFound(req.w)
		return
	}
	a := (*list)[index]
	switch req.r.Method {
	case http.MethodGet:
		writeJSON(req.w, http.StatusOK, s.renderAgreement(req, a, kind))
	case http.MethodPatch, http.MethodPut:
		if !req.authorize(Editor) {
			return
		}
		params, ok := req.wrapped(kind)
		if !ok {
			return
		}
		delete(params, "assignor_external_id")
		delete(params, "assignee_external_id")
		apply(a, params)
		writeJSON(req.w, http.StatusOK, s.renderAgreement(req, a, kind))
	case http.MethodDelete:
		if !req.authorize(Editor) {
			return
		}
		*list = append((*list)[:index], (*list)[index+1:]...)
		req.w.WriteHeader(http.StatusNoContent)
	default:
		routingError(req.w)
	}
}

// ---- reference data -------------------------------------------------------

func (s *Server) pros(req *request, rest []string) {
	if req.r.Method != http.MethodGet {
		routingError(req.w)
		return
	}
	if len(rest) == 0 {
		var out []record
		for _, pro := range s.Pros {
			out = append(out, record{"id": pro["id"], "abbreviation": pro["abbreviation"], "name": pro["name"], "created_at": pro["created_at"], "url": fmt.Sprintf("%s/pros/%v", req.base, pro["id"])})
		}
		sortBy(out, "abbreviation")
		page, pagination := paginate(req.r, out, 0)
		writeJSON(req.w, http.StatusOK, record{"pros": page, "pagination": pagination})
		return
	}
	// FlexibleFinder: by ID, or by abbreviation in upper case.
	for _, pro := range s.Pros {
		if (str(pro["id"]) == rest[0] || str(pro["abbreviation"]) == strings.ToUpper(rest[0])) && len(rest) == 1 {
			writeJSON(req.w, http.StatusOK, pro)
			return
		}
	}
	notFound(req.w)
}

// paginatedReference says which reference lists come with a pagination
// block; royalty_sources and sales_file_formats come whole.
var paginatedReference = map[string]bool{"tis_territories": true, "cis_languages": true, "title_types": true, "writer_designations": true, "publisher_types": true, "movies": true, "series": true, "episodes": true,
	"works_file_formats": true, "registration_types": true, "streamers": true, "production_companies": true, "tis_territory_types": true, "cwr_destinations": true}

func (s *Server) reference(req *request, kind string, list []record, rest []string) {
	if req.r.Method != http.MethodGet {
		routingError(req.w)
		return
	}
	if len(rest) == 0 {
		if !paginatedReference[kind] {
			writeJSON(req.w, http.StatusOK, record{kind: list})
			return
		}
		page, pagination := paginate(req.r, list, 0)
		writeJSON(req.w, http.StatusOK, record{kind: page, "pagination": pagination})
		return
	}
	if len(rest) != 1 {
		routingError(req.w)
		return
	}
	for _, item := range list {
		if str(item["id"]) == rest[0] || (item["code"] != nil && str(item["code"]) == strings.ToUpper(rest[0])) {
			writeJSON(req.w, http.StatusOK, item)
			return
		}
	}
	notFound(req.w)
}

// productions are movies, series and episodes, with title search.
func (s *Server) productions(req *request, segments []string) {
	kind, rest := segments[0], segments[1:]
	list := s.Reference[kind]
	if len(rest) == 1 && rest[0] == "search" {
		var matches []record
		for _, item := range list {
			if contains(str(item["title"]), req.r.URL.Query().Get("q")) {
				matches = append(matches, item)
			}
		}
		page, pagination := paginate(req.r, matches, 0)
		writeJSON(req.w, http.StatusOK, record{kind: page, "pagination": pagination})
		return
	}
	if kind == "series" && len(rest) >= 2 && rest[1] == "episodes" {
		var episodes []record
		for _, episode := range s.Reference["episodes"] {
			if str(episode["series_id"]) == rest[0] && (len(rest) < 3 || rest[2] != "search" || contains(str(episode["title"]), req.r.URL.Query().Get("q"))) {
				episodes = append(episodes, episode)
			}
		}
		if len(rest) == 3 && rest[2] != "search" {
			for _, episode := range episodes {
				if str(episode["id"]) == rest[2] {
					writeJSON(req.w, http.StatusOK, episode)
					return
				}
			}
			notFound(req.w)
			return
		}
		page, pagination := paginate(req.r, episodes, 0)
		writeJSON(req.w, http.StatusOK, record{"episodes": page, "pagination": pagination})
		return
	}
	if len(rest) == 1 {
		for _, item := range list {
			if str(item["id"]) == rest[0] || str(item["imdb_id"]) == rest[0] {
				writeJSON(req.w, http.StatusOK, item)
				return
			}
		}
		notFound(req.w)
		return
	}
	s.reference(req, kind, list, rest)
}
