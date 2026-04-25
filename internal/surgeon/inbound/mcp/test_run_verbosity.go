package mcp

import (
	"github.com/JLugagne/go-surgeon/internal/surgeon/domain"
)

// testRunVerbosityThreshold is the auto-mode tipping point: when a suite
// produces strictly more than this many test cases, the structured payload
// switches to "summary" mode unless the caller explicitly passes
// verbosity="full". The value is calibrated so that small/medium suites keep
// the rich per-test view (timing for every case, raw_output on failure) while
// large suites stay within the 25k-token tool-result budget on which agents
// rely for in-loop verification.
const testRunVerbosityThreshold = 50

// testRunSummaryPayload mirrors domain.TestRunResult but only carries the
// fields meaningful in summary mode. Passing tests are dropped entirely; only
// failed (and skipped) tests survive in Tests, with a counter exposed via
// PassedCount so callers can still see "how many green".
type testRunSummaryPayload struct {
	Success     bool                    `json:"success"`
	Summary     string                  `json:"summary"`
	Tests       []domain.TestCaseResult `json:"tests"`
	PassedCount int                     `json:"passed_count"`
	DurationMS  int                     `json:"duration_ms"`
	TimedOut    bool                    `json:"timed_out,omitempty"`
	Packages    []string                `json:"packages,omitempty"`
	RawOutput   string                  `json:"raw_output,omitempty"`
	Verbosity   string                  `json:"verbosity"`
}

// applyTestRunVerbosity returns the structured payload that should be attached
// to the MCP response, given the caller's explicit verbosity choice and the
// shape of the test result. It honors verbosity="summary"/"full" verbatim;
// when verbosity is empty the threshold drives the decision (auto mode).
//
// In summary mode, raw_output is dropped, passing tests are collapsed into a
// counter, and per-test elapsed_ms / output_lines for the few surviving
// (failed/skipped) tests are preserved untouched so the agent can act on the
// failure context. include_raw_output=true forces raw_output to survive even
// in summary mode (a deliberate escape hatch for agents that still want the
// full go test -json stream alongside the compact tests slice).
func applyTestRunVerbosity(result domain.TestRunResult, verbosity string, includeRawOutput bool) any {
	mode := resolveTestRunVerbosity(verbosity, len(result.Tests))
	if mode == "full" {
		return result
	}

	var failedOrSkipped []domain.TestCaseResult
	passed := 0
	for _, t := range result.Tests {
		if t.Status == "pass" {
			passed++
			continue
		}
		failedOrSkipped = append(failedOrSkipped, t)
	}

	payload := testRunSummaryPayload{
		Success:     result.Success,
		Summary:     result.Summary,
		Tests:       failedOrSkipped,
		PassedCount: passed,
		DurationMS:  result.DurationMS,
		TimedOut:    result.TimedOut,
		Packages:    result.Packages,
		Verbosity:   "summary",
	}

	// raw_output is the single biggest token sink; it stays out of summary
	// payloads unless the caller explicitly opted in via include_raw_output.
	// On failure, formatTestRunResult still surfaces the failure context in
	// the textual result, so the agent is not flying blind.
	if includeRawOutput {
		payload.RawOutput = result.RawOutput
	}

	return payload
}

// resolveTestRunVerbosity picks the effective verbosity mode given the caller
// hint and the suite size. Empty/unknown values fall back to auto, where the
// threshold (testRunVerbosityThreshold) is the only decision input.
func resolveTestRunVerbosity(verbosity string, testCount int) string {
	switch verbosity {
	case "summary":
		return "summary"
	case "full":
		return "full"
	}
	if testCount > testRunVerbosityThreshold {
		return "summary"
	}
	return "full"
}
