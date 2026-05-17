package commands_test

import (
	"context"
	"strings"
	"testing"

	"github.com/JLugagne/go-surgeon/internal/surgeon/domain"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPatchFunction_ReplaceShorterMatch(t *testing.T) {
	ctx := context.Background()

	t.Run("shorter replacement within statement is substituted not deleted", func(t *testing.T) {
		h, fs := newPatchHandler()
		setFile(fs, "lib.go", `package lib

func (lib *Library) AddBook(title string) error {
	err := wait_for_db_connection_timeout()
	if err != nil {
		return err
	}
	return nil
}
`)
		res, err := h.PatchFunction(ctx, domain.PatchFunctionRequest{
			FilePath:   "lib.go",
			Identifier: "Library.AddBook",
			Patches: []domain.FunctionPatch{
				{Op: domain.PatchOpReplace, Match: "wait_for_db_connection_timeout()", Replace: "db.Connect()"},
			},
		})
		require.NoError(t, err)
		assert.Equal(t, 1, res.Applied)
		content := getFile(fs, "lib.go")
		assert.Contains(t, content, "db.Connect()", "replacement should be present")
		assert.NotContains(t, content, "wait_for_db_connection_timeout()", "original match should be gone")
		assert.Contains(t, content, "err := db.Connect()", "the full statement should be correct")
	})

	t.Run("shorter replacement at line start is substituted with indent preserved", func(t *testing.T) {
		h, fs := newPatchHandler()
		setFile(fs, "lib.go", `package lib

func (lib *Library) AddBook(title string) error {
	wait_for_db_connection_timeout()
	return nil
}
`)
		res, err := h.PatchFunction(ctx, domain.PatchFunctionRequest{
			FilePath:   "lib.go",
			Identifier: "Library.AddBook",
			Patches: []domain.FunctionPatch{
				{Op: domain.PatchOpReplace, Match: "wait_for_db_connection_timeout()", Replace: "db.Connect()"},
			},
		})
		require.NoError(t, err)
		assert.Equal(t, 1, res.Applied)
		content := getFile(fs, "lib.go")
		assert.Contains(t, content, "db.Connect()", "replacement should be present")
		assert.NotContains(t, content, "wait_for_db_connection_timeout()", "original match should be gone")
		// Verify indentation is preserved
		assert.Contains(t, content, "\tdb.Connect()", "replacement should have tab indentation")
	})

	t.Run("shorter replacement with whitespace normalization", func(t *testing.T) {
		h, fs := newPatchHandler()
		setFile(fs, "lib.go", `package lib

func (lib *Library) AddBook(title string) error {
	err :=   wait_for_db_connection_timeout()
	return nil
}
`)
		res, err := h.PatchFunction(ctx, domain.PatchFunctionRequest{
			FilePath:   "lib.go",
			Identifier: "Library.AddBook",
			Patches: []domain.FunctionPatch{
				{Op: domain.PatchOpReplace, Match: "wait_for_db_connection_timeout()", Replace: "db.Connect()"},
			},
		})
		require.NoError(t, err)
		assert.Equal(t, 1, res.Applied)
		content := getFile(fs, "lib.go")
		assert.Contains(t, content, "db.Connect()", "replacement should be present")
		assert.NotContains(t, content, "wait_for_db_connection_timeout()", "original match should be gone")
	})

	t.Run("shorter multi-line replacement is substituted not deleted", func(t *testing.T) {
		h, fs := newPatchHandler()
		setFile(fs, "lib.go", `package lib

func (lib *Library) AddBook(title string) error {
	err := setupDatabaseConnection(
		"host=localhost",
		"port=5432",
	)
	if err != nil {
		return err
	}
	return nil
}
`)
		res, err := h.PatchFunction(ctx, domain.PatchFunctionRequest{
			FilePath:   "lib.go",
			Identifier: "Library.AddBook",
			Patches: []domain.FunctionPatch{
				{Op: domain.PatchOpReplace, Match: `setupDatabaseConnection(
		"host=localhost",
		"port=5432",
	)`, Replace: "db.Connect()"},
			},
		})
		require.NoError(t, err)
		assert.Equal(t, 1, res.Applied)
		content := getFile(fs, "lib.go")
		assert.Contains(t, content, "db.Connect()", "replacement should be present")
		assert.NotContains(t, content, "setupDatabaseConnection", "original match should be gone")
	})
}

func TestPatchFunction_ReplaceShorterMatch_WholeLine(t *testing.T) {
	ctx := context.Background()

	t.Run("whole-line match shorter replacement preserves indentation", func(t *testing.T) {
		h, fs := newPatchHandler()
		setFile(fs, "lib.go", `package lib

func F() {
	wait_for_db_connection_timeout()
	return nil
}
`)
		res, err := h.PatchFunction(ctx, domain.PatchFunctionRequest{
			FilePath:   "lib.go",
			Identifier: "F",
			Patches: []domain.FunctionPatch{
				{Op: domain.PatchOpReplace, Match: "wait_for_db_connection_timeout()", Replace: "db.Connect()"},
			},
		})
		require.NoError(t, err)
		assert.Equal(t, 1, res.Applied)
		content := getFile(fs, "lib.go")
		assert.Contains(t, content, "db.Connect()", "replacement should be present")
		assert.NotContains(t, content, "wait_for_db_connection_timeout()", "original match should be gone")
		// Check that indentation is preserved
		lines := strings.Split(content, "\n")
		for _, line := range lines {
			if strings.Contains(line, "db.Connect()") {
				assert.True(t, strings.HasPrefix(line, "\t"), "replacement line should have tab indentation, got: %q", line)
			}
		}
	})
}

func TestPatchFunction_ReplaceShorterMatch_ExistingInFile(t *testing.T) {
	ctx := context.Background()

	t.Run("replacement already exists elsewhere in file should still work", func(t *testing.T) {
		h, fs := newPatchHandler()
		setFile(fs, "lib.go", `package lib

// db.Connect() is used elsewhere
func F() {
	x := wait_for_db_connection_timeout()
	_ = x
}

func G() {
	db.Connect()
}
`)
		res, err := h.PatchFunction(ctx, domain.PatchFunctionRequest{
			FilePath:   "lib.go",
			Identifier: "F",
			Patches: []domain.FunctionPatch{
				{Op: domain.PatchOpReplace, Match: "wait_for_db_connection_timeout()", Replace: "db.Connect()"},
			},
		})
		require.NoError(t, err)
		assert.Equal(t, 1, res.Applied)
		content := getFile(fs, "lib.go")
		// F should have the replacement
		fIdx := strings.Index(content, "func F()")
		gIdx := strings.Index(content, "func G()")
		fBody := content[fIdx:gIdx]
		assert.Contains(t, fBody, "db.Connect()", "F should have the replacement")
		assert.NotContains(t, fBody, "wait_for_db_connection_timeout()", "F should not have the original")
		// G should be unchanged
		gBody := content[gIdx:]
		assert.Contains(t, gBody, "db.Connect()", "G should be unchanged")
	})
}

func TestPatchFunction_ReplaceEmptyField_Error(t *testing.T) {
	ctx := context.Background()

	t.Run("op=replace with empty replace field should error", func(t *testing.T) {
		h, fs := newPatchHandler()
		setFile(fs, "lib.go", `package lib

func F() {
	x := wait_for_db_connection_timeout()
	_ = x
}
`)
		_, err := h.PatchFunction(ctx, domain.PatchFunctionRequest{
			FilePath:   "lib.go",
			Identifier: "F",
			Patches: []domain.FunctionPatch{
				{Op: domain.PatchOpReplace, Match: "wait_for_db_connection_timeout()", Replace: ""},
			},
		})
		require.Error(t, err, "empty replace should error")
		assert.Contains(t, err.Error(), "replace field is empty", "error should mention empty replace")
		assert.Contains(t, err.Error(), "op:delete", "error should suggest op:delete")
		assert.Contains(t, err.Error(), "replacement", "error should mention replacement field name")
		// File should be unchanged
		assert.Equal(t, `package lib

func F() {
	x := wait_for_db_connection_timeout()
	_ = x
}
`, getFile(fs, "lib.go"))
	})
}
