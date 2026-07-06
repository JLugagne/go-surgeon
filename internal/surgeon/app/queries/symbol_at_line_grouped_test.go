package queries_test

import (
	"context"
	"testing"

	"github.com/JLugagne/go-surgeon/internal/surgeon/app/queries"
	"github.com/JLugagne/go-surgeon/internal/surgeon/domain"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestFindSymbolAtLine_GroupedConstReturnsSpecSpanningLine asserts that
// resolving at_line inside a grouped const block returns the member whose
// range spans that line, not the first member of the block.
func TestFindSymbolAtLine_GroupedConstReturnsSpecSpanningLine(t *testing.T) {
	const path = "/tmp/at_line_grouped_const.go"
	// Line layout: 1 package, 2 blank, 3 "const (", 4 Alpha, 5 Beta, 6 Gamma, 7 ")"
	code := "package cfg\n\nconst (\n\tAlpha = 1\n\tBeta  = 2\n\tGamma = 3\n)\n"
	fs := &mockFS{files: map[string][]byte{path: []byte(code)}}
	h := queries.NewSurgeonQueriesHandler(fs)

	res, err := h.FindSymbols(context.Background(), domain.SymbolQuery{File: path, AtLine: 5}, "")
	require.NoError(t, err)
	require.Len(t, res, 1)
	assert.Equal(t, "Beta", res[0].Name, "at_line=5 must resolve the spec spanning that line")
}

// TestFindSymbolAtLine_GroupedTypeReturnsSpecSpanningLine asserts the same
// spec-selection behavior for a grouped type block.
func TestFindSymbolAtLine_GroupedTypeReturnsSpecSpanningLine(t *testing.T) {
	const path = "/tmp/at_line_grouped_type.go"
	// Line layout: 1 package, 2 blank, 3 "type (", 4 Foo, 5 Bar, 6 ")"
	code := "package m\n\ntype (\n\tFoo struct{ A int }\n\tBar struct{ B int }\n)\n"
	fs := &mockFS{files: map[string][]byte{path: []byte(code)}}
	h := queries.NewSurgeonQueriesHandler(fs)

	res, err := h.FindSymbols(context.Background(), domain.SymbolQuery{File: path, AtLine: 5}, "")
	require.NoError(t, err)
	require.Len(t, res, 1)
	assert.Equal(t, "Bar", res[0].Name, "at_line=5 must resolve the type spec spanning that line")
}
