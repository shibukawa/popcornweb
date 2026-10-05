package pwcli

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

// pw generate exists so a build the framework does not drive can still get the
// tree a compiler needs. That only holds if it is reachable: the dispatcher and
// the help table are the two places a command is registered, and a command
// missing from the second is one nobody discovers. pw check is the same command
// pair's other half and was invisible for exactly that reason, as a flag.
func TestGenerateAndCheckAreRegisteredInTheCommandList(t *testing.T) {
	var usage bytes.Buffer
	printUsage(&usage)
	for _, name := range []string{"generate", "check"} {
		var found bool
		for _, command := range commandSummaries {
			if command.name == name {
				found = true
				if command.summary == "" {
					t.Fatalf("%s has no summary line", name)
				}
			}
		}
		if !found {
			t.Fatalf("%s is missing from commandSummaries, so pw help omits it", name)
		}
		if !strings.Contains(usage.String(), name) {
			t.Fatalf("pw help does not name %s", name)
		}

		var out, errOut bytes.Buffer
		if status := Main([]string{name, "--nonsense"}, &out, &errOut); status == 0 {
			t.Fatalf("the dispatcher did not reach %s: a bad argument exited 0", name)
		}
	}
}

// The two retired names are gone with no trace, which is the point of removing
// them rather than aliasing them: nothing in the dispatch, the flag parsing, or
// the help text remembers a command that no longer exists. They fail the way
// any name the CLI never had fails.
func TestTheRetiredNamesAreGone(t *testing.T) {
	var usage bytes.Buffer
	printUsage(&usage)
	if strings.Contains(usage.String(), "prepare") {
		t.Error("pw help still names prepare")
	}
	// pw fmt keeps its own --check, so the help text still contains the word.
	// What must be gone is generate's, and the usage line is where a reader
	// would find it offered.
	if strings.Contains(generateUsage, "--check") {
		t.Errorf("pw generate still offers --check: %s", generateUsage)
	}

	var out, errOut bytes.Buffer
	if status := Main([]string{"prepare"}, &out, &errOut); status == 0 {
		t.Fatal("pw prepare still runs")
	}
	if got := errOut.String(); !strings.Contains(got, "unknown command") {
		t.Errorf("pw prepare did not fail as an unknown command: %q", got)
	}
	if _, err := buildFlags("generate", []string{"--check"}); err == nil {
		t.Error("pw generate still accepts --check")
	}
}

// --backend selects the HTTP implementation for both commands. --target is a
// deployment destination and therefore belongs to build alone.
func TestBuildFlagsSeparateBackendAndTarget(t *testing.T) {
	for _, command := range []string{"build", "generate"} {
		options, err := buildFlags(command, nil)
		if err != nil || options.debug || options.backend != "nethttp" || options.target != "" {
			t.Errorf("%s with no argument: %+v, err = %v", command, options, err)
		}
		options, err = buildFlags(command, []string{"--debug"})
		if err != nil || !options.debug {
			t.Errorf("%s --debug: %+v, err = %v", command, options, err)
		}
		// Both spellings, because a pipeline writes one and a person the other.
		for _, spelling := range [][]string{{"--backend", "fasthttp"}, {"--backend=fasthttp"}} {
			options, err = buildFlags(command, spelling)
			if err != nil || options.backend != "fasthttp" {
				t.Errorf("%s %v: %+v, err = %v", command, spelling, options, err)
			}
			if tags := options.tags(); len(tags) != 2 || tags[0] != "-tags" || tags[1] != "fasthttp" {
				t.Errorf("%s %v compiled with %v", command, spelling, tags)
			}
		}
		// A near miss is refused rather than ignored, because a pipeline that
		// meant to ask for a debug artifact and did not would ship the other one.
		if _, err := buildFlags(command, []string{"-debug"}); err == nil {
			t.Errorf("%s accepted -debug", command)
		}
		// An unknown target is refused rather than passed to the compiler as a
		// build tag nothing sets, which would silently produce the first
		// transport's binary under another name.
		if _, err := buildFlags(command, []string{"--backend", "valyala"}); err == nil {
			t.Errorf("%s accepted an unknown backend", command)
		}
		if _, err := buildFlags(command, []string{"--backend"}); err == nil {
			t.Errorf("%s accepted --backend with no value", command)
		}
	}
	for target := range deploymentTargets {
		options, err := buildFlags("build", []string{"--target", target, "--backend=fasthttp"})
		if err != nil || options.target != target || options.backend != "fasthttp" {
			t.Errorf("build target %s: %+v, err = %v", target, options, err)
		}
	}
	if _, err := buildFlags("generate", []string{"--target=lambda"}); err == nil {
		t.Error("generate accepted a deployment target")
	}
	if _, err := buildFlags("build", []string{"--target=fasthttp"}); err == nil {
		t.Error("fasthttp is still accepted as a target")
	}
}

// --code-only is the narrow generation the old pw generate performed, and it is
// generate's alone: a build needs every input, and a flag that quietly removed
// one would leave the compiler to report the absence as an embed error.
func TestCodeOnlyBelongsToGenerate(t *testing.T) {
	options, err := buildFlags("generate", []string{"--code-only"})
	if err != nil || !options.codeOnly {
		t.Errorf("generate --code-only: %+v, err = %v", options, err)
	}
	if options, err := buildFlags("generate", nil); err != nil || options.codeOnly {
		t.Errorf("generate defaulted to code-only: %+v, err = %v", options, err)
	}
	if _, err := buildFlags("build", []string{"--code-only"}); err == nil {
		t.Error("build accepted --code-only")
	}
	// --debug survives only as source maps in a tree --code-only never builds,
	// so the combination is refused rather than half-honoured.
	if _, err := buildFlags("generate", []string{"--code-only", "--debug"}); err == nil {
		t.Error("--code-only --debug was accepted, so --debug was silently dropped")
	}
}

// A net/http build passes no tags at all, so its command line is what it was
// before the second target existed.
func TestTheDefaultTargetCompilesWithNoTags(t *testing.T) {
	if tags := (buildOptions{debug: true}).tags(); len(tags) != 0 {
		t.Errorf("the default target compiled with %v", tags)
	}
}

// The second transport's half is emitted by generation, and generation emits it
// only for a project that declared it. Building for a target the project never
// declared would compile the authored source with everything it needs tagged
// out, which fails as a pile of undefined symbols rather than as the one thing
// that is wrong.
func TestTheFastHTTPBackendNeedsTheDeclaration(t *testing.T) {
	if err := (buildOptions{backend: "fasthttp"}).check(projectConfig{}); err == nil {
		t.Error("a fasthttp build was accepted without project.fasthttp")
	} else if !strings.Contains(err.Error(), "project.fasthttp") {
		t.Errorf("the refusal does not name the declaration: %v", err)
	}
	if err := (buildOptions{backend: "fasthttp"}).check(projectConfig{FastHTTP: true}); err != nil {
		t.Errorf("a declared fasthttp build was refused: %v", err)
	}
	if err := (buildOptions{}).check(projectConfig{}); err != nil {
		t.Errorf("the default target was refused: %v", err)
	}
}

// None of the three takes a positional argument. Accepting one silently would
// let a caller believe a package or an output path was honoured.
func TestGenerateCheckAndBuildRejectArguments(t *testing.T) {
	ctx := context.Background()
	var out, errOut bytes.Buffer
	if err := runGenerate(ctx, []string{"./cmd/app"}, &out, &errOut); err == nil {
		t.Error("generate accepted an argument")
	} else if !strings.Contains(err.Error(), "generate") {
		t.Errorf("the error does not name the command: %v", err)
	}
	if err := runCheck(ctx, []string{"./cmd/app"}, &out); err == nil {
		t.Error("check accepted an argument")
	} else if !strings.Contains(err.Error(), "check") {
		t.Errorf("the error does not name the command: %v", err)
	}
	if err := runBuild(ctx, []string{"./cmd/app"}, &out, &errOut); err == nil {
		t.Error("build accepted an argument")
	} else if !strings.Contains(err.Error(), "build") {
		t.Errorf("the error does not name the command: %v", err)
	}
}

// buildProject reads the kind before anything runs, and the two commands part
// company there. A build in a package would generate and then fail its link
// step on an entry point that does not exist, which is a late error about the
// wrong thing; generation is the one thing a package project does want, and it
// is how a component package rebuilds its committed artifacts.
func TestBuildProjectRefusesAPackageForBuildOnly(t *testing.T) {
	t.Chdir(t.TempDir())
	if _, _, err := buildProject("build", true); err == nil {
		t.Fatal("build: a directory with no project was accepted")
	}
	if _, _, err := buildProject("generate", false); err == nil {
		t.Fatal("generate: a directory with no project was accepted")
	}
	if err := refuseInPackage(projectConfig{Kind: kindPackage}, "build"); err == nil {
		t.Fatal("build accepted a package project")
	}
}

// Every command answers --help with its own usage. Left to each parser, most
// of them reported the flag as an unknown option.
func TestEveryCommandAnswersHelp(t *testing.T) {
	for _, command := range commandSummaries {
		switch command.name {
		case "help", "version", "lsp", "request", "doctor":
			// help is the list itself, version takes no flags, and the other
			// three answer in their own words.
			continue
		}
		for _, flag := range []string{"--help", "-h"} {
			var out, errOut bytes.Buffer
			if status := Main([]string{command.name, flag}, &out, &errOut); status != 0 {
				t.Errorf("pw %s %s exited %d: %s", command.name, flag, status, errOut.String())
				continue
			}
			if !strings.Contains(out.String(), "usage: pw "+command.name) {
				t.Errorf("pw %s %s printed no usage:\n%s", command.name, flag, out.String())
			}
		}
	}
}
