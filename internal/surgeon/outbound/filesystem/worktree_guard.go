// Package filesystem worktree-guard helpers: anchor writes on the
// worktree the MCP server was launched in, rewrite cross-worktree paths
// that arrive via symlinks (the /go/<module> -> parent-checkout case),
// and refuse writes that would land outside any worktree we can map
// back to that root.
package filesystem

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// rootEnvVar is the environment variable that pins the worktree root.
// When set, it overrides any cwd-based heuristic. The Claude Code
// harness can set this when spawning the MCP server inside an agent
// worktree to guarantee writes land where the agent expects them.
const rootEnvVar = "GO_SURGEON_ROOT"

// resolveRoot returns the worktree root the FileSystem should anchor on.
// Priority:
//  1. GO_SURGEON_ROOT env var (resolved + symlinks).
//  2. Walk up from cwd to find a .git entry.
//  3. Empty string when no worktree is detectable (temp dirs, non-git
//     checkouts); callers fall through to plain os.* semantics.
func resolveRoot() string {
	// MCP_WORKTREE_ROOT takes priority (set by Claude Code harness)
	if env := strings.TrimSpace(os.Getenv("MCP_WORKTREE_ROOT")); env != "" {
		if abs, err := filepath.Abs(env); err == nil {
			return canonical(abs)
		}
		return canonical(env)
	}
	// GO_SURGEON_ROOT is the legacy variable
	if env := strings.TrimSpace(os.Getenv(rootEnvVar)); env != "" {
		if abs, err := filepath.Abs(env); err == nil {
			return canonical(abs)
		}
		return canonical(env)
	}
	cwd, err := os.Getwd()
	if err != nil {
		return ""
	}
	return canonical(findGitWorktreeRoot(cwd))
}

// normalizePath turns a caller-supplied path into a write target inside
// the captured worktree root.
//
//   - Empty root: returns the input unchanged (best-effort mode for
//     tests, /tmp, non-git checkouts).
//   - Relative path: anchored on root rather than os.Getwd(), so a
//     server launched from the parent checkout still writes into the
//     agent worktree once root is set.
//   - GOMODCACHE targets: passed through unchanged (read-only by
//     convention; module cache paths are symlinks by design).
//   - Absolute path that resolves into root (possibly through a
//     symlink): rewritten to use the canonical root prefix with a
//     warning describing the rewrite so callers can surface it to the
//     agent.
//   - Absolute path under root or in a sibling worktree: passed through
//     unchanged so the caller's explicit absolute path is honored.
func normalizePath(root, path string) (string, string, error) {
	if path == "" {
		return path, "", nil
	}
	if root == "" {
		return path, "", nil
	}

	abs := path
	if !filepath.IsAbs(abs) {
		abs = filepath.Join(root, abs)
	}

	if modCache := os.Getenv("GOMODCACHE"); modCache != "" {
		if hasPathPrefix(canonical(abs), canonical(modCache)) {
			return abs, "", nil
		}
	}

	real := resolveRealPath(abs)

	if hasPathPrefix(real, root) {
		rel, err := filepath.Rel(root, real)
		if err != nil {
			return abs, "", nil
		}
		if rel != "" && !strings.HasPrefix(rel, "..") {
			rewritten := filepath.Join(root, rel)
			if rewritten != abs {
				warn := fmt.Sprintf("path %q resolved to %q via symlink; rewrote to %q (root=%q)", abs, real, rewritten, root)
				return rewritten, warn, nil
			}
			return abs, "", nil
		}
		return abs, "", nil
	}

	siblingRoot := findGitWorktreeRoot(real)
	if siblingRoot == "" {
		return abs, "", nil
	}

	if !sameDir(siblingRoot, root) {
		return abs, "", nil
	}

	return abs, "", nil
}

// findGitWorktreeRoot walks up from start looking for a .git entry (file
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

// canonical returns the symlink-resolved absolute form of dir, or dir
// itself when resolution fails.
func canonical(dir string) string {
	if dir == "" {
		return ""
	}
	if real, err := filepath.EvalSymlinks(dir); err == nil {
		return real
	}
	return dir
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
	return canonical(a) == canonical(b)
}
