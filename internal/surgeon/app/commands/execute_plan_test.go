package commands_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/JLugagne/go-surgeon/internal/surgeon/app/commands"
	"github.com/JLugagne/go-surgeon/internal/surgeon/domain"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExecutePlan_ASTActions(t *testing.T) {
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "user.go")

	initialCode := `package main

type User struct {}

func (u *User) Save() error {
	return nil
}

func FreeFunc() {}
`
	err := os.WriteFile(filePath, []byte(initialCode), 0644)
	require.NoError(t, err)

	fs := &mockFS{
		files: map[string][]byte{
			filePath: []byte(initialCode),
		},
	}

	handler := commands.NewExecutePlanHandler(fs)

	t.Run("Update Method with Receiver (*User).Save", func(t *testing.T) {
		newContent := `func (u *User) Save() error {
	return fmt.Errorf("new")
}`
		plan := domain.Plan{
			Actions: []domain.Action{
				{
					Action:     domain.ActionTypeUpdateFunc,
					FilePath:   filePath,
					Identifier: "(*User).Save",
					Content:    newContent,
				},
			},
		}

		result, err := handler.ExecutePlan(context.Background(), plan)
		require.NoError(t, err)
		assert.Empty(t, result.Warnings)

		updated := string(fs.files[filePath])
		assert.Contains(t, updated, `return fmt.Errorf("new")`)
		assert.Contains(t, updated, `func FreeFunc() {}`)
	})

	t.Run("Update Method with Receiver User.Save", func(t *testing.T) {
		newContent := `func (u *User) Save() error {
	return fmt.Errorf("new2")
}`
		plan := domain.Plan{
			Actions: []domain.Action{
				{
					Action:     domain.ActionTypeUpdateFunc,
					FilePath:   filePath,
					Identifier: "User.Save",
					Content:    newContent,
				},
			},
		}

		result, err := handler.ExecutePlan(context.Background(), plan)
		require.NoError(t, err)
		assert.Empty(t, result.Warnings)

		updated := string(fs.files[filePath])
		assert.Contains(t, updated, `return fmt.Errorf("new2")`)
	})

	t.Run("Delete Method", func(t *testing.T) {
		plan := domain.Plan{
			Actions: []domain.Action{
				{
					Action:     domain.ActionTypeDeleteFunc,
					FilePath:   filePath,
					Identifier: "User.Save",
				},
			},
		}

		_, err := handler.ExecutePlan(context.Background(), plan)
		require.NoError(t, err)

		updated := string(fs.files[filePath])
		assert.NotContains(t, updated, `Save()`)
		assert.Contains(t, updated, `func FreeFunc() {}`)
	})

	t.Run("Update func falls back to add when not found", func(t *testing.T) {
		newContent := `func NewHelper() string {
	return "hello"
}`
		plan := domain.Plan{
			Actions: []domain.Action{
				{
					Action:     domain.ActionTypeUpdateFunc,
					FilePath:   filePath,
					Identifier: "NewHelper",
					Content:    newContent,
				},
			},
		}

		result, err := handler.ExecutePlan(context.Background(), plan)
		require.NoError(t, err)
		require.Len(t, result.Warnings, 1)
		assert.Contains(t, result.Warnings[0], "treated as add_func")

		updated := string(fs.files[filePath])
		assert.Contains(t, updated, `func NewHelper() string`)
	})
}

func TestAddFunc_DuplicateDetection(t *testing.T) {
	tmpDir := t.TempDir()
	filePath := tmpDir + "/foo.go"

	initialCode := `package test

type Foo struct{}

func (f *Foo) RegisterKey() {}
`
	fs := &mockFS{
		files: map[string][]byte{
			filePath: []byte(initialCode),
		},
	}
	handler := commands.NewExecutePlanHandler(fs)

	t.Run("add_func duplicate method returns error with existing body", func(t *testing.T) {
		plan := domain.Plan{
			Actions: []domain.Action{
				{
					Action:   domain.ActionTypeAddFunc,
					FilePath: filePath,
					Content:  "func (f *Foo) RegisterKey() {}\n",
				},
			},
		}

		_, err := handler.ExecutePlan(context.Background(), plan)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "NODE_ALREADY_EXISTS")
		assert.Contains(t, err.Error(), "RegisterKey")
		assert.Contains(t, err.Error(), "func (f *Foo) RegisterKey() {}")

		// File must not be modified
		assert.Equal(t, initialCode, string(fs.files[filePath]))
	})

	t.Run("add_func duplicate free function returns error with existing body", func(t *testing.T) {
		code := `package test

func Helper() string { return "hi" }
`
		fp2 := tmpDir + "/free.go"
		fs2 := &mockFS{files: map[string][]byte{fp2: []byte(code)}}
		h2 := commands.NewExecutePlanHandler(fs2)

		plan := domain.Plan{
			Actions: []domain.Action{
				{
					Action:   domain.ActionTypeAddFunc,
					FilePath: fp2,
					Content:  `func Helper() string { return "other" }` + "\n",
				},
			},
		}

		_, err := h2.ExecutePlan(context.Background(), plan)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "NODE_ALREADY_EXISTS")
		assert.Contains(t, err.Error(), "Helper")
	})

	t.Run("add_func on new file skips duplicate check", func(t *testing.T) {
		fp3 := tmpDir + "/new.go"
		fs3 := &mockFS{files: map[string][]byte{}}
		h3 := commands.NewExecutePlanHandler(fs3)

		plan := domain.Plan{
			Actions: []domain.Action{
				{
					Action:      domain.ActionTypeAddFunc,
					FilePath:    fp3,
					PackagePath: "test",
					Content:     "func (f *Foo) RegisterKey() {}\n",
				},
			},
		}

		_, err := h3.ExecutePlan(context.Background(), plan)
		require.NoError(t, err)
	})
}

func TestAddStruct_DuplicateDetection(t *testing.T) {
	tmpDir := t.TempDir()
	filePath := tmpDir + "/models.go"

	initialCode := `package test

type Bar struct{ X int }
`
	fs := &mockFS{
		files: map[string][]byte{
			filePath: []byte(initialCode),
		},
	}
	handler := commands.NewExecutePlanHandler(fs)

	t.Run("add_struct duplicate returns error with existing body", func(t *testing.T) {
		plan := domain.Plan{
			Actions: []domain.Action{
				{
					Action:   domain.ActionTypeAddStruct,
					FilePath: filePath,
					Content:  "type Bar struct{ Y string }\n",
				},
			},
		}

		_, err := handler.ExecutePlan(context.Background(), plan)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "NODE_ALREADY_EXISTS")
		assert.Contains(t, err.Error(), "Bar")
		assert.Contains(t, err.Error(), "type Bar struct")

		// File must not be modified
		assert.Equal(t, initialCode, string(fs.files[filePath]))
	})
}

func TestUpdateFunc_DocHandling(t *testing.T) {
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "doc.go")

	initialCode := `package main

// Save persists the user to the database.
func (u *User) Save() error {
	return nil
}

func NoDoc() {}
`

	t.Run("default preserves existing doc comment", func(t *testing.T) {
		fs := &mockFS{files: map[string][]byte{filePath: []byte(initialCode)}}
		handler := commands.NewExecutePlanHandler(fs)

		plan := domain.Plan{Actions: []domain.Action{{
			Action:     domain.ActionTypeUpdateFunc,
			FilePath:   filePath,
			Identifier: "User.Save",
			Content:    "func (u *User) Save() error {\n\treturn fmt.Errorf(\"updated\")\n}",
		}}}

		_, err := handler.ExecutePlan(context.Background(), plan)
		require.NoError(t, err)

		updated := string(fs.files[filePath])
		assert.Contains(t, updated, "// Save persists the user to the database.")
		assert.Contains(t, updated, `return fmt.Errorf("updated")`)
	})

	t.Run("strip_doc removes existing doc comment", func(t *testing.T) {
		fs := &mockFS{files: map[string][]byte{filePath: []byte(initialCode)}}
		handler := commands.NewExecutePlanHandler(fs)

		plan := domain.Plan{Actions: []domain.Action{{
			Action:     domain.ActionTypeUpdateFunc,
			FilePath:   filePath,
			Identifier: "User.Save",
			Content:    "func (u *User) Save() error {\n\treturn nil\n}",
			StripDoc:   true,
		}}}

		_, err := handler.ExecutePlan(context.Background(), plan)
		require.NoError(t, err)

		updated := string(fs.files[filePath])
		assert.NotContains(t, updated, "// Save persists")
		assert.Contains(t, updated, "func (u *User) Save() error")
	})

	t.Run("doc replaces existing doc comment", func(t *testing.T) {
		fs := &mockFS{files: map[string][]byte{filePath: []byte(initialCode)}}
		handler := commands.NewExecutePlanHandler(fs)

		plan := domain.Plan{Actions: []domain.Action{{
			Action:     domain.ActionTypeUpdateFunc,
			FilePath:   filePath,
			Identifier: "User.Save",
			Content:    "func (u *User) Save() error {\n\treturn nil\n}",
			Doc:        "Save writes user data.",
		}}}

		_, err := handler.ExecutePlan(context.Background(), plan)
		require.NoError(t, err)

		updated := string(fs.files[filePath])
		assert.NotContains(t, updated, "// Save persists")
		assert.Contains(t, updated, "// Save writes user data.")
		assert.Contains(t, updated, "func (u *User) Save() error")
	})

	t.Run("doc adds doc comment to function without one", func(t *testing.T) {
		fs := &mockFS{files: map[string][]byte{filePath: []byte(initialCode)}}
		handler := commands.NewExecutePlanHandler(fs)

		plan := domain.Plan{Actions: []domain.Action{{
			Action:     domain.ActionTypeUpdateFunc,
			FilePath:   filePath,
			Identifier: "NoDoc",
			Content:    "func NoDoc() {}",
			Doc:        "NoDoc does nothing.",
		}}}

		_, err := handler.ExecutePlan(context.Background(), plan)
		require.NoError(t, err)

		updated := string(fs.files[filePath])
		assert.Contains(t, updated, "// NoDoc does nothing.")
		assert.Contains(t, updated, "func NoDoc() {}")
	})

	t.Run("strip_doc on function without doc is a no-op", func(t *testing.T) {
		fs := &mockFS{files: map[string][]byte{filePath: []byte(initialCode)}}
		handler := commands.NewExecutePlanHandler(fs)

		plan := domain.Plan{Actions: []domain.Action{{
			Action:     domain.ActionTypeUpdateFunc,
			FilePath:   filePath,
			Identifier: "NoDoc",
			Content:    "func NoDoc() { return }",
			StripDoc:   true,
		}}}

		_, err := handler.ExecutePlan(context.Background(), plan)
		require.NoError(t, err)

		updated := string(fs.files[filePath])
		assert.Contains(t, updated, "func NoDoc() { return }")
		assert.NotContains(t, updated, "// NoDoc")
	})
}

func TestUpdateStruct_DocHandling(t *testing.T) {
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "doc.go")

	initialCode := `package main

// Config holds application configuration.
type Config struct {
	Port int
}

type Plain struct {
	X int
}
`

	t.Run("default preserves existing struct doc comment", func(t *testing.T) {
		fs := &mockFS{files: map[string][]byte{filePath: []byte(initialCode)}}
		handler := commands.NewExecutePlanHandler(fs)

		plan := domain.Plan{Actions: []domain.Action{{
			Action:     domain.ActionTypeUpdateStruct,
			FilePath:   filePath,
			Identifier: "Config",
			Content:    "type Config struct {\n\tPort int\n\tHost string\n}",
		}}}

		_, err := handler.ExecutePlan(context.Background(), plan)
		require.NoError(t, err)

		updated := string(fs.files[filePath])
		assert.Contains(t, updated, "// Config holds application configuration.")
		assert.Contains(t, updated, "Host string")
	})

	t.Run("strip_doc removes existing struct doc comment", func(t *testing.T) {
		fs := &mockFS{files: map[string][]byte{filePath: []byte(initialCode)}}
		handler := commands.NewExecutePlanHandler(fs)

		plan := domain.Plan{Actions: []domain.Action{{
			Action:     domain.ActionTypeUpdateStruct,
			FilePath:   filePath,
			Identifier: "Config",
			Content:    "type Config struct {\n\tPort int\n}",
			StripDoc:   true,
		}}}

		_, err := handler.ExecutePlan(context.Background(), plan)
		require.NoError(t, err)

		updated := string(fs.files[filePath])
		assert.NotContains(t, updated, "// Config holds")
		assert.Contains(t, updated, "type Config struct")
	})

	t.Run("doc replaces existing struct doc comment", func(t *testing.T) {
		fs := &mockFS{files: map[string][]byte{filePath: []byte(initialCode)}}
		handler := commands.NewExecutePlanHandler(fs)

		plan := domain.Plan{Actions: []domain.Action{{
			Action:     domain.ActionTypeUpdateStruct,
			FilePath:   filePath,
			Identifier: "Config",
			Content:    "type Config struct {\n\tPort int\n}",
			Doc:        "Config stores server settings.",
		}}}

		_, err := handler.ExecutePlan(context.Background(), plan)
		require.NoError(t, err)

		updated := string(fs.files[filePath])
		assert.NotContains(t, updated, "// Config holds")
		assert.Contains(t, updated, "// Config stores server settings.")
		assert.Contains(t, updated, "type Config struct")
	})

	t.Run("doc adds doc comment to struct without one", func(t *testing.T) {
		fs := &mockFS{files: map[string][]byte{filePath: []byte(initialCode)}}
		handler := commands.NewExecutePlanHandler(fs)

		plan := domain.Plan{Actions: []domain.Action{{
			Action:     domain.ActionTypeUpdateStruct,
			FilePath:   filePath,
			Identifier: "Plain",
			Content:    "type Plain struct {\n\tX int\n}",
			Doc:        "Plain is a simple struct.",
		}}}

		_, err := handler.ExecutePlan(context.Background(), plan)
		require.NoError(t, err)

		updated := string(fs.files[filePath])
		assert.Contains(t, updated, "// Plain is a simple struct.")
		assert.Contains(t, updated, "type Plain struct")
	})
}

// TestUpdateStruct_MissingTypeKeyword verifies that update_struct and add_struct
// tolerate content without the "type" keyword (common LLM omission) and still
// produce a valid file.
func TestUpdateStruct_MissingTypeKeyword(t *testing.T) {
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "pub.go")

	initialCode := `package kafka

type bookPublisher struct {
	old string
}
`
	fs := &mockFS{files: map[string][]byte{filePath: []byte(initialCode)}}
	handler := commands.NewExecutePlanHandler(fs)

	t.Run("update_struct without 'type' keyword does not corrupt file", func(t *testing.T) {
		plan := domain.Plan{Actions: []domain.Action{{
			Action:     domain.ActionTypeUpdateStruct,
			FilePath:   filePath,
			Identifier: "bookPublisher",
			// LLM omits the "type" prefix — must be tolerated.
			Content: "bookPublisher struct {\n\twriter string\n}",
		}}}

		_, err := handler.ExecutePlan(context.Background(), plan)
		require.NoError(t, err)

		updated := string(fs.files[filePath])
		// "type" keyword must be present in the output.
		assert.Contains(t, updated, "type bookPublisher struct")
		assert.Contains(t, updated, "writer string")
		// Old field gone.
		assert.NotContains(t, updated, "old string")
	})

	t.Run("add_struct without 'type' keyword produces valid file", func(t *testing.T) {
		fp2 := filepath.Join(tmpDir, "new.go")
		fs2 := &mockFS{files: map[string][]byte{}}
		h2 := commands.NewExecutePlanHandler(fs2)

		plan := domain.Plan{Actions: []domain.Action{{
			Action:      domain.ActionTypeAddStruct,
			FilePath:    fp2,
			PackagePath: "kafka",
			Content:     "bookWriter struct {\n\ttopic string\n}",
		}}}

		_, err := h2.ExecutePlan(context.Background(), plan)
		require.NoError(t, err)

		out := string(fs2.files[fp2])
		assert.Contains(t, out, "type bookWriter struct")
		assert.Contains(t, out, "topic string")
	})
}

// TestReplaceFile_CorruptedSourceFile verifies that update (replace file) does not
// error when the existing file is syntactically invalid — it falls back to inferring
// the package name from sibling files or the directory name.
func TestReplaceFile_CorruptedSourceFile(t *testing.T) {
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "broken.go")
	siblingPath := filepath.Join(tmpDir, "sibling.go")

	fs := &mockFS{files: map[string][]byte{
		filePath:    []byte("bookPublisher struct { writer string }"), // no package decl
		siblingPath: []byte("package kafka\n"),
	}}
	handler := commands.NewExecutePlanHandler(fs)

	plan := domain.Plan{Actions: []domain.Action{{
		Action:   domain.ActionTypeReplaceFile,
		FilePath: filePath,
		Content:  "type bookPublisher struct {\n\twriter string\n}\n",
	}}}

	_, err := handler.ExecutePlan(context.Background(), plan)
	require.NoError(t, err)

	out := string(fs.files[filePath])
	assert.Contains(t, out, "package kafka")
	assert.Contains(t, out, "type bookPublisher struct")
}

// TestCreateFile_InferPackageFromDir verifies that create (add_struct / add_func)
// on a new file with no PackagePath infers the package from sibling .go files.
func TestCreateFile_InferPackageFromDir(t *testing.T) {
	tmpDir := t.TempDir()
	newFile := filepath.Join(tmpDir, "money.go")
	siblingFile := filepath.Join(tmpDir, "book.go")

	fs := &mockFS{files: map[string][]byte{
		siblingFile: []byte("package domain\n\ntype Book struct{}\n"),
	}}
	handler := commands.NewExecutePlanHandler(fs)

	plan := domain.Plan{Actions: []domain.Action{{
		Action:   domain.ActionTypeAddStruct,
		FilePath: newFile,
		// No PackagePath provided — must be inferred from sibling.
		Content: "type Money int64",
	}}}

	_, err := handler.ExecutePlan(context.Background(), plan)
	require.NoError(t, err)

	out := string(fs.files[newFile])
	assert.Contains(t, out, "package domain")
	assert.Contains(t, out, "type Money int64")
}

// TestAddStruct_DocCommentNotPrefixedWithType verifies that add_struct and update_struct
// do not prepend "type " to a leading doc comment when the content starts with one.
// Regression for bookstore-8 bug 001.
func TestAddStruct_DocCommentNotPrefixedWithType(t *testing.T) {
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "order.go")

	fs := &mockFS{files: map[string][]byte{
		filePath: []byte("package domain\n"),
	}}
	handler := commands.NewExecutePlanHandler(fs)

	t.Run("add_struct with leading doc comment", func(t *testing.T) {
		plan := domain.Plan{Actions: []domain.Action{{
			Action:   domain.ActionTypeAddStruct,
			FilePath: filePath,
			Content:  "// OrderItem is a value object.\nOrderItem struct {\n\tID string\n}",
		}}}

		_, err := handler.ExecutePlan(context.Background(), plan)
		require.NoError(t, err)

		out := string(fs.files[filePath])
		// The doc comment must not have "type " prepended to it.
		assert.NotContains(t, out, "type //")
		assert.Contains(t, out, "// OrderItem is a value object.")
		assert.Contains(t, out, "type OrderItem struct")
	})

	t.Run("add_struct with type keyword already present after doc comment", func(t *testing.T) {
		fp2 := filepath.Join(tmpDir, "money.go")
		fs2 := &mockFS{files: map[string][]byte{fp2: []byte("package domain\n")}}
		h2 := commands.NewExecutePlanHandler(fs2)

		plan := domain.Plan{Actions: []domain.Action{{
			Action:   domain.ActionTypeAddStruct,
			FilePath: fp2,
			Content:  "// Money represents cents.\ntype Money int64",
		}}}

		_, err := h2.ExecutePlan(context.Background(), plan)
		require.NoError(t, err)

		out := string(fs2.files[fp2])
		assert.NotContains(t, out, "type //")
		assert.Contains(t, out, "// Money represents cents.")
		assert.Contains(t, out, "type Money int64")
		// Must not have duplicate "type type"
		assert.NotContains(t, out, "type type")
	})
}

// TestUpdateFunc_InterfaceIdentifierReturnsError verifies that calling update_func
// on an interface identifier returns a clear WRONG_OBJECT_TYPE error instead of
// silently inserting a duplicate.
// Regression for bookstore-8 bug 002.
func TestUpdateFunc_InterfaceIdentifierReturnsError(t *testing.T) {
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "repo.go")

	initialCode := `package order

type OrderRepository interface {
	Save(id string) error
}
`
	fs := &mockFS{files: map[string][]byte{filePath: []byte(initialCode)}}
	handler := commands.NewExecutePlanHandler(fs)

	plan := domain.Plan{Actions: []domain.Action{{
		Action:     domain.ActionTypeUpdateFunc,
		FilePath:   filePath,
		Identifier: "OrderRepository",
		Content:    "type OrderRepository interface {\n\tSave(id string) error\n\tFind(id string) error\n}",
	}}}

	_, err := handler.ExecutePlan(context.Background(), plan)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "WRONG_OBJECT_TYPE")
	assert.Contains(t, err.Error(), "OrderRepository")
	assert.Contains(t, err.Error(), "update_interface")

	// File must not be modified.
	assert.Equal(t, initialCode, string(fs.files[filePath]))
}
