package mcp

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/JLugagne/go-surgeon/internal/surgeon/domain"
	"github.com/JLugagne/go-surgeon/internal/surgeon/domain/service"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func textResult(text string) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: text},
		},
	}
}

// previewExecutor is implemented by app/commands.ExecutePlanHandler. It lets
// the MCP layer run a command closure against a dry-run filesystem and
// harvest a unified diff, without exposing the concrete handler type. Tools
// whose domain request already has a Preview field (plan-based tools, patch
// tools) don't need this — they set req.Preview=true and read result.Diff.
// This escape hatch covers the tools whose legacy return types don't carry
// a Diff field: Implement, Mock, Add/Update/DeleteInterface, TagStruct,
// ExtractInterface, and GenerateTest.
type previewExecutor interface {
	PreviewWith(ctx context.Context, fn func(service.SurgeonCommands) error) (string, []string, error)
}

// runPreview invokes fn against a preview-scoped commands (if the commands
// implementation supports it) and returns the harvested diff. When the
// concrete commands value does not implement previewExecutor, preview is
// treated as a no-op and fn runs directly — this keeps test doubles simple.
func runPreview(ctx context.Context, commands service.SurgeonCommands, fn func(service.SurgeonCommands) error) (diff string, files []string, err error) {
	if pe, ok := commands.(previewExecutor); ok {
		return pe.PreviewWith(ctx, fn)
	}
	return "", nil, fn(commands)
}

func errorResult(msg string) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: msg},
		},
		StructuredContent: errorOutput{Code: "ERROR", Message: msg},
		IsError:           true,
	}
}

// formatGraph renders packages the same way the CLI graph command does.
func formatGraph(packages []domain.GraphPackage, opts domain.GraphOptions) string {
	if !opts.Symbols {
		if len(packages) == 0 {
			return fmt.Sprintf("No Go packages found in '%s'.", opts.Dir)
		}
		var sb strings.Builder
		for _, pkg := range packages {
			line := pkg.Path
			if pkg.Summary != "" {
				line += " — " + pkg.Summary
			}
			if len(pkg.Deps) > 0 {
				line += " → " + strings.Join(pkg.Deps, ", ")
			} else if opts.Deps {
				line += " → (none)"
			}
			sb.WriteString(line)
			sb.WriteByte('\n')
		}
		return sb.String()
	}

	showHeader := opts.Summary || opts.Deps
	var sb strings.Builder
	first := true

	for _, pkg := range packages {
		hasFiles := len(pkg.Files) > 0
		hasHeader := showHeader && (pkg.Summary != "" || len(pkg.Deps) > 0)
		if !hasHeader && !hasFiles {
			if opts.Focus != "" {
				if !first {
					sb.WriteByte('\n')
				}
				sb.WriteString(pkg.Path)
				sb.WriteByte('\n')
				first = false
			}
			continue
		}

		if !first {
			sb.WriteByte('\n')
		}
		first = false

		if showHeader {
			line := pkg.Path
			if pkg.Summary != "" {
				line += " — " + pkg.Summary
			}
			if opts.Deps {
				if len(pkg.Deps) > 0 {
					line += " → " + strings.Join(pkg.Deps, ", ")
				} else {
					line += " → (none)"
				}
			}
			sb.WriteString(line)
			sb.WriteByte('\n')
		}

		for _, file := range pkg.Files {
			sb.WriteString(file.Path)
			sb.WriteByte('\n')
			for _, sym := range file.Symbols {
				for _, line := range strings.Split(sym, "\n") {
					sb.WriteString("  ")
					sb.WriteString(line)
					sb.WriteByte('\n')
				}
			}
		}
	}

	if sb.Len() == 0 {
		return fmt.Sprintf("No Go packages found in '%s'.", opts.Dir)
	}

	return sb.String()
}

// findSymbols mirrors the CLI symbol command's multi-form query resolution.
func findSymbols(ctx context.Context, queries service.SurgeonQueries, queryStr string, tests bool, dir string, module string, context string) []domain.SymbolResult {
	parts := strings.Split(queryStr, ".")
	var allResults []domain.SymbolResult

	if len(parts) == 1 {
		query := domain.SymbolQuery{Name: parts[0], Tests: tests, Module: module, Context: context}
		results, _ := queries.FindSymbols(ctx, query, dir)
		allResults = append(allResults, results...)
	}

	if len(parts) == 2 {
		query1 := domain.SymbolQuery{Receiver: parts[0], Name: parts[1], Tests: tests, Module: module, Context: context}
		results1, _ := queries.FindSymbols(ctx, query1, dir)
		allResults = append(allResults, results1...)

		query2 := domain.SymbolQuery{PackageName: parts[0], Name: parts[1], Tests: tests, Module: module, Context: context}
		results2, _ := queries.FindSymbols(ctx, query2, dir)
		allResults = append(allResults, results2...)
	}

	if len(parts) == 3 {
		query := domain.SymbolQuery{PackageName: parts[0], Receiver: parts[1], Name: parts[2], Tests: tests, Module: module, Context: context}
		results, _ := queries.FindSymbols(ctx, query, dir)
		allResults = append(allResults, results...)
	}

	return allResults
}

// formatSymbolResults renders symbol results the same way the CLI symbol command does.
func formatSymbolResults(results []domain.SymbolResult, showBody bool, queryStr string) string {
	var sb strings.Builder

	if len(results) > 1 {
		fmt.Fprintf(&sb, "Found %d matches for '%s'. Please refine your search:\n\n", len(results), queryStr)
		var funcs, methods, structs []domain.SymbolResult
		for _, r := range results {
			if r.Receiver != "" {
				methods = append(methods, r)
			} else if strings.HasPrefix(r.Signature, "func") {
				funcs = append(funcs, r)
			} else {
				structs = append(structs, r)
			}
		}
		if len(methods) > 0 {
			sb.WriteString("Matches (Methods):\n")
			for _, r := range methods {
				fmt.Fprintf(&sb, "- (%s) %s in %s:%d\n", r.Receiver, r.Name, r.File, r.LineStart)
			}
			sb.WriteByte('\n')
		}
		if len(funcs) > 0 {
			sb.WriteString("Matches (Functions):\n")
			for _, r := range funcs {
				fmt.Fprintf(&sb, "- %s in %s:%d\n", r.Name, r.File, r.LineStart)
			}
			sb.WriteByte('\n')
		}
		if len(structs) > 0 {
			sb.WriteString("Matches (Structs):\n")
			for _, r := range structs {
				fmt.Fprintf(&sb, "- %s in %s:%d\n", r.Name, r.File, r.LineStart)
			}
			sb.WriteByte('\n')
		}
		var hintCmd string
		if len(methods) > 0 {
			first := methods[0]
			hintCmd = fmt.Sprintf("symbol query=%s.%s", first.Receiver, first.Name)
		} else if len(funcs) > 0 {
			first := funcs[0]
			hintCmd = fmt.Sprintf("symbol query=%s dir=%s", first.Name, filepath.Dir(first.File))
		} else {
			first := structs[0]
			hintCmd = fmt.Sprintf("symbol query=%s dir=%s", first.Name, filepath.Dir(first.File))
		}
		fmt.Fprintf(&sb, "Hint: refine with '%s'.\n", hintCmd)
		return sb.String()
	}

	res := results[0]
	fmt.Fprintf(&sb, "Symbol: %s\n", res.Name)
	if res.Receiver != "" {
		fmt.Fprintf(&sb, "Receiver: %s\n", res.Receiver)
	}
	bodyLines := res.LineEnd - res.LineStart + 1
	fmt.Fprintf(&sb, "File: %s:%d-%d (%d lines body)\n", res.File, res.LineStart, res.LineEnd, bodyLines)
	if showBody {
		// Package + imports header: gives the agent enough ambient context
		// to patch without a follow-up Read (covers "do I need to add an
		// import?" and "what package is this in?" in one response).
		if res.Package != "" {
			fmt.Fprintf(&sb, "Package: %s\n", res.Package)
		}
		if len(res.Imports) > 0 {
			sb.WriteString("Imports:\n")
			for _, imp := range res.Imports {
				fmt.Fprintf(&sb, "  %s\n", imp)
			}
		}
		if len(res.FileOutline) > 0 {
			sb.WriteString("File outline:\n")
			for _, e := range res.FileOutline {
				if e.Receiver != "" {
					fmt.Fprintf(&sb, "  L%d-%d (%s) %s.%s: %s\n", e.LineStart, e.LineEnd, e.Kind, e.Receiver, e.Name, e.Signature)
				} else {
					fmt.Fprintf(&sb, "  L%d-%d (%s) %s: %s\n", e.LineStart, e.LineEnd, e.Kind, e.Name, e.Signature)
				}
			}
		}
	}
	if res.Doc != "" {
		fmt.Fprintf(&sb, "Doc:\n%s\n", res.Doc)
	}
	if showBody {
		fmt.Fprintf(&sb, "Code (Empty lines stripped):\n%s\n", res.Code)
	} else {
		fmt.Fprintf(&sb, "Signature:\n%s\n", res.Signature)
	}
	return sb.String()
}

func validateGoFile(file string) *mcp.CallToolResult {
	if !strings.HasSuffix(file, ".go") {
		return errorResult(fmt.Sprintf("rejected: file %q is not a Go file (.go extension required)", file))
	}
	return nil
}

// formatPatternResults renders pattern-mode symbol results as a compact list
// grouped by kind (methods / functions / types). When tokenBudget > 0, output
// is truncated with a trailer indicating how many results were omitted.
func formatPatternResults(results []domain.SymbolResult, showBody bool, pattern string, tokenBudget int, outline bool) string {
	var methods, funcs, types []domain.SymbolResult
	for _, r := range results {
		switch {
		case r.Receiver != "":
			methods = append(methods, r)
		case strings.HasPrefix(r.Signature, "func"):
			funcs = append(funcs, r)
		default:
			types = append(types, r)
		}
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "Found %d declaration(s) matching /%s/\n\n", len(results), pattern)

	// Token-budget-aware rendering: when showBody is true and
	// tokenBudget > 0, we emit full Code blocks until the running size
	// (~4 chars/token) would exceed the budget, then degrade remaining
	// matches to signature-only. tokenBudget == 0 means unlimited.
	budgetBytes := tokenBudget * 4
	bodiesEmitted := 0
	signaturesOnly := 0

	writeGroup := func(title string, rs []domain.SymbolResult) {
		if len(rs) == 0 {
			return
		}
		fmt.Fprintf(&sb, "%s:\n", title)
		for _, r := range rs {
			if r.Receiver != "" {
				fmt.Fprintf(&sb, "- (%s) %s — %s:%d\n", r.Receiver, r.Name, r.File, r.LineStart)
			} else {
				fmt.Fprintf(&sb, "- %s — %s:%d\n", r.Name, r.File, r.LineStart)
			}
			if outline {
				fmt.Fprintf(&sb, "  %s\n", r.Signature)
				if s := firstDocSentence(r.Doc); s != "" {
					fmt.Fprintf(&sb, "    — %s\n", s)
				}
				continue
			}
			if !showBody {
				continue
			}
			// Degrade to signature-only once we're over budget.
			if budgetBytes > 0 && sb.Len() > budgetBytes {
				fmt.Fprintf(&sb, "  %s\n", r.Signature)
				signaturesOnly++
				continue
			}
			if r.Code != "" {
				fmt.Fprintf(&sb, "%s\n", r.Code)
				bodiesEmitted++
			} else {
				fmt.Fprintf(&sb, "  %s\n", r.Signature)
				signaturesOnly++
			}
		}
		sb.WriteByte('\n')
	}

	writeGroup("Methods", methods)
	writeGroup("Functions", funcs)
	writeGroup("Types", types)

	if showBody && signaturesOnly > 0 && budgetBytes > 0 {
		fmt.Fprintf(&sb, "... (budget reached after %d bodies; %d more results shown as signatures only; raise token_budget to see all bodies)\n", bodiesEmitted, signaturesOnly)
	}

	return sb.String()
}

// symbolEdit describes one symbol operation within an edit result.
type symbolEdit struct {
	Action     string `json:"action"`
	Identifier string `json:"identifier,omitempty"`
	File       string `json:"file"`
}

// editOutput is the structured result for all write tools (create, update,
// delete, insert_call, execute_plan, add/update/delete_interface).
type editOutput struct {
	FilesModified []string     `json:"files_modified"`
	Symbols       []symbolEdit `json:"symbols,omitempty"`
	Warnings      []string     `json:"warnings,omitempty"`
	AddedImports  []string     `json:"added_imports,omitempty"`
	Preview       bool         `json:"preview,omitempty"`
	Diff          string       `json:"diff,omitempty"`
}

// patchFileOutput is the structured result for patch_file.
type patchFileOutput struct {
	File         string   `json:"file"`
	Applied      int      `json:"applied"`
	Hits         []int    `json:"hits,omitempty"`
	Preview      bool     `json:"preview,omitempty"`
	Diff         string   `json:"diff,omitempty"`
	AddedImports []string `json:"added_imports,omitempty"`
	Warnings     []string `json:"warnings,omitempty"`
}

// patchOutput is the structured result for patch_function, patch_struct and patch_interface.
type patchOutput struct {
	File         string         `json:"file"`
	Identifier   string         `json:"identifier"`
	Applied      int            `json:"applied"`
	Preview      bool           `json:"preview,omitempty"`
	Diff         string         `json:"diff,omitempty"`
	MockUpdated  bool           `json:"mock_updated,omitempty"`
	AddedImports []string       `json:"added_imports,omitempty"`
	Warnings     []string       `json:"warnings,omitempty"`
	AutoLifts    []autoLiftJSON `json:"auto_lifts,omitempty"`
}

// autoLiftJSON mirrors domain.AutoLiftInfo in the structured output so
// callers see when an insert_before/insert_after was lifted out of a
// nested scope to the enclosing top-level statement.
type autoLiftJSON struct {
	PatchIndex int    `json:"patch_index"`
	LiftedFrom string `json:"lifted_from"`
	LiftedTo   string `json:"lifted_to"`
	Context    string `json:"context,omitempty"`
}

// implementOutput is the structured result for the implement tool.
type implementOutput struct {
	File      string   `json:"file"`
	Interface string   `json:"interface"`
	Receiver  string   `json:"receiver"`
	Stubs     []string `json:"stubs_added"`
}

// mockOutput is the structured result for the mock tool.
type mockOutput struct {
	File      string `json:"file"`
	Interface string `json:"interface"`
	MockName  string `json:"mock_name"`
}

// testOutput is the structured result for the test tool.
type testOutput struct {
	TestFile   string `json:"test_file"`
	Identifier string `json:"identifier"`
}

// tagOutput is the structured result for the tag tool.
type tagOutput struct {
	File       string `json:"file"`
	Identifier string `json:"identifier"`
	Field      string `json:"field,omitempty"`
}

// extractInterfaceOutput is the structured result for the extract_interface tool.
type extractInterfaceOutput struct {
	InterfaceName string `json:"interface_name"`
	InterfaceFile string `json:"interface_file"`
	MockFile      string `json:"mock_file,omitempty"`
	MockName      string `json:"mock_name,omitempty"`
}

// buildDiagnostic is the JSON representation of a single compile diagnostic.
type buildDiagnostic struct {
	File    string `json:"file"`
	Line    int    `json:"line"`
	Column  int    `json:"column,omitempty"`
	Message string `json:"message"`
}

// buildCheckOutput is the structured result for the build_check tool.
type buildCheckOutput struct {
	Success     bool              `json:"success"`
	Diagnostics []buildDiagnostic `json:"diagnostics,omitempty"`
	RawOutput   string            `json:"raw_output,omitempty"`
	ExitCode    int               `json:"exit_code"`
	DurationMs  int64             `json:"duration_ms"`
	TimedOut    bool              `json:"timed_out,omitempty"`
	Truncated   bool              `json:"truncated,omitempty"`
}

func convertBuildDiagnostics(diags []domain.BuildDiagnostic) []buildDiagnostic {
	if len(diags) == 0 {
		return nil
	}
	out := make([]buildDiagnostic, len(diags))
	for i, d := range diags {
		out[i] = buildDiagnostic{File: d.File, Line: d.Line, Column: d.Column, Message: d.Message}
	}
	return out
}

// formatBuildCheckResult renders the structured BuildCheckResult into a
// compact human-readable summary: a status header, a grouped list of
// diagnostics (at most a few per file), and the timing/exit metadata.
func formatBuildCheckResult(r domain.BuildCheckResult) string {
	var sb strings.Builder
	switch {
	case r.TimedOut:
		fmt.Fprintf(&sb, "TIMED OUT after %dms (exit_code=%d)\n", r.DurationMs, r.ExitCode)
	case r.Success:
		fmt.Fprintf(&sb, "SUCCESS (build_check): no diagnostics in %dms\n", r.DurationMs)
	default:
		fmt.Fprintf(&sb, "FAILED (build_check): %d diagnostic(s) in %dms (exit_code=%d)\n", len(r.Diagnostics), r.DurationMs, r.ExitCode)
	}

	if len(r.Diagnostics) > 0 {
		// Group by file preserving first-seen order.
		order := []string{}
		groups := map[string][]domain.BuildDiagnostic{}
		for _, d := range r.Diagnostics {
			if _, ok := groups[d.File]; !ok {
				order = append(order, d.File)
			}
			groups[d.File] = append(groups[d.File], d)
		}
		sb.WriteByte('\n')
		for _, file := range order {
			fmt.Fprintf(&sb, "%s\n", file)
			for _, d := range groups[file] {
				if d.Column > 0 {
					fmt.Fprintf(&sb, "  %d:%d  %s\n", d.Line, d.Column, d.Message)
				} else {
					fmt.Fprintf(&sb, "  %d  %s\n", d.Line, d.Message)
				}
			}
		}
	} else if !r.Success && r.RawOutput != "" {
		// Compile failed but no parseable diagnostics — surface the raw output so the agent isn't blind.
		sb.WriteByte('\n')
		sb.WriteString("Raw output:\n")
		sb.WriteString(r.RawOutput)
		if !strings.HasSuffix(r.RawOutput, "\n") {
			sb.WriteByte('\n')
		}
	}

	if r.Truncated {
		sb.WriteString("\n(note: raw build output was truncated at 64 KiB)\n")
	}
	return sb.String()
}

// formatTestRunResult renders a compact, agent-friendly test report.
// Passed tests are collapsed into counts; failed tests get file:line and a
// short output snippet. Skipped tests are listed by name.
func formatTestRunResult(r domain.TestRunResult) string {
	var sb strings.Builder
	if r.Success {
		sb.WriteString("SUCCESS")
	} else if r.TimedOut {
		sb.WriteString("TIMEOUT")
	} else {
		sb.WriteString("FAIL")
	}
	fmt.Fprintf(&sb, " — %s (took %dms)\n", r.Summary, r.DurationMS)

	var fails []domain.TestCaseResult
	var skips []domain.TestCaseResult
	for _, t := range r.Tests {
		switch t.Status {
		case "fail":
			fails = append(fails, t)
		case "skip":
			skips = append(skips, t)
		}
	}

	if len(fails) > 0 {
		sb.WriteString("\nFailures:\n")
		for _, t := range fails {
			loc := ""
			if t.FailureFile != "" {
				loc = fmt.Sprintf(" at %s:%d", t.FailureFile, t.FailureLine)
			}
			fmt.Fprintf(&sb, "- %s/%s%s\n", t.Package, t.Name, loc)
			if t.FailureMessage != "" {
				fmt.Fprintf(&sb, "    %s\n", t.FailureMessage)
			}
		}
	}

	if len(skips) > 0 {
		sb.WriteString("\nSkipped:\n")
		for _, t := range skips {
			fmt.Fprintf(&sb, "- %s/%s\n", t.Package, t.Name)
		}
	}

	return sb.String()
}

// errorOutput is the JSON shape of StructuredContent on any error result.
// Agents can branch on Code (stable machine identifier) instead of
// string-matching Message. Code values come from domain.Error constants
// (e.g. CONFLICT, PATCH_FAILED, NOT_FOUND, INVALID_ARGUMENT,
// PATCH_PRODUCES_INVALID_GO, NODE_NOT_FOUND, FILE_NOT_FOUND). When the
// underlying error is not a *domain.Error, Code is "UNKNOWN"; when the
// error did not originate from a handler path (plain errorResult calls),
// Code is the generic "ERROR".
type errorOutput struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// errorResultWithCode produces a tool-level error result whose
// StructuredContent carries a machine-readable code. If err is a
// *domain.Error, Code is lifted from err.Code; otherwise Code is
// "UNKNOWN". The text content mirrors errorResult's format so the
// human-readable view is unchanged.
func errorResultWithCode(msg string, err error) *mcp.CallToolResult {
	out := errorOutput{Code: "UNKNOWN", Message: msg}
	var de *domain.Error
	if errors.As(err, &de) {
		out.Code = de.Code
	}
	return &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: msg},
		},
		StructuredContent: out,
		IsError:           true,
	}
}

// firstDocSentence returns the first sentence of a doc comment in plain
// text form: it strips any leading `//` / `/*` markers, collapses the
// text up to the first `.` (followed by space or end of text) or the
// first blank line, whichever comes first, and trims the trailing
// period. Returns "" when doc is empty or only whitespace.
func firstDocSentence(doc string) string {
	s := strings.TrimSpace(doc)
	if s == "" {
		return ""
	}
	// Stop at the first blank line (paragraph break) — only consider the
	// first paragraph as a summary candidate.
	if idx := strings.Index(s, "\n\n"); idx >= 0 {
		s = s[:idx]
	}
	// Within the first paragraph, collapse newlines to spaces so that a
	// sentence spread over multiple lines stays together.
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.TrimSpace(s)
	// Find the first ". " or ".\n" terminator; also accept a trailing "."
	// at end-of-string as a sentence terminator.
	for i := 0; i < len(s); i++ {
		if s[i] != '.' {
			continue
		}
		if i == len(s)-1 {
			return strings.TrimSpace(s[:i])
		}
		if next := s[i+1]; next == ' ' || next == '\t' {
			return strings.TrimSpace(s[:i])
		}
	}
	return s
}
