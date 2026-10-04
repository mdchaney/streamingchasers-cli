package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"

	"github.com/mdchaney/streamingchasers-api/internal/api"
	"github.com/mdchaney/streamingchasers-api/internal/output"
	"github.com/spf13/cobra"
)

func (a *App) newAPICmd() *cobra.Command {
	var (
		method  string
		data    string
		queries []string
		include bool
		raw     bool
	)
	cmd := &cobra.Command{
		Use:   "api PATH",
		Short: "Make a request to the API yourself",
		Long: `Make a request to the API with your credentials and print the answer.
It is there for what the other commands do not cover.

PATH is taken from /api/v1 unless it starts with a slash. The method is
GET, or POST when there is --data, unless --method says otherwise. The
API is documented at https://app.streamingchasers.com/api/docs.html.`,
		Example: `  streamingchasers api companies
  streamingchasers api companies/2/works --query per_page=5 --query 'q[title]=daisy'
  streamingchasers api companies/2/writers --data '{"writer": {"external_id": "W-125", "last_name": "Bach"}}'
  streamingchasers api companies/2/works/W-999 --method DELETE
  streamingchasers api companies/2/sales/paid --raw > paid.csv`,
		Args: exactArgs("PATH"),
		RunE: func(cmd *cobra.Command, args []string) error {
			path := args[0]
			inline := ""
			if i := strings.Index(path, "?"); i >= 0 {
				path, inline = path[:i], path[i+1:]
			}
			if !strings.HasPrefix(path, "/") {
				path = "/api/v1/" + path
			}

			query, err := url.ParseQuery(inline)
			if err != nil {
				return usagef("the query in %q cannot be read: %s", args[0], err)
			}
			for _, pair := range queries {
				key, value, ok := strings.Cut(pair, "=")
				if !ok || key == "" {
					return usagef("--query takes key=value, not %q", pair)
				}
				query.Add(key, value)
			}

			var body any
			if data != "" {
				payload, err := a.readSource(data)
				if err != nil {
					return usagef("--data: %s", err)
				}
				if !json.Valid(payload) {
					return usagef("--data must be JSON")
				}
				body = json.RawMessage(payload)
			}
			if method == "" {
				method = http.MethodGet
				if body != nil {
					method = http.MethodPost
				}
			}
			method = strings.ToUpper(method)
			switch method {
			case http.MethodGet, http.MethodHead, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
			default:
				return usagef("--method must be GET, HEAD, POST, PUT, PATCH or DELETE, not %q", method)
			}

			s, err := a.session()
			if err != nil {
				return err
			}
			request := api.Request{Method: method, Path: path, Query: query, Body: body}
			if raw {
				request.Accept = "*/*"
			}
			resp, err := s.client.Do(cmd.Context(), request)
			if err != nil {
				return err
			}

			if include {
				fmt.Fprintf(a.Out, "%d %s\n", resp.StatusCode, http.StatusText(resp.StatusCode))
				names := make([]string, 0, len(resp.Header))
				for name := range resp.Header {
					names = append(names, name)
				}
				sort.Strings(names)
				for _, name := range names {
					for _, value := range resp.Header[name] {
						fmt.Fprintf(a.Out, "%s: %s\n", name, value)
					}
				}
				fmt.Fprintln(a.Out)
			}
			if len(bytes.TrimSpace(resp.Body)) == 0 {
				return nil
			}
			if json.Valid(resp.Body) && !raw {
				return output.WriteJSON(a.Out, resp.Body)
			}
			_, err = a.Out.Write(resp.Body)
			return err
		},
	}
	cmd.Flags().StringVarP(&method, "method", "X", "", "HTTP method (default GET, or POST with --data)")
	cmd.Flags().StringVarP(&data, "data", "d", "", "JSON to send; @FILE reads a file and - reads standard input")
	cmd.Flags().StringArrayVarP(&queries, "query", "q", nil, "query parameter, as key=value (may be repeated)")
	cmd.Flags().BoolVarP(&include, "include", "i", false, "print the status and the headers of the answer too")
	cmd.Flags().BoolVar(&raw, "raw", false, "ask for any content type and print the answer as it came, for CSV downloads")
	return cmd
}
