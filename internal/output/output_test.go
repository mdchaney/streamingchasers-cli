package output

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
)

const track = `{
	"id": 999,
	"title": "Drunken Daisy",
	"lyrics": null,
	"active": true,
	"explicit": false,
	"bpm": 132,
	"ipi": 292898239823,
	"rating": 4.50,
	"style_alikes": ["chieftains", "flatt and scruggs"],
	"mood_ids": [],
	"tracks_composers": [{"composer_id": 186, "share": "100.0"}],
	"search_params": {"nls": "rock", "filter": {"genre": {"value": "Rock"}}}
}`

func TestParseKeepsOrderAndNumbers(t *testing.T) {
	record, err := ParseRecord([]byte(track))
	if err != nil {
		t.Fatal(err)
	}
	want := "id title lyrics active explicit bpm ipi rating style_alikes mood_ids tracks_composers search_params"
	if got := strings.Join(record.Keys(), " "); got != want {
		t.Errorf("keys = %q", got)
	}

	for key, want := range map[string]string{
		"id":               "999",
		"title":            "Drunken Daisy",
		"lyrics":           "",
		"active":           "true",
		"explicit":         "false",
		"ipi":              "292898239823",
		"rating":           "4.50",
		"style_alikes":     "chieftains, flatt and scruggs",
		"mood_ids":         "",
		"tracks_composers": `[{"composer_id":186,"share":"100.0"}]`,
		"search_params":    `{"nls":"rock","filter":{"genre":{"value":"Rock"}}}`,
		"missing":          "",
	} {
		if got := record.String(key); got != want {
			t.Errorf("String(%q) = %q, want %q", key, got, want)
		}
	}

	if _, ok := record.Get("lyrics"); !ok {
		t.Error("a null is still a key the record has")
	}
	if _, ok := record.Get("missing"); ok {
		t.Error("the record does not have a key called missing")
	}
	if record.Value("missing") != nil || record.Value("bpm") != json.Number("132") {
		t.Errorf("Value: %v, %v", record.Value("missing"), record.Value("bpm"))
	}
}

func TestNilRecord(t *testing.T) {
	var record *Record
	if _, ok := record.Get("x"); ok {
		t.Error("a nil record has no keys")
	}
	if record.String("x") != "" {
		t.Error("a nil record has no values")
	}
}

func TestSetAndDelete(t *testing.T) {
	record := NewRecord()
	record.Set("a", "1")
	record.Set("b", "2")
	record.Set("c", "3")
	record.Set("a", "changed")
	if got := strings.Join(record.Keys(), ""); got != "abc" {
		t.Errorf("setting a key again moved it: %q", got)
	}
	if record.String("a") != "changed" {
		t.Error("the value was not replaced")
	}

	record.Delete("b")
	record.Delete("missing")
	if got := strings.Join(record.Keys(), ""); got != "ac" {
		t.Errorf("keys after deleting = %q", got)
	}
	if _, ok := record.Get("b"); ok {
		t.Error("the value was not deleted")
	}
}

func TestMarshalRoundTrip(t *testing.T) {
	record, err := ParseRecord([]byte(track))
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"id":999,"title":"Drunken Daisy","lyrics":null,"active":true,"explicit":false,"bpm":132,"ipi":292898239823,"rating":4.50,` +
		`"style_alikes":["chieftains","flatt and scruggs"],"mood_ids":[],"tracks_composers":[{"composer_id":186,"share":"100.0"}],` +
		`"search_params":{"nls":"rock","filter":{"genre":{"value":"Rock"}}}}`
	if string(data) != want {
		t.Errorf("marshalled:\n got %s\nwant %s", data, want)
	}
}

func TestMarshalKeepsTextReadable(t *testing.T) {
	record := NewRecord()
	record.Set("title", `Tom & Jerry <3 "quotes"`)
	data, err := record.MarshalJSON()
	if err != nil {
		t.Fatal(err)
	}
	if want := `{"title":"Tom & Jerry <3 \"quotes\""}`; string(data) != want {
		t.Errorf("got %s, want %s", data, want)
	}
}

func TestParseErrors(t *testing.T) {
	for name, input := range map[string]string{
		"empty":          ``,
		"not JSON":       `{nope`,
		"unclosed":       `{"a": [1, 2`,
		"trailing data":  `{"a": 1} {"b": 2}`,
		"trailing comma": `{"a": 1,}`,
	} {
		if _, err := Parse([]byte(input)); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
	if _, err := ParseRecord([]byte(`[1, 2]`)); err == nil {
		t.Error("a list is not a record")
	}
	if _, err := ParseRecords([]json.RawMessage{json.RawMessage(`{"a": 1}`), json.RawMessage(`7`)}); err == nil {
		t.Error("a number is not a record")
	}
}

func TestParseStream(t *testing.T) {
	dec := json.NewDecoder(strings.NewReader("{\"a\": 1}\n{\"b\": [true]} 7"))
	dec.UseNumber()
	var got []string
	for {
		value, err := ParseStream(dec)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, Format(value))
	}
	if want := `{"a":1}|{"b":[true]}|7`; strings.Join(got, "|") != want {
		t.Errorf("got %q", strings.Join(got, "|"))
	}
}

func TestParseStreamCutShort(t *testing.T) {
	// A file that ends in the middle of a record is not a shorter file.
	for _, input := range []string{
		"{\"a\": 1}\n{\"b\": ",
		"{\"a\": 1}\n{\"b\"",
		"{\"a\": 1}\n{",
		"{\"a\": 1}\n{\"b\": [1, ",
		"{\"a\": 1}\n{\"b\": {\"c\": 1}",
		"{\"a\": 1}\n[",
	} {
		dec := json.NewDecoder(strings.NewReader(input))
		dec.UseNumber()
		if _, err := ParseStream(dec); err != nil {
			t.Fatalf("%q: the first value: %v", input, err)
		}
		_, err := ParseStream(dec)
		if err == nil || errors.Is(err, io.EOF) {
			t.Errorf("%q: got %v, want an error that is not the end of the stream", input, err)
		}
	}
}

func TestFormat(t *testing.T) {
	tests := []struct {
		value any
		want  string
	}{
		{nil, ""},
		{"text", "text"},
		{true, "true"},
		{false, "false"},
		{json.Number("1e3"), "1e3"},
		{[]any{}, ""},
		{[]any{json.Number("17"), json.Number("169")}, "17, 169"},
		{[]any{"a", nil, true}, "a, , true"},
		{[]any{[]any{"bpm", "desc"}, []any{"title", "asc"}}, `[["bpm","desc"],["title","asc"]]`},
		{42, "42"},
	}
	for _, tt := range tests {
		if got := Format(tt.value); got != tt.want {
			t.Errorf("Format(%v) = %q, want %q", tt.value, got, tt.want)
		}
	}
}

func records(t *testing.T, items ...string) []*Record {
	t.Helper()
	var list []*Record
	for _, item := range items {
		record, err := ParseRecord([]byte(item))
		if err != nil {
			t.Fatal(err)
		}
		list = append(list, record)
	}
	return list
}

func TestWriteTable(t *testing.T) {
	columns := []Column{
		{Header: "ID", Key: "id"},
		{Header: "NAME", Key: "name", Max: 12},
		{Header: "PRO", Key: "pro_id"},
		{Header: "SHOUT", Value: func(r *Record) string { return strings.ToUpper(r.String("name")) }, Max: 5},
	}
	list := records(t,
		`{"id": 1, "name": "Ilze", "pro_id": 10}`,
		`{"id": 292898239823, "name": "Antonín Dvořák of Nelahozeves", "pro_id": null}`,
		`{"id": 3, "name": "two\nlines\tand a tab", "pro_id": 21}`,
	)

	var out bytes.Buffer
	if err := WriteTable(&out, columns, list); err != nil {
		t.Fatal(err)
	}
	want := strings.Join([]string{
		"ID            NAME          PRO  SHOUT",
		"1             Ilze          10   ILZE",
		"292898239823  Antonín Dvo…       ANTO…",
		"3             two lines a…  21   TWO…",
		"",
	}, "\n")
	if out.String() != want {
		t.Errorf("table:\n%s\nwant:\n%s", out.String(), want)
	}
}

func TestWriteTableHasNoTrailingSpaces(t *testing.T) {
	columns := []Column{{Header: "ID", Key: "id"}, {Header: "ISRC", Key: "isrc"}}
	var out bytes.Buffer
	if err := WriteTable(&out, columns, records(t, `{"id": 1, "isrc": null}`, `{"id": 2, "isrc": "USABC2400001"}`)); err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(out.String(), "\n") {
		if strings.HasSuffix(line, " ") {
			t.Errorf("line %q ends in a space", line)
		}
	}
}

func TestWriteFields(t *testing.T) {
	var out bytes.Buffer
	list := records(t, `{"id": 999, "title": "Drunken Daisy", "lyrics": "one\ntwo", "bpm": null, "style_alikes": ["a", "b"], "tracks_composers": [{"composer_id": 1}]}`)
	if err := WriteFields(&out, list[0]); err != nil {
		t.Fatal(err)
	}
	want := strings.Join([]string{
		"id:                999",
		"title:             Drunken Daisy",
		"lyrics:            one two",
		"bpm:",
		"style_alikes:      a, b",
		`tracks_composers:  [{"composer_id":1}]`,
		"",
	}, "\n")
	if out.String() != want {
		t.Errorf("fields:\n%s\nwant:\n%s", out.String(), want)
	}
}

func TestTruncate(t *testing.T) {
	tests := []struct {
		in   string
		max  int
		want string
	}{
		{"short", 10, "short"},
		{"exactly 10", 10, "exactly 10"},
		{"more than ten", 10, "more than…"},
		{"cut at a space here", 9, "cut at a…"},
		{"no limit at all", 0, "no limit at all"},
		{"Dvořák Dvořák", 7, "Dvořák…"},
	}
	for _, tt := range tests {
		if got := Truncate(tt.in, tt.max); got != tt.want {
			t.Errorf("Truncate(%q, %d) = %q, want %q", tt.in, tt.max, got, tt.want)
		}
	}
}

func TestWriteCSV(t *testing.T) {
	list := records(t,
		`{"id": 1, "title": "Plain", "style_alikes": ["a", "b"]}`,
		`{"id": 2, "title": "With, a comma and a \"quote\"", "lyrics": "one\ntwo", "style_alikes": []}`,
	)
	var out bytes.Buffer
	if err := WriteCSV(&out, AllColumns(list), list); err != nil {
		t.Fatal(err)
	}
	want := "id,title,style_alikes,lyrics\n" +
		"1,Plain,\"a, b\",\n" +
		"2,\"With, a comma and a \"\"quote\"\"\",,\"one\ntwo\"\n"
	if out.String() != want {
		t.Errorf("csv:\n%s\nwant:\n%s", out.String(), want)
	}
}

func TestCSVHeadersAreLowerCase(t *testing.T) {
	var out bytes.Buffer
	columns := []Column{{Header: "EXTERNAL IDS", Key: "has_external_id"}, {Header: "#", Key: "rank"}}
	if err := WriteCSV(&out, columns, nil); err != nil {
		t.Fatal(err)
	}
	if out.String() != "external_ids,#\n" {
		t.Errorf("got %q", out.String())
	}
}

func TestWriteJSON(t *testing.T) {
	var out bytes.Buffer
	if err := WriteJSON(&out, []byte(`{"id":292898239823,"title":"Tom & Jerry","list":[1,2]}`)); err != nil {
		t.Fatal(err)
	}
	want := "{\n  \"id\": 292898239823,\n  \"title\": \"Tom & Jerry\",\n  \"list\": [\n    1,\n    2\n  ]\n}\n"
	if out.String() != want {
		t.Errorf("got %q", out.String())
	}
	if err := WriteJSON(&out, []byte(`<html>`)); err == nil {
		t.Error("expected an error for what is not JSON")
	}
}

func TestWriteJSONList(t *testing.T) {
	var out bytes.Buffer
	if err := WriteJSONList(&out, nil); err != nil {
		t.Fatal(err)
	}
	if out.String() != "[]\n" {
		t.Errorf("an empty list = %q", out.String())
	}

	out.Reset()
	items := []json.RawMessage{json.RawMessage(`{"id": 1}`), json.RawMessage(`{"id": 2}`)}
	if err := WriteJSONList(&out, items); err != nil {
		t.Fatal(err)
	}
	var back []map[string]int
	if err := json.Unmarshal(out.Bytes(), &back); err != nil {
		t.Fatalf("the output is not JSON: %v\n%s", err, out.String())
	}
	if len(back) != 2 || back[1]["id"] != 2 {
		t.Errorf("got %v", back)
	}
}

func TestWriteJSONLines(t *testing.T) {
	var out bytes.Buffer
	items := []json.RawMessage{json.RawMessage("{\n  \"id\": 1,\n  \"lyrics\": \"one\\ntwo\"\n}"), json.RawMessage(`{"id": 2}`)}
	if err := WriteJSONLines(&out, items); err != nil {
		t.Fatal(err)
	}
	want := "{\"id\":1,\"lyrics\":\"one\\ntwo\"}\n{\"id\":2}\n"
	if out.String() != want {
		t.Errorf("got %q, want %q", out.String(), want)
	}
	if err := WriteJSONLines(&out, []json.RawMessage{json.RawMessage(`{`)}); err == nil {
		t.Error("expected an error for what is not JSON")
	}
}

func TestValidFormat(t *testing.T) {
	for _, format := range []string{"table", "json", "jsonl", "csv"} {
		if !ValidFormat(format) {
			t.Errorf("%s should be a format", format)
		}
	}
	for _, format := range []string{"", "yaml", "JSON"} {
		if ValidFormat(format) {
			t.Errorf("%q should not be a format", format)
		}
	}
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("disk full") }

func TestWriteErrorsAreReported(t *testing.T) {
	list := records(t, `{"id": 1}`)
	columns := []Column{{Header: "ID", Key: "id"}}
	if err := WriteTable(failingWriter{}, columns, list); err == nil {
		t.Error("WriteTable swallowed the error")
	}
	if err := WriteFields(failingWriter{}, list[0]); err == nil {
		t.Error("WriteFields swallowed the error")
	}
	if err := WriteCSV(failingWriter{}, columns, list); err == nil {
		t.Error("WriteCSV swallowed the error")
	}
	if err := WriteJSON(failingWriter{}, []byte(`{}`)); err == nil {
		t.Error("WriteJSON swallowed the error")
	}
	if err := WriteJSONLines(failingWriter{}, []json.RawMessage{json.RawMessage(`{}`)}); err == nil {
		t.Error("WriteJSONLines swallowed the error")
	}
}
