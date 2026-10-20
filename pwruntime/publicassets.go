package pwruntime

// PublicAssetSettings is the static asset configuration.
//
// It is in the shared leaf for the reason the security headers and the CSRF
// configuration are: both chains read it, none of it names a transport, and one
// declaration means one set of binder tags.

// PublicAssetConfig controls the framework-owned static asset endpoint.
type PublicAssetSettings struct {
	Enabled   bool   `default:"true"`
	Mount     string `default:"/public" dependon:".enabled"`
	ReadLocal bool   `default:"false" dependon:".enabled"`
	LocalRoot string `default:"dist/public" dependon:".enabled"`
	// SVGSandbox adds a sandbox Content-Security-Policy to an image/svg+xml
	// response. An SVG is the one served image that executes, and it executes
	// in the application origin on direct navigation, so this is on by default
	// and is off only for a project deliberately serving an interactive SVG
	// through object, embed, or a link.
	SVGSandbox bool `default:"true" dependon:".enabled"`
}
