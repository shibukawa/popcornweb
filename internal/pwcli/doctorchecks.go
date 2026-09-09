package pwcli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/shibukawa/popcornweb/internal/assetverify"
	"github.com/shibukawa/popcornweb/internal/dotenv"
	"github.com/shibukawa/popcornweb/internal/pwcheck"
	"github.com/shibukawa/popcornweb/internal/pwenv"
	"github.com/shibukawa/popcornweb/internal/pwmigrate"
)

// checkContext is everything one environment's checks may read. A check that
// needs an input this run could not build is skipped and reported as a limit,
// never guessed at.
type checkContext struct {
	Env     string
	Root    string
	Config  environmentConfig
	Graph   importGraph
	State   projectState
	Scan    *projectScan
	Online  bool
	HostEnv string
}

type checkRun struct {
	checkContext
	findings []doctorFinding
	// unresolved collects what the checks could not attribute, so the report
	// states it rather than reporting a gap it cannot back up.
	unresolved []string
}

// report records a finding, resolving the severity from the diagnosed token. A
// check whose scope excludes this environment is silently skipped, which is how
// one catalog serves dev and prod without a second severity system.
func (r *checkRun) report(id, message, evidence string) {
	check := pwcheck.MustLookup(id)
	if !check.AppliesTo(r.Env) {
		return
	}
	r.findings = append(r.findings, doctorFinding{
		Check:    check,
		Severity: check.SeverityFor(r.Env),
		Message:  message,
		Evidence: evidence,
		Remedy:   check.Remedy,
	})
}

// escalate records a finding at a severity the condition itself justifies,
// regardless of environment. It is used where one check covers a mild form and
// a severe one, such as a cookie that is merely insecure and one that is
// cross-site as well.
func (r *checkRun) escalate(id string, severity pwcheck.Severity, message, evidence string) {
	check := pwcheck.MustLookup(id)
	r.findings = append(r.findings, doctorFinding{
		Check: check, Severity: severity, Message: message, Evidence: evidence, Remedy: check.Remedy,
	})
}

func runChecks(ctx context.Context, context checkContext) ([]doctorFinding, []doctorLimit) {
	run := &checkRun{checkContext: context}
	run.checkProject()
	run.checkAssetContent()
	run.checkPackages(ctx)
	run.checkWiring()
	run.checkDependencies()
	run.checkSecrets()
	run.checkDotenvTemplate()
	run.checkEnvironmentValues()
	run.checkIdentityProvider()
	run.checkStorage(ctx)
	run.checkReadiness()
	routeLimit := run.checkRoutes()
	sortFindings(run.findings)
	var limits []doctorLimit
	if routeLimit != nil {
		limits = append(limits, *routeLimit)
	}
	if len(run.unresolved) > 0 {
		sortStrings(run.unresolved)
		limits = append(limits, doctorLimit{
			Subject: "configuration sections [" + strings.Join(run.unresolved, "], [") + "]",
			Reason:  "no binding this analysis knows declares them; a plugin outside the framework may own them",
			Effect:  "they were neither validated nor reported as unclaimed",
		})
	}
	return run.findings, limits
}

func (r *checkRun) checkProject() {
	if !r.Scan.mainExists || !r.Scan.mainIsPackage {
		r.report(pwcheck.MainPackageMissing,
			fmt.Sprintf("project.main %q is not a directory holding package main", r.State.config.Main),
			"popcornweb.toml project.main")
	}
	if r.databaseConfigured() && r.State.config.Migration.Dir != "" {
		if _, err := os.Stat(filepath.Join(r.Root, filepath.FromSlash(r.State.config.Migration.Dir))); err != nil {
			r.report(pwcheck.MigrationDirMissing,
				fmt.Sprintf("the database is configured but %s does not exist", r.State.config.Migration.Dir),
				"popcornweb.toml migration.dir")
		}
	}
	for _, generated := range r.Scan.generated {
		if generated.Orphan {
			r.report(pwcheck.OrphanGeneratedFile,
				"the source of "+generated.Path+" no longer exists, and the generated registrations still run",
				generated.Path)
			continue
		}
		if generated.Stale {
			r.report(pwcheck.GeneratedOlderThanSrc,
				generated.Path+" is older than "+generated.Source,
				generated.Path)
		}
	}
	if r.Scan.inGitTree && len(r.Scan.generated) > 0 && !r.Scan.ignores("*"+generatedSuffix) {
		untracked := 0
		for _, generated := range r.Scan.generated {
			if !r.Scan.tracked(generated.Path) {
				untracked++
			}
		}
		if untracked > 0 && untracked < len(r.Scan.generated) {
			r.report(pwcheck.GeneratedNotIgnored,
				fmt.Sprintf("%d of %d generated sources are neither committed nor ignored", untracked, len(r.Scan.generated)),
				".gitignore")
		}
	}
	if pinned, ok := r.Scan.devboxVersions["go"]; ok && r.Scan.goDirective != "" && pinned != "latest" {
		if !sameMinorVersion(pinned, r.Scan.goDirective) {
			r.report(pwcheck.GoVersionMismatch,
				"devbox pins go "+pinned+" while go.mod asks for "+r.Scan.goDirective,
				"devbox.json, go.mod")
		}
	}
	if r.State.config.Toolchain == toolchainTinyGo {
		if pinned, ok := r.Scan.devboxVersions["tinygo"]; ok && pinned != "latest" && compareVersions(pinned, tinyGoBaseline) < 0 {
			r.report(pwcheck.TinyGoBaselineUnmet,
				"devbox pins tinygo "+pinned+", below the "+tinyGoBaseline+" baseline",
				"devbox.json")
		}
		if !r.Scan.netdevRegistered {
			r.report(pwcheck.MissingNetdev,
				"no tinygohelper.go blank import of the netdev package; the binary would exit with \"Netdev not set\"",
				"tinygohelper.go")
		}
	}
	if r.State.devbox != "" && os.Getenv("DEVBOX_SHELL_ENABLED") == "" {
		r.report(pwcheck.OutsideDevboxShell,
			"the project declares a devbox environment and this shell is not it",
			"devbox.json")
	}
	if r.Config.raw("session.backend") == "redis" && r.State.devbox != "" && !strings.Contains(r.State.devbox, "valkey@") {
		r.report(pwcheck.DeclaredServiceMissing,
			"session.backend is redis and devbox declares no Valkey service",
			"devbox.json, session.backend")
	}
	if r.State.config.Tailwind.Enabled {
		reasons := []string{}
		// Matched by prefix, because the pin carries the major version in the
		// package name: what pw add writes is tailwindcss_4, and asking for the
		// bare name told every project that took the scaffold's own answer that
		// its toolchain was missing.
		if !r.Scan.devboxPins("tailwindcss") && r.State.devbox != "" {
			reasons = append(reasons, "devbox pins no tailwindcss")
		}
		if input := r.State.config.Tailwind.Input; input != "" {
			if _, err := os.Stat(filepath.Join(r.Root, filepath.FromSlash(input))); err != nil {
				reasons = append(reasons, input+" does not exist")
			}
		}
		if len(reasons) > 0 {
			r.report(pwcheck.TailwindToolchain, strings.Join(reasons, ", "), "popcornweb.toml assets.tailwind")
		}
	}
	if r.Env == pwenv.Development {
		if port := r.Config.raw("server.port"); port != "" {
			if listener, err := net.Listen("tcp", "127.0.0.1:"+port); err != nil {
				r.report(pwcheck.PortUnavailable, "port "+port+" is already bound on loopback", "server.port")
			} else {
				_ = listener.Close()
			}
		}
	}
}

// tinyGoBaseline is the oldest TinyGo the framework is verified against.
const tinyGoBaseline = "0.42"

func (r *checkRun) checkWiring() {
	if !r.Graph.available() {
		return
	}
	if r.Graph.linksPrefix(devIdPPackagePrefix) {
		r.report(pwcheck.DevIdPLinked,
			"the application imports the development identity provider, which authenticates nobody",
			devIdPPackagePrefix)
	}
	if r.Config.enabled("session.enabled") {
		backend := r.Config.raw("session.backend")
		pkg := sessionBackendPackage(backend, r.State.config.Database)
		switch {
		case backend == "cookie" || backend == "dev-volatile" || backend == "dev-persist":
			// Built into pw; no storage plugin import is required.
		case pkg != "" && !r.Graph.links(pkg):
			r.report(pwcheck.MissingSessionPlugin,
				"session.backend is "+backend+" and the application links no plugin registering it",
				"add: import _ \""+pkg+"\"")
		case pkg == "" && backend != "":
			r.report(pwcheck.MissingSessionPlugin,
				"session.backend is "+backend+", which no linked plugin registers",
				"session.backend")
		}
	}
	for _, connection := range r.Config.databaseDSNs() {
		scheme := connection.scheme()
		if scheme == "" {
			continue
		}
		pkg, known := driverPackages[scheme]
		switch {
		case !known:
			r.report(pwcheck.MissingSQLDriver,
				"no known driver package answers the "+scheme+" scheme of connection "+connection.Label,
				connection.Key)
		case !r.Graph.links(pkg):
			r.report(pwcheck.MissingSQLDriver,
				"connection "+connection.Label+" uses the "+scheme+" scheme and the application links no driver for it",
				"add: import _ \""+pkg+"\"")
		}
	}
	for _, section := range r.Config.Sections {
		owner, known := configPrefixOwners[section]
		switch {
		case !known && !containsString(frameworkPrefixes, section):
			// A section this analysis cannot attribute may belong to a
			// third-party plugin, so it is a limit rather than a gap.
			r.unresolved = append(r.unresolved, section)
		case known && !r.Graph.links(owner):
			r.report(pwcheck.UnclaimedConfigKey,
				"the ["+section+"] section is configured and the application links no binding for it",
				"add: import _ \""+owner+"\"")
		}
	}
	if r.Graph.links(authPluginPackage) && !r.Config.enabled("auth.enabled") {
		r.report(pwcheck.UnusedLinkedPlugin,
			"the auth plugin is linked and auth.enabled is false",
			authPluginPackage)
	}
}

func (r *checkRun) checkDependencies() {
	authEnabled := r.Config.enabled("auth.enabled")
	sessionEnabled := r.Config.enabled("session.enabled")
	if authEnabled && !sessionEnabled {
		r.report(pwcheck.AuthWithoutSession,
			"auth.enabled is true and session.enabled is false",
			"session.enabled")
	}
	if authEnabled && sessionEnabled && r.Config.raw("session.backend") == "cookie" {
		// The record travels with the browser, so nothing on the server can
		// withdraw it: a logout expires the client's copy while a copy taken
		// beforehand keeps authenticating to its sealed expiry, and account
		// suspension has the same shape.
		r.report(pwcheck.UnrevocableSession,
			"auth.enabled is true and session.backend is cookie, so a logout cannot end a session already issued",
			"session.backend")
	}
	if sessionEnabled && r.Config.raw("session.backend") == "rdb" &&
		r.Config.raw("session.rdb.source") == "middleware" && !r.Config.enabled("middleware.rdb.enabled") {
		r.report(pwcheck.SessionRDBWithoutMW,
			"the session store reuses middleware.rdb and middleware.rdb.enabled is false",
			"middleware.rdb.enabled")
	}
	if !r.Config.enabled("middleware.rdb.enabled") {
		return
	}
	writable := map[string]bool{}
	groups := map[string]bool{}
	for index := 0; ; index++ {
		group, ok := r.Config.value(fmt.Sprintf("middleware.rdb.connections[%d].group", index))
		if !ok {
			if _, dsnPresent := r.Config.value(fmt.Sprintf("middleware.rdb.connections[%d].dsn", index)); !dsnPresent {
				break
			}
			group.Raw = r.Config.raw("middleware.rdb.default_group")
		}
		name := group.Raw
		if name == "" {
			name = "default"
		}
		groups[name] = true
		readonly, _ := strconv.ParseBool(r.Config.raw(fmt.Sprintf("middleware.rdb.connections[%d].readonly", index)))
		if !readonly {
			writable[name] = true
		}
	}
	if len(groups) == 0 {
		return
	}
	writeGroup := r.Config.raw("middleware.rdb.write_group")
	switch {
	case writeGroup != "" && !writable[writeGroup]:
		r.report(pwcheck.ReadonlyFrameworkDB,
			"middleware.rdb.write_group names "+writeGroup+", which holds no writable connection",
			"middleware.rdb.write_group")
	case writeGroup == "" && len(groups) > 1:
		r.report(pwcheck.ReadonlyFrameworkDB,
			"more than one connection group exists and middleware.rdb.write_group names none of them",
			"middleware.rdb.write_group")
	}
}

// placeholderSecrets are the values a scaffold or a hurried edit leaves behind.
// A scaffold value is published in the framework source, so it is a known
// credential rather than a weak one.
var placeholderSecrets = map[string]bool{
	"changeme": true, "change-me": true, "change_me": true, "secret": true,
	"password": true, "todo": true, "xxx": true, "replace-me": true, "example": true,
	// The secrets published in this repository's own examples. Random-looking
	// or not, a value anyone can read out of the framework's source is a known
	// credential, and a project that copied an example forward should hear so.
	"dev-only-not-a-secret":                        true,
	"JYETkv6QoCX3YYcTQj/DwRzVPgXbJLcry0+i5DvL2Vs=": true,
	"teAl0D8gj0CeAqIlB5aKJfATJ9osTqvNIM9n4gcV79A=": true,
	"TrNuSu4f6F2TIZweewu0m3KBJMEg3KMRYdoWsJHPQbQ=": true,
	"w7pGbK0UFoQhTImvxy9KptkfwkIki7dm94avz1Pi4UY=": true,
	"nD20aGUfD4QnZMPpu+U89HGvNyRiEo/ts6IDUKca2+E=": true,
}

func (r *checkRun) checkSecrets() {
	fromFile := 0
	// A dotenv file exists to hold secrets, so a value read from one is not a
	// disclosure the way a literal in the TOML is. Where the file stands is
	// still the question: tracked by git, or readable beyond its owner.
	dotenvHolding := map[string]bool{}
	for _, key := range r.Config.secretKeys() {
		raw := r.Config.raw(key)
		if !carriesCredential(key, raw) {
			// The classification is by field name, so a DSN naming a local file
			// is secret-classified while carrying no credential to disclose.
			continue
		}
		if placeholderSecrets[strings.ToLower(strings.TrimSpace(raw))] {
			// The value is named only as a class, never printed.
			r.report(pwcheck.PlaceholderSecret, key+" still holds a placeholder value", key)
		}
		if file, ok := r.Config.fromDotenv(key); ok {
			dotenvHolding[file] = true
			continue
		}
		if !r.Config.fromFile(key) {
			continue
		}
		if strings.Contains(raw, "${") {
			// An expansion is a reference to the environment, not a literal.
			continue
		}
		fromFile++
		evidence := key + " in " + r.Config.ConfigPath
		if r.Scan.tracked(r.Config.ConfigPath) {
			evidence += " (tracked by git)"
		}
		r.report(pwcheck.LiteralSecretInFile, key+" is set from the configuration file", evidence)
	}
	holding := make([]string, 0, len(dotenvHolding)+1)
	if fromFile > 0 && r.Config.ConfigPath != "" {
		holding = append(holding, r.Config.ConfigPath)
	}
	for file := range dotenvHolding {
		holding = append(holding, file)
	}
	sortStrings(holding)
	for _, file := range holding {
		r.checkSecretFileStanding(file)
	}
}

// checkSecretFileStanding asks where a file that holds a secret stands: whether
// version control carries it, and whether anyone but its owner can read it.
func (r *checkRun) checkSecretFileStanding(file string) {
	if r.Scan.inGitTree && r.Scan.tracked(file) {
		r.report(pwcheck.SecretFileNotIgnored, file+" holds a secret and is tracked by version control", file)
	}
	if mode, ok := r.Scan.configFileModes[file]; ok && mode&0o077 != 0 {
		r.report(pwcheck.SecretFilePerms, fmt.Sprintf("%s holds a secret and its mode is %04o", file, mode), file)
	}
}

// checkDotenvTemplate reads the committed template, which is the one file of
// the family .gitignore lets through. A value there is a committed credential
// whichever environment is diagnosed, so the check is environment-independent
// and the name is what a reader needs: the value itself is never printed.
func (r *checkRun) checkDotenvTemplate() {
	template, err := dotenv.Read(filepath.Join(r.Root, pwenv.DotenvTemplate), pwenv.DotenvTemplate)
	if err != nil || template == nil {
		// A template that does not parse is not one a load ever reads, so
		// there is nothing here to report about it.
		return
	}
	for _, entry := range template.Entries {
		name := strings.ToLower(entry.Name)
		if !secretVariableName(name) || !carriesCredential(strings.ReplaceAll(name, "_", "."), entry.Value) {
			continue
		}
		r.report(pwcheck.EnvTemplateHoldsSecret,
			entry.Name+" is assigned a value in "+pwenv.DotenvTemplate,
			entry.Name+" in "+pwenv.DotenvTemplate)
	}
}

// secretVariableName mirrors the key-name policy configbind masks by: a
// variable is derived from its key, so the same tokens classify both.
func secretVariableName(name string) bool {
	for _, token := range []string{"password", "secret", "apikey", "api_key", "token", "dsn", "private_key", "credential"} {
		if strings.Contains(name, token) {
			return true
		}
	}
	return false
}

func (r *checkRun) checkEnvironmentValues() {
	if r.Config.enabled("session.enabled") {
		secure, resolved := r.Config.boolValue("session.cookie.secure")
		sameSite := strings.ToLower(r.Config.raw("session.cookie.same_site"))
		switch {
		case resolved && !secure && sameSite == "none":
			r.escalate(pwcheck.InsecureSessionCookie, pwcheck.Error,
				"session.cookie.same_site is none without session.cookie.secure, which no browser accepts as a cross-site cookie",
				"session.cookie.secure")
		case resolved && !secure:
			r.report(pwcheck.InsecureSessionCookie,
				"session.cookie.secure is false, so the session cookie travels over plain http",
				"session.cookie.secure")
		}
	}
	if enabled, resolved := r.Config.boolValue("security.headers.enabled"); resolved && !enabled {
		r.report(pwcheck.ResponseHeadersWeak, "security.headers.enabled is false", "security.headers.enabled")
	}
	r.checkCrossOrigin()
	if r.Config.raw("observability.query.enabled") == "on" {
		r.report(pwcheck.QueryDiagnosticsOn,
			"observability.query.enabled is on, with slow_threshold "+r.Config.raw("observability.query.slow_threshold"),
			"observability.query.enabled")
	}
	if r.Config.raw("observability.query.bind_values") == "on" {
		r.report(pwcheck.BindValuesOn,
			"observability.query.bind_values is on, so application row data enters the SQL records",
			"observability.query.bind_values")
	}
	switch strings.ToLower(r.Config.raw("observability.minimum_level")) {
	case "trace", "debug":
		r.report(pwcheck.VerboseLogLevel,
			"observability.minimum_level is "+r.Config.raw("observability.minimum_level"),
			"observability.minimum_level")
	}
	if strings.EqualFold(r.Config.raw("observability.stdout_format"), "plaintext") {
		r.report(pwcheck.PlaintextLogs, "observability.stdout_format is plaintext", "observability.stdout_format")
	}
	if r.databaseConfigured() {
		for _, connection := range r.Config.databaseDSNs() {
			if strings.Contains(connection.DSN, ":memory:") {
				r.report(pwcheck.MemoryDatabase,
					"connection "+connection.Label+" is an in-memory database, so the schema and every row are lost at restart",
					connection.Key)
			}
		}
	}
	if enabled, resolved := r.Config.boolValue("server.public.read_local"); resolved && enabled {
		r.report(pwcheck.LocalPublicRead, "server.public.read_local is true", "server.public.read_local")
	}
	if enabled, resolved := r.Config.boolValue("observability.otel.enabled"); resolved && !enabled {
		r.report(pwcheck.TelemetryDisabled, "observability.otel.enabled is false", "observability.otel.enabled")
	}
	r.checkAPICatalog()
}

// checkAPICatalog reports the two ways the RFC 9727 endpoint is turned on and
// left unable to answer well.
//
// The first is the refusal the runtime already makes, reported here so it is
// found by reading the configuration rather than by a failed start. The second
// is silent: the catalog answers, and every link in it names whichever host the
// caller asked for.
func (r *checkRun) checkAPICatalog() {
	if enabled, resolved := r.Config.boolValue("server.api_catalog"); !resolved || !enabled {
		return
	}
	if r.Config.raw("server.openapi") == "" && r.Config.raw("server.api_doc") == "" {
		r.report(pwcheck.APICatalogWithoutAPI,
			"server.api_catalog is on with neither server.openapi nor server.api_doc set",
			"server.api_catalog")
	}
	if r.Config.raw("server.api_catalog_origin") == "" {
		r.report(pwcheck.APICatalogRelativeLinks,
			"server.api_catalog_origin is unset, so the catalog's links are relative to wherever it is read from",
			"server.api_catalog_origin")
	}
}

func (r *checkRun) checkIdentityProvider() {
	if r.State.config.IdP.Enabled && r.Env != pwenv.Development {
		r.report(pwcheck.DevIdPEnabled, "dev.idp.enabled is true in popcornweb.toml", "popcornweb.toml dev.idp")
	}
	if !r.Config.enabled("auth.enabled") {
		return
	}
	issuer := r.Config.raw("auth.oidc.issuer")
	allowLoopback := r.Config.enabled("auth.oidc.allow_loopback_http")
	redirect := strings.TrimSpace(r.Config.raw("auth.oidc.redirect_url"))
	// The redirect target is checked in every environment: the provider would
	// send the browser to a path the application does not serve, and a loopback
	// redirect that happens to work locally hides it.
	if redirect != "" {
		callback := r.Config.raw("auth.callback_path")
		if parsed, err := url.Parse(redirect); err == nil && callback != "" && parsed.Path != callback {
			r.report(pwcheck.RedirectDisagreement,
				"auth.oidc.redirect_url ends at "+parsed.Path+" while auth.callback_path is "+callback,
				"auth.oidc.redirect_url")
		}
	}
	// In dev the provider values are expected to be absent from the file:
	// pw dev runs the development identity provider and injects them. Nothing
	// below has anything to say about that arrangement working.
	if r.Env == pwenv.Development {
		return
	}
	if parsed, err := url.Parse(redirect); redirect == "" || err != nil || !parsed.IsAbs() || parsed.Host == "" {
		r.report(pwcheck.DynamicOIDCRedirect,
			"auth.oidc.redirect_url is empty or path-only, so its host would come from the request",
			"auth.oidc.redirect_url")
	}
	if issuer != "" {
		if parsed, err := url.Parse(issuer); err == nil {
			if isLoopbackHost(parsed.Hostname()) {
				r.report(pwcheck.DevelopmentIssuer,
					"auth.oidc.issuer is a loopback address, which is a development provider",
					"auth.oidc.issuer")
			}
			if parsed.Scheme == "http" {
				r.report(pwcheck.InsecureIssuer,
					"auth.oidc.issuer uses http; the discovery document and every token would travel in the clear",
					"auth.oidc.issuer")
			}
		}
	}
	if allowLoopback {
		r.report(pwcheck.InsecureIssuer,
			"auth.oidc.allow_loopback_http is true, which is a development-only exception",
			"auth.oidc.allow_loopback_http")
		if secure, resolved := r.Config.boolValue("session.cookie.secure"); resolved && !secure {
			r.report(pwcheck.LoopbackPairing,
				"allow_loopback_http and an insecure session cookie are the development pairing",
				"auth.oidc.allow_loopback_http, session.cookie.secure")
		}
	}
	var missing []string
	for key, variable := range map[string]string{
		"auth.oidc.issuer":        "AUTH_OIDC_ISSUER",
		"auth.oidc.client_id":     "AUTH_OIDC_CLIENT_ID",
		"auth.oidc.client_secret": "AUTH_OIDC_CLIENT_SECRET",
	} {
		if strings.TrimSpace(r.Config.raw(key)) == "" {
			missing = append(missing, variable)
		}
	}
	if len(missing) > 0 {
		sortStrings(missing)
		// Absent here is either platform injection or a real gap, and this host
		// cannot tell which; naming what must be set is the useful half.
		r.report(pwcheck.ProviderNotDeclared,
			"authentication is enabled and no provider values are declared for "+r.Env,
			"the deployment must set "+strings.Join(missing, ", "))
	}
}

func (r *checkRun) checkStorage(ctx context.Context) {
	if !r.databaseConfigured() {
		return
	}
	versions := map[int]string{}
	var ordered []int
	for _, name := range r.State.migrations {
		version, _, found := strings.Cut(name, "_")
		number, err := strconv.Atoi(version)
		if !found || err != nil {
			continue
		}
		if existing, clash := versions[number]; clash {
			r.report(pwcheck.DuplicateMigration,
				"migrations "+existing+" and "+name+" share version "+version,
				r.State.config.Migration.Dir)
			continue
		}
		versions[number] = name
		ordered = append(ordered, number)
	}
	sortInts(ordered)
	for index := 1; index < len(ordered); index++ {
		if ordered[index] != ordered[index-1]+1 {
			r.report(pwcheck.MigrationVersionGap,
				fmt.Sprintf("the migration sequence jumps from %05d to %05d", ordered[index-1], ordered[index]),
				r.State.config.Migration.Dir)
			break
		}
	}
	for _, connection := range r.Config.databaseDSNs() {
		if connection.scheme() != "sqlite" {
			continue
		}
		path := strings.TrimPrefix(connection.DSN, "sqlite://")
		if path == "" || strings.HasPrefix(path, ":memory:") {
			continue
		}
		if !filepath.IsAbs(path) {
			path = filepath.Join(r.Root, filepath.FromSlash(path))
		}
		directory := filepath.Dir(path)
		if _, err := os.Stat(directory); err != nil {
			r.report(pwcheck.DatabasePathUnwrite,
				"connection "+connection.Label+" would open "+relativeToRoot(r.Root, path)+" and its directory does not exist",
				connection.Key)
		}
	}
	if !r.Online {
		r.report(pwcheck.AppliedStateUnknown,
			"the applied migration state was not read, so the pending count is unknown",
			"--online")
		return
	}
	r.checkDatabaseOnline(ctx)
}

// checkDatabaseOnline connects to the configured database. It runs only under
// --online, uses the goose linkage pw migrate already carries, and applies
// nothing.
func (r *checkRun) checkDatabaseOnline(ctx context.Context) {
	connections := r.Config.databaseDSNs()
	if len(connections) == 0 {
		return
	}
	for _, connection := range connections {
		if absent, path := absentLocalDatabase(r.Root, connection); absent {
			// Opening a sqlite DSN creates the file, and a diagnosis that
			// writes is not one. The absence is reported instead.
			r.report(pwcheck.ConnectionFailed,
				"connection "+connection.Label+" points at "+path+", which does not exist yet",
				connection.Key)
			continue
		}
		target, err := pwmigrate.Open(connection.DSN)
		if err != nil {
			r.report(pwcheck.ConnectionFailed,
				"connection "+connection.Label+" could not be opened: "+err.Error(),
				connection.Key)
			continue
		}
		if err := target.DB.PingContext(ctx); err != nil {
			_ = target.Close()
			r.report(pwcheck.ConnectionFailed,
				"connection "+connection.Label+" did not answer: "+err.Error(),
				connection.Key)
			continue
		}
		_ = target.Close()
	}
	migrationDSN := r.migrationDSN(connections)
	if migrationDSN == "" || r.State.config.Migration.Dir == "" {
		return
	}
	if absent, _ := absentLocalDatabase(r.Root, labeledDSN{DSN: migrationDSN}); absent {
		return
	}
	sources, err := pwmigrate.Sources(filepath.Join(r.Root, filepath.FromSlash(r.State.config.Migration.Dir)))
	if err != nil {
		return
	}
	target, err := pwmigrate.Open(migrationDSN)
	if err != nil {
		return
	}
	defer func() { _ = target.Close() }()
	statuses, err := pwmigrate.Statuses(ctx, target, sources)
	if err != nil {
		r.report(pwcheck.ConnectionFailed,
			"the applied migration state could not be read: "+err.Error(),
			"middleware.rdb.migration_group")
		return
	}
	pending := 0
	for _, status := range statuses {
		if !status.Applied {
			pending++
		}
	}
	if pending > 0 {
		message := fmt.Sprintf("%d migration(s) are not applied", pending)
		if !r.State.config.Migration.Auto {
			message += "; migration.auto is false, so pw dev does not apply them either"
		}
		r.report(pwcheck.PendingMigrations, message, r.State.config.Migration.Dir)
	}
}

// migrationDSN picks the connection migrations run against, following the
// configured migration group and then the write group.
func (r *checkRun) migrationDSN(connections []labeledDSN) string {
	group := r.Config.raw("middleware.rdb.migration_group")
	if group == "" {
		group = r.Config.raw("middleware.rdb.write_group")
	}
	if group == "" {
		return connections[0].DSN
	}
	for index, connection := range connections {
		name := r.Config.raw(fmt.Sprintf("middleware.rdb.connections[%d].group", index))
		if name == group {
			return connection.DSN
		}
	}
	return connections[0].DSN
}

func (r *checkRun) checkReadiness() {
	// server.api_doc names a renderer rather than a flag, so anything but the
	// empty or off value serves the page.
	switch renderer := strings.ToLower(strings.TrimSpace(r.Config.raw("server.api_doc"))); renderer {
	case "", "off", "none", "false":
	default:
		r.report(pwcheck.APIDocExposed,
			"server.api_doc is "+renderer+", so "+r.Config.raw("server.api_doc_path")+" is served",
			"server.api_doc")
	}
	tailwind := r.State.config.Tailwind
	if !tailwind.Enabled {
		return
	}
	if !tailwind.Minify {
		r.report(pwcheck.TailwindMinifyOff, "assets.tailwind.minify is false", "popcornweb.toml assets.tailwind.minify")
	}
	if tailwind.Output != "" && tailwind.Input != "" {
		output := filepath.Join(r.Root, filepath.FromSlash(tailwind.Output))
		if olderThan(output, []string{filepath.Join(r.Root, filepath.FromSlash(tailwind.Input))}) {
			r.report(pwcheck.StylesheetStale,
				tailwind.Output+" is older than "+tailwind.Input,
				tailwind.Output)
		}
	}
	r.checkDerivedTree()
	r.checkImageTools()
}

// checkDerivedTree reports a served tree older than what it was built from.
//
// The tree under dist is produced, so a stale one means the last build predates
// an edit and the application would embed bytes nobody asked for.
func (r *checkRun) checkDerivedTree() {
	authored := filepath.Join(r.Root, "public")
	output := filepath.Join(r.Root, filepath.FromSlash(derivedPublicDir))
	if _, err := os.Stat(output); err != nil {
		r.report(pwcheck.PrecompressionOld, "dist/public has not been built", "public")
		return
	}
	var sources []string
	_ = filepath.WalkDir(authored, func(path string, entry os.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return nil
		}
		sources = append(sources, path)
		return nil
	})
	if len(sources) == 0 {
		return
	}
	manifest := filepath.Join(r.Root, assetManifestFile)
	if _, err := os.Stat(manifest); err != nil {
		r.report(pwcheck.PrecompressionOld, "the asset manifest has not been generated", "public")
		return
	}
	if olderThan(manifest, sources) {
		r.report(pwcheck.PrecompressionOld,
			"an authored asset is newer than the built tree", relativeToRoot(r.Root, manifest))
	}
}

// checkImageTools reports a project that asked for image conversion on a
// machine that cannot do it.
//
// Nothing is installed implicitly, so the conversion declines and the authored
// image ships as written. The page is correct and larger than it should be,
// which is why this is a warning rather than a failure.
func (r *checkRun) checkImageTools() {
	assets := r.State.config.Assets
	if !assets.Images {
		return
	}
	if missing := missingImageEncoders(assets.AVIF); len(missing) > 0 {
		r.report(pwcheck.ImageToolMissing,
			fmt.Sprintf("images will be served unconverted: no encoder for %s", strings.Join(missing, " or ")),
			"popcornweb.toml assets.images")
	}
}

// checkAssetContent reports an authored public file that is not the kind of
// file its name claims, and an SVG carrying something that executes.
//
// pw build fails on both conditions. This is the form that reports without a
// build, and the only one that sees the tree server.public.read_local serves,
// which no build ever validated.
func (r *checkRun) checkAssetContent() {
	options := verifyOptions(r.State.config.Assets)
	if !options.Signature && !options.SVGScan {
		return
	}
	authored := filepath.Join(r.Root, "public")
	if info, err := os.Stat(authored); err != nil || !info.IsDir() {
		return
	}
	_ = filepath.WalkDir(authored, func(name string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil || entry.IsDir() {
			return nil
		}
		relative, err := filepath.Rel(authored, name)
		if err != nil {
			return nil
		}
		slashed := filepath.ToSlash(relative)
		if hasSidecarSuffix(slashed) {
			// A sidecar here is left over from a previous build. It is the
			// build's own output wherever it came from, and brotli has no
			// signature at all, so judging one would only produce noise.
			return nil
		}
		if info, statErr := entry.Info(); statErr == nil {
			r.reportEmbeddedSize(slashed, info.Size())
		}
		content, err := leadingBytes(name, assetverify.IsSVG(slashed))
		if err != nil {
			// An unreadable file is a different finding than a mislabelled
			// one, and not this check's to make.
			return nil
		}
		finding, refused := assetverify.File(slashed, content, options)
		if !refused {
			return nil
		}
		id := pwcheck.AssetTypeMismatch
		if finding.Kind == assetverify.ActiveContent {
			id = pwcheck.AssetActiveSVG
		}
		r.report(id, finding.Message(), path.Join("public", slashed))
		return nil
	})
}

// embeddedSizeAdvice is where a file stops being page furniture and starts
// being a payload. It is a round number rather than a measured one: hero
// images, fonts, and stylesheets sit far below it, and the media that sits
// above it is media, so nothing hinges on the exact value.
const embeddedSizeAdvice = 4 << 20

// alreadyCompactExtensions are the kinds the build already declines to convert
// or precompress. A file of one of these gains nothing from being embedded, so
// it is the only kind worth pointing at the second tree.
//
// A large .json or .csv is deliberately absent: those compress, so the embedded
// tree is doing real work for them and moving one there would lose the sidecar.
var alreadyCompactExtensions = map[string]bool{
	".mp4": true, ".m4v": true, ".mov": true, ".webm": true,
	".m4a": true, ".mp3": true, ".ogg": true, ".oga": true, ".ogv": true,
	".wav": true, ".flac": true, ".avi": true,
	".zip": true, ".gz": true, ".zst": true, ".zstd": true, ".7z": true, ".tar": true,
	".pdf": true, ".wasm": true,
}

// reportEmbeddedSize points a large media file at the tree that would not
// compile it into the binary.
//
// It never moves anything. Placement decides where bytes live, so a threshold
// here only decides whether to speak; one that decided the location would make
// the same asset embedded in one build and external in the next, which is the
// behaviour requirement:external-public-assets exists to avoid.
func (r *checkRun) reportEmbeddedSize(name string, size int64) {
	if size < embeddedSizeAdvice || !alreadyCompactExtensions[strings.ToLower(path.Ext(name))] {
		return
	}
	r.report(pwcheck.AssetEmbeddedLarge,
		fmt.Sprintf("public/%s is %d MiB and is compiled into the binary", name, size>>20),
		path.Join("public", name))
}

// leadingBytes reads only what a verdict can depend on.
//
// Every signature fits in the window, so a tree of large images costs a few
// bytes per file here. Only the SVG scan needs the whole file, because a script
// element can sit anywhere in it.
func leadingBytes(name string, whole bool) ([]byte, error) {
	if whole {
		return os.ReadFile(name)
	}
	file, err := os.Open(name)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	buffer := make([]byte, assetverify.Window)
	read, err := io.ReadFull(file, buffer)
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF) {
		return nil, err
	}
	return buffer[:read], nil
}

func (r *checkRun) databaseConfigured() bool {
	return r.Config.enabled("middleware.rdb.enabled")
}

// absentLocalDatabase reports a sqlite file DSN whose file is not there yet,
// with the path as the report would show it. An in-memory DSN is never absent:
// it exists for the length of the connection that opens it.
func absentLocalDatabase(root string, connection labeledDSN) (bool, string) {
	if connection.scheme() != "sqlite" {
		return false, ""
	}
	path := strings.TrimPrefix(strings.TrimSpace(connection.DSN), "sqlite://")
	if path == "" || strings.HasPrefix(path, ":memory:") || strings.Contains(path, "mode=memory") {
		return false, ""
	}
	resolved := path
	if !filepath.IsAbs(resolved) {
		resolved = filepath.Join(root, filepath.FromSlash(resolved))
	}
	if _, err := os.Stat(resolved); err == nil {
		return false, ""
	}
	return true, relativeToRoot(root, resolved)
}

// carriesCredential reports whether a secret-classified value actually holds
// something worth disclosing. Classification is by field name, so it marks
// every DSN; a sqlite path or a credential-free URL has nothing to leak, and
// reporting it would train a reader to ignore the finding that matters.
func carriesCredential(key, raw string) bool {
	value := strings.TrimSpace(raw)
	if value == "" {
		return false
	}
	if !strings.HasSuffix(key, ".dsn") && !strings.Contains(key, "dsn") {
		return true
	}
	parsed, err := url.Parse(value)
	if err != nil {
		return true
	}
	if parsed.User != nil {
		if _, hasPassword := parsed.User.Password(); hasPassword || parsed.User.Username() != "" {
			return true
		}
	}
	for _, name := range []string{"password", "pass", "token", "secret", "auth"} {
		if parsed.Query().Get(name) != "" {
			return true
		}
	}
	return false
}

func isLoopbackHost(host string) bool {
	if host == "localhost" {
		return true
	}
	if address := net.ParseIP(host); address != nil {
		return address.IsLoopback()
	}
	return false
}

// sameMinorVersion compares the first two components, which is the granularity
// a toolchain pin and a go directive agree at.
func sameMinorVersion(a, b string) bool {
	trim := func(value string) string {
		parts := strings.Split(strings.TrimPrefix(value, "v"), ".")
		if len(parts) > 2 {
			parts = parts[:2]
		}
		return strings.Join(parts, ".")
	}
	return trim(a) == trim(b)
}

func sortInts(values []int) {
	for index := 1; index < len(values); index++ {
		for position := index; position > 0 && values[position] < values[position-1]; position-- {
			values[position], values[position-1] = values[position-1], values[position]
		}
	}
}

// checkCrossOrigin reports the two cross-origin arrangements that start fine
// and fail in a browser.
//
// Neither can be a startup refusal: both are legal configurations somebody may
// have meant, and both produce a failure whose only account is a console
// message in a visitor's browser. That is what a diagnostic is for.
func (r *checkRun) checkCrossOrigin() {
	if !r.Config.enabled("security.cors.enabled") {
		return
	}
	origins := configList(r.Config.raw("security.cors.allowed_origins"))
	// The two lists answer different questions — who may read a response, and
	// whose unsafe request the origin comparison accepts — and a credentialed
	// deployment needs both. With only the first, every write from that origin
	// is refused, and the browser reports the 403 as a cross-origin failure, so
	// the deployment goes looking in the wrong policy.
	if r.Config.enabled("security.cors.allow_credentials") && r.Config.enabled("security.csrf.enabled") {
		trusted := configList(r.Config.raw("security.csrf.trusted_origins"))
		for _, origin := range origins {
			if !slices.Contains(trusted, origin) {
				r.report(pwcheck.CORSCredentialedOrigin,
					origin+" may read this deployment with credentials and is not in security.csrf.trusted_origins, so its unsafe requests are refused",
					"security.cors.allowed_origins")
				break
			}
		}
	}
	for _, origin := range origins {
		if strings.HasPrefix(origin, "http://") {
			r.report(pwcheck.CORSPlaintextOrigin,
				origin+" is admitted over plain http",
				"security.cors.allowed_origins")
			break
		}
	}
}

// configList splits a rendered list value into its entries. The doctor reads
// configuration as text, so a list arrives in whatever shape the source wrote
// it, and only the entries matter here.
func configList(raw string) []string {
	entries := strings.FieldsFunc(raw, func(r rune) bool {
		return r == '[' || r == ']' || r == ',' || r == '"' || r == '\'' || r == ' '
	})
	for index, entry := range entries {
		entries[index] = strings.TrimSpace(entry)
	}
	return entries
}
