package pwruntime

import "testing"

// What the Accept header of each kind of request looks like. The navigation
// values name text/html first and fall back to anything; the image value names
// image types first and puts anything last at a low weight, which is the shape
// a browser gives an icon it fetches for itself.
const (
	navigationAccept = "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8"
	imageAccept      = "image/webp,image/avif,image/png,image/svg+xml,image/*;q=0.8,*/*;q=0.5"
)

func TestDocumentRequestSeparatesPagesFromEverythingElse(t *testing.T) {
	for _, testCase := range []struct {
		name          string
		dest, accept  string
		wantsDocument bool
	}{
		{"a navigation", "document", navigationAccept, true},
		{"a destination alone", "document", "", true},
		{"a destination in any case, padded", "  Document ", "*/*", true},
		{"an Accept alone, as the browsers before Fetch Metadata send it", "", navigationAccept, true},
		{"text/html as the only type", "", "text/html", true},
		{"text/html in any case, with a weight", "", "Text/HTML;q=0.9", true},
		{"text/html anywhere in the list", "", "application/json, text/html;q=0.1", true},

		{"an icon fetch", "image", imageAccept, false},
		{"an icon fetch that names no destination", "", imageAccept, false},
		{"a tool that never said what it wanted", "", "*/*", false},
		{"a request that sent nothing", "", "", false},
		{"a script's fetch", "empty", "application/json", false},
		{"a stylesheet", "style", "text/css,*/*;q=0.1", false},
		{"a type that only begins like HTML", "", "text/htmlx", false},
		{"a wildcard over the text types", "", "text/*", false},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			if got := DocumentRequest(testCase.dest, testCase.accept); got != testCase.wantsDocument {
				t.Errorf("DocumentRequest(%q, %q) = %v, want %v",
					testCase.dest, testCase.accept, got, testCase.wantsDocument)
			}
		})
	}
}

// The browser runtime's own requests are fetches, so they name neither a
// document destination nor text/html, and they are still answered with markup
// the runtime places. Counting them is what keeps a session that ended under an
// open page from answering the next partial update with a bare refusal instead
// of the navigation to the login that the runtime turns a redirect into.
func TestAnnouncesPageCountsTheRuntimesOwnRequests(t *testing.T) {
	for _, testCase := range []struct {
		name                                     string
		dest, accept, updateRender, responseMode string
		announces                                bool
	}{
		{"a navigation", "document", navigationAccept, "", "", true},
		{"a partial update", "empty", "*/*", "fragment", "", true},
		{"a live delivery", "empty", "text/event-stream", "", "stream", true},
		{"either runtime header alone", "", "", " x ", "", true},

		{"an icon fetch", "image", imageAccept, "", "", false},
		{"nothing at all", "", "", "", "", false},
		{"headers that are present and empty", "empty", "*/*", "", "", false},
		{"runtime headers that are only whitespace", "empty", "*/*", "  ", "\t", false},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			got := AnnouncesPage(testCase.dest, testCase.accept, testCase.updateRender, testCase.responseMode)
			if got != testCase.announces {
				t.Errorf("AnnouncesPage(%q, %q, %q, %q) = %v, want %v", testCase.dest, testCase.accept,
					testCase.updateRender, testCase.responseMode, got, testCase.announces)
			}
		})
	}
}

// The CSRF check and the login redirect ask the same question of a request, so
// they must not be able to answer it differently. CSRFRendersHTML and
// CSRFHTMLRequest are the names the check kept; what they decide is decided
// here.
func TestTheCSRFPredicatesAreTheSharedOnes(t *testing.T) {
	for _, dest := range []string{"", "document", "image", "empty"} {
		for _, accept := range []string{"", "*/*", navigationAccept, imageAccept} {
			for _, runtimeHeader := range []string{"", "x"} {
				if got, want := CSRFHTMLRequest(dest, accept), DocumentRequest(dest, accept); got != want {
					t.Errorf("CSRFHTMLRequest(%q, %q) = %v, DocumentRequest says %v", dest, accept, got, want)
				}
				if got, want := CSRFRendersHTML(dest, accept, runtimeHeader, ""), AnnouncesPage(dest, accept, runtimeHeader, ""); got != want {
					t.Errorf("CSRFRendersHTML(%q, %q, %q) = %v, AnnouncesPage says %v", dest, accept, runtimeHeader, got, want)
				}
			}
		}
	}
}
