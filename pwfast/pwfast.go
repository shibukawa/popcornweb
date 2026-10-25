package pwfast

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/shibukawa/popcornweb/pwruntime"
	"github.com/shibukawa/tinybind-go/fasthttpbind"
	"github.com/shibukawa/tinybind-go/htmlbind"
	httpbind "github.com/shibukawa/tinybind-go/httpbind"
	"github.com/shibukawa/tinygodriver/fasthttp"
)

// The composition types are htmlbind's on both transports. They describe
// rendering rather than a transport, so there is nothing here to mirror: pw
// aliases the same declarations, and a fragment built by generated code is the
// same value whichever half receives it.
type (
	HTMLFragment = htmlbind.Fragment
	HTMLWrapper  = htmlbind.Wrapper
	HTMLOption   = htmlbind.Option
)

// The application-facing problem is pwruntime's, the same declaration pw
// aliases, so a problem built on one side is the value the other inspects and
// unwraps. An earlier draft aliased the module's two-field problem body under
// this name, which made one name mean two types.
//
// HTTPError stays the module's, because it is the module's own error and both
// of its runtimes already alias one declaration of it.
type (
	Problem    = pwruntime.Problem
	FieldError = pwruntime.FieldError
	RateLimit  = pwruntime.RateLimit
	HTTPError  = fasthttpbind.HTTPError
)

// The problem constructors are pwruntime's too, so a rewritten call finds the
// same names building the same value.
func Field(field, location, message string) FieldError {
	return pwruntime.Field(field, location, message)
}

func BadRequest(values ...any) Problem      { return pwruntime.BadRequest(values...) }
func Unauthorized(values ...any) Problem    { return pwruntime.Unauthorized(values...) }
func Forbidden(values ...any) Problem       { return pwruntime.Forbidden(values...) }
func NotFound(values ...any) Problem        { return pwruntime.NotFound(values...) }
func Conflict(values ...any) Problem        { return pwruntime.Conflict(values...) }
func PayloadTooLarge(values ...any) Problem { return pwruntime.PayloadTooLarge(values...) }
func TooManyRequests(values ...any) Problem { return pwruntime.TooManyRequests(values...) }
func RateLimited(rate RateLimit, values ...any) Problem {
	return pwruntime.RateLimited(rate, values...)
}
func ServiceUnavailable(values ...any) Problem {
	return pwruntime.ServiceUnavailable(values...)
}
func InternalServerError(values ...any) Problem {
	return pwruntime.InternalServerError(values...)
}

// Validation reports a 400 response carrying every detected field failure.
func Validation(fields ...FieldError) Problem { return pwruntime.Validation(fields...) }

// RegisterHTMLDocument and RegisterHTMLErrorPage reach the same state pw's do,
// which is the whole reason that state is in pwruntime: generated registration
// calls whichever package it imports, and both must find one registry.
func RegisterHTMLDocument(wrapper HTMLWrapper) { pwruntime.RegisterHTMLDocument(wrapper) }

func RegisterHTMLErrorPage(resolve pwruntime.HTMLErrorPage) {
	pwruntime.RegisterHTMLErrorPage(resolve)
}

// Stream is the module's own typed event stream, the same declaration pw
// aliases, so the value a callback receives is one type across the pair.
type Stream[T any] = fasthttpbind.Stream[T]

// Parse binds the request into the generated input type.
func Parse[T any](r *fasthttp.RequestCtx) (T, error) { return fasthttpbind.Bind[T](r) }

// WriteAPI writes one typed API response, or the problem that describes why it
// could not.
func WriteAPI[T any](r *fasthttp.RequestCtx, value T) {
	if err := fasthttpbind.Write(r, value); err != nil {
		WriteProblem(r, err)
	}
}

// WriteStatus is WriteAPI with the status the handler chose.
//
// The name is pw's, not a description of what it does. An earlier draft called
// it WriteAPIStatus, which reads better and does not work: a rewritten call
// keeps its selector and only moves its qualifier, so pw.WriteStatus becomes
// pwfast.WriteStatus and a better name here is simply a missing symbol there.
func WriteStatus[T any](r *fasthttp.RequestCtx, status int, value T) {
	if err := fasthttpbind.WriteStatus(r, status, value); err != nil {
		WriteProblem(r, err)
	}
}

// OpenAPIJSON serves the assembled document.
//
// It reassembles per request where the net/http half is served from the
// module's cache, because that cache is keyed on a registration counter the
// module does not export and there is no way to observe an invalidation from
// out here. The endpoint is a documentation route rather than a hot one, so the
// cost buys correctness against a fragment registered after the first read.
func OpenAPIJSON(r *fasthttp.RequestCtx) {
	document, err := httpbind.AssembleOpenAPI()
	if err != nil {
		WriteProblem(r, InternalServerError(err))
		return
	}
	r.Response.Header.SetContentType("application/json; charset=utf-8")
	r.SetStatusCode(fasthttp.StatusOK)
	_, _ = r.Write(document)
}

// WriteProblem answers with the problem document describing err, or with the
// application's error page when the client would rather have one.
//
// One handler answers a browser form post and an API client on the same route,
// so which representation a failure takes is the client's to say. It is read
// from Accept by the shared rule both transports use, and the page it reaches
// is the one registered in the shared registry — so an application registers
// its error pages once and both builds serve them.
func WriteProblem(r *fasthttp.RequestCtx, err error) {
	// A redirect returned rather than written arrives here, on the same terms
	// as on the other transport: the one path a render's error takes is this
	// one, and Redirect applies the safety check and the update branch.
	var redirect pwruntime.RedirectError
	if errors.As(err, &redirect) {
		Redirect(r, redirect.Location, redirect.Status)
		return
	}
	// Every error becomes a problem, through the shared mapping, so a failure
	// this package cannot classify still reaches the negotiation below rather
	// than falling out of it into the binding layer's own writer — which is
	// what used to keep an application's error page from ever being reached by
	// an unrecovered render.
	problem := pwruntime.MapProblem(err)
	if problem.Status < 400 || problem.Status > 599 {
		problem = InternalServerError(err)
	}
	if problem.Title == "" {
		problem.Title = http.StatusText(problem.Status)
	}
	if problem.Status >= 500 {
		problem.Title = http.StatusText(problem.Status)
		if problem.Title == "" {
			problem.Title = "Internal Server Error"
		}
		problem.Message = "internal error"
		problem.Code = "internal"
		problem.Fields = nil
	}
	headers, _ := pwruntime.ProblemHeaders(problem)
	for name, values := range headers {
		for _, value := range values {
			r.Response.Header.Add(name, value)
		}
	}
	if writeHTMLProblem(r, problem) {
		return
	}
	r.Response.Header.SetContentType("application/problem+json")
	r.SetStatusCode(problem.Status)
	var body strings.Builder
	body.WriteString(`{"type":"about:blank","title":`)
	body.WriteString(strconv.Quote(problem.Title))
	body.WriteString(`,"status":`)
	body.WriteString(strconv.Itoa(problem.Status))
	body.WriteString(`,"detail":`)
	body.WriteString(strconv.Quote(problem.Message))
	body.WriteString(`,"code":`)
	body.WriteString(strconv.Quote(problem.Code))
	if len(problem.Fields) > 0 {
		body.WriteString(`,"errors":[`)
		for index, field := range problem.Fields {
			if index > 0 {
				body.WriteByte(',')
			}
			body.WriteString(`{"field":`)
			body.WriteString(strconv.Quote(field.Field))
			body.WriteString(`,"location":`)
			body.WriteString(strconv.Quote(field.Location))
			body.WriteString(`,"message":`)
			body.WriteString(strconv.Quote(field.Message))
			body.WriteByte('}')
		}
		body.WriteByte(']')
	}
	body.WriteString("}\n")
	r.Response.SetBodyString(body.String())
}

// WriteStream answers with a typed event stream, running fn to produce it.
//
// The callback is the shape rather than a returned stream because this
// transport registers a body writer that runs after the handler returns, so a
// stream the handler held would have nothing to write into. The callback
// returning is the close, and an error from it is post-commit and reaches the
// installed stream error handler.
func WriteStream[T any](r *fasthttp.RequestCtx, fn func(*Stream[T]) error) {
	fasthttpbind.WriteStream(r, fn)
}

// SetStreamErrorHandler installs what receives a stream failure raised after
// the response committed. It is process-wide and shared with the net/http half.
func SetStreamErrorHandler(fn func(error)) { fasthttpbind.SetStreamErrorHandler(fn) }
