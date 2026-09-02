package pwcli

import (
	"fmt"
	"strings"
)

// refuseInPackage stops a command that only means something for an application.
//
// A package produces no binary, so there is nothing for pw dev to run and
// nothing for pw build to link. Both would otherwise get some way in and fail
// on the absence of an entry point, which reads as a broken project rather than
// as a command that does not apply — and the error they reach suggests running
// pw init, which in an existing package is the wrong advice twice over.
func refuseInPackage(config projectConfig, command string) error {
	if config.Kind != kindPackage {
		return nil
	}
	return fmt.Errorf("%s: this is a package project, which builds no binary; "+
		"pw generate rebuilds the committed artifacts and go test is the loop", command)
}

// packageScaffoldFiles writes a concept:component-package repository: a Go
// module that ships Popcorn Web sources together with the artifacts generated
// from them.
//
// What makes this different from an application scaffold is one line in
// .gitignore. An application excludes its generated Go and recreates it before
// every build; a package commits it, because the consumer's build is go build
// and its generator never reads a dependency. Every other rule holds — the
// header, the name pattern, the ban on hand edits — and the source is still
// authoritative. The artifact is a build output that happens to be tracked.
func packageScaffoldFiles(options initOptions) map[string]string {
	module := options.Name
	name := moduleDirectory(module)
	pkg := goPackageIdentifier(name)
	files := map[string]string{
		"go.mod": "module " + module + "\n\ngo 1.27.0\n\n" + frameworkModuleDirective(),
		"popcornweb.toml": `# A package project. There is no project.main: the application that imports
# this module owns the entry point, and this one produces no binary.
[project]
name = "` + name + `"
kind = "package"

# What this module publishes. Every reader needs these answers before anything
# is compiled — pw generate has to know what to import in order to emit the
# import, and pw migrate applies a stream with no application binary at all —
# so they live in a file rather than in Go.
[package]
module = "` + module + `"
summary = "TODO: one line, shown wherever pw reports or offers this package"
# The package a consumer links, which is not the module root: generation looks
# at a directory, and a purpose cannot name ".", so the Go lives one level down.
# A consumer's bootstrap imports this path, and without the key it would import
# the module root and find no package there.
import = "` + module + "/" + pkg + `"

# The versions that produced the committed artifacts. They constrain nothing:
# go.mod performs the resolution, and this is the evidence pw doctor compares
# against the supported window when somebody installs this package.
[package.generated_with]
pw = "` + packageGeneratedWith() + `"

# Where generation looks. Every purpose is listed, including the empty ones,
# because none has a default and what each reads should be readable from the
# first run rather than inferred from an absence.
#
# config stays empty: there is no main package here to receive a registration
# linker. queries stays empty and must: a generated query carries one engine's
# placeholder syntax, chosen when this module was published, and this module
# cannot know its consumer's engine.
[generate]
templates = ["` + pkg + `"]
handlers = []
pages = []
queries = []
config = []
dynamo = []
`,
		pkg + "/" + pkg + ".pw.html": `package ` + pkg + `

// A component a consuming application's template can call once cross-package
// components land upstream, and one this package's own handlers can render
// today. The generated renderer beside this file is committed with it.
export component Greeting(name: string): html {
  <p>Hello, {name}.</p>
}
`,
		".gitignore": packageGitignore(),
		".vscode/settings.json": `{
    "files.exclude": {
        "**/*_pw_gen.go": true
    }
}
`,
		".vscode/extensions.json":        editorExtensionsScaffold(options),
		".editorconfig":                  editorConfigScaffold(),
		".github/workflows/generate.yml": packageCheckWorkflow(),
		"README.md":                      packageReadme(module, name),
	}
	return files
}

// packageGitignore is the application ignore file minus one line.
//
// That absence is the whole feature. A package whose generated Go is untracked
// publishes sources nothing in the consumer can turn into code, and the failure
// arrives as a compile error in somebody else's project. The line is left out
// here rather than added and commented, because a commented-out ignore rule is
// a thing somebody restores.
func packageGitignore() string {
	return ".devbox/\n" +
		// No devbox.d line, for the reason the application scaffold states: a
		// devbox service configuration is written once and never rewritten, so
		// excluding it ships a lockfile whose services nobody else can start.
		//
		// No binary line: this module builds none.
		//
		// No *_pw_gen.go line either, per decision:committed-package-artifacts.
		// The generated Go is committed, and the .vscode hide rule beside this
		// file is what keeps it out of the way while staying in the commit.
		//
		// No public/generated line, for the same reason and by the same decision:
		// a component's extracted style and script are what a consumer links, and
		// its build regenerates them no more than it regenerates the Go. The
		// application scaffold carries that line; this one must not gain it.
		"dist/cache/\ndist/derived/\ndist/manifest.json\ndist/routes.json\n*.db\n"
}

// packageCheckWorkflow is the staleness guard.
//
// The whole distribution model rests on the committed artifact matching the
// source it was generated from, and nothing in a consuming project can check
// that: its own generation never reads a dependency, so a package that forgot
// to regenerate fails to compile somewhere else, for somebody else. This runs
// the check where the source is, which is the only place it can be answered.
func packageCheckWorkflow() string {
	return `name: generate

on:
  push:
  pull_request:

jobs:
  check:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version: "1.26"
      - run: go install github.com/shibukawa/popcornweb/cmd/pw@latest
      # The committed artifacts have to be the ones this source produces. A
      # consumer builds them with the Go compiler alone and regenerates
      # nothing, so a stale commit is discovered in their project rather than
      # in this one unless this step catches it first.
      - run: pw check
      - run: go build ./...
      - run: go test ./...
`
}

// packageReadme states the two lines a consumer adds, because installing is a
// declaration rather than a copy and nothing writes those lines for them.
func packageReadme(module, name string) string {
	return "# " + name + `

TODO: what this package does, in a paragraph.

## Install

` + "```sh" + `
pw add ` + module + `
` + "```" + `

That writes the ` + "`go.mod`" + ` requirement and one entry in the consuming
project's ` + "`popcornweb.toml`" + `. Nothing is copied into their tree, and
their ` + "`pw generate`" + ` never reads this module: the generated Go in this
repository is what they compile.

Adding both lines by hand is a supported install.

## Developing

` + "```sh" + `
pw generate
go test ./...
` + "```" + `

` + "`pw dev`" + ` and ` + "`pw build`" + ` do not apply here — there is nothing
to run, and the tests are the loop. Commit what ` + "`pw generate`" + ` writes:
this repository tracks its generated Go on purpose.
`
}

// packageGeneratedWith reports the framework version the artifacts were
// generated against, for the manifest field pw doctor reads.
func packageGeneratedWith() string {
	directive := frameworkModuleDirective()
	// The directive is "require <module> <version>" on its first line, and a
	// development checkout replaces the module rather than pinning it.
	fields := strings.Fields(strings.SplitN(directive, "\n", 2)[0])
	if len(fields) == 3 {
		return fields[2]
	}
	return "latest"
}
