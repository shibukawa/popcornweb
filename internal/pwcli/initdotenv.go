package pwcli

import (
	"strings"

	"github.com/shibukawa/popcornweb/internal/pwenv"
)

// dotenvVariable is one line of the committed template: the variable a
// deployment supplies and the one sentence that says what it feeds.
type dotenvVariable struct {
	name string
	help string
}

// dotenvTemplateScaffold writes .env.example, per requirement:dotenv-files.
//
// The template is the one dotenv file .gitignore lets through, so it carries
// names and never values: every variable the scaffolded configuration expects
// the deployment to supply, with an empty assignment a copy fills in. The list
// follows the answers the wizard gave, the way config.prod.toml does, because a
// template naming a variable the build does not bind would be read as a
// requirement. A value written here is what PW0438 reports.
func dotenvTemplateScaffold(options initOptions) string {
	var b strings.Builder
	local := pwenv.DotenvBase + pwenv.DotenvLocalSuffix
	b.WriteString(`# Dotenv template. Copy it to ` + local + ` (every environment) or to
# ` + pwenv.DotenvFileName(pwenv.Development) + pwenv.DotenvLocalSuffix + `, ` + pwenv.DotenvFileName(pwenv.Staging) + pwenv.DotenvLocalSuffix + `, ` + pwenv.DotenvFileName(pwenv.Production) + pwenv.DotenvLocalSuffix + ` (one
# environment) and fill in the values. git ignores every *` + pwenv.DotenvLocalSuffix + ` file; .env,
# .env.$APP_ENV, and this template are committed and carry no secret.
#
# At startup .env is read first, then ` + local + `, then .env.$APP_ENV, then
# .env.$APP_ENV` + pwenv.DotenvLocalSuffix + `, then every file under ` + pwenv.SecretDir + `, then the
# process environment, and a later source wins. A variable exported in the
# shell or set by the platform therefore always beats a file.
#
# ` + pwenv.Var + ` itself is read from the process, then from .env and ` + local + `; it
# selects the second pair, so it cannot be set there. Uncomment to pin this
# checkout:
#
# ` + pwenv.Var + `=` + pwenv.Development + `
#
# The full variable list of this build, including your own [app] keys:
#
#   ./` + options.Name + ` --generate-config env
`)
	for _, variable := range dotenvTemplateVariables(options) {
		b.WriteString("\n# " + variable.help + "\n" + variable.name + "=\n")
	}
	return b.String()
}

// dotenvTemplateVariables lists what the selected capabilities read from the
// environment outside development, in the order config.prod.toml mentions them.
func dotenvTemplateVariables(options initOptions) []dotenvVariable {
	var variables []dotenvVariable
	if options.Database {
		variables = append(variables, dotenvVariable{
			name: "DATABASE_URL",
			help: "the database DSN config." + pwenv.Production + ".toml references as ${DATABASE_URL}; " +
				pwenv.FileName(pwenv.Development) + " carries the development one as a literal",
		})
	}
	if servesBrowserLogin(options) {
		variables = append(variables, dotenvVariable{
			name: "SESSION_KEYRING_SECRET",
			help: "base64 secret signing and sealing everything the browser carries: openssl rand -base64 32",
		})
		if sessionBackend(options) == sessionRedis {
			variables = append(variables, dotenvVariable{
				name: "SESSION_REDIS_DSN",
				help: "redis:// or rediss:// session server",
			})
		}
	}
	if usesOIDC(options.Auth) {
		variables = append(variables,
			dotenvVariable{name: "AUTH_OIDC_ISSUER", help: "the OpenID provider's issuer URL"},
			dotenvVariable{name: "AUTH_OIDC_CLIENT_ID", help: "the client registered with that provider"},
			dotenvVariable{name: "AUTH_OIDC_CLIENT_SECRET", help: "that client's secret"},
		)
	}
	return variables
}
