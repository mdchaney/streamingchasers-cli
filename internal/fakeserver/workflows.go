package fakeserver

import (
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"
)

// ---- uploads --------------------------------------------------------------

// uploadKind says how one kind of background-processed upload behaves.
type uploadKind struct {
	segment, singular string
	noDelete          bool
	// required are the fields a create must have, besides the file.
	required []string
}

var (
	uploadWorks     = uploadKind{segment: "works_uploads", singular: "works_upload", required: []string{"works_file_format_id"}}
	uploadSales     = uploadKind{segment: "sales_uploads", singular: "sales_upload"}
	uploadStatement = uploadKind{segment: "royalty_statements", singular: "royalty_statement", noDelete: true, required: []string{"royalty_source_id"}}
	uploadDump      = uploadKind{segment: "pro_data_dumps", singular: "pro_data_dump", required: []string{"pro_id"}}
)

// process advances an upload's background processing by one look.
func (s *Server) process(u *Upload) {
	u.Polls++
	if u.Fields["processed_at"] != nil || u.Polls < s.PollsToFinish {
		return
	}
	u.Fields["processed_at"] = time.Now().Format(time.RFC3339)
	rows := strings.Count(strings.TrimSpace(string(u.File)), "\n")
	u.Fields["total_rows"], u.Fields["processed_rows"] = rows, rows
	if s.FailUploads {
		u.Fields["success"] = false
		u.Fields["error_list"] = "Row 2: title is blank"
		return
	}
	u.Fields["success"] = true
	if u.Fields["codes_only"] == "true" || u.Fields["match_by_title"] == "true" {
		u.Report = []byte("row,title,matched\n1,Drunken Daisy,W-999\n")
	}
}

func status(u *Upload) string {
	switch {
	case u.Fields["processed_at"] == nil:
		return "pending"
	case u.Fields["success"] == true || (u.Fields["success"] == nil && blank(u.Fields["error_list"])):
		return "completed"
	}
	return "failed"
}

func (s *Server) renderUpload(req *request, kind uploadKind, u *Upload) record {
	f := u.Fields
	out := record{"id": f["id"], "notes": f["notes"], "processed_at": f["processed_at"], "status": status(u), "created_at": f["created_at"], "updated_at": f["updated_at"]}
	base := fmt.Sprintf("%s/companies/%d/%s/%v", req.base, req.company.ID, kind.segment, f["id"])
	out["url"], out["csv_file_url"] = base, base+"/csv_file"
	switch kind.segment {
	case "works_uploads":
		for _, key := range []string{"company_id", "user_id", "works_file_format_id", "pro_id", "success", "total_rows", "processed_rows", "error_list", "match_by_title", "codes_only"} {
			out[key] = f[key]
		}
		out["processed"] = f["processed_at"] != nil
		out["report_file_available"] = u.Report != nil
		out["csv_file"] = record{"filename": u.Name, "content_type": "text/csv", "byte_size": len(u.File)}
		for _, format := range s.Reference["works_file_formats"] {
			if mustInt(format["id"]) == mustInt(f["works_file_format_id"]) {
				out["works_file_format"] = record{"id": format["id"], "name": format["name"], "format_type": format["format_type"], "requires_pro": format["requires_pro"]}
			}
		}
		if pro := s.pro(f["pro_id"]); pro != nil {
			out["pro"] = record{"id": pro["id"], "name": pro["name"], "abbreviation": pro["abbreviation"]}
		}
	case "sales_uploads":
		out["filename"], out["file_size"], out["content_type"] = u.Name, len(u.File), "text/csv"
		out["processed"], out["error_list"] = f["processed_at"] != nil, f["error_list"]
		count := 0
		for _, sale := range req.company.Sales {
			if mustInt(sale["sales_upload_id"]) == mustInt(f["id"]) {
				count++
			}
		}
		out["sales_count"] = count
	case "royalty_statements":
		out["filename"], out["file_size"], out["content_type"] = u.Name, len(u.File), "text/csv"
		out["starts_at"], out["ends_at"], out["success"], out["error_list"] = f["starts_at"], f["ends_at"], f["success"], f["error_list"]
		count := 0
		for _, r := range req.company.RawRoyaltyRecords {
			if mustInt(r["royalty_statement_id"]) == mustInt(f["id"]) {
				count++
			}
		}
		out["records_count"] = count
		for _, source := range s.Reference["royalty_sources"] {
			if mustInt(source["id"]) == mustInt(f["royalty_source_id"]) {
				out["source_name"] = source["name"]
				out["royalty_source"] = record{"id": source["id"], "name": source["name"], "pro_name": source["pro_name"]}
				for _, format := range listOfRecords(source["royalty_file_formats"]) {
					if mustInt(format["id"]) == mustInt(f["royalty_file_format_id"]) {
						out["format_name"] = format["name"]
						out["royalty_file_format"] = record{"id": format["id"], "name": format["name"]}
					}
				}
			}
		}
	case "pro_data_dumps":
		for _, key := range []string{"company_id", "pro_id", "success", "total_records", "new_codes_count", "error_list"} {
			out[key] = f[key]
		}
		if pro := s.pro(f["pro_id"]); pro != nil {
			out["pro"] = pro["abbreviation"]
		}
		out["csv_file"] = record{"filename": u.Name, "content_type": "text/csv", "byte_size": len(u.File)}
	}
	return out
}

func (s *Server) uploads(req *request, rest []string, kind uploadKind, list *[]*Upload) {
	if !req.authorize(Viewer) {
		return
	}
	company := req.company
	if len(rest) == 0 {
		switch req.r.Method {
		case http.MethodGet:
			var out []record
			for i := len(*list) - 1; i >= 0; i-- {
				out = append(out, s.renderUpload(req, kind, (*list)[i]))
			}
			page, pagination := paginate(req.r, out, 0)
			writeJSON(req.w, http.StatusOK, record{kind.segment: page, "pagination": pagination})
		case http.MethodPost:
			if !req.authorize(Editor) {
				return
			}
			key := req.r.Header.Get("Idempotency-Key")
			if key != "" {
				for _, u := range *list {
					if u.Key == key {
						req.w.Header().Set("Idempotency-Replayed", "true")
						writeJSON(req.w, http.StatusOK, s.renderUpload(req, kind, u))
						return
					}
				}
			}
			params, ok := req.wrapped(kind.singular)
			if !ok {
				return
			}
			var problems []string
			if req.file == nil {
				problems = append(problems, "Csv file can't be blank")
			}
			for _, field := range kind.required {
				if blank(params[field]) {
					problems = append(problems, strings.ToUpper(field[:1])+strings.ReplaceAll(field[1:], "_", " ")+" must exist")
				}
			}
			if kind.segment == "pro_data_dumps" && s.pro(params["pro_id"]) == nil {
				problems = append(problems, "Pro must exist")
			}
			if len(problems) > 0 {
				invalid(req.w, problems...)
				return
			}
			fields := record{"id": s.id(), "company_id": company.ID, "user_id": req.user.ID, "processed_at": nil, "success": nil, "error_list": nil, "notes": nil}
			for k, v := range params {
				fields[k] = v
			}
			if kind.segment == "sales_uploads" && blank(fields["sales_file_format_id"]) {
				fields["sales_file_format_id"] = "1"
			}
			stamp(fields, false)
			u := &Upload{Fields: fields, File: req.file, Name: req.fileName, Key: key}
			*list = append(*list, u)
			writeJSON(req.w, http.StatusCreated, s.renderUpload(req, kind, u))
		default:
			routingError(req.w)
		}
		return
	}

	var u *Upload
	index := -1
	for i, candidate := range *list {
		if str(candidate.Fields["id"]) == rest[0] {
			u, index = candidate, i
		}
	}
	if u == nil {
		notFound(req.w)
		return
	}
	rest = rest[1:]
	if len(rest) == 0 {
		switch req.r.Method {
		case http.MethodGet:
			s.process(u)
			writeJSON(req.w, http.StatusOK, s.renderUpload(req, kind, u))
		case http.MethodDelete:
			if kind.noDelete {
				routingError(req.w)
				return
			}
			if !req.authorize(Editor) {
				return
			}
			*list = append((*list)[:index], (*list)[index+1:]...)
			req.w.WriteHeader(http.StatusNoContent)
		default:
			routingError(req.w)
		}
		return
	}
	switch rest[0] {
	case "csv_file":
		sendFile(req.w, u.Name, "text/csv", u.File)
	case "report_file":
		if kind.segment != "works_uploads" {
			routingError(req.w)
			return
		}
		if u.Report == nil {
			message(req.w, http.StatusNotFound, "File not found")
			return
		}
		sendFile(req.w, strings.TrimSuffix(u.Name, ".csv")+"_report.csv", "text/csv", u.Report)
	case "sales":
		if kind.segment != "sales_uploads" {
			routingError(req.w)
			return
		}
		s.sales(req, rest[1:], u)
	case "reprocess":
		if kind.segment != "royalty_statements" || req.r.Method != http.MethodPost {
			routingError(req.w)
			return
		}
		if !req.authorize(Editor) {
			return
		}
		u.Fields["processed_at"], u.Fields["success"], u.Fields["error_list"] = nil, nil, nil
		u.Polls = 0
		writeJSON(req.w, http.StatusAccepted, s.renderUpload(req, kind, u))
	case "streamers":
		if kind.segment != "royalty_statements" {
			routingError(req.w)
			return
		}
		s.streamers(req, u)
	case "raw_royalty_records":
		if kind.segment != "royalty_statements" {
			routingError(req.w)
			return
		}
		s.rawRoyaltyRecords(req, rest[1:], u)
	default:
		routingError(req.w)
	}
}

func (s *Server) streamers(req *request, u *Upload) {
	type key struct{ streamer, channel string }
	groups := map[key]record{}
	var order []key
	totalRows, matchedRows := 0, 0
	for _, r := range req.company.RawRoyaltyRecords {
		if mustInt(r["royalty_statement_id"]) != mustInt(u.Fields["id"]) {
			continue
		}
		k := key{str(r["streamer_name"]), str(r["channel_name"])}
		group, ok := groups[k]
		if !ok {
			group = record{"streamer_name": r["streamer_name"], "channel_name": r["channel_name"], "rows": 0, "amount": 0.0, "uses": 0, "currency": r["currency"], "matched_streamer": nil}
			if r["streamer_id"] != nil {
				for _, streamer := range s.Reference["streamers"] {
					if mustInt(streamer["id"]) == mustInt(r["streamer_id"]) {
						group["matched_streamer"] = record{"id": streamer["id"], "name": streamer["name"]}
					}
				}
			}
			groups[k] = group
			order = append(order, k)
		}
		group["rows"] = group["rows"].(int) + 1
		var amount float64
		fmt.Sscan(str(r["gross_amount"]), &amount)
		group["amount"] = group["amount"].(float64) + amount
		group["uses"] = group["uses"].(int) + int(mustInt(r["uses"]))
		totalRows++
		if r["streamer_id"] != nil {
			matchedRows++
		}
	}
	rows := []record{}
	for _, k := range order {
		rows = append(rows, groups[k])
	}
	sort.SliceStable(rows, func(i, j int) bool {
		return (rows[i]["matched_streamer"] == nil) && (rows[j]["matched_streamer"] != nil)
	})
	writeJSON(req.w, http.StatusOK, record{"id": u.Fields["id"], "total_rows": totalRows, "matched_rows": matchedRows, "total_amount": "15.5", "matched_amount": "12.5", "streamers": rows})
}

func (s *Server) rawRoyaltyRecords(req *request, rest []string, statement *Upload) {
	if !req.authorize(Viewer) || req.r.Method != http.MethodGet {
		return
	}
	company := req.company
	var list []record
	for _, r := range company.RawRoyaltyRecords {
		if statement == nil || mustInt(r["royalty_statement_id"]) == mustInt(statement.Fields["id"]) {
			list = append(list, r)
		}
	}
	render := func(r record) record {
		out := copyRecord(r)
		delete(out, "streamer_id")
		delete(out, "uses")
		out["url"] = fmt.Sprintf("%s/companies/%d/royalty_statements/%v/raw_royalty_records/%v", req.base, company.ID, r["royalty_statement_id"], r["id"])
		return out
	}
	if len(rest) == 0 {
		list = filterQ(req.r, list, map[string][]string{"title": {"work_title"}, "iswc": {"iswc"}, "work_code": {"work_code"}, "streamer_name": {"streamer_name"}, "territory_code": {"territory_code"}})
		if req.r.URL.Query().Get("q[has_issues]") == "1" {
			var kept []record
			for _, r := range list {
				if r["has_issues"] == true {
					kept = append(kept, r)
				}
			}
			list = kept
		}
		var out []record
		for _, r := range list {
			out = append(out, render(r))
		}
		page, pagination := paginate(req.r, out, 0)
		writeJSON(req.w, http.StatusOK, record{"raw_royalty_records": page, "pagination": pagination})
		return
	}
	for _, r := range list {
		if str(r["id"]) == rest[0] && len(rest) == 1 {
			writeJSON(req.w, http.StatusOK, render(r))
			return
		}
	}
	notFound(req.w)
}

// ---- claims ---------------------------------------------------------------

// availablePeriods are the periods of a PRO that no batch has locked: a
// batch locks its period and every earlier one.
func (s *Server) availablePeriods(company *Company, proID int64) []record {
	latest := ""
	for _, batch := range company.Batches {
		if mustInt(batch["pro_id"]) != proID {
			continue
		}
		for _, period := range s.PaymentPeriods[proID] {
			if mustInt(period["id"]) == mustInt(batch["broadcast_payment_period_id"]) && str(period["report_date"]) > latest {
				latest = str(period["report_date"])
			}
		}
	}
	var available []record
	for _, period := range s.PaymentPeriods[proID] {
		if str(period["report_date"]) > latest {
			available = append(available, period)
		}
	}
	return available
}

func (s *Server) period(proID int64, id any) record {
	for _, period := range s.PaymentPeriods[proID] {
		if mustInt(period["id"]) == mustInt(id) {
			return period
		}
	}
	return nil
}

func (s *Server) renderUnpaid(req *request, row record, proID int64) record {
	work := record{}
	if i := findExternal(req.company.Works, str(row["work_external_id"])); i >= 0 {
		work = req.company.Works[i]
	}
	return record{
		"id": row["id"], "work": record{"id": work["external_id"], "title": work["title"]}, "registration_code": s.workCode(work, proID),
		"production_title": row["production_title"], "film_title": row["film_title"], "series_title": row["series_title"], "episode_title": row["episode_title"],
		"episode_number": row["episode_number"], "air_date": row["air_date"], "streamer": row["streamer"], "territory": row["territory"],
	}
}

// eligibility sorts a PRO's unpaid rows into those a batch would take,
// those left out for missing codes, and those left out as rolled up.
func (s *Server) eligibility(company *Company, proID int64, minMoney float64, minUnpaid int64) (includable, missing, rolled []record) {
	rolledSeries := map[int64]bool{}
	for _, r := range company.Rollups[proID] {
		if r["production_type"] == "Series" {
			rolledSeries[mustInt(r["production_id"])] = true
		}
	}
	scores := map[int64]record{}
	for _, score := range company.ChaseScores[proID] {
		scores[mustInt(score["series_id"])] = score
	}
	for _, row := range company.Unpaid[proID] {
		i := findExternal(company.Works, str(row["work_external_id"]))
		switch {
		case i < 0 || !s.workHasCode(company.Works[i], proID):
			missing = append(missing, row)
		case row["series_id"] != nil && rolledSeries[mustInt(row["series_id"])]:
			rolled = append(rolled, row)
		default:
			if score, ok := scores[mustInt(row["series_id"])]; ok && row["series_id"] != nil {
				var money float64
				fmt.Sscan(str(score["money_observed"]), &money)
				if money < minMoney || mustInt(score["unpaid_broadcasts_count"]) < minUnpaid {
					continue
				}
			}
			includable = append(includable, row)
		}
	}
	return
}

func (s *Server) claimsUnderPro(req *request, rest []string) {
	if !req.authorize(Viewer) || len(rest) < 2 {
		if len(rest) < 2 {
			routingError(req.w)
		}
		return
	}
	pro := s.pro(rest[0])
	if pro == nil {
		notFound(req.w)
		return
	}
	proID := mustInt(pro["id"])
	company := req.company
	rest = rest[1:]
	switch rest[0] {
	case "payment_periods":
		available := map[int64]bool{}
		for _, period := range s.availablePeriods(company, proID) {
			available[mustInt(period["id"])] = true
		}
		out := []record{}
		for _, period := range s.PaymentPeriods[proID] {
			if req.r.URL.Query().Get("available") == "1" && !available[mustInt(period["id"])] {
				continue
			}
			entry := copyRecord(period)
			entry["available"] = available[mustInt(period["id"])]
			entry["batch_id"] = nil
			for _, batch := range company.Batches {
				if mustInt(batch["broadcast_payment_period_id"]) == mustInt(period["id"]) {
					entry["batch_id"] = batch["id"]
				}
			}
			out = append(out, entry)
		}
		writeJSON(req.w, http.StatusOK, record{"pro": pro["abbreviation"], "payment_periods": out})
	case "unpaid_broadcasts":
		var out []record
		for _, row := range company.Unpaid[proID] {
			if i := findExternal(company.Works, str(row["work_external_id"])); i >= 0 && s.workHasCode(company.Works[i], proID) {
				out = append(out, s.renderUnpaid(req, row, proID))
			}
		}
		page, pagination := paginate(req.r, out, 100)
		writeJSON(req.w, http.StatusOK, record{"pro": pro["abbreviation"], "unpaid_broadcasts": page, "pagination": pagination})
	case "broadcast_delivery_batches":
		s.batchesUnderPro(req, rest[1:], pro)
	case "series_chase_scores":
		s.chaseScores(req, rest[1:], pro)
	case "rolled_up_productions":
		s.rollups(req, rest[1:], pro)
	default:
		routingError(req.w)
	}
}

func (s *Server) batchesUnderPro(req *request, rest []string, pro record) {
	company := req.company
	proID := mustInt(pro["id"])
	query := req.r.URL.Query()
	unavailable := func(period record) bool {
		for _, available := range s.availablePeriods(company, proID) {
			if mustInt(available["id"]) == mustInt(period["id"]) {
				return false
			}
		}
		message(req.w, http.StatusConflict, fmt.Sprintf("%s is no longer available - a batch already exists for this or a later %s payment period.", period["name"], pro["abbreviation"]))
		return true
	}
	firstBatch := true
	for _, batch := range company.Batches {
		if mustInt(batch["pro_id"]) == proID {
			firstBatch = false
		}
	}
	var minMoney float64
	fmt.Sscan(query.Get("min_money"), &minMoney)
	minUnpaid := mustInt(query.Get("min_unpaid"))

	switch {
	case len(rest) == 0 && req.r.Method == http.MethodPost:
		if !req.authorize(Editor) {
			return
		}
		params, ok := req.wrapped("broadcast_delivery_batch")
		if !ok {
			return
		}
		period := s.period(proID, params["broadcast_payment_period_id"])
		if period == nil {
			notFound(req.w)
			return
		}
		if unavailable(period) {
			return
		}
		fmt.Sscan(str(params["min_money_observed"]), &minMoney)
		minUnpaid = mustInt(params["min_unpaid_placements"])
		includable, missing, rolled := s.eligibility(company, proID, minMoney, minUnpaid)
		excluded := map[int64]bool{}
		for _, id := range listOfAny(req.params["excluded_broadcasts_pros_sale_ids"]) {
			excluded[mustInt(id)] = true
		}
		var rows []any
		for _, row := range includable {
			if !excluded[mustInt(row["id"])] {
				rows = append(rows, row["id"])
			}
		}
		batch := record{"id": s.id(), "pro_id": proID, "broadcast_payment_period_id": period["id"], "notes": params["notes"], "sent_at": nil,
			"min_money_observed": params["min_money_observed"], "min_views_total_est": params["min_views_total_est"], "min_unpaid_placements": params["min_unpaid_placements"],
			"rows": rows, "created_at": time.Now().Format(time.RFC3339)}
		company.Batches = append(company.Batches, batch)
		out := s.renderBatch(req, batch)
		out["added_count"], out["excluded_by_client"], out["excluded_missing_codes"], out["excluded_rolled_up"] = len(rows), len(excluded), len(missing), len(rolled)
		writeJSON(req.w, http.StatusCreated, out)
	case len(rest) == 1 && rest[0] == "preview":
		period := s.period(proID, query.Get("payment_period_id"))
		if period == nil {
			if query.Get("payment_period_id") == "" {
				writeJSON(req.w, http.StatusBadRequest, record{"status": 400, "error": "param is missing or the value is empty: payment_period_id"})
				return
			}
			notFound(req.w)
			return
		}
		if unavailable(period) {
			return
		}
		includable, missing, rolled := s.eligibility(company, proID, minMoney, minUnpaid)
		var rows []record
		for _, row := range includable {
			rows = append(rows, s.renderUnpaid(req, row, proID))
		}
		page, pagination := paginate(req.r, rows, 100)
		rolledPreview := []record{}
		if len(rolled) > 0 {
			rolledPreview = append(rolledPreview, record{"production_type": "Series", "title": "Bundle Show", "excluded_count": len(rolled)})
		}
		writeJSON(req.w, http.StatusOK, record{
			"pro": pro["abbreviation"], "payment_period": record{"id": period["id"], "name": period["name"], "report_date": period["report_date"]},
			"is_first_batch": firstBatch, "total_includable": len(includable), "excluded_missing_codes": len(missing), "excluded_rolled_up": len(rolled),
			"rolled_up_productions": rolledPreview, "rows": page, "pagination": pagination,
		})
	case len(rest) == 1 && rest[0] == "missing_work_ids":
		seen := map[string]bool{}
		works := []record{}
		for _, row := range company.Unpaid[proID] {
			id := str(row["work_external_id"])
			i := findExternal(company.Works, id)
			if i < 0 || seen[id] || s.workHasCode(company.Works[i], proID) {
				continue
			}
			seen[id] = true
			works = append(works, record{"id": id, "title": company.Works[i]["title"], "catalog": company.Works[i]["catalog"]})
		}
		var registrationType any
		for _, t := range s.Reference["registration_types"] {
			if mustInt(t["pro_id"]) == proID {
				registrationType = record{"id": t["id"], "name": t["name"], "work_id_description": t["work_id_description"]}
			}
		}
		writeJSON(req.w, http.StatusOK, record{"pro": pro["abbreviation"], "registration_type": registrationType, "missing_works": works})
	case len(rest) == 1 && rest[0] == "missing_codes_csv":
		period := s.period(proID, query.Get("payment_period_id"))
		if period == nil {
			notFound(req.w)
			return
		}
		_, missing, _ := s.eligibility(company, proID, 0, 0)
		body := "\xEF\xBB\xBFwork,title,production\n"
		for _, row := range missing {
			body += fmt.Sprintf("%s,%s,%s\n", row["work_external_id"], "", row["production_title"])
		}
		sendFile(req.w, fmt.Sprintf("%s_%s_VOD_%s_missing_work_codes.csv", strings.ReplaceAll(company.Name, " ", "_"), pro["abbreviation"], strings.ReplaceAll(str(period["name"]), " ", "_")), "text/csv; charset=utf-8", []byte(body))
	default:
		routingError(req.w)
	}
}

func (s *Server) renderBatch(req *request, batch record) record {
	pro := s.pro(batch["pro_id"])
	period := s.period(mustInt(batch["pro_id"]), batch["broadcast_payment_period_id"])
	return record{
		"id": batch["id"], "pro": pro["abbreviation"],
		"payment_period": record{"id": period["id"], "name": period["name"], "report_date": period["report_date"]},
		"notes":          batch["notes"], "sent_at": batch["sent_at"], "min_money_observed": batch["min_money_observed"], "min_views_total_est": batch["min_views_total_est"], "min_unpaid_placements": batch["min_unpaid_placements"],
		"created_at": batch["created_at"], "broadcasts_count": len(listOfAny(batch["rows"])),
		"url":     fmt.Sprintf("%s/companies/%d/broadcast_delivery_batches/%v", req.base, req.company.ID, batch["id"]),
		"csv_url": fmt.Sprintf("%s/companies/%d/broadcast_delivery_batches/%v/csv", req.base, req.company.ID, batch["id"]),
	}
}

func (s *Server) batches(req *request, rest []string) {
	if !req.authorize(Viewer) || req.r.Method != http.MethodGet {
		if req.r.Method != http.MethodGet {
			routingError(req.w)
		}
		return
	}
	company := req.company
	if len(rest) == 0 {
		var out []record
		for i := len(company.Batches) - 1; i >= 0; i-- {
			batch := company.Batches[i]
			if pro := req.r.URL.Query().Get("pro_id"); pro != "" && str(batch["pro_id"]) != pro {
				continue
			}
			out = append(out, s.renderBatch(req, batch))
		}
		page, pagination := paginate(req.r, out, 100)
		writeJSON(req.w, http.StatusOK, record{"broadcast_delivery_batches": page, "pagination": pagination})
		return
	}
	var batch record
	for _, candidate := range company.Batches {
		if str(candidate["id"]) == rest[0] {
			batch = candidate
		}
	}
	if batch == nil {
		notFound(req.w)
		return
	}
	switch {
	case len(rest) == 1:
		writeJSON(req.w, http.StatusOK, s.renderBatch(req, batch))
	case len(rest) == 2 && rest[1] == "csv":
		pro := s.pro(batch["pro_id"])
		if req.r.URL.Query().Get("variant") == "missing_codes" {
			_, missing, _ := s.eligibility(company, mustInt(pro["id"]), 0, 0)
			body := "\xEF\xBB\xBFwork,production\n"
			for _, row := range missing {
				body += fmt.Sprintf("%s,%s\n", row["work_external_id"], row["production_title"])
			}
			sendFile(req.w, fmt.Sprintf("batch_%v_missing_work_codes.csv", batch["id"]), "text/csv; charset=utf-8", []byte(body))
			return
		}
		body := "\xEF\xBB\xBFwork,code,production,aired"
		if req.r.URL.Query().Get("with_notes") != "0" {
			body += ",notes"
		}
		body += "\n"
		for _, id := range listOfAny(batch["rows"]) {
			for _, row := range company.Unpaid[mustInt(pro["id"])] {
				if mustInt(row["id"]) == mustInt(id) {
					i := findExternal(company.Works, str(row["work_external_id"]))
					body += fmt.Sprintf("%s,%v,%s,%s\n", row["work_external_id"], s.workCode(company.Works[i], mustInt(pro["id"])), row["production_title"], row["air_date"])
				}
			}
		}
		batch["sent_at"] = time.Now().Format(time.RFC3339)
		sendFile(req.w, fmt.Sprintf("%s_%s_batch_%v.csv", strings.ReplaceAll(company.Name, " ", "_"), pro["abbreviation"], batch["id"]), "text/csv; charset=utf-8", []byte(body))
	default:
		routingError(req.w)
	}
}

func (s *Server) chaseScores(req *request, rest []string, pro record) {
	company := req.company
	proID := mustInt(pro["id"])
	render := func(score record) record {
		return record{"series": record{"id": score["series_id"], "title": score["series_title"]}, "money_observed": score["money_observed"], "views_observed": score["views_observed"],
			"views_estimated_missing": score["views_estimated_missing"], "views_total_est": score["views_total_est"], "paid_broadcasts_count": score["paid_broadcasts_count"],
			"unpaid_broadcasts_count": score["unpaid_broadcasts_count"], "total_broadcasts_count": score["total_broadcasts_count"], "computed_at": score["computed_at"]}
	}
	switch {
	case len(rest) == 0 && req.r.Method == http.MethodGet:
		sortKey := req.r.URL.Query().Get("sort")
		columns := map[string]string{"money": "money_observed", "views_observed": "views_observed", "views": "views_total_est", "paid": "paid_broadcasts_count", "unpaid": "unpaid_broadcasts_count", "updated": "computed_at"}
		if _, ok := columns[sortKey]; !ok {
			sortKey = "money"
		}
		rolled := map[int64]bool{}
		for _, r := range company.Rollups[proID] {
			rolled[mustInt(r["production_id"])] = true
		}
		var out []record
		hiddenRolled, hiddenPaid := 0, 0
		for _, score := range company.ChaseScores[proID] {
			switch {
			case rolled[mustInt(score["series_id"])]:
				hiddenRolled++
			case mustInt(score["unpaid_broadcasts_count"]) == 0:
				hiddenPaid++
			default:
				out = append(out, render(score))
			}
		}
		sort.SliceStable(out, func(i, j int) bool {
			var a, b float64
			fmt.Sscan(str(out[i][columns[sortKey]]), &a)
			fmt.Sscan(str(out[j][columns[sortKey]]), &b)
			return a > b
		})
		page, pagination := paginate(req.r, out, 50)
		writeJSON(req.w, http.StatusOK, record{"pro": pro["abbreviation"], "sort": sortKey, "rolled_up_hidden_count": hiddenRolled, "fully_paid_hidden_count": hiddenPaid, "last_computed_at": seedTime, "series_chase_scores": page, "pagination": pagination})
	case len(rest) == 1 && rest[0] == "recompute" && req.r.Method == http.MethodPost:
		writeJSON(req.w, http.StatusAccepted, record{"message": "Recompute enqueued for " + company.Name})
	case len(rest) >= 1 && req.r.Method == http.MethodGet:
		for _, score := range company.ChaseScores[proID] {
			if str(score["series_id"]) != rest[0] {
				continue
			}
			if len(rest) == 1 {
				writeJSON(req.w, http.StatusOK, render(score))
				return
			}
			if len(rest) == 2 && rest[1] == "placements" {
				var rows []record
				for _, row := range company.Unpaid[proID] {
					if mustInt(row["series_id"]) == mustInt(score["series_id"]) {
						i := findExternal(company.Works, str(row["work_external_id"]))
						title := any(nil)
						if i >= 0 {
							title = company.Works[i]["title"]
						}
						rows = append(rows, record{"id": row["id"], "paid_at": nil, "episode_title": row["episode_title"], "season_number": 1, "episode_number": row["episode_number"], "work_title": title, "streamer": row["streamer"], "air_date": row["air_date"], "money_observed": row["money"], "views_observed": row["views"]})
					}
				}
				page, pagination := paginate(req.r, rows, 0)
				writeJSON(req.w, http.StatusOK, record{"pro": pro["abbreviation"], "series": record{"id": score["series_id"], "title": score["series_title"]}, "placements": page, "pagination": pagination})
				return
			}
		}
		notFound(req.w)
	default:
		routingError(req.w)
	}
}

func (s *Server) rollups(req *request, rest []string, pro record) {
	company := req.company
	proID := mustInt(pro["id"])
	summaries := company.Rollups[proID]
	switch {
	case len(rest) == 0:
		totalUnpaid, totalRecords := 0, 0
		for _, r := range summaries {
			totalUnpaid += int(mustInt(r["unpaid_broadcasts_blocked"]))
			totalRecords += int(mustInt(r["rollup_records"]))
		}
		sortKey := req.r.URL.Query().Get("sort")
		if sortKey == "" {
			sortKey = "unpaid"
		}
		page, pagination := paginate(req.r, summaries, 50)
		writeJSON(req.w, http.StatusOK, record{"pro": pro["abbreviation"], "sort": sortKey, "total_productions": len(summaries), "total_unpaid_blocked": totalUnpaid, "total_rollup_records": totalRecords, "rolled_up_productions": page, "pagination": pagination})
	case len(rest) == 2:
		for _, r := range summaries {
			if str(r["production_type"]) == rest[0] && str(r["production_id"]) == rest[1] {
				works := []record{}
				for _, row := range company.Unpaid[proID] {
					if mustInt(row["series_id"]) == mustInt(r["production_id"]) {
						i := findExternal(company.Works, str(row["work_external_id"]))
						works = append(works, record{"id": row["work_external_id"], "title": company.Works[i]["title"], "placements": 1, "paid": 0, "unpaid": 1, "itemized_lines": 0, "itemized_amount": "0.0"})
					}
				}
				writeJSON(req.w, http.StatusOK, record{
					"pro": pro["abbreviation"], "production": record{"type": r["production_type"], "id": r["production_id"], "title": r["title"]},
					"rollup_total": r["total_amount"], "currencies": r["currencies"],
					"rollup_records": []record{{"id": 9100, "royalty_statement_id": 31, "line_number": 7, "work_title": "VARIOUS", "rights_type": "PERF", "usage_start_date": "2025-01-01", "usage_end_date": "2025-06-30", "use_count": 40, "share": "100", "amount": "30.25", "currency": "USD"}, {"id": 9101, "royalty_statement_id": 31, "line_number": 8, "work_title": "VARIOUS", "rights_type": "PERF", "usage_start_date": "2025-07-01", "usage_end_date": "2025-12-31", "use_count": 30, "share": "100", "amount": "25.00", "currency": "USD"}},
					"works":          works, "unmatched_royalties": nil,
				})
				return
			}
		}
		notFound(req.w)
	default:
		routingError(req.w)
	}
}

// ---- CWR ------------------------------------------------------------------

func (s *Server) renderConnection(req *request, c record) record {
	queued := 0
	for _, ticket := range req.company.CwrTickets {
		if mustInt(ticket["company_cwr_connection_id"]) == mustInt(c["id"]) && ticket["open"] == true {
			queued++
		}
	}
	return record{
		"id": c["id"], "destination": record{"id": c["cwr_destination_id"], "name": c["destination_name"], "pro": c["destination_pro"]},
		"server_username": c["server_username"], "outbound_directory": c["outbound_directory"], "inbound_directory": c["inbound_directory"],
		"cwr_sender_name": c["cwr_sender_name"], "cwr_sender_ipi_number": c["cwr_sender_ipi_number"], "cwr_sender_filename_code": c["cwr_sender_filename_code"],
		"minimum_work_count": c["minimum_work_count"], "filename_serial_offset": c["filename_serial_offset"], "active": c["active"], "created_at": c["created_at"],
		"queued_tickets": queued,
	}
}

func (s *Server) connection(company *Company, id string) (record, int) {
	for i, c := range company.Connections {
		if str(c["id"]) == id {
			return c, i
		}
	}
	return nil, -1
}

func (s *Server) cwrConnections(req *request, rest []string) {
	company := req.company
	if len(rest) == 0 {
		switch req.r.Method {
		case http.MethodGet:
			if !req.authorize(Viewer) {
				return
			}
			out := []record{}
			for _, c := range company.Connections {
				out = append(out, s.renderConnection(req, c))
			}
			writeJSON(req.w, http.StatusOK, record{"cwr_connections": out})
		case http.MethodPost:
			if !req.authorize(Admin) {
				return
			}
			params, ok := req.wrapped("company_cwr_connection")
			if !ok {
				return
			}
			c := record{"id": s.id(), "outbound_directory": nil, "cwr_sender_filename_code": nil, "minimum_work_count": 1, "filename_serial_offset": 0, "active": true, "created_at": time.Now().Format(time.RFC3339)}
			for k, v := range params {
				c[k] = v
			}
			if problems := s.validateConnection(c); len(problems) > 0 {
				invalid(req.w, problems...)
				return
			}
			c["destination_name"], c["destination_pro"], c["pro_id"] = "ASCAP Delivery", "ASCAP", 10
			company.Connections = append(company.Connections, c)
			writeJSON(req.w, http.StatusCreated, s.renderConnection(req, c))
		default:
			routingError(req.w)
		}
		return
	}
	c, index := s.connection(company, rest[0])
	if c == nil {
		if !req.authorize(Viewer) {
			return
		}
		notFound(req.w)
		return
	}
	rest = rest[1:]
	if len(rest) == 0 {
		switch req.r.Method {
		case http.MethodGet:
			if !req.authorize(Viewer) {
				return
			}
			writeJSON(req.w, http.StatusOK, s.renderConnection(req, c))
		case http.MethodPatch, http.MethodPut:
			if !req.authorize(Admin) {
				return
			}
			params, ok := req.wrapped("company_cwr_connection")
			if !ok {
				return
			}
			updated := copyRecord(c)
			for k, v := range params {
				updated[k] = v
			}
			if problems := s.validateConnection(updated); len(problems) > 0 {
				invalid(req.w, problems...)
				return
			}
			company.Connections[index] = updated
			writeJSON(req.w, http.StatusOK, s.renderConnection(req, updated))
		case http.MethodDelete:
			if !req.authorize(Admin) {
				return
			}
			for _, f := range company.CwrFiles {
				if mustInt(f["company_cwr_connection_id"]) == mustInt(c["id"]) {
					message(req.w, http.StatusConflict, "Cannot remove a connection that still has files or acknowledgments")
					return
				}
			}
			company.Connections = append(company.Connections[:index], company.Connections[index+1:]...)
			req.w.WriteHeader(http.StatusNoContent)
		default:
			routingError(req.w)
		}
		return
	}
	switch rest[0] {
	case "download_acks":
		if req.r.Method != http.MethodPost {
			routingError(req.w)
			return
		}
		if !req.authorize(Editor) {
			return
		}
		writeJSON(req.w, http.StatusAccepted, record{"message": "Acknowledgment download queued"})
	case "bulk_tickets":
		s.bulkTickets(req, rest[1:], c)
	case "cwr_files":
		s.cwrFiles(req, rest[1:], c)
	case "cwr_ack_files":
		s.cwrAcks(req, rest[1:], c)
	default:
		routingError(req.w)
	}
}

func (s *Server) validateConnection(c record) []string {
	var problems []string
	for key, label := range map[string]string{"server_username": "Server username", "server_password": "Server password", "inbound_directory": "Inbound directory", "cwr_sender_name": "Cwr sender name", "cwr_sender_ipi_number": "Cwr sender ipi number"} {
		if blank(c[key]) {
			problems = append(problems, label+" can't be blank")
		}
	}
	if blank(c["cwr_destination_id"]) {
		problems = append(problems, "Cwr destination must exist")
	}
	sort.Strings(problems)
	return problems
}

// unsentWorks are the works never in a file on the connection and not
// queued on it.
func (s *Server) unsentWorks(company *Company, connectionID int64) []record {
	sent := map[string]bool{}
	for _, f := range company.CwrFiles {
		if mustInt(f["company_cwr_connection_id"]) == connectionID {
			for _, id := range listOfAny(f["work_ids"]) {
				sent[str(id)] = true
			}
		}
	}
	for _, ticket := range company.CwrTickets {
		if mustInt(ticket["company_cwr_connection_id"]) == connectionID && ticket["open"] == true {
			sent[str(ticket["work_external_id"])] = true
		}
	}
	var works []record
	for _, w := range company.Works {
		if !sent[str(w["external_id"])] {
			works = append(works, w)
		}
	}
	return works
}

func (s *Server) bulkTickets(req *request, rest []string, c record) {
	if !req.authorize(Editor) {
		return
	}
	company := req.company
	proID := mustInt(c["pro_id"])
	switch {
	case len(rest) == 1 && rest[0] == "candidates" && req.r.Method == http.MethodGet:
		works := []record{}
		for _, w := range s.unsentWorks(company, mustInt(c["id"])) {
			works = append(works, record{"id": w["external_id"], "title": w["title"], "has_pro_code": s.workHasCode(w, proID)})
		}
		sortBy(works, "title")
		writeJSON(req.w, http.StatusOK, record{"works": works})
	case len(rest) == 0 && req.r.Method == http.MethodPost:
		reason := strings.TrimSpace(str(req.params["reason"]))
		if reason == "" {
			message(req.w, http.StatusUnprocessableEntity, "A reason is required")
			return
		}
		wanted := map[string]bool{}
		for _, id := range listOfAny(req.params["work_ids"]) {
			wanted[str(id)] = true
		}
		created := 0
		for _, w := range s.unsentWorks(company, mustInt(c["id"])) {
			if !wanted[str(w["external_id"])] {
				continue
			}
			company.CwrTickets = append(company.CwrTickets, record{"id": s.id(), "work_external_id": w["external_id"], "company_cwr_connection_id": c["id"], "reason": reason, "open": true})
			w["include_in_cwr"] = true
			created++
		}
		if created == 0 {
			message(req.w, http.StatusUnprocessableEntity, "Name at least one unsent work (work_ids, by external id)")
			return
		}
		writeJSON(req.w, http.StatusCreated, record{"created": created})
	default:
		routingError(req.w)
	}
}

func (s *Server) cwrTickets(req *request, w record, rest []string) {
	if !req.authorize(Editor) {
		return
	}
	company := req.company
	switch {
	case len(rest) == 0 && req.r.Method == http.MethodPost:
		params, ok := req.wrapped("cwr_ticket")
		if !ok {
			return
		}
		c, _ := s.connection(company, str(params["company_cwr_connection_id"]))
		if c == nil {
			notFound(req.w)
			return
		}
		for _, ticket := range company.CwrTickets {
			if ticket["open"] == true && ticket["work_external_id"] == w["external_id"] && mustInt(ticket["company_cwr_connection_id"]) == mustInt(c["id"]) {
				message(req.w, http.StatusConflict, "This work is already queued on that connection")
				return
			}
		}
		ticket := record{"id": s.id(), "work_external_id": w["external_id"], "company_cwr_connection_id": c["id"], "reason": params["reason"], "open": true}
		company.CwrTickets = append(company.CwrTickets, ticket)
		writeJSON(req.w, http.StatusCreated, record{"id": ticket["id"], "work_id": w["external_id"], "company_cwr_connection_id": c["id"], "reason": ticket["reason"]})
	case len(rest) == 1 && req.r.Method == http.MethodDelete:
		for i, ticket := range company.CwrTickets {
			if str(ticket["id"]) == rest[0] && ticket["work_external_id"] == w["external_id"] && ticket["open"] == true {
				company.CwrTickets = append(company.CwrTickets[:i], company.CwrTickets[i+1:]...)
				req.w.WriteHeader(http.StatusNoContent)
				return
			}
		}
		notFound(req.w)
	default:
		routingError(req.w)
	}
}

func (s *Server) renderCwrFile(f record) record {
	return record{"id": f["id"], "cwr_file_name": f["cwr_file_name"], "works_count": len(listOfAny(f["work_ids"])), "new_count": f["new_count"], "accepted_count": f["accepted_count"],
		"rejected_count": f["rejected_count"], "error_count": f["error_count"], "fully_acked": f["fully_acked"], "sent_at": f["sent_at"], "created_at": f["created_at"]}
}

func (s *Server) cwrFileWithWorks(company *Company, f record) record {
	out := s.renderCwrFile(f)
	works := []record{}
	for _, id := range listOfAny(f["work_ids"]) {
		if i := findExternal(company.Works, str(id)); i >= 0 {
			works = append(works, record{"id": id, "title": company.Works[i]["title"]})
		}
	}
	out["works"] = works
	return out
}

func (s *Server) cwrFiles(req *request, rest []string, c record) {
	company := req.company
	connectionID := mustInt(c["id"])
	var files []record
	for _, f := range company.CwrFiles {
		if mustInt(f["company_cwr_connection_id"]) == connectionID {
			files = append(files, f)
		}
	}
	switch {
	case len(rest) == 0 && req.r.Method == http.MethodGet:
		if !req.authorize(Viewer) {
			return
		}
		var out []record
		for i := len(files) - 1; i >= 0; i-- {
			out = append(out, s.renderCwrFile(files[i]))
		}
		page, pagination := paginate(req.r, out, 0)
		writeJSON(req.w, http.StatusOK, record{"cwr_files": page, "pagination": pagination})
	case len(rest) == 1 && rest[0] == "candidates":
		if !req.authorize(Viewer) {
			return
		}
		candidates := []record{}
		for _, ticket := range company.CwrTickets {
			if ticket["open"] != true || mustInt(ticket["company_cwr_connection_id"]) != connectionID {
				continue
			}
			if i := findExternal(company.Works, str(ticket["work_external_id"])); i >= 0 && company.Works[i]["include_in_cwr"] == true {
				candidates = append(candidates, record{"id": ticket["work_external_id"], "title": company.Works[i]["title"], "new": true, "reason": ticket["reason"]})
			}
		}
		writeJSON(req.w, http.StatusOK, record{"candidates": candidates})
	case len(rest) == 0 && req.r.Method == http.MethodPost:
		if !req.authorize(Editor) {
			return
		}
		var ids []any
		for _, id := range listOfAny(req.params["work_ids"]) {
			if findExternal(company.Works, str(id)) >= 0 {
				ids = append(ids, str(id))
			}
		}
		if len(ids) == 0 {
			message(req.w, http.StatusUnprocessableEntity, "Name at least one work to include (work_ids, by external id)")
			return
		}
		f := record{"id": s.id(), "company_cwr_connection_id": c["id"], "cwr_file_name": fmt.Sprintf("CW26%04d%s_000.V21", len(company.CwrFiles)+1, c["cwr_sender_filename_code"]), "work_ids": ids, "new_count": len(ids), "accepted_count": 0, "rejected_count": 0, "error_count": 0, "fully_acked": false, "sent_at": nil, "created_at": time.Now().Format(time.RFC3339), "contents": "HDRPB" + str(c["cwr_sender_name"]) + "\n"}
		for _, ticket := range company.CwrTickets {
			for _, id := range ids {
				if ticket["work_external_id"] == id && mustInt(ticket["company_cwr_connection_id"]) == connectionID {
					ticket["open"] = false
				}
			}
		}
		company.CwrFiles = append(company.CwrFiles, f)
		writeJSON(req.w, http.StatusCreated, s.cwrFileWithWorks(company, f))
	case len(rest) >= 1:
		var f record
		index := -1
		for i, candidate := range company.CwrFiles {
			if str(candidate["id"]) == rest[0] && mustInt(candidate["company_cwr_connection_id"]) == connectionID {
				f, index = candidate, i
			}
		}
		if f == nil {
			if req.authorize(Viewer) {
				notFound(req.w)
			}
			return
		}
		switch {
		case len(rest) == 1 && req.r.Method == http.MethodGet:
			if !req.authorize(Viewer) {
				return
			}
			writeJSON(req.w, http.StatusOK, s.cwrFileWithWorks(company, f))
		case len(rest) == 2 && rest[1] == "download":
			if !req.authorize(Viewer) {
				return
			}
			sendFile(req.w, str(f["cwr_file_name"]), "text/plain", []byte(str(f["contents"])))
		case len(rest) == 2 && rest[1] == "send_to_pro" && req.r.Method == http.MethodPost:
			if !req.authorize(Editor) {
				return
			}
			if f["sent_at"] != nil {
				message(req.w, http.StatusConflict, "This file was already sent")
				return
			}
			if s.SendFails {
				message(req.w, http.StatusBadGateway, "Send failed: connection refused")
				return
			}
			f["sent_at"] = time.Now().Format(time.RFC3339)
			writeJSON(req.w, http.StatusOK, s.cwrFileWithWorks(company, f))
		case len(rest) == 1 && req.r.Method == http.MethodDelete:
			if !req.authorize(Editor) {
				return
			}
			if f["sent_at"] != nil {
				invalid(req.w, "Cannot delete a file that was sent")
				return
			}
			company.CwrFiles = append(company.CwrFiles[:index], company.CwrFiles[index+1:]...)
			req.w.WriteHeader(http.StatusNoContent)
		default:
			routingError(req.w)
		}
	default:
		routingError(req.w)
	}
}

func (s *Server) cwrAcks(req *request, rest []string, c record) {
	if !req.authorize(Viewer) || req.r.Method != http.MethodGet {
		if req.r.Method != http.MethodGet {
			routingError(req.w)
		}
		return
	}
	company := req.company
	var acks []record
	for _, ack := range company.CwrAcks {
		if mustInt(ack["company_cwr_connection_id"]) == mustInt(c["id"]) {
			acks = append(acks, ack)
		}
	}
	render := func(ack record) record {
		return record{"id": ack["id"], "ack_file_name": ack["ack_file_name"], "pro": ack["pro"], "record_count": ack["record_count"], "accepted_count": ack["accepted_count"], "rejected_count": ack["rejected_count"], "error_count": ack["error_count"], "iswc_count": ack["iswc_count"], "received_at": ack["received_at"]}
	}
	if len(rest) == 0 {
		var out []record
		for _, ack := range acks {
			out = append(out, render(ack))
		}
		page, pagination := paginate(req.r, out, 0)
		writeJSON(req.w, http.StatusOK, record{"cwr_ack_files": page, "pagination": pagination})
		return
	}
	for _, ack := range acks {
		if str(ack["id"]) != rest[0] {
			continue
		}
		if len(rest) == 1 {
			out := render(ack)
			out["works"] = ack["works"]
			writeJSON(req.w, http.StatusOK, out)
			return
		}
		if len(rest) == 2 && rest[1] == "download" {
			sendFile(req.w, str(ack["ack_file_name"]), "text/plain", []byte(str(ack["contents"])))
			return
		}
	}
	notFound(req.w)
}
