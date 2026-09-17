package mcp

import (
	"context"
	"fmt"

	"github.com/JLugagne/go-surgeon/internal/surgeon/domain"
	"github.com/JLugagne/go-surgeon/internal/surgeon/domain/service"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type readInput struct {
	File     string `json:"file" jsonschema:"path to the Go file to read"`
	FromLine int    `json:"from_line,omitempty" jsonschema:"first line to return (1-based, inclusive); 0 means start of file"`
	ToLine   int    `json:"to_line,omitempty" jsonschema:"last line to return (1-based, inclusive); 0 means end of file"`
	MaxBytes int    `json:"max_bytes,omitempty" jsonschema:"cap the output in bytes; default 262144. A truncation notice is appended when hit."`
}

type readOutput struct {
	File       string   `json:"file"`
	Package    string   `json:"package,omitempty"`
	Imports    []string `json:"imports,omitempty"`
	Content    string   `json:"content"`
	LineStart  int      `json:"line_start"`
	LineEnd    int      `json:"line_end"`
	TotalLines int      `json:"total_lines"`
	Truncated  bool     `json:"truncated,omitempty"`
	Notice     string   `json:"notice,omitempty"`
}

// registerReadTool wires the read primitive so agents never need to shell
// out to cat/Read for a .go file (issue #37).
func registerReadTool(s *mcp.Server, queries service.SurgeonQueries) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        "read",
		Description: "Read a whole Go file or a line range with line numbers (same numbering as symbol body=true). Use this instead of shelling out to cat/Read for .go files. from_line/to_line bound the range; max_bytes caps the output and appends an explicit truncation notice. Only .go files are accepted.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in readInput) (*mcp.CallToolResult, any, error) {
		if in.File == "" {
			return errorResult("read: file is required"), nil, nil
		}
		res, err := queries.ReadFile(ctx, domain.ReadFileRequest{
			FilePath: in.File,
			FromLine: in.FromLine,
			ToLine:   in.ToLine,
			MaxBytes: in.MaxBytes,
		})
		if err != nil {
			return errorResultWithCode(fmt.Sprintf("ERROR (read): %v", err), err), nil, nil
		}
		text := res.Content
		if res.Notice != "" {
			text += "\n\n" + res.Notice
		}
		out := textResult(text)
		out.StructuredContent = readOutput{
			File:       res.File,
			Package:    res.Package,
			Imports:    res.Imports,
			Content:    res.Content,
			LineStart:  res.LineStart,
			LineEnd:    res.LineEnd,
			TotalLines: res.TotalLines,
			Truncated:  res.Truncated,
			Notice:     res.Notice,
		}
		return out, nil, nil
	})
}
