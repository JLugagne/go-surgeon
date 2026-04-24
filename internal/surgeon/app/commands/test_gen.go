package commands

import (
	"bytes"
	"context"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/JLugagne/go-surgeon/internal/surgeon/domain"
)

type assertLib int

const (
	assertLibNone    assertLib = iota // stdlib only
	assertLibTestify                  // github.com/stretchr/testify
	assertLibGotest                   // gotest.tools/assert
)

// detectAssertLib inspects _test.go files in the same directory as filePath and
// returns which assertion library they import, defaulting to stdlib-only.
func (h *ExecutePlanHandler) detectAssertLib(ctx context.Context, filePath string) assertLib {
	dir := filepath.Dir(filePath)
	entries, err := h.fs.ReadDir(ctx, dir)
	if err != nil {
		return assertLibNone
	}
	for _, name := range entries {
		if !strings.HasSuffix(name, "_test.go") {
			continue
		}
		data, err := h.fs.ReadFile(ctx, filepath.Join(dir, name))
		if err != nil {
			continue
		}
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, name, data, parser.ImportsOnly)
		if err != nil {
			continue
		}
		for _, imp := range f.Imports {
			path := strings.Trim(imp.Path.Value, `"`)
			if strings.HasPrefix(path, "github.com/stretchr/testify") {
				return assertLibTestify
			}
			if strings.HasPrefix(path, "gotest.tools") {
				return assertLibGotest
			}
		}
	}
	return assertLibNone
}

// isExported reports whether name refers to an exported Go identifier.
// It strips leading pointer/slice sigils before checking.
func isExported(name string) bool {
	name = strings.TrimLeft(name, "*[]")
	if name == "" {
		return false
	}
	return unicode.IsUpper([]rune(name)[0])
}

func typeToString(expr ast.Expr, src []byte, fset *token.FileSet) string {
	start := fset.Position(expr.Pos()).Offset
	end := fset.Position(expr.End()).Offset
	if start >= 0 && end <= len(src) && start <= end {
		return string(src[start:end])
	}
	return ""
}

// capitalizeFirst uppercases the first rune of s, leaving the rest unchanged.
// Unlike cases.Title, this preserves interior casing (e.g. "doWork" → "DoWork").
func capitalizeFirst(s string) string {
	if s == "" {
		return s
	}
	r := []rune(s)
	r[0] = unicode.ToUpper(r[0])
	return string(r)
}

// qualifyType prepends pkgName to an exported type that is not already qualified.
// Examples:
//
//	"*App"       → "*app.App"
//	"App"        → "app.App"
//	"*app.App"   → "*app.App"   (already qualified, unchanged)
//	"*bookRepo"  → "*bookRepo"  (unexported, unchanged)
func qualifyType(typStr, pkgName string) string {
	// Strip pointer/slice sigils to examine the base type.
	prefix := ""
	base := typStr
	for len(base) > 0 && (base[0] == '*' || base[0] == '[' || base[0] == ']') {
		prefix += string(base[0])
		base = base[1:]
	}
	if base == "" {
		return typStr
	}
	// Already package-qualified (contains a dot).
	if strings.Contains(base, ".") {
		return typStr
	}
	// Only qualify exported identifiers.
	if !unicode.IsUpper([]rune(base)[0]) {
		return typStr
	}
	return prefix + pkgName + "." + base
}

func (h *ExecutePlanHandler) GenerateTest(ctx context.Context, filePath, identifier string) (string, error) {
	src, err := h.fs.ReadFile(ctx, filePath)
	if err != nil {
		return "", &domain.Error{Code: "READ_ERROR", Message: "failed to read file", Err: err}
	}

	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, filePath, src, 0)
	if err != nil {
		return "", &domain.Error{Code: "PARSE_ERROR", Message: "failed to parse file", Err: err}
	}

	recvName, funcName := parseIdentifier(identifier)

	var targetFunc *ast.FuncDecl
	for _, decl := range f.Decls {
		if fn, ok := decl.(*ast.FuncDecl); ok {
			if fn.Name.Name == funcName {
				if recvName == "" && fn.Recv == nil {
					targetFunc = fn
					break
				}
				if recvName != "" && fn.Recv != nil && len(fn.Recv.List) > 0 {
					if getRecvType(fn.Recv) == recvName {
						targetFunc = fn
						break
					}
				}
			}
		}
	}

	if targetFunc == nil {
		return "", &domain.Error{Code: "NOT_FOUND", Message: fmt.Sprintf("function '%s' not found", identifier)}
	}

	// Extract params
	type paramInfo struct {
		Name string
		Type string
	}
	var params []paramInfo
	var recvType string
	var recvVar string

	if targetFunc.Recv != nil && len(targetFunc.Recv.List) > 0 {
		recvType = typeToString(targetFunc.Recv.List[0].Type, src, fset)
		if len(targetFunc.Recv.List[0].Names) > 0 {
			recvVar = targetFunc.Recv.List[0].Names[0].Name
		} else {
			recvVar = "recv" // default
		}
	}

	if targetFunc.Type.Params != nil {
		for i, field := range targetFunc.Type.Params.List {
			typStr := typeToString(field.Type, src, fset)
			if len(field.Names) == 0 {
				params = append(params, paramInfo{Name: fmt.Sprintf("arg%d", i), Type: typStr})
			} else {
				for _, name := range field.Names {
					params = append(params, paramInfo{Name: name.Name, Type: typStr})
				}
			}
		}
	}

	// Extract results
	var results []paramInfo
	var returnsError bool

	if targetFunc.Type.Results != nil {
		for i, field := range targetFunc.Type.Results.List {
			typStr := typeToString(field.Type, src, fset)
			if typStr == "error" {
				returnsError = true
				continue
			}
			if len(field.Names) == 0 {
				results = append(results, paramInfo{Name: fmt.Sprintf("want%d", i), Type: typStr})
			} else {
				for _, name := range field.Names {
					results = append(results, paramInfo{Name: "want" + capitalizeFirst(name.Name), Type: typStr})
				}
			}
		}
	}

	// Construct test skeleton
	testName := "Test" + capitalizeFirst(funcName)
	if recvName != "" {
		testName = "Test" + capitalizeFirst(recvName) + "_" + capitalizeFirst(funcName)
	}

	var buf bytes.Buffer
	fmt.Fprintf(&buf, "func %s(t *testing.T) {\n", testName)

	// args struct
	if len(params) > 0 {
		buf.WriteString("\ttype args struct {\n")
		for _, p := range params {
			fmt.Fprintf(&buf, "\t\t%s %s\n", p.Name, p.Type)
		}
		buf.WriteString("\t}\n")
	}

	// Determine whether this will be a black-box test up front so we can use
	// it when emitting the test struct fields too.
	pkgName := f.Name.Name
	blackBox := recvType == "" || isExported(recvType)
	// A free function is black-box only if it is exported.
	if recvType == "" {
		blackBox = isExported(funcName)
	}

	// In a black-box test, an exported receiver type must be package-qualified.
	displayRecvType := recvType
	if blackBox && recvType != "" {
		displayRecvType = qualifyType(recvType, pkgName)
	}

	buf.WriteString("\ttests := []struct {\n")
	buf.WriteString("\t\tname string\n")
	if recvType != "" {
		fmt.Fprintf(&buf, "\t\t%s %s\n", recvVar, displayRecvType)
	}
	if len(params) > 0 {
		buf.WriteString("\t\targs args\n")
	}
	for _, r := range results {
		fmt.Fprintf(&buf, "\t\t%s %s\n", r.Name, r.Type)
	}
	if returnsError {
		buf.WriteString("\t\twantErr bool\n")
	}
	buf.WriteString("\t}{\n")
	buf.WriteString("\t\t// TODO(go-surgeon): Add test cases.\n")
	buf.WriteString("\t}\n")

	// Fail loudly when no cases are defined so an agent can't land an empty
	// test that silently passes.
	fmt.Fprintf(&buf, "\tif len(tests) == 0 {\n")
	fmt.Fprintf(&buf, "\t\tt.Fatal(\"TODO(go-surgeon): no test cases defined for %s — add cases or delete this test\")\n", testName)
	buf.WriteString("\t}\n")

	// Each generated t.Run body is a single t.Fatal placeholder. The agent
	// must replace the body with a real implementation or remove the t.Run
	// entry entirely — no silent passes.
	buf.WriteString("\tfor _, tt := range tests {\n")
	buf.WriteString("\t\tt.Run(tt.name, func(t *testing.T) {\n")
	buf.WriteString("\t\t\tt.Fatal(\"TODO(go-surgeon): implement this test case or remove the t.Run block entirely — do not leave placeholders\")\n")
	buf.WriteString("\t\t})\n")
	buf.WriteString("\t}\n")
	buf.WriteString("}\n")

	// detectAssertLib still drives the import header below, even though the
	// t.Run body no longer emits assertions — a correct import set keeps the
	// file compilable once the agent fills the placeholder in.
	lib := h.detectAssertLib(ctx, filePath)

	// These locals no longer feed the t.Run body (it is a single t.Fatal
	// placeholder). They stay in scope because the surrounding generator still
	// uses some of them, and referencing the others keeps the intent explicit.
	_ = params
	_ = results
	_ = returnsError
	_ = recvVar

	// Formatted generated code
	formattedTest, err := format.Source(buf.Bytes())
	if err != nil {
		formattedTest = buf.Bytes() // fallback
	}

	// Determine target test file.
	// - exported func/receiver  → file_test.go       (package xxx_test)
	// - unexported func/receiver → file_internal_test.go (package xxx, white-box)
	ext := filepath.Ext(filePath)
	base := strings.TrimSuffix(filePath, ext)
	var testFile string
	if blackBox {
		testFile = base + "_test" + ext
	} else {
		testFile = base + "_internal_test" + ext
	}

	testFileSrc, err := h.fs.ReadFile(ctx, testFile)
	if err != nil {
		if os.IsNotExist(err) {
			var header string
			if blackBox {
				switch lib {
				case assertLibTestify:
					header = fmt.Sprintf("package %s_test\n\nimport (\n\t\"testing\"\n\t\"github.com/stretchr/testify/assert\"\n\t\"github.com/stretchr/testify/require\"\n)\n\n", pkgName)
				case assertLibGotest:
					header = fmt.Sprintf("package %s_test\n\nimport (\n\t\"testing\"\n\t\"gotest.tools/assert\"\n)\n\n", pkgName)
				default:
					header = fmt.Sprintf("package %s_test\n\nimport \"testing\"\n\n", pkgName)
				}
			} else {
				// White-box: unexported — stay in same package.
				switch lib {
				case assertLibTestify:
					header = fmt.Sprintf("package %s\n\nimport (\n\t\"testing\"\n\t\"github.com/stretchr/testify/assert\"\n\t\"github.com/stretchr/testify/require\"\n)\n\n", pkgName)
				case assertLibGotest:
					header = fmt.Sprintf("package %s\n\nimport (\n\t\"testing\"\n\t\"gotest.tools/assert\"\n)\n\n", pkgName)
				default:
					header = fmt.Sprintf("package %s\n\nimport \"testing\"\n\n", pkgName)
				}
			}
			testFileSrc = []byte(header)
		} else {
			return "", &domain.Error{Code: "READ_ERROR", Message: "failed to read test file", Err: err}
		}
	} else {
		// File exists: check that testName is not already declared.
		// For unexported functions, also check the black-box file to avoid
		// generating a duplicate that would shadow or conflict.
		if testFuncExists(testFileSrc, testName) {
			return testFile, nil
		}
		if !blackBox {
			bbFile := base + "_test" + ext
			if bbSrc, bbErr := h.fs.ReadFile(ctx, bbFile); bbErr == nil {
				if testFuncExists(bbSrc, testName) {
					return bbFile, nil
				}
			}
		}
	}

	updatedTestSrc := append(testFileSrc, '\n')
	updatedTestSrc = append(updatedTestSrc, formattedTest...)

	if _, err := h.fs.WriteFile(ctx, testFile, updatedTestSrc); err != nil {
		return "", &domain.Error{Code: "WRITE_ERROR", Message: "failed to write test file", Err: err}
	}

	return testFile, nil
}

// testFuncExists reports whether a function named funcName is already declared
// in the given Go source (a test file). It parses the AST so it handles any
// formatting and avoids false positives from comments or string literals.
func testFuncExists(src []byte, funcName string) bool {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "", src, 0)
	if err != nil {
		// If we can't parse, assume it doesn't exist so we still try to append.
		return false
	}
	for _, decl := range f.Decls {
		if fn, ok := decl.(*ast.FuncDecl); ok && fn.Recv == nil {
			if fn.Name.Name == funcName {
				return true
			}
		}
	}
	return false
}
