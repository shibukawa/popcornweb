package pwcli

import (
	"strings"
	"testing"

	"github.com/shibukawa/popcornweb/internal/pwenv"
)

// Every preset has to produce a project the rest of pw init would accept. A
// preset that expressed a combination the validation refuses would be an entry
// in a list that fails after it is chosen, which is worse than one that is not
// offered.
func TestEveryPresetProducesAValidProject(t *testing.T) {
	for _, preset := range initPresetCatalog {
		if preset.name == presetManual {
			continue
		}
		t.Run(preset.name, func(t *testing.T) {
			options, err := parseInitArgs([]string{presetArgument(preset.name), "demo"})
			if err != nil {
				t.Fatalf("--preset=%s = %v", preset.name, err)
			}
			if options.Preset != preset.name {
				t.Fatalf("preset = %q, want %q", options.Preset, preset.name)
			}
			if files := scaffoldFiles(options); len(files) == 0 {
				t.Fatal("the preset scaffolded nothing")
			}
		})
	}
}

// A preset and a capability flag both answer the same question, and neither is
// obviously the winner, so the run stops before anything is written.
func TestPresetRefusesACapabilityFlagBesideIt(t *testing.T) {
	for _, flag := range []string{"--tailwind", "--no-database", "--router=discovered", "--auth=oidc", "--db=postgres"} {
		if _, err := parseInitArgs([]string{"--preset=website-login", "demo", flag}); err == nil ||
			!strings.Contains(err.Error(), "drop one of them") {
			t.Errorf("--preset with %s: err = %v", flag, err)
		}
	}
	// The project name and --yes answer no question a preset answers, so both
	// survive it.
	options, err := parseInitArgs([]string{"--preset=website-login", "demo", "--yes"})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if options.Name != "demo" || !options.Yes {
		t.Fatalf("options = %#v", options)
	}
}

func TestPresetRejectsAnUnknownName(t *testing.T) {
	if _, err := parseInitArgs([]string{"--preset=nonsense", "demo"}); err == nil ||
		!strings.Contains(err.Error(), "--preset must be one of") {
		t.Fatalf("err = %v", err)
	}
}

// --kind is the spelling that names a project kind directly and --preset the
// one that names it through the list. They are the same run, so they agree.
func TestPackageKindAndPackagePresetAreOneScaffold(t *testing.T) {
	byKind, err := parseInitArgs([]string{"--kind=package", "github.com/you/widget"})
	if err != nil {
		t.Fatalf("--kind=package = %v", err)
	}
	byPreset, err := parseInitArgs([]string{"--preset=package", "github.com/you/widget"})
	if err != nil {
		t.Fatalf("--preset=package = %v", err)
	}
	if byKind.Kind != kindPackage || byPreset.Kind != kindPackage {
		t.Fatalf("kind = %q and %q", byKind.Kind, byPreset.Kind)
	}
	// A contradiction stated in one command is refused rather than resolved.
	if _, err := parseInitArgs([]string{"--preset=website-login", "--kind=package", "demo"}); err == nil ||
		!strings.Contains(err.Error(), "contradicts") {
		t.Fatalf("err = %v", err)
	}
}

// The api-server preset writes the mode the authentication question refuses to
// offer. Both halves matter: the preset reaches it, and the question does not.
func TestAPIServerPresetScaffoldsTheBearerMode(t *testing.T) {
	options, err := parseInitArgs([]string{"--preset=api-server", "demo"})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	files := scaffoldFiles(options)
	config := files[pwenv.FileName(pwenv.Development)]
	for _, want := range []string{
		`mode = "jwt_only"`,
		`admission = "authenticated"`,
		`identity_claim = "sub"`,
		`revocation.mode = "off"`,
		`protection.unauthenticated = "unauthorized"`,
	} {
		if !strings.Contains(config, want) {
			t.Errorf("config is missing %s:\n%s", want, config)
		}
	}
	if !strings.Contains(config, "dev.trust_unverified_tokens = true") {
		t.Errorf("the development file does not relax verification, so the project cannot be curled:\n%s", config)
	}
	// The mode validates the whole auth.jwt prefix at startup with no
	// development exemption, so every field it refuses to start without has to
	// carry a value here. An empty issuer made the scaffolded project unable to
	// run at all, which is the state the relaxation exists to prevent.
	for _, want := range []string{
		`issuer = "` + authDevelopmentIssuer + `"`,
		`audience = ["demo"]`,
		`algorithms = ["RS256"]`,
		"allow_loopback_http = true",
	} {
		if !strings.Contains(config, want) {
			t.Errorf("config is missing %s, so the project refuses to start:\n%s", want, config)
		}
	}
	// The token recipe in the comment is the one a reader will paste. It has to
	// name the issuer as well as the subject: the account is derived from the
	// pair, and a token carrying only a subject is refused by admission.
	recipe := config[strings.Index(config, "b64()"):]
	recipe = recipe[:strings.Index(recipe, "\ndev.")]
	for _, want := range []string{`"iss":"` + authDevelopmentIssuer + `"`, `"sub":"dev-user"`} {
		if !strings.Contains(recipe, want) {
			t.Errorf("the scaffolded curl recipe omits %s, so pasting it is refused:\n%s", want, recipe)
		}
	}
}

// The bearer mode registers no account seam, so nothing else in the project
// reaches plugin/auth. Without the import the package is not linked, its
// extension never registers, and the [auth] section is configuration nothing
// reads: startup accepts anything and every request arrives unauthenticated.
func TestAPIServerLinksTheAuthPlugin(t *testing.T) {
	options, err := parseInitArgs([]string{"--preset=api-server", "demo"})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	main := scaffoldFiles(options)["cmd/demo/main.go"]
	if !strings.Contains(main, `_ "github.com/shibukawa/popcornweb/plugin/auth"`) {
		t.Fatalf("the bearer verifier is not linked, so the auth configuration is decorative:\n%s", main)
	}
}

// A resource server mounts no login, so nothing that belongs to a login
// ceremony is written for it.
func TestAPIServerPresetScaffoldsNoBrowserLogin(t *testing.T) {
	options, err := parseInitArgs([]string{"--preset=api-server", "demo"})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	files := scaffoldFiles(options)
	for _, absent := range []string{
		"handlers/accounts.go",
		"public/presence.js",
		"public/passkey.js",
		defaultIdPConfig,
	} {
		if _, ok := files[absent]; ok {
			t.Errorf("a bearer-only project scaffolded %s", absent)
		}
	}
	config := files[pwenv.FileName(pwenv.Development)]
	// No cookie to bind a token to, and no browser to attach one, so the CSRF
	// section would describe a check that could not apply.
	if strings.Contains(config, "[security]") {
		t.Errorf("a bearer-only project scaffolded a CSRF section:\n%s", config)
	}
	// The backend key names the storage of four ceremony stores this mode
	// never opens, and writing it would name a relational database this
	// project does not have.
	if strings.Contains(config, "backend = \"rdb\"") {
		t.Errorf("a bearer-only project named an authentication backend:\n%s", config)
	}
	// The first handler shows reading the verified identity, which is what
	// this project is for.
	handler, ok := files["handlers/me_handler.go"]
	if !ok {
		t.Fatal("no bearer handler was scaffolded")
	}
	if !strings.Contains(handler, "pw.RequestAuthentication(r)") {
		t.Errorf("the handler does not read the verified identity:\n%s", handler)
	}
	// A project with no relational database and no store to open.
	for _, absent := range []string{"queries/users.pw.sql", "migrations/00001_init.sql"} {
		if _, ok := files[absent]; ok {
			t.Errorf("a bearer-only project scaffolded %s", absent)
		}
	}
	// No landing page: every caller of this application arrives with an
	// Authorization header, and none of them renders HTML.
	for _, absent := range []string{"handlers/home.pw.html", "handlers/home_handler.go"} {
		if _, ok := files[absent]; ok {
			t.Errorf("a bearer-only project scaffolded %s", absent)
		}
	}
	// The document shell and the error templates stay, because the error
	// renderer still answers a browser that reaches a failing route.
	for _, present := range []string{"templates/document.pw.html", "templates/500.pw.html"} {
		if _, ok := files[present]; !ok {
			t.Errorf("%s was dropped with the landing page", present)
		}
	}
}

// The jwt_only mode stays out of the question every browser application also
// answers. A preset reaches it; the wizard row and the flag do not.
func TestBearerModeIsNotAWizardAnswer(t *testing.T) {
	if _, err := parseInitArgs([]string{"demo", "--auth=jwt-only"}); err == nil {
		t.Fatal("--auth accepted the bearer mode")
	}
	if _, err := parseInitArgs([]string{"demo", "--auth=jwt_only"}); err == nil {
		t.Fatal("--auth accepted the bearer mode")
	}
	steps := initWizardSteps(defaultInitOptions())
	for _, step := range steps {
		choice, ok := unwrapStep(step).(*choiceStep[initOptions])
		if !ok || step.label() != "Authentication" {
			continue
		}
		for _, option := range choice.choices {
			applied := initOptions{}
			option.apply(&applied)
			if applied.Auth == authJWTOnly {
				t.Fatalf("the authentication question offers %q", option.name)
			}
		}
	}
}

// The one line that makes a package publishable: its generated Go is tracked,
// because nothing in a consuming project can recreate it.
func TestPackagePresetCommitsItsGeneratedCode(t *testing.T) {
	options, err := parseInitArgs([]string{"--preset=package", "github.com/you/widget"})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	files := scaffoldFiles(options)
	ignore := files[".gitignore"]
	if strings.Contains(ignore, "_pw_gen.go") {
		t.Fatalf("a package excluded its generated Go from version control:\n%s", ignore)
	}
	// Hiding a file from an explorer is not excluding it from a commit, so the
	// editor rule stays.
	if !strings.Contains(files[".vscode/settings.json"], "**/*_pw_gen.go") {
		t.Error("the editor hide rule was dropped with the ignore rule")
	}
	// Nothing in the consumer can detect a stale artifact beyond a compile
	// error naming this package, so the check runs where the source is.
	workflow, ok := files[".github/workflows/generate.yml"]
	if !ok {
		t.Fatal("no staleness guard was scaffolded")
	}
	if !strings.Contains(workflow, "pw check") {
		t.Errorf("the workflow does not run the check:\n%s", workflow)
	}
}

// A package produces no binary, so everything an application scaffold writes
// for one is absent.
func TestPackagePresetWritesNoApplication(t *testing.T) {
	options, err := parseInitArgs([]string{"--preset=package", "github.com/you/widget"})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	files := scaffoldFiles(options)
	for _, absent := range []string{
		"cmd/widget/main.go",
		"templates/document.pw.html",
		pwenv.FileName(pwenv.Development),
		"devbox.json",
		"handlers/index.go",
	} {
		if _, ok := files[absent]; ok {
			t.Errorf("a package scaffolded %s", absent)
		}
	}
	config := files["popcornweb.toml"]
	for _, want := range []string{`kind = "package"`, `module = "github.com/you/widget"`} {
		if !strings.Contains(config, want) {
			t.Errorf("popcornweb.toml is missing %s:\n%s", want, config)
		}
	}
	// The consumer's generated bootstrap imports what the manifest names. A
	// generation purpose may not be ".", so the Go lives one directory down,
	// and without this key the consumer imports the module root and fails at
	// go mod tidy with "does not contain package".
	if !strings.Contains(config, `import = "github.com/you/widget/widget"`) {
		t.Errorf("the manifest does not name the package a consumer links:\n%s", config)
	}
	// Read the keys rather than the prose: the comments explain why an entry
	// point is absent, and that explanation names the key.
	for _, line := range strings.Split(config, "\n") {
		key, _, found := strings.Cut(strings.TrimSpace(line), "=")
		if !found || strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		switch strings.TrimSpace(key) {
		case "main":
			t.Errorf("a package named an entry point: %s", line)
		case "queries":
			// Declared and empty, which is the shape every purpose takes here.
			// A non-empty one would be a generated query carrying one engine's
			// placeholder syntax, and a package cannot know its consumer's
			// engine; the project loader refuses it outright.
			if strings.TrimSpace(strings.SplitN(line, "=", 2)[1]) != "[]" {
				t.Errorf("a package declared a query purpose: %s", line)
			}
		}
	}
}

// A package is named by the module path a consumer imports, because a project
// named widget and published at github.com/you/widget would have to be renamed
// before its first release.
func TestPackageIsNamedByItsModulePath(t *testing.T) {
	t.Chdir(t.TempDir())
	if err := validateModulePath("widget"); err == nil {
		t.Fatal("a single element was accepted as a published module path")
	}
	if err := validateModulePath("github.com/you/widget"); err != nil {
		t.Fatalf("a module path was rejected: %v", err)
	}
	if directory := moduleDirectory("github.com/you/widget"); directory != "widget" {
		t.Fatalf("directory = %q", directory)
	}
}

// Choosing a preset in the wizard has to produce the same project as passing
// it as a flag.
//
// It did not, and the failure was silent. Every question below the preset step
// still carried the cursor it was constructed with, and the fold that collects
// the answers runs over all of them in order — so the Router question wrote
// "registered" over the preset's "discovered" on its way past, and a reader who
// picked a preset got the default project with the preset's name on the review
// screen.
func TestPresetChosenInTheWizardMatchesTheFlag(t *testing.T) {
	t.Chdir(t.TempDir())
	for _, preset := range initPresetCatalog {
		if preset.name == presetManual || preset.name == presetPackage {
			continue
		}
		t.Run(preset.name, func(t *testing.T) {
			byFlag, err := parseInitArgs([]string{presetArgument(preset.name), "demo"})
			if err != nil {
				t.Fatalf("--preset=%s = %v", preset.name, err)
			}
			model := startWizard(t, preset.name, "demo", defaultInitOptions())
			byWizard := wizardResult(model, defaultInitOptions())
			if byWizard != byFlag {
				t.Fatalf("the wizard and the flag disagree\n wizard: %#v\n   flag: %#v", byWizard, byFlag)
			}
		})
	}
}

// Editing a row on the answer list must not disturb the rows the preset
// answered, which is the same fold the bug above lived in.
func TestEditingOneRowKeepsThePresetsOtherAnswers(t *testing.T) {
	t.Chdir(t.TempDir())
	model := startWizard(t, presetWebsiteLogin, "demo", defaultInitOptions())
	before := wizardResult(model, defaultInitOptions())
	after := wizardResult(answerHubRow(t, model, "TinyGo support", 2), defaultInitOptions())
	if after.TinyGo {
		t.Fatal("the edited answer did not take")
	}
	// Everything the preset decided is still what it decided.
	after.TinyGo = before.TinyGo
	if after != before {
		t.Fatalf("editing one row changed another\n before: %#v\n  after: %#v", before, after)
	}
}

// A page tree with a login calls into the handler package, because the account
// seams live there whichever router the project took. Without the import that
// call does not compile, and no preset before this one produced the pair.
func TestAPageTreeWithALoginImportsTheHandlerPackage(t *testing.T) {
	options, err := parseInitArgs([]string{presetArgument(presetWebsiteLogin), "demo"})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	files := scaffoldFiles(options)
	main := files["cmd/demo/main.go"]
	if !strings.Contains(main, "handlers.RegisterAccounts()") {
		t.Fatalf("the account seams are not installed:\n%s", main)
	}
	if !strings.Contains(main, `"demo/`+defaultRegisteredDir+`"`) {
		t.Fatalf("main calls into %s without importing it:\n%s", defaultRegisteredDir, main)
	}
	if _, ok := files[defaultRegisteredDir+"/accounts.go"]; !ok {
		t.Fatalf("the package main imports was not written")
	}
	// A page tree with no login imports no handler package, since there is
	// nothing in it to call.
	quiet, err := parseInitArgs([]string{presetArgument(presetWebsiteDiscovered), "demo"})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if quietMain := scaffoldFiles(quiet)["cmd/demo/main.go"]; strings.Contains(quietMain, `"demo/`+defaultRegisteredDir+`"`) {
		t.Fatalf("a login-free page tree imported a package it never calls:\n%s", quietMain)
	}
}

// A package builds no binary, so the two commands that produce one say that
// rather than getting part way and failing on the entry point they never find.
// The error they used to reach suggested running pw init, which in an existing
// package is wrong twice over.
func TestDevAndBuildDoNotApplyToAPackage(t *testing.T) {
	pkg := projectConfig{Kind: kindPackage}
	// pw generate is absent from this list on purpose: it is the command a
	// package regenerates its committed artifacts with, so it runs there and
	// reaches only the generated Go.
	for _, command := range []string{"dev", "build"} {
		err := refuseInPackage(pkg, command)
		if err == nil {
			t.Fatalf("%s: a package accepted a command that builds a binary", command)
		}
		if !strings.Contains(err.Error(), "package project") || !strings.Contains(err.Error(), "go test") {
			t.Errorf("%s: err = %v", command, err)
		}
	}
	// An application is unaffected, including one that never set the key.
	for _, kind := range []string{kindApplication, ""} {
		if err := refuseInPackage(projectConfig{Kind: kind}, "dev"); err != nil {
			t.Errorf("kind %q: err = %v", kind, err)
		}
	}
}

// The usage line has to name every installable capability. The two routers were
// installable and missing from it, and the tutorial tells a reader to install
// one of them.
func TestAddUsageNamesEveryCapability(t *testing.T) {
	for _, capability := range capabilityOrder {
		if !strings.Contains(addUsage, capability) {
			t.Errorf("pw add installs %q and its usage line does not name it: %s", capability, addUsage)
		}
	}
}

// presetArgument spells the flag for a preset name.
func presetArgument(name string) string { return "--preset=" + name }

// The API preset is the one whose caller is likely to be a browser page on
// another origin, so it carries the block that admits one — commented, because
// which origins may call is the whole content of the decision and a key set to
// a default nobody chose reads as one somebody did.
func TestAPIServerPresetCarriesACommentedCORSBlock(t *testing.T) {
	options, err := parseInitArgs([]string{"--preset=api-server", "demo"})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	config := scaffoldFiles(options)[pwenv.FileName(pwenv.Development)]
	for _, want := range []string{
		"# [security.cors]",
		"# enabled = true",
		`# allowed_origins = ["https://app.example.com"]`,
	} {
		if !strings.Contains(config, want) {
			t.Errorf("config is missing %q:\n%s", want, config)
		}
	}
	// Commented means off. A live section here would be a policy nobody chose,
	// and its empty origin list would fail startup rather than serve.
	if strings.Contains(config, "\n[security.cors]") || strings.Contains(config, "\nallowed_origins") {
		t.Errorf("the cross-origin block is live rather than commented:\n%s", config)
	}
}

// The API preset is the one publishing a machine contract on purpose, so it is
// the one that announces where the contract is. The origin stays commented for
// the reason the CORS block does: it is a deployment fact nobody has chosen yet.
func TestAPIServerPresetTurnsOnTheAPICatalog(t *testing.T) {
	options, err := parseInitArgs([]string{"--preset=api-server", "demo"})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	config := scaffoldFiles(options)[pwenv.FileName(pwenv.Development)]
	if !strings.Contains(config, "\napi_catalog = true\n") {
		t.Errorf("the catalog is not enabled:\n%s", config)
	}
	if !strings.Contains(config, `# api_catalog_origin = "https://api.example.com"`) {
		t.Errorf("the origin key is not offered:\n%s", config)
	}
	// The endpoint it links has to be in the same file, or the process refuses
	// to start and pw doctor reports PW0425.
	if !strings.Contains(config, `openapi = "/openapi.json"`) {
		t.Errorf("the catalog is on with no document to link:\n%s", config)
	}
}

// A browser project publishes an API as a side effect of having handlers, which
// is not the same as setting out to publish one, so its catalog ships off. The
// key is still written: a reader deciding whether to publish one should find it
// beside the endpoints it would link, not in the reference.
func TestBrowserPresetScaffoldsTheAPICatalogOff(t *testing.T) {
	options, err := parseInitArgs([]string{"demo"})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	config := scaffoldFiles(options)[pwenv.FileName(pwenv.Development)]
	if !strings.Contains(config, "\napi_catalog = false\n") {
		t.Errorf("the catalog switch was not scaffolded:\n%s", config)
	}
	if !strings.Contains(config, `# api_catalog_origin = "https://api.example.com"`) {
		t.Errorf("the origin key is not offered:\n%s", config)
	}
}

// A package has no listener and no configuration file at all, so nothing about
// an endpoint reaches it.
func TestPackageProjectScaffoldsNoAPICatalog(t *testing.T) {
	options, err := parseInitArgs([]string{"--preset=package", "demo"})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	for name, body := range scaffoldFiles(options) {
		if strings.Contains(body, "api_catalog") {
			t.Errorf("%s mentions the catalog:\n%s", name, body)
		}
	}
}

// A browser project scaffolds the CSRF section and no cross-origin one: its
// pages are served from the origin that reads them, so admitting another origin
// is a decision it has no reason to be shown.
func TestBrowserPresetCarriesNoCORSBlock(t *testing.T) {
	options, err := parseInitArgs([]string{"demo"})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	config := scaffoldFiles(options)[pwenv.FileName(pwenv.Development)]
	if strings.Contains(config, "security.cors") {
		t.Errorf("a browser project was offered a cross-origin policy:\n%s", config)
	}
}
