package pwcli

import (
	"encoding/base64"
	"github.com/shibukawa/tinybind-go/minitoml"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/shibukawa/popcornweb/internal/pwenv"
	"github.com/shibukawa/popcornweb/internal/pwmigrate"
	"github.com/shibukawa/popcornweb/plugin/auth"
	"github.com/shibukawa/popcornweb/sessionstore"
)

func TestParseInitArgsRejectsAnUnknownAuthMode(t *testing.T) {
	if _, err := parseInitArgs([]string{"demo", "--auth=basic"}); err == nil ||
		!strings.Contains(err.Error(), "--auth must be") {
		t.Fatalf("err = %v", err)
	}
}

func TestScaffoldWiresTheLocalEmulator(t *testing.T) {
	files := scaffoldFiles(initOptions{Name: "demo", TinyGo: true, Auth: authOIDC, AuthEmulator: true})

	project := files["popcornweb.toml"]
	if !strings.Contains(project, "[dev.idp]") || !strings.Contains(project, "enabled = true") {
		t.Fatalf("popcornweb.toml = %q", project)
	}
	roster, ok := files[defaultIdPConfig]
	if !ok {
		t.Fatalf("%s was not scaffolded", defaultIdPConfig)
	}
	if !strings.Contains(roster, "[users.admin]") {
		t.Fatalf("roster = %q", roster)
	}
	config := files[pwenv.FileName(pwenv.Development)]
	if !strings.Contains(config, `mode = "oidc_only"`) {
		t.Fatalf("config = %q", config)
	}
	// pw dev supplies the provider values, so the committed file must not
	// carry an issuer or a client credential at all.
	for _, key := range []string{"issuer =", "client_id =", "client_secret ="} {
		if strings.Contains(config, key) {
			t.Fatalf("config declares %s even though pw dev injects it:\n%s", key, config)
		}
	}
	for _, expected := range []string{"AUTH_OIDC_ISSUER", "allow_loopback_http = true"} {
		if !strings.Contains(config, expected) {
			t.Fatalf("config is missing %s:\n%s", expected, config)
		}
	}
}

func TestScaffoldWiresAnExternalProvider(t *testing.T) {
	files := scaffoldFiles(initOptions{Name: "demo", TinyGo: true, Auth: authOIDC})

	if strings.Contains(files["popcornweb.toml"], "[dev.idp]") {
		t.Fatal("an external provider must not enable the development identity provider")
	}
	if _, ok := files[defaultIdPConfig]; ok {
		t.Fatalf("%s must not be scaffolded for an external provider", defaultIdPConfig)
	}
	config := files[pwenv.FileName(pwenv.Development)]
	for _, expected := range []string{
		`issuer = ""`, `client_id = ""`, `client_secret = ""`, "allow_loopback_http = false",
	} {
		if !strings.Contains(config, expected) {
			t.Fatalf("config is missing %s:\n%s", expected, config)
		}
	}
}

func TestScaffoldWiresTheFrameworkOwnedEndpoints(t *testing.T) {
	files := scaffoldFiles(initOptions{Name: "demo", TinyGo: true, Auth: authOIDC, AuthEmulator: true})

	// Registering the account seams is the whole application-side wiring: it
	// also imports plugin/auth, whose extensions serve the endpoints.
	main := files["cmd/demo/main.go"]
	if !strings.Contains(main, "handlers.RegisterAccounts()") {
		t.Fatalf("main.go does not install the account seams:\n%s", main)
	}
	resolver, ok := files["handlers/accounts.go"]
	if !ok || !strings.Contains(resolver, "auth.SetAccountResolver(resolveAccount)") {
		t.Fatalf("accounts.go = %q", resolver)
	}
	// The framework tables come from the packages that own them.
	if files["migrations/00002_"+sessionstore.MigrationName+".sql"] != mustSessionMigration() {
		t.Fatal("the scaffolded session migration is not the one sessionstore/sqlite publishes")
	}
	if files["migrations/00003_"+auth.MigrationName+".sql"] != mustAuthMigration() {
		t.Fatal("the scaffolded auth migration is not the one plugin/auth publishes")
	}

	config := files[pwenv.FileName(pwenv.Development)]
	for _, expected := range []string{
		"[session]", `backend = "dev-volatile"`, "cookie.secure = false",
		`post_login_path = "/"`, "protection.unauthenticated", `logout_scope = "reconfirm"`,
	} {
		if !strings.Contains(config, expected) {
			t.Fatalf("config is missing %s:\n%s", expected, config)
		}
	}
	// The starter page reads the account and signs out with a POST form.
	handler := files["handlers/home_handler.go"]
	if !strings.Contains(handler, "auth.User(r.Context())") {
		t.Fatalf("home handler does not read the session:\n%s", handler)
	}
	template := files["handlers/home.pw.html"]
	if !strings.Contains(template, `<form method="post" action="{logoutPath}">`) {
		t.Fatalf("logout must be a POST form:\n%s", template)
	}
	if !strings.Contains(template, `href="{loginPath}">Sign in</a>`) {
		t.Fatalf("no login link:\n%s", template)
	}
}

// The scaffolded keys must be registered, otherwise the generated project fails
// to start on an unknown key.
//
// The scan runs from [session] to the end of the file, so it covers every
// section the scaffold appends after that point, not only the auth ones.
func TestScaffoldedAuthKeysAreRegistered(t *testing.T) {
	known := map[string]bool{
		// [session]
		"enabled": true, "backend": true, "ttl": true, "idle_timeout": true,
		"cookie.name": true, "cookie.secure": true, "rdb.source": true,
		// [auth]
		"mode": true, "post_login_path": true,
		"protection.include": true, "protection.unauthenticated": true,
		"recent_auth_max_age": true, "registration.policy": true, "recovery.policy": true,
		"bootstrap.issue_ttl": true, "bootstrap.enrollment_ttl": true, "bootstrap.max_attempts": true,
		// [auth.passkey]
		"rp_id": true, "rp_name": true, "origins": true,
		"user_verification": true, "discoverable": true,
		// [auth.oidc]
		"issuer": true, "client_id": true, "client_secret": true,
		"redirect_url": true, "scopes": true, "identity_claim": true,
		"admission": true, "auto_provision": true,
		"logout_scope": true, "allow_loopback_http": true,
		// [session]
		"keyring.secret": true,
		// [auth], where every session lifetime is declared
		"session.ttl": true, "session.idle_timeout": true,
		// [security]
		"csrf.enabled": true, "csrf.include": true, "csrf.exclude": true,
	}
	for _, mode := range []string{authOIDC, authOIDCPasskey, authPasskey} {
		t.Run(mode, func(t *testing.T) {
			config := scaffoldFiles(initOptions{Name: "demo", Auth: mode})[pwenv.FileName(pwenv.Development)]
			section := config[strings.Index(config, "[session]"):]
			for _, line := range strings.Split(section, "\n") {
				line = strings.TrimSpace(line)
				if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "[") {
					continue
				}
				key, _, found := strings.Cut(line, "=")
				if !found {
					t.Fatalf("unparsable line %q", line)
				}
				if !known[strings.TrimSpace(key)] {
					t.Fatalf("scaffolded key %q is not registered by the plugins", strings.TrimSpace(key))
				}
			}
		})
	}
}

// passkey_only has no provider, so it scaffolds neither an OIDC section nor an
// identity provider roster, and it must choose both lifecycle policies.
func TestScaffoldPasskeyOnly(t *testing.T) {
	files := scaffoldFiles(initOptions{Name: "demo", Auth: authPasskey, AuthEmulator: true})
	config := files[pwenv.FileName(pwenv.Development)]
	for _, want := range []string{
		`mode = "passkey_only"`, `registration.policy = "administrator"`,
		`recovery.policy = "administrator"`, `rp_id = "localhost"`,
	} {
		if !strings.Contains(config, want) {
			t.Fatalf("config is missing %q:\n%s", want, config)
		}
	}
	if strings.Contains(config, "[auth.oidc]") {
		t.Fatal("passkey_only reads no OIDC setting, so the scaffold must write none")
	}
	if _, ok := files[defaultIdPConfig]; ok {
		t.Fatal("passkey-only must not scaffold an identity provider roster")
	}
	accounts := files["handlers/accounts.go"]
	for _, want := range []string{"SetAccountLookup", "SetAccountActivator", "IssueBootstrapCredential"} {
		if !strings.Contains(accounts, want) {
			t.Fatalf("handlers/accounts.go is missing %q:\n%s", want, accounts)
		}
	}
	if !strings.Contains(files["public/passkey.js"], "redeemBootstrap") {
		t.Fatal("passkey_only needs the browser side of the bootstrap redemption")
	}
}

// oidc_passkey serves both, so it carries the provider registration and the
// relying-party registration together.
func TestScaffoldOIDCPasskey(t *testing.T) {
	files := scaffoldFiles(initOptions{Name: "demo", Auth: authOIDCPasskey, AuthEmulator: true})
	config := files[pwenv.FileName(pwenv.Development)]
	for _, want := range []string{
		`mode = "oidc_passkey"`, "[auth.oidc]", "[auth.passkey]",
		`recovery.policy = "oidc"`,
		`# redirect_url is derived from the loopback request Host.`,
	} {
		if !strings.Contains(config, want) {
			t.Fatalf("config is missing %q:\n%s", want, config)
		}
	}
	if strings.Contains(config, "registration.policy") {
		t.Fatal("oidc_passkey lets registration default to the provider login")
	}
	accounts := files["handlers/accounts.go"]
	if !strings.Contains(accounts, "SetAccountResolver") || !strings.Contains(accounts, "SetAccountLookup") {
		t.Fatalf("oidc_passkey needs both account seams:\n%s", accounts)
	}
	if strings.Contains(accounts, "SetAccountActivator") {
		t.Fatal("only passkey_only activates a provisional account")
	}
	if _, ok := files[defaultIdPConfig]; !ok {
		t.Fatal("oidc_passkey with the emulator needs an identity provider roster")
	}
}

// Only a mode that mounts a ceremony endpoint needs the browser side.
func TestBrowserScaffoldFollowsTheMode(t *testing.T) {
	for mode, want := range map[string]bool{
		authNone: false, authOIDC: false, authOIDCPasskey: true, authPasskey: true,
	} {
		_, ok := scaffoldFiles(initOptions{Name: "demo", Auth: mode})["public/passkey.js"]
		if ok != want {
			t.Fatalf("auth %s scaffolded passkey.js = %v, want %v", mode, ok, want)
		}
	}
}

func TestScaffoldWritesNoAuthSectionForNone(t *testing.T) {
	files := scaffoldFiles(initOptions{Name: "demo", Auth: authNone})
	if strings.Contains(files[pwenv.FileName(pwenv.Development)], "[auth]") {
		t.Fatal("auth none must not write an [auth] section")
	}
	if _, ok := files["handlers/accounts.go"]; ok {
		t.Fatal("auth none must not scaffold a resolver")
	}
}

func TestInitWizardAsksForTheProviderOnlyForOIDC(t *testing.T) {
	t.Chdir(t.TempDir())
	model := startWizard(t, presetManual, "demo", defaultInitOptions())
	// Without a login there is no provider to choose and no store question to
	// answer either, so both rows are absent before the mode is chosen.
	if hubRow(model, "OIDC provider") {
		t.Fatal("the provider row is on the list before a mode that uses one was chosen")
	}
	model = answerHubRow(t, model, "Authentication", 2) // OIDC
	// A login has to keep its sessions somewhere, so the store row appears
	// with it and the whether-a-database row leaves.
	if !hubRow(model, "Store") || hubRow(model, "Database") {
		t.Fatalf("choosing a login did not swap whether for which: rows are %v", hubLabels(model))
	}
	if !hubRow(model, "Session storage") {
		t.Fatal("choosing OIDC must ask where the session lives")
	}
	if !hubRow(model, "OIDC provider") {
		t.Fatalf("the provider row is missing: rows are %v", hubLabels(model))
	}
	model = answerHubRow(t, model, "OIDC provider", 1) // Local emulator
	options := wizardResult(model, defaultInitOptions())
	if options.Auth != authOIDC || !options.AuthEmulator {
		t.Fatalf("options = %#v", options)
	}
}

// oidc_passkey still needs a provider, so the list keeps carrying one.
func TestInitWizardAsksForTheProviderForOIDCPasskey(t *testing.T) {
	t.Chdir(t.TempDir())
	model := startWizard(t, presetManual, "demo", defaultInitOptions())
	model = answerHubRow(t, model, "Authentication", 3) // OIDC and passkey
	if !hubRow(model, "OIDC provider") {
		t.Fatalf("the provider row is missing for a mode that uses one: rows are %v", hubLabels(model))
	}
}

func TestInitWizardSkipsTheProviderStepWithoutOIDC(t *testing.T) {
	t.Chdir(t.TempDir())
	seeded := initOptions{TinyGo: true, Devbox: true, Database: true, Engine: engineSQLite, Redis: true, Auth: authOIDC, AuthEmulator: true}
	model := startWizard(t, presetManual, "demo", seeded)
	model = answerHubRow(t, model, "Authentication", 4) // Passkey only
	if hubRow(model, "OIDC provider") {
		t.Fatalf("the provider row survived a mode with no provider: rows are %v", hubLabels(model))
	}
	// The seeded emulator answer must not survive a mode that has no provider.
	options := wizardResult(model, seeded)
	if options.Auth != authPasskey || options.AuthEmulator {
		t.Fatalf("options = %#v", options)
	}
}

// TestInitWizardDropsARowThatStoppedApplying is the hub's version of walking
// past a skipped question: an answer that removes a row takes that row's
// answer with it, so a question the project never reached cannot leak into it.
func TestInitWizardDropsARowThatStoppedApplying(t *testing.T) {
	t.Chdir(t.TempDir())
	model := startWizard(t, presetManual, "demo", defaultInitOptions())
	model = answerHubRow(t, model, "Authentication", 2) // OIDC, which asks for a provider
	model = answerHubRow(t, model, "OIDC provider", 1)  // Local emulator
	if !wizardResult(model, defaultInitOptions()).AuthEmulator {
		t.Fatal("the provider answer did not reach the options")
	}
	model = answerHubRow(t, model, "Authentication", 1) // back to None
	if hubRow(model, "OIDC provider") {
		t.Fatalf("the provider row outlived the mode that asked for it: rows are %v", hubLabels(model))
	}
	if options := wizardResult(model, defaultInitOptions()); options.AuthEmulator {
		t.Fatalf("a removed row left its answer behind: %#v", options)
	}
}

// TestScaffoldedMigrationsApply guards the version ranges of the scaffold. The
// framework packages own everything below 00010, so an application migration
// that reused one of those versions made a fresh project fail its very first
// pw migrate up with a duplicate version.
func TestScaffoldedMigrationsApply(t *testing.T) {
	for _, mode := range []string{authNone, authOIDC} {
		files := scaffoldFiles(initOptions{Name: "demo", TinyGo: true, Database: true, Auth: mode, AuthEmulator: usesOIDC(mode)})
		directory := t.TempDir()
		for path, content := range files {
			name, ok := strings.CutPrefix(path, "migrations/")
			if !ok {
				continue
			}
			if err := os.WriteFile(filepath.Join(directory, name), []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		sources, err := pwmigrate.Sources(directory)
		if err != nil {
			t.Fatalf("auth=%s: %v", mode, err)
		}
		target, err := pwmigrate.Open("sqlite://" + filepath.Join(t.TempDir(), "scaffold.db"))
		if err != nil {
			t.Fatalf("auth=%s: %v", mode, err)
		}
		result, err := pwmigrate.Apply(t.Context(), target, sources, pwmigrate.ActionUp, 0)
		_ = target.Close()
		if err != nil {
			t.Fatalf("auth=%s: %v", mode, err)
		}
		if result.Current == 0 {
			t.Fatalf("auth=%s applied no migration", mode)
		}
	}
}

// The session backend decides three scaffold outputs at once: the import that
// contributes it, the configuration keys that describe it, and whether a
// migration is needed at all.
// The scaffolded keyring is generated per project rather than shipped in this
// source. A constant here would be a published credential in every project that
// ever ran pw init, which is the thing the placeholder check exists to catch.
func TestTheScaffoldedKeyringIsGeneratedPerProject(t *testing.T) {
	// The scaffold writes [session] alongside [auth] today, so this asks for an
	// authentication mode to get one.
	first := scaffoldFiles(initOptions{Name: "demo", Auth: authOIDC, Session: sessionCookie, SessionExplicit: true})["config.dev.toml"]
	second := scaffoldFiles(initOptions{Name: "demo", Auth: authOIDC, Session: sessionCookie, SessionExplicit: true})["config.dev.toml"]
	secretOf := func(config string) string {
		for _, line := range strings.Split(config, "\n") {
			if key, value, found := strings.Cut(strings.TrimSpace(line), "="); found &&
				strings.TrimSpace(key) == "keyring.secret" {
				return strings.Trim(strings.TrimSpace(value), `"`)
			}
		}
		return ""
	}
	one, other := secretOf(first), secretOf(second)
	if one == "" {
		t.Fatalf("no keyring was scaffolded:\n%s", first)
	}
	if one == other {
		t.Error("two projects were scaffolded with the same keyring")
	}
	decoded, err := base64.StdEncoding.DecodeString(one)
	if err != nil {
		t.Fatalf("the scaffolded keyring is not base64: %v", err)
	}
	if len(decoded) < 32 {
		t.Errorf("the scaffolded keyring is %d bytes, want at least 32", len(decoded))
	}
}

func TestScaffoldFollowsTheSessionBackendChoice(t *testing.T) {
	rdbFiles := scaffoldFiles(initOptions{Name: "demo", Database: true, Auth: authOIDC, Session: sessionRDB, SessionExplicit: true})
	if !strings.Contains(rdbFiles["cmd/demo/main.go"], `_ "github.com/shibukawa/popcornweb/sessionstore/sqlite"`) {
		t.Errorf("rdb backend is not imported:\n%s", rdbFiles["cmd/demo/main.go"])
	}
	// The login ceremony records live in the database whichever backend holds
	// the sessions.
	if !strings.Contains(rdbFiles["cmd/demo/main.go"], `_ "github.com/shibukawa/popcornweb/authstate/sqlite"`) {
		t.Errorf("ceremony store is not imported:\n%s", rdbFiles["cmd/demo/main.go"])
	}
	if !strings.Contains(rdbFiles["config.dev.toml"], `backend = "rdb"`) ||
		!strings.Contains(rdbFiles["config.dev.toml"], `rdb.source = "middleware"`) {
		t.Errorf("rdb session config:\n%s", rdbFiles["config.dev.toml"])
	}
	if _, ok := rdbFiles["migrations/00002_"+sessionstore.MigrationName+".sql"]; !ok {
		t.Error("rdb backend did not scaffold its session migration")
	}
	if _, ok := rdbFiles["migrations/00003_"+auth.MigrationName+".sql"]; !ok {
		t.Error("auth migration is not numbered after the session one")
	}

	cookieFiles := scaffoldFiles(initOptions{Name: "demo", Database: true, Auth: authOIDC, Session: sessionCookie, SessionExplicit: true})
	if strings.Contains(cookieFiles["cmd/demo/main.go"], "sessionstore/") {
		t.Errorf("the built-in cookie backend was imported:\n%s", cookieFiles["cmd/demo/main.go"])
	}
	// Every backend seals a record into the browser while a session is still
	// anonymous, so the keyring is scaffolded whatever the backend is, and the
	// cookie backend adds no key of its own.
	if !strings.Contains(cookieFiles["config.dev.toml"], "keyring.secret = ") {
		t.Errorf("cookie session config:\n%s", cookieFiles["config.dev.toml"])
	}
	if strings.Contains(cookieFiles["config.dev.toml"], "rdb.source") {
		t.Error("cookie backend wrote keys of a backend it does not use")
	}
	// No session table exists, so the auth migration takes the free version.
	if _, ok := cookieFiles["migrations/00002_"+auth.MigrationName+".sql"]; !ok {
		t.Errorf("auth migration was not renumbered: %v", migrationNames(cookieFiles))
	}

	redisFiles := scaffoldFiles(initOptions{Name: "demo", Database: true, Auth: authOIDC, Session: sessionRedis, SessionExplicit: true})
	if !strings.Contains(redisFiles["cmd/demo/main.go"], `_ "github.com/shibukawa/popcornweb/sessionstore/redis"`) {
		t.Errorf("redis backend is not imported:\n%s", redisFiles["cmd/demo/main.go"])
	}
	if !strings.Contains(redisFiles["config.dev.toml"], "redis.dsn") {
		t.Errorf("redis session config:\n%s", redisFiles["config.dev.toml"])
	}
	if _, ok := redisFiles["migrations/00002_"+auth.MigrationName+".sql"]; !ok {
		t.Errorf("auth migration was not renumbered: %v", migrationNames(redisFiles))
	}
}

// A Redis-backed session brings the server that serves it into the development
// environment, rather than leaving a project that cannot start.
func TestRedisSessionTakesTheDevelopmentServer(t *testing.T) {
	options, err := parseInitArgs([]string{"demo", "--auth=oidc", "--session=redis", "--no-redis"})
	if err != nil {
		t.Fatal(err)
	}
	if !options.Redis {
		t.Fatalf("options = %#v", options)
	}
	if _, err := parseInitArgs([]string{"demo", "--session=memcached"}); err == nil {
		t.Fatal("an unknown backend was accepted")
	}
}

func migrationNames(files map[string]string) []string {
	var names []string
	for path := range files {
		if strings.HasPrefix(path, "migrations/") {
			names = append(names, path)
		}
	}
	sort.Strings(names)
	return names
}

// mustSessionMigration and mustAuthMigration are the SQLite migrations the
// scaffold writes, which is the dialect these fixtures use.
func mustSessionMigration() string {
	migration, err := sessionstore.MigrationSQL("sqlite", "popcornweb_session")
	if err != nil {
		panic(err)
	}
	return migration
}

func mustAuthMigration() string {
	migration, err := auth.MigrationSQL("sqlite")
	if err != nil {
		panic(err)
	}
	return migration
}

// A SQL store is one package per engine, so the engine answer decides which
// storage packages the entry point imports and which dialect the migrations
// are written in.
func TestScaffoldImportsTheStoresOfTheSelectedEngine(t *testing.T) {
	for engine, want := range map[string][]string{
		engineSQLite:   {"sessionstore/sqlite", "authstate/sqlite"},
		enginePostgres: {"sessionstore/postgres", "authstate/postgres"},
		engineMySQL:    {"sessionstore/mysql", "authstate/mysql"},
	} {
		t.Run(engine, func(t *testing.T) {
			files := scaffoldFiles(initOptions{
				Name: "demo", Database: true, Engine: engine, Auth: authOIDC, Session: sessionRDB, SessionExplicit: true,
			})
			main := files["cmd/demo/main.go"]
			for _, path := range want {
				if !strings.Contains(main, `_ "github.com/shibukawa/popcornweb/`+path+`"`) {
					t.Errorf("%s is not imported:\n%s", path, main)
				}
			}
			// The migration has to be readable by the engine that will run it.
			migration := files["migrations/00002_"+sessionstore.MigrationName+".sql"]
			expected, err := sessionstore.MigrationSQL(engine, "popcornweb_session")
			if err != nil {
				t.Fatal(err)
			}
			if migration != expected {
				t.Errorf("session migration is not in the %s dialect:\n%s", engine, migration)
			}
		})
	}
}

// A browser login arrives with an unsafe route already written: the sign-out
// control posts. So the question CSRF is off until somebody answers is answered
// by the scaffold itself, and a generated form refuses to render without a
// token rather than emit an unprotected one — which made the scaffolded home
// page return 500 the moment anybody signed in.
func TestScaffoldTurnsCSRFOnForABrowserLogin(t *testing.T) {
	config := scaffoldFiles(initOptions{Name: "demo", Auth: authOIDC})[pwenv.FileName(pwenv.Development)]
	if !strings.Contains(config, "[security]") {
		t.Fatalf("no security section:\n%s", config)
	}
	if !strings.Contains(config, "csrf.enabled = true") {
		t.Errorf("a project whose starter page posts has CSRF off:\n%s", config)
	}
	// The anonymous path is a comment, so a project that needs it finds the
	// keys rather than the documentation.
	if !strings.Contains(config, "# csrf.anonymous.enabled = true") {
		t.Errorf("the anonymous path is not shown:\n%s", config)
	}
}

// The development provider accepts loopback callbacks, so the scaffold lets
// the login return to the exact loopback origin the developer opened.
func TestEmulatorScaffoldDerivesTheRedirectFromTheRequest(t *testing.T) {
	for _, mode := range []string{authOIDC, authOIDCPasskey} {
		config := scaffoldFiles(initOptions{Name: "demo", Auth: mode, AuthEmulator: true})[pwenv.FileName(pwenv.Development)]
		if strings.Contains(config, "redirect_url =") {
			t.Errorf("%s fixes the development callback to one loopback spelling:\n%s", mode, config)
		}
	}
}

// A page tree's action endpoints are POSTs reachable with the session cookie
// and nothing else in front of them, so the prefix must be in the include list.
func TestScaffoldCoversThePageActionPrefix(t *testing.T) {
	pages := scaffoldFiles(initOptions{Name: "demo", Auth: authOIDC, Router: routerDiscovered})[pwenv.FileName(pwenv.Development)]
	if !strings.Contains(pages, `csrf.include = ["/_action/**", "/**"]`) {
		t.Errorf("a page tree did not cover the action prefix:\n%s", pages)
	}
}

// TOML refuses a key written twice, and a login with a page tree once wrote
// csrf.include for the login and again for the page actions, so the scaffolded
// file did not parse at all. Every scaffold that writes the section is parsed
// here, and the include list is checked to be the one both callers need.
func TestScaffoldedSecuritySectionParsesWithOneIncludeList(t *testing.T) {
	for _, options := range []initOptions{
		{Name: "demo", Database: true, Auth: authOIDC, Router: routerBoth},
		{Name: "demo", Database: true, Auth: authOAuth, Router: routerBoth},
		{Name: "demo", Database: true, Auth: authOIDC, Router: routerRegistered},
		{Name: "demo", Tailwind: true, Router: routerBoth},
		{Name: "demo", Router: routerDiscovered},
	} {
		config := scaffoldFiles(options)[pwenv.FileName(pwenv.Development)]
		if _, err := minitoml.ParseString(config); err != nil {
			t.Errorf("%+v: config.dev.toml does not parse: %v\n%s", options, err, config)
			continue
		}
		if got := strings.Count(config, "\ncsrf.include = "); got != 1 {
			t.Errorf("%+v: csrf.include is written %d times:\n%s", options, got, config)
		}
		want := `csrf.include = ["/**"]`
		if hasDiscoveredPages(options) {
			want = `csrf.include = ["/_action/**", "/**"]`
		}
		if !strings.Contains(config, want) {
			t.Errorf("%+v: the include list is not %s:\n%s", options, want, config)
		}
	}
}

// Without a session there is nothing to bind a token to, so the section would
// only describe a check that could not pass.
func TestScaffoldWritesNoSecuritySectionWithoutASession(t *testing.T) {
	config := scaffoldFiles(initOptions{Name: "demo"})[pwenv.FileName(pwenv.Development)]
	if strings.Contains(config, "[security]") {
		t.Errorf("a session-less project got a security section:\n%s", config)
	}
}

// Choosing DynamoDB for the login asks for no SQL engine behind it. All four
// tables plugin/auth owns move to DynamoDB with auth.backend, so there is
// nothing left for a relational database to carry.
func TestInitWizardAsksForNoEngineBehindADynamoLogin(t *testing.T) {
	t.Chdir(t.TempDir())
	model := startWizard(t, presetManual, "demo", defaultInitOptions())
	model = answerHubRow(t, model, "Authentication", 2) // OIDC
	model = answerHubRow(t, model, "Store", 4)          // DynamoDB
	options := wizardResult(model, defaultInitOptions())
	options.Preset = ""
	if !options.Dynamo || options.Database {
		t.Fatalf("options = %#v", options)
	}
	// Neither the engine nor the DynamoDB row survives: the store answer
	// settled both, so there is nothing left to ask.
	for _, label := range []string{"DynamoDB", "Database engine", "Database"} {
		if hubRow(model, label) {
			t.Fatalf("%q is still on the list after DynamoDB was the store answer: rows are %v", label, hubLabels(model))
		}
	}
}

// The project that answer produces is the one the backend was built for: no rdb
// section, and the four auth tables named as DynamoDB's.
func TestDynamoLoginScaffoldsTheDynamoAuthBackend(t *testing.T) {
	options := initOptions{
		Name: "demo", Router: routerRegistered, Auth: authOIDC, AuthStore: dynamoStore,
		Dynamo: true, Database: false, Session: sessionDynamo, SessionExplicit: true,
	}
	if backend := authBackend(options); backend != "dynamo" {
		t.Fatalf("auth backend = %q", backend)
	}
	config := authRuntimeConfig(options)
	if !strings.Contains(config, `backend = "dynamo"`) {
		t.Fatalf("[auth] section does not name the backend:\n%s", config)
	}
	// Both halves are imported: the ceremony store and the account-side stores.
	imports := sessionBackendImport(options)
	for _, wanted := range []string{"authstate/dynamo", "authstore/dynamo"} {
		if !strings.Contains(imports, wanted) {
			t.Fatalf("imports do not carry %s:\n%s", wanted, imports)
		}
	}
	if strings.Contains(imports, "authstate/sqlite") {
		t.Fatalf("a relational ceremony store was imported anyway:\n%s", imports)
	}
}

// The Firestore project is the same shape as the DynamoDB one: no rdb section,
// and the four auth stores named as Firestore's.
func TestFirestoreLoginScaffoldsTheFirestoreAuthBackend(t *testing.T) {
	options := initOptions{
		Name: "demo", Router: routerRegistered, Auth: authOIDC,
		Firestore: true, Database: false, Session: sessionFirestore, SessionExplicit: true,
	}
	if backend := authBackend(options); backend != "firestore" {
		t.Fatalf("auth backend = %q", backend)
	}
	config := authRuntimeConfig(options)
	if !strings.Contains(config, `backend = "firestore"`) {
		t.Fatalf("[auth] section does not name the backend:\n%s", config)
	}
	// Both halves are imported: the ceremony store and the account-side stores.
	imports := sessionBackendImport(options)
	for _, wanted := range []string{"authstate/firestore", "authstore/firestore"} {
		if !strings.Contains(imports, wanted) {
			t.Fatalf("imports do not carry %s:\n%s", wanted, imports)
		}
	}
	if strings.Contains(imports, "authstate/sqlite") {
		t.Fatalf("a relational ceremony store was imported anyway:\n%s", imports)
	}
	// Nothing is generated for Firestore, so the client middleware import has
	// to be written by the scaffold rather than arriving with generated code.
	if store := storeMiddlewareImport(options); !strings.Contains(store, "database/firestore") {
		t.Fatalf("the client middleware is not imported: %q", store)
	}
}

// The configuration points at the emulator and names no credential: the
// emulator ignores the Authorization header, so a key here would be pretending
// to exercise the token path.
func TestTheFirestoreSectionScaffoldsForTheEmulator(t *testing.T) {
	section := firestoreRuntimeConfig(initOptions{Firestore: true})
	for _, want := range []string{"[middleware.firestore]", "127.0.0.1:8081", "Datastore mode", "gcloud beta emulators datastore start"} {
		if !strings.Contains(section, want) {
			t.Errorf("the section does not carry %q:\n%s", want, section)
		}
	}
	for _, absent := range []string{"credentials_file", "secret", "auto_migrate", "verify_schema"} {
		if strings.Contains(section, absent) {
			t.Errorf("the section carries %q, which it should not", absent)
		}
	}
	if firestoreRuntimeConfig(initOptions{}) != "" {
		t.Error("a project without the store still got a section")
	}
}

// A login with neither store has nowhere to keep its records, and both ways out
// are named. Firestore is one of them.
func TestAFirestoreOnlyLoginIsAccepted(t *testing.T) {
	_, err := parseInitArgs([]string{"demo", "--auth=oidc", "--no-database"})
	if err == nil || !strings.Contains(err.Error(), "--firestore") {
		t.Fatalf("the refusal does not name --firestore: %v", err)
	}
	options, err := parseInitArgs([]string{"demo", "--auth=oidc", "--no-database", "--firestore"})
	if err != nil {
		t.Fatalf("a Firestore-only login = %v", err)
	}
	if backend := sessionBackend(options); backend != sessionFirestore {
		t.Fatalf("session backend = %q", backend)
	}
	if backend := authBackend(options); backend != "firestore" {
		t.Fatalf("auth backend = %q", backend)
	}
}

// auth.backend names one store for all four of its kinds, so a project with
// both non-relational stores and no database has no defined winner. Asking is
// better than picking one.
func TestTwoStoresAndNoDatabaseIsRefusedForALogin(t *testing.T) {
	_, err := parseInitArgs([]string{"demo", "--auth=oidc", "--no-database", "--dynamo", "--firestore"})
	if err == nil {
		t.Fatal("a login with two stores and no database was accepted")
	}
	// Without a login there is nothing to choose between, so both stores stand.
	if _, err := parseInitArgs([]string{"demo", "--auth=none", "--no-database", "--dynamo", "--firestore"}); err != nil {
		t.Fatalf("two stores without a login = %v", err)
	}
}

// A relational login is untouched by any of this.
func TestRelationalLoginKeepsTheRelationalBackend(t *testing.T) {
	options := initOptions{
		Name: "demo", Router: routerRegistered, Auth: authOIDC, AuthStore: engineSQLite,
		Database: true, Engine: engineSQLite, Session: sessionRDB, SessionExplicit: true,
	}
	if backend := authBackend(options); backend != "rdb" {
		t.Fatalf("auth backend = %q", backend)
	}
	if !strings.Contains(authRuntimeConfig(options), `backend = "rdb"`) {
		t.Fatal("[auth] section does not name the relational backend")
	}
	imports := sessionBackendImport(options)
	if !strings.Contains(imports, "authstate/sqlite") || strings.Contains(imports, "authstore/dynamo") {
		t.Fatalf("imports = %s", imports)
	}
}

// A login with neither store has nowhere to keep its records, and the refusal
// names both ways out rather than only the database.
func TestALoginNeedsAStore(t *testing.T) {
	_, err := parseInitArgs([]string{"demo", "--auth=oidc", "--no-database"})
	if err == nil || !strings.Contains(err.Error(), "--dynamo") {
		t.Fatalf("a login with no store = %v", err)
	}
	// Naming the other store is accepted, and the session follows it rather
	// than defaulting to a pool the project does not open.
	options, err := parseInitArgs([]string{"demo", "--auth=oidc", "--no-database", "--dynamo"})
	if err != nil {
		t.Fatalf("a DynamoDB-only login = %v", err)
	}
	if backend := sessionBackend(options); backend != sessionDynamo {
		t.Fatalf("session backend = %q", backend)
	}
	if backend := authBackend(options); backend != "dynamo" {
		t.Fatalf("auth backend = %q", backend)
	}
}

// A SQL login still reaches the DynamoDB question: the store answer covered one
// kind of store, and the other one is a separate decision.
func TestInitWizardStillAsksAboutDynamoAfterASQLLogin(t *testing.T) {
	t.Chdir(t.TempDir())
	model := startWizard(t, presetManual, "demo", defaultInitOptions())
	model = answerHubRow(t, model, "Authentication", 2) // OIDC
	model = answerHubRow(t, model, "Store", 1)          // SQLite
	if !hubRow(model, "DynamoDB") {
		t.Fatalf("the other kind of store is missing from the list: rows are %v", hubLabels(model))
	}
	model = answerHubRow(t, model, "DynamoDB", 1) // Yes
	options := wizardResult(model, defaultInitOptions())
	options.Preset = ""
	if !options.Dynamo || options.Engine != engineSQLite {
		t.Fatalf("options = %#v", options)
	}
}

// Partial updates are scaffolded off, with the key they need shown rather than
// left for the reader to find. Enabling them without one is a startup failure.
func TestScaffoldWritesTheUpdateSectionOffByDefault(t *testing.T) {
	config := scaffoldFiles(initOptions{Name: "demo", Auth: authOIDC})[pwenv.FileName(pwenv.Development)]
	if !strings.Contains(config, "[html.update]") {
		t.Fatalf("no update section:\n%s", config)
	}
	if !strings.Contains(config, "enabled = false") {
		t.Errorf("updates are not scaffolded off:\n%s", config)
	}
	if !strings.Contains(config, "# validator_key =") {
		t.Errorf("the validator key is not shown:\n%s", config)
	}
}

// Session storage is not a login. A project that declares only a language
// preference still needs the middleware, and the framework installs it from
// session.enabled rather than from an authentication plugin.
func TestSessionIsScaffoldedWithoutALogin(t *testing.T) {
	files := scaffoldFiles(initOptions{Name: "demo", Database: true})
	config := files["config.dev.toml"]
	if !strings.Contains(config, "[session]") {
		t.Fatalf("a project without a login got no session section:\n%s", config)
	}
	if strings.Contains(config, "[auth]") {
		t.Fatal("a project without a login got an auth section")
	}
	// Development defaults to process-local memory: no table, migration,
	// storage import, persisted codec, or sealed record cookie.
	if !strings.Contains(config, `backend = "dev-volatile"`) {
		t.Fatalf("session backend without a login:\n%s", config)
	}
	if strings.Contains(files["cmd/demo/main.go"], "sessionstore/") {
		t.Error("a cookie session imported a storage plugin")
	}
	if strings.Contains(files["cmd/demo/main.go"], "authstate/") {
		t.Error("a project with no login imported the login ceremony store")
	}
	for name := range files {
		if strings.Contains(name, sessionstore.MigrationName) {
			t.Errorf("a cookie session scaffolded a table migration: %s", name)
		}
	}
	if strings.Contains(config, "keyring.secret = ") {
		t.Error("an all-memory development session scaffolded an unused keyring")
	}
}

func TestUnansweredSessionDefaultsToMemoryOnlyInDevelopmentConfig(t *testing.T) {
	options, err := parseInitArgs([]string{"--yes", "demo"})
	if err != nil {
		t.Fatal(err)
	}
	if options.SessionExplicit {
		t.Fatal("the default session backend was marked as an operator answer")
	}
	if backend := developmentSessionBackend(options); backend != sessionDevVolatile {
		t.Fatalf("development backend = %q", backend)
	}
	if backend := sessionBackend(options); backend != sessionRDB {
		t.Fatalf("deployment backend = %q", backend)
	}

	explicit, err := parseInitArgs([]string{"--yes", "demo", "--session=rdb"})
	if err != nil {
		t.Fatal(err)
	}
	if !explicit.SessionExplicit || developmentSessionBackend(explicit) != sessionRDB {
		t.Fatalf("explicit backend was not preserved: %#v", explicit)
	}

	persist, err := parseInitArgs([]string{"--yes", "demo", "--session=dev-persist"})
	if err != nil {
		t.Fatal(err)
	}
	config := scaffoldFiles(persist)["config.dev.toml"]
	if !strings.Contains(config, `backend = "dev-persist"`) || !strings.Contains(config, "keyring.secret = ") {
		t.Fatalf("dev-persist config:\n%s", config)
	}
	if _, err := parseInitArgs([]string{"--yes", "demo", "--session=memory"}); err == nil {
		t.Fatal("the retired memory configuration name was accepted")
	}
}

// Asking for a server backend without a login is honored, and it brings the
// import with it rather than leaving a configuration nothing serves.
func TestAServerBackendWithoutALoginBringsItsImport(t *testing.T) {
	files := scaffoldFiles(initOptions{Name: "demo", Database: true, Session: sessionRDB, SessionExplicit: true})
	if !strings.Contains(files["config.dev.toml"], `backend = "rdb"`) {
		t.Fatal("the selected backend was not scaffolded")
	}
	if !strings.Contains(files["cmd/demo/main.go"], "sessionstore/") {
		t.Errorf("the rdb backend was configured without its import:\n%s", files["cmd/demo/main.go"])
	}
}

// A login the scaffold configures and no page offers is a project nobody can
// sign in to. With no registered router there is no handler page to carry the
// controls, so the page tree root carries them — and it said so either way,
// because the landing sections name the login the project took.
func TestADiscoveredOnlyLoginIsReachableFromItsStarterPage(t *testing.T) {
	files := scaffoldFiles(initOptions{Name: "demo", Router: routerDiscovered, Auth: authOIDC})
	page := files["pages/page.pw.html"]
	for _, want := range []string{"Account", "{if signedIn}", `href="{loginPath}"`, `action="{logoutPath}"`} {
		if !strings.Contains(page, want) {
			t.Errorf("the page tree root does not carry %q:\n%s", want, page)
		}
	}
	// The session is on the request context, which the rungs below the handler
	// one cannot see, so the root takes that rung and composes its own chain.
	load, ok := files["pages/page.go"]
	if !ok {
		t.Fatal("the page tree root has no page.go, so nothing reads the session")
	}
	for _, want := range []string{
		"func Load(w http.ResponseWriter, r *http.Request)",
		"auth.User(r.Context())",
		"BindLayout(LayoutParams{})",
		"pwpage.Render(w, r, wrappers, Page(params))",
	} {
		if !strings.Contains(load, want) {
			t.Errorf("pages/page.go is missing %q:\n%s", want, load)
		}
	}
}

// With a registered router the controls are on its page, so a second copy on
// the page tree root would be two account sections to tell apart.
func TestOnlyOneStarterPageCarriesTheAccountSection(t *testing.T) {
	for _, router := range []string{routerBoth, routerRegistered} {
		files := scaffoldFiles(initOptions{Name: "demo", Router: router, Auth: authOIDC})
		if page, ok := files["pages/page.pw.html"]; ok && strings.Contains(page, "Account") {
			t.Errorf("router %q put the account section on both starter pages:\n%s", router, page)
		}
		if _, ok := files["pages/page.go"]; ok {
			t.Errorf("router %q raised the page tree root a rung it does not need", router)
		}
		if !strings.Contains(files["handlers/home.pw.html"], "Account") {
			t.Errorf("router %q left the handler page without the account section", router)
		}
	}
}

// A login with no browser flow has nothing to offer on a page.
func TestABearerProjectScaffoldsNoAccountSection(t *testing.T) {
	files := scaffoldFiles(initOptions{Name: "demo", Router: routerDiscovered, Auth: authJWTOnly})
	if page := files["pages/page.pw.html"]; strings.Contains(page, "Account") {
		t.Errorf("a bearer project got sign-in controls:\n%s", page)
	}
	if _, ok := files["pages/page.go"]; ok {
		t.Error("a bearer project raised the page tree root a rung")
	}
}

// A DynamoDB project has to compile from the files pw init wrote. The codec is
// emitted for the directions a package is discovered to use, so a store
// scaffold has to call something. Both stores declare a type and then read and
// write it, because a type nothing touches is a type with no codec — and a
// scaffold whose calls are not the discovered ones ships a project that cannot
// build, which is what the handle-form entries did before tinybind v0.5.4
// registered them.
func TestScaffoldedStoresCallWhatGenerationDiscovers(t *testing.T) {
	files := scaffoldFiles(initOptions{Name: "demo", Dynamo: true})
	record, ok := files["records/note.go"]
	if !ok {
		t.Fatal("no dynamo record was scaffolded")
	}
	for _, want := range []string{`h.Store(ctx, "note", note)`, "h.Load[Note](ctx, \"note\",", "dynamo.Handle(ctx)"} {
		if !strings.Contains(record, want) {
			t.Errorf("records/note.go does not carry %q:\n%s", want, record)
		}
	}
	// The declaration the Firestore scaffold has always had, and this one had
	// not: it is a use of its result type, so it is what the read side is
	// emitted for once a project declares an access pattern.
	if _, ok := files["records/notes.pw.dynamo"]; !ok {
		t.Error("the dynamo scaffold declares no access pattern, unlike the firestore one")
	}

	// The Firestore scaffold declared an entity and never touched it, so it had
	// no codec either — it simply compiled, because nothing called for one.
	entity, ok := scaffoldFiles(initOptions{Name: "demo", Firestore: true})["entities/note.go"]
	if !ok {
		t.Fatal("no firestore entity was scaffolded")
	}
	for _, want := range []string{"h.Store(ctx, note)", "h.Load[Note](ctx,", "firestore.Handle(ctx)"} {
		if !strings.Contains(entity, want) {
			t.Errorf("entities/note.go does not carry %q:\n%s", want, entity)
		}
	}
}

// The OAuth answer is a browser login like the OIDC one, so it gets the same
// resolver, the same sign-in control, and the same empty client placeholders;
// what it never gets is an issuer or the development identity provider, because
// a plain OAuth provider has neither.
func TestScaffoldWiresAnOAuthProvider(t *testing.T) {
	files := scaffoldFiles(initOptions{Name: "demo", Router: routerRegistered, Database: true, Auth: authOAuth, AuthEmulator: true})

	config := files[pwenv.FileName(pwenv.Development)]
	for _, expected := range []string{`mode = "oauth_only"`, "[auth.oauth]", `provider = "x"`, `client_id = ""`, `client_secret = ""`,
		`redirect_url = "http://localhost:8080/auth/callback"`, "allow_loopback_http = true", "AUTH_OAUTH_CLIENT_ID"} {
		if !strings.Contains(config, expected) {
			t.Errorf("config is missing %s:\n%s", expected, config)
		}
	}
	for _, absent := range []string{"[auth.oidc]", "issuer =", "AUTH_OIDC"} {
		if strings.Contains(config, absent) {
			t.Errorf("config carries %s, which the mode refuses:\n%s", absent, config)
		}
	}
	if _, present := files[defaultIdPConfig]; present || strings.Contains(files["popcornweb.toml"], "[dev.idp]") {
		t.Error("an OAuth login must not scaffold the development identity provider, which is an OpenID Provider")
	}
	accounts := files["handlers/accounts.go"]
	if !strings.Contains(accounts, "auth.SetAccountResolver(resolveAccount)") {
		t.Errorf("the OAuth login needs the account resolver:\n%s", accounts)
	}
	showsSignIn := false
	for _, content := range files {
		showsSignIn = showsSignIn || strings.Contains(content, "ProviderLogin: true")
	}
	if !showsSignIn {
		t.Error("no scaffolded page shows the sign-in control for the OAuth login")
	}
	env := files[".env.example"]
	for _, expected := range []string{"\nAUTH_OAUTH_CLIENT_ID=\n", "\nAUTH_OAUTH_CLIENT_SECRET=\n"} {
		if !strings.Contains(env, expected) {
			t.Errorf(".env.example does not name %q:\n%s", strings.TrimSpace(expected), env)
		}
	}
	if strings.Contains(env, "AUTH_OIDC") {
		t.Errorf(".env.example names an oidc variable the mode refuses:\n%s", env)
	}
}
