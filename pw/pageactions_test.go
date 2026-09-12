package pw

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/shibukawa/popcornweb/pwruntime"
	"github.com/shibukawa/tinybind-go/htmlbind"
)

// publishing installs one route's actions for the duration of a test.
func publishing(t *testing.T) {
	t.Helper()
	t.Cleanup(pwruntime.ResetPageActions)
	pwruntime.ResetPageActions()
	pwruntime.RegisterPageActions("GET /users/{id}",
		pwruntime.PageAction{Name: "Rename", Path: "/_action/abc/Rename"},
		pwruntime.PageAction{Name: "Retire", Path: "/_action/def/Retire"})
}

func matched(pattern string) *http.Request {
	request := httptest.NewRequest(http.MethodGet, "/users/42", nil)
	request.Pattern = pattern
	return request
}

// The set travels with the document, because a component script calling a
// server function has no element to read an address off and cannot compute one:
// the address holds a digest of the declaring directory.
func TestPageActionsAreContributedForTheRouteThatMatched(t *testing.T) {
	publishing(t)
	nodes := pageActionHeadNodes(matched("GET /users/{id}"))
	if len(nodes) != 1 {
		t.Fatalf("head nodes = %d, want the one meta", len(nodes))
	}
	// A malformed node would fail the render before the first byte, so the node
	// has to be writable to be worth contributing.
	tags, err := htmlbind.RenderHeadNodes(nodes)
	if err != nil {
		t.Fatalf("the contributed node is not writable: %v", err)
	}
	rendered := strings.Join(tags, "")
	for _, want := range []string{`name="pw-actions"`, "Rename", "/_action/abc/Rename", "Retire"} {
		if !strings.Contains(rendered, want) {
			t.Errorf("the node is missing %q: %s", want, rendered)
		}
	}
	// Inert: it is attribute-escaped metadata rather than a script, which is
	// what lets a strict CSP with no inline allowance carry it.
	if strings.Contains(rendered, "<script") {
		t.Errorf("the set reached the head as script: %s", rendered)
	}
}

// The route decides the set, so a page whose package exports no handler pays no
// bytes for saying so.
func TestPageActionsAreAbsentWhereTheRoutePublishesNone(t *testing.T) {
	publishing(t)
	for _, testCase := range []struct{ name, pattern string }{
		{"a route with no actions", "GET /{$}"},
		// A request that matched no pattern is the same answer rather than a
		// special case: a handler registered without one, and a render driven
		// straight from a test, both look like this.
		{"a request that matched nothing", ""},
	} {
		if nodes := pageActionHeadNodes(matched(testCase.pattern)); len(nodes) != 0 {
			t.Errorf("%s contributed %d nodes", testCase.name, len(nodes))
		}
	}
}
