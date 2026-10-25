package pwruntime

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	tinybind "github.com/shibukawa/tinybind-go/httpbind"
)

// Problem is the application-facing RFC problem value.
//
// It lives here rather than in pw because both transport runtimes have to
// build, inspect, and unwrap the same type. Two structs that agree today are
// two chances to disagree later, and the failure is silent: an errors.As that
// stops matching, a problem that no longer unwraps. The module reached the same
// conclusion for its own error types and put them in a leaf both of its
// runtimes alias.
//
// This is not the module's Problem, which carries a code and a message and is
// the body its error constructors take. This one is the whole answer: the
// status a response gets, the title it shows, the fields that failed, and the
// cause that stays server-side.
type Problem struct {
	Status    int
	Title     string
	Code      string
	Message   string
	Fields    []FieldError
	Cause     error
	RateLimit *RateLimit
}

// RateLimit is the retry metadata attached to a 429 problem. X-RateLimit-*
// fields are compatibility conventions; Retry-After is the standard signal.
type RateLimit struct {
	Limit      uint64
	Remaining  uint64
	Reset      time.Time
	RetryAfter time.Duration
}

// Validate rejects metadata that would contradict itself on the wire.
func (r RateLimit) Validate() error {
	if r.Remaining > r.Limit {
		return fmt.Errorf("pwruntime: rate limit remaining %d exceeds limit %d", r.Remaining, r.Limit)
	}
	if r.RetryAfter < 0 {
		return fmt.Errorf("pwruntime: negative retry-after %s", r.RetryAfter)
	}
	return nil
}

// ApplyProblemHeaders writes response metadata before a problem response is
// committed. Invalid optional metadata is returned to the caller and omitted.
//
// It writes straight onto the caller's header rather than through the detached
// map ProblemHeaders builds: this runs on every error response, and the common
// problem carries zero or one header.
func ApplyProblemHeaders(header http.Header, problem Problem) error {
	if problem.Status == http.StatusTooManyRequests {
		header.Set("Cache-Control", "no-store")
	}
	if problem.RateLimit == nil {
		return nil
	}
	if problem.Status != http.StatusTooManyRequests {
		return fmt.Errorf("pwruntime: rate limit metadata requires HTTP 429")
	}
	rate := *problem.RateLimit
	if err := rate.Validate(); err != nil {
		return err
	}
	header.Set("X-RateLimit-Limit", strconv.FormatUint(rate.Limit, 10))
	header.Set("X-RateLimit-Remaining", strconv.FormatUint(rate.Remaining, 10))
	if !rate.Reset.IsZero() {
		header.Set("X-RateLimit-Reset", strconv.FormatInt(rate.Reset.Unix(), 10))
	}
	if rate.RetryAfter > 0 {
		seconds := rate.RetryAfter / time.Second
		if rate.RetryAfter%time.Second != 0 {
			seconds++
		}
		header.Set("Retry-After", strconv.FormatInt(int64(seconds), 10))
	}
	return nil
}

// ProblemHeaders returns a detached set of response metadata for either HTTP
// transport runtime.
func ProblemHeaders(problem Problem) (http.Header, error) {
	header := make(http.Header)
	err := ApplyProblemHeaders(header, problem)
	return header, err
}

// FieldError describes a single field-level validation failure. It is the
// module's own type, already shared by both of its runtimes.
type FieldError = tinybind.FieldError

// Field builds a field-level validation error for Validation.
func Field(field, location, message string) FieldError {
	return tinybind.Field(field, location, message)
}

func (p Problem) Error() string {
	if p.Message != "" {
		return p.Message
	}
	if p.Title != "" {
		return p.Title
	}
	return http.StatusText(p.Status)
}

func (p Problem) Unwrap() error { return p.Cause }

// NewProblem builds one from a status, a title, and whatever the caller passed:
// nothing, another problem to carry through, an error to wrap, or a message.
func NewProblem(status int, title string, value any) Problem {
	return newCodedProblem(status, title, strings.ReplaceAll(strings.ToLower(title), " ", "_"), value)
}

// newCodedProblem is NewProblem with the code stated, for the fixed
// constructors whose titles are compile-time constants: deriving "bad_request"
// from "Bad Request" on every construction was two string allocations for an
// answer known when this file was written.
func newCodedProblem(status int, title, code string, value any) Problem {
	p := Problem{Status: status, Title: title, Code: code}
	switch value := value.(type) {
	case nil:
		p.Message = title
	case Problem:
		if value.Status == 0 {
			value.Status = status
		}
		if value.Title == "" {
			value.Title = title
		}
		return value
	case error:
		p.Message, p.Cause = value.Error(), value
	case string:
		p.Message = value
	default:
		p.Message = fmt.Sprint(value)
	}
	return p
}

// FirstValue is the variadic-to-optional shim the constructors share.
func FirstValue(values []any) any {
	if len(values) == 0 {
		return nil
	}
	return values[0]
}

func BadRequest(values ...any) Problem {
	return newCodedProblem(http.StatusBadRequest, "Bad Request", "bad_request", FirstValue(values))
}
func Unauthorized(values ...any) Problem {
	return newCodedProblem(http.StatusUnauthorized, "Unauthorized", "unauthorized", FirstValue(values))
}
func Forbidden(values ...any) Problem {
	return newCodedProblem(http.StatusForbidden, "Forbidden", "forbidden", FirstValue(values))
}
func NotFound(values ...any) Problem {
	return newCodedProblem(http.StatusNotFound, "Not Found", "not_found", FirstValue(values))
}
func Conflict(values ...any) Problem {
	return newCodedProblem(http.StatusConflict, "Conflict", "conflict", FirstValue(values))
}
func PayloadTooLarge(values ...any) Problem {
	return newCodedProblem(http.StatusRequestEntityTooLarge, "Payload Too Large", "payload_too_large", FirstValue(values))
}
func TooManyRequests(values ...any) Problem {
	p := newCodedProblem(http.StatusTooManyRequests, "Too Many Requests", "rate_limit_exceeded", FirstValue(values))
	// Forced after construction so a passed-through Problem carries it too:
	// this constructor answers with its own code whatever it was handed.
	p.Code = "rate_limit_exceeded"
	return p
}

// RateLimited builds a 429 problem carrying retry and quota metadata.
func RateLimited(rate RateLimit, values ...any) Problem {
	p := TooManyRequests(values...)
	copy := rate
	p.RateLimit = &copy
	return p
}
func ServiceUnavailable(values ...any) Problem {
	return newCodedProblem(http.StatusServiceUnavailable, "Service Unavailable", "service_unavailable", FirstValue(values))
}
func InternalServerError(values ...any) Problem {
	p := newCodedProblem(http.StatusInternalServerError, "Internal Server Error", "internal", FirstValue(values))
	// Forced after construction so a passed-through Problem carries it too.
	p.Code = "internal"
	return p
}

// Validation reports a 400 response carrying every detected field failure.
func Validation(fields ...FieldError) Problem {
	p := NewProblem(http.StatusBadRequest, "Validation failed", nil)
	p.Fields = append([]FieldError(nil), fields...)
	return p
}

// SanitizeProblem drops what a 5xx must never carry out of the process.
//
// Applying it twice changes nothing, which is what makes it safe to put at each
// writer rather than at one entry point: a boundary that failed with no recover
// clause reaches a writer directly, and every path that can answer with a
// server error has to lose the cause on the way out.
func SanitizeProblem(problem Problem) Problem {
	if problem.Status < 500 {
		return problem
	}
	// Title is reset too, matching the net/http ErrorHandler: a hand-built 5xx
	// carrying a sensitive custom title must not survive to the client any more
	// than Message does.
	problem.Title = http.StatusText(problem.Status)
	if problem.Title == "" {
		problem.Title = "Internal Server Error"
	}
	problem.Message = "internal error"
	problem.Code = "internal"
	problem.Fields = nil
	return problem
}

// AppendProblemJSON appends the RFC problem document to dst.
//
// It is here because it was written by hand in each runtime, and the two copies
// were one response body described twice — which is the shape of thing that
// stays identical until the day it does not, with nothing to say which client
// saw which. It is built by hand rather than marshalled because the document is
// flat and known, and this path must not fail: it is what answers when
// everything else already has.
//
// The caller sanitizes and sets the headers; this writes the body alone.
func AppendProblemJSON(dst []byte, problem Problem) []byte {
	dst = append(dst, `{"type":"about:blank","title":`...)
	dst = strconv.AppendQuote(dst, problem.Title)
	dst = append(dst, `,"status":`...)
	dst = strconv.AppendInt(dst, int64(problem.Status), 10)
	dst = append(dst, `,"detail":`...)
	dst = strconv.AppendQuote(dst, problem.Message)
	dst = append(dst, `,"code":`...)
	dst = strconv.AppendQuote(dst, problem.Code)
	if len(problem.Fields) > 0 {
		dst = append(dst, `,"errors":[`...)
		for index, field := range problem.Fields {
			if index > 0 {
				dst = append(dst, ',')
			}
			dst = append(dst, `{"field":`...)
			dst = strconv.AppendQuote(dst, field.Field)
			dst = append(dst, `,"location":`...)
			dst = strconv.AppendQuote(dst, field.Location)
			dst = append(dst, `,"message":`...)
			dst = strconv.AppendQuote(dst, field.Message)
			dst = append(dst, '}')
		}
		dst = append(dst, ']')
	}
	return append(dst, "}\n"...)
}
