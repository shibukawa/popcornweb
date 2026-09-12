package pathpattern

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// The CSRF middleware and the authentication guard both decide what to protect
// by matching an include list, so a path that slips past a pattern is not merely
// unmatched — it is unprotected. These two shapes used to slip past.

func TestATrailingSlashStaysInsideAnInclude(t *testing.T) {
	patterns, err := Compile([]string{"/admin/delete"})
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/admin/delete", "/admin/delete/"} {
		if !MatchAny(patterns, path) {
			t.Errorf("%q did not match the pattern that names it", path)
		}
	}
	// The normalization is one trailing slash, not a free ride for anything
	// deeper.
	for _, path := range []string{"/admin/delete/extra", "/admin", "/admin/deleted"} {
		if MatchAny(patterns, path) {
			t.Errorf("%q matched a pattern that does not name it", path)
		}
	}
}

func TestASubtreePatternStillHandlesATrailingSlash(t *testing.T) {
	patterns, err := Compile([]string{"/admin/**"})
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/admin", "/admin/", "/admin/delete", "/admin/delete/"} {
		if !MatchAny(patterns, path) {
			t.Errorf("%q did not match /admin/**", path)
		}
	}
}

// A doubled slash is a path whose target depends on who resolves it, so no
// policy can decide about it. Callers treat a refusal here as a refusal of the
// request, which is the direction that cannot leave a route unprotected.
func TestADoubledSlashIsNotCanonical(t *testing.T) {
	for _, target := range []string{"//admin/delete", "/admin//delete", "/a//"} {
		request := httptest.NewRequest(http.MethodPost, "http://example.test"+target, nil)
		if path, ok := CanonicalPath(request); ok {
			t.Errorf("CanonicalPath(%q) = %q, true; want a refusal", target, path)
		}
	}
}

// The ordinary shapes stay canonical, including the directory form.
func TestOrdinaryPathsStayCanonical(t *testing.T) {
	for _, target := range []string{"/", "/admin", "/admin/", "/admin/delete", "/a/b/c"} {
		request := httptest.NewRequest(http.MethodPost, "http://example.test"+target, nil)
		if _, ok := CanonicalPath(request); !ok {
			t.Errorf("CanonicalPath(%q) refused an ordinary path", target)
		}
	}
}

// Exclusions take precedence over an including subtree, including the
// equivalent trailing-slash path form.
func TestExcludeAlsoSeesTheTrailingSlash(t *testing.T) {
	include, err := Compile([]string{"/public/**"})
	if err != nil {
		t.Fatal(err)
	}
	exclude, err := Compile([]string{"/public/health"})
	if err != nil {
		t.Fatal(err)
	}
	if Protected(include, exclude, "/public/health/") {
		t.Error("an excluded trailing-slash path remained protected")
	}
	if !Protected(include, exclude, "/public/memos") {
		t.Error("the including subtree stopped protecting a non-excluded path")
	}
}
