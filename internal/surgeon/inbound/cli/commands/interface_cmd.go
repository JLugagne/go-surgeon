package commands

import (
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/JLugagne/go-surgeon/internal/surgeon/domain"
	"github.com/JLugagne/go-surgeon/internal/surgeon/domain/service"
	"github.com/spf13/cobra"
)

func NewInterfaceCommand(surgeon service.SurgeonCommands, actionType domain.ActionType, name string) *cobra.Command {
	var file string
	var id string
	var mockFile string
	var mockName string
	var doc string
	var stripDoc bool

	isDelete := actionType == domain.ActionTypeDeleteInterface
	isUpdate := actionType == domain.ActionTypeUpdateInterface
	needsID := isUpdate || isDelete

	cmd := &cobra.Command{
		Use:     name,
		Short:   interfaceShortDesc(actionType),
		Long:    interfaceLongDesc(actionType),
		Example: interfaceExample(actionType),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			var content string
			if !isDelete {
				stat, err := os.Stdin.Stat()
				if err == nil && (stat.Mode()&os.ModeCharDevice) != 0 {
					return fmt.Errorf("ERROR (%s): interface source required on stdin\nExample: cat iface.go | go-surgeon %s --file <path>", name, name)
				}
				data, err := io.ReadAll(os.Stdin)
				if err != nil {
					return fmt.Errorf("ERROR (%s): failed to read stdin: %w", name, err)
				}
				content = string(data)
			}

			req := domain.InterfaceActionRequest{
				FilePath:   file,
				Identifier: id,
				Content:    content,
				MockFile:   mockFile,
				MockName:   mockName,
				Doc:        doc,
				StripDoc:   stripDoc,
			}

			var (
				result string
				err    error
			)
			switch actionType {
			case domain.ActionTypeAddInterface:
				result, err = surgeon.AddInterface(ctx, req)
			case domain.ActionTypeUpdateInterface:
				result, err = surgeon.UpdateInterface(ctx, req)
			case domain.ActionTypeDeleteInterface:
				result, err = surgeon.DeleteInterface(ctx, req)
			default:
				return fmt.Errorf("ERROR (%s): unknown action type %s", name, actionType)
			}
			if err != nil {
				hint := interfaceErrorHint(err, actionType, file, id)
				if hint != "" {
					return fmt.Errorf("ERROR (%s): %w\n%s", name, err, hint)
				}
				return fmt.Errorf("ERROR (%s): %w", name, err)
			}
			fmt.Printf("%s\n", result)
			return nil
		},
	}

	cmd.Flags().StringVarP(&file, "file", "f", "", "File containing the interface (required)")
	_ = cmd.MarkFlagRequired("file")
	cmd.Flags().StringVarP(&id, "id", "i", "", "Interface name (required for update/delete)")
	if needsID {
		_ = cmd.MarkFlagRequired("id")
	}
	if !isDelete {
		cmd.Flags().StringVarP(&mockFile, "mock-file", "m", "", "Target file for the generated mock")
		cmd.Flags().StringVarP(&mockName, "mock-name", "n", "", "Name of the mock struct")
	}
	if isUpdate {
		cmd.Flags().StringVar(&doc, "doc", "", "Set or replace the doc comment (raw text, // prefix added automatically)")
		cmd.Flags().BoolVar(&stripDoc, "strip-doc", false, "Remove the existing doc comment")
	}
	return cmd
}

func interfaceShortDesc(actionType domain.ActionType) string {
	switch actionType {
	case domain.ActionTypeAddInterface:
		return "Add a new interface and generate its mock"
	case domain.ActionTypeUpdateInterface:
		return "Update an interface and regenerate its mock"
	case domain.ActionTypeDeleteInterface:
		return "Delete an interface"
	default:
		return "Manage an interface"
	}
}

func interfaceLongDesc(actionType domain.ActionType) string {
	switch actionType {
	case domain.ActionTypeAddInterface:
		return `Reads an interface definition from stdin and writes it to --file, then generates
a function-field mock at --mock-file.

The mock struct has a FuncField per method, delegation methods that call the field, and
a compile-time check (var _ MyInterface = (*MockMyInterface)(nil)).

Omit --mock-file and --mock-name to skip mock generation.`
	case domain.ActionTypeUpdateInterface:
		return `Replaces the interface identified by --id in --file with the source read from stdin,
then regenerates the mock at --mock-file from scratch.

--id is the interface name as it appears in the source (e.g. "BookRepository").
Omit --mock-file and --mock-name to skip mock regeneration.`
	case domain.ActionTypeDeleteInterface:
		return `Removes the interface identified by --id from --file.

The mock file is NOT automatically deleted — the compile-time check
(var _ MyInterface = (*Mock)(nil)) will cause a build failure until the mock is
cleaned up manually. This is intentional: it forces you to explicitly handle
dependent tests.`
	default:
		return ""
	}
}

func interfaceExample(actionType domain.ActionType) string {
	switch actionType {
	case domain.ActionTypeAddInterface:
		return `  cat <<'EOF' | go-surgeon add-interface \
    --file internal/domain/repositories/book.go \
    --mock-file internal/domain/repositories/booktest/mock.go \
    --mock-name MockBookRepository
  type BookRepository interface {
    Create(ctx context.Context, book domain.Book) error
    FindByID(ctx context.Context, id domain.BookID) (*domain.Book, error)
  }
  EOF

  # Without mock generation
  cat <<'EOF' | go-surgeon add-interface --file internal/domain/repositories/book.go
  type BookRepository interface {
    Create(ctx context.Context, book domain.Book) error
  }
  EOF`
	case domain.ActionTypeUpdateInterface:
		return `  cat <<'EOF' | go-surgeon update-interface \
    --file internal/domain/repositories/book.go \
    --id BookRepository \
    --mock-file internal/domain/repositories/booktest/mock.go \
    --mock-name MockBookRepository
  type BookRepository interface {
    Create(ctx context.Context, book domain.Book) error
    FindByID(ctx context.Context, id domain.BookID) (*domain.Book, error)
    Delete(ctx context.Context, id domain.BookID) error
  }
  EOF`
	case domain.ActionTypeDeleteInterface:
		return `  go-surgeon delete-interface \
    --file internal/domain/repositories/book.go \
    --id BookRepository`
	default:
		return ""
	}
}

// interfaceErrorHint returns an actionable hint for known interface command errors.
func interfaceErrorHint(err error, actionType domain.ActionType, file, id string) string {
	var de *domain.Error
	if !errors.As(err, &de) {
		return ""
	}
	switch de.Code {
	case "NODE_ALREADY_EXISTS":
		if actionType == domain.ActionTypeAddInterface {
			name := extractQuotedName(de.Message)
			if name != "" {
				return fmt.Sprintf("Hint: use 'update-interface --file %s --id %s' to replace it.", file, name)
			}
			return fmt.Sprintf("Hint: use 'update-interface --file %s --id <InterfaceName>' to replace it.", file)
		}
	case "NODE_NOT_FOUND":
		if id != "" {
			return fmt.Sprintf("Hint: use 'go-surgeon symbol %s' to verify the interface exists.", id)
		}
		return "Hint: use 'go-surgeon symbol <InterfaceName>' to verify the interface exists."
	case "PARSE_ERROR":
		return "Hint: stdin must contain a complete interface declaration: 'type MyInterface interface { Method() error }'."
	case "FILE_NOT_FOUND":
		return fmt.Sprintf("Hint: '%s' not found. Use 'go-surgeon graph' to list packages, or check the --file path.", file)
	}
	return ""
}
