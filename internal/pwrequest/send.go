package pwrequest

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
)

// Result is one response, as the report prints it.
type Result struct {
	Status  int         `json:"status"`
	Headers http.Header `json:"headers"`
	// Body is the response bytes; the JSON report parses it when the response
	// says it is JSON.
	Body []byte `json:"-"`
	// Request is what was sent, so a reader can copy it into curl.
	Request SentRequest `json:"request"`
}

// SentRequest is the request as it left, for the report.
type SentRequest struct {
	Method string `json:"method"`
	URL    string `json:"url"`
}

// Send issues the planned request against origin with client, without
// following redirects unless follow is set. A cookie-jar client already holds
// the caller's cookies; explicit -b cookies join them on this one request.
func Send(ctx context.Context, client *http.Client, origin string, plan Plan, follow bool, extraHeaders []Pair, cookieHeader string, basic string) (Result, error) {
	target, err := url.Parse(strings.TrimSuffix(origin, "/") + plan.Path)
	if err != nil {
		return Result{}, err
	}
	if len(plan.Query) > 0 {
		values := target.Query()
		for key, list := range plan.Query {
			for _, value := range list {
				values.Add(key, value)
			}
		}
		target.RawQuery = values.Encode()
	}
	var body io.Reader
	if len(plan.Body) > 0 {
		body = bytes.NewReader(plan.Body)
	}
	request, err := http.NewRequestWithContext(ctx, plan.Method, target.String(), body)
	if err != nil {
		return Result{}, err
	}
	request.Header.Set("User-Agent", "pw-request")
	if plan.ContentType != "" && body != nil {
		request.Header.Set("Content-Type", plan.ContentType)
	}
	for key, value := range plan.Headers {
		request.Header.Set(key, value)
	}
	for _, header := range extraHeaders {
		if strings.EqualFold(header.Key, "Content-Type") && body == nil {
			continue
		}
		request.Header.Set(header.Key, header.Value)
	}
	cookies := append([]string{}, plan.Cookies...)
	if cookieHeader != "" {
		cookies = append(cookies, cookieHeader)
	}
	if len(cookies) > 0 {
		request.Header.Set("Cookie", strings.Join(cookies, "; "))
	}
	if basic != "" {
		user, password, _ := strings.Cut(basic, ":")
		request.SetBasicAuth(user, password)
	}
	if !follow {
		copied := *client
		copied.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
		client = &copied
	}
	response, err := client.Do(request)
	if err != nil {
		return Result{}, err
	}
	defer response.Body.Close()
	content, err := io.ReadAll(response.Body)
	if err != nil {
		return Result{}, fmt.Errorf("read response: %w", err)
	}
	return Result{
		Status:  response.StatusCode,
		Headers: response.Header,
		Body:    content,
		Request: SentRequest{Method: plan.Method, URL: target.String()},
	}, nil
}

// Report is the one JSON object --format json prints.
type Report struct {
	Status    int         `json:"status"`
	Headers   http.Header `json:"headers"`
	Body      any         `json:"body"`
	Request   SentRequest `json:"request"`
	Operation *Operation  `json:"operation"`
	Reason    string      `json:"reason,omitempty"`
	Routing   []Routed    `json:"routing"`
	// Target says how the origin was found: url, dev, guess.
	Target string `json:"target"`
	Origin string `json:"origin"`
}

// NewReport folds a result and its plan into the report shape. A JSON body is
// parsed so an agent reads one shape; anything else is a string.
func NewReport(result Result, plan Plan, target, origin string) Report {
	report := Report{
		Status:    result.Status,
		Headers:   result.Headers,
		Request:   result.Request,
		Operation: plan.Operation,
		Reason:    plan.Reason,
		Routing:   plan.Routing,
		Target:    target,
		Origin:    origin,
	}
	if report.Routing == nil {
		report.Routing = []Routed{}
	}
	mediaType := strings.ToLower(result.Headers.Get("Content-Type"))
	if strings.Contains(mediaType, "json") {
		var parsed any
		if err := json.Unmarshal(result.Body, &parsed); err == nil {
			report.Body = parsed
			return report
		}
	}
	report.Body = string(result.Body)
	return report
}

// HeaderLines returns the response headers in a stable order for -i.
func (r Result) HeaderLines() []string {
	names := make([]string, 0, len(r.Headers))
	for name := range r.Headers {
		names = append(names, name)
	}
	sort.Strings(names)
	var lines []string
	for _, name := range names {
		for _, value := range r.Headers[name] {
			lines = append(lines, name+": "+value)
		}
	}
	return lines
}
