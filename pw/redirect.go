package pw

import (
	"net/http"

	"github.com/shibukawa/popcornweb/internal/safeurl"
	"github.com/shibukawa/popcornweb/pwruntime"
	tinybind "github.com/shibukawa/tinybind-go"
)

// Redirect sends the browser to another location.
//
// It exists rather than leaving handlers on http.Redirect for two reasons, and
// only one of them is that a stdlib helper taking the writer and the request is
// a call no second transport can follow.
//
// The other is the branch. An update request is a fetch, so a 303 is followed
// by the fetch and its target applied as a region set for the wrong page; the
// answer there is the navigate directive instead. That branch belongs in one
// place rather than in every action handler that redirects.
//
// The target is refused unless a browser can follow it without running script.
// A redirect target is commonly a return path taken from the request, and the
// update runtime hands it to location.assign, which executes a javascript: URL
// rather than navigating to it. Refusing here means an application cannot turn
// its own redirect into script execution by forwarding a parameter it did not
// check — and the check is on both branches, because a handler should not have
// to know which one it took.
func Redirect(w http.ResponseWriter, r *http.Request, url string, status int) {
	if !safeurl.Navigable(url) {
		WriteProblem(w, r, InternalServerError(errUnsafeNavigation))
		return
	}
	if WantsUpdate(r) {
		WriteUpdateNavigate(w, r, url)
		return
	}
	http.Redirect(w, r, url, status)
}

// RedirectSeeOther is the form an action handler wants: 303, so a reload does
// not repost what the handler just applied.
func RedirectSeeOther(w http.ResponseWriter, r *http.Request, url string) {
	Redirect(w, r, url, http.StatusSeeOther)
}

// QueryValue reads one query parameter, and reports whether it was present.
//
// A handler binding its whole input takes api:request-binding instead. This is
// for the one-value case, where declaring a type costs more than it explains —
// and it exists so that case does not have to reach into the request itself,
// which is the read no second transport can follow.
func QueryValue(r *http.Request, key string) (string, bool) {
	return tinybind.QueryValue(r, key)
}

// FormValue reads one submitted form field, on the same terms as QueryValue.
func FormValue(r *http.Request, key string) string {
	values, err := tinybind.ParseFormMap(r)
	if err != nil {
		return ""
	}
	return values[key]
}

// PathValue reads one path parameter the router stored when it matched.
//
// It exists rather than a handler calling r.PathValue because that read is one
// no second transport can follow: fasthttp has no path routing of its own, so
// its path values come from whatever router ran and there is no method on the
// request to call. Routing both through the framework is what lets one
// generated decoder serve either.
func PathValue(r *http.Request, key string) string { return tinybind.PathValue(r, key) }

// QueryValues is the request's query string split once into raw key=value
// spans, in wire order. It is the module's alias, so the value a decoder holds
// is the same type on either transport and the lookups cannot drift apart.
type QueryValues = tinybind.QueryValues

// Queries returns the parsed query, for a decoder reading several parameters
// from one request.
//
// Since system:tinybind v0.5.27 the parse is the module's own rather than
// url.Values: the two transports used to split the same raw query with two
// parsers — url.ParseQuery here, the driver's on fasthttp — and a pair one of
// them drops, a semicolon or a broken percent escape, then bound different
// values for a query the client chose. One parser is what makes the answer one.
func Queries(r *http.Request) QueryValues { return tinybind.Queries(r) }

// QueryLookup reads one parameter from a parsed query, reporting whether it was
// present. Presence and emptiness are different answers: a flag parameter
// arrives with no value at all.
func QueryLookup(query QueryValues, key string) (string, bool) {
	return tinybind.QueryLookup(query, key)
}

// QueryLookupAll reads every value of one repeated parameter, in URL order,
// which is the array spelling an urlencoded form submits for a checkbox group.
// It is the accessor a generated decoder reads a string[] input through.
func QueryLookupAll(query QueryValues, key string) []string {
	return tinybind.QueryLookupAll(query, key)
}

// The returned forms, for code that decides a response without holding a
// writer — a loader a template binds with {val}, or any function whose only
// way out is (T, error).
//
// They are named for their status, like the problem constructors beside them,
// and they are returned the same way: NotFound and SeeOther are both values a
// function hands back rather than writes, and the response path reads the
// intent off whichever it gets.
//
// A redirect has two axes, so there are four:
//
//	                     | method may become GET | method preserved
//	 temporary           | SeeOther              | TemporaryRedirect
//	 permanent           | MovedPermanently      | PermanentRedirect
//
// Both axes matter wherever a POST can reach the code. In a page loader neither
// does much: the render answering it is a GET, and SeeOther and
// TemporaryRedirect are indistinguishable on one. SeeOther is the answer to
// reach for there.
//
// The location is checked and the update branch is taken where the value is
// written rather than here, so a returned redirect and a written one are the
// same redirect.

// SeeOther sends the browser to url with 303, which fetches the target with GET
// whatever the request was, so a reload repeats nothing.
func SeeOther(url string) error { return pwruntime.NewRedirect(url, http.StatusSeeOther) }

// TemporaryRedirect sends the browser to url with 307, keeping the method.
func TemporaryRedirect(url string) error {
	return pwruntime.NewRedirect(url, http.StatusTemporaryRedirect)
}

// MovedPermanently sends the browser to url with 301. Prefer PermanentRedirect
// unless a client you must serve predates it: 301 is ambiguous about the method
// in practice, where 308 is not.
func MovedPermanently(url string) error {
	return pwruntime.NewRedirect(url, http.StatusMovedPermanently)
}

// PermanentRedirect sends the browser to url with 308, keeping the method. It
// is the one to reach for when an address is retired.
func PermanentRedirect(url string) error {
	return pwruntime.NewRedirect(url, http.StatusPermanentRedirect)
}
