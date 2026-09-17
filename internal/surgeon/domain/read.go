package domain

// ReadFileRequest describes a whole-file or line-range read of a Go
// source file. FromLine/ToLine are 1-based and inclusive; zero means
// "unbounded on that side". MaxBytes caps the rendered output (0 uses
// the implementation default).
type ReadFileRequest struct {
	FilePath string
	FromLine int
	ToLine   int
	MaxBytes int
}

// ReadFileResult is the numbered-content result of a file read. Content
// uses the same "N: line" numbering as symbol body=true. LineStart and
// LineEnd are the first and last lines actually returned; TotalLines is
// the file's full line count before any range/truncation was applied.
type ReadFileResult struct {
	File       string
	Package    string
	Imports    []string
	Content    string
	LineStart  int
	LineEnd    int
	TotalLines int
	Truncated  bool
	Notice     string
}
