package pathpattern

import (
	"net/http/httptest"
	"testing"
)

func TestPatternMatching(t *testing.T) {
	cases := []struct {
		pattern string
		path    string
		want    bool
	}{
		{"/account", "/account", true},
		{"/account", "/account/settings", false},
		{"/users/*/settings", "/users/42/settings", true},
		{"/users/*/settings", "/users//settings", false},
		{"/users/*/settings", "/users/42/43/settings", false},
		{"/admin/**", "/admin", true},
		{"/admin/**", "/admin/users", true},
		{"/admin/**", "/admin/users/42", true},
		{"/admin/**", "/administration", false},
		{"/**", "/anything/at/all", true},
	}
	for _, c := range cases {
		compiled, err := compileOne(c.pattern)
		if err != nil {
			t.Fatalf("compile %q: %v", c.pattern, err)
		}
		if got := compiled.Match(c.path); got != c.want {
			t.Errorf("%q matches %q = %v, want %v", c.pattern, c.path, got, c.want)
		}
	}
}

func TestPatternCompilationRejectsUnsupportedForms(t *testing.T) {
	for _, value := range []string{
		"account", "/admin/**/users", "/users/*id/settings", "/a//b", "/a/../b", "/a?x=1", "/a#b",
	} {
		if _, err := compileOne(value); err == nil {
			t.Errorf("pattern %q was accepted", value)
		}
	}
}

func TestCanonicalPathRejectsAmbiguousRequests(t *testing.T) {
	ok := httptest.NewRequest("GET", "/admin/users", nil)
	if path, valid := CanonicalPath(ok); !valid || path != "/admin/users" {
		t.Fatalf("canonical path = %q valid=%v", path, valid)
	}
	// An encoded separator would let a request match a shorter pattern than
	// the path the router finally dispatches.
	encoded := httptest.NewRequest("GET", "/admin%2Fusers", nil)
	if _, valid := CanonicalPath(encoded); valid {
		t.Fatal("encoded separator was accepted")
	}
}
