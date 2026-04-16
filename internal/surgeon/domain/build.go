package domain

// BuildCheckRequest asks to run `go build` against a directory/package.
// Dir is relative to the project root when set; an empty value means "./...".
type BuildCheckRequest struct {
	Dir            string
	Tests          bool
	TimeoutSeconds int
}

// BuildDiagnostic is a single compiler error/warning with file:line:col + message.
type BuildDiagnostic struct {
	File    string
	Line    int
	Column  int
	Message string
}

// BuildCheckResult is the structured result of a build_check run. Success is
// true when the underlying `go build` invocation exited cleanly. RawOutput is
// the (possibly truncated) combined stderr/stdout of the process.
type BuildCheckResult struct {
	Success     bool
	Diagnostics []BuildDiagnostic
	RawOutput   string
	ExitCode    int
	DurationMs  int64
	TimedOut    bool
	Truncated   bool
}
