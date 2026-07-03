package commands_test

import (
	"context"
	"strings"
	"testing"

	"github.com/JLugagne/go-surgeon/internal/surgeon/app/commands"
	"github.com/JLugagne/go-surgeon/internal/surgeon/domain"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestUpdateDecl_TypeDeclReplacedInPlace asserts update_decl can target a
// plain type declaration (alias/defined type). Before the fix,
// findDeclOffsets only matched const/var, so a type update fell into the
// append fallback and produced a duplicate declaration that no longer
// compiles.
func TestUpdateDecl_TypeDeclReplacedInPlace(t *testing.T) {
	const path = "/tmp/update_decl_type.go"
	const original = "package ids\n\n// UserID identifies a user.\ntype UserID string\n\nfunc Keep() {}\n"
	fs := &mockFS{files: map[string][]byte{path: []byte(original)}}
	h := commands.NewExecutePlanHandler(fs)

	result, err := h.ExecutePlan(context.Background(), domain.Plan{
		Actions: []domain.Action{{
			Action:     domain.ActionTypeUpdateDecl,
			FilePath:   path,
			Identifier: "UserID",
			Content:    "type UserID int64",
		}},
	})
	require.NoError(t, err)
	assert.Empty(t, result.Warnings, "in-place update must not warn about append fallback")

	updated := string(fs.files[path])
	assert.Contains(t, updated, "type UserID int64")
	assert.NotContains(t, updated, "type UserID string", "old declaration must be gone")
	assert.Equal(t, 1, strings.Count(updated, "type UserID"), "declaration must not be duplicated")
	assert.Contains(t, updated, "func Keep()", "sibling declarations must survive")
}

// TestUpdateDecl_GroupedConstPreservesSiblings asserts that updating one
// member of a grouped const block replaces only that member. Before the
// fix, findDeclOffsets returned the whole GenDecl range, so updating A
// silently deleted B (reported SUCCESS, no warning).
func TestUpdateDecl_GroupedConstPreservesSiblings(t *testing.T) {
	const path = "/tmp/update_decl_grouped.go"
	const original = "package cfg\n\nconst (\n\tA = 1\n\tB = 2\n)\n"
	fs := &mockFS{files: map[string][]byte{path: []byte(original)}}
	h := commands.NewExecutePlanHandler(fs)

	_, err := h.ExecutePlan(context.Background(), domain.Plan{
		Actions: []domain.Action{{
			Action:     domain.ActionTypeUpdateDecl,
			FilePath:   path,
			Identifier: "A",
			Content:    "const A = 3",
		}},
	})
	require.NoError(t, err)

	updated := string(fs.files[path])
	assert.Contains(t, updated, "A = 3", "target member must be updated")
	assert.NotContains(t, updated, "A = 1", "old value must be gone")
	assert.Contains(t, updated, "B = 2", "sibling members of the group must survive")
}

// TestUpdateDecl_PreservesDocComment asserts update_decl keeps the existing
// doc comment when the new content carries none, matching update_func /
// update_struct semantics (resolveDocReplacement). Before the fix the
// splice started at DocStart unconditionally, silently deleting the doc.
func TestUpdateDecl_PreservesDocComment(t *testing.T) {
	const path = "/tmp/update_decl_doc.go"
	const original = "package cfg\n\n// maxRetries bounds the retry loop.\nconst maxRetries = 3\n"
	fs := &mockFS{files: map[string][]byte{path: []byte(original)}}
	h := commands.NewExecutePlanHandler(fs)

	_, err := h.ExecutePlan(context.Background(), domain.Plan{
		Actions: []domain.Action{{
			Action:     domain.ActionTypeUpdateDecl,
			FilePath:   path,
			Identifier: "maxRetries",
			Content:    "const maxRetries = 5",
		}},
	})
	require.NoError(t, err)

	updated := string(fs.files[path])
	assert.Contains(t, updated, "const maxRetries = 5")
	assert.Contains(t, updated, "// maxRetries bounds the retry loop.", "doc comment must survive when content has none")
}
