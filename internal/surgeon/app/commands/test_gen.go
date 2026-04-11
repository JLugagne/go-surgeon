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

// GenerateTest generates a table-driven test skeleton for a specific function.
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

	buf.WriteString("\ttests := []struct {\n")
	buf.WriteString("\t\tname string\n")
	if recvType != "" {
		fmt.Fprintf(&buf, "\t\t%s %s\n", recvVar, recvType)
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
	buf.WriteString("\t\t// TODO: Add test cases.\n")
	buf.WriteString("\t}\n")

	// Skip with a clear message when no test cases have been added yet,
	// so the test shows up as SKIP in verbose mode rather than silently passing.
	fmt.Fprintf(&buf, "\tif len(tests) == 0 {\n")
	fmt.Fprintf(&buf, "\t\tt.Skip(\"TODO: no test cases defined for %s\")\n", testName)
	buf.WriteString("\t}\n")

	buf.WriteString("\tfor _, tt := range tests {\n")
	buf.WriteString("\t\tt.Run(tt.name, func(t *testing.T) {\n")

	// Call the function
	var callArgs []string
	for _, p := range params {
		callArgs = append(callArgs, "tt.args."+p.Name)
	}

	lib := h.detectAssertLib(ctx, filePath)

	var assignVars []string
	var wantChecks []string
	for i, r := range results {
		vName := fmt.Sprintf("got%d", i)
		if len(results) == 1 {
			vName = "got"
		}
		assignVars = append(assignVars, vName)
		switch lib {
		case assertLibTestify:
			wantChecks = append(wantChecks, fmt.Sprintf("\t\t\tassert.Equal(t, tt.%s, %s)", r.Name, vName))
		case assertLibGotest:
			wantChecks = append(wantChecks, fmt.Sprintf("\t\t\tassert.Equal(t, %s, tt.%s)", vName, r.Name))
		default:
			wantChecks = append(wantChecks, fmt.Sprintf("\t\t\tif got, want := %s, tt.%s; got != want { t.Errorf(\"%%v != %%v\", got, want) }", vName, r.Name))
		}
	}
	if returnsError {
		assignVars = append(assignVars, "err")
	}

	// Determine whether this will be a black-box test (unexported receiver → white-box).
	pkgName := f.Name.Name
	blackBox := recvType == "" || isExported(recvType)

	var callStr string
	if recvVar != "" {
		callStr = fmt.Sprintf("tt.%s.%s(%s)", recvVar, funcName, strings.Join(callArgs, ", "))
	} else if blackBox {
		// Black-box test: qualify free function with package name.
		callStr = fmt.Sprintf("%s.%s(%s)", pkgName, funcName, strings.Join(callArgs, ", "))
	} else {
		callStr = fmt.Sprintf("%s(%s)", funcName, strings.Join(callArgs, ", "))
	}

	if len(assignVars) > 0 {
		fmt.Fprintf(&buf, "\t\t\t%s := %s\n", strings.Join(assignVars, ", "), callStr)
	} else {
		fmt.Fprintf(&buf, "\t\t\t%s\n", callStr)
	}

	if returnsError {
		buf.WriteString("\t\t\tif tt.wantErr {\n")
		switch lib {
		case assertLibTestify:
			buf.WriteString("\t\t\t\tassert.Error(t, err)\n")
		case assertLibGotest:
			buf.WriteString("\t\t\t\tassert.ErrorContains(t, err, \"\")\n")
		default:
			buf.WriteString("\t\t\t\tif err == nil { t.Error(\"expected error, got nil\") }\n")
		}
		buf.WriteString("\t\t\t\treturn\n")
		buf.WriteString("\t\t\t}\n")
		switch lib {
		case assertLibTestify:
			buf.WriteString("\t\t\trequire.NoError(t, err)\n")
		case assertLibGotest:
			buf.WriteString("\t\t\tassert.NilError(t, err)\n")
		default:
			buf.WriteString("\t\t\tif err != nil { t.Fatalf(\"unexpected error: %v\", err) }\n")
		}
	}

	for _, check := range wantChecks {
		buf.WriteString(check + "\n")
	}

	buf.WriteString("\t\t})\n")
	buf.WriteString("\t}\n")
	buf.WriteString("}\n")

	// Formatted generated code
	formattedTest, err := format.Source(buf.Bytes())
	if err != nil {
		formattedTest = buf.Bytes() // fallback
	}

	// Write to test file.
	// Use white-box package (no _test suffix) when the receiver is unexported,
	// since unexported types are inaccessible from external test packages.
	ext := filepath.Ext(filePath)
	base := strings.TrimSuffix(filePath, ext)
	testFile := base + "_test" + ext

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
				// White-box: unexported receiver type — stay in same package.
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
	}

	updatedTestSrc := append(testFileSrc, '\n')
	updatedTestSrc = append(updatedTestSrc, formattedTest...)

	if err := h.fs.WriteFile(ctx, testFile, updatedTestSrc); err != nil {
		return "", &domain.Error{Code: "WRITE_ERROR", Message: "failed to write test file", Err: err}
	}

	_ = h.fs.ExecuteGoImports(ctx, []string{testFile})

	return testFile, nil
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
