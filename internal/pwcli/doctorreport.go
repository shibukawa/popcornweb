package pwcli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/charmbracelet/x/term"
	"github.com/shibukawa/popcornweb/internal/pwcheck"
	"github.com/shibukawa/popcornweb/internal/pwtree"
	"github.com/shibukawa/tinybind-go/configbind"
)

// resolveMiddleware lists the framework middleware this configuration would
// apply, in the order data:middleware-runtime-config records. It answers "what
// will run" without constructing anything.
func resolveMiddleware(config environmentConfig) []string {
	ordered := []struct {
		name    string
		enabled bool
	}{
		{"recovery", config.enabled("middleware.recovery")},
		{"trusted proxy", config.raw("server.trusted_proxies") != ""},
		{"request id", config.enabled("middleware.request_id")},
		{"access log", config.enabled("middleware.access_log")},
		{"request timeout", config.raw("middleware.request_timeout") != "" && config.raw("middleware.request_timeout") != "0s"},
		{"database pool", config.enabled("middleware.rdb.enabled")},
		{"session", config.enabled("session.enabled")},
		{"authentication", config.enabled("auth.enabled")},
		{"security headers", config.enabled("security.headers.enabled")},
		{"compression", config.enabled("middleware.compression")},
	}
	var chain []string
	for _, entry := range ordered {
		if entry.enabled {
			chain = append(chain, entry.name)
		}
	}
	return append(chain, "application handler")
}

// resolveConnections renders the connection set the way its errors and logs
// will name it, so a finding and a log line point at the same connection.
func resolveConnections(config environmentConfig, graph importGraph) []doctorConnection {
	var connections []doctorConnection
	defaultGroup := config.raw("middleware.rdb.default_group")
	writeGroup := config.raw("middleware.rdb.write_group")
	migrationGroup := config.raw("middleware.rdb.migration_group")
	configured := config.databaseDSNs()
	// One connection answers every group name, so a configuration that names
	// no group still has one connection carrying all three roles. Reading the
	// pointers alone would print a blank role for the commonest project there
	// is: the one with a single database.
	sole := len(configured) == 1
	for _, connection := range configured {
		scheme := connection.scheme()
		driver := scheme
		if pkg, known := driverPackages[scheme]; known && graph.available() && !graph.links(pkg) {
			driver = scheme + " (no driver linked)"
		} else if !known {
			driver = scheme + " (unknown scheme)"
		}
		group, _, _ := strings.Cut(connection.Label, "#")
		var roles []string
		if sole {
			roles = []string{"default", "migration", "write"}
		}
		for name, role := range map[string]string{defaultGroup: "default", writeGroup: "write", migrationGroup: "migration"} {
			if name != "" && name == group && !containsString(roles, role) {
				roles = append(roles, role)
			}
		}
		sortStrings(roles)
		connections = append(connections, doctorConnection{
			Label: connection.Label, Driver: driver, Role: strings.Join(roles, ", "),
		})
	}
	return connections
}

// resolveRegistrations lists what the application actually links, which is the
// half of the answer no configuration file carries.
func resolveRegistrations(graph importGraph) []string {
	if !graph.available() {
		return nil
	}
	known := []struct {
		path string
		kind string
	}{
		{authPluginPackage, "auth plugin"},
		{rdbSessionPackage, "rdb session backend"},
		{sqliteDriverPackage, "sqlite driver"},
		{postgresDriverPackage, "postgres driver"},
		{mysqlDriverPackage, "mysql driver"},
		{d1DriverPackage, "d1 driver"},
	}
	var linked []string
	for _, entry := range known {
		if graph.links(entry.path) {
			linked = append(linked, entry.kind+"  "+entry.path)
		}
	}
	return linked
}

// resolveFeatures is the "what is on" view: one line per framework feature,
// state first, with the key that decided it and the implementation behind it.
// A feature that is off collapses to one line, so the report length tracks what
// is enabled.
func resolveFeatures(config environmentConfig, graph importGraph, state projectState) []doctorFeature {
	features := []doctorFeature{
		booleanFeature(config, "database", "middleware.rdb.enabled", ""),
		booleanFeature(config, "session", "session.enabled", "session.backend"),
		booleanFeature(config, "authentication", "auth.enabled", "auth.mode"),
		booleanFeature(config, "security headers", "security.headers.enabled", ""),
		booleanFeature(config, "compression", "middleware.compression", ""),
		booleanFeature(config, "access log", "middleware.access_log", ""),
		booleanFeature(config, "telemetry export", "observability.otel.enabled", "observability.otel.endpoint"),
		booleanFeature(config, "public assets", "server.public.enabled", "server.public.mount"),
		booleanFeature(config, "api documentation", "server.api_doc", "server.api_doc_path"),
	}
	query := doctorFeature{Name: "query diagnostics", State: "off", DecidedBy: "observability.query.enabled"}
	switch value := config.raw("observability.query.enabled"); value {
	case "on":
		query.State = "on"
	case "auto":
		query.Detail = "auto"
		if config.Env == "dev" {
			query.State = "on"
		}
	}
	features = append(features, query)
	tailwind := doctorFeature{Name: "tailwind", State: "off", DecidedBy: "popcornweb.toml assets.tailwind.enabled"}
	if state.config.Tailwind.Enabled {
		tailwind.State, tailwind.Detail = "on", state.config.Tailwind.Output
	}
	features = append(features, tailwind)

	if graph.available() {
		for index := range features {
			features[index].Implementation = featureImplementation(features[index], graph, config, state.config.Database)
		}
	}
	return features
}

func booleanFeature(config environmentConfig, name, key, detailKey string) doctorFeature {
	feature := doctorFeature{Name: name, State: "off", DecidedBy: key}
	value, resolved := config.boolValue(key)
	switch {
	case !resolved:
		// The deciding key was not reported for this environment, which happens
		// when its binding is not linked or its parent is off.
		feature.State = "off"
	case value:
		feature.State = "on"
	}
	if feature.State == "on" && detailKey != "" {
		feature.Detail = config.Values[detailKey].Shown
	}
	return feature
}

// featureImplementation names what stands behind an enabled feature, or reports
// that configuration selects something the binary does not link.
func featureImplementation(feature doctorFeature, graph importGraph, config environmentConfig, engine string) string {
	if feature.State != "on" {
		return ""
	}
	switch feature.Name {
	case "session":
		backend := config.raw("session.backend")
		// The development and cookie backends are pw itself, so naming no plugin
		// is the correct answer rather than a missing one. Reported as an absence
		// it read like a fault on the default a scaffolded project starts with.
		if backend == "cookie" || backend == "dev-volatile" || backend == "dev-persist" {
			return "built into pw"
		}
		if pkg := sessionBackendPackage(backend, engine); pkg != "" {
			if graph.links(pkg) {
				return pkg
			}
			return "not linked: " + pkg
		}
		return "no plugin registers " + backend
	case "authentication":
		if graph.links(authPluginPackage) {
			return authPluginPackage
		}
		return "not linked: " + authPluginPackage
	case "database":
		var drivers []string
		for _, connection := range config.databaseDSNs() {
			scheme := connection.scheme()
			if pkg, ok := driverPackages[scheme]; ok && graph.links(pkg) {
				drivers = append(drivers, scheme)
				continue
			}
			drivers = append(drivers, scheme+" (no driver linked)")
		}
		return strings.Join(drivers, ", ")
	}
	return ""
}

type doctorStyle struct{ color bool }

func (s doctorStyle) wrap(code, text string) string {
	if !s.color || text == "" {
		return text
	}
	return "\x1b[" + code + "m" + text + "\x1b[0m"
}

func (s doctorStyle) bold(text string) string  { return s.wrap("1", text) }
func (s doctorStyle) dim(text string) string   { return s.wrap("2", text) }
func (s doctorStyle) red(text string) string   { return s.wrap("31", text) }
func (s doctorStyle) amber(text string) string { return s.wrap("33", text) }

func (s doctorStyle) severity(level pwcheck.Severity) string {
	switch level {
	case pwcheck.Error:
		return s.red("error")
	case pwcheck.Warning:
		return s.amber("warning")
	default:
		return s.dim("note")
	}
}

func isTerminalFile(file *os.File) bool { return term.IsTerminal(file.Fd()) }

// writeDoctorText renders the report the way the startup summary renders
// configuration, so a reader who has seen one has already learned the other.
// State comes before findings, because a finding is only readable next to the
// value that produced it.
func writeDoctorText(out io.Writer, report doctorReport, style doctorStyle) {
	var body strings.Builder
	for _, line := range report.Summary {
		body.WriteString(style.dim(line) + "\n")
	}
	body.WriteByte('\n')

	for _, environment := range report.Envs {
		body.WriteString(style.bold("environment "+environment.Env) + "  " + style.dim(configCaption(environment)) + "\n")
		if environment.LoadError != "" {
			body.WriteString("  " + style.red("configuration could not be loaded: ") + environment.LoadError + "\n\n")
			continue
		}
		body.WriteString("\n" + style.dim("features") + "\n")
		for _, feature := range environment.Features {
			body.WriteString(featureLine(feature, style))
		}
		if chain := environment.Middleware; len(chain) > 0 {
			body.WriteString("\n" + style.dim("middleware, in order") + "\n")
			for index, name := range chain {
				body.WriteString(fmt.Sprintf("  %d. %s\n", index+1, name))
			}
		}
		if connections := environment.Connections; len(connections) > 0 {
			body.WriteString("\n" + style.dim("database") + "\n")
			for _, connection := range connections {
				body.WriteString("  " + connection.Label + "  " + connection.Driver)
				if connection.Role != "" {
					body.WriteString("  " + style.dim(connection.Role))
				}
				body.WriteByte('\n')
			}
		}
		if registrations := environment.Registrations; len(registrations) > 0 {
			body.WriteString("\n" + style.dim("linked registrations") + "\n")
			for _, registration := range registrations {
				body.WriteString("  " + registration + "\n")
			}
		}
		// With more than one environment diagnosed, the question a reader is
		// asking is which value changes when the environment does, so the
		// differing keys are shown once, as columns, after the environments.
		if len(report.Envs) == 1 {
			if lines := pwtree.Lines(environment.Entries); len(lines) > 0 {
				body.WriteString("\n" + style.dim("configuration") + "\n")
				pwtree.Render(&body, lines, doctorSourceTag, style.dim)
			}
		}
		body.WriteString("\n" + style.dim("findings") + "\n")
		if len(environment.Findings) == 0 {
			body.WriteString("  none\n")
		}
		for _, finding := range environment.Findings {
			body.WriteString(findingLines(finding, style))
		}
		body.WriteByte('\n')
	}

	writeConfigurationDifferences(&body, report, style)

	limits := report.allLimits()
	if len(limits) > 0 {
		body.WriteString(style.dim("not examined") + "\n")
		for _, limit := range limits {
			body.WriteString("  " + limit.Subject + ": " + limit.Reason + "\n")
			body.WriteString("    " + style.dim(limit.Effect) + "\n")
		}
		body.WriteByte('\n')
	}
	body.WriteString(countsLine(report, style) + "\n")
	_, _ = io.WriteString(out, body.String())
}

// writeConfigurationDifferences renders one column per diagnosed environment,
// showing only the keys whose value differs between them. A key that is the
// same everywhere answers no question a multi-environment run was asking.
func writeConfigurationDifferences(out *strings.Builder, report doctorReport, style doctorStyle) {
	if len(report.Envs) < 2 {
		return
	}
	values := make([]map[string]string, len(report.Envs))
	var order []string
	seen := map[string]bool{}
	for index, environment := range report.Envs {
		values[index] = map[string]string{}
		for _, entry := range environment.Entries {
			values[index][entry.Key] = pwtree.DisplayValue(entry.Value)
			if !seen[entry.Key] {
				seen[entry.Key] = true
				order = append(order, entry.Key)
			}
		}
	}
	const absent = "-"
	keyWidth := 0
	var differing []string
	for _, key := range order {
		first, same := "", true
		for index := range report.Envs {
			value, present := values[index][key]
			if !present {
				value = absent
			}
			if index == 0 {
				first = value
				continue
			}
			if value != first {
				same = false
			}
		}
		if same {
			continue
		}
		differing = append(differing, key)
		keyWidth = max(keyWidth, len(key))
	}
	if len(differing) == 0 {
		out.WriteString(style.dim("configuration") + "\n  every diagnosed environment resolves the same values\n\n")
		return
	}
	out.WriteString(style.dim("configuration, where the environments differ") + "\n")
	columns := make([]int, len(report.Envs))
	for index, environment := range report.Envs {
		columns[index] = len(environment.Env)
		for _, key := range differing {
			value, present := values[index][key]
			if !present {
				value = absent
			}
			columns[index] = max(columns[index], len(value))
		}
	}
	header := fmt.Sprintf("  %-*s", keyWidth, "")
	for index, environment := range report.Envs {
		header += fmt.Sprintf("  %-*s", columns[index], environment.Env)
	}
	out.WriteString(style.dim(header) + "\n")
	for _, key := range differing {
		line := fmt.Sprintf("  %-*s", keyWidth, key)
		for index := range report.Envs {
			value, present := values[index][key]
			if !present {
				value = absent
			}
			line += fmt.Sprintf("  %-*s", columns[index], value)
		}
		out.WriteString(strings.TrimRight(line, " ") + "\n")
	}
	out.WriteByte('\n')
}

func configCaption(environment doctorEnvReport) string {
	caption := "no config file (defaults and environment only)"
	if environment.ConfigFound && environment.ConfigPath != "" {
		caption = environment.ConfigPath
	}
	if len(environment.DotenvFiles) > 0 {
		caption += " · " + strings.Join(environment.DotenvFiles, ", ")
	}
	return caption
}

func featureLine(feature doctorFeature, style doctorStyle) string {
	state := feature.State
	if state == "on" {
		state = style.bold(state)
	} else {
		state = style.dim(state)
	}
	line := fmt.Sprintf("  %-20s %s", feature.Name, state)
	var extras []string
	if feature.Detail != "" {
		extras = append(extras, feature.Detail)
	}
	if feature.Implementation != "" {
		extras = append(extras, feature.Implementation)
	}
	if len(extras) > 0 {
		line += "  " + style.dim(strings.Join(extras, "  "))
	}
	return line + "\n"
}

func findingLines(finding doctorFinding, style doctorStyle) string {
	var out strings.Builder
	out.WriteString("  " + style.severity(finding.Severity) + " " + style.bold(finding.Check.ID) + ": " + finding.Message + "\n")
	if finding.Evidence != "" {
		out.WriteString("        " + style.dim(finding.Evidence) + "\n")
	}
	if finding.Remedy != "" {
		out.WriteString("        " + style.dim("fix: "+finding.Remedy) + "\n")
	}
	out.WriteString("        " + style.dim(finding.Check.DocsURL()) + "\n")
	return out.String()
}

func countsLine(report doctorReport, style doctorStyle) string {
	errors, warnings, notes := report.counts()
	line := fmt.Sprintf("%d error, %d warning, %d note", errors, warnings, notes)
	switch {
	case errors > 0:
		return style.red(line)
	case warnings > 0:
		return style.amber(line)
	}
	return style.dim(line)
}

func (r doctorReport) counts() (errors, warnings, notes int) {
	for _, environment := range r.Envs {
		if environment.LoadError != "" {
			errors++
		}
		for _, finding := range environment.Findings {
			switch finding.Severity {
			case pwcheck.Error:
				errors++
			case pwcheck.Warning:
				warnings++
			default:
				notes++
			}
		}
	}
	return errors, warnings, notes
}

func (r doctorReport) allLimits() []doctorLimit {
	limits := append([]doctorLimit(nil), r.Limits...)
	for _, environment := range r.Envs {
		limits = append(limits, environment.Limits...)
	}
	return limits
}

// doctorSourceTag names the layer that won a key, matching the startup summary:
// defaults stay unmarked so the values someone chose are the ones that catch
// the eye. The environment layer says whose environment it was, because a value
// read on a workstation is not the deployment's.
func doctorSourceTag(source string) string {
	switch configbind.Place(source) {
	case configbind.PlaceDefault, "":
		return ""
	case configbind.PlaceFile:
		return "file"
	case configbind.PlaceEnv:
		return "env (this host)"
	case configbind.PlaceCLI:
		return "flag"
	default:
		if file, ok := configbind.EnvFileOf(configbind.Place(source)); ok {
			return file
		}
		return source
	}
}

// doctorJSON is the machine-readable shape. Section and field names are a
// supported interface, because a release gate asserts on them.
type doctorJSON struct {
	Project      doctorProject           `json:"project"`
	HostMode     string                  `json:"host_mode"`
	Online       bool                    `json:"online"`
	Environments []doctorEnvironmentJSON `json:"environments"`
	Limits       []doctorLimit           `json:"limits"`
	Counts       map[string]int          `json:"counts"`
}

type doctorEnvironmentJSON struct {
	Env           string              `json:"env"`
	ConfigPath    string              `json:"config_path"`
	ConfigFound   bool                `json:"config_found"`
	DotenvFiles   []string            `json:"dotenv_files"`
	LoadError     string              `json:"load_error,omitempty"`
	Features      []doctorFeature     `json:"features"`
	Middleware    []string            `json:"middleware"`
	Connections   []doctorConnection  `json:"connections"`
	Registrations []string            `json:"registrations"`
	Config        []doctorConfigJSON  `json:"config"`
	Findings      []doctorFindingJSON `json:"findings"`
}

type doctorConfigJSON struct {
	Key    string `json:"key"`
	Value  string `json:"value"`
	Source string `json:"source"`
}

type doctorFindingJSON struct {
	ID       string `json:"id"`
	Severity string `json:"severity"`
	Title    string `json:"title"`
	Message  string `json:"message"`
	Evidence string `json:"evidence,omitempty"`
	Remedy   string `json:"remedy,omitempty"`
	Docs     string `json:"docs"`
}

func writeDoctorJSON(out io.Writer, report doctorReport) error {
	errors, warnings, notes := report.counts()
	document := doctorJSON{
		Project:  report.Project,
		HostMode: report.HostMode,
		Online:   report.Online,
		Limits:   report.allLimits(),
		Counts:   map[string]int{"error": errors, "warning": warnings, "note": notes},
	}
	for _, environment := range report.Envs {
		converted := doctorEnvironmentJSON{
			Env:           environment.Env,
			ConfigPath:    environment.ConfigPath,
			ConfigFound:   environment.ConfigFound,
			DotenvFiles:   append([]string{}, environment.DotenvFiles...),
			LoadError:     environment.LoadError,
			Features:      environment.Features,
			Middleware:    environment.Middleware,
			Connections:   environment.Connections,
			Registrations: environment.Registrations,
		}
		for _, entry := range environment.Entries {
			converted.Config = append(converted.Config, doctorConfigJSON{Key: entry.Key, Value: entry.Value, Source: entry.Source})
		}
		for _, finding := range environment.Findings {
			converted.Findings = append(converted.Findings, doctorFindingJSON{
				ID:       finding.Check.ID,
				Severity: finding.Severity.String(),
				Title:    finding.Check.Title,
				Message:  finding.Message,
				Evidence: finding.Evidence,
				Remedy:   finding.Remedy,
				Docs:     finding.Check.DocsURL(),
			})
		}
		document.Environments = append(document.Environments, converted)
	}
	encoder := json.NewEncoder(out)
	encoder.SetIndent("", "  ")
	return encoder.Encode(document)
}
