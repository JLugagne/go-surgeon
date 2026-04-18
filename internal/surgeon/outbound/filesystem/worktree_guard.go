package filesystem

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// guardWriteInWorktree refuses writes whose resolved real path lands
// outside the caller's current git worktree. The guard exists to catch
// symlink-induced writes: the Claude Code harness sometimes symlinks
// /go/<module> to the main checkout while a subagent runs inside a
// worktree under .claude/worktrees/. A naive os.WriteFile follows the
// symlink and clobbers main silently. This helper makes that case a
// loud error instead.
//
// Returns nil (allow) when:
//   - cwd is not inside a git worktree (temp dirs, non-git checkouts);
//   - target is inside GOMODCACHE (module-cache paths are symlinks by
//     design and edits there are an orthogonal concern);
//   - target resolves to the same worktree root as cwd.
//
// Returns a descriptive error otherwise so the caller can retry with a
// worktree-absolute path or cd into the intended worktree.
func guardWriteInWorktree(path string) error {
	cwd, err := os.Getwd()
	if err != nil {
		return nil // can't compute; fall through to normal write
	}
	cwdRoot := findGitWorktreeRoot(cwd)
	if cwdRoot == "" {
		return nil
	}

	abs, err := filepath.Abs(path)
	if err != nil {
		return nil
	}
	// Resolve symlinks on the deepest existing parent so the guard works
	// for new files in existing directories.
	real := resolveRealPath(abs)

	if modCache := os.Getenv("GOMODCACHE"); modCache != "" {
		if hasPathPrefix(real, modCache) {
			return nil
		}
	}

	targetRoot := findGitWorktreeRoot(real)
	if targetRoot == "" {
		// Target is outside any git worktree; allow (could be /tmp, etc.)
		return nil
	}
	if sameDir(targetRoot, cwdRoot) {
		return nil
	}
	return fmt.Errorf("refusing write: %q resolves to %q which is outside the current worktree %q; pass a worktree-absolute path or run from inside the intended worktree", path, real, cwdRoot)
}

// findGitWorktreeRoot walks up from dir looking for a .git entry (file
// or directory). Returns "" when no worktree root is found.
func findGitWorktreeRoot(start string) string {
	dir := start
	for {
		if dir == "" {
			return ""
		}
		info, err := os.Stat(filepath.Join(dir, ".git"))
		if err == nil && (info.IsDir() || info.Mode().IsRegular()) {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

// resolveRealPath resolves symlinks on the deepest existing ancestor of
// path and re-appends the missing tail. EvalSymlinks on a non-existent
// path errors out, so we fall back to resolving ancestors when the
// target is a new file.
func resolveRealPath(abs string) string {
	if real, err := filepath.EvalSymlinks(abs); err == nil {
		return real
	}
	dir, file := filepath.Split(abs)
	dir = strings.TrimRight(dir, string(filepath.Separator))
	if dir == "" || dir == abs {
		return abs
	}
	realDir := resolveRealPath(dir)
	return filepath.Join(realDir, file)
}

// hasPathPrefix reports whether path is equal to prefix or lives under
// prefix, using the path separator to avoid /foo matching /foobar.
func hasPathPrefix(path, prefix string) bool {
	if prefix == "" {
		return false
	}
	prefix = strings.TrimRight(prefix, string(filepath.Separator))
	if path == prefix {
		return true
	}
	return strings.HasPrefix(path, prefix+string(filepath.Separator))
}

func sameDir(a, b string) bool {
	ra, err := filepath.EvalSymlinks(a)
	if err != nil {
		ra = a
	}
	rb, err := filepath.EvalSymlinks(b)
	if err != nil {
		rb = b
	}
	return ra == rb
}
