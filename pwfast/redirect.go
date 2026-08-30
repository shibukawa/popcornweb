package pwfast

import (
	"html"
	"net/url"
	"path"
	"strings"

	"github.com/shibukawa/popcornweb/internal/botdetect"
	"github.com/shibukawa/popcornweb/internal/safeurl"
	"github.com/shibukawa/popcornweb/pwruntime"
	"github.com/shibukawa/tinybind-go/fasthttpbind"
	"github.com/shibukawa/tinygodriver/fasthttp"
)

// Redirect sends the browser to another location.
//
// It branches the way the net/http half does, and the branch is the reason it
// exists rather than a redirect helper being unportable. An update request is a
// fetch, so a 303 is followed by the fetch and its target applied as a region
// set for the wrong page; the answer there is the navigate directive instead.
//
// The target is refused unless a browser can follow it without running script.
// A redirect target is commonly a return path taken from the request, and the
// update runtime hands it to location.assign, which executes a javascript: URL
// rather than navigating to it.
func Redirect(r *fasthttp.RequestCtx, url string, status int) {
	if !safeurl.Navigable(url) {
		WriteProblem(r, InternalServerError(errUnsafeNavigation))
		return
	}
	if WantsUpdate(r) {
		WriteUpdateNavigate(r, url)
		return
	}
	// Not r.Redirect. That resolves the target into an absolute URI built from
	// the Host header and from whether this process terminated TLS, so an
	// application behind a TLS-terminating proxy answers https requests with
	// Location: http://…, and every redirect takes the browser to plaintext
	// before the proxy sends it back. http.Redirect never does this: it keeps a
	// relative target relative, and the browser resolves it against the URL it
	// actually used. Matching that is both the safer answer and the one that
	// makes the two transports send the same header.
	location := redirectLocation(string(r.Path()), url)
	r.Response.Header.Set("Location", location)
	// The short body is what a user agent that does not understand the status
	// falls back to, and http.Redirect writes it under the same conditions: the
	// content type on GET and HEAD, the body on GET, and each only when the
	// caller named no content type of its own.
	untyped := untypedResponse(r)
	method := string(r.Method())
	if untyped && (method == fasthttp.MethodGet || method == fasthttp.MethodHead) {
		r.Response.Header.SetContentType("text/html; charset=utf-8")
	}
	r.SetStatusCode(status)
	if untyped && method == fasthttp.MethodGet {
		// Two newlines, which looks like a typo and is not: http.Redirect ends
		// the markup with one and then writes it through Fprintln, which adds
		// the second. The point of this half is that a handler redirecting sends
		// the same bytes whichever transport carried it, and that includes the
		// bytes nobody meant.
		_, _ = r.WriteString("<a href=\"" + html.EscapeString(location) + "\">" +
			fasthttp.StatusMessage(status) + "</a>.\n\n")
	}
}

// defaultContentType is what this transport reports for a response nobody set a
// type on.
//
// It has to be compared against, because the question http.Redirect asks —
// whether the caller named a type of its own — has no direct answer here: the
// header reads back as this default rather than as absent, so a check for an
// empty one is never true and the fallback body was never written. A caller
// that set this exact value deliberately and then redirected gets the body it
// would have got on the other transport, which is the direction to be wrong in.
const defaultContentType = "text/plain; charset=utf-8"

func untypedResponse(r *fasthttp.RequestCtx) bool {
	current := r.Response.Header.ContentType()
	return len(current) == 0 || string(current) == defaultContentType
}

// redirectLocation resolves a target the way http.Redirect does, so a handler
// that redirects sends one header whichever transport carried it.
//
// A target naming a scheme or a host is already absolute and passes through. A
// relative one is resolved against the request path and stays relative, which
// is what keeps the scheme the client chose rather than the scheme this process
// happens to be speaking.
func redirectLocation(requestPath, target string) string {
	parsed, err := url.Parse(target)
	if err != nil || parsed.Scheme != "" || parsed.Host != "" {
		return escapeNonASCII(target)
	}
	if requestPath == "" {
		requestPath = "/"
	}
	if target == "" || target[0] != '/' {
		directory, _ := path.Split(requestPath)
		target = directory + target
	}
	query := ""
	if mark := strings.Index(target, "?"); mark != -1 {
		target, query = target[:mark], target[mark:]
	}
	// path.Clean drops a trailing slash, and the distinction matters to a route
	// table where a directory and a document are different resources.
	trailing := strings.HasSuffix(target, "/")
	target = path.Clean(target)
	if trailing && !strings.HasSuffix(target, "/") {
		target += "/"
	}
	return escapeNonASCII(target + query)
}

// escapeNonASCII percent-encodes the bytes a header field may not carry. A
// header is bytes, and a path taken from a request can hold anything.
func escapeNonASCII(value string) string {
	ascii := true
	for index := 0; index < len(value); index++ {
		if value[index] >= 0x80 {
			ascii = false
			break
		}
	}
	if ascii {
		return value
	}
	var escaped strings.Builder
	for index := 0; index < len(value); index++ {
		if b := value[index]; b >= 0x80 {
			escaped.WriteByte('%')
			escaped.WriteByte("0123456789ABCDEF"[b>>4])
			escaped.WriteByte("0123456789ABCDEF"[b&0x0f])
		} else {
			escaped.WriteByte(b)
		}
	}
	return escaped.String()
}

// RedirectSeeOther is the form an action handler wants: 303, so a reload does
// not repost what the handler just applied.
func RedirectSeeOther(r *fasthttp.RequestCtx, url string) {
	Redirect(r, url, fasthttp.StatusSeeOther)
}

// QueryValue reads one query parameter, and reports whether it was present.
//
// A handler binding its whole input takes Parse instead. This is for the
// one-value case, where declaring a type costs more than it explains — and it
// exists so that case does not have to reach into the request itself.
func QueryValue(r *fasthttp.RequestCtx, key string) (string, bool) {
	return fasthttpbind.QueryValue(r, key)
}

// FormValue reads one submitted form field, on the same terms as QueryValue.
func FormValue(r *fasthttp.RequestCtx, key string) string {
	values, err := fasthttpbind.ParseFormMap(r)
	if err != nil {
		return ""
	}
	return values[key]
}

// IsBot reports whether the request came from a client that will not run the
// boundary runtime, so WriteHTMLChain should render the settled document rather
// than committing fallbacks it can never replace.
//
// The verdict comes from the shared classifier over the shared token list, so
// this half and the other reach the same answer about the same crawler. What
// differs is only where the two values it consults come from: the net/http half
// reads the configuration off the request context, and this one reads what that
// half published, because no configuration is bound on this transport yet.
func IsBot(r *fasthttp.RequestCtx) bool {
	if r == nil {
		return false
	}
	settings := pwruntime.ResolvedBotSettings()
	if !settings.Enabled {
		return false
	}
	return botdetect.Classify(string(r.Request.Header.UserAgent()), settings.UserAgents)
}

// PathValue reads one path parameter, which the router stored when it matched.
//
// It is here rather than being a method on the request value because this
// transport has no path routing of its own: the value comes from whatever
// router ran, and a generated decoder calling one function works with any of
// them. The net/http half has the same entry for the same reason.
func PathValue(r *fasthttp.RequestCtx, key string) string {
	return fasthttpbind.PathValue(r, key)
}

// QueryValues is the request's query string split once into raw key=value
// spans, in wire order — the same alias the net/http half holds, so a decoder
// moved between transports keeps its type.
type QueryValues = fasthttpbind.QueryValues

// Queries returns the parsed query, for a decoder reading several
// parameters from one request.
//
// Since system:tinybind v0.5.27 it goes through the module rather than
// QueryArgs: the driver's parser admits pairs url.ParseQuery drops, and the
// two runtimes then bound different values for a query the client chose. One
// parser is what makes the answer one.
func Queries(r *fasthttp.RequestCtx) QueryValues { return fasthttpbind.Queries(r) }

// QueryLookup reads one parameter from a parsed query, reporting whether it was
// present. Presence and emptiness are different answers: a flag parameter
// arrives with no value at all.
func QueryLookup(query QueryValues, key string) (string, bool) {
	return fasthttpbind.QueryLookup(query, key)
}

// QueryLookupAll reads every value of one repeated parameter, in URL order,
// which is the array spelling an urlencoded form submits for a checkbox group.
// It is the accessor a generated decoder reads a string[] input through.
func QueryLookupAll(query QueryValues, key string) []string {
	return fasthttpbind.QueryLookupAll(query, key)
}

// WantsLive reports whether this request asked for deliveries instead of a
// document, which is what a page renders differently for.
//
// It is the counterpart of the net/http half's predicate, and the reason both
// exist is this one: the mode arrives in a header, and a page that read the
// header itself would be a page only one transport could serve.
func WantsLive(r *fasthttp.RequestCtx) bool {
	if r == nil {
		return false
	}
	return string(r.Request.Header.Peek(ResponseModeHeader)) == LiveResponseMode
}
