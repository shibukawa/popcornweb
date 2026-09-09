package pwcli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shibukawa/popcornweb/internal/pwcheck"
)

// A dotenv file is where a secret is meant to be, so a value read from one is
// not a disclosure; where the file stands still is, and the report names the
// file the value came from.
func TestDoctorReadsTheDotenvFilesOfTheDiagnosedToken(t *testing.T) {
	root := diagnosedProject(t, map[string]string{
		"config.prod.toml": `[auth]
enabled = true
[auth.oidc]
issuer = "https://issuer.example.com"
client_id = "fixture"
`,
		".env":            "AUTH_OIDC_CLIENT_ID=from-base\n",
		".env.prod.local": "AUTH_OIDC_CLIENT_SECRET=s3cret-value-of-production\n",
		".env.dev.local":  "AUTH_OIDC_CLIENT_SECRET=never-read-for-prod\n",
	})
	if err := os.Chmod(filepath.Join(root, ".env.prod.local"), 0o644); err != nil {
		t.Fatal(err)
	}
	report := diagnoseFor(t, root, doctorOptions{Envs: []string{"prod"}}, "PATH=/usr/bin")
	var prod doctorEnvReport
	for _, environment := range report.Envs {
		if environment.Env == "prod" {
			prod = environment
		}
	}
	if got := strings.Join(prod.DotenvFiles, ","); got != ".env,.env.prod.local" {
		t.Fatalf("dotenv files = %q", got)
	}
	sources := map[string]string{}
	for _, entry := range prod.Entries {
		sources[entry.Key] = entry.Source
	}
	if sources["auth.oidc.client_secret"] != "file_env:.env.prod.local" || sources["auth.oidc.client_id"] != "file_env:.env" {
		t.Fatalf("sources = %v", sources)
	}
	findings := findingsFor(report, "prod")
	if _, reported := findings[pwcheck.LiteralSecretInFile]; reported {
		t.Error("a secret read from a dotenv file is not a literal in the configuration file")
	}
	if finding, reported := findings[pwcheck.SecretFilePerms]; !reported {
		t.Error("a world-readable dotenv file holding a secret must be reported")
	} else if !strings.Contains(finding.Evidence, ".env.prod.local") {
		t.Errorf("the finding does not name the file: %+v", finding)
	}
	if _, reported := findings[pwcheck.ProviderNotDeclared]; reported {
		t.Error("the provider values the dotenv files supply count as declared")
	}
}

// The template is committed, so a value in it is a committed secret whatever
// environment is diagnosed.
func TestDoctorReportsASecretAssignedInTheDotenvTemplate(t *testing.T) {
	root := diagnosedProject(t, map[string]string{
		".env.example": "# fill in\nAUTH_OIDC_CLIENT_SECRET=real-looking-value\nAUTH_OIDC_CLIENT_ID=\nDATABASE_URL=sqlite://local.db\n",
		// The scaffolded config.prod.toml references ${DATABASE_URL}, and an
		// undefined reference fails the load; the base file is where a
		// checkout keeps it.
		".env": "DATABASE_URL=sqlite://fixture.db\n",
	})
	report := diagnoseFor(t, root, doctorOptions{Envs: []string{"dev", "prod"}}, "PATH=/usr/bin")
	for _, env := range []string{"dev", "prod"} {
		finding, reported := findingsFor(report, env)[pwcheck.EnvTemplateHoldsSecret]
		if !reported {
			t.Errorf("%s: the template's secret was not reported", env)
			continue
		}
		if finding.Severity != pwcheck.Error || !strings.Contains(finding.Evidence, "AUTH_OIDC_CLIENT_SECRET in .env.example") {
			t.Errorf("%s: finding = %+v", env, finding)
		}
		if strings.Contains(finding.Message+finding.Evidence, "real-looking-value") {
			t.Errorf("%s: the value was printed", env)
		}
	}
}

// The scaffolded template assigns nothing, so a fresh project is silent here.
func TestDoctorIsSilentOnTheScaffoldedDotenvTemplate(t *testing.T) {
	root := diagnosedProject(t, nil)
	report := diagnoseFor(t, root, doctorOptions{Envs: []string{"dev"}}, "PATH=/usr/bin")
	if _, reported := findingsFor(report, "dev")[pwcheck.EnvTemplateHoldsSecret]; reported {
		t.Fatal("the scaffolded template was reported")
	}
}
