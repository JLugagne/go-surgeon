package domain

// TestRunRequest configures a scoped `go test` run initiated through the
// test_run query. Fields mirror the MCP tool inputs.
type TestRunRequest struct {
	// Dir is the target directory (relative to the project root) whose
	// tests will be executed. Empty or "." means "./...".
	Dir string
	// Run is the optional -run filter (regexp) passed to go test.
	Run string
	// Count is the number of times each test runs. Zero falls back to 1.
	Count int
	// Race enables -race.
	Race bool
	// Tags is a comma-separated list of build tags.
	Tags string
	// TimeoutSeconds caps the overall wall-clock budget. Defaults to 120
	// and is clamped to 600.
	TimeoutSeconds int
}

// TestCaseResult summarizes a single `go test -json` test action.
type TestCaseResult struct {
	Package        string   `json:"package"`
	Name           string   `json:"name"`
	Status         string   `json:"status"` // pass | fail | skip
	ElapsedMS      int      `json:"elapsed_ms"`
	OutputLines    []string `json:"output_lines,omitempty"`
	FailureFile    string   `json:"failure_file,omitempty"`
	FailureLine    int      `json:"failure_line,omitempty"`
	FailureMessage string   `json:"failure_message,omitempty"`
}

// TestRunResult is the compact, agent-friendly summary returned by the
// test_run query.
type TestRunResult struct {
	Success    bool             `json:"success"`
	Tests      []TestCaseResult `json:"tests"`
	Summary    string           `json:"summary"`
	RawOutput  string           `json:"raw_output,omitempty"`
	DurationMS int              `json:"duration_ms"`
	TimedOut   bool             `json:"timed_out,omitempty"`
}
