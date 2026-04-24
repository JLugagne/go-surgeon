package commands

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"strings"

	"github.com/JLugagne/go-surgeon/internal/surgeon/domain"
)

// checkDirectivesIntact verifies that no critical Go directives have been
// detached from their target declaration by a patch. Returns an error
// describing the first violation found, or nil if all directives are intact.
func checkDirectivesIntact(src []byte, filePath string) error {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, filePath, src, parser.ParseComments)
	if err != nil {
		// If the source does not parse we cannot check directives; let the
		// caller's own parse/format validation catch this.
		return nil
	}

	// Build a CommentMap to know which comment groups are associated with
	// which AST nodes (declaration-adjacent comments).
	cmap := ast.NewCommentMap(fset, f, f.Comments)

	criticalPrefixes := []string{
		"//go:embed",
		"//go:generate",
		"//go:noescape",
		"//go:nosplit",
		"//go:linkname",
	}

	srcLines := strings.Split(string(src), "\n")

	// Build a map from declaration start line to the declaration node.
	declAtLine := make(map[int]ast.Decl)
	for _, decl := range f.Decls {
		declAtLine[fset.Position(decl.Pos()).Line] = decl
	}

	for _, cg := range f.Comments {
		for ci, c := range cg.List {
			directive := ""
			for _, prefix := range criticalPrefixes {
				if strings.HasPrefix(c.Text, prefix) {
					directive = prefix
					break
				}
			}
			if directive == "" {
				continue
			}

			directiveLine := fset.Position(c.Pos()).Line

			// Rule 1: the directive must be the LAST comment in its group.
			// If there are more comments after it in the same group, something
			// was inserted between the directive and the declaration.
			if ci < len(cg.List)-1 {
				return &domain.Error{
					Code: "PATCH_BREAKS_DIRECTIVE",
					Message: fmt.Sprintf(
						"patch would detach %s directive at line %d from its target declaration — insert code after the declaration, not between the directive and its target",
						directive, directiveLine,
					),
				}
			}

			// Find the declaration associated with this comment group.
			var assocDecl ast.Decl
			for node := range cmap {
				decl, ok := node.(ast.Decl)
				if !ok {
					continue
				}
				for _, assocCG := range cmap[decl] {
					if assocCG == cg {
						assocDecl = decl
						break
					}
				}
				if assocDecl != nil {
					break
				}
			}

			if assocDecl == nil {
				// Not associated with any declaration: check for blank line
				// or non-declaration following the directive.
				nextLine := directiveLine + 1
				for nextLine-1 < len(srcLines) {
					line := strings.TrimSpace(srcLines[nextLine-1])
					if line == "" {
						return &domain.Error{
							Code: "PATCH_BREAKS_DIRECTIVE",
							Message: fmt.Sprintf(
								"patch would detach %s directive at line %d from its target declaration — insert code after the declaration, not between the directive and its target",
								directive, directiveLine,
							),
						}
					}
					if strings.HasPrefix(line, "//") {
						nextLine++
						continue
					}
					if _, ok := declAtLine[nextLine]; !ok {
						return &domain.Error{
							Code: "PATCH_BREAKS_DIRECTIVE",
							Message: fmt.Sprintf(
								"patch would detach %s directive at line %d from its target declaration — insert code after the declaration, not between the directive and its target",
								directive, directiveLine,
							),
						}
					}
					break
				}
			} else {
				// Associated with a declaration: verify no blank line between
				// end of comment group and start of declaration.
				groupEndLine := fset.Position(cg.End()).Line
				declStartLine := fset.Position(assocDecl.Pos()).Line
				if declStartLine != groupEndLine+1 {
					return &domain.Error{
						Code: "PATCH_BREAKS_DIRECTIVE",
						Message: fmt.Sprintf(
							"patch would detach %s directive at line %d from its target declaration — insert code after the declaration, not between the directive and its target",
							directive, directiveLine,
						),
					}
				}
			}
		}
	}

	return nil
}
