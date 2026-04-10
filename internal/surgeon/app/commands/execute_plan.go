package commands

import (
	"context"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/JLugagne/go-surgeon/internal/surgeon/domain"
	"github.com/JLugagne/go-surgeon/internal/surgeon/domain/repositories/filesystem"
)

// ExecutePlanHandler handles the execution of a surgery plan.
type ExecutePlanHandler struct {
	fs filesystem.FileSystem
}

// NewExecutePlanHandler creates a new ExecutePlanHandler.
func NewExecutePlanHandler(fs filesystem.FileSystem) *ExecutePlanHandler {
	return &ExecutePlanHandler{fs: fs}
}

func (h *ExecutePlanHandler) Handle(ctx context.Context, plan domain.Plan) (domain.PlanResult, error) {
	if len(plan.Actions) == 0 {
		return domain.PlanResult{}, domain.ErrEmptyPlan
	}

	modifiedFiles := make(map[string]bool)
	var warnings []string

	for _, action := range plan.Actions {
		w, err := h.executeAction(ctx, action)
		if err != nil {
			return domain.PlanResult{}, err
		}
		warnings = append(warnings, w...)
		modifiedFiles[action.FilePath] = true
	}

	return domain.PlanResult{FilesModified: len(modifiedFiles), Warnings: warnings}, nil
}

// ExecutePlan implements the SurgeonCommands interface.
func (h *ExecutePlanHandler) ExecutePlan(ctx context.Context, plan domain.Plan) (domain.PlanResult, error) {
	return h.Handle(ctx, plan)
}

func (h *ExecutePlanHandler) executeAction(ctx context.Context, action domain.Action) ([]string, error) {
	switch action.Action {
	case domain.ActionTypeCreateFile:
		return nil, h.handleCreateFile(ctx, action)
	case domain.ActionTypeReplaceFile:
		return nil, h.handleReplaceFile(ctx, action)
	case domain.ActionTypeUpdateFunc, domain.ActionTypeAddFunc, domain.ActionTypeUpdateStruct, domain.ActionTypeAddStruct, domain.ActionTypeDeleteFunc, domain.ActionTypeDeleteStruct:
		return h.handleASTAction(ctx, action)
	case domain.ActionTypeAddInterface:
		req := domain.InterfaceActionRequest{
			FilePath: action.FilePath,
			Content:  action.Content,
			MockFile: action.MockFile,
			MockName: action.MockName,
		}
		_, err := h.AddInterface(ctx, req)
		return nil, err
	case domain.ActionTypeUpdateInterface:
		req := domain.InterfaceActionRequest{
			FilePath:   action.FilePath,
			Identifier: action.Identifier,
			Content:    action.Content,
			MockFile:   action.MockFile,
			MockName:   action.MockName,
			Doc:        action.Doc,
			StripDoc:   action.StripDoc,
		}
		_, err := h.UpdateInterface(ctx, req)
		return nil, err
	case domain.ActionTypeDeleteInterface:
		req := domain.InterfaceActionRequest{
			FilePath:   action.FilePath,
			Identifier: action.Identifier,
		}
		_, err := h.DeleteInterface(ctx, req)
		return nil, err
	default:
		return nil, fmt.Errorf("invalid action type: %s", action.Action)
	}
}

func (h *ExecutePlanHandler) handleCreateFile(ctx context.Context, action domain.Action) error {
	if _, err := h.fs.ReadFile(ctx, action.FilePath); err == nil {
		return domain.ErrFileAlreadyExists
	}
	dir := filepath.Dir(action.FilePath)
	if err := h.fs.MkdirAll(ctx, dir); err != nil {
		return &domain.Error{Code: "INTERNAL_ERROR", Message: "failed to create directory", Err: err}
	}
	return h.fs.WriteFile(ctx, action.FilePath, []byte(action.Content))
}

func (h *ExecutePlanHandler) handleReplaceFile(ctx context.Context, action domain.Action) error {
	if _, err := h.fs.ReadFile(ctx, action.FilePath); err != nil {
		if os.IsNotExist(err) {
			return domain.ErrFileNotFound
		}
		return &domain.Error{Code: "INTERNAL_ERROR", Message: "failed to read file", Err: err}
	}
	return h.fs.WriteFile(ctx, action.FilePath, []byte(action.Content))
}

func (h *ExecutePlanHandler) handleASTAction(ctx context.Context, action domain.Action) ([]string, error) {
	fset := token.NewFileSet()

	src, err := h.fs.ReadFile(ctx, action.FilePath)
	isFileNew := false
	if err != nil {
		if os.IsNotExist(err) {
			if action.Action != domain.ActionTypeAddFunc && action.Action != domain.ActionTypeAddStruct {
				return nil, domain.ErrFileNotFound
			}
			isFileNew = true
			src = []byte(fmt.Sprintf("package %s\n", action.PackagePath))
		} else {
			return nil, &domain.Error{Code: "INTERNAL_ERROR", Message: "failed to read file", Err: err}
		}
	}

	f, err := parser.ParseFile(fset, action.FilePath, src, parser.ParseComments)
	if err != nil {
		return nil, &domain.Error{Code: "PARSE_ERROR", Message: "failed to parse file", Err: err}
	}

	var updated bool
	var updatedSrc []byte
	var warnings []string

	switch action.Action {
	case domain.ActionTypeUpdateFunc:
		offsets, ok := findFuncOffsets(fset, f, action.Identifier)
		if ok {
			start, replacement := resolveDocReplacement(offsets, action.Content, action.Doc, action.StripDoc)
			updatedSrc = append([]byte(nil), src[:start]...)
			updatedSrc = append(updatedSrc, []byte(replacement)...)
			updatedSrc = append(updatedSrc, src[offsets.End:]...)
			updated = true
		} else {
			// Fall back to add_func behavior
			content := action.Content
			if action.Doc != "" {
				content = formatDocComment(action.Doc) + "\n" + content
			}
			updatedSrc = append([]byte(nil), src...)
			if len(updatedSrc) > 0 && updatedSrc[len(updatedSrc)-1] != '\n' {
				updatedSrc = append(updatedSrc, '\n')
			}
			updatedSrc = append(updatedSrc, []byte("\n"+content+"\n")...)
			updated = true
			warnings = append(warnings, fmt.Sprintf("update_func: identifier %q not found in %s, treated as add_func", action.Identifier, action.FilePath))
		}
	case domain.ActionTypeAddFunc:
		if !isFileNew {
			if funcID, parseErr := extractFuncIdentifierFromContent(action.Content); parseErr == nil && funcID != "" {
				if offsets, ok := findFuncOffsets(fset, f, funcID); ok {
					existingBody := strings.TrimSpace(string(src[offsets.DocStart:offsets.End]))
					return nil, &domain.Error{
						Code:    "NODE_ALREADY_EXISTS",
						Message: fmt.Sprintf("function %q already declared in %s:\n\n%s", funcID, action.FilePath, existingBody),
					}
				}
			}
		}
		updatedSrc = append([]byte(nil), src...)
		if len(updatedSrc) > 0 && updatedSrc[len(updatedSrc)-1] != '\n' {
			updatedSrc = append(updatedSrc, '\n')
		}
		updatedSrc = append(updatedSrc, []byte("\n"+action.Content+"\n")...)
		updated = true
	case domain.ActionTypeAddStruct:
		if !isFileNew {
			if structName, parseErr := extractStructNameFromContent(action.Content); parseErr == nil && structName != "" {
				if offsets, ok := findStructOffsets(fset, f, structName); ok {
					existingBody := strings.TrimSpace(string(src[offsets.DocStart:offsets.End]))
					return nil, &domain.Error{
						Code:    "NODE_ALREADY_EXISTS",
						Message: fmt.Sprintf("struct %q already declared in %s:\n\n%s", structName, action.FilePath, existingBody),
					}
				}
			}
		}
		updatedSrc = append([]byte(nil), src...)
		if len(updatedSrc) > 0 && updatedSrc[len(updatedSrc)-1] != '\n' {
			updatedSrc = append(updatedSrc, '\n')
		}
		updatedSrc = append(updatedSrc, []byte("\n"+action.Content+"\n")...)
		updated = true
	case domain.ActionTypeUpdateStruct:
		offsets, ok := findStructOffsets(fset, f, action.Identifier)
		if ok {
			start, replacement := resolveDocReplacement(offsets, action.Content, action.Doc, action.StripDoc)
			updatedSrc = append([]byte(nil), src[:start]...)
			updatedSrc = append(updatedSrc, []byte(replacement)...)
			updatedSrc = append(updatedSrc, src[offsets.End:]...)
			updated = true
		} else {
			// Fall back to add_struct behavior
			content := action.Content
			if action.Doc != "" {
				content = formatDocComment(action.Doc) + "\n" + content
			}
			updatedSrc = append([]byte(nil), src...)
			if len(updatedSrc) > 0 && updatedSrc[len(updatedSrc)-1] != '\n' {
				updatedSrc = append(updatedSrc, '\n')
			}
			updatedSrc = append(updatedSrc, []byte("\n"+content+"\n")...)
			updated = true
			warnings = append(warnings, fmt.Sprintf("update_struct: identifier %q not found in %s, treated as add_struct", action.Identifier, action.FilePath))
		}
	case domain.ActionTypeDeleteFunc:
		offsets, ok := findFuncOffsets(fset, f, action.Identifier)
		if ok {
			updatedSrc = append([]byte(nil), src[:offsets.DocStart]...)
			updatedSrc = append(updatedSrc, src[offsets.End:]...)
			updated = true
		}
	case domain.ActionTypeDeleteStruct:
		// Delete struct and its methods
		ranges := findStructAndMethodsOffsets(fset, f, action.Identifier)
		if len(ranges) > 0 {
			updatedSrc = deleteRanges(src, ranges)
			updated = true
		}
	}

	if !updated {
		if action.Action == domain.ActionTypeDeleteFunc || action.Action == domain.ActionTypeDeleteStruct {
			return nil, domain.ErrNodeNotFound
		}
		return nil, &domain.Error{Code: "INTERNAL_ERROR", Message: "failed to apply AST action"}
	}

	if isFileNew {
		dir := filepath.Dir(action.FilePath)
		if err := h.fs.MkdirAll(ctx, dir); err != nil {
			return nil, &domain.Error{Code: "INTERNAL_ERROR", Message: "failed to create directory", Err: err}
		}
	}

	return warnings, h.fs.WriteFile(ctx, action.FilePath, updatedSrc)
}

func findFuncOffsets(fset *token.FileSet, f *ast.File, identifier string) (nodeOffsets, bool) {
	recvTarget, nameTarget := parseIdentifier(identifier)

	for _, decl := range f.Decls {
		if fn, ok := decl.(*ast.FuncDecl); ok && fn.Name.Name == nameTarget {
			var recvName string
			if fn.Recv != nil {
				recvName = getRecvType(fn.Recv)
			}

			// Match if receiver matches, or if recvTarget is the package name and it's a global function
			if recvName == recvTarget || (recvName == "" && recvTarget == f.Name.Name) {
				nodeStart := fset.Position(fn.Pos()).Offset
				docStart := nodeStart
				hasDoc := fn.Doc != nil
				if hasDoc {
					docStart = fset.Position(fn.Doc.Pos()).Offset
				}
				return nodeOffsets{
					DocStart:  docStart,
					NodeStart: nodeStart,
					End:       fset.Position(fn.End()).Offset,
					HasDoc:    hasDoc,
				}, true
			}
		}
	}
	return nodeOffsets{}, false
}

func getRecvType(recv *ast.FieldList) string {
	if recv == nil || len(recv.List) == 0 {
		return ""
	}
	switch t := recv.List[0].Type.(type) {
	case *ast.Ident:
		return t.Name
	case *ast.StarExpr:
		if ident, ok := t.X.(*ast.Ident); ok {
			return ident.Name
		}
	}
	return ""
}

func parseIdentifier(id string) (string, string) {
	parts := strings.Split(id, ".")
	if len(parts) == 1 {
		return "", id
	}

	if len(parts) == 3 {
		// pkg.Receiver.Method
		receiver := strings.Trim(parts[1], "()*")
		return receiver, parts[2]
	}

	// Two parts: could be pkg.Func or Receiver.Method
	// We'll treat the first part as receiver. If it's a package name,
	// the caller (findFuncOffsets) might need to handle the fallback.
	// But usually, receivers are what we want in a single file.
	receiver := strings.Trim(parts[0], "()*")
	return receiver, parts[1]
}

func findStructOffsets(fset *token.FileSet, f *ast.File, identifier string) (nodeOffsets, bool) {
	pkgTarget, nameTarget := parseIdentifier(identifier)
	if pkgTarget != "" && pkgTarget != f.Name.Name {
		return nodeOffsets{}, false
	}

	for _, decl := range f.Decls {
		if gen, ok := decl.(*ast.GenDecl); ok && gen.Tok == token.TYPE {
			for _, spec := range gen.Specs {
				if typeSpec, ok := spec.(*ast.TypeSpec); ok && typeSpec.Name.Name == nameTarget {
					var nodeStart, docStart int
					var endPos token.Pos
					var hasDoc bool

					if len(gen.Specs) == 1 {
						nodeStart = fset.Position(gen.Pos()).Offset
						docStart = nodeStart
						if gen.Doc != nil {
							hasDoc = true
							docStart = fset.Position(gen.Doc.Pos()).Offset
						}
						endPos = gen.End()
					} else {
						nodeStart = fset.Position(typeSpec.Pos()).Offset
						docStart = nodeStart
						if typeSpec.Doc != nil {
							hasDoc = true
							docStart = fset.Position(typeSpec.Doc.Pos()).Offset
						}
						endPos = typeSpec.End()
					}

					return nodeOffsets{
						DocStart:  docStart,
						NodeStart: nodeStart,
						End:       fset.Position(endPos).Offset,
						HasDoc:    hasDoc,
					}, true
				}
			}
		}
	}
	return nodeOffsets{}, false
}

func findStructAndMethodsOffsets(fset *token.FileSet, f *ast.File, identifier string) [][2]int {
	var ranges [][2]int
	// Find struct
	if offsets, ok := findStructOffsets(fset, f, identifier); ok {
		ranges = append(ranges, [2]int{offsets.DocStart, offsets.End})
	}

	// Find methods
	for _, decl := range f.Decls {
		if fn, ok := decl.(*ast.FuncDecl); ok && fn.Recv != nil {
			if getRecvType(fn.Recv) == identifier {
				start := fn.Pos()
				if fn.Doc != nil {
					start = fn.Doc.Pos()
				}
				ranges = append(ranges, [2]int{fset.Position(start).Offset, fset.Position(fn.End()).Offset})
			}
		}
	}
	return ranges
}

func deleteRanges(src []byte, ranges [][2]int) []byte {
	// Sort ranges by start position in descending order to avoid offset shifts
	sort.Slice(ranges, func(i, j int) bool {
		return ranges[i][0] > ranges[j][0]
	})

	result := append([]byte(nil), src...)
	for _, r := range ranges {
		result = append(result[:r[0]], result[r[1]:]...)
	}
	return result
}

func extractFuncIdentifierFromContent(content string) (string, error) {
	src := "package p\n" + content
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "", src, 0)
	if err != nil {
		return "", err
	}
	for _, decl := range f.Decls {
		if fn, ok := decl.(*ast.FuncDecl); ok {
			recv := getRecvType(fn.Recv)
			if recv != "" {
				return recv + "." + fn.Name.Name, nil
			}
			return fn.Name.Name, nil
		}
	}
	return "", nil
}

func extractStructNameFromContent(content string) (string, error) {
	src := "package p\n" + content
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "", src, 0)
	if err != nil {
		return "", err
	}
	for _, decl := range f.Decls {
		if gen, ok := decl.(*ast.GenDecl); ok && gen.Tok == token.TYPE {
			for _, spec := range gen.Specs {
				if typeSpec, ok := spec.(*ast.TypeSpec); ok {
					return typeSpec.Name.Name, nil
				}
			}
		}
	}
	return "", nil
}

type nodeOffsets struct {
	DocStart  int
	NodeStart int
	End       int
	HasDoc    bool
}

func formatDocComment(text string) string {
	lines := strings.Split(text, "\n")
	var b strings.Builder
	for _, line := range lines {
		if line == "" {
			b.WriteString("//\n")
		} else {
			b.WriteString("// ")
			b.WriteString(line)
			b.WriteByte('\n')
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

// resolveDocReplacement determines the replacement start offset and content
// based on doc/strip_doc options. Default: preserve existing doc comment.
func resolveDocReplacement(offsets nodeOffsets, content, doc string, stripDoc bool) (int, string) {
	if doc != "" {
		return offsets.DocStart, formatDocComment(doc) + "\n" + content
	}
	if stripDoc {
		return offsets.DocStart, content
	}
	// Default: preserve existing doc by replacing only the node body
	return offsets.NodeStart, content
}
