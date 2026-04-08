package commands

import (
	"fmt"
	"io"
	"os"

	"github.com/JLugagne/go-surgeon/internal/surgeon/domain"
	"github.com/JLugagne/go-surgeon/internal/surgeon/domain/service"
	"github.com/spf13/cobra"
)

func NewActionCommand(surgeon service.SurgeonCommands, actionType domain.ActionType, name string, needsID, needsStdin bool) *cobra.Command {
	var file string
	var id string

	cmd := &cobra.Command{
		Use:     name,
		Short:   actionShortDesc(actionType),
		Long:    actionLongDesc(actionType),
		Example: actionExample(actionType),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			var content string
			if needsStdin {
				stat, err := os.Stdin.Stat()
				if err == nil && (stat.Mode()&os.ModeCharDevice) != 0 {
					return fmt.Errorf("ERROR (%s): Go source code required on stdin\nExample: cat file.go | go-surgeon %s --file <path>", name, name)
				}
				data, err := io.ReadAll(os.Stdin)
				if err != nil {
					return fmt.Errorf("ERROR (%s): failed to read stdin: %w", name, err)
				}
				content = string(data)
			}

			action := domain.Action{
				Action:     actionType,
				FilePath:   file,
				Identifier: id,
				Content:    content,
			}

			result, err := surgeon.ExecutePlan(ctx, domain.Plan{Actions: []domain.Action{action}})
			if err != nil {
				return fmt.Errorf("ERROR (%s): %w", name, err)
			}

			for _, w := range result.Warnings {
				fmt.Printf("WARNING (%s): %s\n", name, w)
			}
			fmt.Printf("SUCCESS (%s): %s\n", name, actionSuccessMessage(actionType, file, id, content))
			return nil
		},
	}

	cmd.Flags().StringVarP(&file, "file", "f", "", "Target file path (required)")
	_ = cmd.MarkFlagRequired("file")
	cmd.Flags().StringVarP(&id, "id", "i", "", "AST identifier, e.g. FuncName or Receiver.Method")
	if needsID {
		_ = cmd.MarkFlagRequired("id")
	}
	return cmd
}

func actionShortDesc(actionType domain.ActionType) string {
	switch actionType {
	case domain.ActionTypeCreateFile:
		return "Create a new Go file from stdin"
	case domain.ActionTypeReplaceFile:
		return "Replace an existing Go file from stdin"
	case domain.ActionTypeAddFunc:
		return "Append a function or method to a file"
	case domain.ActionTypeUpdateFunc:
		return "Replace a function or method by AST identifier"
	case domain.ActionTypeDeleteFunc:
		return "Delete a function or method by AST identifier"
	case domain.ActionTypeAddStruct:
		return "Append a struct or type declaration to a file"
	case domain.ActionTypeUpdateStruct:
		return "Replace a struct or type declaration by AST identifier"
	case domain.ActionTypeDeleteStruct:
		return "Delete a struct and all its methods by AST identifier"
	default:
		return "Apply an AST action to a file"
	}
}

func actionLongDesc(actionType domain.ActionType) string {
	switch actionType {
	case domain.ActionTypeCreateFile:
		return `Creates a new Go file at --file from Go source provided on stdin.

The file must not already exist. goimports runs automatically — do not include import
statements or a package declaration in the source.`
	case domain.ActionTypeReplaceFile:
		return `Replaces the entire contents of --file with Go source provided on stdin.

The file must already exist. goimports runs automatically. Prefer the targeted
update-func / update-struct commands for single-symbol changes; use replace-file
only when the whole file must be rewritten.`
	case domain.ActionTypeAddFunc:
		return `Appends a function or method to --file from Go source provided on stdin.

goimports runs automatically. Provide only the function declaration — no package
statement or imports needed.`
	case domain.ActionTypeUpdateFunc:
		return `Replaces the function or method identified by --id in --file with the source
provided on stdin.

--id accepts "FuncName" for free functions or "Receiver.Method" for methods
(e.g. "BookHandler.Handle"). The entire function including its signature must
be provided on stdin. goimports runs automatically.`
	case domain.ActionTypeDeleteFunc:
		return `Deletes the function or method identified by --id from --file.

--id accepts "FuncName" for free functions or "Receiver.Method" for methods.
The function's doc comment is also removed.`
	case domain.ActionTypeAddStruct:
		return `Appends a struct or type declaration to --file from Go source provided on stdin.

goimports runs automatically. Provide only the type declaration — no package
statement or imports needed.`
	case domain.ActionTypeUpdateStruct:
		return `Replaces the struct or type declaration identified by --id in --file with the
source provided on stdin.

The entire type declaration must be provided on stdin. goimports runs automatically.`
	case domain.ActionTypeDeleteStruct:
		return `Deletes the struct or type declaration identified by --id from --file.

All methods defined on that struct within the same file are also deleted.`
	default:
		return ""
	}
}

func actionExample(actionType domain.ActionType) string {
	switch actionType {
	case domain.ActionTypeCreateFile:
		return `  cat <<'EOF' | go-surgeon create-file --file internal/catalog/domain/book.go
  package domain

  type Book struct {
    ID    string
    Title string
  }
  EOF`
	case domain.ActionTypeReplaceFile:
		return `  cat <<'EOF' | go-surgeon replace-file --file internal/catalog/domain/book.go
  package domain

  type Book struct {
    ID        string
    Title     string
    CreatedAt time.Time
  }
  EOF`
	case domain.ActionTypeAddFunc:
		return `  # Add a free function
  cat <<'EOF' | go-surgeon add-func --file internal/catalog/domain/book.go
  func NewBook(title string) (*Book, error) {
    if title == "" {
      return nil, errors.New("title required")
    }
    return &Book{Title: title}, nil
  }
  EOF

  # Add a method  (-f is short for --file)
  cat <<'EOF' | go-surgeon add-func -f internal/catalog/domain/book.go
  func (b *Book) Validate() error {
    if b.Title == "" {
      return errors.New("title required")
    }
    return nil
  }
  EOF`
	case domain.ActionTypeUpdateFunc:
		return `  # Update a free function  (-f --file, -i --id)
  cat <<'EOF' | go-surgeon update-func --file internal/catalog/domain/book.go --id NewBook
  func NewBook(title, author string) (*Book, error) {
    if title == "" {
      return nil, errors.New("title required")
    }
    return &Book{Title: title, Author: author}, nil
  }
  EOF

  # Update a method  (Receiver.Method form)
  cat <<'EOF' | go-surgeon update-func -f internal/catalog/domain/book.go -i Book.Validate
  func (b *Book) Validate() error {
    return nil
  }
  EOF`
	case domain.ActionTypeDeleteFunc:
		return `  # Delete a free function
  go-surgeon delete-func --file internal/catalog/domain/book.go --id NewBook

  # Delete a method  (-f --file, -i --id)
  go-surgeon delete-func -f internal/catalog/domain/book.go -i Book.Validate`
	case domain.ActionTypeAddStruct:
		return `  cat <<'EOF' | go-surgeon add-struct --file internal/catalog/domain/book.go
  type BookStatus string

  const (
    BookStatusDraft     BookStatus = "draft"
    BookStatusPublished BookStatus = "published"
  )
  EOF`
	case domain.ActionTypeUpdateStruct:
		return `  cat <<'EOF' | go-surgeon update-struct --file internal/catalog/domain/book.go --id Book
  type Book struct {
    ID        string
    Title     string
    Author    string
    CreatedAt time.Time
  }
  EOF`
	case domain.ActionTypeDeleteStruct:
		return `  go-surgeon delete-struct --file internal/catalog/domain/book.go --id Book`
	default:
		return ""
	}
}

func actionSuccessMessage(actionType domain.ActionType, file, id, content string) string {
	switch actionType {
	case domain.ActionTypeCreateFile:
		return fmt.Sprintf("Created %s", file)
	case domain.ActionTypeReplaceFile:
		return fmt.Sprintf("Replaced %s", file)
	case domain.ActionTypeAddFunc, domain.ActionTypeAddStruct:
		name := id
		if name == "" {
			name = extractFirstIdentifier(content)
		}
		return fmt.Sprintf("Added %s to %s", name, file)
	case domain.ActionTypeUpdateFunc, domain.ActionTypeUpdateStruct:
		return fmt.Sprintf("Updated %s in %s", id, file)
	case domain.ActionTypeDeleteFunc, domain.ActionTypeDeleteStruct:
		return fmt.Sprintf("Deleted %s from %s", id, file)
	default:
		return fmt.Sprintf("Applied action to %s", file)
	}
}

func extractFirstIdentifier(src string) string {
	for _, line := range splitLines(src) {
		if len(line) > 5 {
			if line[:5] == "func " {
				rest := line[5:]
				if len(rest) > 0 && rest[0] == '(' {
					end := indexOfByte(rest, ')')
					if end >= 0 && len(rest) > end+2 {
						rest = rest[end+2:]
					}
				}
				end := indexOfAnyByte(rest, "( \t")
				if end > 0 {
					return rest[:end]
				}
			}
			if line[:5] == "type " {
				rest := line[5:]
				end := indexOfAnyByte(rest, " \t")
				if end > 0 {
					return rest[:end]
				}
			}
		}
	}
	return "content"
}

func splitLines(s string) []string {
	var lines []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			lines = append(lines, s[start:i])
			start = i + 1
		}
	}
	if start < len(s) {
		lines = append(lines, s[start:])
	}
	return lines
}

func indexOfByte(s string, c byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == c {
			return i
		}
	}
	return -1
}

func indexOfAnyByte(s string, chars string) int {
	for i := 0; i < len(s); i++ {
		for j := 0; j < len(chars); j++ {
			if s[i] == chars[j] {
				return i
			}
		}
	}
	return -1
}
