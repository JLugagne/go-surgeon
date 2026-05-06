package mcp

import (
	"context"
	"fmt"
	"strings"

	"github.com/JLugagne/go-surgeon/internal/surgeon/domain"
	"github.com/JLugagne/go-surgeon/internal/surgeon/domain/service"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type implementInput struct {
	Interface string `json:"interface" jsonschema:"fully qualified interface name, e.g. io.ReadCloser or github.com/org/repo/pkg.Interface"`
	Receiver  string `json:"receiver" jsonschema:"receiver type, e.g. *MyStruct"`
	File      string `json:"file" jsonschema:"target file to append stubs to"`
	Preview   bool   `json:"preview,omitempty" jsonschema:"if true, return diff without writing the file"`
}

type mockInput struct {
	Interface string `json:"interface" jsonschema:"fully qualified interface name"`
	MockName  string `json:"mock_name" jsonschema:"name of the mock struct, e.g. MockBookRepository"`
	File      string `json:"file" jsonschema:"target file to write the mock to"`
	Preview   bool   `json:"preview,omitempty" jsonschema:"if true, return diff without writing the file"`
}

type testInput struct {
	File       string `json:"file" jsonschema:"target Go file containing the function"`
	Identifier string `json:"identifier" jsonschema:"function or method identifier, e.g. NewApp or Book.Validate"`
	Preview    bool   `json:"preview,omitempty" jsonschema:"if true, return diff without writing the test file"`
}

type extractInterfaceInput struct {
	File       string `json:"file" jsonschema:"target Go file containing the struct"`
	Identifier string `json:"identifier" jsonschema:"struct identifier"`
	Name       string `json:"name" jsonschema:"name of the interface to create"`
	Out        string `json:"out,omitempty" jsonschema:"output file path for the interface"`
	MockFile   string `json:"mock_file,omitempty" jsonschema:"generate mock file path"`
	MockName   string `json:"mock_name,omitempty" jsonschema:"name of the mock struct"`
	Preview    bool   `json:"preview,omitempty" jsonschema:"if true, return diff without writing any file"`
}

func registerCodegenTools(s *mcp.Server, commands service.SurgeonCommands) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        "implement",
		Description: "Generate method stubs for a struct to satisfy an interface. Already-implemented methods are skipped. Stubs are marked '// TODO(go-surgeon): implement'. Interface must be fully qualified (e.g. io.ReadCloser, github.com/org/repo/pkg.Interface). preview=true returns a unified diff without writing.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in implementInput) (*mcp.CallToolResult, any, error) {
		if err := validateGoFile(in.File); err != nil {
			return err, nil, nil
		}
		reqDomain := domain.ImplementRequest{
			Interface: in.Interface,
			Receiver:  in.Receiver,
			FilePath:  in.File,
		}
		var results []domain.SymbolResult
		var diff string
		var err error
		if in.Preview {
			diff, _, err = runPreview(ctx, commands, func(sc service.SurgeonCommands) error {
				var innerErr error
				results, innerErr = sc.Implement(ctx, reqDomain)
				return innerErr
			})
		} else {
			results, err = commands.Implement(ctx, reqDomain)
		}
		if err != nil {
			return errorResultWithCode(fmt.Sprintf("failed to implement interface: %v", err), err), nil, nil
		}

		if len(results) == 0 && diff == "" {
			res := textResult("All methods are already implemented.")
			res.StructuredContent = implementOutput{File: in.File, Interface: in.Interface, Receiver: in.Receiver, Stubs: []string{}}
			return res, nil, nil
		}

		var sb strings.Builder
		verb := "Generated"
		if in.Preview {
			verb = "PREVIEW — would generate"
		}
		fmt.Fprintf(&sb, "%s %d missing methods for %s:\n\n", verb, len(results), in.Interface)
		for _, rr := range results {
			fmt.Fprintf(&sb, "Symbol: %s\nReceiver: %s\nFile: %s:%d-%d\nCode:\n%s\n\n",
				rr.Name, rr.Receiver, rr.File, rr.LineStart, rr.LineEnd, rr.Code)
		}
		if diff != "" {
			sb.WriteString(diff)
		}
		stubs := make([]string, len(results))
		for i, r := range results {
			stubs[i] = r.Name
		}
		res := textResult(sb.String())
		res.StructuredContent = implementOutput{File: in.File, Interface: in.Interface, Receiver: in.Receiver, Stubs: stubs}
		return res, nil, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "mock",
		Description: "Generate a function-field mock for an interface you don't own (stdlib, third-party). For your own interfaces, use the interface tool (action=add) with mock_file instead. Interface must be fully qualified. preview=true returns a unified diff without writing.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in mockInput) (*mcp.CallToolResult, any, error) {
		if err := validateGoFile(in.File); err != nil {
			return err, nil, nil
		}
		reqDomain := domain.MockRequest{
			Interface: in.Interface,
			Receiver:  in.MockName,
			FilePath:  in.File,
		}
		var result, diff string
		var err error
		if in.Preview {
			diff, _, err = runPreview(ctx, commands, func(sc service.SurgeonCommands) error {
				var innerErr error
				result, innerErr = sc.Mock(ctx, reqDomain)
				return innerErr
			})
		} else {
			result, err = commands.Mock(ctx, reqDomain)
		}
		if err != nil {
			return errorResultWithCode(fmt.Sprintf("failed to generate mock: %v", err), err), nil, nil
		}
		msg := result
		if in.Preview {
			msg = "PREVIEW (mock): " + result
			if diff != "" {
				msg += "\n\n" + diff
			}
		}
		res := textResult(msg)
		res.StructuredContent = mockOutput{File: in.File, Interface: in.Interface, MockName: in.MockName}
		return res, nil, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "test",
		Description: "Scaffold a table-driven _test.go for a function or method. Handles boilerplate (t.Run loop, tt struct, receiver setup). identifier: 'FuncName' or 'Type.Method'. preview=true returns a unified diff without writing.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in testInput) (*mcp.CallToolResult, any, error) {
		if err := validateGoFile(in.File); err != nil {
			return err, nil, nil
		}
		var testFile, diff string
		var err error
		if in.Preview {
			diff, _, err = runPreview(ctx, commands, func(sc service.SurgeonCommands) error {
				var innerErr error
				testFile, innerErr = sc.GenerateTest(ctx, in.File, in.Identifier)
				return innerErr
			})
		} else {
			testFile, err = commands.GenerateTest(ctx, in.File, in.Identifier)
		}
		if err != nil {
			return errorResultWithCode(fmt.Sprintf("failed to generate test: %v", err), err), nil, nil
		}
		msg := fmt.Sprintf("SUCCESS: Generated test skeleton in %s", testFile)
		if in.Preview {
			msg = fmt.Sprintf("PREVIEW: would generate test skeleton in %s", testFile)
			if diff != "" {
				msg += "\n\n" + diff
			}
		}
		res := textResult(msg)
		res.StructuredContent = testOutput{TestFile: testFile, Identifier: in.Identifier}
		return res, nil, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "extract_interface",
		Description: "Derive an interface from a struct's exported methods. Use out to place it in a specific file. Set mock_file + mock_name to generate the mock in the same step. preview=true returns a unified diff without writing.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in extractInterfaceInput) (*mcp.CallToolResult, any, error) {
		if err := validateGoFile(in.File); err != nil {
			return err, nil, nil
		}
		reqDomain := domain.ExtractInterfaceRequest{
			FilePath:      in.File,
			StructName:    in.Identifier,
			InterfaceName: in.Name,
			OutPath:       in.Out,
			MockFile:      in.MockFile,
			MockName:      in.MockName,
		}
		var interfaceFile, diff string
		var err error
		if in.Preview {
			diff, _, err = runPreview(ctx, commands, func(sc service.SurgeonCommands) error {
				var innerErr error
				interfaceFile, innerErr = sc.ExtractInterface(ctx, reqDomain)
				return innerErr
			})
		} else {
			interfaceFile, err = commands.ExtractInterface(ctx, reqDomain)
		}
		if err != nil {
			return errorResultWithCode(fmt.Sprintf("failed to extract interface: %v", err), err), nil, nil
		}
		msg := fmt.Sprintf("SUCCESS: Extracted interface %s into %s", in.Name, interfaceFile)
		if in.Preview {
			msg = fmt.Sprintf("PREVIEW: would extract interface %s into %s", in.Name, interfaceFile)
			if diff != "" {
				msg += "\n\n" + diff
			}
		}
		res := textResult(msg)
		res.StructuredContent = extractInterfaceOutput{InterfaceName: in.Name, InterfaceFile: interfaceFile, MockFile: in.MockFile, MockName: in.MockName}
		return res, nil, nil
	})
}
