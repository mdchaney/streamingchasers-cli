package output

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"unicode/utf8"
)

// Format names accepted by --output.
const (
	Table = "table"
	JSON  = "json"
	JSONL = "jsonl"
	CSV   = "csv"
)

// Formats lists the accepted formats.
var Formats = []string{Table, JSON, JSONL, CSV}

// ValidFormat reports whether name is an accepted format.
func ValidFormat(name string) bool {
	for _, format := range Formats {
		if format == name {
			return true
		}
	}
	return false
}

// Column is a column of a table.
type Column struct {
	Header string
	// Key is the record key the column shows.
	Key string
	// Value, if set, computes the cell instead of Key.
	Value func(*Record) string
	// Max is the widest a cell is drawn in a table; longer values are cut
	// short.  Zero means no limit.
	Max int
}

func (c Column) cell(r *Record) string {
	if c.Value != nil {
		return c.Value(r)
	}
	return r.String(c.Key)
}

// WriteTable draws records as an aligned table.
func WriteTable(w io.Writer, columns []Column, records []*Record) error {
	rows := make([][]string, 0, len(records)+1)
	header := make([]string, len(columns))
	for i, column := range columns {
		header[i] = column.Header
	}
	rows = append(rows, header)
	for _, record := range records {
		row := make([]string, len(columns))
		for i, column := range columns {
			row[i] = Truncate(oneLine(column.cell(record)), column.Max)
		}
		rows = append(rows, row)
	}
	return writeAligned(w, rows)
}

// WriteFields draws one record as a list of its fields, in the record's
// own order.  Nested lists and objects are shown as JSON.
func WriteFields(w io.Writer, record *Record) error {
	rows := make([][]string, 0, len(record.Keys()))
	for _, key := range record.Keys() {
		rows = append(rows, []string{key + ":", oneLine(record.String(key))})
	}
	return writeAligned(w, rows)
}

// writeAligned pads every column but the last to its widest cell.
// Widths count characters rather than bytes so that accented names line up.
func writeAligned(w io.Writer, rows [][]string) error {
	var widths []int
	for _, row := range rows {
		for i, cell := range row {
			if i >= len(widths) {
				widths = append(widths, 0)
			}
			if n := utf8.RuneCountInString(cell); n > widths[i] {
				widths[i] = n
			}
		}
	}

	var b bytes.Buffer
	for _, row := range rows {
		for i, cell := range row {
			if i == len(row)-1 {
				b.WriteString(cell)
				break
			}
			b.WriteString(cell)
			b.WriteString(strings.Repeat(" ", widths[i]-utf8.RuneCountInString(cell)+2))
		}
		// Trailing padding from empty last cells would be invisible noise.
		line := strings.TrimRight(b.String(), " ")
		b.Reset()
		if _, err := fmt.Fprintln(w, line); err != nil {
			return err
		}
	}
	return nil
}

// oneLine keeps a multi-line value, such as lyrics, on one table row.
func oneLine(s string) string {
	if !strings.ContainsAny(s, "\r\n\t") {
		return s
	}
	return strings.Join(strings.Fields(s), " ")
}

// Truncate cuts s to at most max characters, marking the cut with an
// ellipsis.  A max of zero leaves s alone.
func Truncate(s string, max int) string {
	if max <= 0 || utf8.RuneCountInString(s) <= max {
		return s
	}
	runes := []rune(s)
	return strings.TrimRight(string(runes[:max-1]), " ") + "…"
}

// WriteCSV writes records as CSV with a header row.  Cells are not cut
// short: CSV is for other programs to read.
func WriteCSV(w io.Writer, columns []Column, records []*Record) error {
	out := csv.NewWriter(w)
	header := make([]string, len(columns))
	for i, column := range columns {
		header[i] = strings.ToLower(strings.ReplaceAll(column.Header, " ", "_"))
	}
	if err := out.Write(header); err != nil {
		return err
	}
	for _, record := range records {
		row := make([]string, len(columns))
		for i, column := range columns {
			row[i] = column.cell(record)
		}
		if err := out.Write(row); err != nil {
			return err
		}
	}
	out.Flush()
	return out.Error()
}

// AllColumns returns a column for every key that appears in records, in
// the order first seen.  It is what CSV uses when the records' fields
// are not known in advance, as with a library's own metadata types.
func AllColumns(records []*Record) []Column {
	seen := map[string]bool{}
	var columns []Column
	for _, record := range records {
		for _, key := range record.Keys() {
			if !seen[key] {
				seen[key] = true
				columns = append(columns, Column{Header: key, Key: key})
			}
		}
	}
	return columns
}

// WriteJSON writes raw JSON indented.
func WriteJSON(w io.Writer, raw []byte) error {
	var b bytes.Buffer
	if err := json.Indent(&b, raw, "", "  "); err != nil {
		return fmt.Errorf("the server sent a response that is not JSON: %w", err)
	}
	b.WriteByte('\n')
	_, err := w.Write(b.Bytes())
	return err
}

// WriteJSONList writes items as one indented JSON array.
func WriteJSONList(w io.Writer, items []json.RawMessage) error {
	if len(items) == 0 {
		_, err := fmt.Fprintln(w, "[]")
		return err
	}
	var b bytes.Buffer
	b.WriteByte('[')
	for i, item := range items {
		if i > 0 {
			b.WriteByte(',')
		}
		b.Write(item)
	}
	b.WriteByte(']')
	return WriteJSON(w, b.Bytes())
}

// WriteJSONLines writes each item on a line of its own.
func WriteJSONLines(w io.Writer, items []json.RawMessage) error {
	for _, item := range items {
		var b bytes.Buffer
		if err := json.Compact(&b, item); err != nil {
			return fmt.Errorf("the server sent a record that is not JSON: %w", err)
		}
		b.WriteByte('\n')
		if _, err := w.Write(b.Bytes()); err != nil {
			return err
		}
	}
	return nil
}
