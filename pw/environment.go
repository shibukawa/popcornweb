package pw

import (
	"strings"

	"github.com/shibukawa/popcornweb/pwconfig"
)

// EnvVar names the environment variable that selects the runtime environment.
const EnvVar = pwconfig.EnvVar

// Well-known runtime environments. Any other lowercase token is also accepted.
const (
	EnvDevelopment = pwconfig.EnvDevelopment
	EnvStaging     = pwconfig.EnvStaging
	EnvProduction  = pwconfig.EnvProduction
)

// DefaultEnv is used when EnvVar is unset or empty.
const DefaultEnv = pwconfig.DefaultEnv

// Env returns the resolved runtime environment token.
//
// The value comes from APP_ENV and falls back to DefaultEnv. An invalid token
// resolves to DefaultEnv here and fails ParseConfig before requests are served.
func Env() string { return pwconfig.Env() }

// Development reports whether the development relaxations apply.
//
// An unset APP_ENV counts, because it resolves to development and running the
// framework with no environment set is what working on an application looks
// like. What does not count is any other named environment: the relaxations used
// to be refused by a list of the environments that must not have them — "stg",
// "prod", "production" — which meant "staging", "prd", "live", "uat" and every
// other spelling walked past a lock built to stop exactly them.
//
// The cost of admitting the unset case is that a deployment which forgot the
// variable gets development behaviour. That is a real exposure — query records
// carry bind values there, and the session cookie may travel without Secure — so
// it is not silent: see EnvironmentDeclared and the startup warning it drives.
func Development() bool { return pwconfig.Development() }

// EnvironmentDeclared reports whether APP_ENV named this process's environment,
// as opposed to the process defaulting to development because nothing set it.
//
// It exists for the startup warning. Nothing decides a relaxation on it: the
// decision is Development, and this only says whether anyone asked for the
// answer that came back.
func EnvironmentDeclared() bool { return pwconfig.EnvironmentDeclared() }

// reportEnvironment says at startup that nobody named this process's
// environment, and what that silence bought.
//
// An unset APP_ENV resolves to development, and development is not a neutral
// default: query records may carry bind values, the session cookie may travel
// without Secure, and an error page carries the whole problem rather than its
// status. Those are the right answers for someone working on the application and
// the wrong ones for a deployment that simply forgot the variable.
//
// So the framework keeps the convenient behaviour and stops being quiet about
// it. This is a warning rather than a refusal because refusing would fail the
// case it is trying to help — the developer who just cloned the project and ran
// it — and because a deployment that reads its own startup log has everything it
// needs here: the variable to set, and what setting it changes.
// reportCompressionCodings says so when compression is on and the list names a
// coding this framework does not encode. Naming one is otherwise a silent
// no-op: the coding is skipped and the rest of the list still negotiates, so an
// operator who wrote brotli into the file would never learn that nothing came
// of it.
func reportCompressionCodings(config MiddlewareConfig) {
	if !config.Compression {
		return
	}
	missing := unavailableResponseCodings(config.CompressionCodings)
	if len(missing) == 0 {
		return
	}
	processLogger().Warn("middleware.compression_codings names a coding this framework cannot produce",
		String("codings", strings.Join(missing, ",")),
		String("effect", "the named codings are skipped and the rest of the list still negotiates"),
		String("action", "remove the coding from the list; zstd and gzip are the two that encode"),
	)
}

// reportDotenvWarnings says what the dotenv read noticed and went on from:
// an APP_ENV written into the file that APP_ENV selected. It is a warning and
// not a refusal because the value the developer wanted is one file up.
func reportDotenvWarnings() {
	for _, warning := range pwconfig.DotenvWarnings() {
		processLogger().Warn("a dotenv file carries a setting that cannot apply there",
			String("detail", warning),
			String("action", "set "+EnvVar+" in the process environment or in .env, which are read before the file is chosen"),
		)
	}
}

func reportEnvironment() {
	if EnvironmentDeclared() {
		return
	}
	processLogger().Warn("popcornweb is running as development because no environment was named",
		String("variable", EnvVar),
		String("environment", Env()),
		String("effect", "query bind values may be logged, session.cookie.secure may be false, and error pages carry their detail"),
		String("action", "set "+EnvVar+" to the environment this process is actually running in"),
	)
}
