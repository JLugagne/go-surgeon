package queries

import (
	"context"
	"fmt"
	"go/parser"
	"go/token"
	"strings"

	"github.com/JLugagne/go-surgeon/internal/surgeon/domain"
)

// ReadFile returns the text of a Go source file with line numbers, or a
// numbered line range. Scaffold implementation.
func (h *SurgeonQueriesHandler) ReadFile(ctx context.Context, req domain.ReadFileRequest) (domain.ReadFileResult, error) {
	if req.FilePath == "" {
		return domain.ReadFileResult{}, &domain.Error{Code: "INVALID_ARGUMENT", Message: "read: file path is required"}
	}
	if !strings.HasSuffix(req.FilePath, ".go") {
		return domain.ReadFileResult{}, &domain.Error{Code: "INVALID_ARGUMENT", Message: fmt.Sprintf("read only operates on .go files, got %q", req.FilePath)}
	}
	if req.FromLine < 0 || req.ToLine < 0 {
		return domain.ReadFileResult{}, &domain.Error{Code: "INVALID_ARGUMENT", Message: "read: from/to must be non-negative line numbers"}
	}
	if req.FromLine > 0 && req.ToLine > 0 && req.FromLine > req.ToLine {
		return domain.ReadFileResult{}, &domain.Error{Code: "INVALID_ARGUMENT", Message: fmt.Sprintf("read: from (%d) must not exceed to (%d)", req.FromLine, req.ToLine)}
	}

	src, err := h.fs.ReadFile(ctx, req.FilePath)
	if err != nil {
		return domain.ReadFileResult{}, &domain.Error{Code: "READ_ERROR", Message: fmt.Sprintf("cannot read %s: %v", req.FilePath, err), Err: err}
	}

	lines := splitFileLines(string(src))
	total := len(lines)

	from := req.FromLine
	if from <= 0 {
		from = 1
	}
	to := req.ToLine
	if to <= 0 || to > total {
		to = total
	}

	res := domain.ReadFileResult{File: req.FilePath, TotalLines: total}
	if from > total {
		res.LineStart = from
		res.LineEnd = from - 1
		res.Notice = fmt.Sprintf("requested range starts after end of file (%d lines)", total)
		return res, nil
	}
	res.LineStart = from

	// defaultReadMaxBytes caps a single read; larger files are truncated
	// with a notice so the caller knows to narrow the range.
	maxBytes := req.MaxBytes
	if maxBytes <= 0 {
		maxBytes = 256 * 1024
	}

	var b strings.Builder
	lastReturned := from - 1
	for n := from; n <= to; n++ {
		line := fmt.Sprintf("%d: %s\n", n, lines[n-1])
		if b.Len()+len(line) > maxBytes {
			res.Truncated = true
			break
		}
		b.WriteString(line)
		lastReturned = n
	}
	res.LineEnd = lastReturned
	res.Content = strings.TrimSuffix(b.String(), "\n")
	if res.Truncated {
		res.Notice = fmt.Sprintf("output truncated at %d bytes; returned lines %d-%d of %d — re-run with from/to to read the rest", maxBytes, res.LineStart, res.LineEnd, total)
	}

	if f, perr := parser.ParseFile(token.NewFileSet(), req.FilePath, src, parser.PackageClauseOnly); perr == nil && f.Name != nil {
		res.Package = f.Name.Name
	}
	if f, perr := parser.ParseFile(token.NewFileSet(), req.FilePath, src, parser.ImportsOnly); perr == nil {
		for _, imp := range f.Imports {
			res.Imports = append(res.Imports, strings.Trim(imp.Path.Value, `"`))
		}
	}
	return res, nil
}

// splitFileLines splits src into lines without counting a single trailing
// newline as an extra empty line, so numbering matches symbol body=true.
func splitFileLines(src string) []string {
	if src == "" {
		return nil
	}
	lines := strings.Split(src, "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}
