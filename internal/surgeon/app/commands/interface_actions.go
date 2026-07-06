package commands

import (
	"context"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"

	"github.com/JLugagne/go-surgeon/internal/surgeon/domain"
)

// AddInterface appends an interface type declaration to a file and optionally generates a mock.
func (h *ExecutePlanHandler) AddInterface(ctx context.Context, req domain.InterfaceActionRequest) (string, []string, error) {
	if req.Preview {
		child := req
		child.Preview = false
		previewH, _ := h.previewHandler()
		return previewH.AddInterface(ctx, child)
	}
	action := domain.Action{
		Action:   domain.ActionTypeAddStruct,
		FilePath: req.FilePath,
		Content:  req.Content,
	}
	_, addedImports, err := h.executeAction(ctx, action, false)
	if err != nil {
		return "", nil, err
	}

	ifaceName := extractTypeName(req.Content)

	if req.MockFile != "" && req.MockName != "" {
		mockResult, err := h.MockFromSource(ctx, req.Content, req.MockName, req.MockFile, req.FilePath)
		if err != nil {
			return "", nil, fmt.Errorf("failed to generate mock: %w", err)
		}
		return fmt.Sprintf("Added %s to %s, %s", ifaceName, filepath.Base(req.FilePath), mockResult), addedImports, nil
	}

	return fmt.Sprintf("Added %s to %s", ifaceName, filepath.Base(req.FilePath)), addedImports, nil
}

// UpdateInterface replaces an existing interface type declaration and regenerates its mock.
func (h *ExecutePlanHandler) UpdateInterface(ctx context.Context, req domain.InterfaceActionRequest) (string, []string, error) {
	if req.Preview {
		child := req
		child.Preview = false
		previewH, _ := h.previewHandler()
		return previewH.UpdateInterface(ctx, child)
	}
	// Doc-only updates (content omitted): reuse the existing declaration
	// text so the doc splice cannot erase the interface itself.
	if req.Content == "" {
		src, rerr := h.fs.ReadFile(ctx, req.FilePath)
		if rerr != nil {
			return "", nil, &domain.Error{Code: "READ_ERROR", Message: "failed to read file", Err: rerr}
		}
		fset := token.NewFileSet()
		file, perr := parser.ParseFile(fset, req.FilePath, src, parser.ParseComments)
		if perr != nil {
			return "", nil, &domain.Error{Code: "PARSE_ERROR", Message: "failed to parse file", Err: perr}
		}
		offsets, found := findStructOffsets(fset, file, req.Identifier)
		if !found {
			return "", nil, &domain.Error{Code: "NODE_NOT_FOUND", Message: fmt.Sprintf("interface %q not found in %s", req.Identifier, req.FilePath)}
		}
		req.Content = string(src[offsets.NodeStart:offsets.End])
	}

	action := domain.Action{
		Action:     domain.ActionTypeUpdateStruct,
		FilePath:   req.FilePath,
		Identifier: req.Identifier,
		Content:    req.Content,
		Doc:        req.Doc,
		StripDoc:   req.StripDoc,
	}
	warnings, addedImports, err := h.executeAction(ctx, action, false)
	if err != nil {
		return "", nil, err
	}

	var fallback bool
	for _, w := range warnings {
		if strings.Contains(w, "not found in") {
			fallback = true
			break
		}
	}

	var msg string
	if fallback {
		extractedName := extractTypeName(req.Content)
		if extractedName == "interface" {
			extractedName = "new declaration"
		}
		msg = fmt.Sprintf("SUCCESS: Added %s to %s (NOTE: '--id %s' not found, content was appended as a new declaration)", extractedName, filepath.Base(req.FilePath), req.Identifier)
	} else {
		msg = fmt.Sprintf("SUCCESS: Updated %s in %s", req.Identifier, filepath.Base(req.FilePath))
	}

	if req.MockFile != "" && req.MockName != "" {
		mockResult, err := h.MockFromSource(ctx, req.Content, req.MockName, req.MockFile, req.FilePath)
		if err != nil {
			return "", nil, fmt.Errorf("failed to regenerate mock: %w", err)
		}
		msg += ", regenerated " + mockResult
	}

	for _, w := range warnings {
		msg += fmt.Sprintf("\nWARNING (update-interface): %s", w)
	}

	return msg, addedImports, nil
}

// DeleteInterface removes an interface type declaration from a file. The mock is NOT auto-deleted unless req.DeleteMock is true.
// DeleteInterface removes an interface type declaration from a file. When
// req.DeleteMock is true and MockFile/MockName are provided, also removes the
// mock struct, its methods, and its compile-time interface assertion from
// MockFile — but leaves the file itself in place (even if empty) so other
// mocks that might share the file are not disturbed.
func (h *ExecutePlanHandler) DeleteInterface(ctx context.Context, req domain.InterfaceActionRequest) (string, []string, error) {
	if req.Preview {
		child := req
		child.Preview = false
		previewH, _ := h.previewHandler()
		return previewH.DeleteInterface(ctx, child)
	}
	action := domain.Action{
		Action:     domain.ActionTypeDeleteStruct,
		FilePath:   req.FilePath,
		Identifier: req.Identifier,
	}
	if _, _, err := h.executeAction(ctx, action, false); err != nil {
		return "", nil, err
	}

	msg := fmt.Sprintf("SUCCESS: Deleted %s from %s", req.Identifier, filepath.Base(req.FilePath))

	if req.DeleteMock {
		if req.MockFile == "" || req.MockName == "" {
			return "", nil, &domain.Error{
				Code:    "INVALID_ARGUMENT",
				Message: "delete_mock requires both mock_file and mock_name",
			}
		}
		mockMsg, err := h.deleteMock(ctx, req.MockFile, req.MockName)
		if err != nil {
			// Interface was already deleted; report the partial success plus the mock error.
			return "", nil, fmt.Errorf("%s, but mock deletion failed: %w", msg, err)
		}
		msg += ", " + mockMsg
	}

	return msg, nil, nil
}

// extractTypeName extracts the type name from a Go type declaration source string.
// Returns "interface" as fallback if parsing fails.
func extractTypeName(src string) string {
	wrapped := "package p\n\n" + src
	// Simple string parsing: find "type <Name> "
	for _, line := range strings.Split(wrapped, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "type ") {
			parts := strings.Fields(line)
			if len(parts) >= 2 {
				return parts[1]
			}
		}
	}
	return "interface"
}

// deleteMock removes the mock struct (with all its methods) and the
// compile-time interface assertion (var _ Iface = (*MockName)(nil)) from the
// given mockFile. It is idempotent: a missing file or missing struct returns
// a warning message rather than an error.
func (h *ExecutePlanHandler) deleteMock(ctx context.Context, mockFile, mockName string) (string, error) {
	receiverName := strings.TrimPrefix(mockName, "*")

	src, err := h.fs.ReadFile(ctx, mockFile)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Sprintf("mock file %s not found (skipped)", filepath.Base(mockFile)), nil
		}
		return "", &domain.Error{Code: "READ_ERROR", Message: "failed to read mock file", Err: err}
	}

	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, mockFile, src, parser.ParseComments)
	if err != nil {
		return "", &domain.Error{Code: "PARSE_ERROR", Message: "failed to parse mock file", Err: err}
	}

	var ranges [][2]int

	// 1. The mock struct itself plus all methods on *MockName / MockName.
	structRanges := findStructAndMethodsOffsets(fset, f, receiverName)
	ranges = append(ranges, structRanges...)

	// 2. The compile-time assertion: `var _ Foo = (*MockName)(nil)` (any form
	//    that references receiverName in a type-assertion-like pattern).
	for _, decl := range f.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.VAR {
			continue
		}
		// An assertion decl has one spec with name "_" and a value expression
		// mentioning the mock receiver type.
		if !isMockAssertion(gen, receiverName) {
			continue
		}
		start := fset.Position(gen.Pos()).Offset
		end := fset.Position(gen.End()).Offset
		ranges = append(ranges, [2]int{start, end})
	}

	if len(structRanges) == 0 {
		return fmt.Sprintf("mock struct %s not found in %s (skipped)", receiverName, filepath.Base(mockFile)), nil
	}

	updated := deleteRanges(src, ranges)
	if _, err := h.fs.WriteFile(ctx, mockFile, updated); err != nil {
		return "", &domain.Error{Code: "WRITE_ERROR", Message: "failed to write mock file", Err: err}
	}

	return fmt.Sprintf("removed mock %s from %s", receiverName, filepath.Base(mockFile)), nil
}

// isMockAssertion returns true when gen is a `var _ Something = (*Receiver)(nil)`
// form where the receiver name matches receiverName.
func isMockAssertion(gen *ast.GenDecl, receiverName string) bool {
	for _, spec := range gen.Specs {
		vs, ok := spec.(*ast.ValueSpec)
		if !ok {
			continue
		}
		// Name must be exactly "_".
		if len(vs.Names) != 1 || vs.Names[0].Name != "_" {
			continue
		}
		if len(vs.Values) != 1 {
			continue
		}
		// Value is (*Receiver)(nil). The outer shape is ast.CallExpr with one
		// arg (nil) whose Fun is ast.ParenExpr wrapping ast.StarExpr -> Ident.
		call, ok := vs.Values[0].(*ast.CallExpr)
		if !ok || len(call.Args) != 1 {
			continue
		}
		paren, ok := call.Fun.(*ast.ParenExpr)
		if !ok {
			continue
		}
		star, ok := paren.X.(*ast.StarExpr)
		if !ok {
			continue
		}
		id, ok := star.X.(*ast.Ident)
		if !ok {
			continue
		}
		if id.Name == receiverName {
			return true
		}
	}
	return false
}
