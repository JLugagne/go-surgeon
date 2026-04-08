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
