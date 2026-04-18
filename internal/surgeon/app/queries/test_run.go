package queries

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/JLugagne/go-surgeon/internal/surgeon/domain"
)

// TestRun executes `go test -json` scoped to a package/file and returns a
// compact, agent-friendly report. It is read-only (no mutation of the
// repository) which is why it lives behind SurgeonQueries.
//
// Safety:
//   - dir must stay inside the project root (absolute paths and paths
//     escaping via `..` are rejected).
//   - tags are restricted to [a-z_][a-z0-9_,.]* to prevent injecting extra
//     go-test flags through the build-tags argument.
//   - timeout is clamped to [1, 600] seconds.
//   - raw output is capped at 128 KiB.

const (
	testRunDefaultTimeoutSeconds = 120
	testRunMaxTimeoutSeconds     = 600
	testRunMaxOutputBytes        = 128 * 1024
	testRunMaxOutputLinesPerTest = 50
)

var testRunTagsRegex = regexp.MustCompile(`^[a-z_][a-z0-9_,.]*$`)

// testFailureLineRegex captures the stdlib testing "file.go:line:" prefix
// emitted by t.Errorf / t.Fatalf. The testing package writes those with
// leading whitespace (typically a single tab).
var testFailureLineRegex = regexp.MustCompile(`^\s+(\S+\.go):(\d+):\s?(.*)$`)

// testEvent mirrors the shape of `go test -json` records (src/cmd/test2json).
type testEvent struct {
	Time    string  `json:"Time"`
	Action  string  `json:"Action"`
	Package string  `json:"Package"`
	Test    string  `json:"Test"`
	Elapsed float64 `json:"Elapsed"`
	Output  string  `json:"Output"`
}

// TestRun implements service.SurgeonQueries.TestRun.
func (h *SurgeonQueriesHandler) TestRun(ctx context.Context, req domain.TestRunRequest) (domain.TestRunResult, error) {
	dir := strings.TrimSpace(req.Dir)
	affectedBy := strings.TrimSpace(req.AffectedBy)
	if dir != "" && affectedBy != "" {
		return domain.TestRunResult{}, fmt.Errorf("dir and affected_by are mutually exclusive")
	}

	var targets []string
	var affectedPkgs []string
	if affectedBy != "" {
		// Use tests=false for the closure: _test.go files can only be
		// imported from inside the same package, so they never create
		// new reverse-dep edges. Passing tests=true here just drags in
		// synthetic testmain pseudo-packages that pollute the Packages
		// result. `go test` on each affected package will still compile
		// and run that package's own tests.
		pkgs, err := h.ComputeAffectedPackages(ctx, affectedBy, false)
		if err != nil {
			return domain.TestRunResult{}, err
		}
		targets = pkgs
		affectedPkgs = pkgs
	} else {
		target, err := resolveTestTarget(dir)
		if err != nil {
			return domain.TestRunResult{}, err
		}
		targets = []string{target}
	}

	if req.Tags != "" && !testRunTagsRegex.MatchString(req.Tags) {
		return domain.TestRunResult{}, fmt.Errorf("invalid build tags %q: must match [a-z_][a-z0-9_,.]*", req.Tags)
	}

	timeout := req.TimeoutSeconds
	if timeout <= 0 {
		timeout = testRunDefaultTimeoutSeconds
	}
	if timeout > testRunMaxTimeoutSeconds {
		timeout = testRunMaxTimeoutSeconds
	}

	count := req.Count
	if count <= 0 {
		count = 1
	}

	args := []string{"test", "-json", fmt.Sprintf("-count=%d", count)}
	if req.Race {
		args = append(args, "-race")
	}
	if req.Tags != "" {
		args = append(args, "-tags", req.Tags)
	}
	if req.Run != "" {
		args = append(args, "-run", req.Run)
	}
	// Give `go test` its own timeout slightly below ours so it has a chance
	// to flush a final "fail" event before we kill it.
	goTestTimeout := timeout - 5
	if goTestTimeout < 1 {
		goTestTimeout = timeout
	}
	args = append(args, fmt.Sprintf("-timeout=%ds", goTestTimeout))
	args = append(args, targets...)

	runCtx, cancel := context.WithTimeout(ctx, time.Duration(timeout)*time.Second)
	defer cancel()

	start := time.Now()
	cmd := exec.CommandContext(runCtx, "go", args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	runErr := cmd.Run()
	duration := time.Since(start)

	timedOut := errors.Is(runCtx.Err(), context.DeadlineExceeded)

	result := parseTestRunOutput(stdout.Bytes())
	result.DurationMS = int(duration.Milliseconds())
	result.TimedOut = timedOut
	result.Packages = affectedPkgs

	// Surface go-test stderr (vet errors, build failures) appended to the
	// raw output so the agent sees why a run failed without tests executing.
	raw := stdout.String()
	if stderr.Len() > 0 {
		if raw != "" {
			raw += "\n"
		}
		raw += "STDERR:\n" + stderr.String()
	}
	if len(raw) > testRunMaxOutputBytes {
		raw = raw[:testRunMaxOutputBytes] + "\n... (truncated)"
	}
	result.RawOutput = raw

	if timedOut {
		result.Success = false
		if result.Summary == "" {
			result.Summary = fmt.Sprintf("timed out after %ds", timeout)
		} else {
			result.Summary += " (timed out)"
		}
		return result, nil
	}

	// go test exits non-zero for any failure — we already captured the
	// structured outcome, so we don't propagate ExitError here. Anything
	// else (binary missing, permission error) is a real error.
	if runErr != nil {
		var exitErr *exec.ExitError
		if !errors.As(runErr, &exitErr) {
			return result, fmt.Errorf("go test failed to start: %w", runErr)
		}
	}

	return result, nil
}

// resolveTestTarget converts the requested dir into a `go test` target while
// rejecting paths that escape the project root.
func resolveTestTarget(dir string) (string, error) {
	dir = strings.TrimSpace(dir)
	if dir == "" || dir == "./..." {
		return "./...", nil
	}

	if filepath.IsAbs(dir) {
		return "", fmt.Errorf("dir must be a relative path, got %q", dir)
	}

	cleaned := filepath.Clean(dir)
	// Reject any traversal that reaches or climbs above the project root.
	if cleaned == ".." || strings.HasPrefix(cleaned, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("dir %q escapes the project root", dir)
	}

	// Use ./<dir> so `go test` interprets it as a relative package path
	// rather than an import path. If the caller already asked for a
	// recursive target with "/...", preserve it.
	if strings.HasSuffix(dir, "/...") {
		return "./" + cleaned, nil
	}
	return "./" + cleaned, nil
}

// parseTestRunOutput consumes `go test -json` stdout line-by-line and builds
// the per-test summary.
func parseTestRunOutput(stdout []byte) domain.TestRunResult {
	type caseKey struct {
		pkg, name string
	}
	cases := make(map[caseKey]*domain.TestCaseResult)
	order := make([]caseKey, 0)
	packageSet := make(map[string]struct{})

	scanner := bufio.NewScanner(bytes.NewReader(stdout))
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 || line[0] != '{' {
			continue
		}
		var ev testEvent
		if err := json.Unmarshal(line, &ev); err != nil {
			continue
		}
		if ev.Package != "" {
			packageSet[ev.Package] = struct{}{}
		}
		if ev.Test == "" {
			// package-level events (pass/fail/output without Test) are
			// mostly about build diagnostics — surfaced via RawOutput.
			continue
		}
		key := caseKey{pkg: ev.Package, name: ev.Test}
		tc, ok := cases[key]
		if !ok {
			tc = &domain.TestCaseResult{Package: ev.Package, Name: ev.Test}
			cases[key] = tc
			order = append(order, key)
		}
		switch ev.Action {
		case "output":
			out := strings.TrimRight(ev.Output, "\n")
			if tc.FailureFile == "" {
				if m := testFailureLineRegex.FindStringSubmatch(out); m != nil {
					tc.FailureFile = m[1]
					if ln, err := strconv.Atoi(m[2]); err == nil {
						tc.FailureLine = ln
					}
					tc.FailureMessage = strings.TrimSpace(m[3])
				}
			}
			tc.OutputLines = append(tc.OutputLines, out)
		case "pass":
			tc.Status = "pass"
			tc.ElapsedMS = int(ev.Elapsed * 1000)
		case "fail":
			tc.Status = "fail"
			tc.ElapsedMS = int(ev.Elapsed * 1000)
		case "skip":
			tc.Status = "skip"
			tc.ElapsedMS = int(ev.Elapsed * 1000)
		}
	}

	tests := make([]domain.TestCaseResult, 0, len(order))
	passed, failed, skipped := 0, 0, 0
	for _, key := range order {
		tc := cases[key]
		// Tests left without a terminal action are treated as failed
		// (the most common cause is a timeout killing the run mid-test).
		if tc.Status == "" {
			tc.Status = "fail"
		}
		switch tc.Status {
		case "pass":
			passed++
			tc.OutputLines = nil
		case "skip":
			skipped++
			tc.OutputLines = nil
		case "fail":
			failed++
			if len(tc.OutputLines) > testRunMaxOutputLinesPerTest {
				tc.OutputLines = tc.OutputLines[:testRunMaxOutputLinesPerTest]
			}
		}
		tests = append(tests, *tc)
	}

	totalSeconds := 0.0
	for _, t := range tests {
		totalSeconds += float64(t.ElapsedMS) / 1000.0
	}

	pkgNoun := "package"
	if len(packageSet) != 1 {
		pkgNoun = "packages"
	}
	summary := fmt.Sprintf("%d passed, %d failed in %d %s (%.1fs)", passed, failed, len(packageSet), pkgNoun, totalSeconds)
	if skipped > 0 {
		summary = fmt.Sprintf("%d passed, %d failed, %d skipped in %d %s (%.1fs)", passed, failed, skipped, len(packageSet), pkgNoun, totalSeconds)
	}

	return domain.TestRunResult{
		Success: failed == 0,
		Tests:   tests,
		Summary: summary,
	}
}
