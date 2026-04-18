package mcp

import (
	"strings"
	"testing"

	"github.com/JLugagne/go-surgeon/internal/surgeon/domain"
	"github.com/stretchr/testify/assert"
)

// TestFirstDocSentence covers the doc-summary extraction helper used by
// symbol pattern outline mode. The behaviour mirrors the task spec:
// stop at the first "." followed by a space, ignore later sentences or
// paragraphs, and return "" for empty / whitespace-only input.
func TestFirstDocSentence(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"empty", "", ""},
		{"whitespace only", "   \n\t  ", ""},
		{"single sentence with period", "Foo does X.", "Foo does X"},
		{"sentence then another", "Foo does X. Y is separate.", "Foo does X"},
		{"sentence with trailing whitespace", "   Foo does X. Y is separate.   ", "Foo does X"},
		{"no period at all", "Foo does X without a period", "Foo does X without a period"},
		{"sentence spanning two lines", "Foo does X\nand continues.", "Foo does X and continues"},
		{"first paragraph then blank-line break", "Foo does X.\n\nSecond paragraph is ignored.", "Foo does X"},
		{"multi-line first sentence, paragraph break", "Foo does X and\nmore context.\n\nSecond paragraph.", "Foo does X and more context"},
		{"period inside a word (decimal-like)", "Version 1.2 ships today", "Version 1.2 ships today"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, firstDocSentence(tc.in))
		})
	}
}

// TestFormatPatternResults_Outline verifies that outline=true emits the
// signature plus a "    — <first sentence>" suffix for each match that
// has a non-empty Doc, and omits the summary line when the Doc is empty.
func TestFormatPatternResults_Outline(t *testing.T) {
	results := []domain.SymbolResult{
		{
			Name:      "registerFoo",
			File:      "registry.go",
			LineStart: 10,
			Signature: "func registerFoo()",
			Doc:       "registerFoo wires Foo into the registry. Y is separate.",
		},
		{
			Name:      "registerBare",
			File:      "registry.go",
			LineStart: 20,
			Signature: "func registerBare()",
			// No doc comment.
		},
	}

	out := formatPatternResults(results, false, "^register", 0, true)

	// Header line is always present.
	assert.Contains(t, out, "- registerFoo — registry.go:10")
	// Signature line is always present in outline mode.
	assert.Contains(t, out, "  func registerFoo()")
	// The summary line uses an em-dash and stops at the first sentence.
	assert.Contains(t, out, "    — registerFoo wires Foo into the registry")
	// The second sentence must NOT leak into the output.
	assert.NotContains(t, out, "Y is separate")

	// Bare declaration has a signature but no summary suffix.
	assert.Contains(t, out, "- registerBare — registry.go:20")
	assert.Contains(t, out, "  func registerBare()")
	// Count em-dash suffix lines: only the first result should have one.
	summaryCount := strings.Count(out, "\n    — ")
	assert.Equal(t, 1, summaryCount, "expected exactly one summary line (registerFoo), not for registerBare")
}

// TestFormatPatternResults_SignatureOnlyWhenNoOutline is the negative
// control: outline=false preserves the pre-existing signature-only
// rendering (no em-dash summary suffix, even when Doc is populated).
func TestFormatPatternResults_SignatureOnlyWhenNoOutline(t *testing.T) {
	results := []domain.SymbolResult{
		{
			Name:      "registerFoo",
			File:      "registry.go",
			LineStart: 10,
			Signature: "func registerFoo()",
			Doc:       "registerFoo wires Foo into the registry.",
		},
	}

	out := formatPatternResults(results, false, "^register", 0, false)

	assert.Contains(t, out, "- registerFoo — registry.go:10")
	// No signature line emitted when showBody=false and outline=false (prior behaviour).
	assert.NotContains(t, out, "  func registerFoo()")
	// No summary line.
	assert.NotContains(t, out, "    — ")
}
