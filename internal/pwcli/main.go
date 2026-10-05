package pwcli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
)

func Main(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		printUsage(stderr)
		return 2
	}
	// Every command answers --help, from the same table pw help prints. Left
	// to each parser it was an "unknown option" in most of them.
	if asksForHelp(args[0], args[1:]) && printCommandUsage(stdout, args[0]) {
		return 0
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	var err error
	switch args[0] {
	case "init":
		err = runInit(args[1:], stdout)
	case "add":
		err = runAdd(ctx, args[1:], stdout)
	case "new":
		err = runNew(ctx, args[1:], stdout)
	case "generate":
		err = runGenerate(ctx, args[1:], stdout, stderr)
	case "check":
		err = runCheck(ctx, args[1:], stdout)
	case "fmt":
		err = runFmt(ctx, args[1:], stdout, stderr)
	case "i18n":
		err = runI18n(args[1:], stdout)
	case "migrate":
		err = runMigrate(ctx, args[1:], stdout, stderr)
	case "seed":
		err = runSeed(ctx, args[1:], stdout, stderr)
	case "build":
		err = runBuild(ctx, args[1:], stdout, stderr)
	case "dev":
		err = runDev(ctx, args[1:], stdout, stderr)
	case "doctor":
		err = runDoctor(ctx, args[1:], stdout, stderr)
	case "request":
		err = runRequest(ctx, args[1:], stdout, stderr)
	case "rename":
		err = runRename(args[1:], stdout)
	case "lsp":
		err = runLSP(args[1:], os.Stdin, stdout)
	case "version", "--version", "-v":
		err = runVersion(args[1:], stdout)
	case "help", "-h", "--help":
		printUsage(stdout)
		return 0
	default:
		err = fmt.Errorf("unknown command %q", args[0])
	}
	if err != nil {
		// A command that already rendered its own report exits on the finding
		// rather than on a second error line.
		var findings *exitError
		if errors.As(err, &findings) {
			fmt.Fprintln(stderr, findings.command+":", findings.message)
			return 1
		}
		// A command that shares its exit codes with a tool its callers
		// already script against says which one.
		var coded *exitCodeError
		if errors.As(err, &coded) {
			fmt.Fprintln(stderr, "pw:", coded.message)
			return coded.code
		}
		fmt.Fprintln(stderr, "pw:", err)
		return 1
	}
	return 0
}

// commandSummaries is the one-line description of every command, in the order
// a project meets them: create, extend, generate, run, then diagnose. The
// dispatch switch above and this table are the two places a new command is
// added, and help that omits a command is how the documented list went stale
// before.
//
// check sits beside generate rather than with the diagnostics at the end,
// because it answers a question about generate's output and a reader scanning
// this list for the CI gate looks for it there.
var commandSummaries = []struct{ name, summary string }{
	{"init", "create a project in a new directory"},
	{"add", "enable a capability in a project that declined it"},
	{"new", "scaffold a handler or a page beside the ones you have"},
	{"generate", "write everything a compiler needs, stopping before the compiler"},
	{"check", "report generated files that are stale or missing"},
	{"fmt", "format template sources into their canonical form"},
	{"i18n", "reconcile message catalogs against the templates that use them"},
	{"migrate", "inspect and apply database migrations"},
	{"seed", "load seed datasets into the database"},
	{"build", "run generate and then compile the project"},
	{"dev", "watch, regenerate, rebuild, and restart"},
	{"doctor", "report what a named environment will actually run"},
	{"request", "send one request to the running application, routed by its OpenAPI"},
	{"rename", "rename a template declaration and everything that names it"},
	{"lsp", "serve editor analysis over the Language Server Protocol"},
	{"version", "print the version, revision, and toolchain"},
	{"help", "print this message"},
}

// commandHelp is the usage of each command and the notes that go with it, in
// the order pw help prints them. It is one table so that pw help and
// pw <command> --help cannot drift into saying different things.
var commandHelp = []struct {
	name  string
	lines []string
}{
	{"init", []string{
		initUsage,
		"  Omit the project name to answer the same questions in the wizard.",
		"  A capability declined here can be enabled later with pw add.",
	}},
	{"add", []string{
		addUsage,
		"  Omit the capability to pick from what this project does not already have.",
	}},
	{"new", []string{
		newUsage,
		"  Omit the kind to pick one, then answer for the route and the package.",
	}},
	{"generate", []string{
		generateUsage,
		"  --code-only writes the generated Go and stops, for the editor and the",
		"  inner loop. What it leaves out is what a compiler needs: without the",
		"  asset tree, the embed directive in public.go has no directory to read.",
	}},
	{"check", []string{
		checkUsage,
	}},
	{"fmt", []string{
		fmtUsage,
		"  Omit every path to format the sources your generate purposes list.",
	}},
	{"migrate", []string{
		migrateUsage,
		"  Actions: " + strings.Join(migrateActions, ", "),
	}},
	{"seed", []string{
		seedUsage,
	}},
	{"build", []string{
		buildUsage,
		"  --backend selects the HTTP implementation; --target selects deployment packaging.",
		"  --debug keeps the source maps, and pw build also keeps the Go symbols.",
		"  Without it the artifact carries neither, which is what staging and",
		"  production want: an artifact that ships its own sources rehearses nothing.",
	}},
	{"doctor", []string{
		doctorUsage,
	}},
	{"request", []string{
		requestUsage,
		"  curl flags keep their meaning: -X -d -F -H -b -c -u -i -f -s -L -G --json.",
		"  With the catalog, -d key=value goes where the handler reads it: a path",
		"  segment, the query, a header, a cookie, or the body. --format=json",
		"  prints one object for an agent. --url picks the origin; without it the",
		"  running pw dev application is asked, then the configured port is tried.",
	}},
	{"rename", []string{
		renameUsage,
		"  Previews the edit set; --apply writes it. The set reaches handwritten",
		"  Go, so seeing it first is the point. Generated files are not edited:",
		"  run pw generate afterwards.",
	}},
	{"lsp", []string{
		lspUsage,
		"  Started by an editor rather than by hand; stdio carries the protocol,",
		"  so nothing but protocol messages may be written to it.",
	}},
}

// summaryOnlyUsage is the usage line of a command pw help describes by its
// summary alone, because the line would add nothing to the list there. Asked
// for directly, it is still the answer.
var summaryOnlyUsage = map[string]string{
	"dev":  "usage: pw dev",
	"i18n": i18nUsage,
}

// printCommandUsage prints what pw help says about one command, and reports
// whether it knew the command.
func printCommandUsage(w io.Writer, name string) bool {
	lines, known := []string(nil), false
	for _, command := range commandHelp {
		if command.name == name {
			lines, known = command.lines, true
		}
	}
	if line, ok := summaryOnlyUsage[name]; ok {
		lines, known = []string{line}, true
	}
	if !known {
		return false
	}
	for _, summary := range commandSummaries {
		if summary.name == name {
			fmt.Fprintf(w, "pw %s: %s\n\n", name, summary.summary)
		}
	}
	for _, line := range lines {
		fmt.Fprintln(w, line)
	}
	return true
}

// asksForHelp reports whether a command line asks for its own usage.
//
// request and doctor answer the flag themselves, and lsp reads no flags at
// all. Arguments after a bare -- belong to something else, so they are not
// searched.
func asksForHelp(command string, args []string) bool {
	switch command {
	case "request", "doctor", "lsp":
		return false
	}
	for _, arg := range args {
		switch arg {
		case "--":
			return false
		case "--help", "-h":
			return true
		}
	}
	return false
}

func printUsage(w io.Writer) {
	fmt.Fprintln(w, "Usage: pw <command> [arguments]")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Commands:")
	for _, command := range commandSummaries {
		fmt.Fprintf(w, "  %-8s  %s\n", command.name, command.summary)
	}
	fmt.Fprintln(w)
	for _, command := range commandHelp {
		for _, line := range command.lines {
			fmt.Fprintln(w, line)
		}
	}
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Documentation: https://shibukawa.github.io/popcornweb/")
}
