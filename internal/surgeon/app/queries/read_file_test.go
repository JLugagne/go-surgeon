package queries_test

import (
	"context"
	"testing"

	"github.com/JLugagne/go-surgeon/internal/surgeon/app/queries"
	"github.com/JLugagne/go-surgeon/internal/surgeon/domain"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestReadFile_WholeFileNumbered pins the read primitive requested by
// issue #37: the whole file with the same line numbering as
// symbol body=true.
func TestReadFile_WholeFileNumbered(t *testing.T) {
	path := "/virtual/read.go"
	src := "package p\n\nfunc A() {}\n\nfunc B() {\n\treturn\n}\n"
	fs := &mockFS{files: map[string][]byte{path: []byte(src)}}
	h := queries.NewSurgeonQueriesHandler(fs)

	res, err := h.ReadFile(context.Background(), domain.ReadFileRequest{FilePath: path})
	require.NoError(t, err)
	assert.False(t, res.Truncated)
	assert.Equal(t, 1, res.LineStart)
	assert.Equal(t, 7, res.LineEnd)
	assert.Equal(t, 7, res.TotalLines)
	assert.Equal(t, "1: package p\n2: \n3: func A() {}\n4: \n5: func B() {\n6: \treturn\n7: }", res.Content)
	assert.Equal(t, "p", res.Package)
}

// TestReadFile_LineRange covers the optional --from/--to window.
func TestReadFile_LineRange(t *testing.T) {
	path := "/virtual/read_range.go"
	src := "package p\n\nfunc A() {}\n\nfunc B() {\n\treturn\n}\n"
	fs := &mockFS{files: map[string][]byte{path: []byte(src)}}
	h := queries.NewSurgeonQueriesHandler(fs)

	res, err := h.ReadFile(context.Background(), domain.ReadFileRequest{FilePath: path, FromLine: 3, ToLine: 5})
	require.NoError(t, err)
	assert.Equal(t, 3, res.LineStart)
	assert.Equal(t, 5, res.LineEnd)
	assert.Equal(t, 7, res.TotalLines)
	assert.Equal(t, "3: func A() {}\n4: \n5: func B() {", res.Content)
}

// TestReadFile_Truncates tells the caller when the cap kicked in.
func TestReadFile_Truncates(t *testing.T) {
	path := "/virtual/read_big.go"
	src := "package p\n\nfunc A() {}\n\nfunc B() {\n\treturn\n}\n"
	fs := &mockFS{files: map[string][]byte{path: []byte(src)}}
	h := queries.NewSurgeonQueriesHandler(fs)

	res, err := h.ReadFile(context.Background(), domain.ReadFileRequest{FilePath: path, MaxBytes: 30})
	require.NoError(t, err)
	assert.True(t, res.Truncated)
	assert.NotEmpty(t, res.Notice)
	assert.LessOrEqual(t, len(res.Content), 30)
}

// TestReadFile_RejectsNonGo keeps the primitive scoped to Go sources.
func TestReadFile_RejectsNonGo(t *testing.T) {
	path := "/virtual/README.md"
	fs := &mockFS{files: map[string][]byte{path: []byte("# hi\n")}}
	h := queries.NewSurgeonQueriesHandler(fs)

	_, err := h.ReadFile(context.Background(), domain.ReadFileRequest{FilePath: path})
	require.Error(t, err)
}
