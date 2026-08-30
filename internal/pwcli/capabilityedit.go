package pwcli

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// tailwindDevboxPackage is the pinned toolchain of decision:tailwind-host-toolchain.
const tailwindDevboxPackage = "tailwindcss_4@4.1.18"

// tailwindToolchainRequirement is what an operator installing the toolchain
// themselves has to satisfy. The Devbox package name is a nixpkgs identifier,
// so it means nothing to someone reaching for mise, Homebrew, or Scoop.
const tailwindToolchainRequirement = "the standalone tailwindcss CLI, version 4 or later"

// imageDevboxPackages are the pinned encoders of decision:asset-transform-toolchain.
// libwebp carries cwebp and libavif carries avifenc.
//
// The versions are pinned rather than tracked, because a produced file now
// carries the digest of its own bytes in its name: an encoder that changed
// under a project would move every image URL, which is correct and is not
// something a build should do without the project saying so.
var imageDevboxPackages = []string{"libwebp@1.6.0", "libavif@1.3.0"}

// imageToolchainRequirement is what an operator installing the encoders
// themselves has to satisfy. The Devbox names are nixpkgs identifiers, so they
// mean nothing to someone reaching for Homebrew or apt.
const imageToolchainRequirement = "cwebp from libwebp, and avifenc from libavif for the optional avif variant"

// capabilityQueriesPurpose is the generate purpose the database capability has
// to open before its SQL example is read by anything.
const capabilityQueriesPurpose = "queries"

// capabilityDynamoPurpose is the generate purpose the DynamoDB capability
// opens. Like the page tree purpose it may be absent, because a project written
// without the store never named it.
const capabilityDynamoPurpose = "dynamo"

// capabilityFirestorePurpose is the generate purpose the Firestore capability
// opens, on the same terms.
const capabilityFirestorePurpose = "firestore"

// capabilityPageTreePurpose is the generate purpose the page tree capability
// opens. Unlike the others it may be absent, because a project written before
// page trees existed never named it.
const capabilityPageTreePurpose = "pages"

// setPagesPurpose writes the page tree purpose, adding the key when the project
// predates it. Every other purpose is required and can be edited in place; this
// one is the exception, so it is the only edit that may have to insert a line.
//
// Whether to insert is decided by looking for the key, never by whether the
// loaded list has entries: a scaffold that declined the capability writes
// pages = [], which is a present key holding nothing, and inserting beside it
// would define generate.pages twice — which the TOML parser refuses.
func setPagesPurpose(state projectState, values []string) (string, error) {
	source, err := os.ReadFile(filepath.Join(state.root, "popcornweb.toml"))
	if err != nil {
		return "", err
	}
	if edited, err := setGeneratePurposeIn(string(source), capabilityPageTreePurpose, values); err == nil {
		return edited, nil
	}
	entry := capabilityPageTreePurpose + " = [" + quotedList(values) + "]\n"
	table := ""
	inserted := false
	var out strings.Builder
	for line := range strings.Lines(string(source)) {
		trimmed := strings.TrimSpace(line)
		// The key belongs to [generate], so it goes in just before whatever
		// table follows it rather than at the end of the file.
		if !inserted && table == "generate" && strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
			out.WriteString(entry)
			inserted = true
		}
		if strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
			table = strings.Trim(trimmed, "[]")
		}
		out.WriteString(line)
	}
	if !inserted {
		if table != "generate" {
			return "", fmt.Errorf("popcornweb.toml: no [generate] table to add %s to", capabilityPageTreePurpose)
		}
		if !strings.HasSuffix(out.String(), "\n") {
			out.WriteString("\n")
		}
		out.WriteString(entry)
	}
	return out.String(), nil
}

// setGeneratePurpose rewrites one generate purpose in popcornweb.toml. The
// key is edited in place rather than appended, because every purpose is
// required and therefore already present.
func setGeneratePurpose(state projectState, purpose string, values []string) (string, error) {
	source, err := os.ReadFile(filepath.Join(state.root, "popcornweb.toml"))
	if err != nil {
		return "", err
	}
	return setGeneratePurposeIn(string(source), purpose, values)
}

// setGeneratePurposeIn is the same edit over a document already in hand, so two
// purposes can be rewritten in one plan without the second read undoing the
// first write.
func setGeneratePurposeIn(source, purpose string, values []string) (string, error) {
	table := ""
	var out strings.Builder
	replaced := false
	for line := range strings.Lines(source) {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
			table = strings.Trim(trimmed, "[]")
		}
		if !replaced && table == "generate" && isKeyLine(trimmed, purpose) {
			out.WriteString(purpose + " = [" + quotedList(values) + "]\n")
			replaced = true
			continue
		}
		out.WriteString(line)
	}
	if !replaced {
		return "", fmt.Errorf("popcornweb.toml: generate.%s not found", purpose)
	}
	return out.String(), nil
}

// setProjectDatabase records the engine .pw.sql sources are generated for,
// replacing an existing key or writing one under [project]. It takes the
// document rather than reading the file, so it composes with the other edits a
// plan makes to popcornweb.toml instead of overwriting them.
func setProjectDatabase(document, engine string) (string, error) {
	assignment := "database = " + strconv.Quote(engine) + "\n"
	table := ""
	var out strings.Builder
	written := false
	// The key belongs at the end of [project], which is wherever the next
	// table header starts or the document ends.
	for line := range strings.Lines(document) {
		trimmed := strings.TrimSpace(line)
		header := strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]")
		if !written && table == "project" && (header || isKeyLine(trimmed, "database")) {
			out.WriteString(assignment)
			written = true
			if isKeyLine(trimmed, "database") {
				continue
			}
		}
		if header {
			table = strings.Trim(trimmed, "[]")
		}
		out.WriteString(line)
	}
	if !written {
		if table != "project" {
			return "", fmt.Errorf("popcornweb.toml: [project] not found")
		}
		out.WriteString(assignment)
	}
	return out.String(), nil
}

// isKeyLine reports whether a trimmed line assigns the named key.
func isKeyLine(trimmed, key string) bool {
	rest, ok := strings.CutPrefix(trimmed, key)
	return ok && strings.HasPrefix(strings.TrimSpace(rest), "=")
}

// addDevboxPackage adds one package to the devbox.json array. The file is
// application-owned, so an unrecognized shape stops the command with the edit
// to make rather than a rewrite that loses the operator's formatting.
func addDevboxPackage(devbox, pkg string) (string, error) {
	if devbox == "" {
		return "", fmt.Errorf("devbox.json not found; add %q to the development environment yourself", pkg)
	}
	if strings.Contains(devbox, strconv.Quote(pkg)) {
		return devbox, nil
	}
	key := strings.Index(devbox, `"packages"`)
	if key < 0 {
		return "", fmt.Errorf(`devbox.json declares no "packages" array; add %q to it yourself`, pkg)
	}
	open := strings.Index(devbox[key:], "[")
	if open < 0 {
		return "", fmt.Errorf(`devbox.json "packages" is not an array; add %q to it yourself`, pkg)
	}
	open += key
	closing := strings.Index(devbox[open:], "]")
	if closing < 0 {
		return "", fmt.Errorf(`devbox.json "packages" is unterminated; add %q to it yourself`, pkg)
	}
	closing += open
	separator := ", "
	if strings.TrimSpace(devbox[open+1:closing]) == "" {
		separator = ""
	}
	return devbox[:closing] + separator + strconv.Quote(pkg) + devbox[closing:], nil
}

// devboxScaffold is the development environment api:cli-init writes and
// api:cli-add installs, so both reach the same file state.
func devboxScaffold(packages []string) string {
	return `{
  "$schema": "https://raw.githubusercontent.com/jetify-com/devbox/0.14.2/.schema/devbox.schema.json",
  "packages": [` + quotedList(packages) + `],
  "shell": {"init_hook": ["echo 'Popcorn Web development environment'"]}
}
`
}

// tailwindProjectConfig is the assets section api:cli-init scaffolds and
// api:cli-add appends, so both reach the same file state.
func tailwindProjectConfig() string {
	return `
[assets.tailwind]
enabled = true
input = "` + defaultTailwindInput + `"
output = "` + defaultTailwindOutput + `"
minify = true
`
}

// imagesProjectConfig turns the conversion on. The avif variant is off by
// default: it costs a second encode of every image and wins less often than the
// webp conversion does.
func imagesProjectConfig() string {
	return `
[assets.images]
enabled = true
# quality applies to a lossy source; a png stays on the lossless axis.
quality = ` + strconv.Itoa(defaultImageQuality) + `
avif = false
`
}

// tailwindEntryScaffold writes the CSS entry point. Tailwind scans the sources
// named here, so the entry names every purpose that reads templates: those are
// exactly the directories that can contain a class name, and a page tree is one
// of them even though it is a purpose of its own rather than a template
// directory.
//
// Naming them is belt and braces — Tailwind's own detection walks the project
// and finds these anyway — but the entry is a file the project owns and edits,
// and one that lists two of the three directories reads like the third was
// decided against.
func tailwindEntryScaffold(scope generationScope) string {
	sources := append(append([]string{}, scope.Templates...), scope.Pages...)
	if len(sources) == 0 {
		sources = []string{"handlers", "templates"}
	}
	entry := "@import \"tailwindcss\";\n"
	for _, source := range sources {
		entry += "@source \"../" + source + "\";\n"
	}
	return entry
}
