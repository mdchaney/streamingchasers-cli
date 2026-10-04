package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
)

// Error is a response outside the 2xx range.
type Error struct {
	Method     string
	Path       string
	StatusCode int
	// Message is the server's message: "Not found", "Forbidden",
	// "Validation failed", or what is wrong with a claims batch.
	Message string
	// Detail is the server's error field, when it sends one alongside
	// the message.
	Detail string
	// Problems lists what was wrong with the data, for a 422: the
	// server's full messages, such as "Title can't be blank".
	Problems []string
	// RetryAfter is the Retry-After header of a 429, in seconds.
	RetryAfter string
	// Body is the response body as sent.
	Body []byte
}

func newError(method, path string, resp *Response) *Error {
	e := &Error{Method: method, Path: path, StatusCode: resp.StatusCode, Body: resp.Body, RetryAfter: resp.Header.Get("Retry-After")}

	var top map[string]json.RawMessage
	if json.Unmarshal(resp.Body, &top) != nil {
		return e
	}
	for key, raw := range top {
		switch key {
		case "message":
			_ = json.Unmarshal(raw, &e.Message)
		case "error", "error_description":
			var text string
			if json.Unmarshal(raw, &text) == nil && text != "" && (key == "error_description" || e.Detail == "") {
				e.Detail = text
			}
		case "errors":
			e.Problems = append(e.Problems, problems(raw)...)
		}
	}
	// An OAuth-style error with no message reads as its description.
	if e.Message == "" && e.Detail != "" {
		e.Message, e.Detail = e.Detail, ""
	}
	return e
}

// problems reads the errors of a 422, which come as a list of messages
// or, from some endpoints, as a map from field to its messages.
func problems(raw json.RawMessage) []string {
	var list []string
	if json.Unmarshal(raw, &list) == nil {
		return list
	}
	var fields map[string][]string
	if json.Unmarshal(raw, &fields) != nil {
		return nil
	}
	names := make([]string, 0, len(fields))
	for name := range fields {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		for _, message := range fields[name] {
			if name == "base" {
				list = append(list, message)
			} else {
				list = append(list, name+" "+message)
			}
		}
	}
	return list
}

func (e *Error) Error() string {
	var b strings.Builder
	switch {
	case e.Message != "":
		b.WriteString(e.Message)
	case len(e.Problems) > 0:
		b.WriteString("the server rejected the request")
	case e.StatusCode == http.StatusNotFound && !json.Valid(e.Body):
		// A path the server has no route for is answered with a page
		// of HTML.
		b.WriteString("the server has no such endpoint")
	default:
		fmt.Fprintf(&b, "the server answered %d %s", e.StatusCode, http.StatusText(e.StatusCode))
	}
	fmt.Fprintf(&b, " (%s %s: %d)", e.Method, e.Path, e.StatusCode)
	if e.Detail != "" {
		b.WriteString("\n  " + e.Detail)
	}
	for _, problem := range e.Problems {
		b.WriteString("\n  " + problem)
	}
	return b.String()
}

// StatusOf returns the HTTP status of err if it is an *Error, else 0.
func StatusOf(err error) int {
	var apiErr *Error
	if errors.As(err, &apiErr) {
		return apiErr.StatusCode
	}
	return 0
}

// IsNotFound reports whether err is a 404.
func IsNotFound(err error) bool { return StatusOf(err) == http.StatusNotFound }

// IsUnauthorized reports whether err is a 401.
func IsUnauthorized(err error) bool { return StatusOf(err) == http.StatusUnauthorized }

// IsConflict reports whether err is a 409.
func IsConflict(err error) bool { return StatusOf(err) == http.StatusConflict }
