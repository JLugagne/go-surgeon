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
