package mcp

import (
	"context"
	"fmt"

	"github.com/JLugagne/go-surgeon/internal/surgeon/domain"
	"github.com/JLugagne/go-surgeon/internal/surgeon/domain/service"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// --- Interface tools ---

type interfaceInput struct {
	File       string `json:"file" jsonschema:"file containing the interface, required"`
	Identifier string `json:"identifier,omitempty" jsonschema:"interface name, required for update and delete"`
	Content    string `json:"content,omitempty" jsonschema:"raw Go interface source, no package declaration or imports"`
	MockFile   string `json:"mock_file,omitempty" jsonschema:"target file for the generated mock (add/update), or file containing the mock to delete (delete)"`
	MockName   string `json:"mock_name,omitempty" jsonschema:"name of the mock struct"`
	Doc        string `json:"doc,omitempty" jsonschema:"set or replace the doc comment (raw text without // prefix, update only)"`
	StripDoc   bool   `json:"strip_doc,omitempty" jsonschema:"remove the existing doc comment (update only)"`
	DeleteMock bool   `json:"delete_mock,omitempty" jsonschema:"delete only: also remove the mock struct, its methods and its compile-time assertion from mock_file; requires mock_file and mock_name"`
	Preview    bool   `json:"preview,omitempty" jsonschema:"if true, return diff without writing the file"`
}

func registerInterfaceTools(s *mcp.Server, commands service.SurgeonCommands) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        "add_interface",
		Description: "Add a new interface and its mock atomically. Set mock_file + mock_name to generate a function-field mock with a compile-time var assertion. Prefer this over create for interfaces. preview=true returns a unified diff without writing.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in interfaceInput) (*mcp.CallToolResult, any, error) {
		if err := validateGoFile(in.File); err != nil {
			return err, nil, nil
		}
		reqDomain := domain.InterfaceActionRequest{
			FilePath: in.File, Identifier: in.Identifier, Content: in.Content,
			MockFile: in.MockFile, MockName: in.MockName,
		}
		var result string
		var addedImports []string
		var diff string
		var writtenFiles []string
		var err error
		if in.Preview {
			diff, writtenFiles, err = runPreview(ctx, commands, func(sc service.SurgeonCommands) error {
				var innerErr error
				result, addedImports, innerErr = sc.AddInterface(ctx, reqDomain)
				return innerErr
			})
		} else {
			result, addedImports, err = commands.AddInterface(ctx, reqDomain)
		}
		if err != nil {
			return errorResultWithCode(fmt.Sprintf("ERROR (add_interface): %v", err), err), nil, nil
		}
		files := []string{in.File}
		if in.MockFile != "" {
			files = append(files, in.MockFile)
		}
		if in.Preview && len(writtenFiles) > 0 {
			files = writtenFiles
		}
		msg := result
		if in.Preview {
			msg = fmt.Sprintf("PREVIEW (add_interface): %s", result)
			if diff != "" {
				msg += "\n\n" + diff
			}
		}
		res := textResult(msg)
		res.StructuredContent = editOutput{
			FilesModified: files,
			Symbols:       []symbolEdit{{Action: "add_interface", Identifier: in.Identifier, File: in.File}},
			AddedImports:  addedImports,
			Preview:       in.Preview,
			Diff:          diff,
		}
		return res, nil, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "update_interface",
		Description: "Replace an interface wholesale and regenerate its mock. For single-method changes, prefer patch_interface. Pass mock_file + mock_name to keep the mock in sync. Doc comments are kept unless you set doc or strip_doc=true. preview=true returns a unified diff without writing.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in interfaceInput) (*mcp.CallToolResult, any, error) {
		if err := validateGoFile(in.File); err != nil {
			return err, nil, nil
		}
		reqDomain := domain.InterfaceActionRequest{
			FilePath: in.File, Identifier: in.Identifier, Content: in.Content,
			MockFile: in.MockFile, MockName: in.MockName,
			Doc: in.Doc, StripDoc: in.StripDoc,
		}
		var result string
		var addedImports []string
		var diff string
		var writtenFiles []string
		var err error
		if in.Preview {
			diff, writtenFiles, err = runPreview(ctx, commands, func(sc service.SurgeonCommands) error {
				var innerErr error
				result, addedImports, innerErr = sc.UpdateInterface(ctx, reqDomain)
				return innerErr
			})
		} else {
			result, addedImports, err = commands.UpdateInterface(ctx, reqDomain)
		}
		if err != nil {
			return errorResultWithCode(fmt.Sprintf("ERROR (update_interface): %v", err), err), nil, nil
		}
		files := []string{in.File}
		if in.MockFile != "" {
			files = append(files, in.MockFile)
		}
		if in.Preview && len(writtenFiles) > 0 {
			files = writtenFiles
		}
		msg := result
		if in.Preview {
			msg = fmt.Sprintf("PREVIEW (update_interface): %s", result)
			if diff != "" {
				msg += "\n\n" + diff
			}
		}
		res := textResult(msg)
		res.StructuredContent = editOutput{
			FilesModified: files,
			Symbols:       []symbolEdit{{Action: "update_interface", Identifier: in.Identifier, File: in.File}},
			AddedImports:  addedImports,
			Preview:       in.Preview,
			Diff:          diff,
		}
		return res, nil, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "delete_interface",
		Description: "Delete an interface. Pass delete_mock=true with mock_file + mock_name to also remove the mock struct, its methods, and the var assertion. The mock file is kept even if empty. preview=true returns a unified diff without writing.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in interfaceInput) (*mcp.CallToolResult, any, error) {
		if err := validateGoFile(in.File); err != nil {
			return err, nil, nil
		}
		if in.MockFile != "" {
			if err := validateGoFile(in.MockFile); err != nil {
				return err, nil, nil
			}
		}
		reqDomain := domain.InterfaceActionRequest{
			FilePath:   in.File,
			Identifier: in.Identifier,
			MockFile:   in.MockFile,
			MockName:   in.MockName,
			DeleteMock: in.DeleteMock,
		}
		var result string
		var addedImports []string
		var diff string
		var writtenFiles []string
		var err error
		if in.Preview {
			diff, writtenFiles, err = runPreview(ctx, commands, func(sc service.SurgeonCommands) error {
				var innerErr error
				result, addedImports, innerErr = sc.DeleteInterface(ctx, reqDomain)
				return innerErr
			})
		} else {
			result, addedImports, err = commands.DeleteInterface(ctx, reqDomain)
		}
		if err != nil {
			return errorResultWithCode(fmt.Sprintf("ERROR (delete_interface): %v", err), err), nil, nil
		}
		files := []string{in.File}
		if in.DeleteMock && in.MockFile != "" {
			files = append(files, in.MockFile)
		}
		if in.Preview && len(writtenFiles) > 0 {
			files = writtenFiles
		}
		msg := result
		if in.Preview {
			msg = fmt.Sprintf("PREVIEW (delete_interface): %s", result)
			if diff != "" {
				msg += "\n\n" + diff
			}
		}
		res := textResult(msg)
		res.StructuredContent = editOutput{
			FilesModified: files,
			Symbols:       []symbolEdit{{Action: "delete_interface", Identifier: in.Identifier, File: in.File}},
			AddedImports:  addedImports,
			Preview:       in.Preview,
			Diff:          diff,
		}
		return res, nil, nil
	})
}
