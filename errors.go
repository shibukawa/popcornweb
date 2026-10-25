package petitweb

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/shibukawa/popcornweb/pwruntime"
	httpbind "github.com/shibukawa/tinybind-go/httpbind"
)

// ErrorPage is the sanitized model passed to an application's HTML renderer.
type ErrorPage struct {
	Status    int
	Title     string
	Detail    string
	Code      string
	RequestID string
	fields    []httpbind.FieldError
	problem   pwruntime.Problem
}

// ErrorRenderer writes a complete HTML error response.
type ErrorRenderer func(http.ResponseWriter, *http.Request, ErrorPage) error

// ErrorHandler negotiates safe HTML and RFC 9457 error responses.
type ErrorHandler struct {
	Renderer ErrorRenderer
	// Logger is optional; the zero value falls back to the request logger.
	Logger Logger
}

// WriteError writes err exactly once. Internal error causes are never exposed.
func (h ErrorHandler) WriteError(w http.ResponseWriter, r *http.Request, err error) {
	if err == nil {
		return
	}
	page := errorPage(r, err)
	_ = pwruntime.ApplyProblemHeaders(w.Header(), page.problem)
	if h.Renderer != nil && acceptsHTML(r) {
		guard := &commitGuard{ResponseWriter: w}
		if renderErr := h.Renderer(guard, r, page); renderErr == nil {
			return
		} else {
			logger := h.Logger
			if !logger.Enabled(pwruntime.LevelError) {
				logger = ReadLogger(r.Context())
			}
			logger.Log(r.Context(), pwruntime.LevelError, "petitweb error renderer failed", pwruntime.Err(renderErr))
			if guard.committed {
				return
			}
			clearProblemHeaders(w.Header())
			page = ErrorPage{Status: http.StatusInternalServerError, Title: "Internal Server Error", Detail: "internal error", Code: "internal", RequestID: page.RequestID}
		}
	}
	writeProblem(w, page)
}

// WriteError writes with safe defaults and RFC 9457 negotiation.
func WriteError(w http.ResponseWriter, r *http.Request, err error) {
	ErrorHandler{}.WriteError(w, r, err)
}

func errorPage(r *http.Request, err error) ErrorPage {
	page := ErrorPage{Status: http.StatusInternalServerError, Title: "Internal Server Error", Detail: "internal error", Code: "internal"}
	if r != nil {
		if requestID, ok := ReadRequestID(r.Context()); ok {
			page.RequestID = requestID
		}
	}
	var problem pwruntime.Problem
	if errors.As(err, &problem) {
		if problem.Status == 0 {
			problem.Status = http.StatusInternalServerError
		}
		if problem.Title == "" {
			problem.Title = http.StatusText(problem.Status)
		}
		page.Status = problem.Status
		page.Title = problem.Title
		page.Detail = problem.Message
		page.Code = problem.Code
		page.fields = append([]httpbind.FieldError(nil), problem.Fields...)
		page.problem = problem
		if page.Status >= 500 {
			page.Title = http.StatusText(page.Status)
			page.Detail = "internal error"
			page.Code = "internal"
			page.fields = nil
		}
		if page.Status < 400 || page.Status > 599 {
			return ErrorPage{Status: http.StatusInternalServerError, Title: "Internal Server Error", Detail: "internal error", Code: "internal", RequestID: page.RequestID}
		}
		return page
	}
	if mapped, ok := httpbind.AsHTTPError(err); ok {
		page.Status = mapped.Status
		page.Title = mapped.Title
		if page.Title == "" {
			page.Title = http.StatusText(page.Status)
		}
		if mapped.Problem.Code != "" {
			page.Code = mapped.Problem.Code
		}
		if mapped.Problem.Message != "" {
			page.Detail = mapped.Problem.Message
		} else {
			page.Detail = page.Title
		}
		if page.Status >= 500 {
			page.Title = http.StatusText(page.Status)
			page.Detail = "internal error"
			page.Code = "internal"
		} else {
			page.fields = append([]httpbind.FieldError(nil), mapped.Fields...)
		}
	}
	if page.Status < 400 || page.Status > 599 {
		page = ErrorPage{Status: http.StatusInternalServerError, Title: "Internal Server Error", Detail: "internal error", Code: "internal", RequestID: page.RequestID}
	}
	return page
}

func clearProblemHeaders(header http.Header) {
	for _, name := range []string{"Cache-Control", "Retry-After", "X-RateLimit-Limit", "X-RateLimit-Remaining", "X-RateLimit-Reset"} {
		header.Del(name)
	}
}

func writeProblem(w http.ResponseWriter, page ErrorPage) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(page.Status)
	var b strings.Builder
	b.WriteString(`{"type":"about:blank","title":`)
	b.WriteString(strconv.Quote(page.Title))
	b.WriteString(`,"status":`)
	b.WriteString(strconv.Itoa(page.Status))
	b.WriteString(`,"detail":`)
	b.WriteString(strconv.Quote(page.Detail))
	b.WriteString(`,"code":`)
	b.WriteString(strconv.Quote(page.Code))
	if page.RequestID != "" {
		b.WriteString(`,"request_id":`)
		b.WriteString(strconv.Quote(page.RequestID))
	}
	if len(page.fields) > 0 {
		b.WriteString(`,"errors":[`)
		for i, field := range page.fields {
			if i > 0 {
				b.WriteByte(',')
			}
			b.WriteString(`{"field":`)
			b.WriteString(strconv.Quote(field.Field))
			b.WriteString(`,"location":`)
			b.WriteString(strconv.Quote(field.Location))
			b.WriteString(`,"message":`)
			b.WriteString(strconv.Quote(field.Message))
			b.WriteByte('}')
		}
		b.WriteByte(']')
	}
	b.WriteByte('}')
	_, _ = io.WriteString(w, b.String())
}

func acceptsHTML(r *http.Request) bool {
	if r == nil {
		return false
	}
	accept := r.Header.Get("Accept")
	if accept == "" {
		return false
	}
	var htmlQ, jsonQ float64 = -1, -1
	for item := range splitSeq(accept, ',') {
		media, parameters, _ := strings.Cut(item, ";")
		media = strings.TrimSpace(strings.ToLower(media))
		q := 1.0
		for parameter := range splitSeq(parameters, ';') {
			key, value, ok := strings.Cut(strings.TrimSpace(parameter), "=")
			if ok && strings.EqualFold(key, "q") {
				parsed, parseErr := strconv.ParseFloat(value, 64)
				if parseErr != nil || parsed < 0 || parsed > 1 {
					q = 0
				} else {
					q = parsed
				}
			}
		}
		switch media {
		case "text/html", "application/xhtml+xml":
			if q > htmlQ {
				htmlQ = q
			}
		case "application/problem+json", "application/json":
			if q > jsonQ {
				jsonQ = q
			}
		case "*/*":
			if q > jsonQ {
				jsonQ = q
			}
		}
	}
	return htmlQ > 0 && htmlQ >= jsonQ
}

// splitSeq yields the separator-delimited pieces of value without allocating.
// Unlike strings.SplitSeq it never yields anything for an empty value, which
// is what a header parse wants.
func splitSeq(value string, separator byte) func(func(string) bool) {
	return func(yield func(string) bool) {
		for value != "" {
			var piece string
			if index := strings.IndexByte(value, separator); index >= 0 {
				piece, value = value[:index], value[index+1:]
			} else {
				piece, value = value, ""
			}
			if !yield(piece) {
				return
			}
		}
	}
}

type commitGuard struct {
	http.ResponseWriter
	committed bool
}

func (w *commitGuard) WriteHeader(status int) {
	w.committed = true
	w.ResponseWriter.WriteHeader(status)
}

func (w *commitGuard) Write(p []byte) (int, error) {
	w.committed = true
	return w.ResponseWriter.Write(p)
}

func (p ErrorPage) String() string { return fmt.Sprintf("%d %s", p.Status, p.Title) }
