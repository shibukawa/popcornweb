package pwcli

import (
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

// verifyScriptModuleTags refuses a script tag that references a built entry
// point without type=module.
//
// The build emits a module, and a module executed by a classic script tag is a
// syntax error at load time: the page renders, the script is silently gone, and
// nothing in the build or the response says so. That makes it exactly the kind
// of failure worth spending a scan on.
//
// It lives here rather than in the transform because the transform cannot see
// the tag. A conversion is memoized per distinct value and replayed for every
// occurrence, so an output decided from one tag would be served to every other
// tag naming the same file. The templates, on the other hand, are all readable
// at once from here.
func verifyScriptModuleTags(root string) error {
	var findings []string
	err := filepath.WalkDir(root, func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			switch entry.Name() {
			case ".git", ".pw", "dist", "node_modules", ".devbox":
				return filepath.SkipDir
			}
			// A template of another checkout is that checkout's to verify.
			if nestedWorktree(root, name) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(entry.Name(), ".pw.html") {
			return nil
		}
		source, err := os.ReadFile(name)
		if err != nil {
			return err
		}
		relative, relErr := filepath.Rel(root, name)
		if relErr != nil {
			relative = name
		}
		findings = append(findings, classicScriptTags(string(source), filepath.ToSlash(relative))...)
		return nil
	})
	if err != nil {
		return err
	}
	if len(findings) == 0 {
		return nil
	}
	sort.Strings(findings)
	return fmt.Errorf("a built script needs type=\"module\" on its tag, because the build emits a module:\n  %s",
		strings.Join(findings, "\n  "))
}

// classicScriptTags reports every script tag in one template that names a
// buildable entry and carries no module type.
func classicScriptTags(source, name string) []string {
	var findings []string
	rest, offset := source, 0
	for {
		start := strings.Index(rest, "<script")
		if start < 0 {
			return findings
		}
		end := strings.Index(rest[start:], ">")
		if end < 0 {
			return findings
		}
		end += start
		tag := rest[start : end+1]
		if reference := tagAttribute(tag, "src"); buildableEntry(reference) {
			if !strings.EqualFold(tagAttribute(tag, "type"), "module") {
				findings = append(findings, fmt.Sprintf("%s:%d: <script src=%q> has no type=\"module\"",
					name, lineOf(source, offset+start), reference))
			}
		}
		offset += end + 1
		rest = rest[end+1:]
	}
}

// buildableEntry reports whether a src is one the script hook will rewrite, so
// the check and the transform agree on what a built entry is.
//
// Both TypeScript extensions are entries. The build picks its loader from the
// extension and reads the project's tsconfig for the JSX transform, so a tsx
// entry needs nothing downstream that a ts entry did not already need; this
// gate was the whole difference.
func buildableEntry(value string) bool {
	if !localReference(value) {
		return false
	}
	switch strings.ToLower(path.Ext(assetTreePath(value))) {
	case ".ts", ".tsx":
		return true
	}
	return false
}

// tagAttribute reads one attribute out of a tag, accepting the quoting styles a
// template may be written in. An expression-valued attribute reads as empty,
// which is correct here: a dynamic src is reported by the seam itself and is
// never rewritten.
func tagAttribute(tag, name string) string {
	lower := strings.ToLower(tag)
	search := 0
	for {
		index := strings.Index(lower[search:], name)
		if index < 0 {
			return ""
		}
		index += search
		search = index + len(name)
		// The match has to be a whole attribute name: preceded by space and
		// followed by an equals sign, so "type" does not match "data-type".
		if index == 0 || !isTagSpace(tag[index-1]) {
			continue
		}
		rest := strings.TrimLeft(tag[index+len(name):], " \t\r\n")
		if !strings.HasPrefix(rest, "=") {
			continue
		}
		value := strings.TrimLeft(rest[1:], " \t\r\n")
		if value == "" {
			return ""
		}
		switch value[0] {
		case '"', '\'':
			if closing := strings.IndexByte(value[1:], value[0]); closing >= 0 {
				return value[1 : 1+closing]
			}
			return ""
		default:
			if closing := strings.IndexAny(value, " \t\r\n>"); closing >= 0 {
				return value[:closing]
			}
			return value
		}
	}
}

func isTagSpace(character byte) bool {
	return character == ' ' || character == '\t' || character == '\r' || character == '\n'
}

func lineOf(source string, offset int) int {
	if offset > len(source) {
		offset = len(source)
	}
	return strings.Count(source[:offset], "\n") + 1
}
