package queries

import (
	"bytes"
	"context"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/JLugagne/go-surgeon/internal/surgeon/app/loader"
	"github.com/JLugagne/go-surgeon/internal/surgeon/domain"
	"github.com/JLugagne/go-surgeon/internal/surgeon/domain/repositories/filesystem"
)

type SurgeonQueriesHandler struct {
	fs       filesystem.FileSystem
	resolver *moduleResolver
	loader   *loader.Loader
}

func NewSurgeonQueriesHandler(fs filesystem.FileSystem) *SurgeonQueriesHandler {
	return &SurgeonQueriesHandler{fs: fs, resolver: newModuleResolver(), loader: loader.New()}
}

// WithLoader lets callers share a package loader cache across handlers
// (e.g. wire the same *loader.Loader into both queries and commands so
// a find_references followed by a rename_symbol hits the cache on the
// second call). Returns h for chaining.
func (h *SurgeonQueriesHandler) WithLoader(l *loader.Loader) *SurgeonQueriesHandler {
	if l != nil {
		h.loader = l
	}
	return h
}

// Loader exposes the cached packages loader so other handlers can share it.
func (h *SurgeonQueriesHandler) Loader() *loader.Loader {
	return h.loader
}

func (h *SurgeonQueriesHandler) FindSymbols(ctx context.Context, query domain.SymbolQuery, targetDir string) ([]domain.SymbolResult, error) {
	if query.AtLine > 0 {
		result, err := h.findSymbolAtLine(ctx, query)
		if err != nil {
			return nil, err
		}
		if result == nil {
			return nil, nil
		}
		return []domain.SymbolResult{*result}, nil
	}
	var moduleDir string
	if query.Module != "" {
		info, err := h.resolver.Resolve(ctx, query.Module)
		if err != nil {
			return nil, err
		}
		moduleDir = info.Dir
		if targetDir == "" || targetDir == "." {
			targetDir = info.Dir
		} else if !filepath.IsAbs(targetDir) {
			targetDir = filepath.Join(info.Dir, targetDir)
		} else {
			return nil, fmt.Errorf("dir must be a relative path when module is set; got %q", targetDir)
		}
	}

	var results []domain.SymbolResult
	var nameRE *regexp.Regexp
	if query.Pattern != "" {
		re, err := regexp.Compile(query.Pattern)
		if err != nil {
			return nil, fmt.Errorf("invalid pattern %q: %w", query.Pattern, err)
		}
		nameRE = re
	}

	err := filepath.Walk(targetDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // skip errors
		}
		if info.IsDir() {
			if info.Name() == "vendor" || (strings.HasPrefix(info.Name(), ".") && path != ".") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		if strings.HasSuffix(path, "_test.go") && !query.Tests {
			return nil
		}

		src, err := h.fs.ReadFile(ctx, path)
		if err != nil {
			return nil
		}

		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, path, src, parser.ParseComments)
		if err != nil {
			return nil
		}

		var outline []domain.OutlineEntry
		if query.Context == "file" {
			outline = buildFileOutline(fset, src, f)
		}
		if query.PackageName != "" && f.Name.Name != query.PackageName {
			return nil
		}

		for _, decl := range f.Decls {
			if fn, ok := decl.(*ast.FuncDecl); ok {
				var recvName string
				if fn.Recv != nil {
					recvName = getRecvType(fn.Recv)
				}

				nameMatches := nameRE != nil && nameRE.MatchString(fn.Name.Name) || nameRE == nil && fn.Name.Name == query.Name
				if nameMatches && (query.Receiver == "" || query.Receiver == recvName) {
					results = append(results, h.extractFuncResult(fset, src, f, fn, path, recvName, outline))
				}
			} else if gen, ok := decl.(*ast.GenDecl); ok && gen.Tok == token.TYPE && query.Receiver == "" {
				for _, spec := range gen.Specs {
					tsMatches := func(n string) bool {
						if nameRE != nil {
							return nameRE.MatchString(n)
						}
						return n == query.Name
					}
					if typeSpec, ok := spec.(*ast.TypeSpec); ok && tsMatches(typeSpec.Name.Name) {
						results = append(results, h.extractStructResult(fset, src, f, gen, typeSpec, path, outline))
					}
				}
			} else if gen, ok := decl.(*ast.GenDecl); ok && (gen.Tok == token.VAR || gen.Tok == token.CONST) && query.Receiver == "" {
				for _, spec := range gen.Specs {
					vs, ok := spec.(*ast.ValueSpec)
					if !ok {
						continue
					}
					for _, id := range vs.Names {
						vsMatches := nameRE != nil && nameRE.MatchString(id.Name) || nameRE == nil && id.Name == query.Name
						if vsMatches {
							results = append(results, h.extractValueResult(fset, src, f, gen, vs, id, path, outline))
						}
					}
				}
			}
		}

		return nil
	})

	if moduleDir != "" {
		for i := range results {
			if rel, err2 := filepath.Rel(moduleDir, results[i].File); err2 == nil {
				results[i].File = rel
			}
		}
	}

	return results, err
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

func (h *SurgeonQueriesHandler) extractFuncResult(fset *token.FileSet, src []byte, f *ast.File, fn *ast.FuncDecl, path, recv string, outline []domain.OutlineEntry) domain.SymbolResult {
	startPos := fset.Position(fn.Pos())
	endPos := fset.Position(fn.End())

	doc := ""
	if fn.Doc != nil {
		doc = strings.TrimSpace(fn.Doc.Text())
	}

	sigEnd := fn.Body.Pos()
	if sigEnd == token.NoPos {
		sigEnd = fn.End()
	}
	sigBytes := src[startPos.Offset:fset.Position(sigEnd).Offset]
	signature := strings.TrimSpace(string(sigBytes))

	codeLines := strings.Split(string(src[startPos.Offset:endPos.Offset]), "\n")
	var buf bytes.Buffer
	currentLine := startPos.Line
	for _, line := range codeLines {
		if strings.TrimSpace(line) != "" {
			fmt.Fprintf(&buf, "%d: %s\n", currentLine, line)
		}
		currentLine++
	}

	return domain.SymbolResult{
		File:        path,
		Package:     filePackageName(f),
		Imports:     fileImportPaths(f),
		FileOutline: outline,
		LineStart:   startPos.Line,
		LineEnd:     endPos.Line,
		Name:        fn.Name.Name,
		Receiver:    recv,
		Signature:   signature,
		Doc:         doc,
		Code:        strings.TrimSuffix(buf.String(), "\n"),
	}
}

func (h *SurgeonQueriesHandler) extractStructResult(fset *token.FileSet, src []byte, f *ast.File, gen *ast.GenDecl, typeSpec *ast.TypeSpec, path string, outline []domain.OutlineEntry) domain.SymbolResult {
	startPos := fset.Position(typeSpec.Pos())
	endPos := fset.Position(typeSpec.End())

	doc := ""
	if typeSpec.Doc != nil {
		doc = strings.TrimSpace(typeSpec.Doc.Text())
	} else if len(gen.Specs) == 1 && gen.Doc != nil {
		doc = strings.TrimSpace(gen.Doc.Text())
		startPos = fset.Position(gen.Doc.Pos()) // Include struct group comment start
	}

	sigBytes := src[fset.Position(typeSpec.Pos()).Offset:endPos.Offset]
	signature := strings.TrimSpace(string(sigBytes))
	// For struct, signature is usually just the type definition.
	// We'll treat the entire spec as signature.

	codeLines := strings.Split(string(src[startPos.Offset:endPos.Offset]), "\n")
	var buf bytes.Buffer
	currentLine := startPos.Line
	for _, line := range codeLines {
		if strings.TrimSpace(line) != "" {
			fmt.Fprintf(&buf, "%d: %s\n", currentLine, line)
		}
		currentLine++
	}

	return domain.SymbolResult{
		File:        path,
		Package:     filePackageName(f),
		Imports:     fileImportPaths(f),
		FileOutline: outline,
		LineStart:   startPos.Line,
		LineEnd:     endPos.Line,
		Name:        typeSpec.Name.Name,
		Receiver:    "",
		Signature:   signature,
		Doc:         doc,
		Code:        strings.TrimSuffix(buf.String(), "\n"),
	}
}

// filePackageName returns the package name declared in f, or an empty string.
func filePackageName(f *ast.File) string {
	if f == nil || f.Name == nil {
		return ""
	}
	return f.Name.Name
}

// fileImportPaths returns the import paths declared in f, in source order.
func fileImportPaths(f *ast.File) []string {
	if f == nil || len(f.Imports) == 0 {
		return nil
	}
	paths := make([]string, 0, len(f.Imports))
	for _, imp := range f.Imports {
		paths = append(paths, strings.Trim(imp.Path.Value, "\""))
	}
	return paths
}

// buildFileOutline walks every top-level declaration in f and returns
// compact signature entries (no bodies). Used when SymbolQuery.Context
// is "file" so the caller sees the whole file's structure in one shot.
func buildFileOutline(fset *token.FileSet, src []byte, f *ast.File) []domain.OutlineEntry {
	if f == nil {
		return nil
	}
	var out []domain.OutlineEntry
	for _, decl := range f.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			startPos := fset.Position(d.Pos())
			endPos := fset.Position(d.End())
			var sigEnd token.Pos
			if d.Body != nil {
				sigEnd = d.Body.Pos()
			} else {
				sigEnd = d.End()
			}
			sig := strings.TrimSpace(string(src[startPos.Offset:fset.Position(sigEnd).Offset]))
			kind := "func"
			recv := ""
			if d.Recv != nil {
				kind = "method"
				recv = getRecvTypeFromFields(d.Recv)
			}
			out = append(out, domain.OutlineEntry{
				Kind:      kind,
				Name:      d.Name.Name,
				Receiver:  recv,
				Signature: sig,
				LineStart: startPos.Line,
				LineEnd:   endPos.Line,
			})
		case *ast.GenDecl:
			for _, spec := range d.Specs {
				switch s := spec.(type) {
				case *ast.TypeSpec:
					startPos := fset.Position(s.Pos())
					endPos := fset.Position(s.End())
					kind := "type"
					name := s.Name.Name
					sig := outlineTypeSignature(s)
					out = append(out, domain.OutlineEntry{
						Kind:      kind,
						Name:      name,
						Signature: sig,
						LineStart: startPos.Line,
						LineEnd:   endPos.Line,
					})
				case *ast.ValueSpec:
					kind := "var"
					if d.Tok == token.CONST {
						kind = "const"
					}
					for _, id := range s.Names {
						startPos := fset.Position(id.Pos())
						endPos := fset.Position(s.End())
						typeStr := ""
						if s.Type != nil {
							typeStr = " " + strings.TrimSpace(string(src[fset.Position(s.Type.Pos()).Offset:fset.Position(s.Type.End()).Offset]))
						}
						out = append(out, domain.OutlineEntry{
							Kind:      kind,
							Name:      id.Name,
							Signature: kind + " " + id.Name + typeStr,
							LineStart: startPos.Line,
							LineEnd:   endPos.Line,
						})
					}
				}
			}
		}
	}
	return out
}

// outlineTypeSignature renders a compact one-line signature for a type
// declaration: "type Name struct", "type Name interface", "type Name alias".
func outlineTypeSignature(ts *ast.TypeSpec) string {
	switch ts.Type.(type) {
	case *ast.StructType:
		return "type " + ts.Name.Name + " struct"
	case *ast.InterfaceType:
		return "type " + ts.Name.Name + " interface"
	default:
		return "type " + ts.Name.Name
	}
}

// getRecvTypeFromFields extracts the receiver type name from a FuncDecl's
// Recv list (strips pointer stars, returns the bare type identifier).
func getRecvTypeFromFields(recv *ast.FieldList) string {
	if recv == nil || len(recv.List) == 0 {
		return ""
	}
	switch t := recv.List[0].Type.(type) {
	case *ast.Ident:
		return t.Name
	case *ast.StarExpr:
		if id, ok := t.X.(*ast.Ident); ok {
			return id.Name
		}
	}
	return ""
}

// extractValueResult builds a SymbolResult for a single package-level
// var/const declaration (one of the ValueSpec names inside a GenDecl).
// Unlike funcs and types, the "signature" is rendered as "var Name Type"
// or "const Name Type = Value" for human readability; the Code field
// contains the surrounding GenDecl source so the agent can patch it.
func (h *SurgeonQueriesHandler) extractValueResult(fset *token.FileSet, src []byte, f *ast.File, gen *ast.GenDecl, spec *ast.ValueSpec, name *ast.Ident, path string, outline []domain.OutlineEntry) domain.SymbolResult {
	// Use the whole GenDecl as the extracted range when the spec is alone,
	// otherwise just the spec itself. This mirrors how extractStructResult
	// handles single-spec vs. multi-spec type blocks.
	var startPos, endPos token.Position
	if len(gen.Specs) == 1 {
		startPos = fset.Position(gen.Pos())
		endPos = fset.Position(gen.End())
	} else {
		startPos = fset.Position(spec.Pos())
		endPos = fset.Position(spec.End())
	}

	doc := ""
	if spec.Doc != nil {
		doc = strings.TrimSpace(spec.Doc.Text())
	} else if len(gen.Specs) == 1 && gen.Doc != nil {
		doc = strings.TrimSpace(gen.Doc.Text())
	}

	kind := "var"
	if gen.Tok == token.CONST {
		kind = "const"
	}
	typeStr := ""
	if spec.Type != nil {
		typeStr = " " + strings.TrimSpace(string(src[fset.Position(spec.Type.Pos()).Offset:fset.Position(spec.Type.End()).Offset]))
	}
	signature := kind + " " + name.Name + typeStr

	codeLines := strings.Split(string(src[startPos.Offset:endPos.Offset]), "\n")
	var buf bytes.Buffer
	currentLine := startPos.Line
	for _, line := range codeLines {
		if strings.TrimSpace(line) != "" {
			fmt.Fprintf(&buf, "%d: %s\n", currentLine, line)
		}
		currentLine++
	}

	return domain.SymbolResult{
		File:        path,
		Package:     filePackageName(f),
		Imports:     fileImportPaths(f),
		FileOutline: outline,
		LineStart:   startPos.Line,
		LineEnd:     endPos.Line,
		Name:        name.Name,
		Receiver:    "",
		Signature:   signature,
		Doc:         doc,
		Code:        strings.TrimSuffix(buf.String(), "\n"),
	}
}

func (h *SurgeonQueriesHandler) findSymbolAtLine(ctx context.Context, query domain.SymbolQuery) (*domain.SymbolResult, error) {
	if query.File == "" {
		return nil, fmt.Errorf("file is required when at_line is set")
	}
	src, err := h.fs.ReadFile(ctx, query.File)
	if err != nil {
		return nil, fmt.Errorf("cannot read %s: %w", query.File, err)
	}
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, query.File, src, parser.ParseComments)
	if err != nil {
		return nil, fmt.Errorf("cannot parse %s: %w", query.File, err)
	}
	var outline []domain.OutlineEntry
	if query.Context == "file" {
		outline = buildFileOutline(fset, src, f)
	}
	line := query.AtLine
	for _, decl := range f.Decls {
		start := fset.Position(decl.Pos()).Line
		end := fset.Position(decl.End()).Line
		if line < start || line > end {
			continue
		}
		if fn, ok := decl.(*ast.FuncDecl); ok {
			recv := ""
			if fn.Recv != nil {
				recv = getRecvType(fn.Recv)
			}
			r := h.extractFuncResult(fset, src, f, fn, query.File, recv, outline)
			return &r, nil
		}
		if gen, ok := decl.(*ast.GenDecl); ok {
			switch gen.Tok {
			case token.TYPE:
				for _, spec := range gen.Specs {
					ts, ok := spec.(*ast.TypeSpec)
					if !ok {
						continue
					}
					r := h.extractStructResult(fset, src, f, gen, ts, query.File, outline)
					return &r, nil
				}
			case token.VAR, token.CONST:
				for _, spec := range gen.Specs {
					vs, ok := spec.(*ast.ValueSpec)
					if !ok {
						continue
					}
					if len(vs.Names) > 0 {
						r := h.extractValueResult(fset, src, f, gen, vs, vs.Names[0], query.File, outline)
						return &r, nil
					}
				}
			}
		}
	}
	return nil, nil
}
