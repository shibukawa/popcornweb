package pw

import (
	"io"

	"github.com/shibukawa/popcornweb/pwruntime"
	"github.com/shibukawa/tinybind-go/htmlbind"
)

// The scope catalog is the shared leaf's: it describes a composition rather
// than a transport, and the other build has to write the same marker and the
// same header from the same rules. What stays here is this runtime's spelling
// of those names.

// scopeChainAttribute names the attribute on the stream end marker.
const scopeChainAttribute = pwruntime.ScopeChainAttribute

// ScopeChainHeader carries the chain on a navigation delta.
const ScopeChainHeader = pwruntime.ScopeChainHeader

// scopeEntry is one declaration that has a scoped script.
type scopeEntry = pwruntime.ScopeEntry

func scopeCatalog(wrappers []HTMLWrapper, leaf HTMLFragment) []scopeEntry {
	return pwruntime.ScopeEntries(wrappers, leaf)
}

func appendScopeEntries(chain []scopeEntry, assets []htmlbind.Asset) []scopeEntry {
	return pwruntime.AppendScopeEntries(chain, assets)
}

func encodeScopeChain(chain []scopeEntry) string { return pwruntime.EncodeScopeChain(chain) }

func writeDocumentScopes(w io.Writer, scopes string) error {
	return pwruntime.WriteDocumentScopes(w, scopes)
}
