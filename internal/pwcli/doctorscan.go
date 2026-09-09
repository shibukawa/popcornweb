package pwcli

import (
	"bytes"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/shibukawa/popcornweb/internal/pwenv"
	"strconv"
	"strings"

	"github.com/shibukawa/popcornweb/internal/pwgen"
)

// projectScan is the file-level view of the project: what was generated, what
// it was generated from, and what the repository does with those files. It is
// collected once and shared by every diagnosed environment, because none of it
// depends on the environment token.
type projectScan struct {
	root string
	// generated maps a generated file to the source it was generated from,
	// which is absent for an orphan.
	generated []generatedFile
	// gitTracked is the set of repository-tracked paths, empty outside a work
	// tree.
	gitTracked map[string]bool
	inGitTree  bool
	// gitignore is the raw ignore file, empty when there is none.
	gitignore string
	// configFileModes records the permission bits of each environment file.
	configFileModes map[string]fs.FileMode
	// mainIsPackage records whether project.main holds a main package.
	mainIsPackage bool
	mainExists    bool
	// netdevRegistered records the tinygohelper.go blank import.
	netdevRegistered bool
	goDirective      string
	devboxVersions   map[string]string
}

type generatedFile struct {
	Path string
	// Source is the .pw.html or .pw.sql the artifact came from, or empty when
	// the artifact outlived it.
	Source     string
	SourceKind string
	Orphan     bool
	Stale      bool
}

const generatedSuffix = "_pw_gen.go"

// packageArtifacts are generated per package rather than from one source, so
// they have no source file to outlive. Keep this in step with the fixed names
// pw generate writes.
var packageArtifacts = map[string]bool{
	"popcornweb_bootstrap_pw_gen.go": true,
	"tinybind_openapi_pw_gen.go":     true,
	// The development registrations are generated from what a package already
	// produced rather than from one source beside them, so a scan looking for
	// that source finds none and would report every project carrying them.
	storybookFileName:     true,
	queryRegistryFileName: true,
	queryLinkFileName:     true,
	// A page tree route is a directory, so these two come from the directory
	// rather than from a file inside it. Both are written into every route
	// package a tree holds, which is what made their absence here a diagnosis
	// that every discovered-routing project failed on the day it was created.
	pwgen.PageDecoderOutput:  true,
	pwgen.PageRegistryOutput: true,
}

func newProjectScan(root string, state projectState, configFiles map[string]string) *projectScan {
	scan := &projectScan{
		root:            root,
		gitTracked:      map[string]bool{},
		configFileModes: map[string]fs.FileMode{},
		devboxVersions:  map[string]string{},
	}
	scan.scanGenerated()
	scan.scanGit()
	scan.scanMain(state.config.Main)
	scan.scanNetdev()
	scan.scanGoDirective()
	scan.scanDevbox(state.devbox)
	for name := range configFiles {
		if info, err := os.Stat(filepath.Join(root, filepath.FromSlash(name))); err == nil {
			scan.configFileModes[name] = info.Mode().Perm()
		}
	}
	// The dotenv files are read by the load rather than listed by the project
	// state, so their modes are gathered here, where the TOML ones are.
	if entries, err := os.ReadDir(root); err == nil {
		for _, entry := range entries {
			if entry.IsDir() || !pwenv.IsDotenvFileName(entry.Name()) {
				continue
			}
			if info, err := entry.Info(); err == nil {
				scan.configFileModes[entry.Name()] = info.Mode().Perm()
			}
		}
	}
	return scan
}

// scanGenerated pairs every generated artifact with the source it came from.
// Generating beside the source is what makes an orphan possible: the file still
// compiles and its registrations still run, so a deleted page keeps serving.
func (s *projectScan) scanGenerated() {
	_ = filepath.WalkDir(s.root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if entry.IsDir() {
			switch entry.Name() {
			case ".git", "node_modules", ".devbox", ".knowledge", storybookDirectory:
				// The storybook harness is a generated main in a directory of
				// its own, with no source beside it and nothing for a
				// diagnosis to say about it.
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(entry.Name(), generatedSuffix) || packageArtifacts[entry.Name()] {
			return nil
		}
		relative := s.relative(path)
		stem := strings.TrimSuffix(path, generatedSuffix)
		file := generatedFile{Path: relative, Orphan: true}
		// Every source kind pw generate reads. A kind missing from this list is
		// one whose output has no source the scan can find, so the diagnosis
		// tells a project to delete the file that serves it.
		for suffix, kind := range map[string]string{
			".pw.html":      "template",
			".pw.sql":       "query",
			".pw.dynamo":    "query",
			".pw.firestore": "query",
		} {
			candidate := stem + suffix
			info, statErr := os.Stat(candidate)
			if statErr != nil {
				continue
			}
			file.Source, file.SourceKind, file.Orphan = s.relative(candidate), kind, false
			if generatedInfo, genErr := entry.Info(); genErr == nil {
				file.Stale = generatedInfo.ModTime().Before(info.ModTime())
			}
			break
		}
		if file.Orphan && !s.generatedFromGo(stem) {
			s.generated = append(s.generated, file)
			return nil
		}
		if !file.Orphan {
			s.generated = append(s.generated, file)
		}
		return nil
	})
}

// generatedFromGo reports whether a Go source of the same stem exists. Handler
// and configuration binders are generated from Go sources, so their artifacts
// are not orphans just because no .pw.html sits beside them.
func (s *projectScan) generatedFromGo(stem string) bool {
	_, err := os.Stat(stem + ".go")
	return err == nil
}

func (s *projectScan) scanGit() {
	command := exec.Command("git", "ls-files")
	command.Dir = s.root
	var stdout bytes.Buffer
	command.Stdout = &stdout
	if err := command.Run(); err != nil {
		return
	}
	s.inGitTree = true
	for _, line := range strings.Split(stdout.String(), "\n") {
		if path := strings.TrimSpace(line); path != "" {
			s.gitTracked[path] = true
		}
	}
	if content, err := os.ReadFile(filepath.Join(s.root, ".gitignore")); err == nil {
		s.gitignore = string(content)
	}
}

func (s *projectScan) scanMain(main string) {
	if main == "" {
		return
	}
	directory := filepath.Join(s.root, filepath.FromSlash(strings.TrimPrefix(main, "./")))
	entries, err := os.ReadDir(directory)
	if err != nil {
		return
	}
	s.mainExists = true
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") {
			continue
		}
		content, readErr := os.ReadFile(filepath.Join(directory, entry.Name()))
		if readErr != nil {
			continue
		}
		for _, line := range strings.Split(string(content), "\n") {
			if strings.TrimSpace(line) == "package main" {
				s.mainIsPackage = true
				return
			}
		}
	}
}

// scanNetdev looks for the blank import a TinyGo build needs. Without it the
// binary builds and then exits at startup with "Netdev not set", which is the
// kind of failure worth naming before it happens.
func (s *projectScan) scanNetdev() {
	content, err := os.ReadFile(filepath.Join(s.root, "tinygohelper.go"))
	if err != nil {
		return
	}
	s.netdevRegistered = strings.Contains(string(content), "tinygodriver/netdev")
}

func (s *projectScan) scanGoDirective() {
	content, err := os.ReadFile(filepath.Join(s.root, "go.mod"))
	if err != nil {
		return
	}
	for _, line := range strings.Split(string(content), "\n") {
		if fields := strings.Fields(line); len(fields) == 2 && fields[0] == "go" {
			s.goDirective = fields[1]
			return
		}
	}
}

// scanDevbox records the version of each pinned package. devbox.json pins are
// "name@version" strings, so the value is readable without a JSON schema for
// the whole file.
func (s *projectScan) scanDevbox(devbox string) {
	for _, field := range strings.Split(devbox, "\"") {
		name, version, ok := strings.Cut(field, "@")
		if !ok || strings.ContainsAny(name, " \t{}[]:,") || version == "" {
			continue
		}
		s.devboxVersions[name] = version
	}
}

// devboxPins reports whether a package of this name is pinned. The name is a
// prefix, because a devbox package may carry a major version in its name —
// tailwindcss_4 is the one this asks about — and a project pinning that is
// pinning the tool the question is about.
func (s *projectScan) devboxPins(name string) bool {
	for pinned := range s.devboxVersions {
		if pinned == name || strings.HasPrefix(pinned, name+"_") {
			return true
		}
	}
	return false
}

func (s *projectScan) relative(path string) string {
	if relative, err := filepath.Rel(s.root, path); err == nil {
		return filepath.ToSlash(relative)
	}
	return filepath.ToSlash(path)
}

// tracked reports whether the repository tracks a project-relative path.
func (s *projectScan) tracked(path string) bool { return s.gitTracked[path] }

// ignores reports whether .gitignore mentions a pattern.
func (s *projectScan) ignores(pattern string) bool {
	for _, line := range strings.Split(s.gitignore, "\n") {
		if strings.TrimSpace(line) == pattern {
			return true
		}
	}
	return false
}

// olderThan reports whether the file at path is older than any of the sources.
func olderThan(path string, sources []string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	for _, source := range sources {
		sourceInfo, sourceErr := os.Stat(source)
		if sourceErr != nil {
			continue
		}
		if info.ModTime().Before(sourceInfo.ModTime()) {
			return true
		}
	}
	return false
}

// compareVersions reports whether a is older than b for dotted numeric
// versions. A component that is not a number compares as zero, which is enough
// for the toolchain pins this reads.
func compareVersions(a, b string) int {
	aParts, bParts := strings.Split(a, "."), strings.Split(b, ".")
	for index := 0; index < max(len(aParts), len(bParts)); index++ {
		left, right := 0, 0
		if index < len(aParts) {
			left, _ = strconv.Atoi(numericPrefix(aParts[index]))
		}
		if index < len(bParts) {
			right, _ = strconv.Atoi(numericPrefix(bParts[index]))
		}
		if left != right {
			if left < right {
				return -1
			}
			return 1
		}
	}
	return 0
}

func numericPrefix(value string) string {
	for index, r := range value {
		if r < '0' || r > '9' {
			return value[:index]
		}
	}
	return value
}
