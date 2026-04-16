package queries

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/JLugagne/go-surgeon/internal/surgeon/domain"
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
	if dir == "" {
		dir = "./..."
	}

	target, err := resolveBuildTarget(dir)
	if err != nil {
		return domain.BuildCheckResult{}, err
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
	args = append(args, target)

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
	return s[:rawOutputCharLimit] + "\n... (output truncated)"
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
	b.buf = append(b.buf, p[:room]...)
	b.truncated = true
	return len(p), nil
}

func (b *limitedBuffer) String() string {
	return string(b.buf)
}
