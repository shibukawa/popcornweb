package pwcli

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/shibukawa/tinybind-go/minitoml"
)

const (
	defaultTailwindInput  = "assets/app.css"
	defaultTailwindOutput = "public/generated/app.css"
	defaultMigrationDir   = "migrations"
	defaultIdPConfig      = "devidp.toml"
	defaultDevLogDir      = ".log"
	// defaultIdPPort is what the scaffold pins the development provider to. A
	// reserved port would move on every run, and the issuer it appears in is
	// part of the account identity the scaffolded resolver derives.
	defaultIdPPort = 18080
	// defaultConsolePort sits beside the identity provider's, for the opposite
	// reason: not because anything derives an identity from it, but because a
	// console is bookmarked, and a reserved port would hand out a new address
	// every run.
	defaultConsolePort = 18081
	// defaultImageQuality is libwebp's own default, so a project that sets
	// nothing gets what the encoder considers reasonable rather than a number
	// this framework invented.
	defaultImageQuality = 75
)

// Target compilers recorded by project.toolchain. Projects scaffolded before the
// key existed used TinyGo compatible routing, so that stays the default.
const (
	toolchainTinyGo = "tinygo"
	toolchainGo     = "go"
)

// The corners dev.console.launcher.corner accepts, listed in the order an error
// message names them.
//
// These repeat the names the framework declares, because those live in its
// pwdev half and this is a host build that cannot reference it. The variable
// names below consoleEnviron are duplicated for the same reason.
//
// defaultLauncherCorner is the bottom left because the bottom right is where
// applications put their own floating controls, and the framework taking it
// would collide with the work the launcher exists to help with.
const defaultLauncherCorner = "bottom-left"

var launcherCorners = []string{"bottom-left", "bottom-right", "top-left", "top-right"}

// Project kinds recorded by project.kind. An application builds a binary; a
// package is published as a Go module and imported by one. Every project written
// before the key existed is an application, because it was the only kind there
// was, so the absent key means that rather than an error.
const (
	kindApplication = "application"
	kindPackage     = "package"
)

type tailwindConfig struct {
	Enabled bool
	Input   string
	Output  string
	Minify  bool
}

type migrationConfig struct {
	Dir  string
	Auto bool
}

// seedConfig governs the one place pw applies a dataset on its own: after the
// developer loop rolls a schema back to reach an edited migration, which empties
// the tables below it. Seeding is clear-insert, so it never runs on an ordinary
// rebuild, where it would delete what the developer just typed in.
type seedConfig struct {
	Auto bool
}

// idpConfig selects the development identity provider `pw dev` runs beside the
// application. The port defaults to 0 because pw dev injects the resolved
// issuer, so a fixed port only matters to an externally registered client.
type idpConfig struct {
	Enabled bool
	Config  string
	Port    int
}

// otelConfig selects the telemetry viewer `pw dev` runs beside the application.
// It is on by default because an observable developer loop is the point of it,
// and the port defaults to 0 because pw dev injects the resolved endpoint. Max
// bounds the retained records per signal; zero keeps the viewer default.
type otelConfig struct {
	Enabled bool
	Port    int
	Max     int
}

// devLogsConfig selects the structured local record file pw dev asks the
// application process to append. It is tooling configuration, not runtime
// observability configuration: deployed processes never see the selected path.
type devLogsConfig struct {
	Enabled   bool
	Directory string
}

// consoleConfig selects the development console `pw dev` serves beside the
// application: one loopback listener holding the index and every pane.
//
// The port is fixed rather than reserved, which is the opposite of every other
// development listener here. A reserved port moves on every run, and a surface
// the developer bookmarks and returns to all day cannot move. The telemetry
// receiver keeps its reserved port because the address it publishes is one a
// process is handed rather than one a person types.
type consoleConfig struct {
	Enabled bool
	Port    int
	// Assets enables the static asset pane. Each pane has a key of its own so
	// that turning one off is not turning the console off.
	Assets bool
	// Overlay puts the loop's failures over the pages the application serves.
	// Turning it off is what makes a development page byte-identical to a
	// production one, because nothing is served to load.
	Overlay bool
	// Reload reloads a page whose application has been replaced. It only
	// applies where the overlay is already attached.
	Reload bool
	// Launcher puts a floating link to the console in a corner of the pages
	// the application serves. It is independent of Overlay: a developer who
	// wants the way in does not necessarily want a sheet over the page.
	Launcher bool
	// LauncherCorner places it. A corner is a value that travels with the
	// project rather than drag state held per browser, which is why the
	// collision it answers is settled here and not in the page.
	LauncherCorner string
	// Data serves the table browser, the row editor, the statement console,
	// and the declared query runner. It is the one pane the application serves
	// itself, because the development database is only addressable from inside
	// the process that opened it.
	Data bool
	// Storybook builds and runs the generated harness that renders templates
	// on their own. It is the one pane whose availability depends on a second
	// build succeeding.
	Storybook bool
}

// generationScope records, per generation purpose, the directories pw generate
// may read for it. Paths are project-relative and in slash form. No purpose has
// a default: a project states where each kind of generated code comes from
// instead of letting one walk of the tree feed all of them.
type generationScope struct {
	Handlers  []string
	Templates []string
	Queries   []string
	Config    []string
	// Pages lists page tree roots. An entry is a whole tree rather than a
	// directory of independent sources: one entry is one generation run and one
	// generated registry.
	Pages []string
	// Dynamo lists the directories holding dynamo-tagged types and .pw.dynamo
	// query declarations. It reads Go type declarations rather than a template
	// language, which is why it is not part of generate.queries.
	Dynamo []string
	// Firestore lists the directories holding firestore-tagged types and
	// .pw.firestore query declarations, on the same terms as Dynamo. The two
	// are separate purposes because a project may have either store, and a
	// directory listed for one is not a generation source for the other.
	Firestore []string
}

// generatePurposes are the configuration keys of generationScope, in the order
// their errors and listings are reported.
var generatePurposes = []struct {
	key    string
	target func(*generationScope) *[]string
	// optional marks a purpose whose absent key means the empty list. Only a
	// purpose added after the key set was fixed is one: every project written
	// before page trees or DynamoDB existed has no such key, and none of them
	// has the sources either.
	optional bool
}{
	{key: "generate.handlers", target: func(s *generationScope) *[]string { return &s.Handlers }},
	{key: "generate.templates", target: func(s *generationScope) *[]string { return &s.Templates }},
	{key: "generate.queries", target: func(s *generationScope) *[]string { return &s.Queries }},
	{key: "generate.config", target: func(s *generationScope) *[]string { return &s.Config }},
	{key: "generate.pages", target: func(s *generationScope) *[]string { return &s.Pages }, optional: true},
	{key: "generate.dynamo", target: func(s *generationScope) *[]string { return &s.Dynamo }, optional: true},
	{key: "generate.firestore", target: func(s *generationScope) *[]string { return &s.Firestore }, optional: true},
}

// cborGenerateConfig declares application/cbor request and response bodies
// for every API route at once. It is project-wide on purpose: which media
// types a service accepts is a property of the service, not of one route, so
// there is no per-route or per-type spelling. With the block absent the
// generated output is byte-identical to today's and no CBOR code is linked.
type cborGenerateConfig struct {
	Enabled bool
	// RejectFloats refuses float64 fields at generation and floats arriving
	// in a request body at decode, for schemas carrying scaled integers.
	RejectFloats bool
	// SortedKeys emits map members in RFC 8949 bytewise key order instead of
	// struct field order, for clients that verify deterministic encoding.
	SortedKeys bool
}

// watchConfig widens or trims the pw dev walk. Unlike generation, the walk has a
// working default, because a rebuild is triggered by any compiled input; only
// the trimming is worth leaving to the project.
type watchConfig struct {
	Includes []string
	Excludes []string
}

// packageRef is one entry of the consuming project's packages array. It names a
// module and nothing else: the declaration says the application intends to use
// that package, which is a different fact from go.mod saying the module is
// available, and it is the fact the bootstrap generator needs in order to know
// what to import.
type packageRef struct {
	Module string
}

type projectConfig struct {
	Name string
	// Kind selects which commands apply and which keys are legal. It is read
	// before anything else, so a command that does not apply to the kind is
	// reported as such rather than failing later on a missing key.
	Kind      string
	Main      string
	Toolchain string
	// Database is the engine .pw.sql sources generate for. It sits beside the
	// toolchain because both are build-time properties of the source tree: one
	// decides which compiler reads it, the other which dialect it is written
	// in. The runtime engine still comes from the rdb DSN scheme, which must
	// agree with this.
	Database string
	// FastHTTP declares that this project is built for the fasthttp backend as
	// well as for net/http. It adds a build rather than selecting one: the
	// net/http source stays the source an author writes, and the second build
	// is derived from it.
	//
	// What it changes is generation, in three ways. Every emitted file that
	// imports net/http gains a !fasthttp constraint, so the two builds do not
	// both define the same symbols. Every authored handler is derived into one
	// taking the fasthttp request, constrained to that build. And every page
	// tree gains a second copy of the files that read the request, beside the
	// compiled components both builds share.
	FastHTTP bool
	Generate generationScope
	// LineDirectives maps generated template code back to the template line
	// that produced it, per requirement:template-source-positions.
	//
	// It is a project setting rather than a command flag because generation
	// output must not depend on who ran it: api:cli-check compares the tree
	// against a fresh generation, and a flag one machine passed and another
	// did not would report drift on every one of them.
	//
	// Off by default, because turning it on rewrites every generated file
	// holding a template and makes go test -cover report lines that do not
	// exist in the file the profile names. A project takes one or the other.
	LineDirectives bool
	// CBOR is the generate.api.cbor block: whether generated binders and
	// writers negotiate application/cbor bodies, and which CBOR subset the
	// codecs are generated for. A project setting rather than a pw generate
	// flag for the reason LineDirectives is: generation output must not depend
	// on who ran it, and the profile is hashed into the generation fingerprint
	// so both ends of the protocol agree on every regeneration.
	CBOR      cborGenerateConfig
	Watch     watchConfig
	IdP       idpConfig
	Otel      otelConfig
	Logs      devLogsConfig
	Console   consoleConfig
	Migration migrationConfig
	Seed      seedConfig
	Tailwind  tailwindConfig
	// Cloudflare is the deploy.cloudflare table: what pw build --target
	// cloudflare-workers compiles with and writes into wrangler.jsonc, per
	// requirement:cloudflare-workers-build-target. Every value has a default
	// drawn from the rest of the project, so the table is optional.
	Cloudflare cloudflareConfig
	// Assets is the build-time conversion set. Every field defaults to off, so
	// a project that declares nothing embeds a copy of its authored tree and
	// serves exactly what it served before any of this existed.
	Assets assetsConfig
	// Packages are the component packages an application declares. Declaring one
	// is what links it: pw generate emits the blank import from this list, so a
	// module in go.mod and not here is an ordinary dependency.
	Packages []packageRef
	// Package is the manifest a package project publishes. It is empty for an
	// application, and an application carrying the section is an error.
	Package packageManifest
	// I18n is the message catalog and locale routing declaration. An absent
	// block means the project is single-locale, which is the shape every
	// project written before requirement:application-i18n has.
	I18n i18nConfig
}

// i18nConfig declares the locales a project ships and how each route decides
// which one it is serving.
//
// Almost every key is build time, because it changes generated output: the
// declared locales decide which table columns and which plural rules are
// emitted, and the default decides what the fallback chain is flattened into.
// See .knowledge data:i18n-config.
type i18nConfig struct {
	// Locales are the declared tags in declaration order. The index of each is
	// the subscript of every generated message table.
	Locales []string
	// DefaultLocale is the terminal of the fallback chain and the locale a
	// route with no declared mode serves.
	DefaultLocale string
	// Catalog is the project-relative directory holding the message files.
	Catalog string
	// Missing is the severity of a locale supplying no translation for a
	// message: "error" or "warn". A build that must ship complete fails; one
	// still being translated reports and falls back.
	Missing string
	// PrefixDefault decides whether the default locale carries a path prefix.
	//
	// It defaults to true because changing which locale is default under false
	// moves every URL on the site, which is a migration; under true it changes
	// only where the root redirects.
	PrefixDefault bool
	// Labels is the display name of each locale, written in that locale. It is
	// declared rather than derived because shipping CLDR display-name tables is
	// weight for a value an application often wants to word itself.
	Labels map[string]string
	// Routes are the locale modes by path prefix, longest prefix first.
	Routes []localeRoute
}

// Enabled reports whether the project declared any locale.
func (c i18nConfig) Enabled() bool { return len(c.Locales) > 0 }

// localeRoute binds one path prefix to the way its locale is decided.
type localeRoute struct {
	Prefix string
	Mode   string
}

// localeModes are the declared modes and the configuration key each is listed
// under. The mode is the key name rather than a field of a table, so the block
// needs no array of tables.
var localeModes = []struct {
	key  string
	mode string
}{
	{key: "i18n.path_routes", mode: "path"},
	{key: "i18n.cookie_routes", mode: "cookie"},
	{key: "i18n.header_routes", mode: "header"},
}

const (
	defaultCatalogDir  = "messages"
	i18nLabelKeyPrefix = "i18n.label."
)

func loadProjectConfig(root string) (projectConfig, error) {
	path := filepath.Join(root, "popcornweb.toml")
	source, err := os.ReadFile(path)
	if err != nil {
		return projectConfig{}, fmt.Errorf("read %s: %w", path, err)
	}
	document, err := minitoml.Parse(source)
	if err != nil {
		return projectConfig{}, fmt.Errorf("parse %s: %w", path, err)
	}
	known := []string{
		"project.name", "project.kind", "project.main", "project.toolchain", "project.database",
		"project.fasthttp",
		"packages",
		"generate.handlers", "generate.templates", "generate.queries", "generate.config", "generate.pages",
		"generate.dynamo", "generate.firestore",
		"generate.line_directives",
		"generate.api.cbor.enabled", "generate.api.cbor.reject_floats", "generate.api.cbor.sorted_keys",
		"dev.watch.includes", "dev.watch.excludes",
		"seed.auto",
		"dev.idp.enabled", "dev.idp.config", "dev.idp.port",
		"dev.otel.enabled", "dev.otel.port", "dev.otel.max",
		"dev.logs.enabled", "dev.logs.directory",
		"dev.console.enabled", "dev.console.port", "dev.console.assets.enabled",
		"dev.console.overlay.enabled", "dev.console.overlay.reload",
		"dev.console.launcher.enabled", "dev.console.launcher.corner",
		"dev.console.storybook.enabled", "dev.console.data.enabled",
		"migration.dir", "migration.auto",
		"assets.tailwind.enabled", "assets.tailwind.input",
		"assets.tailwind.output", "assets.tailwind.minify",
		"assets.css.minify", "assets.images.enabled", "assets.images.quality",
		"assets.images.avif", "assets.scripts.enabled",
		"assets.verify.enabled", "assets.verify.svg_scan", "assets.verify.allow",
		"deploy.cloudflare.compiler", "deploy.cloudflare.name", "deploy.cloudflare.compatibility_date",
		"deploy.cloudflare.d1", "deploy.cloudflare.r2.binding", "deploy.cloudflare.r2.bucket_name",
		"deploy.cloudflare.kv",
	}
	known = append(known,
		"i18n.locales", "i18n.default_locale", "i18n.catalog", "i18n.missing", "i18n.prefix_default",
		"i18n.path_routes", "i18n.cookie_routes", "i18n.header_routes")
	known = append(known, packageManifestKeys...)
	for _, key := range document.Keys() {
		// A label key names a locale, so the set cannot be enumerated the way
		// every other key can. The prefix is checked instead, and the tag it
		// names is checked against the declared list below, which is the check
		// that actually catches a typo.
		if strings.HasPrefix(key, i18nLabelKeyPrefix) {
			continue
		}
		if !slices.Contains(known, key) {
			return projectConfig{}, fmt.Errorf("popcornweb.toml: unknown key %s", key)
		}
	}
	config := projectConfig{}
	config.Name, err = scalar(document, "project.name")
	if err != nil {
		return projectConfig{}, err
	}
	config.Kind, err = optionalScalar(document, "project.kind")
	if err != nil {
		return projectConfig{}, err
	}
	if config.Kind == "" {
		config.Kind = kindApplication
	}
	if config.Kind != kindApplication && config.Kind != kindPackage {
		return projectConfig{}, fmt.Errorf("popcornweb.toml: project.kind must be %q or %q", kindApplication, kindPackage)
	}
	config.Main, err = optionalScalar(document, "project.main")
	if err != nil {
		return projectConfig{}, err
	}
	config.Toolchain, err = optionalScalar(document, "project.toolchain")
	if err != nil {
		return projectConfig{}, err
	}
	if config.Toolchain == "" {
		config.Toolchain = toolchainTinyGo
	}
	if config.Toolchain != toolchainTinyGo && config.Toolchain != toolchainGo {
		return projectConfig{}, fmt.Errorf("popcornweb.toml: project.toolchain must be %q or %q", toolchainTinyGo, toolchainGo)
	}
	config.Database, err = optionalScalar(document, "project.database")
	if err != nil {
		return projectConfig{}, err
	}
	if config.Database == "" {
		// Projects scaffolded before the key existed could only be SQLite, and
		// it generates what they already have.
		config.Database = engineSQLite
	}
	if !validEngine(config.Database) {
		return projectConfig{}, fmt.Errorf("popcornweb.toml: project.database must be %s", engineNames())
	}
	if value, ok := document.Get("project.fasthttp"); ok {
		config.FastHTTP, err = value.AsBool()
		if err != nil {
			return projectConfig{}, fmt.Errorf("popcornweb.toml: project.fasthttp: %w", err)
		}
	}
	config.Cloudflare, err = cloudflareSettings(document, config)
	if err != nil {
		return projectConfig{}, err
	}
	config.Generate, err = generationSources(document, root)
	if err != nil {
		return projectConfig{}, err
	}
	config.Watch, err = watchPaths(document, root)
	if err != nil {
		return projectConfig{}, err
	}
	if err := config.readKindSections(document); err != nil {
		return projectConfig{}, err
	}
	if value, ok := document.Get("dev.idp.enabled"); ok {
		config.IdP.Enabled, err = value.AsBool()
		if err != nil {
			return projectConfig{}, fmt.Errorf("popcornweb.toml: dev.idp.enabled: %w", err)
		}
	}
	config.IdP.Config, err = optionalScalar(document, "dev.idp.config")
	if err != nil {
		return projectConfig{}, err
	}
	if config.IdP.Config == "" {
		config.IdP.Config = defaultIdPConfig
	}
	if filepath.IsAbs(config.IdP.Config) {
		return projectConfig{}, fmt.Errorf("popcornweb.toml: dev.idp.config must be relative to the project")
	}
	if value, ok := document.Get("dev.idp.port"); ok {
		port, err := value.AsInt()
		if err != nil {
			return projectConfig{}, fmt.Errorf("popcornweb.toml: dev.idp.port: %w", err)
		}
		if port < 0 || port > 65535 {
			return projectConfig{}, fmt.Errorf("popcornweb.toml: dev.idp.port must be between 0 and 65535")
		}
		config.IdP.Port = int(port)
	}
	config.Otel.Enabled = true
	if value, ok := document.Get("dev.otel.enabled"); ok {
		config.Otel.Enabled, err = value.AsBool()
		if err != nil {
			return projectConfig{}, fmt.Errorf("popcornweb.toml: dev.otel.enabled: %w", err)
		}
	}
	if value, ok := document.Get("dev.otel.port"); ok {
		port, err := value.AsInt()
		if err != nil {
			return projectConfig{}, fmt.Errorf("popcornweb.toml: dev.otel.port: %w", err)
		}
		if port < 0 || port > 65535 {
			return projectConfig{}, fmt.Errorf("popcornweb.toml: dev.otel.port must be between 0 and 65535")
		}
		config.Otel.Port = int(port)
	}
	if value, ok := document.Get("dev.otel.max"); ok {
		max, err := value.AsInt()
		if err != nil {
			return projectConfig{}, fmt.Errorf("popcornweb.toml: dev.otel.max: %w", err)
		}
		if max < 0 {
			return projectConfig{}, fmt.Errorf("popcornweb.toml: dev.otel.max must not be negative")
		}
		config.Otel.Max = int(max)
	}
	config.Logs.Enabled = true
	if value, ok := document.Get("dev.logs.enabled"); ok {
		config.Logs.Enabled, err = value.AsBool()
		if err != nil {
			return projectConfig{}, fmt.Errorf("popcornweb.toml: dev.logs.enabled: %w", err)
		}
	}
	config.Logs.Directory, err = optionalScalar(document, "dev.logs.directory")
	if err != nil {
		return projectConfig{}, err
	}
	if config.Logs.Directory == "" {
		config.Logs.Directory = defaultDevLogDir
	}
	logDirectory := filepath.Clean(filepath.FromSlash(config.Logs.Directory))
	if filepath.IsAbs(logDirectory) || logDirectory == "." || logDirectory == ".." || strings.HasPrefix(logDirectory, ".."+string(filepath.Separator)) {
		return projectConfig{}, fmt.Errorf("popcornweb.toml: dev.logs.directory must be a relative directory within the project")
	}
	config.Logs.Directory = filepath.ToSlash(logDirectory)
	config.Console.Enabled = true
	config.Console.Assets = true
	config.Console.Overlay = true
	config.Console.Reload = true
	config.Console.Launcher = true
	config.Console.LauncherCorner = defaultLauncherCorner
	config.Console.Storybook = true
	config.Console.Data = true
	config.Console.Port = defaultConsolePort
	if value, ok := document.Get("dev.console.enabled"); ok {
		config.Console.Enabled, err = value.AsBool()
		if err != nil {
			return projectConfig{}, fmt.Errorf("popcornweb.toml: dev.console.enabled: %w", err)
		}
	}
	if value, ok := document.Get("dev.console.assets.enabled"); ok {
		config.Console.Assets, err = value.AsBool()
		if err != nil {
			return projectConfig{}, fmt.Errorf("popcornweb.toml: dev.console.assets.enabled: %w", err)
		}
	}
	if value, ok := document.Get("generate.line_directives"); ok {
		config.LineDirectives, err = value.AsBool()
		if err != nil {
			return projectConfig{}, fmt.Errorf("popcornweb.toml: generate.line_directives: %w", err)
		}
	}
	if value, ok := document.Get("generate.api.cbor.enabled"); ok {
		config.CBOR.Enabled, err = value.AsBool()
		if err != nil {
			return projectConfig{}, fmt.Errorf("popcornweb.toml: generate.api.cbor.enabled: %w", err)
		}
	}
	if value, ok := document.Get("generate.api.cbor.reject_floats"); ok {
		config.CBOR.RejectFloats, err = value.AsBool()
		if err != nil {
			return projectConfig{}, fmt.Errorf("popcornweb.toml: generate.api.cbor.reject_floats: %w", err)
		}
	}
	if value, ok := document.Get("generate.api.cbor.sorted_keys"); ok {
		config.CBOR.SortedKeys, err = value.AsBool()
		if err != nil {
			return projectConfig{}, fmt.Errorf("popcornweb.toml: generate.api.cbor.sorted_keys: %w", err)
		}
	}
	if value, ok := document.Get("dev.console.overlay.enabled"); ok {
		config.Console.Overlay, err = value.AsBool()
		if err != nil {
			return projectConfig{}, fmt.Errorf("popcornweb.toml: dev.console.overlay.enabled: %w", err)
		}
	}
	if value, ok := document.Get("dev.console.overlay.reload"); ok {
		config.Console.Reload, err = value.AsBool()
		if err != nil {
			return projectConfig{}, fmt.Errorf("popcornweb.toml: dev.console.overlay.reload: %w", err)
		}
	}
	if value, ok := document.Get("dev.console.launcher.enabled"); ok {
		config.Console.Launcher, err = value.AsBool()
		if err != nil {
			return projectConfig{}, fmt.Errorf("popcornweb.toml: dev.console.launcher.enabled: %w", err)
		}
	}
	if value, ok := document.Get("dev.console.launcher.corner"); ok {
		corner, err := value.AsString()
		if err != nil {
			return projectConfig{}, fmt.Errorf("popcornweb.toml: dev.console.launcher.corner: %w", err)
		}
		if !slices.Contains(launcherCorners, corner) {
			// Named rather than defaulted, so a typo does not read as a corner
			// the project chose. project.toolchain and project.database reject
			// an unknown value for the same reason.
			return projectConfig{}, fmt.Errorf(
				"popcornweb.toml: dev.console.launcher.corner: %q is not one of %s",
				corner, strings.Join(launcherCorners, ", "))
		}
		config.Console.LauncherCorner = corner
	}
	if value, ok := document.Get("dev.console.storybook.enabled"); ok {
		config.Console.Storybook, err = value.AsBool()
		if err != nil {
			return projectConfig{}, fmt.Errorf("popcornweb.toml: dev.console.storybook.enabled: %w", err)
		}
	}
	if value, ok := document.Get("dev.console.data.enabled"); ok {
		config.Console.Data, err = value.AsBool()
		if err != nil {
			return projectConfig{}, fmt.Errorf("popcornweb.toml: dev.console.data.enabled: %w", err)
		}
	}
	if value, ok := document.Get("dev.console.port"); ok {
		port, err := value.AsInt()
		if err != nil {
			return projectConfig{}, fmt.Errorf("popcornweb.toml: dev.console.port: %w", err)
		}
		if port < 0 || port > 65535 {
			return projectConfig{}, fmt.Errorf("popcornweb.toml: dev.console.port must be between 0 and 65535")
		}
		config.Console.Port = int(port)
	}
	config.Migration.Dir, err = optionalScalar(document, "migration.dir")
	if err != nil {
		return projectConfig{}, err
	}
	if config.Migration.Dir == "" {
		config.Migration.Dir = defaultMigrationDir
	}
	if filepath.IsAbs(config.Migration.Dir) {
		return projectConfig{}, fmt.Errorf("popcornweb.toml: migration.dir must be relative to the project")
	}
	config.Migration.Auto = true
	if value, ok := document.Get("migration.auto"); ok {
		config.Migration.Auto, err = value.AsBool()
		if err != nil {
			return projectConfig{}, fmt.Errorf("popcornweb.toml: migration.auto: %w", err)
		}
	}
	config.Seed.Auto = true
	if value, ok := document.Get("seed.auto"); ok {
		config.Seed.Auto, err = value.AsBool()
		if err != nil {
			return projectConfig{}, fmt.Errorf("popcornweb.toml: seed.auto: %w", err)
		}
	}
	// Both verification checks read bytes the asset walk already holds, so
	// there is no cost to defaulting them on. The switches exist for a project
	// shipping a file the signature table judges wrongly, and for one serving
	// an SVG that is interactive on purpose.
	config.Assets.Verify = true
	config.Assets.VerifySVG = true
	for _, binding := range []struct {
		key    string
		target *bool
	}{
		{"assets.css.minify", &config.Assets.CSSMinify},
		{"assets.images.enabled", &config.Assets.Images},
		{"assets.images.avif", &config.Assets.AVIF},
		{"assets.scripts.enabled", &config.Assets.Scripts},
		{"assets.verify.enabled", &config.Assets.Verify},
		{"assets.verify.svg_scan", &config.Assets.VerifySVG},
	} {
		value, ok := document.Get(binding.key)
		if !ok {
			continue
		}
		parsed, err := value.AsBool()
		if err != nil {
			return projectConfig{}, fmt.Errorf("popcornweb.toml: %s: %w", binding.key, err)
		}
		*binding.target = parsed
	}
	// The quality only reaches a lossy source. Its default is libwebp's own, so
	// a project that states nothing gets what the tool considers reasonable.
	config.Assets.ImageQuality = defaultImageQuality
	if value, ok := document.Get("assets.images.quality"); ok {
		quality, err := value.AsInt()
		if err != nil {
			return projectConfig{}, fmt.Errorf("popcornweb.toml: assets.images.quality: %w", err)
		}
		if quality < 1 || quality > 100 {
			return projectConfig{}, fmt.Errorf("popcornweb.toml: assets.images.quality must be between 1 and 100")
		}
		config.Assets.ImageQuality = int(quality)
	}
	config.Assets.VerifyAllow, err = array(document, "assets.verify.allow")
	if err != nil {
		return projectConfig{}, fmt.Errorf("popcornweb.toml: assets.verify.allow: %w", err)
	}
	for _, glob := range config.Assets.VerifyAllow {
		// An absolute or escaping glob would silently exempt nothing, since
		// every path it is matched against is relative to the authored tree.
		if glob == "" || strings.HasPrefix(glob, "/") || strings.HasPrefix(glob, "../") {
			return projectConfig{}, fmt.Errorf("popcornweb.toml: assets.verify.allow: %q must be relative to the public directory", glob)
		}
	}
	if value, ok := document.Get("assets.tailwind.enabled"); ok {
		config.Tailwind.Enabled, err = value.AsBool()
		if err != nil {
			return projectConfig{}, fmt.Errorf("popcornweb.toml: assets.tailwind.enabled: %w", err)
		}
	}
	if value, ok := document.Get("assets.tailwind.minify"); ok {
		config.Tailwind.Minify, err = value.AsBool()
		if err != nil {
			return projectConfig{}, fmt.Errorf("popcornweb.toml: assets.tailwind.minify: %w", err)
		}
	}
	config.Tailwind.Input, err = optionalScalar(document, "assets.tailwind.input")
	if err != nil {
		return projectConfig{}, err
	}
	config.Tailwind.Output, err = optionalScalar(document, "assets.tailwind.output")
	if err != nil {
		return projectConfig{}, err
	}
	if config.Tailwind.Enabled {
		if config.Tailwind.Input == "" {
			config.Tailwind.Input = defaultTailwindInput
		}
		if config.Tailwind.Output == "" {
			config.Tailwind.Output = defaultTailwindOutput
		}
		if filepath.IsAbs(config.Tailwind.Input) || filepath.IsAbs(config.Tailwind.Output) {
			return projectConfig{}, fmt.Errorf("popcornweb.toml: Tailwind input and output must be relative to the project")
		}
		input := filepath.Clean(filepath.Join(root, filepath.FromSlash(config.Tailwind.Input)))
		output := filepath.Clean(filepath.Join(root, filepath.FromSlash(config.Tailwind.Output)))
		if input == output {
			return projectConfig{}, fmt.Errorf("popcornweb.toml: Tailwind input and output must be different files")
		}
	}
	config.I18n, err = readI18n(document)
	if err != nil {
		return projectConfig{}, err
	}
	return config, nil
}

// readI18n reads the locale declaration.
//
// An absent block is a single-locale project rather than a default set: nothing
// is inferred from a catalog directory existing, because a project keeping
// translations it has not adopted yet would then start generating against them.
func readI18n(document minitoml.Document) (i18nConfig, error) {
	config := i18nConfig{Catalog: defaultCatalogDir, Missing: "error", PrefixDefault: true}

	locales, err := array(document, "i18n.locales")
	if err != nil {
		return i18nConfig{}, err
	}
	if len(locales) == 0 {
		for _, key := range document.Keys() {
			if strings.HasPrefix(key, "i18n.") {
				return i18nConfig{}, fmt.Errorf("popcornweb.toml: %s is set but i18n.locales is empty, so nothing declares which languages exist", key)
			}
		}
		return i18nConfig{}, nil
	}
	seen := map[string]bool{}
	for _, tag := range locales {
		if tag == "" {
			return i18nConfig{}, fmt.Errorf("popcornweb.toml: i18n.locales holds an empty tag")
		}
		if seen[tag] {
			return i18nConfig{}, fmt.Errorf("popcornweb.toml: i18n.locales lists %q twice", tag)
		}
		seen[tag] = true
	}
	config.Locales = locales

	config.DefaultLocale, err = optionalScalar(document, "i18n.default_locale")
	if err != nil {
		return i18nConfig{}, err
	}
	if config.DefaultLocale == "" {
		// The first declared locale rather than an error: a list has an order,
		// and reading the first one as primary is what a reader assumes anyway.
		config.DefaultLocale = locales[0]
	}
	if !seen[config.DefaultLocale] {
		return i18nConfig{}, fmt.Errorf("popcornweb.toml: i18n.default_locale %q is not in i18n.locales", config.DefaultLocale)
	}

	if catalog, err := optionalScalar(document, "i18n.catalog"); err != nil {
		return i18nConfig{}, err
	} else if catalog != "" {
		if filepath.IsAbs(catalog) || strings.HasPrefix(catalog, "../") {
			return i18nConfig{}, fmt.Errorf("popcornweb.toml: i18n.catalog %q must be inside the project", catalog)
		}
		config.Catalog = catalog
	}

	if missing, err := optionalScalar(document, "i18n.missing"); err != nil {
		return i18nConfig{}, err
	} else if missing != "" {
		if missing != "error" && missing != "warn" {
			return i18nConfig{}, fmt.Errorf("popcornweb.toml: i18n.missing is %q, want error or warn", missing)
		}
		config.Missing = missing
	}

	if value, ok := document.Get("i18n.prefix_default"); ok {
		config.PrefixDefault, err = value.AsBool()
		if err != nil {
			return i18nConfig{}, fmt.Errorf("popcornweb.toml: i18n.prefix_default: %w", err)
		}
	}

	config.Labels = map[string]string{}
	for _, key := range document.Keys() {
		if !strings.HasPrefix(key, i18nLabelKeyPrefix) {
			continue
		}
		tag := strings.TrimPrefix(key, i18nLabelKeyPrefix)
		if !seen[tag] {
			return i18nConfig{}, fmt.Errorf("popcornweb.toml: %s names %q, which is not in i18n.locales", key, tag)
		}
		label, err := scalar(document, key)
		if err != nil {
			return i18nConfig{}, err
		}
		config.Labels[tag] = label
	}

	prefixes := map[string]string{}
	for _, mode := range localeModes {
		entries, err := array(document, mode.key)
		if err != nil {
			return i18nConfig{}, err
		}
		for _, prefix := range entries {
			if !strings.HasPrefix(prefix, "/") {
				return i18nConfig{}, fmt.Errorf("popcornweb.toml: %s %q must start with a slash", mode.key, prefix)
			}
			if previous, clash := prefixes[prefix]; clash {
				return i18nConfig{}, fmt.Errorf("popcornweb.toml: prefix %q is declared as both %s and %s", prefix, previous, mode.mode)
			}
			prefixes[prefix] = mode.mode
			config.Routes = append(config.Routes, localeRoute{Prefix: prefix, Mode: mode.mode})
		}
	}
	// Longest first, so a lookup takes the first match and a nested prefix wins
	// over the root the way every other prefix policy resolves.
	sort.SliceStable(config.Routes, func(i, j int) bool {
		return len(config.Routes[i].Prefix) > len(config.Routes[j].Prefix)
	})
	return config, nil
}

// readKindSections reads the keys that belong to one project kind and rejects
// the ones that belong to the other. The two are mutually exclusive rather than
// merely unused: a package has no entry point to name, and an application has
// nothing to publish, so each key appearing under the wrong kind is a mistake
// worth naming rather than a value to ignore.
func (config *projectConfig) readKindSections(document minitoml.Document) error {
	hasManifest := false
	for _, key := range packageManifestKeys {
		if _, ok := document.Get(key); ok {
			hasManifest = true
			break
		}
	}
	_, hasPackages := document.Get("packages")
	switch config.Kind {
	case kindPackage:
		if config.Main != "" {
			return fmt.Errorf("popcornweb.toml: project.main does not apply to a package; the application that imports it owns the entry point")
		}
		if hasPackages {
			return fmt.Errorf("popcornweb.toml: packages does not apply to a package; a package depends on another through go.mod like any Go module")
		}
		if !hasManifest {
			return fmt.Errorf("popcornweb.toml: a package project requires the package section; start with package.module")
		}
		manifest, err := loadPackageManifest(document)
		if err != nil {
			return err
		}
		config.Package = manifest
		// A generated query carries one engine's placeholder syntax, chosen when
		// the package was published, and a package cannot know its consumer's
		// engine. Generating one would ship a query that compiles and then fails
		// at the first call in half the projects that install it.
		if len(config.Generate.Queries) > 0 {
			return fmt.Errorf("popcornweb.toml: generate.queries must be empty in a package; a generated query is compiled for one engine and a package cannot know its consumer's")
		}
	default:
		if config.Main == "" {
			return fmt.Errorf("popcornweb.toml: project.main is required")
		}
		if hasManifest {
			return fmt.Errorf("popcornweb.toml: the package section applies to a package; set project.kind = %q to publish this module", kindPackage)
		}
		refs, err := packageRefs(document)
		if err != nil {
			return err
		}
		config.Packages = refs
	}
	return nil
}

// sourcesExample shows an operator what a purpose entry looks like, for the
// error raised when one of the required keys is missing.
const sourcesExample = `generate.handlers = ["handlers"]`

// generationSources reads the directories each generation purpose is allowed to
// read. Every purpose key is required, and an empty list is how a project says
// that a purpose generates nothing — which a missing key cannot express.
func generationSources(document minitoml.Document, root string) (generationScope, error) {
	scope := generationScope{}
	for _, purpose := range generatePurposes {
		directories, err := purposeDirectories(document, root, purpose.key, purpose.optional)
		if err != nil {
			return generationScope{}, err
		}
		*purpose.target(&scope) = directories
	}
	if err := checkPageRoots(scope); err != nil {
		return generationScope{}, err
	}
	return scope, nil
}

// checkPageRoots keeps a page tree from being read twice. The tree run already
// compiles the page and layout templates it holds, so a directory inside a root
// that another purpose also lists would have one output written twice, by two
// runs, with different content.
func checkPageRoots(scope generationScope) error {
	overlaps := func(key string, entries []string) error {
		for _, root := range scope.Pages {
			for _, entry := range entries {
				if entry == root || strings.HasPrefix(entry, root+"/") {
					return fmt.Errorf("popcornweb.toml: %s %q is inside the page tree root %q, which generates it already", key, entry, root)
				}
				if strings.HasPrefix(root, entry+"/") {
					return fmt.Errorf("popcornweb.toml: page tree root %q is inside %s %q, which would read its templates a second time", root, key, entry)
				}
			}
		}
		return nil
	}
	if err := overlaps("generate.templates", scope.Templates); err != nil {
		return err
	}
	return overlaps("generate.handlers", scope.Handlers)
}

// purposeDirectories validates one purpose list.
func purposeDirectories(document minitoml.Document, root, key string, optional bool) ([]string, error) {
	value, ok := document.Get(key)
	if !ok {
		if optional {
			return nil, nil
		}
		return nil, fmt.Errorf("popcornweb.toml: %s is required; list the directories it reads, such as %s, or [] when it generates nothing", key, sourcesExample)
	}
	entries, err := value.AsStringSlice()
	if err != nil {
		return nil, fmt.Errorf("popcornweb.toml: %s: %w", key, err)
	}
	seen := make(map[string]bool, len(entries))
	directories := make([]string, 0, len(entries))
	for _, entry := range entries {
		if filepath.IsAbs(entry) {
			return nil, fmt.Errorf("popcornweb.toml: %s %q must be relative to the project", key, entry)
		}
		clean := filepath.ToSlash(filepath.Clean(filepath.FromSlash(entry)))
		if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
			return nil, fmt.Errorf("popcornweb.toml: %s %q must name a directory inside the project", key, entry)
		}
		if seen[clean] {
			return nil, fmt.Errorf("popcornweb.toml: %s lists %q twice", key, entry)
		}
		seen[clean] = true
		info, err := os.Stat(filepath.Join(root, filepath.FromSlash(clean)))
		if err != nil {
			return nil, fmt.Errorf("popcornweb.toml: %s %q: %w", key, entry, err)
		}
		if !info.IsDir() {
			return nil, fmt.Errorf("popcornweb.toml: %s %q is not a directory", key, entry)
		}
		directories = append(directories, clean)
	}
	// A nested entry would have its sources planned twice within one purpose,
	// and the second plan would delete what the first one wrote.
	for _, outer := range directories {
		for _, inner := range directories {
			if outer != inner && strings.HasPrefix(inner, outer+"/") {
				return nil, fmt.Errorf("popcornweb.toml: %s %q is already covered by %q", key, inner, outer)
			}
		}
	}
	slices.Sort(directories)
	return directories, nil
}

// watchPaths reads the pw dev walk adjustments. Both lists are optional,
// because the walk of the module is already the right default.
func watchPaths(document minitoml.Document, root string) (watchConfig, error) {
	config := watchConfig{}
	var err error
	config.Includes, err = array(document, "dev.watch.includes")
	if err != nil {
		return watchConfig{}, fmt.Errorf("popcornweb.toml: dev.watch.includes: %w", err)
	}
	for _, pattern := range config.Includes {
		if filepath.IsAbs(pattern) {
			return watchConfig{}, fmt.Errorf("popcornweb.toml: dev.watch.includes paths must be relative")
		}
		if _, err := filepath.Glob(filepath.Join(root, filepath.FromSlash(pattern))); err != nil {
			return watchConfig{}, fmt.Errorf("popcornweb.toml: dev.watch.includes %q: %w", pattern, err)
		}
	}
	config.Excludes, err = array(document, "dev.watch.excludes")
	if err != nil {
		return watchConfig{}, fmt.Errorf("popcornweb.toml: dev.watch.excludes: %w", err)
	}
	for index, entry := range config.Excludes {
		if filepath.IsAbs(entry) {
			return watchConfig{}, fmt.Errorf("popcornweb.toml: dev.watch.excludes paths must be relative")
		}
		clean := filepath.ToSlash(filepath.Clean(filepath.FromSlash(entry)))
		if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
			return watchConfig{}, fmt.Errorf("popcornweb.toml: dev.watch.excludes %q must name a directory inside the project", entry)
		}
		config.Excludes[index] = clean
	}
	return config, nil
}

func scalar(document minitoml.Document, key string) (string, error) {
	value, ok := document.Get(key)
	if !ok {
		return "", fmt.Errorf("popcornweb.toml: %s is required", key)
	}
	return value.AsString()
}

func optionalScalar(document minitoml.Document, key string) (string, error) {
	value, ok := document.Get(key)
	if !ok {
		return "", nil
	}
	result, err := value.AsString()
	if err != nil {
		return "", fmt.Errorf("popcornweb.toml: %s: %w", key, err)
	}
	return result, nil
}

func array(document minitoml.Document, key string) ([]string, error) {
	value, ok := document.Get(key)
	if !ok {
		return nil, nil
	}
	return value.AsStringSlice()
}

func projectRoot(start string) (string, error) {
	current, err := filepath.Abs(start)
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(current, "popcornweb.toml")); err == nil {
			return current, nil
		}
		parent := filepath.Dir(current)
		if parent == current {
			return "", fmt.Errorf("popcornweb.toml not found")
		}
		current = parent
	}
}
