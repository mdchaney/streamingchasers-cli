// Package output renders API records as tables, CSV and JSON.
package output

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

// Record is a JSON object that remembers the order of its keys, so that
// a record is shown the way the server laid it out.  Values are nil,
// bool, json.Number, string, []any or *Record.
type Record struct {
	keys   []string
	values map[string]any
}

// NewRecord returns an empty record.
func NewRecord() *Record {
	return &Record{values: map[string]any{}}
}

// Keys returns the keys in order.
func (r *Record) Keys() []string { return r.keys }

// Get returns the value for key and whether the record has it.
func (r *Record) Get(key string) (any, bool) {
	if r == nil {
		return nil, false
	}
	v, ok := r.values[key]
	return v, ok
}

// Value returns the value for key, or nil if the record does not have it.
func (r *Record) Value(key string) any {
	v, _ := r.Get(key)
	return v
}

// Set adds or replaces a value, keeping the position of an existing key.
func (r *Record) Set(key string, value any) {
	if _, ok := r.values[key]; !ok {
		r.keys = append(r.keys, key)
	}
	r.values[key] = value
}

// Delete removes a key.
func (r *Record) Delete(key string) {
	if _, ok := r.values[key]; !ok {
		return
	}
	delete(r.values, key)
	for i, k := range r.keys {
		if k == key {
			r.keys = append(r.keys[:i], r.keys[i+1:]...)
			break
		}
	}
}

// String returns the value for key formatted for display, or "".
func (r *Record) String(key string) string {
	v, _ := r.Get(key)
	return Format(v)
}

// MarshalJSON writes the record with its keys in order.
func (r *Record) MarshalJSON() ([]byte, error) {
	var b bytes.Buffer
	b.WriteByte('{')
	for i, key := range r.keys {
		if i > 0 {
			b.WriteByte(',')
		}
		name, err := marshal(key)
		if err != nil {
			return nil, err
		}
		b.Write(name)
		b.WriteByte(':')
		value, err := marshal(r.values[key])
		if err != nil {
			return nil, err
		}
		b.Write(value)
	}
	b.WriteByte('}')
	return b.Bytes(), nil
}

// marshal encodes v without escaping <, > and &, which the default
// encoder does for the benefit of HTML and which makes lyrics unreadable.
func marshal(v any) ([]byte, error) {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimRight(b.Bytes(), "\n"), nil
}

// ParseRecord decodes a JSON object.
func ParseRecord(data []byte) (*Record, error) {
	v, err := Parse(data)
	if err != nil {
		return nil, err
	}
	record, ok := v.(*Record)
	if !ok {
		return nil, errors.New("expected a JSON object")
	}
	return record, nil
}

// ParseRecords decodes a list of JSON objects.
func ParseRecords(items []json.RawMessage) ([]*Record, error) {
	records := make([]*Record, 0, len(items))
	for _, item := range items {
		record, err := ParseRecord(item)
		if err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	return records, nil
}

// Parse decodes any JSON value, turning objects into Records and keeping
// numbers exactly as written.
func Parse(data []byte) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	v, err := parseValue(dec)
	if err != nil {
		return nil, err
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, errors.New("unexpected data after the JSON value")
	}
	return v, nil
}

// ParseStream decodes the next JSON value from dec, which must have been
// set to UseNumber.  It returns io.EOF at the end of the input.
func ParseStream(dec *json.Decoder) (any, error) {
	return parseValue(dec)
}

func parseValue(dec *json.Decoder) (any, error) {
	token, err := dec.Token()
	if err != nil {
		return nil, err
	}
	delim, ok := token.(json.Delim)
	if !ok {
		return token, nil
	}
	switch delim {
	case '{':
		record := NewRecord()
		for dec.More() {
			keyToken, err := dec.Token()
			if err != nil {
				return nil, unexpected(err)
			}
			key, ok := keyToken.(string)
			if !ok {
				return nil, fmt.Errorf("expected an object key, got %v", keyToken)
			}
			value, err := parseValue(dec)
			if err != nil {
				return nil, unexpected(err)
			}
			record.Set(key, value)
		}
		if _, err := dec.Token(); err != nil {
			return nil, unexpected(err)
		}
		return record, nil
	case '[':
		list := []any{}
		for dec.More() {
			value, err := parseValue(dec)
			if err != nil {
				return nil, unexpected(err)
			}
			list = append(list, value)
		}
		if _, err := dec.Token(); err != nil {
			return nil, unexpected(err)
		}
		return list, nil
	}
	return nil, fmt.Errorf("unexpected %v", delim)
}

// unexpected turns the end of the input into an error when it comes in
// the middle of a value.  The decoder reports it as io.EOF wherever it
// comes, and a reader of a stream takes io.EOF to mean the stream ended
// where it should: a file cut short would pass for a shorter file.
func unexpected(err error) error {
	if errors.Is(err, io.EOF) {
		return io.ErrUnexpectedEOF
	}
	return err
}

// Format renders a value for a table cell: scalars as themselves, null
// as nothing, a list of scalars joined by commas, and anything nested as
// compact JSON.
func Format(v any) string {
	switch value := v.(type) {
	case nil:
		return ""
	case string:
		return value
	case bool:
		if value {
			return "true"
		}
		return "false"
	case json.Number:
		return value.String()
	case []any:
		parts := make([]string, 0, len(value))
		for _, item := range value {
			switch item.(type) {
			case *Record, []any:
				data, err := marshal(value)
				if err != nil {
					return fmt.Sprint(value)
				}
				return string(data)
			}
			parts = append(parts, Format(item))
		}
		return strings.Join(parts, ", ")
	default:
		data, err := marshal(value)
		if err != nil {
			return fmt.Sprint(value)
		}
		return string(data)
	}
}
