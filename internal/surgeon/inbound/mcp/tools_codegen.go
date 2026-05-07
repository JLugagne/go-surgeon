package mcp

import (
	"context"
	"fmt"
	"strings"

	"github.com/JLugagne/go-surgeon/internal/surgeon/domain"
	"github.com/JLugagne/go-surgeon/internal/surgeon/domain/service"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type testInput struct {
	File       string `json:"file" jsonschema:"target Go file containing the function"`
	Identifier string `json:"identifier" jsonschema:"function or method identifier, e.g. NewApp or Book.Validate"`
	Preview    bool   `json:"preview,omitempty" jsonschema:"if true, return diff without writing the test file"`
}

// deriveInput is the unified shape for the derive tool. The kind field
// discriminates between three previously-separate tools (extract_interface,
// implement, mock). Field semantics depend on kind — see the field tags
// and the tool description.
type deriveInput struct {
	Kind string `json:"kind" jsonschema:"what to derive: interface_from_type | impl_from_interface | mock_from_interface"`
	File string `json:"file" jsonschema:"target Go file (interface_from_type: file containing the source struct; impl_from_interface: file to append stubs to; mock_from_interface: file to write the mock to)"`

	Identifier string `json:"identifier,omitempty" jsonschema:"interface_from_type only: source struct name"`
	Source     string `json:"source,omitempty" jsonschema:"impl_from_interface and mock_from_interface: fully qualified interface name (e.g. io.ReadCloser)"`
	Target     string `json:"target,omitempty" jsonschema:"naming parameter — interface_from_type: name of the new interface; impl_from_interface: receiver type for the stubs (e.g. *MyStruct); mock_from_interface: name of the mock struct (e.g. MockBookRepository)"`
	Out        string `json:"out,omitempty" jsonschema:"interface_from_type only: optional output file path for the interface (default: same file as the source struct)"`
	MockFile   string `json:"mock_file,omitempty" jsonschema:"interface_from_type only: also generate a mock for the new interface in this file"`
	MockName   string `json:"mock_name,omitempty" jsonschema:"interface_from_type only: name of the mock struct to generate alongside (paired with mock_file)"`
	Preview    bool   `json:"preview,omitempty" jsonschema:"if true, return a unified diff without writing"`
}

const deriveDescription = `Generate code derived from an existing symbol. kind selects what to derive:
- interface_from_type: derive an interface from a struct's exported methods. Required: file, identifier (struct), target (new interface name). Optional: out (interface output file), mock_file + mock_name (also generate a mock).
- impl_from_interface: generate method stubs on a receiver to satisfy an interface (already-implemented methods are skipped, stubs marked '// TODO(go-surgeon): implement'). Required: file, source (fully-qualified interface, e.g. io.ReadCloser), target (receiver, e.g. *MyStruct).
- mock_from_interface: generate a function-field mock for an interface you don't own (stdlib, third-party). For your own interfaces, prefer 'interface' action=add with mock_file. Required: file, source (fully-qualified interface), target (mock struct name, e.g. MockReader).
preview=true returns a unified diff without writing.`

func registerCodegenTools(s *mcp.Server, commands service.SurgeonCommands) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        "derive",
		Description: deriveDescription,
	}, func(ctx context.Context, req *mcp.CallToolRequest, in deriveInput) (*mcp.CallToolResult, any, error) {
		switch in.Kind {
		case "":
			return errorResultWithCode("kind is required: interface_from_type, impl_from_interface, or mock_from_interface", nil), nil, nil
		case "interface_from_type":
			return runDeriveInterfaceFromType(ctx, commands, in)
		case "impl_from_interface":
			return runDeriveImplFromInterface(ctx, commands, in)
		case "mock_from_interface":
			return runDeriveMockFromInterface(ctx, commands, in)
		default:
			return errorResultWithCode(fmt.Sprintf("unknown kind %q: must be interface_from_type, impl_from_interface, or mock_from_interface", in.Kind), nil), nil, nil
		}
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
}

func runDeriveInterfaceFromType(ctx context.Context, commands service.SurgeonCommands, in deriveInput) (*mcp.CallToolResult, any, error) {
	if in.Identifier == "" {
		return errorResultWithCode("derive kind=interface_from_type: identifier (source struct name) is required", nil), nil, nil
	}
	if in.Target == "" {
		return errorResultWithCode("derive kind=interface_from_type: target (new interface name) is required", nil), nil, nil
	}
	if in.Source != "" {
		return errorResultWithCode("derive kind=interface_from_type: source is not allowed (use identifier for the source struct)", nil), nil, nil
	}
	if (in.MockFile == "") != (in.MockName == "") {
		return errorResultWithCode("derive kind=interface_from_type: mock_file and mock_name must be both set or both omitted", nil), nil, nil
	}
	if err := validateGoFile(in.File); err != nil {
		return err, nil, nil
	}
	reqDomain := domain.ExtractInterfaceRequest{
		FilePath:      in.File,
		StructName:    in.Identifier,
		InterfaceName: in.Target,
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
	msg := fmt.Sprintf("SUCCESS: Extracted interface %s into %s", in.Target, interfaceFile)
	if in.Preview {
		msg = fmt.Sprintf("PREVIEW: would extract interface %s into %s", in.Target, interfaceFile)
		if diff != "" {
			msg += "\n\n" + diff
		}
	}
	res := textResult(msg)
	res.StructuredContent = extractInterfaceOutput{InterfaceName: in.Target, InterfaceFile: interfaceFile, MockFile: in.MockFile, MockName: in.MockName}
	return res, nil, nil
}

func runDeriveImplFromInterface(ctx context.Context, commands service.SurgeonCommands, in deriveInput) (*mcp.CallToolResult, any, error) {
	if in.Source == "" {
		return errorResultWithCode("derive kind=impl_from_interface: source (fully qualified interface name) is required", nil), nil, nil
	}
	if in.Target == "" {
		return errorResultWithCode("derive kind=impl_from_interface: target (receiver type, e.g. *MyStruct) is required", nil), nil, nil
	}
	if in.Identifier != "" {
		return errorResultWithCode("derive kind=impl_from_interface: identifier is not allowed", nil), nil, nil
	}
	if in.Out != "" {
		return errorResultWithCode("derive kind=impl_from_interface: out is not allowed (interface_from_type only)", nil), nil, nil
	}
	if in.MockFile != "" || in.MockName != "" {
		return errorResultWithCode("derive kind=impl_from_interface: mock_file/mock_name are not allowed (interface_from_type only)", nil), nil, nil
	}
	if err := validateGoFile(in.File); err != nil {
		return err, nil, nil
	}
	reqDomain := domain.ImplementRequest{
		Interface: in.Source,
		Receiver:  in.Target,
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
		res.StructuredContent = implementOutput{File: in.File, Interface: in.Source, Receiver: in.Target, Stubs: []string{}}
		return res, nil, nil
	}

	var sb strings.Builder
	verb := "Generated"
	if in.Preview {
		verb = "PREVIEW — would generate"
	}
	fmt.Fprintf(&sb, "%s %d missing methods for %s:\n\n", verb, len(results), in.Source)
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
	res.StructuredContent = implementOutput{File: in.File, Interface: in.Source, Receiver: in.Target, Stubs: stubs}
	return res, nil, nil
}

func runDeriveMockFromInterface(ctx context.Context, commands service.SurgeonCommands, in deriveInput) (*mcp.CallToolResult, any, error) {
	if in.Source == "" {
		return errorResultWithCode("derive kind=mock_from_interface: source (fully qualified interface name) is required", nil), nil, nil
	}
	if in.Target == "" {
		return errorResultWithCode("derive kind=mock_from_interface: target (mock struct name) is required", nil), nil, nil
	}
	if in.Identifier != "" {
		return errorResultWithCode("derive kind=mock_from_interface: identifier is not allowed", nil), nil, nil
	}
	if in.Out != "" {
		return errorResultWithCode("derive kind=mock_from_interface: out is not allowed (interface_from_type only)", nil), nil, nil
	}
	if in.MockFile != "" || in.MockName != "" {
		return errorResultWithCode("derive kind=mock_from_interface: mock_file/mock_name are not allowed (interface_from_type only); use target for the mock struct name", nil), nil, nil
	}
	if err := validateGoFile(in.File); err != nil {
		return err, nil, nil
	}
	reqDomain := domain.MockRequest{
		Interface: in.Source,
		Receiver:  in.Target,
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
	res.StructuredContent = mockOutput{File: in.File, Interface: in.Source, MockName: in.Target}
	return res, nil, nil
}
