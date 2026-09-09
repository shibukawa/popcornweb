package pwcli

import (
	"context"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/shibukawa/popcornweb/internal/pwcheck"
	"github.com/shibukawa/popcornweb/internal/pwenv"
	"github.com/shibukawa/popcornweb/internal/pwtree"
)

const doctorUsage = "usage: pw doctor [--env=token|all]... [--config-path=path] [--format=text|json] [--strict] [--online]"

// doctorOptions are the answers pw doctor takes from its own command line. The
// environment is an option rather than an ambient variable because diagnosing
// production from a development host is the point of the command; it selects
// what to read and never reaches an application process.
type doctorOptions struct {
	Envs       []string
	ConfigPath string
	Format     string
	Strict     bool
	Online     bool
}

// doctorFinding is one check that fired.
type doctorFinding struct {
	Check    pwcheck.Check
	Severity pwcheck.Severity
	// Message names the resolved value in one sentence.
	Message string
	// Evidence is the key and place, the missing import, or the path. A secret
	// finding carries the key and the file, never the value.
	Evidence string
	Remedy   string
}

// doctorLimit records something this run could not determine, and which checks
// it therefore did not run. A report that looks clean because it did not look
// is the failure mode this exists to prevent.
type doctorLimit struct {
	Subject string
	Reason  string
	Effect  string
}

// doctorFeature is one line of the "what is on" view.
type doctorFeature struct {
	Name string
	// State is on, off, unavailable, or undetermined.
	State          string
	DecidedBy      string
	Implementation string
	Detail         string
}

// doctorEnvReport is everything the run resolved for one environment token.
type doctorEnvReport struct {
	Env           string
	ConfigPath    string
	ConfigFound   bool
	DotenvFiles   []string
	Entries       []pwtree.Entry
	Features      []doctorFeature
	Middleware    []string
	Connections   []doctorConnection
	Registrations []string
	Findings      []doctorFinding
	Limits        []doctorLimit
	LoadError     string
}

// doctorReport is the whole run.
type doctorReport struct {
	Root     string
	HostMode string
	Online   bool
	Summary  []string
	Project  doctorProject
	Envs     []doctorEnvReport
	Limits   []doctorLimit
}

// doctorConnection is one line of the database view, labeled the way its
// errors and logs will name it.
type doctorConnection struct {
	Label  string
	Driver string
	Role   string
}

// doctorProject is the host-only view of the project tree.
type doctorProject struct {
	Name         string
	Main         string
	Toolchain    string
	Capabilities []string
	Generated    int
	Migrations   int
	Devbox       bool
}

func runDoctor(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	options, err := parseDoctorOptions(args)
	if err != nil {
		return err
	}
	root, err := projectRoot(".")
	if err != nil {
		return err
	}
	report, err := diagnose(ctx, root, options, processEnviron())
	if err != nil {
		return err
	}
	if options.Format == "json" {
		if err := writeDoctorJSON(stdout, report); err != nil {
			return err
		}
	} else {
		writeDoctorText(stdout, report, doctorStyleFor(stdout))
	}
	if failed, reason := report.failing(options.Strict); failed {
		return &exitError{command: "pw doctor", message: reason}
	}
	return nil
}

// exitError reports a nonzero exit whose message the caller already rendered in
// the report body.
// exitError reports a command that already printed its own findings, so Main
// exits nonzero without adding a second error line. command names the prefix
// the message is attributed to.
type exitError struct {
	command string
	message string
}

func (e *exitError) Error() string { return e.message }

func parseDoctorOptions(args []string) (doctorOptions, error) {
	options := doctorOptions{Format: "text"}
	for _, arg := range args {
		name, value, hasValue := strings.Cut(arg, "=")
		switch {
		case name == "--env" && hasValue:
			options.Envs = append(options.Envs, value)
		case name == "--config-path" && hasValue:
			options.ConfigPath = value
		case name == "--format" && hasValue:
			if value != "text" && value != "json" {
				return options, fmt.Errorf("unknown format %q (want text or json)\n%s", value, doctorUsage)
			}
			options.Format = value
		case arg == "--strict":
			options.Strict = true
		case arg == "--online":
			options.Online = true
		case arg == "-h", arg == "--help":
			return options, fmt.Errorf("%s", doctorUsage)
		default:
			return options, fmt.Errorf("unknown doctor option %q\n%s", arg, doctorUsage)
		}
	}
	return options, nil
}

// resolveTokens expands the --env answers. Without one, the token is the
// APP_ENV of this process, then dev, so the command answers about the
// environment its shell is already in.
func resolveTokens(root string, options doctorOptions, environ []string) ([]string, error) {
	if len(options.Envs) == 0 {
		env, err := pwenv.Resolve(environ)
		if err != nil {
			return nil, err
		}
		return []string{env}, nil
	}
	var tokens []string
	seen := map[string]bool{}
	for _, requested := range options.Envs {
		expanded := []string{requested}
		if requested == "all" {
			discovered, err := environmentTokens(root)
			if err != nil {
				return nil, err
			}
			if len(discovered) == 0 {
				return nil, fmt.Errorf("no %s file found to diagnose", pwenv.FileName("<env>"))
			}
			expanded = discovered
		}
		for _, token := range expanded {
			if !pwenv.Valid(token) {
				return nil, fmt.Errorf("invalid environment %q: use lowercase letters, digits, '-' or '_'", token)
			}
			if !seen[token] {
				seen[token] = true
				tokens = append(tokens, token)
			}
		}
	}
	return tokens, nil
}

// diagnose reads the project and every requested environment. Nothing here
// builds the application, starts a process, or writes a file; a connection is
// opened only when --online asked for one.
func diagnose(ctx context.Context, root string, options doctorOptions, environ []string) (doctorReport, error) {
	tokens, err := resolveTokens(root, options, environ)
	if err != nil {
		return doctorReport{}, err
	}
	state, err := loadProjectState(root)
	if err != nil {
		return doctorReport{}, err
	}
	report := doctorReport{Root: root, HostMode: hostMode(environ), Online: options.Online}
	report.Project = doctorProject{
		Name:       state.config.Name,
		Main:       state.config.Main,
		Toolchain:  state.config.Toolchain,
		Migrations: len(state.migrations),
		Devbox:     state.devbox != "",
	}
	if carried, err := state.carriedCapabilities(); err == nil {
		report.Project.Capabilities = carried
	}

	graph := resolveImportGraph(ctx, root, state.config.Main)
	if graph.Err != nil {
		report.Limits = append(report.Limits, doctorLimit{
			Subject: "import graph",
			Reason:  "go list could not resolve the main package: " + graph.Err.Error(),
			Effect:  "the wiring checks did not run",
		})
	}
	if !options.Online {
		report.Limits = append(report.Limits, doctorLimit{
			Subject: "database",
			Reason:  "--online was not given, so nothing was contacted",
			Effect:  "applied migration state and connection reachability were not read",
		})
	}
	if report.HostMode == "workstation" {
		report.Limits = append(report.Limits, doctorLimit{
			Subject: "environment variables",
			Reason:  "this host does not hold a deployment's environment",
			Effect:  "a key whose deployed value arrives from the environment is reported as unknown at this host",
		})
	}

	files, err := state.readConfigFiles()
	if err != nil {
		return doctorReport{}, err
	}
	scan := newProjectScan(root, state, files)
	secretsByEnv := map[string]map[string]string{}

	for _, token := range tokens {
		environment := doctorEnvReport{Env: token}
		loaded, loadErr := loadEnvironmentConfig(root, token, options.ConfigPath, environ)
		if loadErr != nil {
			// A load that cannot complete is one finding; the sections it would
			// have produced are omitted with their reason rather than guessed.
			environment.LoadError = loadErr.Error()
			environment.Limits = append(environment.Limits, doctorLimit{
				Subject: "configuration",
				Reason:  loadErr.Error(),
				Effect:  "every configuration check for this environment did not run",
			})
			report.Envs = append(report.Envs, environment)
			continue
		}
		environment.ConfigPath, environment.ConfigFound = loaded.ConfigPath, loaded.ConfigFound
		environment.DotenvFiles = loaded.DotenvFiles
		environment.Entries = loaded.Entries
		environment.Features = resolveFeatures(loaded, graph, state)
		environment.Middleware = resolveMiddleware(loaded)
		environment.Connections = resolveConnections(loaded, graph)
		environment.Registrations = resolveRegistrations(graph)
		findings, limits := runChecks(ctx, checkContext{
			Env:     token,
			Root:    root,
			Config:  loaded,
			Graph:   graph,
			State:   state,
			Scan:    scan,
			Online:  options.Online,
			HostEnv: report.HostMode,
		})
		environment.Findings = findings
		environment.Limits = append(environment.Limits, limits...)
		secretsByEnv[token] = secretValues(loaded)
		report.Envs = append(report.Envs, environment)
	}
	appendSharedSecretFindings(&report, secretsByEnv)
	report.Summary = summaryLines(report)
	return report, nil
}

// secretValues returns the raw secret-classified values that came from a file,
// keyed by configuration key. They are compared between environments and never
// rendered.
func secretValues(loaded environmentConfig) map[string]string {
	values := map[string]string{}
	for _, key := range loaded.secretKeys() {
		if loaded.fromFile(key) {
			values[key] = loaded.raw(key)
		}
	}
	return values
}

// appendSharedSecretFindings reports a literal secret that appears in more than
// one environment's file. Only a reader of every environment file sees this,
// which is why it is a doctor check and not a startup one.
func appendSharedSecretFindings(report *doctorReport, byEnv map[string]map[string]string) {
	if len(byEnv) < 2 {
		return
	}
	check := pwcheck.MustLookup(pwcheck.SecretSharedBetween)
	type pair struct{ env, key string }
	shared := map[string][]pair{}
	for env, values := range byEnv {
		for key, value := range values {
			shared[value] = append(shared[value], pair{env: env, key: key})
		}
	}
	for _, holders := range shared {
		envs := map[string]bool{}
		for _, holder := range holders {
			envs[holder.env] = true
		}
		if len(envs) < 2 {
			continue
		}
		sort.Slice(holders, func(i, j int) bool { return holders[i].env < holders[j].env })
		var parts []string
		deployed := false
		for _, holder := range holders {
			parts = append(parts, holder.env+" "+holder.key)
			if holder.env != pwenv.Development {
				deployed = true
			}
		}
		severity := pwcheck.Warning
		if deployed {
			severity = pwcheck.Error
		}
		finding := doctorFinding{
			Check:    check,
			Severity: severity,
			Message:  "one secret value is set in more than one environment file",
			Evidence: strings.Join(parts, ", "),
			Remedy:   check.Remedy,
		}
		for index := range report.Envs {
			if envs[report.Envs[index].Env] {
				report.Envs[index].Findings = append(report.Envs[index].Findings, finding)
				sortFindings(report.Envs[index].Findings)
			}
		}
	}
}

// failing reports whether the run should exit nonzero.
func (r doctorReport) failing(strict bool) (bool, string) {
	errors, warnings := 0, 0
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
			}
		}
	}
	switch {
	case errors > 0:
		return true, fmt.Sprintf("%d error finding(s)", errors)
	case strict && warnings > 0:
		return true, fmt.Sprintf("%d warning finding(s) under --strict", warnings)
	}
	return false, ""
}

func sortFindings(findings []doctorFinding) {
	sort.SliceStable(findings, func(i, j int) bool {
		if findings[i].Severity != findings[j].Severity {
			return findings[i].Severity > findings[j].Severity
		}
		return findings[i].Check.ID < findings[j].Check.ID
	})
}

// summaryLines is the block an operator pastes into an issue: the environments,
// the host mode, and what is on.
func summaryLines(report doctorReport) []string {
	lines := []string{
		"project    " + report.Project.Name + " (" + report.Project.Toolchain + ")",
	}
	if len(report.Project.Capabilities) > 0 {
		lines = append(lines, "capability "+strings.Join(report.Project.Capabilities, ", "))
	}
	var tokens []string
	for _, environment := range report.Envs {
		tokens = append(tokens, environment.Env)
	}
	lines = append(lines, "diagnosed  "+strings.Join(tokens, ", ")+"  on a "+report.HostMode+" host")
	return lines
}

func doctorStyleFor(out io.Writer) doctorStyle {
	file, ok := out.(*os.File)
	if !ok {
		return doctorStyle{}
	}
	return doctorStyle{color: isTerminalFile(file) && os.Getenv("NO_COLOR") == "" && os.Getenv("TERM") != "dumb"}
}
