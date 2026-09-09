package pwconfig

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/shibukawa/popcornweb/internal/dotenv"
	"github.com/shibukawa/popcornweb/internal/pwenv"
	"github.com/shibukawa/popcornweb/pwruntime"
	"github.com/shibukawa/tinybind-go/configbind"
)

type configEntry struct {
	prefix string
	ptr    any
}

var configState = struct {
	sync.RWMutex
	entries  map[reflect.Type]configEntry
	parsed   bool
	parseErr error
	options  configbind.LoadOptions
	hooks    Hooks
}{
	entries: make(map[reflect.Type]configEntry),
	options: defaultLoadOptions,
}

// dotenvState is the layer the last Parse read, for the startup summary. It
// has a lock of its own because Parse holds configState for its whole body and
// calls the Loaded hook from inside it; the hook reads this, and a read of the
// same lock from under its writer would never return.
var dotenvState struct {
	sync.RWMutex
	layer dotenv.Layer
}

// defaultLoadOptions is what a process loads with when nothing customized it,
// and what SetLoadOptions falls back to field by field.
var defaultLoadOptions = configbind.LoadOptions{
	Vendor:   "popcornweb",
	FileName: "config.toml",
}

// configEntries is the entry table published for the serving path. Every
// pw.Config and pwfast.Config read passes through the lookup this feeds, so
// the read is a load on a frozen map rather than an RLock — which is still an
// atomic write on a shared cache line — while registration replaces the map
// under the mutex.
var configEntries atomic.Pointer[map[reflect.Type]configEntry]

// publishEntriesLocked mirrors the entry table into the published snapshot.
// The caller holds configState.
func publishEntriesLocked() {
	published := make(map[reflect.Type]configEntry, len(configState.entries))
	for typ, entry := range configState.entries {
		published[typ] = entry
	}
	configEntries.Store(&published)
}

// Hooks are what a runtime layers on top of the load without this package
// having to know which runtime it is.
//
// Both are optional, and a runtime that sets neither still gets a complete
// load: the settings are the point, and the two things below are what a
// particular runtime does around them.
type Hooks struct {
	// Args filters the command arguments before the load, which is how a
	// runtime takes its own subcommands off the line before configbind sees
	// them. Returning an error fails the parse.
	//
	// A nil hook does not mean no filtering: the framework's own arguments are
	// this package's and are taken off the line regardless, through
	// ParseFrameworkAction. A runtime sets this only to add to that.
	Args func([]string) ([]string, error)
	// Loaded receives the load result, which is what a startup summary reads.
	// It runs only on a successful load.
	Loaded func(*configbind.LoadResult)
}

// SetHooks installs the hooks of the runtime performing startup. It must be
// called before Parse.
func SetHooks(hooks Hooks) {
	configState.Lock()
	defer configState.Unlock()
	if configState.parsed {
		panic("popcornweb: configuration hooks set after Parse")
	}
	configState.hooks = hooks
}

// Register registers one generated configbind target without parsing it.
func Register[T any](prefix string) {
	if strings.TrimSpace(prefix) == "" {
		panic("popcornweb: empty configuration prefix")
	}
	typ := reflect.TypeFor[T]()
	configState.Lock()
	defer configState.Unlock()
	if configState.parsed {
		panic("popcornweb: configuration registered after ParseConfig")
	}
	if existing, ok := configState.entries[typ]; ok {
		if existing.prefix != prefix {
			panic(fmt.Sprintf("popcornweb: %v already registered with prefix %q", typ, existing.prefix))
		}
		return
	}
	configState.entries[typ] = configEntry{prefix: prefix, ptr: configbind.Bind[T](prefix)}
	publishEntriesLocked()
}

// SetLoadOptions customizes configbind loading before Parse.
//
// A field the caller left empty keeps the framework's default rather than
// becoming empty: a caller that supplies only Environ, the way a generated
// Worker entry does, is overriding the environment and not withdrawing the
// vendor name the configuration search needs.
func SetLoadOptions(options configbind.LoadOptions) {
	configState.Lock()
	defer configState.Unlock()
	if configState.parsed {
		panic("popcornweb: config options changed after ParseConfig")
	}
	if options.Vendor == "" {
		options.Vendor = defaultLoadOptions.Vendor
	}
	if options.FileName == "" {
		options.FileName = defaultLoadOptions.FileName
	}
	configState.options = options
}

// Parse loads every registered binding, once. Repeated calls return the first
// answer rather than reloading, because a process has one configuration.
func Parse() error {
	// Publishing takes the read lock through the lookup, so it happens after
	// this one is released rather than inside it.
	publish := false
	defer func() {
		if publish {
			PublishChainSettings()
			PublishUpdateSettings()
		}
	}()
	configState.Lock()
	defer configState.Unlock()
	if configState.parsed {
		return configState.parseErr
	}
	configState.parsed = true
	plan, envErr := resolveLoadOptions(configState.options)
	if envErr != nil {
		configState.parseErr = envErr
		return envErr
	}
	options, env := plan.options, plan.env
	setEnv(env, plan.declared)
	if plan.dotenv != nil {
		dotenvState.Lock()
		dotenvState.layer = *plan.dotenv
		dotenvState.Unlock()
	}
	// The framework's own arguments are this package's, so they are filtered off
	// the line whether or not a runtime installed a hook. A runtime that wants
	// to add to that installs one; a build that installs none still answers
	// --generate-config and the health probe, which is what makes the two
	// builds of one application understand the same words.
	filter := configState.hooks.Args
	if filter == nil {
		filter = ParseFrameworkAction
	}
	var actionErr error
	if options.Args, actionErr = filter(commandArgs(options.Args)); actionErr != nil {
		configState.parseErr = actionErr
		return actionErr
	}
	var result *configbind.LoadResult
	var err error
	if plan.dotenv != nil {
		result, err = dotenv.Load(options, *plan.dotenv, plan.process)
	} else {
		result, err = configbind.Load(options)
	}
	configState.parseErr = err
	if err != nil {
		return err
	}
	if err := applyConnectionsEnv(options.Environ); err != nil {
		configState.parseErr = err
		return err
	}
	if err := applyBucketsEnv(options.Environ); err != nil {
		configState.parseErr = err
		return err
	}
	DeriveExportEnabled(result, boundConfig[ObservabilityConfig]())
	DeriveTraceSampler(result, boundConfig[ObservabilityConfig](), env)
	if loaded := configState.hooks.Loaded; loaded != nil {
		loaded(result)
	}
	publish = true
	return nil
}

// Parsed reports whether Parse has run, which is what a runtime checks before
// deciding whether it is the one that should perform startup.
func Parsed() bool {
	configState.RLock()
	defer configState.RUnlock()
	return configState.parsed
}

// Snapshot copies every registered binding by value, which is what a request
// capsule carries.
func Snapshot() map[reflect.Type]any {
	configState.RLock()
	defer configState.RUnlock()
	configs := make(map[reflect.Type]any, len(configState.entries))
	for typ, entry := range configState.entries {
		value := reflect.ValueOf(entry.ptr)
		if value.Kind() == reflect.Pointer && !value.IsNil() {
			configs[typ] = value.Elem().Interface()
		}
	}
	return configs
}

// Seed gives a registered binding its documented values before anything parses
// a configuration source.
//
// A reader reports a registered target even while it is unparsed, so a zero
// value there reads as an explicit setting rather than as "not configured yet".
// For HTMLConfig that distinction matters: a zero Streaming would turn
// progressive rendering off in every test and in every embedding that never
// parses, which is the opposite of the documented default.
func Seed[T any](value T) {
	configState.Lock()
	defer configState.Unlock()
	entry, ok := configState.entries[reflect.TypeFor[T]()]
	if !ok {
		return
	}
	if ptr, ok := entry.ptr.(*T); ok && ptr != nil {
		*ptr = value
	}
}

// Bound returns the pointer the registry holds for T, which is the value
// configbind writes into.
//
// Nothing on the serving path uses it: a reader takes a copy through
// pwruntime.ResolveConfig, because a pointer into the registry is a value
// anything could change under a request that already read it. It exists for a
// caller that drives a real load and then has to put back what the process had,
// which is a test and only a test.
func Bound[T any]() (*T, bool) {
	configState.RLock()
	defer configState.RUnlock()
	bound := boundConfig[T]()
	return bound, bound != nil
}

// boundConfig returns the registered binding for T, or nil when none is
// registered. Callers hold configState.
func boundConfig[T any]() *T {
	entry, ok := configState.entries[reflect.TypeFor[T]()]
	if !ok {
		return nil
	}
	bound, _ := entry.ptr.(*T)
	return bound
}

// The resolved configuration is published for every runtime, none of which
// binds its own. It is an init rather than a step inside Parse because the
// closure reads the registry when it is called rather than capturing it, so
// publishing it before anything is parsed is both correct and one less ordering
// constraint.
func init() {
	pwruntime.PublishConfigLookup(func(target reflect.Type) (any, bool) {
		entries := configEntries.Load()
		if entries == nil {
			return nil, false
		}
		entry, ok := (*entries)[target]
		if !ok {
			return nil, false
		}
		return entry.ptr, true
	})
}

// DeriveExportEnabled turns observability.otel.enabled on whenever an endpoint
// arrived from any source.
//
// Naming an endpoint is the OTLP convention for asking for export, and it is how
// api:cli-dev points an application at its viewer. But every other otel key
// declares enabled as its dependon parent, so leaving the switch off would hide
// the very address that was just configured, and the summary would describe a
// process that does not exist.
//
// The value is written into the overlay as well as the bound struct, because the
// startup summary reads provenance rather than the struct. It is recorded at the
// place the endpoint came from, so the summary says who asked for export instead
// of claiming a default did.
//
// bound is passed in rather than looked up so that the function carries no
// locking contract and a test can drive it against its own value.
func DeriveExportEnabled(result *configbind.LoadResult, bound *ObservabilityConfig) {
	if result == nil || result.Overlay == nil {
		return
	}
	endpoint, ok := result.Overlay.Get("observability.otel.endpoint")
	if !ok || strings.TrimSpace(endpoint.Raw) == "" {
		return
	}
	result.Overlay.Set("observability.otel.enabled", "true", endpoint.Place)
	if bound != nil {
		bound.Otel.Enabled = true
	}
}

// Trace sampling defaults, selected by environment rather than fixed.
const (
	// DefaultDevSampler records every trace in development. requirement:
	// dev-request-timing-surface reads its values from the spans the tracer
	// opened, and the telemetry viewer is the developer's only view of a
	// request, so a sampled dev loop is one where the page just looked at is
	// missing.
	DefaultDevSampler = "parentbased_always_on"
	// DefaultDeployedSampler samples every other environment, because the
	// process may be the last stage that can decline a span.
	DefaultDeployedSampler = "parentbased_traceidratio"
	// DefaultDeployedSamplerArg is the fraction kept under that sampler. It is
	// a number the framework had to choose rather than a measured one: one in
	// ten keeps a low-traffic deployment visible in a trace list and removes an
	// order of magnitude from a busy one.
	DefaultDeployedSamplerArg = "0.1"
)

// PlaceEnvironmentDefault marks a value the environment token selected, so the
// startup summary says which default applied rather than implying a file or a
// variable set it.
const PlaceEnvironmentDefault configbind.Place = "default_env"

// DefaultTraceSampler returns the sampler an environment gets when nothing
// configures one.
//
// Development is the only exception, and it is stated as the exception rather
// than as "staging and production sample": the environment token permits
// extension values, and an unfamiliar environment is one somebody added for
// traffic. Spelling it the other way would leave every extension token
// recording everything, which is the expensive branch reached by not thinking
// about it.
func DefaultTraceSampler(env string) (name, argument string) {
	if env == EnvDevelopment {
		return DefaultDevSampler, ""
	}
	return DefaultDeployedSampler, DefaultDeployedSamplerArg
}

// DeriveTraceSampler fills the sampler keys the environment decides, when
// nothing else set them.
//
// It writes into the overlay as well as the bound struct for the reason
// DeriveExportEnabled does: the startup summary reads provenance rather than the
// struct, and a process keeping one trace in ten looks exactly like a broken
// tracer from outside. Naming the place default_env is what tells the operator
// which of the two it is.
func DeriveTraceSampler(result *configbind.LoadResult, bound *ObservabilityConfig, env string) {
	if result == nil || result.Overlay == nil {
		return
	}
	if entry, ok := result.Overlay.Get("observability.trace.sampler"); ok && strings.TrimSpace(entry.Raw) != "" {
		return
	}
	name, argument := DefaultTraceSampler(env)
	result.Overlay.Set("observability.trace.sampler", name, PlaceEnvironmentDefault)
	if bound != nil {
		bound.Trace.Sampler = name
	}
	// The argument is written only when the default carries one, so a dev
	// summary does not report an empty key beside a sampler that takes none.
	if argument == "" {
		return
	}
	if entry, ok := result.Overlay.Get("observability.trace.sampler_arg"); ok && strings.TrimSpace(entry.Raw) != "" {
		return
	}
	result.Overlay.Set("observability.trace.sampler_arg", argument, PlaceEnvironmentDefault)
	if bound != nil {
		bound.Trace.SamplerArg = argument
	}
}

// loadPlan is a load with its environment settled: the options configbind
// reads, the token, and — when the process reads its own environment — the
// dotenv files that environment was composed from.
type loadPlan struct {
	options  configbind.LoadOptions
	env      string
	declared bool
	// dotenv is nil when the caller supplied Environ. A Cloudflare Worker entry
	// and a test harness do that to say no filesystem stands behind them, and a
	// dotenv read there would be a second source nobody asked for.
	dotenv  *dotenv.Layer
	process []string
}

// resolveLoadOptions completes options for the active runtime environment.
// Project-local candidates are environment-specific and searched in the working
// directory before its config/ directory; the user and system configuration
// directories keep the environment-neutral file name.
//
// When nothing supplied Environ, the environment is the process's own, laid
// over the policy:dotenv-resolution files of the working directory: .env, then
// .env.{env}, with the token itself read from the process first and the base
// file second.
func resolveLoadOptions(options configbind.LoadOptions) (loadPlan, error) {
	if options.Tool == "" {
		options.Tool = ExecutableName()
	}
	plan := loadPlan{options: options}
	if options.Environ == nil {
		process := os.Environ()
		secretDirs := options.EnvSecretDirs
		if secretDirs == nil {
			secretDirs = []string{pwenv.SecretDir}
		}
		layer, env, declared, err := dotenv.Resolve(".", process, secretDirs)
		if err != nil {
			return loadPlan{}, err
		}
		plan.dotenv, plan.process = &layer, process
		plan.env, plan.declared = env, declared
		plan.options.Environ = layer.Environ(process)
	} else {
		env, declared, err := pwenv.ResolveDeclared(options.Environ)
		if err != nil {
			return loadPlan{}, err
		}
		plan.env, plan.declared = env, declared
	}
	if plan.options.FileName == "" {
		plan.options.FileName = pwenv.NeutralFileName
	}
	if plan.options.ExtraConfigReadPaths == nil {
		plan.options.ExtraConfigReadPaths = pwenv.ReadPaths(plan.env)
	}
	return plan, nil
}

// DotenvFiles names the policy:dotenv-resolution files and secret directories
// the load read, in the order they were layered, which is what the startup
// summary reports.
func DotenvFiles() []string {
	dotenvState.RLock()
	defer dotenvState.RUnlock()
	return dotenvState.layer.Names()
}

// DotenvWarnings is what the dotenv read noticed and went on from.
func DotenvWarnings() []string {
	dotenvState.RLock()
	defer dotenvState.RUnlock()
	return append([]string(nil), dotenvState.layer.Warnings...)
}

// ExecutableName is the name a configuration search falls back to when the
// caller named no tool.
func ExecutableName() string {
	name := filepath.Base(os.Args[0])
	if name == "" || name == "." {
		return "app"
	}
	return name
}

// commandArgs is the command line as configbind should see it.
//
// A test binary's own flags are dropped, because go test puts them on the same
// line and a configuration parser has no way to know they are not its own.
func commandArgs(configured []string) []string {
	if configured != nil {
		return configured
	}
	args := os.Args[1:]
	if !strings.HasSuffix(os.Args[0], ".test") {
		return args
	}
	filtered := make([]string, 0, len(args))
	for _, arg := range args {
		if !strings.HasPrefix(arg, "-test.") {
			filtered = append(filtered, arg)
		}
	}
	return filtered
}

// Swap replaces one registered binding and returns the pointer it installed
// together with the restore.
//
// It exists because the framework's configuration is process state, and the
// tests that need one setting to differ would otherwise reach into this
// package's map from outside it — which is what they did while the registry
// was in pw and they were in the same package. A seam that restores what it
// replaced is the smaller of the two evils; the alternative is an exported map.
//
// The pointer is handed back because a test that installs a configuration
// often has to adjust one field of it partway through, and reinstalling the
// whole value to change one would be a second seam for the same reason.
func Swap[T any](value T) (*T, func()) {
	configState.Lock()
	defer configState.Unlock()
	typ := reflect.TypeFor[T]()
	previous, existed := configState.entries[typ]
	installed := &value
	configState.entries[typ] = configEntry{prefix: previous.prefix, ptr: installed}
	publishEntriesLocked()
	return installed, func() {
		configState.Lock()
		defer configState.Unlock()
		if existed {
			configState.entries[typ] = previous
		} else {
			delete(configState.entries, typ)
		}
		publishEntriesLocked()
	}
}
