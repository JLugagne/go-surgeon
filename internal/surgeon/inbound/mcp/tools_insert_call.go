package mcp

import (
	"context"
	"fmt"
	"strings"

	"github.com/JLugagne/go-surgeon/internal/surgeon/domain"
	"github.com/JLugagne/go-surgeon/internal/surgeon/domain/service"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// --- Insert-call tool ---

type insertCallInput struct {
	File     string `json:"file" jsonschema:"target Go file"`
	Function string `json:"function" jsonschema:"function identifier: FuncName or Receiver.Method"`
	Call     string `json:"call" jsonschema:"statement to insert, e.g. setupPayOrderRoute(mux, app)"`
	Position string `json:"position,omitempty" jsonschema:"where to insert: before-return (default), end-of-body, or after:<marker>"`
	Preview  bool   `json:"preview,omitempty" jsonschema:"if true, return diff without writing the file"`
}

func registerInsertCallTool(s *mcp.Server, commands service.SurgeonCommands) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        "insert_call",
		Description: "Insert one statement into a function body. Idempotent: skipped if already present. position: 'before-return' (default), 'end-of-body', or 'after:<marker>'.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in insertCallInput) (*mcp.CallToolResult, any, error) {
		if err := validateGoFile(in.File); err != nil {
			return err, nil, nil
		}
		pos := domain.InsertPosition(in.Position)
		if pos == "" {
			pos = domain.InsertBeforeReturn
		}
		result, err := commands.ExecutePlan(ctx, domain.Plan{
			Preview: in.Preview,
			Actions: []domain.Action{{
				Action:     domain.ActionTypeInsertCall,
				FilePath:   in.File,
				Identifier: in.Function,
				Content:    in.Call,
				Position:   pos,
			}},
		})
		if err != nil {
			return errorResultWithCode(fmt.Sprintf("ERROR (insert_call): %v", err), err), nil, nil
		}
		var sb strings.Builder
		for _, w := range result.Warnings {
			fmt.Fprintf(&sb, "WARNING: %s\n", w)
		}
		verb := "SUCCESS"
		if result.Preview {
			verb = "PREVIEW"
		}
		fmt.Fprintf(&sb, "%s (insert_call): %d files modified", verb, result.FilesModified)
		if result.Diff != "" {
			sb.WriteString("\n\n")
			sb.WriteString(result.Diff)
		}
		res := textResult(sb.String())
		res.StructuredContent = editOutput{
			FilesModified: result.Files,
			Symbols:       []symbolEdit{{Action: "insert_call", Identifier: in.Function, File: in.File}},
			Warnings:      result.Warnings,
			AddedImports:  result.AddedImports,
			Preview:       result.Preview,
			Diff:          result.Diff,
		}
		return res, nil, nil
	})
}
