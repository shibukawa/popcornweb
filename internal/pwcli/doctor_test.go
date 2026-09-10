package pwcli

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shibukawa/popcornweb/internal/pwcheck"
)

// diagnosedProject is a scaffolded project plus whatever the case under test
// writes into it. The scaffold is the real starter, so a finding here is a
// finding a developer would see.
func diagnosedProject(t *testing.T, files map[string]string) string {
	t.Helper()
	root := writeScaffoldedProject(t, initOptions{
		Name: "fixture", TinyGo: true, Devbox: true, Database: true, Auth: authNone,
	})
	for path, content := range files {
		target := filepath.Join(root, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(target, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func diagnoseFor(t *testing.T, root string, options doctorOptions, environ ...string) doctorReport {
	t.Helper()
	if options.Format == "" {
		options.Format = "text"
	}
	report, err := diagnose(context.Background(), root, options, environ)
	if err != nil {
		t.Fatalf("diagnose: %v", err)
	}
	return report
}

// findingsFor collects the identifiers reported for one environment.
func findingsFor(report doctorReport, env string) map[string]doctorFinding {
	found := map[string]doctorFinding{}
	for _, environment := range report.Envs {
		if environment.Env != env {
			continue
		}
		for _, finding := range environment.Findings {
			found[finding.Check.ID] = finding
		}
	}
	return found
}

func TestDoctorRequiresTheSelectedSQLiteDriverImport(t *testing.T) {
	config := environmentConfig{Env: "dev", Values: map[string]configValue{
		"middleware.rdb.connections[0].dsn": {Raw: "sqlite://fixture.db"},
	}}
	unlinked := importGraph{packages: map[string]bool{"github.com/shibukawa/popcornweb/pw": true}}
	run := checkRun{checkContext: checkContext{Env: "dev", Config: config, Graph: unlinked}}
	run.checkWiring()
	if len(run.findings) != 1 || run.findings[0].Check.ID != pwcheck.MissingSQLDriver {
		t.Fatalf("findings = %#v, want the missing SQL driver", run.findings)
	}
	if !strings.Contains(run.findings[0].Evidence, sqliteDriverPackage) {
		t.Fatalf("evidence = %q, want the import to add", run.findings[0].Evidence)
	}
	connections := resolveConnections(config, unlinked)
	if len(connections) != 1 || connections[0].Driver != "sqlite (no driver linked)" {
		t.Fatalf("connections = %#v", connections)
	}
	implementation := featureImplementation(doctorFeature{Name: "database", State: "on"}, unlinked, config, engineSQLite)
	if implementation != "sqlite (no driver linked)" {
		t.Fatalf("implementation = %q", implementation)
	}

	linked := importGraph{packages: map[string]bool{sqliteDriverPackage: true}}
	run = checkRun{checkContext: checkContext{Env: "dev", Config: config, Graph: linked}}
	run.checkWiring()
	if len(run.findings) != 0 {
		t.Fatalf("linked SQLite findings = %#v", run.findings)
	}
}

const deployedConfig = `[session]
enabled = true
backend = "rdb"
[session.cookie]
secure = false

[observability]
minimum_level = "debug"
[observability.query]
enabled = "on"
bind_values = "on"

[middleware.rdb]
enabled = true

[[middleware.rdb.connections]]
dsn = "sqlite://fixture.db"
`

// The same file is correct in dev and inadvisable in a deployment, which is the
// whole reason the environment is an argument rather than an ambient variable.
func TestSameConfigurationIsJudgedByTheDiagnosedEnvironment(t *testing.T) {
	root := diagnosedProject(t, map[string]string{
		"config.dev.toml":  deployedConfig,
		"config.prod.toml": deployedConfig,
	})
	report := diagnoseFor(t, root, doctorOptions{Envs: []string{"dev", "prod"}})

	dev, prod := findingsFor(report, "dev"), findingsFor(report, "prod")
	for _, id := range []string{pwcheck.QueryDiagnosticsOn, pwcheck.BindValuesOn, pwcheck.VerboseLogLevel} {
		if _, reported := dev[id]; reported {
			t.Errorf("%s must stay silent in dev", id)
		}
		if _, reported := prod[id]; !reported {
			t.Errorf("%s must fire for prod", id)
		}
	}
	if finding, reported := prod[pwcheck.InsecureSessionCookie]; !reported {
		t.Error("an insecure session cookie must be reported for prod")
	} else if finding.Severity != pwcheck.Error {
		t.Errorf("severity = %v, want error", finding.Severity)
	}
	// api:cli-init writes secure = false into the development file on purpose,
	// so saying it back in dev would be a note with nothing to do about it.
	if _, reported := dev[pwcheck.InsecureSessionCookie]; reported {
		t.Error("an insecure session cookie must stay silent in dev")
	}
}

// A secret in a file is the finding, and the value is never the evidence.
func TestSecretFindingsNameThePlaceAndNeverTheValue(t *testing.T) {
	const password = "s3cr3t-should-never-be-printed"
	root := diagnosedProject(t, map[string]string{
		"config.prod.toml": `[middleware.rdb]
enabled = true

[[middleware.rdb.connections]]
dsn = "mysql://app:` + password + `@db.internal:3306/app"
`,
	})
	report := diagnoseFor(t, root, doctorOptions{Envs: []string{"prod"}})
	findings := findingsFor(report, "prod")
	finding, reported := findings[pwcheck.LiteralSecretInFile]
	if !reported {
		t.Fatal("a literal secret in a deployment file must be reported")
	}
	if finding.Severity != pwcheck.Error {
		t.Errorf("severity = %v, want error", finding.Severity)
	}
	if !strings.Contains(finding.Evidence, "config.prod.toml") {
		t.Errorf("evidence %q must name the file", finding.Evidence)
	}
	var rendered strings.Builder
	writeDoctorText(&rendered, report, doctorStyle{})
	if strings.Contains(rendered.String(), password) {
		t.Fatal("the report printed a secret value")
	}
}

// pw init writes a generated session keyring into config.dev.toml so that a
// scaffolded project runs without an authored secret. That convenience is
// bounded by this check: the same literal is a note in dev and an error
// anywhere else, so it cannot travel to a deployment unnoticed.
//
// Nothing here names the keyring: the check reads the configbind secret
// classification, so the key was covered the moment it was marked secret.
func TestTheDevelopmentKeyringIsANoteInDevAndAnErrorElsewhere(t *testing.T) {
	const keyring = `keyring.secret = "c2VjcmV0LXRoaXJ0eS10d28tYnl0ZXMtZm9yLWEtdGVzdCE="`
	body := "[session]\nenabled = true\n" + keyring + "\n"
	root := diagnosedProject(t, map[string]string{
		"config.dev.toml":  body,
		"config.prod.toml": body,
	})
	report := diagnoseFor(t, root, doctorOptions{Envs: []string{"dev", "prod"}})

	finding, reported := findingsFor(report, "prod")[pwcheck.LiteralSecretInFile]
	if !reported {
		t.Fatal("a keyring literal must be reported outside development")
	}
	if finding.Severity != pwcheck.Error {
		t.Errorf("prod severity = %v, want error", finding.Severity)
	}
	if !strings.Contains(finding.Evidence, "config.prod.toml") {
		t.Errorf("evidence %q must name the file", finding.Evidence)
	}
	if finding, reported := findingsFor(report, "dev")[pwcheck.LiteralSecretInFile]; reported &&
		finding.Severity != pwcheck.Note {
		t.Errorf("dev severity = %v, want note, because pw init put it there", finding.Severity)
	}
}

// Where a secret is kept is a deployment question. A development machine keeps
// the password of the database running beside it in the file it shares with the
// people who run it, so the same content that is an error for prod is nothing
// at all for dev.
func TestASecretInTheDevelopmentFileIsNotAFinding(t *testing.T) {
	const withCredential = `[middleware.rdb]
enabled = true

[[middleware.rdb.connections]]
dsn = "postgres://app:s3cret@localhost:5432/fixture"
`
	root := diagnosedProject(t, map[string]string{
		"config.dev.toml":  withCredential,
		"config.prod.toml": withCredential,
	})
	// The file mode is the third of the three checks, and the scaffold writes
	// exactly this one.
	if err := os.Chmod(filepath.Join(root, "config.dev.toml"), 0o644); err != nil {
		t.Fatal(err)
	}
	report := diagnoseFor(t, root, doctorOptions{Envs: []string{"dev", "prod"}})
	dev, prod := findingsFor(report, "dev"), findingsFor(report, "prod")
	for _, id := range []string{pwcheck.LiteralSecretInFile, pwcheck.SecretFileNotIgnored, pwcheck.SecretFilePerms} {
		if finding, reported := dev[id]; reported {
			t.Errorf("%s must stay silent in dev: %s", id, finding.Message)
		}
	}
	if finding, reported := prod[pwcheck.LiteralSecretInFile]; !reported {
		t.Error("a literal secret in a deployment file must still be reported")
	} else if finding.Severity != pwcheck.Error {
		t.Errorf("severity = %v, want error", finding.Severity)
	}
}

// A sqlite path is secret-classified by name and carries no credential, so
// reporting it would train a reader to ignore the finding that matters.
func TestCredentialFreeDSNIsNotReportedAsADisclosure(t *testing.T) {
	root := diagnosedProject(t, map[string]string{
		"config.prod.toml": `[middleware.rdb]
enabled = true

[[middleware.rdb.connections]]
dsn = "sqlite://fixture.db"
`,
	})
	report := diagnoseFor(t, root, doctorOptions{Envs: []string{"prod"}})
	if _, reported := findingsFor(report, "prod")[pwcheck.LiteralSecretInFile]; reported {
		t.Error("a sqlite path must not be reported as a disclosed secret")
	}
}

// Only a reader of every environment file can see a shared secret, which is why
// it is a doctor check and not a startup one.
func TestASecretSharedBetweenEnvironmentsIsReported(t *testing.T) {
	shared := `[auth]
enabled = true
[auth.oidc]
issuer = "https://issuer.example.com"
client_id = "fixture"
client_secret = "shared-across-environments"
`
	root := diagnosedProject(t, map[string]string{
		"config.dev.toml":  shared,
		"config.prod.toml": shared,
	})
	report := diagnoseFor(t, root, doctorOptions{Envs: []string{"all"}})
	finding, reported := findingsFor(report, "prod")[pwcheck.SecretSharedBetween]
	if !reported {
		t.Fatal("one secret in two environment files must be reported")
	}
	if !strings.Contains(finding.Evidence, "dev") || !strings.Contains(finding.Evidence, "prod") {
		t.Errorf("evidence %q must name both environments", finding.Evidence)
	}
	if strings.Contains(finding.Evidence, "shared-across-environments") {
		t.Error("the evidence must name the keys, not the value")
	}
}

// dev may authenticate against the local emulator; a deployment must not.
func TestDevelopmentIssuerIsOnlyAFindingOutsideDev(t *testing.T) {
	development := `[auth]
enabled = true
callback_path = "/auth/callback"
[auth.oidc]
issuer = "http://localhost:18080/"
client_id = "fixture"
client_secret = "fixture-secret"
redirect_url = "http://localhost:8080/auth/callback"
allow_loopback_http = true
`
	root := diagnosedProject(t, map[string]string{
		"config.dev.toml":  development,
		"config.prod.toml": development,
	})
	report := diagnoseFor(t, root, doctorOptions{Envs: []string{"dev", "prod"}})
	dev, prod := findingsFor(report, "dev"), findingsFor(report, "prod")
	for _, id := range []string{pwcheck.DevelopmentIssuer, pwcheck.InsecureIssuer} {
		if _, reported := dev[id]; reported {
			t.Errorf("%s must stay silent in dev, where the local provider is the arrangement", id)
		}
		if _, reported := prod[id]; !reported {
			t.Errorf("%s must fire for prod", id)
		}
	}
}

// The provider would redirect to a URL the application does not serve, and a
// loopback redirect that works locally hides it.
func TestRedirectPathDisagreementIsReportedInEveryEnvironment(t *testing.T) {
	root := diagnosedProject(t, map[string]string{
		"config.dev.toml": `[auth]
enabled = true
callback_path = "/auth/callback"
[auth.oidc]
issuer = "https://issuer.example.com"
client_id = "fixture"
client_secret = "fixture-secret"
redirect_url = "https://app.example.com/callback"
`,
	})
	report := diagnoseFor(t, root, doctorOptions{Envs: []string{"dev"}})
	if _, reported := findingsFor(report, "dev")[pwcheck.RedirectDisagreement]; !reported {
		t.Fatal("a redirect URL that does not end at callback_path must be reported in dev too")
	}
}

func TestDynamicOIDCRedirectIsDevelopmentOnly(t *testing.T) {
	for _, redirectLine := range []string{"", "redirect_url = \"/auth/callback\"\n"} {
		config := `[auth]
enabled = true
callback_path = "/auth/callback"
[auth.oidc]
issuer = "http://localhost:18080/"
client_id = "fixture"
client_secret = "fixture-secret"
allow_loopback_http = true
` + redirectLine
		root := diagnosedProject(t, map[string]string{
			"config.dev.toml":  config,
			"config.prod.toml": config,
		})
		report := diagnoseFor(t, root, doctorOptions{Envs: []string{"dev", "prod"}})
		if _, reported := findingsFor(report, "dev")[pwcheck.DynamicOIDCRedirect]; reported {
			t.Fatal("a request-derived redirect must be accepted in dev")
		}
		finding, reported := findingsFor(report, "prod")[pwcheck.DynamicOIDCRedirect]
		if !reported || finding.Severity != pwcheck.Error {
			t.Fatalf("a request-derived redirect in prod = %#v, %v; want error", finding, reported)
		}
	}
}

func TestAbsoluteOIDCRedirectIsAcceptedForDeployment(t *testing.T) {
	root := diagnosedProject(t, map[string]string{
		"config.prod.toml": `[auth]
enabled = true
callback_path = "/auth/callback"
[auth.oidc]
issuer = "https://issuer.example.com"
client_id = "fixture"
client_secret = "fixture-secret"
redirect_url = "https://app.example.com/auth/callback"
`,
	})
	report := diagnoseFor(t, root, doctorOptions{Envs: []string{"prod"}})
	if _, reported := findingsFor(report, "prod")[pwcheck.DynamicOIDCRedirect]; reported {
		t.Fatal("an absolute deployed redirect must not be reported")
	}
}

// An absent provider value is either platform injection or a real gap, and this
// host cannot tell which, so it is a note naming what must be set.
func TestUndeclaredProviderIsANoteNamingTheVariables(t *testing.T) {
	root := diagnosedProject(t, map[string]string{
		"config.prod.toml": "[auth]\nenabled = true\n",
	})
	report := diagnoseFor(t, root, doctorOptions{Envs: []string{"prod"}})
	finding, reported := findingsFor(report, "prod")[pwcheck.ProviderNotDeclared]
	if !reported {
		t.Fatal("an undeclared provider must be reported for a deployment")
	}
	if finding.Severity != pwcheck.Note {
		t.Errorf("severity = %v, want note; this host cannot tell injection from a gap", finding.Severity)
	}
	if !strings.Contains(finding.Evidence, "AUTH_OIDC_ISSUER") {
		t.Errorf("evidence %q must name the environment variables", finding.Evidence)
	}
}

// The orphan still compiles and its registrations still run, so a deleted page
// keeps serving. Nothing else in the toolchain reports it.
func TestOrphanGeneratedFileIsAnError(t *testing.T) {
	root := diagnosedProject(t, map[string]string{
		"handlers/removed_pw_gen.go": "package handlers\n",
	})
	report := diagnoseFor(t, root, doctorOptions{Envs: []string{"dev"}})
	finding, reported := findingsFor(report, "dev")[pwcheck.OrphanGeneratedFile]
	if !reported {
		t.Fatal("a generated file whose source is gone must be reported")
	}
	if finding.Severity != pwcheck.Error {
		t.Errorf("severity = %v, want error", finding.Severity)
	}
	if !strings.Contains(finding.Evidence, "handlers/removed_pw_gen.go") {
		t.Errorf("evidence %q must name the file", finding.Evidence)
	}
}

// A package-level artifact has no source to outlive, so it must not read as an
// orphan.
func TestPackageArtifactsAreNotOrphans(t *testing.T) {
	root := diagnosedProject(t, map[string]string{
		"handlers/tinybind_openapi_pw_gen.go": "package handlers\n",
	})
	report := diagnoseFor(t, root, doctorOptions{Envs: []string{"dev"}})
	if _, reported := findingsFor(report, "dev")[pwcheck.OrphanGeneratedFile]; reported {
		t.Error("a package-level generated artifact must not be reported as an orphan")
	}
}

// Nothing the analysis could not determine may be silently skipped: the report
// says what it did not look at.
func TestLimitsRecordWhatWasNotExamined(t *testing.T) {
	root := diagnosedProject(t, map[string]string{"config.dev.toml": deployedConfig})
	report := diagnoseFor(t, root, doctorOptions{Envs: []string{"dev"}})
	subjects := map[string]bool{}
	for _, limit := range report.allLimits() {
		subjects[limit.Subject] = true
		if limit.Reason == "" || limit.Effect == "" {
			t.Errorf("limit %q must state a reason and what it suppressed", limit.Subject)
		}
	}
	for _, want := range []string{"routes", "database", "environment variables"} {
		if !subjects[want] {
			t.Errorf("the report must record that %q was not examined", want)
		}
	}
}

// A clean run exits zero; an error finding does not; --strict promotes warnings.
func TestExitStatusFollowsTheFindings(t *testing.T) {
	clean := doctorReport{Envs: []doctorEnvReport{{Env: "dev"}}}
	if failed, _ := clean.failing(false); failed {
		t.Error("a report without findings must exit zero")
	}
	warned := doctorReport{Envs: []doctorEnvReport{{Env: "prod", Findings: []doctorFinding{
		{Severity: pwcheck.Warning},
	}}}}
	if failed, _ := warned.failing(false); failed {
		t.Error("a warning alone must not fail without --strict")
	}
	if failed, _ := warned.failing(true); !failed {
		t.Error("--strict must promote a warning to a failing exit")
	}
	broken := doctorReport{Envs: []doctorEnvReport{{Env: "prod", Findings: []doctorFinding{
		{Severity: pwcheck.Error},
	}}}}
	if failed, _ := broken.failing(false); !failed {
		t.Error("an error finding must fail")
	}
}

// The JSON shape is a supported interface, because a release gate asserts on it.
func TestJSONCarriesIdentifiersAndDocumentationLinks(t *testing.T) {
	root := diagnosedProject(t, map[string]string{"config.prod.toml": deployedConfig})
	report := diagnoseFor(t, root, doctorOptions{Envs: []string{"prod"}, Format: "json"})
	var rendered strings.Builder
	if err := writeDoctorJSON(&rendered, report); err != nil {
		t.Fatal(err)
	}
	var document struct {
		HostMode     string         `json:"host_mode"`
		Counts       map[string]int `json:"counts"`
		Environments []struct {
			Env      string `json:"env"`
			Findings []struct {
				ID       string `json:"id"`
				Severity string `json:"severity"`
				Docs     string `json:"docs"`
			} `json:"findings"`
		} `json:"environments"`
	}
	if err := json.Unmarshal([]byte(rendered.String()), &document); err != nil {
		t.Fatal(err)
	}
	if len(document.Environments) != 1 || document.Environments[0].Env != "prod" {
		t.Fatalf("environments = %+v", document.Environments)
	}
	if len(document.Environments[0].Findings) == 0 {
		t.Fatal("the deployed fixture must produce findings")
	}
	for _, finding := range document.Environments[0].Findings {
		if finding.ID == "" || finding.Severity == "" {
			t.Errorf("finding %+v must carry an identifier and a severity", finding)
		}
		if !strings.Contains(finding.Docs, strings.ToLower(finding.ID)) {
			t.Errorf("docs %q must resolve from the identifier", finding.Docs)
		}
	}
	if document.Counts["warning"] == 0 {
		t.Error("counts must be reported")
	}
}

// The environment is an option, and without one it is the APP_ENV of this
// process, then dev.
func TestEnvironmentSelectionPrefersTheOptionThenAppEnv(t *testing.T) {
	root := diagnosedProject(t, map[string]string{"config.stg.toml": "", "config.prod.toml": ""})
	tokens, err := resolveTokens(root, doctorOptions{}, []string{"APP_ENV=stg"})
	if err != nil {
		t.Fatal(err)
	}
	if len(tokens) != 1 || tokens[0] != "stg" {
		t.Errorf("tokens = %v, want [stg]", tokens)
	}
	if tokens, err = resolveTokens(root, doctorOptions{}, nil); err != nil || tokens[0] != "dev" {
		t.Errorf("tokens = %v, err = %v; want [dev]", tokens, err)
	}
	if tokens, err = resolveTokens(root, doctorOptions{Envs: []string{"all"}}, nil); err != nil {
		t.Fatal(err)
	} else if strings.Join(tokens, ",") != "dev,prod,stg" {
		t.Errorf("--env=all discovered %v", tokens)
	}
	if _, err := resolveTokens(root, doctorOptions{Envs: []string{"Prod/../x"}}, nil); err == nil {
		t.Error("an invalid token must be rejected")
	}
}

// The question a multi-environment run asks is which value changes when the
// environment does, so identical keys are not repeated.
func TestMultipleEnvironmentsRenderOnlyTheDifferingKeys(t *testing.T) {
	root := diagnosedProject(t, map[string]string{
		"config.dev.toml":  "[observability]\nstdout_format = \"plaintext\"\n",
		"config.prod.toml": "[observability]\nstdout_format = \"json\"\n",
	})
	report := diagnoseFor(t, root, doctorOptions{Envs: []string{"dev", "prod"}})
	var rendered strings.Builder
	writeDoctorText(&rendered, report, doctorStyle{})
	page := rendered.String()
	if !strings.Contains(page, "configuration, where the environments differ") {
		t.Fatal("a multi-environment report must show the differences")
	}
	if !strings.Contains(page, "observability.stdout_format") {
		t.Error("a key whose value differs must be listed")
	}
	if strings.Count(page, "server.read_header_timeout") != 0 {
		t.Error("a key that is identical everywhere answers nothing and must be omitted")
	}
}

func TestOptionsRejectUnknownArguments(t *testing.T) {
	if _, err := parseDoctorOptions([]string{"--fix"}); err == nil {
		t.Error("doctor has no --fix; remedies belong to pw add, pw generate, and pw migrate")
	}
	options, err := parseDoctorOptions([]string{"--env=prod", "--env=stg", "--strict", "--online", "--format=json"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(options.Envs, ",") != "prod,stg" || !options.Strict || !options.Online || options.Format != "json" {
		t.Fatalf("options = %+v", options)
	}
}

// The runtime reads only the provider section auth.mode selects, and so do
// these advisories: an oauth_only project is judged on auth.oauth and told to
// set AUTH_OAUTH_*, never on an oidc section it is forbidden to fill.
func TestOAuthOnlyIsDiagnosedOnItsOwnSection(t *testing.T) {
	root := diagnosedProject(t, map[string]string{
		"config.prod.toml": `[auth]
enabled = true
mode = "oauth_only"
callback_path = "/auth/callback"
[auth.oauth]
provider = "x"
redirect_url = "https://app.example.com/auth/callback"
`,
	})
	report := diagnoseFor(t, root, doctorOptions{Envs: []string{"prod"}})
	findings := findingsFor(report, "prod")
	if _, reported := findings[pwcheck.DynamicOIDCRedirect]; reported {
		t.Error("an absolute auth.oauth.redirect_url must not be reported as request-derived")
	}
	for _, code := range []string{pwcheck.DevelopmentIssuer, pwcheck.InsecureIssuer, pwcheck.LoopbackPairing, pwcheck.RedirectDisagreement} {
		if _, reported := findings[code]; reported {
			t.Errorf("%s fired for an oauth_only project with no oidc section", code)
		}
	}
	finding, reported := findings[pwcheck.ProviderNotDeclared]
	if !reported {
		t.Fatal("empty oauth client values must be reported for a deployment")
	}
	for _, want := range []string{"AUTH_OAUTH_CLIENT_ID", "AUTH_OAUTH_CLIENT_SECRET"} {
		if !strings.Contains(finding.Evidence, want) {
			t.Errorf("evidence %q must name %s", finding.Evidence, want)
		}
	}
	if strings.Contains(finding.Evidence, "OIDC") {
		t.Errorf("evidence %q names an oidc variable the mode refuses", finding.Evidence)
	}
}

func TestOAuthOnlyRedirectAdvisoriesReadTheOAuthSection(t *testing.T) {
	root := diagnosedProject(t, map[string]string{
		"config.prod.toml": `[auth]
enabled = true
mode = "oauth_only"
callback_path = "/auth/callback"
[auth.oauth]
provider = "x"
client_id = "fixture"
client_secret = "fixture-secret"
redirect_url = "https://app.example.com/elsewhere"
allow_loopback_http = true
`,
	})
	report := diagnoseFor(t, root, doctorOptions{Envs: []string{"prod"}})
	findings := findingsFor(report, "prod")
	if finding, reported := findings[pwcheck.RedirectDisagreement]; !reported || !strings.Contains(finding.Evidence, "auth.oauth.redirect_url") {
		t.Errorf("redirect disagreement = %#v, %v; want it named on auth.oauth.redirect_url", finding, reported)
	}
	if finding, reported := findings[pwcheck.InsecureIssuer]; !reported || !strings.Contains(finding.Evidence, "auth.oauth.allow_loopback_http") {
		t.Errorf("loopback exception = %#v, %v; want it named on auth.oauth.allow_loopback_http", finding, reported)
	}
	if _, reported := findings[pwcheck.ProviderNotDeclared]; reported {
		t.Error("declared oauth client values must count as declared")
	}
	root = diagnosedProject(t, map[string]string{
		"config.prod.toml": `[auth]
enabled = true
mode = "oauth_only"
[auth.oauth]
provider = "x"
client_id = "fixture"
client_secret = "fixture-secret"
`,
	})
	report = diagnoseFor(t, root, doctorOptions{Envs: []string{"prod"}})
	if finding, reported := findingsFor(report, "prod")[pwcheck.DynamicOIDCRedirect]; !reported || !strings.Contains(finding.Evidence, "auth.oauth.redirect_url") {
		t.Errorf("empty oauth redirect = %#v, %v; want PW0437 naming auth.oauth.redirect_url", finding, reported)
	}
}

// A passkey-only login has no provider and a bearer API runs no login
// ceremony, so neither has a provider section for the advisories to read.
func TestModesWithoutAProviderSkipTheProviderAdvisories(t *testing.T) {
	for _, mode := range []string{"passkey_only", "jwt_only"} {
		root := diagnosedProject(t, map[string]string{
			"config.prod.toml": "[auth]\nenabled = true\nmode = \"" + mode + "\"\n",
		})
		report := diagnoseFor(t, root, doctorOptions{Envs: []string{"prod"}})
		findings := findingsFor(report, "prod")
		for _, code := range []string{pwcheck.ProviderNotDeclared, pwcheck.DynamicOIDCRedirect, pwcheck.InsecureIssuer, pwcheck.DevelopmentIssuer} {
			if _, reported := findings[code]; reported {
				t.Errorf("%s fired under %s, which has no provider section", code, mode)
			}
		}
	}
}
