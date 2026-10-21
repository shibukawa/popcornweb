package pwruntime

import "strings"

// DocumentRequest reports whether a request is the kind a browser makes to open
// a page, given its Sec-Fetch-Dest and Accept header values.
//
// A navigation says so one of two ways. Every current browser sends document as
// the destination, and the Accept a browser sends for one names text/html, which
// is also how the browsers that predate Fetch Metadata say it and how a script
// asks for markup back. A request that says neither is not a page: an icon, an
// image, a stylesheet, a script, an API call, or the generic */* of a tool that
// never said what it wanted.
//
// Both transports reach this through AnnouncesPage, so the answer cannot drift.
func DocumentRequest(secFetchDest, accept string) bool {
	if strings.EqualFold(strings.TrimSpace(secFetchDest), "document") {
		return true
	}
	for remainder := accept; remainder != ""; {
		var mediaRange string
		mediaRange, remainder, _ = strings.Cut(remainder, ",")
		mediaType, _, _ := strings.Cut(mediaRange, ";")
		if strings.EqualFold(strings.TrimSpace(mediaType), "text/html") {
			return true
		}
	}
	return false
}

// AnnouncesPage reports whether a request says, in its own headers, that what
// it is answered with will be shown to a person as a page. It takes the four
// header values it reads — Sec-Fetch-Dest, Accept, UpdateRenderHeader, and
// ResponseModeHeader — rather than a reader, so a transport whose header values
// are not strings converts only what is there.
//
// A document request says so itself, through DocumentRequest. The browser
// runtime's own requests do not: a partial update and a live delivery are
// fetches, so they arrive with no text/html in Accept and a Sec-Fetch-Dest of
// empty. They are still answered with markup the runtime places, and a redirect
// they receive is turned into a navigation by the runtime instead of being
// followed, so for every question about what a page-shaped answer may be, they
// count.
//
// It is the question an answer that only a person can act on asks first. A login
// redirect is one. A browser fetching an icon, an image, or a script follows it
// all the same, and the request that follows has already reached the login
// endpoint and done what a visit there does — including using up what that
// endpoint hands out once — before it ends on a login page nobody sees.
func AnnouncesPage(secFetchDest, accept, updateRender, responseMode string) bool {
	return DocumentRequest(secFetchDest, accept) ||
		strings.TrimSpace(updateRender) != "" || strings.TrimSpace(responseMode) != ""
}
