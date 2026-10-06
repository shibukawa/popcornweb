package pwruntime

import (
	"io"
	"strings"

	"github.com/shibukawa/tinybind-go/htmlbind"
)

// The scope catalog is every scoped script this render could require, as owner
// and URL pairs.
//
// It is a catalog and never a mount list, which is the distinction the whole
// design turns on. Assets deliberately reports what a composition could need,
// including a component below a slot that never rendered, so mounting from it
// would start a lifecycle for markup that is not on screen. What decides the
// mount list is the DOM: system:tinybind v0.5.7 writes the declaring component
// onto every rendered instance as static markup, so the client scans for markers
// and looks each one up here.
//
// That marker is emitted rather than the instance attribute because an ordinary
// component call opens no update boundary at all — a Counter rendered twenty
// times inside a page carries no instance id and appears in no manifest, which
// is precisely the case a per-instance lifecycle exists for. Being static also
// means it survives a render that collects nothing and a first load, where the
// client holds no manifest because the manifest is a header the client sends
// back.
//
// An earlier revision of this file sent the composition chain instead, outermost
// first, and the client diffed it. That worked only for chain members, which
// have one instance each by construction. The marker supersedes it and reaches
// every instance of every declaration.

//
// It lives in this leaf rather than in either runtime because the catalog is a
// fact about a composition and not about a transport. While it was the net/http
// runtime's alone, the other build rendered the same components and told the
// client nothing about them on any response, so no component script ever
// started there.

// ScopeChainAttribute names the attribute on the stream end marker, and
// ScopeChainHeader the response header a navigation delta carries.
//
// Two channels because the two responses differ in what this framework owns: the
// document marker is written here, and a delta body is written by the module and
// has no field to add one to.
const ScopeChainAttribute = "scopes"

// ScopeChainHeader carries the chain on a navigation delta.
const ScopeChainHeader = "Pw-Scopes"

// ScopeEntry is one declaration that has a scoped script.
type ScopeEntry struct {
	// Owner is the package-qualified declaration identity, which is the same
	// string the rendered marker carries. It was the bare declared name until
	// v0.5.7 normalized the two identity spaces into one; a short name could not
	// be joined against a render without colliding across packages.
	Owner string
	// URL is the module carrying its setup export.
	URL string
}

// ScopeCatalog returns every scoped script the composition could require,
// encoded for the wire.
func ScopeCatalog(wrappers []htmlbind.Wrapper, leaf htmlbind.Fragment) string {
	return EncodeScopeChain(ScopeEntries(wrappers, leaf))
}

// ScopeEntries returns every scoped script the composition could require.
//
// Each layer's own set is read rather than the merged one only because
// MergeAssets deduplicates by content: two declarations with identical bytes
// share one file and would collapse to one entry, and each still needs its own
// owner. Order carries no meaning here — the DOM decides what mounts and in what
// order, because an ancestor's marker is found before its descendant's.
func ScopeEntries(wrappers []htmlbind.Wrapper, leaf htmlbind.Fragment) []ScopeEntry {
	var catalog []ScopeEntry
	for _, wrapper := range wrappers {
		catalog = AppendScopeEntries(catalog, wrapper.Assets())
	}
	return AppendScopeEntries(catalog, leaf.Assets())
}

// AppendScopeEntries adds the scoped scripts among one layer's assets.
func AppendScopeEntries(chain []ScopeEntry, assets []htmlbind.Asset) []ScopeEntry {
	for _, asset := range assets {
		// An empty scope is document lifetime: the head tag loads it once and
		// nothing ever releases it, which is what a head contribution has always
		// been and needs no chain entry.
		if asset.Scope == "" || asset.Type != htmlbind.AssetTypeScript {
			continue
		}
		// One owner appears once. A layer reachable from two others contributes
		// the same declaration twice, and the client looks an owner up rather
		// than walking the list.
		if scopeChainHolds(chain, asset.Scope) {
			continue
		}
		chain = append(chain, ScopeEntry{Owner: asset.Scope, URL: asset.URL})
	}
	return chain
}

func scopeChainHolds(chain []ScopeEntry, owner string) bool {
	for _, entry := range chain {
		if entry.Owner == owner {
			return true
		}
	}
	return false
}

// DocumentScopesElement is the marker a document that arrived whole carries its
// scope catalog in. It takes the boundary prefix like every other element this
// framework emits.
const DocumentScopesElement = UpdateAttributePrefix + "-scopes"

// WriteDocumentScopes hands the catalog to a document that was not streamed.
//
// The client mounts a component's script only when it is told which module
// belongs to which owner. A streamed document says so on its end marker and a
// navigation delta in a header, and a buffered document said nothing at all: a
// page opened directly rendered its components, loaded their modules, and never
// called a setup, while the same page reached through the runtime did. Every
// project with partial updates on renders buffered, so that was the first load
// of every page.
//
// It is an element of its own rather than the end marker, because that marker
// also announces that a stream finished and may open a live connection, and
// neither is true of a document that streamed nothing.
func WriteDocumentScopes(w io.Writer, scopes string) error {
	if scopes == "" {
		return nil
	}
	_, err := io.WriteString(w, `<`+DocumentScopesElement+` value="`+htmlbind.Escape(scopes)+`"></`+DocumentScopesElement+`>`)
	return err
}

// EncodeScopeChain writes the catalog as owner and URL pairs.
//
// The grammar is the one the manifest attribute already uses — comma between
// entries, colon within one — so a reader of either learns one shape. An owner
// is a generated identifier and a URL is a generation-time path, so neither can
// hold a separator; an entry that somehow did is dropped here rather than
// producing one the client would mis-split.
func EncodeScopeChain(chain []ScopeEntry) string {
	if len(chain) == 0 {
		return ""
	}
	pairs := make([]string, 0, len(chain))
	for _, entry := range chain {
		if entry.Owner == "" || entry.URL == "" {
			continue
		}
		if strings.ContainsAny(entry.Owner, ",:") || strings.ContainsAny(entry.URL, ",") {
			continue
		}
		pairs = append(pairs, entry.Owner+":"+entry.URL)
	}
	return strings.Join(pairs, ",")
}
