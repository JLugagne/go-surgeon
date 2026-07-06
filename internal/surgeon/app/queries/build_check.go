package queries

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/JLugagne/go-surgeon/internal/surgeon/domain"
	"golang.org/x/tools/go/packages"
)

const (
	defaultBuildTimeoutSeconds = 60
	maxBuildTimeoutSeconds     = 600
	maxBuildOutputBytes        = 64 * 1024
	rawOutputCharLimit         = 4000
)

// diagnosticRegex matches a single `go build` compiler diagnostic line of
// the shape "path/to/file.go:LINE:COL: message". The column is optional:
// some diagnostics (e.g. "vet: ...") omit it.
var diagnosticRegex = regexp.MustCompile(`^(.+\.go):(\d+)(?::(\d+))?:\s*(.+)$`)

// BuildCheck runs `go build` scoped to req.Dir (default "./...") and returns
// a structured report of any compile diagnostics. The invocation is bounded
// by req.TimeoutSeconds (defaulting to 60, capped at 600), and the captured
// output is truncated at 64 KiB. Diagnostics are deduplicated per file.
func (h *SurgeonQueriesHandler) BuildCheck(ctx context.Context, req domain.BuildCheckRequest) (domain.BuildCheckResult, error) {
	dir := strings.TrimSpace(req.Dir)
	affectedBy := strings.TrimSpace(req.AffectedBy)
	if dir != "" && affectedBy != "" {
		return domain.BuildCheckResult{}, fmt.Errorf("dir and affected_by are mutually exclusive")
	}

	var targets []string
	var affectedPkgs []string
	if affectedBy != "" {
		pkgs, err := h.ComputeAffectedPackages(ctx, affectedBy, req.Tests)
		if err != nil {
			return domain.BuildCheckResult{}, err
		}
		targets = pkgs
		affectedPkgs = pkgs
	} else {
		if dir == "" {
			dir = "./..."
		}
		target, err := resolveBuildTarget(dir)
		if err != nil {
			return domain.BuildCheckResult{}, err
		}
		targets = []string{target}
	}

	timeout := req.TimeoutSeconds
	if timeout <= 0 {
		timeout = defaultBuildTimeoutSeconds
	}
	if timeout > maxBuildTimeoutSeconds {
		timeout = maxBuildTimeoutSeconds
	}

	runCtx, cancel := context.WithTimeout(ctx, time.Duration(timeout)*time.Second)
	defer cancel()

	args := []string{"build"}
	if req.Tests {
		// `go test -count=0` compiles test binaries without running them.
		args = []string{"test", "-count=0", "-run", "^$"}
	}
	args = append(args, targets...)

	cmd := exec.CommandContext(runCtx, "go", args...)
	cmd.Env = os.Environ()

	start := time.Now()
	var buf limitedBuffer
	buf.limit = maxBuildOutputBytes
	cmd.Stdout = &buf
	cmd.Stderr = &buf

	runErr := cmd.Run()
	duration := time.Since(start)

	timedOut := runCtx.Err() == context.DeadlineExceeded

	exitCode := 0
	if runErr != nil {
		if ee, ok := runErr.(*exec.ExitError); ok {
			exitCode = ee.ExitCode()
		} else if !timedOut {
			// Exec failure that wasn't an ExitError (e.g. `go` missing);
			// bubble up as an error rather than a compile diagnostic.
			return domain.BuildCheckResult{
				Success:    false,
				RawOutput:  truncateRawOutput(buf.String()),
				ExitCode:   -1,
				DurationMs: duration.Milliseconds(),
				TimedOut:   false,
				Truncated:  buf.truncated,
			}, fmt.Errorf("failed to run go build: %w", runErr)
		} else {
			exitCode = -1
		}
	}

	raw := buf.String()
	diagnostics := parseBuildDiagnostics(raw)

	return domain.BuildCheckResult{
		Success:     runErr == nil,
		Diagnostics: diagnostics,
		RawOutput:   truncateRawOutput(raw),
		ExitCode:    exitCode,
		DurationMs:  duration.Milliseconds(),
		TimedOut:    timedOut,
		Truncated:   buf.truncated,
		Packages:    affectedPkgs,
	}, nil
}

// resolveBuildTarget validates req.Dir and returns a sanitized argument for
// `go build`. Only three shapes are accepted: the default "./..." recursive
// pattern, a relative path (optionally suffixed with "/..."), or the bare
// "..." wildcard. Absolute paths and any traversal via ".." are rejected so
// the tool cannot be coerced into building code outside the project.
func resolveBuildTarget(dir string) (string, error) {
	if dir == "./..." || dir == "..." {
		return "./...", nil
	}
	if filepath.IsAbs(dir) {
		return "", fmt.Errorf("dir must be a relative path within the project; got absolute %q", dir)
	}
	// Strip a trailing "/..." to validate the base path, then re-attach.
	base := strings.TrimSuffix(dir, "/...")
	base = strings.TrimSuffix(base, string(filepath.Separator)+"...")
	cleaned := filepath.Clean(base)
	if cleaned == ".." || strings.HasPrefix(cleaned, ".."+string(filepath.Separator)) || strings.Contains(cleaned, string(filepath.Separator)+"..") {
		return "", fmt.Errorf("dir must stay within the project root; got %q", dir)
	}
	if cleaned == "." {
		if strings.HasSuffix(dir, "...") {
			return "./...", nil
		}
		return ".", nil
	}
	// go build accepts "./pkg/path" or "./pkg/path/..." — normalize to that.
	target := "./" + cleaned
	if strings.HasSuffix(dir, "...") {
		target += "/..."
	}
	return target, nil
}

// parseBuildDiagnostics walks raw go-build output line by line, picks up
// lines that look like a compiler diagnostic and deduplicates them per
// (file, line, column, message) tuple so repeated "cannot find name X"
// from multiple import sites don't inflate the result set.
func parseBuildDiagnostics(raw string) []domain.BuildDiagnostic {
	if raw == "" {
		return nil
	}
	seen := make(map[string]struct{})
	var out []domain.BuildDiagnostic
	scanner := bufio.NewScanner(strings.NewReader(raw))
	// Allow long lines — compiler errors can embed long file paths.
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		m := diagnosticRegex.FindStringSubmatch(trimmed)
		if m == nil {
			continue
		}
		file := m[1]
		lineNum, _ := strconv.Atoi(m[2])
		colNum := 0
		if m[3] != "" {
			colNum, _ = strconv.Atoi(m[3])
		}
		msg := strings.TrimSpace(m[4])
		key := fmt.Sprintf("%s|%d|%d|%s", file, lineNum, colNum, msg)
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, domain.BuildDiagnostic{
			File:    file,
			Line:    lineNum,
			Column:  colNum,
			Message: msg,
		})
	}
	return out
}

// truncateRawOutput clips the raw compiler output at rawOutputCharLimit chars
// and appends a visible marker so callers know something was dropped.
func truncateRawOutput(s string) string {
	if len(s) <= rawOutputCharLimit {
		return s
	}
	// Back the cut off to a rune boundary so we never split a multi-byte rune
	// (which would otherwise surface as U+FFFD).
	cut := rawOutputCharLimit
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + "\n... (output truncated)"
}

// limitedBuffer is an io.Writer that drops bytes past `limit`, flagging the
// event so BuildCheckResult can expose a `truncated` signal to the caller.
type limitedBuffer struct {
	buf       []byte
	limit     int
	truncated bool
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	if b.limit <= 0 || len(b.buf) >= b.limit {
		b.truncated = true
		return len(p), nil
	}
	room := b.limit - len(b.buf)
	if len(p) <= room {
		b.buf = append(b.buf, p...)
		return len(p), nil
	}
	// Back off to a rune boundary so the truncated tail never splits a
	// multi-byte rune (which would render as U+FFFD).
	for room > 0 && !utf8.RuneStart(p[room]) {
		room--
	}
	b.buf = append(b.buf, p[:room]...)
	b.truncated = true
	return len(p), nil
}

func (b *limitedBuffer) String() string {
	return string(b.buf)
}

// ComputeAffectedPackages resolves the owning package of filePath and walks
// the module's reverse-dependency graph to find every package whose (transitive)
// imports include the owner. The returned slice is the owner plus its rdeps,
// suitable for passing as positional args to `go build` or `go test`.
//
// The function loads the whole module once via the shared loader cache (keyed
// on absolute module root + tests), builds a forward-import map, then inverts
// it into a reverse-dep map and does a BFS from the owning package. Only
// packages that belong to the same module as the owner are considered.
func (h *SurgeonQueriesHandler) ComputeAffectedPackages(ctx context.Context, filePath string, tests bool) ([]string, error) {
	if filePath == "" {
		return nil, fmt.Errorf("affected_by is empty")
	}
	absFile, err := filepath.Abs(filePath)
	if err != nil {
		return nil, fmt.Errorf("resolve affected_by %q: %w", filePath, err)
	}
	st, err := os.Stat(absFile)
	if err != nil {
		return nil, fmt.Errorf("affected_by %q: %w", filePath, err)
	}
	if st.IsDir() {
		return nil, fmt.Errorf("affected_by %q must be a file, not a directory", filePath)
	}

	absFileDir := filepath.Dir(absFile)

	// Load from the current working directory so cache keys align with the
	// repo's module root (the typical case from MCP/CLI callers).
	cwd, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("getwd: %w", err)
	}
	loaded, err := h.loader.Load(ctx, cwd, tests)
	if err != nil {
		return nil, fmt.Errorf("load packages: %w", err)
	}
	if len(loaded.Pkgs) == 0 {
		return nil, fmt.Errorf("no Go packages found under %q", cwd)
	}

	// Identify the owning package by matching the file's directory against
	// the package's GoFiles (Dir(file) equals the package dir for file-per-
	// package layouts). CompiledGoFiles is authoritative because it survives
	// build-tag filtering, but GoFiles is a useful fallback on older Go.
	var owner *packages.Package
	for _, p := range loaded.Pkgs {
		if pkgContainsFile(p, absFile, absFileDir) {
			owner = p
			break
		}
	}
	if owner == nil {
		return nil, fmt.Errorf("affected_by %q is not inside any package of the current module", filePath)
	}
	if owner.Module == nil {
		return nil, fmt.Errorf("affected_by %q has no module information", filePath)
	}
	moduleRoot := owner.Module.Dir
	modulePath := owner.Module.Path

	// Build the reverse-dependency closure over packages belonging to the
	// same module as the owner. Packages outside the module (stdlib, deps)
	// are ignored — we only rebuild in-repo code.
	inModule := make(map[string]*packages.Package)
	for _, p := range loaded.Pkgs {
		if p.Module != nil && p.Module.Path == modulePath {
			inModule[p.PkgPath] = p
		}
	}

	// Forward edges: pkg -> its direct imports (within the module).
	// BFS reverse: from owner, walk pkgs that import something already in the
	// affected set. Keep iterating until no new pkg is added.
	affected := map[string]struct{}{owner.PkgPath: {}}
	for {
		added := false
		for path, p := range inModule {
			if _, ok := affected[path]; ok {
				continue
			}
			for imp := range p.Imports {
				if _, hit := affected[imp]; hit {
					affected[path] = struct{}{}
					added = true
					break
				}
			}
		}
		if !added {
			break
		}
	}

	// Translate pkg import paths into `./relative/path` form so `go build`
	// resolves them without needing -C. Packages at the module root map to ".".
	out := make([]string, 0, len(affected))
	for pkgPath := range affected {
		p := inModule[pkgPath]
		if p == nil || p.Module == nil {
			continue
		}
		rel, err := filepath.Rel(moduleRoot, pkgDir(p))
		if err != nil || rel == "" || rel == "." {
			out = append(out, ".")
			continue
		}
		out = append(out, "./"+filepath.ToSlash(rel))
	}
	sort.Strings(out)
	return out, nil
}

// pkgContainsFile reports whether absFile is one of the package's Go files,
// falling back to a directory-equality check when the file lists are empty
// (e.g. a newly-created file the loader hasn't seen yet).
func pkgContainsFile(p *packages.Package, absFile, absFileDir string) bool {
	for _, f := range p.CompiledGoFiles {
		if sameFile(f, absFile) {
			return true
		}
	}
	for _, f := range p.GoFiles {
		if sameFile(f, absFile) {
			return true
		}
	}
	// Directory fallback: every package file lives in the same directory, so
	// matching the dir of any known file to absFileDir is sufficient.
	if d := pkgDir(p); d != "" && sameDir(d, absFileDir) {
		return true
	}
	return false
}

// pkgDir returns the directory holding the package's Go files, or "" if the
// package has no files (rare; typically test-only synthetic packages).
func pkgDir(p *packages.Package) string {
	if len(p.GoFiles) > 0 {
		return filepath.Dir(p.GoFiles[0])
	}
	if len(p.CompiledGoFiles) > 0 {
		return filepath.Dir(p.CompiledGoFiles[0])
	}
	return ""
}

// sameFile compares two paths after EvalSymlinks + Clean so that symlinked
// GOPATH/GOROOT entries still match. Falls back to plain Clean equality when
// EvalSymlinks fails (e.g. a file that doesn't exist yet).
func sameFile(a, b string) bool {
	if filepath.Clean(a) == filepath.Clean(b) {
		return true
	}
	ra, errA := filepath.EvalSymlinks(a)
	rb, errB := filepath.EvalSymlinks(b)
	if errA == nil && errB == nil {
		return filepath.Clean(ra) == filepath.Clean(rb)
	}
	return false
}

// sameDir is sameFile for directory paths.
func sameDir(a, b string) bool {
	return sameFile(a, b)
}
