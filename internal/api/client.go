// Package api is a client for the Streaming Chasers REST API.
//
// Responses are returned as raw JSON and passed through to the output,
// so that every field the server sends reaches the user whether or not
// this program knows of it.
package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/url"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// maxBody caps how much of a response is read.  A claims sheet for a
// year of placements is a few megabytes; nothing is near this.
const maxBody = 256 << 20

// MaxPerPage is the largest page the server returns.
const MaxPerPage = 200

// Authorizer supplies the bearer token for requests.
type Authorizer interface {
	// Token returns the token to send, refreshing it first if it is
	// about to expire.
	Token(ctx context.Context) (string, error)
	// Refresh is called when the server rejects a token.  It returns a
	// replacement, or false when there is nothing to replace it with.
	Refresh(ctx context.Context, rejected string) (string, bool, error)
}

// StaticToken is an Authorizer for a token that cannot be refreshed.
type StaticToken string

// Token returns the token.
func (t StaticToken) Token(context.Context) (string, error) { return string(t), nil }

// Refresh reports that the token cannot be replaced.
func (t StaticToken) Refresh(context.Context, string) (string, bool, error) { return "", false, nil }

// Client talks to one Streaming Chasers server.
type Client struct {
	// BaseURL is the server, such as https://app.streamingchasers.com.
	BaseURL    string
	HTTPClient *http.Client
	Auth       Authorizer
	UserAgent  string
	// Debug, if set, receives a line for each request and response.
	Debug io.Writer
	// Retries is how many times a request that is safe to repeat is
	// retried after a network error or a 502, 503 or 504.
	Retries int
	// RetryDelay is the pause before the first retry; it doubles each time.
	RetryDelay time.Duration
	// APIVersion is sent as X-Client-API-Version; "" sends nothing.
	APIVersion string
	// Versions, if set, is told what the server says of its version.
	Versions *Versions
}

// Response is a successful response.
type Response struct {
	StatusCode int
	Header     http.Header
	Body       []byte
}

// Replayed reports whether the server answered an upload with the
// record it made of an earlier request with the same Idempotency-Key.
func (r *Response) Replayed() bool {
	return strings.EqualFold(r.Header.Get("Idempotency-Replayed"), "true")
}

// Filename is the file name the server suggested, from
// Content-Disposition, or "".
func (r *Response) Filename() string {
	_, params, err := mime.ParseMediaType(r.Header.Get("Content-Disposition"))
	if err != nil {
		return ""
	}
	// Only the base name: a server cannot be let to choose a directory.
	return filepath.Base(strings.TrimSpace(params["filename"]))
}

// Path builds an API path from segments, escaping each one, so that an
// external ID with a slash in it stays one segment.
func Path(segments ...any) string {
	var b strings.Builder
	b.WriteString("/api/v1")
	for _, segment := range segments {
		b.WriteByte('/')
		b.WriteString(url.PathEscape(fmt.Sprint(segment)))
	}
	return b.String()
}

// Request is one request to the API.
type Request struct {
	Method string
	Path   string
	Query  url.Values
	// Body may be nil, a []byte or json.RawMessage of JSON, or any
	// value that marshals to JSON.  Multipart requests set Form instead.
	Body any
	// Form, if set, is sent as multipart/form-data.
	Form *Form
	// Headers are sent in addition to the usual ones.
	Headers http.Header
	// Accept is what to ask for; "" means JSON.
	Accept string
}

// Form is a multipart request body: fields and one file.
type Form struct {
	Fields map[string]string
	// FileField is the name the file is sent under, such as
	// "works_upload[csv_file]".
	FileField string
	Filename  string
	File      []byte
}

// Get sends a GET request.
func (c *Client) Get(ctx context.Context, path string, query url.Values) (*Response, error) {
	return c.Do(ctx, Request{Method: http.MethodGet, Path: path, Query: query})
}

// Post sends a POST request with a JSON body.
func (c *Client) Post(ctx context.Context, path string, body any) (*Response, error) {
	return c.Do(ctx, Request{Method: http.MethodPost, Path: path, Body: body})
}

// Patch sends a PATCH request with a JSON body.
func (c *Client) Patch(ctx context.Context, path string, body any) (*Response, error) {
	return c.Do(ctx, Request{Method: http.MethodPatch, Path: path, Body: body})
}

// Delete sends a DELETE request.
func (c *Client) Delete(ctx context.Context, path string) (*Response, error) {
	return c.Do(ctx, Request{Method: http.MethodDelete, Path: path})
}

// Download fetches a file, such as a CSV, rather than JSON.
func (c *Client) Download(ctx context.Context, path string, query url.Values) (*Response, error) {
	return c.Do(ctx, Request{Method: http.MethodGet, Path: path, Query: query, Accept: "*/*"})
}

// Do sends a request.  A response outside the 2xx range is returned as
// an *Error.
func (c *Client) Do(ctx context.Context, r Request) (*Response, error) {
	payload, contentType, err := encodeBody(r)
	if err != nil {
		return nil, err
	}
	target := strings.TrimRight(c.BaseURL, "/") + r.Path
	if len(r.Query) > 0 {
		target += "?" + r.Query.Encode()
	}
	headers := http.Header{}
	for name, values := range r.Headers {
		headers[name] = values
	}
	if contentType != "" {
		headers.Set("Content-Type", contentType)
	}
	headers.Set("Accept", "application/json")
	if r.Accept != "" {
		headers.Set("Accept", r.Accept)
	}

	token := ""
	if c.Auth != nil {
		if token, err = c.Auth.Token(ctx); err != nil {
			return nil, err
		}
	}

	// A create that carries an Idempotency-Key may be repeated: the
	// server answers a repeat with the record the first one made.
	retryable := idempotent(r.Method) || headers.Get("Idempotency-Key") != ""

	resp, err := c.send(ctx, r.Method, target, payload, headers, token, retryable)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode == http.StatusUnauthorized && c.Auth != nil && token != "" {
		replacement, ok, refreshErr := c.Auth.Refresh(ctx, token)
		if refreshErr != nil {
			return nil, refreshErr
		}
		if ok {
			if resp, err = c.send(ctx, r.Method, target, payload, headers, replacement, retryable); err != nil {
				return nil, err
			}
		}
	}

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, newError(r.Method, r.Path, resp)
	}
	return resp, nil
}

func encodeBody(r Request) ([]byte, string, error) {
	if r.Form != nil {
		var buf bytes.Buffer
		w := multipart.NewWriter(&buf)
		for name, value := range r.Form.Fields {
			if err := w.WriteField(name, value); err != nil {
				return nil, "", err
			}
		}
		if r.Form.FileField != "" {
			part, err := w.CreateFormFile(r.Form.FileField, r.Form.Filename)
			if err != nil {
				return nil, "", err
			}
			if _, err := part.Write(r.Form.File); err != nil {
				return nil, "", err
			}
		}
		if err := w.Close(); err != nil {
			return nil, "", err
		}
		return buf.Bytes(), w.FormDataContentType(), nil
	}
	switch b := r.Body.(type) {
	case nil:
		return nil, "", nil
	case []byte:
		return b, "application/json", nil
	case json.RawMessage:
		return b, "application/json", nil
	default:
		payload, err := json.Marshal(r.Body)
		if err != nil {
			return nil, "", fmt.Errorf("cannot encode the request body: %w", err)
		}
		return payload, "application/json", nil
	}
}

// send performs one request, retrying it when that is safe.
func (c *Client) send(ctx context.Context, method, target string, payload []byte, headers http.Header, token string, retryable bool) (*Response, error) {
	delay := c.RetryDelay
	for attempt := 0; ; attempt++ {
		resp, err := c.sendOnce(ctx, method, target, payload, headers, token)
		again := retryable && attempt < c.Retries && ctx.Err() == nil &&
			(err != nil || resp.StatusCode == http.StatusBadGateway || resp.StatusCode == http.StatusServiceUnavailable || resp.StatusCode == http.StatusGatewayTimeout)
		if !again {
			return resp, err
		}
		if c.Debug != nil {
			fmt.Fprintf(c.Debug, "* retrying in %s\n", delay)
		}
		select {
		case <-time.After(delay):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		delay *= 2
	}
}

// idempotent reports whether repeating a request cannot change its
// effect.  POST is left out: a create that timed out may have happened.
func idempotent(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	}
	return false
}

func (c *Client) sendOnce(ctx context.Context, method, target string, payload []byte, headers http.Header, token string) (*Response, error) {
	var reader io.Reader
	if payload != nil {
		reader = bytes.NewReader(payload)
	}
	req, err := http.NewRequestWithContext(ctx, method, target, reader)
	if err != nil {
		return nil, err
	}
	for name, values := range headers {
		req.Header[name] = values
	}
	if c.UserAgent != "" {
		req.Header.Set("User-Agent", c.UserAgent)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if c.APIVersion != "" {
		req.Header.Set("X-Client-API-Version", c.APIVersion)
	}

	if c.Debug != nil {
		fmt.Fprintf(c.Debug, "> %s %s\n", method, target)
		if c.APIVersion != "" {
			fmt.Fprintf(c.Debug, "> X-Client-API-Version: %s\n", c.APIVersion)
		}
		if key := headers.Get("Idempotency-Key"); key != "" {
			fmt.Fprintf(c.Debug, "> Idempotency-Key: %s\n", key)
		}
		if payload != nil {
			if strings.HasPrefix(headers.Get("Content-Type"), "multipart/") {
				fmt.Fprintf(c.Debug, "> (multipart body, %d bytes)\n", len(payload))
			} else {
				fmt.Fprintf(c.Debug, "> %s\n", truncate(payload, 2000))
			}
		}
	}
	started := time.Now()

	hc := c.HTTPClient
	if hc == nil {
		hc = http.DefaultClient
	}
	httpResp, err := hc.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		var urlErr *url.Error
		if errors.As(err, &urlErr) {
			// The URL is in every message already; keep the cause.
			if urlErr.Timeout() {
				return nil, fmt.Errorf("%s did not answer in time", c.BaseURL)
			}
			return nil, fmt.Errorf("cannot reach %s: %w", c.BaseURL, urlErr.Err)
		}
		return nil, err
	}
	defer httpResp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(httpResp.Body, maxBody+1))
	if err != nil {
		return nil, fmt.Errorf("reading the response from %s failed: %w", c.BaseURL, err)
	}
	if len(body) > maxBody {
		return nil, fmt.Errorf("the response from %s is larger than %d MB", c.BaseURL, maxBody>>20)
	}

	c.Versions.Seen(httpResp.Header.Get("X-API-Version"), httpResp.Header.Get("X-API-Version-Status"))

	if c.Debug != nil {
		fmt.Fprintf(c.Debug, "< %s (%s)\n", httpResp.Status, time.Since(started).Round(time.Millisecond))
		if version := httpResp.Header.Get("X-API-Version"); version != "" {
			fmt.Fprintf(c.Debug, "< X-API-Version: %s (%s)\n", version, httpResp.Header.Get("X-API-Version-Status"))
		}
		if len(body) > 0 {
			if strings.HasPrefix(httpResp.Header.Get("Content-Type"), "application/json") {
				fmt.Fprintf(c.Debug, "< %s\n", truncate(body, 2000))
			} else {
				fmt.Fprintf(c.Debug, "< (%s, %d bytes)\n", httpResp.Header.Get("Content-Type"), len(body))
			}
		}
	}
	return &Response{StatusCode: httpResp.StatusCode, Header: httpResp.Header, Body: body}, nil
}

// secrets are the values that are never written to the log, whichever
// way they are travelling: passwords going up, tokens coming down.
var secrets = regexp.MustCompile(`"(password|password_confirmation|server_password|auth_token|access_token|refresh_token|client_secret)"(\s*:\s*)"(?:[^"\\]|\\.)*"`)

// redact takes the secrets out of a body for the log.
func redact(data []byte) []byte {
	return secrets.ReplaceAll(data, []byte(`"$1"$2"(not shown)"`))
}

func truncate(data []byte, max int) string {
	data = redact(data)
	if len(data) <= max {
		return string(data)
	}
	return string(data[:max]) + fmt.Sprintf("... (%d bytes)", len(data))
}

// Pagination is the pagination block of a list response.
type Pagination struct {
	CurrentPage int `json:"current_page"`
	TotalPages  int `json:"total_pages"`
	TotalCount  int `json:"total_count"`
	PerPage     int `json:"per_page"`
}

// NextPage is the page after this one, or 0 when this is the last.
func (p Pagination) NextPage() int {
	if p.CurrentPage > 0 && p.CurrentPage < p.TotalPages {
		return p.CurrentPage + 1
	}
	return 0
}

// Page is one page of a list.
type Page struct {
	// Paginated says whether the server sent a pagination block.  Some
	// lists come whole.
	Paginated  bool
	Pagination Pagination
	Items      []json.RawMessage
	// Extra holds the top-level keys other than the list and the
	// pagination, such as a chase-score list's hidden counts.
	Extra map[string]json.RawMessage
}

// Total is how many records the whole list has.
func (p *Page) Total() int {
	if p.Paginated {
		return p.Pagination.TotalCount
	}
	return len(p.Items)
}

// ListOptions selects a page of a list.
type ListOptions struct {
	Page    int
	PerPage int
	// Query holds whatever else narrows the list, such as q[title].
	Query url.Values
}

func (o ListOptions) query() url.Values {
	query := url.Values{}
	for key, values := range o.Query {
		query[key] = values
	}
	if o.Page > 0 {
		query.Set("page", strconv.Itoa(o.Page))
	}
	if o.PerPage > 0 {
		query.Set("per_page", strconv.Itoa(o.PerPage))
	}
	return query
}

// List fetches one page of the list at path, whose records are under key.
func (c *Client) List(ctx context.Context, path, key string, opts ListOptions) (*Page, error) {
	resp, err := c.Get(ctx, path, opts.query())
	if err != nil {
		return nil, err
	}
	return ParsePage(resp.Body, key)
}

// ParsePage reads a list response: {key: [...], pagination: {...}}, the
// pagination being optional.
func ParsePage(body []byte, key string) (*Page, error) {
	page := &Page{Extra: map[string]json.RawMessage{}}
	var top map[string]json.RawMessage
	if err := json.Unmarshal(body, &top); err != nil {
		return nil, fmt.Errorf("the server sent a list that is not a JSON object: %w", err)
	}
	items, ok := top[key]
	if !ok {
		return nil, fmt.Errorf("the server sent a list without %q", key)
	}
	if err := json.Unmarshal(items, &page.Items); err != nil {
		return nil, fmt.Errorf("the server sent a %q that is not a list: %w", key, err)
	}
	if raw, ok := top["pagination"]; ok {
		if err := json.Unmarshal(raw, &page.Pagination); err != nil {
			return nil, fmt.Errorf("the server sent pagination that cannot be read: %w", err)
		}
		page.Paginated = true
	}
	for name, raw := range top {
		if name != key && name != "pagination" {
			page.Extra[name] = raw
		}
	}
	return page, nil
}

// ListAll fetches every page of the list at path that query selects,
// stopping early once limit records have been read (zero means no
// limit).  each, if set, is called after every page with the number of
// records read so far and the total.
func (c *Client) ListAll(ctx context.Context, path, key string, query url.Values, limit int, each func(read, total int)) (*Page, error) {
	all := &Page{}
	opts := ListOptions{Page: 1, PerPage: MaxPerPage, Query: query}
	if limit > 0 && limit < MaxPerPage {
		opts.PerPage = limit
	}
	for {
		page, err := c.List(ctx, path, key, opts)
		if err != nil {
			return nil, err
		}
		if opts.Page == 1 {
			all.Extra = page.Extra
			all.Paginated = page.Paginated
		}
		all.Items = append(all.Items, page.Items...)
		all.Pagination = page.Pagination
		if each != nil {
			each(len(all.Items), page.Total())
		}
		if limit > 0 && len(all.Items) >= limit {
			all.Items = all.Items[:limit]
			break
		}
		// An empty page ends the walk even if the server claims there is
		// another, so a miscounting server cannot loop the client.
		next := page.Pagination.NextPage()
		if !page.Paginated || next <= opts.Page || len(page.Items) == 0 {
			break
		}
		opts.Page = next
	}
	return all, nil
}
