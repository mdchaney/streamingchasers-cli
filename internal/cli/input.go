package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"unicode"

	"github.com/mdchaney/streamingchasers-api/internal/output"
)

// readOnlyKeys are the fields of a record that the server computes.
// They are dropped from what is sent, so that a record that was read
// from the API can be sent back to it.
var readOnlyKeys = []string{
	"created_at", "updated_at", "url", "csv_file_url", "works_count", "full_name", "pro_name",
	"territories", "alt_names", "language", "language_code", "alt_titles", "registration_codes",
	"publishers", "writers", "pro", "work", "production", "sales_upload", "work_title", "work_external_id",
}

// readSource reads a flag's value: the text itself, the file named
// after an @, or standard input for "-" or "@-".
func (a *App) readSource(value string) ([]byte, error) {
	switch {
	case value == "-" || value == "@-":
		return io.ReadAll(a.In)
	case strings.HasPrefix(value, "@"):
		data, err := os.ReadFile(value[1:])
		if err != nil {
			return nil, err
		}
		return data, nil
	}
	return []byte(value), nil
}

// readFile reads a file argument, or standard input for "-".
func (a *App) readFile(name string) ([]byte, error) {
	if name == "-" {
		return io.ReadAll(a.In)
	}
	return os.ReadFile(name)
}

// parseObject reads a JSON object given to a flag such as --data.
func (a *App) parseObject(flag, value string) (*output.Record, error) {
	data, err := a.readSource(value)
	if err != nil {
		return nil, usagef("--%s: %s", flag, err)
	}
	record, err := output.ParseRecord(data)
	if err != nil {
		return nil, usagef("--%s must be a JSON object: %s", flag, err)
	}
	return record, nil
}

// parseJSON reads any JSON value given to a flag such as --filter.
func (a *App) parseJSON(flag, value string) (any, error) {
	data, err := a.readSource(value)
	if err != nil {
		return nil, usagef("--%s: %s", flag, err)
	}
	parsed, err := output.Parse(data)
	if err != nil {
		return nil, usagef("--%s must be JSON: %s", flag, err)
	}
	return parsed, nil
}

// unwrap returns the record inside a wrapper such as {"track": {...}},
// which is how the API documentation writes request bodies, or the
// record itself when it is not wrapped.
func unwrap(record *output.Record, wrapper string) *output.Record {
	if keys := record.Keys(); len(keys) == 1 && keys[0] == wrapper {
		if value, _ := record.Get(wrapper); value != nil {
			if nested, ok := value.(*output.Record); ok {
				return nested
			}
		}
	}
	return record
}

// parseAssignment reads a --set argument, key=value.  The value is JSON
// when it parses as JSON (so 120, true, null and ["a","b"] mean what they
// say) and a string otherwise.
func parseAssignment(arg string) (string, any, error) {
	key, value, ok := strings.Cut(arg, "=")
	key = strings.TrimSpace(key)
	if !ok || key == "" {
		return "", nil, usagef("--set takes key=value, not %q", arg)
	}
	if parsed, err := output.Parse([]byte(value)); err == nil {
		return key, parsed, nil
	}
	return key, value, nil
}

// readRecords reads the records of an import: a JSON array of objects,
// or objects one after another (JSON Lines).
func readRecords(data []byte) ([]*output.Record, error) {
	data = bytes.TrimPrefix(data, []byte("\xef\xbb\xbf"))
	trimmed := bytes.TrimLeftFunc(data, unicode.IsSpace)
	if len(trimmed) == 0 {
		return nil, nil
	}

	if trimmed[0] == '[' {
		parsed, err := output.Parse(trimmed)
		if err != nil {
			return nil, fmt.Errorf("the input is not valid JSON: %w", err)
		}
		list, _ := parsed.([]any)
		records := make([]*output.Record, 0, len(list))
		for i, item := range list {
			record, ok := item.(*output.Record)
			if !ok {
				return nil, fmt.Errorf("record %d is not a JSON object", i+1)
			}
			records = append(records, record)
		}
		return records, nil
	}

	dec := json.NewDecoder(bytes.NewReader(trimmed))
	dec.UseNumber()
	var records []*output.Record
	for {
		parsed, err := output.ParseStream(dec)
		if errors.Is(err, io.EOF) {
			return records, nil
		}
		if err != nil {
			return nil, fmt.Errorf("record %d is not valid JSON: %w", len(records)+1, err)
		}
		record, ok := parsed.(*output.Record)
		if !ok {
			return nil, fmt.Errorf("record %d is not a JSON object", len(records)+1)
		}
		records = append(records, record)
	}
}
