package mcp

import (
	"context"
	"fmt"

	"github.com/JLugagne/go-surgeon/internal/surgeon/domain"
	"github.com/JLugagne/go-surgeon/internal/surgeon/domain/service"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// --- Interface tool ---

type interfaceInput struct {
	Action     string `json:"action" jsonschema:"action: add | update | delete (required)"`
	File       string `json:"file" jsonschema:"file containing the interface (required)"`
	Identifier string `json:"identifier,omitempty" jsonschema:"interface name (required for update and delete; recommended for add)"`
	Content    string `json:"content,omitempty" jsonschema:"raw Go interface source, no package declaration or imports (required for add; optional for update to rewrite the body)"`
	MockFile   string `json:"mock_file,omitempty" jsonschema:"target file for the generated mock (add/update); for delete, the file containing the mock to remove"`
	MockName   string `json:"mock_name,omitempty" jsonschema:"name of the mock struct"`
	Doc        string `json:"doc,omitempty" jsonschema:"set or replace the doc comment (raw text without // prefix, update only)"`
	StripDoc   bool   `json:"strip_doc,omitempty" jsonschema:"remove the existing doc comment (update only)"`
	DeleteMock bool   `json:"delete_mock,omitempty" jsonschema:"delete only: also remove the mock struct, its methods and its compile-time assertion from mock_file; requires mock_file and mock_name"`
	Preview    bool   `json:"preview,omitempty" jsonschema:"if true, return diff without writing the file"`
}

const interfaceToolDescription = `Manage interfaces and their mocks atomically. The action field selects the sub-operation:

- action=add: create a new interface. Requires file and content (raw Go interface body or full declaration). Optional mock_file + mock_name also generate a function-field mock with a compile-time var assertion. Prefer this over create for interfaces.
  Example: {"action": "add", "file": "repo.go", "identifier": "Repository", "content": "type Repository interface { FindByID(id string) (*Entity, error) }", "mock_file": "mock_repo.go", "mock_name": "MockRepository"}

- action=update: replace an interface wholesale and regenerate its mock. Requires file and identifier. Pass content to rewrite the interface body; pass doc to set/replace the doc comment, or strip_doc=true to remove it. mock_file + mock_name keep the mock in sync. For single-method changes, prefer patch target=interface.
  Example: {"action": "update", "file": "repo.go", "identifier": "Repository", "content": "type Repository interface { FindByID(id string) (*Entity, error); Delete(id string) error }", "mock_file": "mock_repo.go", "mock_name": "MockRepository"}

- action=delete: remove an interface. Requires file and identifier. Pass delete_mock=true with mock_file + mock_name to also remove the mock struct, its methods, and the var assertion (mock file is kept even if empty).
  Example: {"action": "delete", "file": "repo.go", "identifier": "Repository", "delete_mock": true, "mock_file": "mock_repo.go", "mock_name": "MockRepository"}

preview=true returns a unified diff without writing for any action.`

func registerInterfaceTools(s *mcp.Server, commands service.SurgeonCommands) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        "interface",
		Description: interfaceToolDescription,
	}, func(ctx context.Context, req *mcp.CallToolRequest, in interfaceInput) (*mcp.CallToolResult, any, error) {
		// Action discriminator validation.
		switch in.Action {
		case "":
			return errorResultWithCode("ERROR (interface): action is required: add, update, or delete", nil), nil, nil
		case "add", "update", "delete":
			// ok
		default:
			return errorResultWithCode(fmt.Sprintf("ERROR (interface): unknown action %q — must be one of: add, update, delete", in.Action), nil), nil, nil
		}

		// Cross-field validation (action-scoped).
		if in.DeleteMock && in.Action != "delete" {
			return errorResultWithCode(fmt.Sprintf("ERROR (interface): delete_mock is only valid with action=delete (got action=%q)", in.Action), nil), nil, nil
		}
		if in.DeleteMock && (in.MockFile == "" || in.MockName == "") {
			return errorResultWithCode("ERROR (interface): delete_mock=true requires both mock_file and mock_name", nil), nil, nil
		}
		if in.StripDoc && in.Action != "update" {
			return errorResultWithCode(fmt.Sprintf("ERROR (interface): strip_doc is only valid with action=update (got action=%q)", in.Action), nil), nil, nil
		}
		if in.Doc != "" && in.Action != "update" {
			return errorResultWithCode(fmt.Sprintf("ERROR (interface): doc is only valid with action=update (got action=%q)", in.Action), nil), nil, nil
		}
		if in.Action == "add" && in.Content == "" {
			return errorResultWithCode("ERROR (interface): content is required for action=add", nil), nil, nil
		}
		if in.Action == "update" && in.Content == "" && in.Doc == "" && !in.StripDoc {
			return errorResultWithCode("ERROR (interface): action=update requires at least one of content, doc, or strip_doc=true", nil), nil, nil
		}

		if errResult := validateGoFile(in.File); errResult != nil {
			return errResult, nil, nil
		}
		if in.Action == "delete" && in.MockFile != "" {
			if errResult := validateGoFile(in.MockFile); errResult != nil {
				return errResult, nil, nil
			}
		}

		reqDomain := domain.InterfaceActionRequest{
			FilePath:   in.File,
			Identifier: in.Identifier,
			Content:    in.Content,
			MockFile:   in.MockFile,
			MockName:   in.MockName,
			Doc:        in.Doc,
			StripDoc:   in.StripDoc,
			DeleteMock: in.DeleteMock,
		}

		// Pick the domain command per action.
		var cmd func(service.SurgeonCommands) (string, []string, error)
		var actionLabel string
		var symbolAction string
		switch in.Action {
		case "add":
			actionLabel = "interface add"
			symbolAction = "add_interface"
			cmd = func(sc service.SurgeonCommands) (string, []string, error) {
				return sc.AddInterface(ctx, reqDomain)
			}
		case "update":
			actionLabel = "interface update"
			symbolAction = "update_interface"
			cmd = func(sc service.SurgeonCommands) (string, []string, error) {
				return sc.UpdateInterface(ctx, reqDomain)
			}
		case "delete":
			actionLabel = "interface delete"
			symbolAction = "delete_interface"
			cmd = func(sc service.SurgeonCommands) (string, []string, error) {
				return sc.DeleteInterface(ctx, reqDomain)
			}
		}

		var result string
		var addedImports []string
		var diff string
		var writtenFiles []string
		var err error
		if in.Preview {
			diff, writtenFiles, err = runPreview(ctx, commands, func(sc service.SurgeonCommands) error {
				var innerErr error
				result, addedImports, innerErr = cmd(sc)
				return innerErr
			})
		} else {
			result, addedImports, err = cmd(commands)
		}
		if err != nil {
			return errorResultWithCode(fmt.Sprintf("ERROR (%s): %v", actionLabel, err), err), nil, nil
		}

		// Compose files_modified — preserve previous per-action shape.
		files := []string{in.File}
		switch in.Action {
		case "add", "update":
			if in.MockFile != "" {
				files = append(files, in.MockFile)
			}
		case "delete":
			if in.DeleteMock && in.MockFile != "" {
				files = append(files, in.MockFile)
			}
		}
		if in.Preview && len(writtenFiles) > 0 {
			files = writtenFiles
		}

		msg := result
		if in.Preview {
			msg = fmt.Sprintf("PREVIEW (%s): %s", actionLabel, result)
			if diff != "" {
				msg += "\n\n" + diff
			}
		}
		res := textResult(msg)
		res.StructuredContent = editOutput{
			FilesModified: files,
			Symbols:       []symbolEdit{{Action: symbolAction, Identifier: in.Identifier, File: in.File}},
			AddedImports:  addedImports,
			Preview:       in.Preview,
			Diff:          diff,
		}
		return res, nil, nil
	})
}
