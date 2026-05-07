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

	"github.com/JLugagne/go-surgeon/internal/surgeon/app/loader"
	"github.com/JLugagne/go-surgeon/internal/surgeon/domain"
	"github.com/JLugagne/go-surgeon/internal/surgeon/domain/repositories/filesystem"
	"github.com/JLugagne/go-surgeon/internal/surgeon/domain/service"
)

// ExecutePlanHandler handles the execution of a surgery plan.
// ExecutePlanHandler handles the execution of a surgery plan.
type ExecutePlanHandler struct {
	fs         filesystem.FileSystem
	ifaceCache *ifaceLRU
	loader     *loader.Loader
}

// NewExecutePlanHandler creates a new ExecutePlanHandler.
func NewExecutePlanHandler(fs filesystem.FileSystem) *ExecutePlanHandler {
	return &ExecutePlanHandler{
		fs:         fs,
		ifaceCache: newIfaceLRU(50),
		loader:     loader.New(),
	}
}

// WithLoader lets callers share a package loader cache across handlers
// (e.g. wire the same *loader.Loader into both queries and commands so
// a find_references followed by a rename_symbol hits the cache on the
// second call). Returns h for chaining.
func (h *ExecutePlanHandler) WithLoader(l *loader.Loader) *ExecutePlanHandler {
	if l != nil {
		h.loader = l
	}
	return h
}

// Loader exposes the cached packages loader so other handlers can share it.
func (h *ExecutePlanHandler) Loader() *loader.Loader {
	return h.loader
}

func (h *ExecutePlanHandler) Handle(ctx context.Context, plan domain.Plan) (domain.PlanResult, error) {
	if len(plan.Actions) == 0 {
		return domain.PlanResult{}, domain.ErrEmptyPlan
	}

	// Preview=true funnels the exact same write path through an in-memory
	// filesystem, so we get a unified diff but never touch disk. Using the
	// real handler code guarantees the preview matches what a subsequent
	// non-preview run would produce.
	if plan.Preview {
		previewH, dry := h.previewHandler()
		childPlan := plan
		childPlan.Preview = false
		res, err := previewH.Handle(ctx, childPlan)
		if err != nil {
			return domain.PlanResult{}, err
		}
		diff, diffErr := dry.Diff(ctx)
		if diffErr != nil {
			return domain.PlanResult{}, diffErr
		}
		res.Files = dry.WrittenFiles()
		res.FilesModified = len(res.Files)
		res.Preview = true
		res.Diff = diff
		// Added imports are not exercised on the preview path (preview FS
		// does not run goimports), so blank the field to avoid implying a
		// guarantee we are not making.
		res.AddedImports = nil
		return res, nil
	}

	modifiedFiles := make(map[string]bool)
	var warnings []string
	var addedImports []string
	seenImports := make(map[string]bool)

	for _, action := range plan.Actions {
		w, imps, err := h.executeAction(ctx, action, plan.Preview)
		if err != nil {
			return domain.PlanResult{}, err
		}
		warnings = append(warnings, w...)
		for _, imp := range imps {
			if !seenImports[imp] {
				addedImports = append(addedImports, imp)
				seenImports[imp] = true
			}
		}
		modifiedFiles[action.FilePath] = true
	}

	files := make([]string, 0, len(modifiedFiles))
	for f := range modifiedFiles {
		files = append(files, f)
	}
	sort.Strings(files)
	return domain.PlanResult{FilesModified: len(modifiedFiles), Files: files, Warnings: warnings, AddedImports: addedImports}, nil
}

// ExecutePlan implements the SurgeonCommands interface.
func (h *ExecutePlanHandler) ExecutePlan(ctx context.Context, plan domain.Plan) (domain.PlanResult, error) {
	return h.Handle(ctx, plan)
}

func (h *ExecutePlanHandler) executeAction(ctx context.Context, action domain.Action, preview bool) ([]string, []string, error) {
	switch action.Action {
	case domain.ActionTypeCreateFile:
		imps, err := h.handleCreateFile(ctx, action)
		return nil, imps, err
	case domain.ActionTypeReplaceFile:
		imps, err := h.handleReplaceFile(ctx, action)
		return nil, imps, err
	case domain.ActionTypeUpdateFunc, domain.ActionTypeAddFunc, domain.ActionTypeUpdateStruct, domain.ActionTypeAddStruct, domain.ActionTypeDeleteFunc, domain.ActionTypeDeleteStruct, domain.ActionTypeInsertCall:
		return h.handleASTAction(ctx, action)
	case domain.ActionTypeAddInterface:
		req := domain.InterfaceActionRequest{
			FilePath: action.FilePath,
			Content:  action.Content,
			MockFile: action.MockFile,
			MockName: action.MockName,
		}
		_, imps, err := h.AddInterface(ctx, req)
		return nil, imps, err
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
		_, imps, err := h.UpdateInterface(ctx, req)
		return nil, imps, err
	case domain.ActionTypeDeleteInterface:
		req := domain.InterfaceActionRequest{
			FilePath:   action.FilePath,
			Identifier: action.Identifier,
		}
		_, imps, err := h.DeleteInterface(ctx, req)
		return nil, imps, err
	case domain.ActionTypePatchFunction:
		res, err := h.PatchFunction(ctx, domain.PatchFunctionRequest{
			FilePath:   action.FilePath,
			Identifier: action.Identifier,
			Patches:    action.PatchFunctionOps,
			Preview:    preview,
		})
		if err != nil {
			return nil, nil, err
		}
		return res.Warnings, res.AddedImports, nil
	case domain.ActionTypePatchStruct:
		res, err := h.PatchStruct(ctx, domain.PatchStructRequest{
			FilePath:   action.FilePath,
			Identifier: action.Identifier,
			Patches:    action.PatchStructOps,
			Preview:    preview,
		})
		if err != nil {
			return nil, nil, err
		}
		return res.Warnings, res.AddedImports, nil
	case domain.ActionTypePatchInterface:
		res, err := h.PatchInterface(ctx, domain.PatchInterfaceRequest{
			FilePath:   action.FilePath,
			Identifier: action.Identifier,
			Patches:    action.PatchInterfaceOps,
			Preview:    preview,
			MockFile:   action.MockFile,
			MockName:   action.MockName,
		})
		if err != nil {
			return nil, nil, err
		}
		return res.Warnings, res.AddedImports, nil
	case domain.ActionTypePatchFile:
		res, err := h.PatchFile(ctx, domain.PatchFileRequest{
			FilePath: action.FilePath,
			Patches:  action.PatchFileOps,
			Preview:  preview,
		})
		if err != nil {
			return nil, nil, err
		}
		return res.Warnings, res.AddedImports, nil
	case domain.ActionTypePatchDecl:
		res, err := h.PatchDecl(ctx, domain.PatchDeclRequest{
			FilePath:   action.FilePath,
			Identifier: action.Identifier,
			Patches:    action.PatchDeclOps,
			Preview:    preview,
		})
		if err != nil {
			return nil, nil, err
		}
		return res.Warnings, res.AddedImports, nil
	case domain.ActionTypeDeleteFile:
		return nil, nil, h.fs.DeleteFile(ctx, action.FilePath)
	default:
		return nil, nil, fmt.Errorf("invalid action type: %s", action.Action)
	}
}

func (h *ExecutePlanHandler) handleCreateFile(ctx context.Context, action domain.Action) ([]string, error) {
	if _, err := h.fs.ReadFile(ctx, action.FilePath); err == nil {
		return nil, domain.ErrFileAlreadyExists
	}
	dir := filepath.Dir(action.FilePath)
	if err := h.fs.MkdirAll(ctx, dir); err != nil {
		return nil, &domain.Error{Code: "INTERNAL_ERROR", Message: "failed to create directory", Err: err}
	}
	pkgName := action.PackagePath
	if pkgName == "" {
		pkgName = h.inferPackageName(ctx, dir)
	}
	if pkgName == "" {
		pkgName = filepath.Base(dir)
	}
	content := ensurePackageHeader(action.Content, pkgName)
	return h.fs.WriteFile(ctx, action.FilePath, []byte(content))
}

func (h *ExecutePlanHandler) handleReplaceFile(ctx context.Context, action domain.Action) ([]string, error) {
	existing, err := h.fs.ReadFile(ctx, action.FilePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, domain.ErrFileNotFound
		}
		return nil, &domain.Error{Code: "INTERNAL_ERROR", Message: "failed to read file", Err: err}
	}
	pkgName := extractPackageName(existing)
	if pkgName == "" {
		// File may be corrupted; fall back to sibling files then dir name.
		pkgName = h.inferPackageName(ctx, filepath.Dir(action.FilePath))
	}
	if pkgName == "" {
		pkgName = filepath.Base(filepath.Dir(action.FilePath))
	}
	if pkgName == "" {
		return nil, &domain.Error{Code: "PARSE_ERROR", Message: fmt.Sprintf("failed to determine package name from existing file %s", action.FilePath)}
	}
	content := ensurePackageHeader(action.Content, pkgName)
	return h.fs.WriteFile(ctx, action.FilePath, []byte(content))
}

func (h *ExecutePlanHandler) handleASTAction(ctx context.Context, action domain.Action) ([]string, []string, error) {
	fset := token.NewFileSet()

	src, err := h.fs.ReadFile(ctx, action.FilePath)
	isFileNew := false
	if err != nil {
		if os.IsNotExist(err) {
			if action.Action != domain.ActionTypeAddFunc && action.Action != domain.ActionTypeAddStruct {
				return nil, nil, domain.ErrFileNotFound
			}
			isFileNew = true
			pkgName := action.PackagePath
			if pkgName == "" {
				pkgName = h.inferPackageName(ctx, filepath.Dir(action.FilePath))
			}
			if pkgName == "" {
				pkgName = filepath.Base(filepath.Dir(action.FilePath))
			}
			src = []byte(fmt.Sprintf("package %s\n", pkgName))
		} else {
			return nil, nil, &domain.Error{Code: "INTERNAL_ERROR", Message: "failed to read file", Err: err}
		}
	}

	f, err := parser.ParseFile(fset, action.FilePath, src, parser.ParseComments)
	if err != nil {
		return nil, nil, &domain.Error{Code: "PARSE_ERROR", Message: "failed to parse file", Err: err}
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
			// Before falling back to add_func, check whether the identifier is an
			// interface — update_func cannot update interfaces.
			if _, isIface := findInterfaceOffset(fset, f, action.Identifier); isIface {
				return nil, nil, &domain.Error{
					Code:    "WRONG_OBJECT_TYPE",
					Message: fmt.Sprintf("%q is an interface in %s; for direct edits use the 'interface' tool with action=update, or inside execute_plan use action='update_interface'. update object='func' cannot update interfaces.", action.Identifier, action.FilePath),
				}
			}
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
					return nil, nil, &domain.Error{
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
		normalizedContent := normalizeStructContent(action.Content)
		if !isFileNew {
			if structName, parseErr := extractStructNameFromContent(normalizedContent); parseErr == nil && structName != "" {
				if offsets, ok := findStructOffsets(fset, f, structName); ok {
					existingBody := strings.TrimSpace(string(src[offsets.DocStart:offsets.End]))
					return nil, nil, &domain.Error{
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
		updatedSrc = append(updatedSrc, []byte("\n"+normalizedContent+"\n")...)
		updated = true
	case domain.ActionTypeUpdateStruct:
		normalizedContent := normalizeStructContent(action.Content)
		offsets, ok := findStructOffsets(fset, f, action.Identifier)
		if ok {
			start, replacement := resolveDocReplacement(offsets, normalizedContent, action.Doc, action.StripDoc)
			updatedSrc = append([]byte(nil), src[:start]...)
			updatedSrc = append(updatedSrc, []byte(replacement)...)
			updatedSrc = append(updatedSrc, src[offsets.End:]...)
			updated = true
		} else {
			// Fall back to add_struct behavior
			content := normalizedContent
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
	case domain.ActionTypeInsertCall:
		result, warn, err := insertCallIntoFunc(fset, f, src, action.Identifier, action.Content, action.Position)
		if err != nil {
			return nil, nil, err
		}
		if warn != "" {
			warnings = append(warnings, warn)
			updated = true   // no-op write still counts as "handled"
			updatedSrc = src // unchanged
		} else {
			updatedSrc = result
			updated = true
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
			return nil, nil, domain.ErrNodeNotFound
		}
		return nil, nil, &domain.Error{Code: "INTERNAL_ERROR", Message: "failed to apply AST action"}
	}

	if isFileNew {
		dir := filepath.Dir(action.FilePath)
		if err := h.fs.MkdirAll(ctx, dir); err != nil {
			return nil, nil, &domain.Error{Code: "INTERNAL_ERROR", Message: "failed to create directory", Err: err}
		}
	}

	addedImports, err := h.fs.WriteFile(ctx, action.FilePath, updatedSrc)
	if err != nil {
		return nil, nil, err
	}

	// Auto-generate test skeleton when requested (add_func / update_func only).
	if action.WithTest && (action.Action == domain.ActionTypeAddFunc || action.Action == domain.ActionTypeUpdateFunc) {
		identifier := action.Identifier
		if identifier == "" {
			identifier, _ = extractFuncIdentifierFromContent(action.Content)
		}
		if identifier != "" {
			if testFile, testErr := h.GenerateTest(ctx, action.FilePath, identifier); testErr != nil {
				warnings = append(warnings, fmt.Sprintf("with_test: failed to generate test for %s: %v", identifier, testErr))
			} else {
				warnings = append(warnings, fmt.Sprintf("with_test: generated test skeleton in %s", testFile))
			}
		}
	}

	return warnings, addedImports, nil
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

// extractPackageName parses the package name from existing Go source.
// Returns empty string if it cannot be determined.
func extractPackageName(src []byte) string {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "", src, 0)
	if err != nil || f.Name == nil {
		return ""
	}
	return f.Name.Name
}

// ensurePackageHeader prepends "package <name>\n\n" to content if it lacks a package declaration.
// fallback is used when the package name cannot be inferred from content itself.
func ensurePackageHeader(content, fallback string) string {
	// Check if content already has a package declaration.
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "", content, 0)
	if err == nil && f.Name != nil {
		return content
	}
	if fallback == "" {
		return content
	}
	return "package " + fallback + "\n\n" + content
}

// inferPackageName reads existing .go files in dir to determine the package name.
// Returns empty string if no .go files exist yet (new package).
func (h *ExecutePlanHandler) inferPackageName(ctx context.Context, dir string) string {
	entries, err := h.fs.ReadDir(ctx, dir)
	if err != nil {
		return ""
	}
	for _, entry := range entries {
		if !strings.HasSuffix(entry, ".go") || strings.HasSuffix(entry, "_test.go") {
			continue
		}
		src, err := h.fs.ReadFile(ctx, filepath.Join(dir, entry))
		if err != nil {
			continue
		}
		if name := extractPackageName(src); name != "" {
			return name
		}
	}
	return ""
}

// normalizeStructContent ensures the content passed to add_struct / update_struct
// contains the "type" keyword before the identifier, adding it when the LLM omits it.
// It skips leading doc comments before checking, so content like:
//
//	// Foo does something.
//	Foo struct { ... }
//
// becomes:
//
//	// Foo does something.
//	type Foo struct { ... }
func normalizeStructContent(content string) string {
	lines := strings.Split(content, "\n")
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "//") {
			continue // skip blank lines and doc comments
		}
		// This is the first non-comment, non-blank line.
		if !strings.HasPrefix(trimmed, "type ") {
			lines[i] = "type " + trimmed
		}
		break
	}
	return strings.Join(lines, "\n")
}

// findInterfaceOffset returns the byte offsets of a named interface type declaration.
func findInterfaceOffset(fset *token.FileSet, f *ast.File, name string) (nodeOffsets, bool) {
	_, nameTarget := parseIdentifier(name)
	for _, decl := range f.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.TYPE {
			continue
		}
		for _, spec := range gen.Specs {
			ts, ok := spec.(*ast.TypeSpec)
			if !ok || ts.Name.Name != nameTarget {
				continue
			}
			if _, isIface := ts.Type.(*ast.InterfaceType); !isIface {
				continue
			}
			nodeStart := fset.Position(gen.Pos()).Offset
			docStart := nodeStart
			if gen.Doc != nil {
				docStart = fset.Position(gen.Doc.Pos()).Offset
			}
			return nodeOffsets{
				DocStart:  docStart,
				NodeStart: nodeStart,
				End:       fset.Position(gen.End()).Offset,
				HasDoc:    gen.Doc != nil,
			}, true
		}
	}
	return nodeOffsets{}, false
}

// insertCallIntoFunc inserts a statement into the body of the named function at
// the position specified by pos.
//
// Returns:
//   - (updatedSrc, "", nil)  — insertion performed
//   - (nil, warning, nil)    — call already present (idempotent no-op)
//   - (nil, "", err)         — hard failure (function not found, invalid position)
func insertCallIntoFunc(fset *token.FileSet, f *ast.File, src []byte, identifier, call string, pos domain.InsertPosition) ([]byte, string, error) {
	if pos == "" {
		pos = domain.InsertBeforeReturn
	}

	// Locate the function.
	offsets, ok := findFuncOffsets(fset, f, identifier)
	if !ok {
		return nil, "", &domain.Error{
			Code:    "NODE_NOT_FOUND",
			Message: fmt.Sprintf("function %q not found in file", identifier),
		}
	}

	// Find the FuncDecl to get the body braces positions.
	var targetFn *ast.FuncDecl
	recvTarget, nameTarget := parseIdentifier(identifier)
	for _, decl := range f.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Name.Name != nameTarget {
			continue
		}
		var recvName string
		if fn.Recv != nil {
			recvName = getRecvType(fn.Recv)
		}
		if recvName == recvTarget || (recvName == "" && recvTarget == f.Name.Name) {
			targetFn = fn
			break
		}
	}
	if targetFn == nil || targetFn.Body == nil {
		return nil, "", &domain.Error{
			Code:    "NODE_NOT_FOUND",
			Message: fmt.Sprintf("function %q has no body", identifier),
		}
	}

	bodyStart := fset.Position(targetFn.Body.Lbrace).Offset // offset of '{'
	bodyEnd := fset.Position(targetFn.Body.Rbrace).Offset   // offset of '}'
	bodyContent := string(src[bodyStart+1 : bodyEnd])       // content between braces

	// Idempotency: if the exact call is already present, no-op.
	callTrimmed := strings.TrimSpace(call)
	for _, line := range strings.Split(bodyContent, "\n") {
		if strings.TrimSpace(line) == callTrimmed {
			return nil, fmt.Sprintf("insert_call: %q already present in %s — skipped", callTrimmed, identifier), nil
		}
	}

	// Determine insertion offset within src.
	var insertAt int
	switch {
	case pos == domain.InsertBeforeReturn:
		insertAt = findLastReturnOffset(fset, targetFn, bodyEnd)

	case pos == domain.InsertEndOfBody:
		insertAt = bodyEnd

	case strings.HasPrefix(string(pos), "after:"):
		marker := strings.TrimPrefix(string(pos), "after:")
		idx := strings.Index(bodyContent, marker)
		if idx == -1 {
			return nil, "", &domain.Error{
				Code:    "MARKER_NOT_FOUND",
				Message: fmt.Sprintf("marker %q not found in body of %s", marker, identifier),
			}
		}
		// Auto-lift: if the marker landed inside a nested scope (closure,
		// for-loop, if-branch, switch case), move the insertion to the
		// end of the outermost statement in targetFn.Body that contains
		// the marker. This mirrors patch_function's insert_before/after
		// behavior and prevents silently landing inside a test closure
		// or other nested block.
		fileOff := bodyStart + 1 + idx
		if lt := findLiftTarget(fset, targetFn, fileOff); lt.ShouldLift && lt.TopStmt != nil {
			insertAt = fset.Position(lt.TopStmt.End()).Offset
			// Skip trailing newline so the next line begins after the block.
			if insertAt < len(src) && src[insertAt] == '\n' {
				insertAt++
			}
			break
		}
		// Find end of the line containing the marker.
		lineEnd := strings.Index(bodyContent[idx:], "\n")
		if lineEnd == -1 {
			insertAt = bodyEnd
		} else {
			insertAt = bodyStart + 1 + idx + lineEnd + 1
		}

	default:
		return nil, "", &domain.Error{
			Code:    "INVALID_POSITION",
			Message: fmt.Sprintf("unknown position %q: use before-return, end-of-body, or after:<marker>", pos),
		}
	}

	// Compute indentation from the surrounding body.
	indent := detectBodyIndent(bodyContent)

	// Build the line to insert (trimmed call + newline).
	line := indent + callTrimmed + "\n"

	result := make([]byte, 0, len(src)+len(line))
	result = append(result, src[:insertAt]...)
	result = append(result, []byte(line)...)
	result = append(result, src[insertAt:]...)

	_ = offsets // used via findFuncOffsets above for existence check
	return result, "", nil
}

// findLastReturnOffset returns the byte offset of the last top-level return
// statement inside fn's body, or bodyEnd if there is none.
func findLastReturnOffset(fset *token.FileSet, fn *ast.FuncDecl, bodyEnd int) int {
	var lastReturn token.Pos
	for _, stmt := range fn.Body.List {
		if _, ok := stmt.(*ast.ReturnStmt); ok {
			lastReturn = stmt.Pos()
		}
	}
	if !lastReturn.IsValid() {
		return bodyEnd
	}
	return fset.Position(lastReturn).Offset
}

// detectBodyIndent returns the indentation string used by the first non-empty
// line in bodyContent (the text between the function's braces), defaulting to a tab.
func detectBodyIndent(bodyContent string) string {
	for _, line := range strings.Split(bodyContent, "\n") {
		trimmed := strings.TrimLeft(line, " \t")
		if trimmed == "" {
			continue
		}
		indent := line[:len(line)-len(trimmed)]
		if indent != "" {
			return indent
		}
	}
	return "\t"
}

// withFS returns a shallow clone of the handler whose filesystem is swapped
// for fs. It lets preview paths run the normal handler logic against a
// DryRunFileSystem and then harvest the diff, without leaking the swap
// across concurrent callers of the shared handler instance.
func (h *ExecutePlanHandler) withFS(fs filesystem.FileSystem) *ExecutePlanHandler {
	clone := *h
	clone.fs = fs
	return &clone
}

// previewHandler pairs a shallow handler clone whose filesystem is a
// DryRunFileSystem wrapping h.fs with that same DryRunFileSystem, so
// preview branches can run the normal write-path and then harvest a
// unified diff without touching disk.
// previewHandler pairs a shallow handler clone whose filesystem is a
// previewFS wrapping h.fs with that same previewFS, so preview branches
// can run the normal write-path and then harvest a unified diff without
// touching disk. The previewFS is a tiny commands-local implementation so
// that this package does not depend on the outbound layer.
// previewHandler pairs a shallow handler clone whose filesystem is a
// previewFS wrapping h.fs with that same previewFS, so preview branches
// can run the normal write-path and then harvest a unified diff without
// touching disk. The previewFS is a tiny commands-local implementation so
// that this package does not depend on the outbound layer.
//
// Idempotent: if h.fs is already a previewFS (because the caller went
// through PreviewWith), we keep the same FS instead of stacking another
// layer, so writes continue to land in the outer preview buffer and the
// diff stays complete.
func (h *ExecutePlanHandler) previewHandler() (*ExecutePlanHandler, *previewFS) {
	if existing, ok := h.fs.(*previewFS); ok {
		return h, existing
	}
	dry := newPreviewFS(h.fs)
	return h.withFS(dry), dry
}

// PreviewWith runs fn against a shallow clone of this handler whose file
// system is swapped for an in-memory previewFS, then returns the unified
// diff of every would-be write together with the list of target file
// paths. It is the uniform escape hatch for commands whose legacy return
// types do not carry a Diff field (Implement, Mock, TagStruct, Extract-
// Interface, GenerateTest, AddInterface/UpdateInterface/DeleteInterface).
// The caller keeps running against the original, disk-backed handler —
// only the closure sees the preview FS.
func (h *ExecutePlanHandler) PreviewWith(ctx context.Context, fn func(service.SurgeonCommands) error) (string, []string, error) {
	previewH, dry := h.previewHandler()
	if err := fn(previewH); err != nil {
		return "", nil, err
	}
	diff, err := dry.Diff(ctx)
	if err != nil {
		return "", nil, err
	}
	return diff, dry.WrittenFiles(), nil
}
