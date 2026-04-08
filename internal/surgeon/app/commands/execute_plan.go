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

// Handle executes the surgery plan and returns the number of modified files.
func (h *ExecutePlanHandler) Handle(ctx context.Context, plan domain.Plan) (int, error) {
	if len(plan.Actions) > domain.MaxActions {
		return 0, domain.ErrPlanTooLarge
	}
	if len(plan.Actions) == 0 {
		return 0, domain.ErrEmptyPlan
	}

	modifiedFiles := make(map[string]bool)

	for _, action := range plan.Actions {
		if err := h.executeAction(ctx, action); err != nil {
			return 0, err
		}
		modifiedFiles[action.FilePath] = true
	}

	files := make([]string, 0, len(modifiedFiles))
	for f := range modifiedFiles {
		files = append(files, f)
	}

	return len(modifiedFiles), nil
}

// Renaming for consistency with SurgeonCommands interface
func (h *ExecutePlanHandler) ExecutePlan(ctx context.Context, plan domain.Plan) (int, error) {
	return h.Handle(ctx, plan)
}

func (h *ExecutePlanHandler) executeAction(ctx context.Context, action domain.Action) error {
	switch action.Action {
	case domain.ActionTypeCreateFile:
		return h.handleCreateFile(ctx, action)
	case domain.ActionTypeReplaceFile:
		return h.handleReplaceFile(ctx, action)
	case domain.ActionTypeUpdateFunc, domain.ActionTypeAddFunc, domain.ActionTypeUpdateStruct, domain.ActionTypeAddStruct, domain.ActionTypeDeleteFunc, domain.ActionTypeDeleteStruct:
		return h.handleASTAction(ctx, action)
	default:
		// Use ErrInvalidAction from domain if available
		return fmt.Errorf("invalid action type: %s", action.Action)
	}
}

func (h *ExecutePlanHandler) handleCreateFile(ctx context.Context, action domain.Action) error {
	dir := filepath.Dir(action.FilePath)
	if err := h.fs.MkdirAll(ctx, dir); err != nil {
		return &domain.Error{Code: "INTERNAL_ERROR", Message: "failed to create directory", Err: err}
	}
	return h.fs.WriteFile(ctx, action.FilePath, []byte(action.Content))
}

func (h *ExecutePlanHandler) handleReplaceFile(ctx context.Context, action domain.Action) error {
	return h.fs.WriteFile(ctx, action.FilePath, []byte(action.Content))
}

func (h *ExecutePlanHandler) handleASTAction(ctx context.Context, action domain.Action) error {
	fset := token.NewFileSet()

	src, err := h.fs.ReadFile(ctx, action.FilePath)
	isFileNew := false
	if err != nil {
		if os.IsNotExist(err) {
			if action.Action != domain.ActionTypeAddFunc && action.Action != domain.ActionTypeAddStruct {
				return domain.ErrFileNotFound
			}
			isFileNew = true
			src = []byte(fmt.Sprintf("package %s\n", action.PackagePath))
		} else {
			return &domain.Error{Code: "INTERNAL_ERROR", Message: "failed to read file", Err: err}
		}
	}

	f, err := parser.ParseFile(fset, action.FilePath, src, parser.ParseComments)
	if err != nil {
		return &domain.Error{Code: "PARSE_ERROR", Message: "failed to parse file", Err: err}
	}

	var updated bool
	var updatedSrc []byte

	switch action.Action {
	case domain.ActionTypeUpdateFunc:
		start, end, ok := findFuncOffsets(fset, f, action.Identifier)
		if ok {
			updatedSrc = append([]byte(nil), src[:start]...)
			updatedSrc = append(updatedSrc, []byte(action.Content)...)
			updatedSrc = append(updatedSrc, src[end:]...)
			updated = true
		}
	case domain.ActionTypeAddFunc, domain.ActionTypeAddStruct:
		updatedSrc = append([]byte(nil), src...)
		if len(updatedSrc) > 0 && updatedSrc[len(updatedSrc)-1] != '\n' {
			updatedSrc = append(updatedSrc, '\n')
		}
		updatedSrc = append(updatedSrc, []byte("\n"+action.Content+"\n")...)
		updated = true
	case domain.ActionTypeUpdateStruct:
		start, end, ok := findStructOffsets(fset, f, action.Identifier)
		if ok {
			updatedSrc = append([]byte(nil), src[:start]...)
			updatedSrc = append(updatedSrc, []byte(action.Content)...)
			updatedSrc = append(updatedSrc, src[end:]...)
			updated = true
		}
	case domain.ActionTypeDeleteFunc:
		start, end, ok := findFuncOffsets(fset, f, action.Identifier)
		if ok {
			updatedSrc = append([]byte(nil), src[:start]...)
			updatedSrc = append(updatedSrc, src[end:]...)
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
		if action.Action == domain.ActionTypeUpdateFunc || action.Action == domain.ActionTypeUpdateStruct || action.Action == domain.ActionTypeDeleteFunc || action.Action == domain.ActionTypeDeleteStruct {
			return domain.ErrNodeNotFound
		}
		return &domain.Error{Code: "INTERNAL_ERROR", Message: "failed to apply AST action"}
	}

	if isFileNew {
		dir := filepath.Dir(action.FilePath)
		if err := h.fs.MkdirAll(ctx, dir); err != nil {
			return &domain.Error{Code: "INTERNAL_ERROR", Message: "failed to create directory", Err: err}
		}
	}

	return h.fs.WriteFile(ctx, action.FilePath, updatedSrc)
}

func findFuncOffsets(fset *token.FileSet, f *ast.File, identifier string) (int, int, bool) {
	recvTarget, nameTarget := parseIdentifier(identifier)

	for _, decl := range f.Decls {
		if fn, ok := decl.(*ast.FuncDecl); ok && fn.Name.Name == nameTarget {
			var recvName string
			if fn.Recv != nil {
				recvName = getRecvType(fn.Recv)
			}

			if recvName == recvTarget {
				startPos := fn.Pos()
				if fn.Doc != nil {
					startPos = fn.Doc.Pos()
				}
				return fset.Position(startPos).Offset, fset.Position(fn.End()).Offset, true
			}
		}
	}
	return 0, 0, false
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
	// Handle (*Receiver).Method or Receiver.Method
	receiver := strings.Trim(parts[0], "()*")
	return receiver, parts[1]
}

func findStructOffsets(fset *token.FileSet, f *ast.File, identifier string) (int, int, bool) {
	for _, decl := range f.Decls {
		if gen, ok := decl.(*ast.GenDecl); ok && gen.Tok == token.TYPE {
			for _, spec := range gen.Specs {
				if typeSpec, ok := spec.(*ast.TypeSpec); ok && typeSpec.Name.Name == identifier {
					startPos := typeSpec.Pos()
					if typeSpec.Doc != nil {
						startPos = typeSpec.Doc.Pos()
					} else if len(gen.Specs) == 1 && gen.Doc != nil {
						startPos = gen.Doc.Pos()
					}
					endPos := typeSpec.End()

					if len(gen.Specs) == 1 {
						if gen.Doc != nil {
							startPos = gen.Doc.Pos()
						} else {
							startPos = gen.Pos()
						}
						endPos = gen.End()
					}
					return fset.Position(startPos).Offset, fset.Position(endPos).Offset, true
				}
			}
		}
	}
	return 0, 0, false
}

func findStructAndMethodsOffsets(fset *token.FileSet, f *ast.File, identifier string) [][2]int {
	var ranges [][2]int
	// Find struct
	if s, e, ok := findStructOffsets(fset, f, identifier); ok {
		ranges = append(ranges, [2]int{s, e})
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
