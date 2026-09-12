package pw

import (
	"strings"
	"testing"

	"github.com/shibukawa/tinybind-go/htmlbind"
)

func scriptAsset(scope, url string) htmlbind.Asset {
	return htmlbind.Asset{ID: url, Type: htmlbind.AssetTypeScript, URL: url, Scope: scope}
}

// The catalog is what a client looks an owner up in. Order carries no meaning to
// the mount — the DOM decides that — but composition order is what the layers
// are walked in, and one declaration appears once.
func TestScopeCatalogIsCompositionOrder(t *testing.T) {
	chain := appendScopeEntries(nil, []htmlbind.Asset{scriptAsset("app.doc.Document", "/d.js")})
	chain = appendScopeEntries(chain, []htmlbind.Asset{scriptAsset("app.layout.Layout", "/l.js")})
	chain = appendScopeEntries(chain, []htmlbind.Asset{scriptAsset("app.page.Page", "/p.js")})

	if got := encodeScopeChain(chain); got != "app.doc.Document:/d.js,app.layout.Layout:/l.js,app.page.Page:/p.js" {
		t.Errorf("catalog = %q, want each declaration once, in composition order", got)
	}
}

// An empty scope is document lifetime: the head tag loads it once and nothing
// ever releases it, so it is not a chain member at all. A stylesheet is not one
// either, whatever its scope says.
func TestScopeCatalogCarriesOnlyScopedScripts(t *testing.T) {
	chain := appendScopeEntries(nil, []htmlbind.Asset{
		{ID: "a", Type: htmlbind.AssetTypeScript, URL: "/head.js"},
		{ID: "b", Type: htmlbind.AssetTypeStyle, URL: "/c.css", Scope: "app.counter.Counter"},
		scriptAsset("app.counter.Counter", "/counter.js"),
	})

	if got := encodeScopeChain(chain); got != "app.counter.Counter:/counter.js" {
		t.Errorf("chain = %q, want the scoped script alone", got)
	}
}

// One declaration reachable from two layers appears once. The client looks an
// owner up rather than walking the list, so a repeat buys nothing and a second
// URL for one owner would be ambiguous.
func TestScopeCatalogHoldsOneOwnerOnce(t *testing.T) {
	chain := appendScopeEntries(nil, []htmlbind.Asset{scriptAsset("app.shared.Shared", "/s.js")})
	chain = appendScopeEntries(chain, []htmlbind.Asset{scriptAsset("app.shared.Shared", "/s.js")})

	if got := encodeScopeChain(chain); got != "app.shared.Shared:/s.js" {
		t.Errorf("chain = %q, want one entry", got)
	}
}

// The grammar is the manifest's, so a value that could be mis-split is dropped
// rather than encoded into something the client would read as two entries.
func TestScopeCatalogDropsAnEntryItCannotSpell(t *testing.T) {
	chain := []scopeEntry{
		{Owner: "Good", URL: "/good.js"},
		{Owner: "Bad:Name", URL: "/b.js"},
		{Owner: "Comma", URL: "/a,b.js"},
		{Owner: "", URL: "/empty.js"},
	}

	if got := encodeScopeChain(chain); got != "Good:/good.js" {
		t.Errorf("chain = %q, want only the entry that can be spelled", got)
	}
}

// A URL holds colons, so only the first separates. This is the encoder's half of
// the split the client performs.
func TestScopeCatalogKeepsAURLsOwnColons(t *testing.T) {
	const url = "https://assets.example.test/public/generated/x.script.abc.js"
	got := encodeScopeChain([]scopeEntry{{Owner: "app.x.Comp", URL: url}})
	if _, gotURL, found := strings.Cut(got, ":"); !found || gotURL != url {
		t.Errorf("chain = %q, want the URL to survive intact", got)
	}
}

// The marker carries the chain on every state, unlike the manifest. A page that
// will never change again still has scripts to start: a lifecycle runs on a page
// that is merely on screen.
func TestStreamEndCarriesTheScopeChainOnEveryState(t *testing.T) {
	for _, state := range []string{streamEndFinal, streamEndLive} {
		var out strings.Builder
		if err := writeStreamEnd(&out, state, nil, "app.page.Page:/p.js"); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out.String(), `scopes="app.page.Page:/p.js"`) {
			t.Errorf("state %s: marker carries no scope chain: %s", state, out.String())
		}
	}
}

// A composition with no scoped script writes no attribute, so a project using
// none produces the marker it always did.
func TestStreamEndOmitsAnEmptyScopeChain(t *testing.T) {
	var out strings.Builder
	if err := writeStreamEnd(&out, streamEndFinal, nil, ""); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "scopes=") {
		t.Errorf("marker carries an empty scope chain: %s", out.String())
	}
}
