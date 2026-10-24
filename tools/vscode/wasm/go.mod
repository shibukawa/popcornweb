// A module of its own, so `go build ./...` and the TinyGo matrix at the
// repository root never compile the extension's WebAssembly entry.
//
// The tinybind-go version here is the formatter the extension ships and is
// deliberately independent of the version the framework pins: an extension
// release must not require a framework release. decision:formatter-delivery
// carries what that costs and how the delegated path removes it.
//
// It tracks the framework's pin anyway whenever the template syntax moves.
// The v0.3.2 formatter refused a `{val x = f()}` binding, which the repository's
// own pages use, so an editor formatting one of them reported a syntax error
// the project's own pw fmt does not.
module github.com/shibukawa/popcornweb/tools/vscode/wasm

go 1.27.0

require github.com/shibukawa/tinybind-go v0.5.37
