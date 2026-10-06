package pw

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"strconv"

	"github.com/shibukawa/popcornweb/pwruntime"
	"github.com/shibukawa/tinybind-go/htmlbind"
)

// HTMLErrorPage resolves the fragment shown in place of a page whose rendering
// failed. It receives the mapped problem rather than the original error, so a
// template can never render a cause the server meant to keep.
//
// It is declared in pwruntime for the reason the document shell is: generated
// registration reaches whichever runtime it imports, and the resolver names no
// transport, so one registration serves both.
type HTMLErrorPage = pwruntime.HTMLErrorPage

// RegisterHTMLErrorPage installs the application's error page resolver. It is
// intended for generated templates code and for an application that wants its
// own presentation; without one, a minimal built-in page is used.
func RegisterHTMLErrorPage(resolve HTMLErrorPage) { pwruntime.RegisterHTMLErrorPage(resolve) }

func registeredHTMLErrorPage() HTMLErrorPage { return pwruntime.RegisteredHTMLErrorPage() }

// writeDocumentEscalation replaces everything below the document shell with an
// error page.
//
// It exists because a boundary that failed with no recover clause has nothing
// to put in its own placeholder: the template said what to show while waiting
// and what to show on success, and nothing about failure. Leaving the committed
// fallback in place would make the page claim forever that it is still loading.
//
// The status went out with the shell, so this changes only what a reader sees.
// The failure reaches an operator through Logger, never through the status line.
//
// options are the failed render's own, so the error page is rendered the way
// the page it replaces was: with the request's context, and with the token an
// unsafe form on it needs.
func writeDocumentEscalation(w io.Writer, problem Problem, options []htmlbind.Option) error {
	// The application error page is handed the sanitized, environment-bounded
	// problem, never the raw internal cause — the same reduction writeHTMLProblem
	// applies. Without it a triggerable boundary failure (a driver error, an
	// internal hostname, a panic message) would be swapped into the already
	// committed document and returned to the client. builtinErrorPage carries
	// Status and Title only, so the fallback branch is unaffected either way.
	problem = publicProblem(sanitizedProblem(problem))
	var body bytes.Buffer
	if resolve := registeredHTMLErrorPage(); resolve != nil {
		fragment := resolve(problem)
		if fragment.Present() {
			if err := htmlbind.Render(&body, fragment, options...); err != nil {
				// The error page is the last thing standing between a reader and
				// a permanent loading state, so its own failure falls back to the
				// built-in rather than propagating.
				body.Reset()
				builtinErrorPage(&body, problem)
			}
		} else {
			builtinErrorPage(&body, problem)
		}
	} else {
		builtinErrorPage(&body, problem)
	}
	// Same framing discipline as a boundary completion: an inert template, then
	// a marker that commits it. A parser inserts an element at its start tag, so
	// a runtime reacting to the template could read one whose content had not
	// arrived. The marker cannot exist before its template is closed.
	if _, err := io.WriteString(w, `<template data-tb-document>`); err != nil {
		return err
	}
	if _, err := body.WriteTo(w); err != nil {
		return err
	}
	_, err := io.WriteString(w, `</template><tb-apply-document></tb-apply-document>`)
	return err
}

// builtinErrorPage writes the fallback presentation. It carries the status and
// its standard title only: everything else about the failure is server-side.
func builtinErrorPage(w io.Writer, problem Problem) {
	status := problem.Status
	if status == 0 {
		status = 500
	}
	title := problem.Title
	if title == "" {
		title = http.StatusText(status)
	}
	_, _ = io.WriteString(w, `<main><h1>`+strconv.Itoa(status)+` `+htmlbind.Escape(title)+`</h1></main>`)
}

// publicProblem bounds what an error page is allowed to say, by environment
// rather than by status or by client.
//
// In development the reader is the person who caused the failure and is about
// to fix it, so the page carries everything the problem does. Anywhere else the
// same page is served to the public, and it says what went wrong without saying
// why. The template is the same in both: the difference is what it is given,
// because a template that decides this itself decides it once and then gets
// copied into an application that meant something else by it.
func publicProblem(problem Problem) Problem {
	if Development() {
		return problem
	}
	return Problem{Status: problem.Status, Title: problem.Title}
}

// acceptsHTML reports whether the client would rather have a page than a
// document, from the header this transport carries it in.
//
// The rule is the shared leaf's, because it is a rule about a header rather
// than about a transport, and the two builds of one application must not
// disagree about which representation a browser asked for.
func acceptsHTML(r *http.Request) bool {
	if r == nil {
		return false
	}
	return pwruntime.AcceptsHTML(r.Header.Get("Accept"))
}

// writeHTMLProblem answers an uncommitted HTML request with the error page and
// its real status.
//
// It exists so the two render branches tell the same story: the streaming one
// can only patch an error page into a response that already said 200, and this
// one can say 500 while showing the reader the same thing.
//
// It renders through the same wrapper chain the failed page used, so the error
// page keeps the document shell instead of arriving as a bare fragment.
func writeHTMLProblem(w http.ResponseWriter, r *http.Request, wrappers []HTMLWrapper, problem Problem) {
	resolve := registeredHTMLErrorPage()
	if resolve == nil {
		writeProblemJSON(w, r, problem)
		return
	}
	problem = sanitizedProblem(problem)
	fragment := resolve(publicProblem(problem))
	if !fragment.Present() {
		writeProblemJSON(w, r, problem)
		return
	}
	addVaryHeader(w.Header(), "Accept")
	// The error page renders through the failed page's own wrapper chain, so it
	// carries whatever that shell carries — a signed-in reader's name in the
	// header of a 500 is the ordinary case, not an unusual one. It reaches this
	// writer instead of WriteHTMLChain, so the policy that chain decides has to
	// be asked for here rather than inherited.
	writeChainCachePolicy(w, r, wrappers, fragment)
	// The chain is the failed page's own, so it is rendered with that page's
	// options. It used to be rendered with none, which a shell that only binds
	// values survives and one holding a sign-out form does not: the render
	// failed for want of a token, and a browser asking for a page was answered
	// with the problem document instead of the application's error page.
	//
	// The context keeps the request's values and drops its cancellation. A
	// request that ran out of time is one of the failures this page reports,
	// and rendering the report under the deadline that just expired would fail
	// it too.
	ctx := requestContext(r)
	config := ConfigContext[HTMLConfig](ctx)
	options := renderOptions(context.WithoutCancel(ctx), config, false,
		chainRenderOptions(config, csrfRenderToken(w, r), csrfDisabled(ctx)))
	var body bytes.Buffer
	if err := htmlbind.RenderChain(&body, wrappers, fragment, options...); err != nil {
		// Never let an error page's own failure recurse into another one.
		LoggerContext(requestContext(r)).Log(requestContext(r), LevelError, "HTML error page render failed", Err(err))
		writeProblemJSON(w, r, problem)
		return
	}
	// The shell's scoped scripts, which an error document has as much use for
	// as any other: the navigation a layout's script drives is still on screen.
	if err := writeDocumentScopes(&body, encodeScopeChain(scopeCatalog(wrappers, fragment))); err != nil {
		LoggerContext(ctx).Log(ctx, LevelError, "document scope catalog write failed", Err(err))
	}
	status := problem.Status
	if status == 0 {
		status = http.StatusInternalServerError
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Length", strconv.Itoa(body.Len()))
	w.WriteHeader(status)
	if _, err := body.WriteTo(w); err != nil {
		LoggerContext(requestContext(r)).Log(requestContext(r), LevelError, "HTML error response write failed", Err(err))
	}
}
