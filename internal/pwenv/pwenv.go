// Package pwenv resolves the runtime environment token that selects
// project-local configuration files.
package pwenv

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Var names the environment variable that selects the runtime environment.
const Var = "APP_ENV"

// DevLogFileVar names the private handoff from pw dev to the application
// process. The value is an absolute JSONL path selected once per developer-loop
// invocation; it is not a runtime configuration key and is never exported to
// the developer's shell.
const DevLogFileVar = "PW_DEV_LOG_FILE"

// Well-known runtime environments. Any other lowercase token is also accepted.
const (
	Development = "dev"
	Staging     = "stg"
	Production  = "prod"
)

// Default is used when Var is unset or empty.
const Default = Development

// NeutralFileName is the environment-neutral name used by the user and system
// configuration directories.
const NeutralFileName = "config.toml"

// Resolve reads Var from environ, or from the process environment when environ
// is nil, and validates it as a config filename component.
func Resolve(environ []string) (string, error) {
	value, _, err := ResolveDeclared(environ)
	return value, err
}

// ResolveDeclared is Resolve, and also reports whether the environment was named
// rather than defaulted.
//
// The distinction exists because "dev" is two different facts wearing one name.
// A deployment that sets APP_ENV=dev is asking for the development relaxations.
// A deployment that sets nothing is not asking for anything — it forgot — and
// answering "dev" to that is how a production service ends up logging every SQL
// statement with its bind values and accepting a session cookie without Secure.
//
// Which is why the relaxations key off declared rather than off the token. The
// token still defaults, because it also selects the config file and refusing to
// start over a missing variable would be a poor trade for that. `pw dev` sets
// the variable, so the ordinary development path declares it and keeps
// everything it had.
func ResolveDeclared(environ []string) (value string, declared bool, err error) {
	raw := lookup(environ, Var)
	if strings.TrimSpace(raw) == "" {
		return Default, false, nil
	}
	if !Valid(raw) {
		return "", false, fmt.Errorf("popcornweb: invalid %s %q: use lowercase letters, digits, '-' or '_'", Var, raw)
	}
	return raw, true, nil
}

// Valid reports whether value is usable as a config filename component.
func Valid(value string) bool {
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-', r == '_':
		default:
			return false
		}
	}
	return value != ""
}

// FileName returns the project-local configuration file name for env.
func FileName(env string) string {
	return "config." + env + ".toml"
}

// IsFileName reports whether name is an environment-specific configuration
// file. The environment-neutral config.toml is never read from a project tree.
func IsFileName(name string) bool {
	return strings.HasPrefix(name, "config.") && strings.HasSuffix(name, ".toml") && name != NeutralFileName
}

// ReadPaths returns the project-local candidates for env in search order: the
// working directory first, then its config/ directory.
func ReadPaths(env string) []string {
	name := FileName(env)
	return []string{name, filepath.Join("config", name)}
}

func lookup(environ []string, key string) string {
	if environ == nil {
		return os.Getenv(key)
	}
	prefix := key + "="
	value := ""
	for _, entry := range environ {
		if strings.HasPrefix(entry, prefix) {
			value = strings.TrimPrefix(entry, prefix)
		}
	}
	return value
}

// DotenvBase is the environment-neutral dotenv file, read for every token and
// layered under the token's own file, per policy:dotenv-resolution. It is
// committed, so it carries shared values and never a secret.
const DotenvBase = ".env"

// DotenvLocalSuffix marks the machine-local member of each pair: .env.local
// beside .env, and .env.{env}.local beside .env.{env}. The local files are
// what .gitignore excludes, so they are where a secret goes.
const DotenvLocalSuffix = ".local"

// SecretDir is the directory a container runtime mounts file secrets into:
// Docker's secrets and a Kubernetes Secret volume both default to it. Each
// regular file there is one variable, read as a secret by origin, per
// policy:dotenv-resolution. It is skipped when absent, which is every
// workstation.
const SecretDir = "/run/secrets"

// DotenvTemplate is the committed dotenv template, read by nothing. It names
// the variables a deployment supplies, with no value.
const DotenvTemplate = ".env.example"

// DotenvFileName returns the committed dotenv file read for env.
func DotenvFileName(env string) string {
	return DotenvBase + "." + env
}

// DotenvFileNames returns every dotenv file a load reads for env, in read
// order: the shared file, its local override, the token's file, and its local
// override. A later file wins over an earlier one on the same name, which is
// the order Vite and Next read the same family in: the environment's own file
// outranks a machine-wide local one, and the environment's local file outranks
// everything.
func DotenvFileNames(env string) []string {
	return []string{
		DotenvBase, DotenvBase + DotenvLocalSuffix,
		DotenvFileName(env), DotenvFileName(env) + DotenvLocalSuffix,
	}
}

// IsDotenvFileName reports whether name is a dotenv file a load would read:
// the base file, a token-named one, or the local override of either. The
// template is excluded, because a change to it changes nothing about a running
// process.
func IsDotenvFileName(name string) bool {
	if name == DotenvTemplate {
		return false
	}
	name = strings.TrimSuffix(name, DotenvLocalSuffix)
	if name == DotenvBase {
		return true
	}
	if !strings.HasPrefix(name, DotenvBase+".") {
		return false
	}
	return Valid(strings.TrimPrefix(name, DotenvBase+"."))
}
