package pwcli

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/shibukawa/popcornweb/internal/configview"
	"github.com/shibukawa/popcornweb/internal/dotenv"
	"github.com/shibukawa/popcornweb/internal/pwenv"
	"github.com/shibukawa/popcornweb/internal/pwtree"
	"github.com/shibukawa/tinybind-go/configbind"

	// The framework's configuration definitions register during this import, so
	// the host reads the same keys, defaults, and secret classification the
	// application would. Only the metadata is used: whether a binding is
	// actually linked into the project is answered by its import graph, because
	// the CLI links plugins a given project may not.
	_ "github.com/shibukawa/popcornweb/pw"
)

// configValue is one key as the host resolved it for a diagnosed environment.
type configValue struct {
	Key string
	// Raw is the value before masking. It is compared but never rendered, so a
	// secret can be checked without being printed.
	Raw string
	// Shown is the value as the report may display it.
	Shown  string
	Place  configbind.Place
	Secret bool
}

// environmentConfig is the merged configuration for one environment token.
type environmentConfig struct {
	Env         string
	ConfigPath  string
	ConfigFound bool
	// DotenvFiles are the policy:dotenv-resolution files of the token, in the
	// order they were layered under this host's environment.
	DotenvFiles []string
	Values      map[string]configValue
	// Entries keeps provenance order, which the tree renders as-is.
	Entries []pwtree.Entry
	// Sections lists the top-level prefixes the merged configuration carries,
	// which is the unit a binding is registered for.
	Sections []string
}

// loadEnvironmentConfig merges typed defaults, the project's dotenv files for
// the token, this host's environment, and the TOML file the token selects, the
// way api:runtime-configuration would for the layers a host can see. CLI
// arguments are never a layer here: doctor's own arguments are not the
// application's.
func loadEnvironmentConfig(root, env, explicitPath string, environ []string) (environmentConfig, error) {
	if environ == nil {
		environ = processEnviron()
	}
	layer, err := dotenv.ReadLayer(root, env, []string{pwenv.SecretDir})
	if err != nil {
		return environmentConfig{}, err
	}
	extras := make([]string, 0, 2)
	for _, candidate := range pwenv.ReadPaths(env) {
		extras = append(extras, filepath.Join(root, filepath.FromSlash(candidate)))
	}
	if explicitPath != "" && !filepath.IsAbs(explicitPath) {
		explicitPath = filepath.Join(root, filepath.FromSlash(explicitPath))
	}
	result, err := dotenv.Load(configbind.LoadOptions{
		Vendor:               "popcornweb",
		Tool:                 "pw-doctor",
		FileName:             pwenv.NeutralFileName,
		Args:                 []string{},
		ExplicitConfigPath:   explicitPath,
		ExtraConfigReadPaths: extras,
	}, layer, environ)
	if err != nil {
		return environmentConfig{}, err
	}
	loaded := environmentConfig{
		Env:         env,
		ConfigFound: result.FoundFile,
		DotenvFiles: layer.Names(),
		Values:      map[string]configValue{},
	}
	if result.FoundFile {
		loaded.ConfigPath = relativeToRoot(root, result.ConfigPath)
	}
	for _, entry := range result.Provenance() {
		value := configValue{Key: entry.Key, Shown: entry.Value, Place: entry.Place, Secret: entry.Masked}
		value.Raw = entry.Value
		if raw, ok := configview.Raw(result.Overlay, entry); ok {
			value.Raw = raw
			// A DSN masked whole answers none of what this report is opened
			// for. The framework renders its public half in the startup
			// summary, and the two summaries a reader compares must agree.
			if entry.Masked && configview.IsDSNKey(entry.Key) {
				value.Shown = configview.DSN(raw)
			}
		}
		loaded.Values[entry.Key] = value
		loaded.Entries = append(loaded.Entries, pwtree.Entry{Key: entry.Key, Value: value.Shown, Source: string(entry.Place)})
	}
	// Sections are grouped by their top-level prefix. A key that provenance
	// does not report is hidden by a disabled parent rather than unknown, so
	// ownership is asked of the section, which is the unit a plugin registers.
	// Only a section something configured counts: a binding contributes its
	// defaults to every project, and defaults are not a statement of intent.
	for _, key := range result.Overlay.Keys() {
		prefix, _, ok := strings.Cut(key, ".")
		if !ok {
			continue
		}
		if entry, found := result.Overlay.Get(key); !found || entry.Place == configbind.PlaceDefault {
			continue
		}
		if !containsString(loaded.Sections, prefix) {
			loaded.Sections = append(loaded.Sections, prefix)
		}
	}
	sortStrings(loaded.Sections)
	return loaded, nil
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

// value returns the resolved value for key.
func (c environmentConfig) value(key string) (configValue, bool) {
	value, ok := c.Values[key]
	return value, ok
}

// raw returns the unmasked value for key, or the empty string.
func (c environmentConfig) raw(key string) string {
	return c.Values[key].Raw
}

// boolValue reports a boolean key and whether it was resolved at all.
func (c environmentConfig) boolValue(key string) (bool, bool) {
	value, ok := c.Values[key]
	if !ok {
		return false, false
	}
	parsed, err := strconv.ParseBool(strings.TrimSpace(value.Raw))
	if err != nil {
		return false, false
	}
	return parsed, true
}

// enabled reports a boolean key, defaulting to false when it is absent because
// its parent is off. A check that examines a feature is skipped, not passed,
// when the feature is not on.
func (c environmentConfig) enabled(key string) bool {
	value, _ := c.boolValue(key)
	return value
}

// place reports the layer that won a key.
func (c environmentConfig) place(key string) configbind.Place {
	return c.Values[key].Place
}

// fromFile reports whether the file layer won the key.
func (c environmentConfig) fromFile(key string) bool {
	return c.place(key) == configbind.PlaceFile
}

// fromDotenv names the dotenv file that won the key, when one did.
func (c environmentConfig) fromDotenv(key string) (string, bool) {
	return configbind.EnvFileOf(c.place(key))
}

// secretKeys lists the secret-classified keys that resolved to a value, in key
// order, so a report is deterministic.
func (c environmentConfig) secretKeys() []string {
	var keys []string
	for key, value := range c.Values {
		if value.Secret && strings.TrimSpace(value.Raw) != "" {
			keys = append(keys, key)
		}
	}
	sortStrings(keys)
	return keys
}

// databaseDSNs lists every configured DSN with the label its errors will use.
func (c environmentConfig) databaseDSNs() []labeledDSN {
	var found []labeledDSN
	groups := map[string]int{}
	for index := 0; ; index++ {
		key := fmt.Sprintf("middleware.rdb.connections[%d].dsn", index)
		value, ok := c.Values[key]
		if !ok {
			break
		}
		group := c.raw(fmt.Sprintf("middleware.rdb.connections[%d].group", index))
		if group == "" {
			group = c.raw("middleware.rdb.default_group")
		}
		if group == "" {
			group = "default"
		}
		groups[group]++
		found = append(found, labeledDSN{Key: key, Label: fmt.Sprintf("%s#%d", group, groups[group]), DSN: value.Raw})
	}
	return found
}

type labeledDSN struct {
	Key   string
	Label string
	DSN   string
}

// scheme returns the driver scheme of the DSN.
func (d labeledDSN) scheme() string {
	scheme, _, ok := strings.Cut(strings.TrimSpace(d.DSN), "://")
	if !ok {
		return ""
	}
	return scheme
}

func relativeToRoot(root, path string) string {
	if relative, err := filepath.Rel(root, path); err == nil && !strings.HasPrefix(relative, "..") {
		return filepath.ToSlash(relative)
	}
	return path
}

// environmentTokens lists the tokens the project has a configuration file for.
func environmentTokens(root string) ([]string, error) {
	files, err := environmentConfigFiles(root)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var tokens []string
	for _, file := range files {
		token := environmentToken(file)
		if token == "" || seen[token] {
			continue
		}
		seen[token] = true
		tokens = append(tokens, token)
	}
	sortStrings(tokens)
	return tokens, nil
}

// environmentToken reads the environment out of a configuration file path, so
// what is written into a file is decided by the environment it configures.
func environmentToken(file string) string {
	name := filepath.Base(filepath.FromSlash(file))
	return strings.TrimSuffix(strings.TrimPrefix(name, "config."), ".toml")
}

// hostMode records whether this host is the one holding the deployment's
// environment. The same clean report means different things in each, so the
// report says which it was.
func hostMode(environ []string) string {
	for _, entry := range environ {
		if strings.HasPrefix(entry, "CI=") && !strings.HasSuffix(entry, "=false") {
			return "deployment"
		}
	}
	return "workstation"
}

func processEnviron() []string { return os.Environ() }
