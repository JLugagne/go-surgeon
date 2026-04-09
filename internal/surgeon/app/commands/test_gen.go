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
		// Just a placeholder or setup func for receiver
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

	buf.WriteString("\tfor _, tt := range tests {\n")
	buf.WriteString("\t\tt.Run(tt.name, func(t *testing.T) {\n")

	// Call the function
	var callArgs []string
	for _, p := range params {
		callArgs = append(callArgs, "tt.args."+p.Name)
	}

	var assignVars []string
	var wantChecks []string
	for i, r := range results {
		vName := fmt.Sprintf("got%d", i)
		if len(results) == 1 {
			vName = "got"
		}
		assignVars = append(assignVars, vName)
		wantChecks = append(wantChecks, fmt.Sprintf("\t\t\tassert.Equal(t, tt.%s, %s)", r.Name, vName))
	}
	if returnsError {
		assignVars = append(assignVars, "err")
	}

	callStr := fmt.Sprintf("%s(%s)", funcName, strings.Join(callArgs, ", "))
	if recvVar != "" {
		callStr = fmt.Sprintf("tt.%s.%s(%s)", recvVar, funcName, strings.Join(callArgs, ", "))
	}

	if len(assignVars) > 0 {
		fmt.Fprintf(&buf, "\t\t\t%s := %s\n", strings.Join(assignVars, ", "), callStr)
	} else {
		fmt.Fprintf(&buf, "\t\t\t%s\n", callStr)
	}

	if returnsError {
		buf.WriteString("\t\t\tif tt.wantErr {\n")
		buf.WriteString("\t\t\t\tassert.Error(t, err)\n")
		buf.WriteString("\t\t\t\treturn\n")
		buf.WriteString("\t\t\t}\n")
		buf.WriteString("\t\t\trequire.NoError(t, err)\n")
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

	// Write to test file
	ext := filepath.Ext(filePath)
	base := strings.TrimSuffix(filePath, ext)
	testFile := base + "_test" + ext

	testFileSrc, err := h.fs.ReadFile(ctx, testFile)
	if err != nil {
		if os.IsNotExist(err) {
			// Create new test file
			pkgName := f.Name.Name
			testFileSrc = []byte(fmt.Sprintf("package %s_test\n\nimport (\n\t\"testing\"\n\t\"github.com/stretchr/testify/assert\"\n\t\"github.com/stretchr/testify/require\"\n)\n\n", pkgName))
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
