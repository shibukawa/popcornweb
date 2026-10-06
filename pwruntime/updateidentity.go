package pwruntime

import (
	"runtime/debug"
	"sync"
)

// These are fixed rather than configured. They are contracts between this
// framework and the runtime it ships — the header namespace, the attribute
// prefix, the installed name, and the endpoint prefix all reach the browser as
// one configuration object, and a deployment changing one would be describing a
// framework it is not running.
//
// They are here rather than on either runtime because the document a browser
// receives is the same document whichever transport wrote it, and the settings
// reduction that describes it is published by whichever half parsed the
// configuration.
const (
	// UpdateHeaderPrefix yields Pw-Render, Pw-Manifest, and Pw-Build.
	UpdateHeaderPrefix = "Pw"
	// UpdateRenderHeader is the request header every update request names its
	// mode in, and a document request leaves out.
	UpdateRenderHeader = UpdateHeaderPrefix + "-Render"
	// UpdateAttributePrefix names the boundary attributes generation writes and
	// the placeholder element the render option spells, so one document holds
	// one spelling rather than two.
	//
	// It is the module's default rather than this framework's brand because
	// routetree compiles a page tree's templates without the prefix option, so
	// branding it here would split a document's naming in exactly the way the
	// option exists to prevent. internal/pwgen names the same value.
	UpdateAttributePrefix = "tb"
	// UpdateGlobalName is the browser namespace the client update API installs.
	UpdateGlobalName = "popcornweb"
	// UpdatePathPrefix is where the framework's own endpoints answer.
	UpdatePathPrefix = "/_pw"
)

// UpdateBuildID identifies the binary that rendered a page.
//
// It answers the same question the live delivery stream's version does: was the
// page asking rendered by this build? A page from another one holds client
// state this binary cannot vouch for — a template it does not have, a runtime
// that renders differently — and none of that is visible in a validator.
//
// The two differ on an unstamped binary, and the difference is deliberate. Live
// delivery reports nothing there, which disables its check rather than inventing
// a value that would differ per process and reload every client on every
// restart. An update falls back to the module's per-process identity instead,
// which costs a complete document after a restart and never a wrong delta. A
// frozen screen is worse than a re-transferred page.
var UpdateBuildID = sync.OnceValue(func() string {
	if info, ok := debug.ReadBuildInfo(); ok {
		for _, setting := range info.Settings {
			if setting.Key == "vcs.revision" {
				return setting.Value
			}
		}
	}
	return ""
})
