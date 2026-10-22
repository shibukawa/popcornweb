package pwcli

import (
	"os"
	"path"
	"path/filepath"
	"strings"
)

// gitFileLimit bounds a read of a .git file, which is one line naming a
// directory. A larger file is not a link to one.
const gitFileLimit = 4096

// nestedWorktree reports whether directory, met while walking below root, is
// the top of another working copy of the repository the project lives in: a
// linked git worktree, such as the checkouts Claude Code keeps under
// .claude/worktrees.
//
// A walk over the whole project stops there. A worktree holds the project again
// — its popcornweb.toml, its sources, the files generated from them — so every
// scan of the tree finds a second copy of everything and judges it against a
// configuration written for the first. pw dev printed a stale-file warning for
// each generated file of each worktree on every regeneration, and its watcher
// counted their sources as rebuild inputs, so an edit in one restarted a server
// that does not run it.
//
// The walk root is never one: pw dev started inside a worktree is working on
// that checkout, whose own .git file looks like any other's.
func nestedWorktree(root, directory string) bool {
	return directory != root && linkedWorktree(directory)
}

// linkedWorktree reports whether directory is the top of a linked git worktree.
//
// A worktree's .git is a file rather than a directory. It holds "gitdir: " and
// the path of the worktree's administrative directory inside the main
// repository, <repository>/.git/worktrees/<name>. A submodule's .git is a file
// of the same shape naming <repository>/.git/modules/<name>, and a submodule is
// part of the project that contains it — listed in generate.*, or imported by
// the build — so the name of that directory's parent is what tells the two
// apart.
//
// Anything unreadable or unrecognised is not a worktree. A directory is left
// out of a scan on positive evidence only.
func linkedWorktree(directory string) bool {
	marker := filepath.Join(directory, ".git")
	// A directory is a clone of its own, and the type is checked before the
	// read so that a named pipe called .git cannot block the walk.
	info, err := os.Lstat(marker)
	if err != nil || !info.Mode().IsRegular() || info.Size() > gitFileLimit {
		return false
	}
	content, err := os.ReadFile(marker)
	if err != nil {
		return false
	}
	line, _, _ := strings.Cut(string(content), "\n")
	gitdir, found := strings.CutPrefix(line, "gitdir:")
	if !found {
		return false
	}
	return path.Base(path.Dir(path.Clean(filepath.ToSlash(strings.TrimSpace(gitdir))))) == "worktrees"
}
