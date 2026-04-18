package commands_test

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/JLugagne/go-surgeon/internal/surgeon/app/commands"
	"github.com/JLugagne/go-surgeon/internal/surgeon/domain"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ── helpers ──────────────────────────────────────────────────────────────────

func newPatchHandler() (*commands.ExecutePlanHandler, *mockFS) {
	fs := &mockFS{files: make(map[string][]byte)}
	return commands.NewExecutePlanHandler(fs), fs
}

func setFile(fs *mockFS, path, src string)   { fs.files[path] = []byte(src) }
func getFile(fs *mockFS, path string) string { return string(fs.files[path]) }

// ── replace ───────────────────────────────────────────────────────────────────

func TestPatchFunction_Replace(t *testing.T) {
	ctx := context.Background()

	t.Run("unique match is replaced", func(t *testing.T) {
		h, fs := newPatchHandler()
		setFile(fs, "f.go", `package p
func Greet(name string) string {
	msg := "hello " + name
	return msg
}
`)
		res, err := h.PatchFunction(ctx, domain.PatchFunctionRequest{
			FilePath:   "f.go",
			Identifier: "Greet",
			Patches: []domain.FunctionPatch{
				{Op: domain.PatchOpReplace, Match: `"hello " + name`, Replace: `"hi " + name`},
			},
		})
		require.NoError(t, err)
		assert.Equal(t, 1, res.Applied)
		assert.Contains(t, getFile(fs, "f.go"), `"hi " + name`)
		assert.NotContains(t, getFile(fs, "f.go"), `"hello " + name`)
	})

	t.Run("result diff is non-empty on success", func(t *testing.T) {
		h, fs := newPatchHandler()
		setFile(fs, "f.go", `package p
func F() { x := 1 }
`)
		res, err := h.PatchFunction(ctx, domain.PatchFunctionRequest{
			FilePath:   "f.go",
			Identifier: "F",
			Patches:    []domain.FunctionPatch{{Op: domain.PatchOpReplace, Match: "1", Replace: "2"}},
		})
		require.NoError(t, err)
		assert.NotEmpty(t, res.Diff, "diff should be non-empty after a successful patch")
		assert.Contains(t, res.Diff, "-")
		assert.Contains(t, res.Diff, "+")
	})

	t.Run("occurrence:2 targets second duplicate, leaves first intact", func(t *testing.T) {
		h, fs := newPatchHandler()
		setFile(fs, "f.go", `package p
func Multi() string {
	a := "x"
	b := "x"
	return a + b
}
`)
		res, err := h.PatchFunction(ctx, domain.PatchFunctionRequest{
			FilePath:   "f.go",
			Identifier: "Multi",
			Patches: []domain.FunctionPatch{
				{Op: domain.PatchOpReplace, Match: `"x"`, Occurrence: 2, Replace: `"y"`},
			},
		})
		require.NoError(t, err)
		assert.Equal(t, 1, res.Applied)
		content := getFile(fs, "f.go")
		assert.Contains(t, content, `a := "x"`)
		assert.Contains(t, content, `b := "y"`)
	})

	t.Run("occurrence:1 targets first duplicate", func(t *testing.T) {
		h, fs := newPatchHandler()
		setFile(fs, "f.go", `package p
func Multi() string {
	a := "x"
	b := "x"
	return a + b
}
`)
		_, err := h.PatchFunction(ctx, domain.PatchFunctionRequest{
			FilePath:   "f.go",
			Identifier: "Multi",
			Patches: []domain.FunctionPatch{
				{Op: domain.PatchOpReplace, Match: `"x"`, Occurrence: 1, Replace: `"first"`},
			},
		})
		require.NoError(t, err)
		content := getFile(fs, "f.go")
		assert.Contains(t, content, `a := "first"`)
		assert.Contains(t, content, `b := "x"`)
	})

	t.Run("occurrence out of range returns error without write", func(t *testing.T) {
		h, fs := newPatchHandler()
		src := `package p
func F() { x := 1 }
`
		setFile(fs, "f.go", src)
		_, err := h.PatchFunction(ctx, domain.PatchFunctionRequest{
			FilePath:   "f.go",
			Identifier: "F",
			Patches: []domain.FunctionPatch{
				{Op: domain.PatchOpReplace, Match: "1", Occurrence: 5, Replace: "2"},
			},
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "occurrence 5")
		assert.Equal(t, src, getFile(fs, "f.go"))
	})

	t.Run("ambiguous match without occurrence errors with candidates list", func(t *testing.T) {
		h, fs := newPatchHandler()
		src := `package p
func Dup() {
	x := 1
	x = 1
}
`
		setFile(fs, "f.go", src)
		_, err := h.PatchFunction(ctx, domain.PatchFunctionRequest{
			FilePath:   "f.go",
			Identifier: "Dup",
			Patches:    []domain.FunctionPatch{{Op: domain.PatchOpReplace, Match: "1", Replace: "2"}},
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "matched 2 times")
		assert.Contains(t, err.Error(), "Disambiguate")
		// Candidates now include absolute line numbers and the error carries
		// a retry hint pointing at at_line (faster than occurrence rescanning).
		assert.Regexp(t, `L\d+: x := 1`, err.Error())
		assert.Contains(t, err.Error(), "Hint: retry with at_line:")
		assert.Contains(t, err.Error(), "or use occurrence: 1..2")
		assert.Equal(t, src, getFile(fs, "f.go"))
	})

	t.Run("match not found returns descriptive error", func(t *testing.T) {
		h, fs := newPatchHandler()
		setFile(fs, "f.go", `package p
func F() { doWork() }
`)
		_, err := h.PatchFunction(ctx, domain.PatchFunctionRequest{
			FilePath:   "f.go",
			Identifier: "F",
			Patches:    []domain.FunctionPatch{{Op: domain.PatchOpReplace, Match: "nonexistent", Replace: "x"}},
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "no match found")
	})

	t.Run("patch only targets the named function, not a sibling with same text", func(t *testing.T) {
		h, fs := newPatchHandler()
		setFile(fs, "f.go", `package p
func Foo() {
	doWork()
}
func Bar() {
	doWork()
}
`)
		_, err := h.PatchFunction(ctx, domain.PatchFunctionRequest{
			FilePath:   "f.go",
			Identifier: "Foo",
			Patches:    []domain.FunctionPatch{{Op: domain.PatchOpReplace, Match: "doWork()", Replace: "doFoo()"}},
		})
		require.NoError(t, err)
		content := getFile(fs, "f.go")
		assert.Contains(t, content, "doFoo()")
		// Bar's body must be completely untouched
		barIdx := strings.Index(content, "func Bar()")
		require.True(t, barIdx >= 0, "Bar should still exist")
		barBody := content[barIdx:]
		assert.Contains(t, barBody, "doWork()", "sibling function must not be touched")
		assert.NotContains(t, barBody, "doFoo()", "sibling function must not be touched")
	})
}

// ── input validation ──────────────────────────────────────────────────────────

func TestPatchFunction_InputValidation(t *testing.T) {
	ctx := context.Background()

	t.Run("missing match and match_regex errors", func(t *testing.T) {
		h, fs := newPatchHandler()
		setFile(fs, "f.go", `package p
func F() { x := 1 }
`)
		_, err := h.PatchFunction(ctx, domain.PatchFunctionRequest{
			FilePath:   "f.go",
			Identifier: "F",
			Patches:    []domain.FunctionPatch{{Op: domain.PatchOpReplace, Replace: "2"}},
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "match, match_regex, at_line, or from_line/to_line is required")
	})

	t.Run("match and match_regex both set errors", func(t *testing.T) {
		h, fs := newPatchHandler()
		setFile(fs, "f.go", `package p
func F() { x := 1 }
`)
		_, err := h.PatchFunction(ctx, domain.PatchFunctionRequest{
			FilePath:   "f.go",
			Identifier: "F",
			Patches: []domain.FunctionPatch{
				{Op: domain.PatchOpReplace, Match: "1", MatchRegex: `\d`, Replace: "2"},
			},
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "mutually exclusive")
	})

	t.Run("invalid match_regex errors without write", func(t *testing.T) {
		h, fs := newPatchHandler()
		src := `package p
func F() { x := 1 }
`
		setFile(fs, "f.go", src)
		_, err := h.PatchFunction(ctx, domain.PatchFunctionRequest{
			FilePath:   "f.go",
			Identifier: "F",
			Patches:    []domain.FunctionPatch{{Op: domain.PatchOpDelete, MatchRegex: `[invalid(`}},
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "invalid match_regex")
		assert.Equal(t, src, getFile(fs, "f.go"))
	})

	t.Run("unknown op errors without write", func(t *testing.T) {
		h, fs := newPatchHandler()
		src := `package p
func F() { x := 1 }
`
		setFile(fs, "f.go", src)
		_, err := h.PatchFunction(ctx, domain.PatchFunctionRequest{
			FilePath:   "f.go",
			Identifier: "F",
			Patches:    []domain.FunctionPatch{{Op: "teleport", Match: "1"}},
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "unknown op")
		assert.Equal(t, src, getFile(fs, "f.go"))
	})

	t.Run("multiple validation errors are all reported", func(t *testing.T) {
		h, fs := newPatchHandler()
		setFile(fs, "f.go", `package p
func F() { x := 1 }
`)
		_, err := h.PatchFunction(ctx, domain.PatchFunctionRequest{
			FilePath:   "f.go",
			Identifier: "F",
			Patches: []domain.FunctionPatch{
				{Op: domain.PatchOpReplace, Match: "nothere", Replace: "x"},
				{Op: domain.PatchOpReplace, Match: "alsonothere", Replace: "y"},
			},
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "patch #1")
		assert.Contains(t, err.Error(), "patch #2")
	})

	t.Run("function not found errors", func(t *testing.T) {
		h, fs := newPatchHandler()
		setFile(fs, "f.go", `package p
func Exists() {}
`)
		_, err := h.PatchFunction(ctx, domain.PatchFunctionRequest{
			FilePath:   "f.go",
			Identifier: "Missing",
			Patches:    []domain.FunctionPatch{{Op: domain.PatchOpReplace, Match: "x", Replace: "y"}},
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not found")
	})

	t.Run("file not found errors", func(t *testing.T) {
		h, _ := newPatchHandler()
		_, err := h.PatchFunction(ctx, domain.PatchFunctionRequest{
			FilePath:   "does_not_exist.go",
			Identifier: "F",
			Patches:    []domain.FunctionPatch{{Op: domain.PatchOpReplace, Match: "x", Replace: "y"}},
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "READ_ERROR")
	})
}

// ── insert_before / insert_after ─────────────────────────────────────────────

func TestPatchFunction_Insert(t *testing.T) {
	ctx := context.Background()

	src := `package p
func Process() {
	// step 1
	doWork()
	return
}
`

	t.Run("insert_before places line before match", func(t *testing.T) {
		h, fs := newPatchHandler()
		setFile(fs, "f.go", src)
		_, err := h.PatchFunction(ctx, domain.PatchFunctionRequest{
			FilePath:   "f.go",
			Identifier: "Process",
			Patches: []domain.FunctionPatch{
				{Op: domain.PatchOpInsertBefore, Match: "doWork()", Code: `log.Println("before")`},
			},
		})
		require.NoError(t, err)
		content := getFile(fs, "f.go")
		logIdx := strings.Index(content, `log.Println("before")`)
		doIdx := strings.Index(content, "doWork()")
		assert.True(t, logIdx >= 0 && doIdx >= 0 && logIdx < doIdx,
			"inserted line must appear before doWork()")
	})

	t.Run("insert_before inherits indentation from matched line", func(t *testing.T) {
		h, fs := newPatchHandler()
		setFile(fs, "f.go", src)
		_, err := h.PatchFunction(ctx, domain.PatchFunctionRequest{
			FilePath:   "f.go",
			Identifier: "Process",
			Patches: []domain.FunctionPatch{
				{Op: domain.PatchOpInsertBefore, Match: "doWork()", Code: `log.Println("x")`},
			},
		})
		require.NoError(t, err)
		content := getFile(fs, "f.go")
		// Both the inserted line and doWork() should start with a tab
		for _, line := range strings.Split(content, "\n") {
			if strings.Contains(line, `log.Println("x")`) {
				assert.True(t, strings.HasPrefix(line, "\t"), "inserted line should be tab-indented, got: %q", line)
			}
		}
	})

	t.Run("insert_after places line after match", func(t *testing.T) {
		h, fs := newPatchHandler()
		setFile(fs, "f.go", src)
		_, err := h.PatchFunction(ctx, domain.PatchFunctionRequest{
			FilePath:   "f.go",
			Identifier: "Process",
			Patches: []domain.FunctionPatch{
				{Op: domain.PatchOpInsertAfter, Match: "doWork()", Code: `log.Println("after")`},
			},
		})
		require.NoError(t, err)
		content := getFile(fs, "f.go")
		doIdx := strings.Index(content, "doWork()")
		logIdx := strings.Index(content, `log.Println("after")`)
		assert.True(t, doIdx >= 0 && logIdx >= 0 && doIdx < logIdx,
			"inserted line must appear after doWork()")
	})

	t.Run("insert_after inherits indentation from matched line", func(t *testing.T) {
		h, fs := newPatchHandler()
		setFile(fs, "f.go", src)
		_, err := h.PatchFunction(ctx, domain.PatchFunctionRequest{
			FilePath:   "f.go",
			Identifier: "Process",
			Patches: []domain.FunctionPatch{
				{Op: domain.PatchOpInsertAfter, Match: "doWork()", Code: `log.Println("y")`},
			},
		})
		require.NoError(t, err)
		content := getFile(fs, "f.go")
		for _, line := range strings.Split(content, "\n") {
			if strings.Contains(line, `log.Println("y")`) {
				assert.True(t, strings.HasPrefix(line, "\t"), "inserted line should be tab-indented, got: %q", line)
			}
		}
	})
}

// ── delete ────────────────────────────────────────────────────────────────────

func TestPatchFunction_Delete(t *testing.T) {
	ctx := context.Background()

	t.Run("delete whole line by literal match", func(t *testing.T) {
		h, fs := newPatchHandler()
		setFile(fs, "f.go", `package p
func Init() {
	setup()
	// TODO: remove this
	run()
}
`)
		_, err := h.PatchFunction(ctx, domain.PatchFunctionRequest{
			FilePath:   "f.go",
			Identifier: "Init",
			Patches:    []domain.FunctionPatch{{Op: domain.PatchOpDelete, Match: "// TODO: remove this"}},
		})
		require.NoError(t, err)
		content := getFile(fs, "f.go")
		assert.NotContains(t, content, "TODO: remove this")
		assert.Contains(t, content, "setup()")
		assert.Contains(t, content, "run()")
		// No blank line left behind
		assert.NotContains(t, content, "\n\n\n")
	})

	t.Run("delete by regex removes first occurrence", func(t *testing.T) {
		h, fs := newPatchHandler()
		setFile(fs, "f.go", `package p
func Cleanup() {
	// TODO: fix this
	doA()
	// TODO: fix that
	doB()
}
`)
		_, err := h.PatchFunction(ctx, domain.PatchFunctionRequest{
			FilePath:   "f.go",
			Identifier: "Cleanup",
			Patches:    []domain.FunctionPatch{{Op: domain.PatchOpDelete, MatchRegex: `// TODO: fix this`}},
		})
		require.NoError(t, err)
		content := getFile(fs, "f.go")
		assert.NotContains(t, content, "// TODO: fix this")
		assert.Contains(t, content, "doA()")
		// Second TODO line untouched
		assert.Contains(t, content, "// TODO: fix that")
	})

	t.Run("delete all TODO lines with regex + occurrence disambiguation", func(t *testing.T) {
		h, fs := newPatchHandler()
		setFile(fs, "f.go", `package p
func Cleanup() {
	// TODO: first
	doA()
	// TODO: second
	doB()
}
`)
		// Delete occurrence:1 (first TODO), then occurrence:1 again after first is gone
		// — but since patches resolve against the ORIGINAL body, both must be explicit.
		_, err := h.PatchFunction(ctx, domain.PatchFunctionRequest{
			FilePath:   "f.go",
			Identifier: "Cleanup",
			Patches: []domain.FunctionPatch{
				{Op: domain.PatchOpDelete, MatchRegex: `// TODO: first`},
				{Op: domain.PatchOpDelete, MatchRegex: `// TODO: second`},
			},
		})
		require.NoError(t, err)
		content := getFile(fs, "f.go")
		assert.NotContains(t, content, "TODO:")
		assert.Contains(t, content, "doA()")
		assert.Contains(t, content, "doB()")
	})
}

// ── wrap ──────────────────────────────────────────────────────────────────────

func TestPatchFunction_Wrap(t *testing.T) {
	ctx := context.Background()

	t.Run("wrap replaces call with error-checking pattern", func(t *testing.T) {
		h, fs := newPatchHandler()
		setFile(fs, "f.go", `package p
func FetchAll() error {
	doFetch()
	return nil
}
`)
		_, err := h.PatchFunction(ctx, domain.PatchFunctionRequest{
			FilePath:   "f.go",
			Identifier: "FetchAll",
			Patches: []domain.FunctionPatch{
				{Op: domain.PatchOpWrap, Match: "doFetch()", Wrap: "if err := %s; err != nil { return err }"},
			},
		})
		require.NoError(t, err)
		content := getFile(fs, "f.go")
		assert.Contains(t, content, "if err := doFetch(); err != nil { return err }")
		assert.NotContains(t, content, "\tdoFetch()\n")
	})

	t.Run("wrap preserves indentation of the original line", func(t *testing.T) {
		h, fs := newPatchHandler()
		setFile(fs, "f.go", `package p
func F() error {
	doThing()
	return nil
}
`)
		_, err := h.PatchFunction(ctx, domain.PatchFunctionRequest{
			FilePath:   "f.go",
			Identifier: "F",
			Patches: []domain.FunctionPatch{
				{Op: domain.PatchOpWrap, Match: "doThing()", Wrap: "if err := %s; err != nil { return err }"},
			},
		})
		require.NoError(t, err)
		content := getFile(fs, "f.go")
		for _, line := range strings.Split(content, "\n") {
			if strings.Contains(line, "if err :=") {
				assert.True(t, strings.HasPrefix(line, "\t"), "wrapped line should be tab-indented, got: %q", line)
			}
		}
	})

	t.Run("wrap with occurrence targets the right duplicate call", func(t *testing.T) {
		h, fs := newPatchHandler()
		setFile(fs, "f.go", `package p
func F() error {
	doThing()
	doThing()
	return nil
}
`)
		_, err := h.PatchFunction(ctx, domain.PatchFunctionRequest{
			FilePath:   "f.go",
			Identifier: "F",
			Patches: []domain.FunctionPatch{
				{Op: domain.PatchOpWrap, Match: "doThing()", Occurrence: 2, Wrap: "if err := %s; err != nil { return err }"},
			},
		})
		require.NoError(t, err)
		content := getFile(fs, "f.go")
		lines := strings.Split(content, "\n")
		// First occurrence should still be plain doThing()
		foundPlain, foundWrapped := false, false
		for _, l := range lines {
			if strings.TrimSpace(l) == "doThing()" {
				foundPlain = true
			}
			if strings.Contains(l, "if err := doThing()") {
				foundWrapped = true
			}
		}
		assert.True(t, foundPlain, "first doThing() must remain unwrapped")
		assert.True(t, foundWrapped, "second doThing() must be wrapped")
	})

	t.Run("wrap invalid template returns error without writing", func(t *testing.T) {
		h, fs := newPatchHandler()
		src := `package p
func Bad() {
	doThing()
}
`
		setFile(fs, "f.go", src)
		_, err := h.PatchFunction(ctx, domain.PatchFunctionRequest{
			FilePath:   "f.go",
			Identifier: "Bad",
			Patches:    []domain.FunctionPatch{{Op: domain.PatchOpWrap, Match: "doThing()", Wrap: "if %s {{{"}},
		})
		require.Error(t, err)
		assert.Equal(t, src, getFile(fs, "f.go"))
	})
}

// ── atomicity ─────────────────────────────────────────────────────────────────

func TestPatchFunction_Atomicity(t *testing.T) {
	ctx := context.Background()

	src := `package p
func Handler() {
	a()
	b()
	c()
}
`

	t.Run("all patches applied when all succeed", func(t *testing.T) {
		h, fs := newPatchHandler()
		setFile(fs, "f.go", src)
		_, err := h.PatchFunction(ctx, domain.PatchFunctionRequest{
			FilePath:   "f.go",
			Identifier: "Handler",
			Patches: []domain.FunctionPatch{
				{Op: domain.PatchOpReplace, Match: "a()", Replace: "alpha()"},
				{Op: domain.PatchOpReplace, Match: "b()", Replace: "beta()"},
			},
		})
		require.NoError(t, err)
		content := getFile(fs, "f.go")
		assert.Contains(t, content, "alpha()")
		assert.Contains(t, content, "beta()")
		assert.Contains(t, content, "c()")
	})

	t.Run("no write when any patch fails", func(t *testing.T) {
		h, fs := newPatchHandler()
		setFile(fs, "f.go", src)
		_, err := h.PatchFunction(ctx, domain.PatchFunctionRequest{
			FilePath:   "f.go",
			Identifier: "Handler",
			Patches: []domain.FunctionPatch{
				{Op: domain.PatchOpReplace, Match: "a()", Replace: "alpha()"},
				{Op: domain.PatchOpReplace, Match: "nothere()", Replace: "x()"},
			},
		})
		require.Error(t, err)
		assert.Equal(t, src, getFile(fs, "f.go"), "file must not be written when any patch fails")
	})

	t.Run("all failure messages collected before returning", func(t *testing.T) {
		h, fs := newPatchHandler()
		setFile(fs, "f.go", src)
		_, err := h.PatchFunction(ctx, domain.PatchFunctionRequest{
			FilePath:   "f.go",
			Identifier: "Handler",
			Patches: []domain.FunctionPatch{
				{Op: domain.PatchOpReplace, Match: "miss1()", Replace: "x"},
				{Op: domain.PatchOpReplace, Match: "miss2()", Replace: "y"},
				{Op: domain.PatchOpReplace, Match: "miss3()", Replace: "z"},
			},
		})
		require.Error(t, err)
		msg := err.Error()
		assert.Contains(t, msg, "patch #1")
		assert.Contains(t, msg, "patch #2")
		assert.Contains(t, msg, "patch #3")
	})

	t.Run("patches resolved against original body, not intermediate state", func(t *testing.T) {
		// Both patches reference the original "x" text.
		// After applying patch #1 (replace x→y in line 1), line 2 still says "x"
		// and patch #2 should target it correctly via original offsets.
		h, fs := newPatchHandler()
		setFile(fs, "f.go", `package p
func F() {
	x := 1
	y := x
}
`)
		_, err := h.PatchFunction(ctx, domain.PatchFunctionRequest{
			FilePath:   "f.go",
			Identifier: "F",
			Patches: []domain.FunctionPatch{
				{Op: domain.PatchOpReplace, Match: "x := 1", Replace: "x := 10"},
				{Op: domain.PatchOpReplace, Match: "y := x", Replace: "y := x * 2"},
			},
		})
		require.NoError(t, err)
		content := getFile(fs, "f.go")
		assert.Contains(t, content, "x := 10")
		assert.Contains(t, content, "y := x * 2")
	})
}

// ── preview ───────────────────────────────────────────────────────────────────

func TestPatchFunction_Preview(t *testing.T) {
	ctx := context.Background()

	t.Run("preview returns diff without writing file", func(t *testing.T) {
		h, fs := newPatchHandler()
		src := `package p
func Greet() {
	say("hello")
}
`
		setFile(fs, "f.go", src)
		res, err := h.PatchFunction(ctx, domain.PatchFunctionRequest{
			FilePath:   "f.go",
			Identifier: "Greet",
			Patches:    []domain.FunctionPatch{{Op: domain.PatchOpReplace, Match: `"hello"`, Replace: `"world"`}},
			Preview:    true,
		})
		require.NoError(t, err)
		assert.True(t, res.Preview)
		assert.NotEmpty(t, res.Diff)
		assert.Contains(t, res.Diff, `"hello"`)
		assert.Contains(t, res.Diff, `"world"`)
		// File must not be mutated.
		assert.Equal(t, src, getFile(fs, "f.go"))
	})

	t.Run("preview applied count equals patch count", func(t *testing.T) {
		h, fs := newPatchHandler()
		setFile(fs, "f.go", `package p
func F() { a(); b() }
`)
		res, err := h.PatchFunction(ctx, domain.PatchFunctionRequest{
			FilePath:   "f.go",
			Identifier: "F",
			Patches: []domain.FunctionPatch{
				{Op: domain.PatchOpReplace, Match: "a()", Replace: "x()"},
				{Op: domain.PatchOpReplace, Match: "b()", Replace: "y()"},
			},
			Preview: true,
		})
		require.NoError(t, err)
		assert.Equal(t, 2, res.Applied)
	})
}

// ── receiver / identifier forms ───────────────────────────────────────────────

func TestPatchFunction_Identifiers(t *testing.T) {
	ctx := context.Background()

	t.Run("pointer receiver Receiver.Method", func(t *testing.T) {
		h, fs := newPatchHandler()
		setFile(fs, "f.go", `package p
type Worker struct{}
func (w *Worker) Run() {
	start()
}
`)
		_, err := h.PatchFunction(ctx, domain.PatchFunctionRequest{
			FilePath:   "f.go",
			Identifier: "Worker.Run",
			Patches:    []domain.FunctionPatch{{Op: domain.PatchOpReplace, Match: "start()", Replace: "stop()"}},
		})
		require.NoError(t, err)
		content := getFile(fs, "f.go")
		assert.Contains(t, content, "stop()")
		assert.NotContains(t, content, "start()")
	})

	t.Run("value receiver Receiver.Method", func(t *testing.T) {
		h, fs := newPatchHandler()
		setFile(fs, "f.go", `package p
type Counter struct{}
func (c Counter) Inc() {
	increment()
}
`)
		_, err := h.PatchFunction(ctx, domain.PatchFunctionRequest{
			FilePath:   "f.go",
			Identifier: "Counter.Inc",
			Patches:    []domain.FunctionPatch{{Op: domain.PatchOpReplace, Match: "increment()", Replace: "add(1)"}},
		})
		require.NoError(t, err)
		assert.Contains(t, getFile(fs, "f.go"), "add(1)")
	})

	t.Run("free function by plain name", func(t *testing.T) {
		h, fs := newPatchHandler()
		setFile(fs, "f.go", `package p
func Init() {
	setup()
}
`)
		_, err := h.PatchFunction(ctx, domain.PatchFunctionRequest{
			FilePath:   "f.go",
			Identifier: "Init",
			Patches:    []domain.FunctionPatch{{Op: domain.PatchOpReplace, Match: "setup()", Replace: "bootstrap()"}},
		})
		require.NoError(t, err)
		assert.Contains(t, getFile(fs, "f.go"), "bootstrap()")
	})

	t.Run("wrong receiver name does not match", func(t *testing.T) {
		h, fs := newPatchHandler()
		setFile(fs, "f.go", `package p
type A struct{}
type B struct{}
func (a A) Run() { fromA() }
func (b B) Run() { fromB() }
`)
		_, err := h.PatchFunction(ctx, domain.PatchFunctionRequest{
			FilePath:   "f.go",
			Identifier: "A.Run",
			Patches:    []domain.FunctionPatch{{Op: domain.PatchOpReplace, Match: "fromA()", Replace: "newA()"}},
		})
		require.NoError(t, err)
		content := getFile(fs, "f.go")
		assert.Contains(t, content, "newA()")
		assert.Contains(t, content, "fromB()", "B.Run must not be touched")
	})
}

// ── whitespace normalization ──────────────────────────────────────────────────

func TestPatchFunction_Normalization(t *testing.T) {
	ctx := context.Background()

	t.Run("match with extra spaces finds tab-indented line", func(t *testing.T) {
		h, fs := newPatchHandler()
		setFile(fs, "f.go", `package p
func Foo() {
	doSomething()
}
`)
		_, err := h.PatchFunction(ctx, domain.PatchFunctionRequest{
			FilePath:   "f.go",
			Identifier: "Foo",
			// Extra spaces in match — should still find the tab-indented "doSomething()"
			Patches: []domain.FunctionPatch{{Op: domain.PatchOpReplace, Match: "doSomething()", Replace: "doOther()"}},
		})
		require.NoError(t, err)
		assert.Contains(t, getFile(fs, "f.go"), "doOther()")
	})

	t.Run("match with collapsed internal spaces finds original", func(t *testing.T) {
		h, fs := newPatchHandler()
		setFile(fs, "f.go", `package p
func F() {
	x  :=  1
}
`)
		_, err := h.PatchFunction(ctx, domain.PatchFunctionRequest{
			FilePath:   "f.go",
			Identifier: "F",
			Patches:    []domain.FunctionPatch{{Op: domain.PatchOpReplace, Match: "x := 1", Replace: "x := 99"}},
		})
		require.NoError(t, err)
		assert.Contains(t, getFile(fs, "f.go"), "99")
	})
}

// ── regex-specific behaviour ──────────────────────────────────────────────────

func TestPatchFunction_MatchRegex(t *testing.T) {
	ctx := context.Background()

	t.Run("match_regex finds and deletes matching content", func(t *testing.T) {
		h, fs := newPatchHandler()
		setFile(fs, "f.go", `package p
func F() {
	// TODO: clean up
	doWork()
}
`)
		_, err := h.PatchFunction(ctx, domain.PatchFunctionRequest{
			FilePath:   "f.go",
			Identifier: "F",
			Patches:    []domain.FunctionPatch{{Op: domain.PatchOpDelete, MatchRegex: `// TODO:.*`}},
		})
		require.NoError(t, err)
		content := getFile(fs, "f.go")
		assert.NotContains(t, content, "TODO")
		assert.Contains(t, content, "doWork()")
	})

	t.Run("match_regex with occurrence selects nth match", func(t *testing.T) {
		h, fs := newPatchHandler()
		setFile(fs, "f.go", `package p
func F() {
	x := 1
	x = 2
	x = 3
}
`)
		_, err := h.PatchFunction(ctx, domain.PatchFunctionRequest{
			FilePath:   "f.go",
			Identifier: "F",
			Patches: []domain.FunctionPatch{
				{Op: domain.PatchOpReplace, MatchRegex: `x = \d`, Occurrence: 2, Replace: "x = 99"},
			},
		})
		require.NoError(t, err)
		content := getFile(fs, "f.go")
		assert.Contains(t, content, "x = 2")
		assert.Contains(t, content, "x = 99")
		assert.NotContains(t, content, "x = 3")
	})
}

func TestPatchFunction_RegexSafety(t *testing.T) {
	ctx := context.Background()

	const smallSrc = `package p
func F() { x := 1 }
`

	t.Run("rejects pattern exceeding length cap", func(t *testing.T) {
		h, fs := newPatchHandler()
		setFile(fs, "f.go", smallSrc)
		// 2KB pattern — easily exceeds the 1KB cap
		huge := strings.Repeat("a", 2048)
		_, err := h.PatchFunction(ctx, domain.PatchFunctionRequest{
			FilePath:   "f.go",
			Identifier: "F",
			Patches:    []domain.FunctionPatch{{Op: domain.PatchOpDelete, MatchRegex: huge}},
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "too long")
		assert.Equal(t, smallSrc, getFile(fs, "f.go"), "file must not be written")
	})

	t.Run("rejects pattern with explosive match count", func(t *testing.T) {
		h, fs := newPatchHandler()
		// Build a body with many newlines so `.` matches explosively.
		var sb strings.Builder
		sb.WriteString("package p\nfunc Big() {\n")
		for i := 0; i < 2000; i++ {
			sb.WriteString("\ta := 1\n")
		}
		sb.WriteString("}\n")
		src := sb.String()
		setFile(fs, "big.go", src)

		_, err := h.PatchFunction(ctx, domain.PatchFunctionRequest{
			FilePath:   "big.go",
			Identifier: "Big",
			Patches:    []domain.FunctionPatch{{Op: domain.PatchOpDelete, MatchRegex: `.`}},
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "matched more than")
		assert.Equal(t, src, getFile(fs, "big.go"), "file must not be written")
	})

	t.Run("rejects zero-width anchor match", func(t *testing.T) {
		h, fs := newPatchHandler()
		setFile(fs, "f.go", smallSrc)
		_, err := h.PatchFunction(ctx, domain.PatchFunctionRequest{
			FilePath:   "f.go",
			Identifier: "F",
			Patches:    []domain.FunctionPatch{{Op: domain.PatchOpDelete, MatchRegex: `^`}},
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "zero-width")
		assert.Equal(t, smallSrc, getFile(fs, "f.go"), "file must not be written")
	})

	t.Run("rejects zero-width empty group match", func(t *testing.T) {
		h, fs := newPatchHandler()
		setFile(fs, "f.go", smallSrc)
		_, err := h.PatchFunction(ctx, domain.PatchFunctionRequest{
			FilePath:   "f.go",
			Identifier: "F",
			Patches:    []domain.FunctionPatch{{Op: domain.PatchOpDelete, MatchRegex: `(?:)`}},
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "zero-width")
	})

	t.Run("rejects invalid regex syntax", func(t *testing.T) {
		h, fs := newPatchHandler()
		setFile(fs, "f.go", smallSrc)
		_, err := h.PatchFunction(ctx, domain.PatchFunctionRequest{
			FilePath:   "f.go",
			Identifier: "F",
			Patches:    []domain.FunctionPatch{{Op: domain.PatchOpDelete, MatchRegex: `[unclosed`}},
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "invalid match_regex")
		assert.Equal(t, smallSrc, getFile(fs, "f.go"))
	})

	t.Run("allows reasonable regex up to just under cap", func(t *testing.T) {
		// ~900 bytes pattern — under the 1024 cap, should succeed (no match in body)
		pattern := strings.Repeat("a", 900)
		h, fs := newPatchHandler()
		setFile(fs, "f.go", smallSrc)
		_, err := h.PatchFunction(ctx, domain.PatchFunctionRequest{
			FilePath:   "f.go",
			Identifier: "F",
			Patches:    []domain.FunctionPatch{{Op: domain.PatchOpDelete, MatchRegex: pattern}},
		})
		// Should fail with "no match found" (not a size error).
		require.Error(t, err)
		assert.Contains(t, err.Error(), "no match found")
	})

	t.Run("candidates list is capped in ambiguity error", func(t *testing.T) {
		// Force a literal (non-regex) pattern to match ~30 times; the error
		// message should truncate at maxCandidatesShown and indicate the total.
		h, fs := newPatchHandler()
		var sb strings.Builder
		sb.WriteString("package p\nfunc Big() {\n")
		for i := 0; i < 30; i++ {
			sb.WriteString("\tx := 0\n")
		}
		sb.WriteString("}\n")
		setFile(fs, "big.go", sb.String())

		_, err := h.PatchFunction(ctx, domain.PatchFunctionRequest{
			FilePath:   "big.go",
			Identifier: "Big",
			Patches:    []domain.FunctionPatch{{Op: domain.PatchOpReplace, Match: "x := 0", Replace: "x := 1"}},
		})
		require.Error(t, err)
		msg := err.Error()
		assert.Contains(t, msg, "matched 30 times")
		assert.Contains(t, msg, "more)")
	})

	t.Run("RE2 protects against would-be backtracking pattern", func(t *testing.T) {
		// A classic ReDoS trigger in PCRE: `(a+)+$` on a long "aaaa...X" string.
		// Under RE2 this runs in linear time — the test just verifies the call
		// completes quickly without hanging. We bound it with a short deadline.
		h, fs := newPatchHandler()
		setFile(fs, "f.go", `package p
func F() {
	s := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaX"
}
`)

		done := make(chan struct{})
		go func() {
			_, _ = h.PatchFunction(ctx, domain.PatchFunctionRequest{
				FilePath:   "f.go",
				Identifier: "F",
				Patches:    []domain.FunctionPatch{{Op: domain.PatchOpDelete, MatchRegex: `(a+)+$`}},
			})
			close(done)
		}()
		select {
		case <-done:
			// Success — RE2 did not hang.
		case <-time.After(2 * time.Second):
			t.Fatal("regex match hung — RE2 should run in linear time on (a+)+$")
		}
	})
}

// TestPatchFunction_MultiLineMatchAcrossNewline reproduces a failure observed
// in the harness-eval run-26 logs: an agent passed a multi-line match
// (a comment line followed by the next code line) and got "no match found"
// even though the text was definitely present.
//
// Root cause: mapNormBodyToOrig walked from byte 0 of the original body, but
// normalizeWS (via strings.Fields) trims leading whitespace — so on bodies
// that start with "\n\t" (the universal case for function bodies), the walk
// failed on the first character.
func TestPatchFunction_MultiLineMatchAcrossNewline(t *testing.T) {
	ctx := context.Background()
	h, fs := newPatchHandler()

	setFile(fs, "main.go", `package main

func main() {
	defer pool.Close()

	// Kafka backend: use a no-op backend (replace with a real kafka.Writer when ready).
	kafkaBackend := kafkaadapter.NewNoopBackend()

	backends := catalog.Backends{
		Postgres: pool,
	}
	_ = backends
	_ = kafkaBackend
}
`)

	// Exact match string from the failed agent call (comment + next line).
	matchText := "// Kafka backend: use a no-op backend (replace with a real kafka.Writer when ready).\n\tkafkaBackend := kafkaadapter.NewNoopBackend()"
	replaceText := "// Kafka backend: no-op (replace with a real broker client when ready).\n\tkafkaBackend := catalog.NoopKafkaBackend{}"

	res, err := h.PatchFunction(ctx, domain.PatchFunctionRequest{
		FilePath:   "main.go",
		Identifier: "main",
		Patches: []domain.FunctionPatch{
			{Op: domain.PatchOpReplace, Match: matchText, Replace: replaceText},
		},
	})
	require.NoError(t, err, "the multi-line match should be found")
	assert.Equal(t, 1, res.Applied)

	content := getFile(fs, "main.go")
	assert.Contains(t, content, "no-op (replace with a real broker client when ready)")
	assert.Contains(t, content, "catalog.NoopKafkaBackend{}")
	assert.NotContains(t, content, "kafkaadapter.NewNoopBackend()")
}

// TestPatchFunction_NoMatchSuggestsClosest verifies that a failed match
// returns "did you mean?" hints listing the lines that share the most tokens
// with the needle, so the agent can fix its call without re-reading the file.
// TestPatchFunction_NoMatchSuggestsClosest verifies that a failed match
// returns "did you mean?" hints listing the lines that share the most tokens
// with the needle, so the agent can fix its call without re-reading the file.
func TestPatchFunction_NoMatchSuggestsClosest(t *testing.T) {
	ctx := context.Background()

	t.Run("suggests lines sharing tokens with the needle", func(t *testing.T) {
		h, fs := newPatchHandler()
		setFile(fs, "f.go", `package p

func F() {
	for _, item := range items {
		doThing(item)
	}
	for _, book := range books {
		doThing(book)
	}
	x := 1
	_ = x
}
`)
		_, err := h.PatchFunction(ctx, domain.PatchFunctionRequest{
			FilePath:   "f.go",
			Identifier: "F",
			Patches: []domain.FunctionPatch{
				{Op: domain.PatchOpReplace, Match: "for _, thing := range things {", Replace: "// gone"},
			},
		})
		require.Error(t, err)
		msg := err.Error()
		assert.Contains(t, msg, "no match found")
		assert.Contains(t, msg, "Closest lines")
		assert.Contains(t, msg, "for _, item := range items {")
		assert.Contains(t, msg, "for _, book := range books {")
		assert.Contains(t, msg, "L")
	})

	t.Run("omits suggestions when no line shares any token", func(t *testing.T) {
		h, fs := newPatchHandler()
		setFile(fs, "f.go", `package p

func F() {
	x := 1
	_ = x
}
`)
		_, err := h.PatchFunction(ctx, domain.PatchFunctionRequest{
			FilePath:   "f.go",
			Identifier: "F",
			Patches: []domain.FunctionPatch{
				{Op: domain.PatchOpReplace, Match: "completely_unrelated_stuff_here", Replace: "x"},
			},
		})
		require.Error(t, err)
		msg := err.Error()
		assert.Contains(t, msg, "no match found")
		assert.NotContains(t, msg, "Closest lines")
	})

	t.Run("caps suggestions at 3 lines", func(t *testing.T) {
		h, fs := newPatchHandler()
		var body strings.Builder
		body.WriteString("package p\n\nfunc F() {\n")
		for i := 0; i < 10; i++ {
			fmt.Fprintf(&body, "\tdoThing(%d)\n", i)
		}
		body.WriteString("}\n")
		setFile(fs, "f.go", body.String())

		_, err := h.PatchFunction(ctx, domain.PatchFunctionRequest{
			FilePath:   "f.go",
			Identifier: "F",
			Patches: []domain.FunctionPatch{
				{Op: domain.PatchOpReplace, Match: "doThing(999)", Replace: "x"},
			},
		})
		require.Error(t, err)
		count := strings.Count(err.Error(), "tokens):")
		assert.Equal(t, 3, count, "suggestion list should be capped at 3")
	})

	t.Run("truncates very long candidate lines", func(t *testing.T) {
		h, fs := newPatchHandler()
		// Long line inside a string literal so Go still parses. The token
		// 'reallylongtoken' is shared between body and needle, so the line
		// will be scored as a candidate and must be truncated with '...'.
		longLit := strings.Repeat("reallylongtoken ", 20) + "finalToken"
		src := "package p\n\nfunc F() {\n\ts := \"" + longLit + "\"\n\t_ = s\n}\n"
		setFile(fs, "f.go", src)

		_, err := h.PatchFunction(ctx, domain.PatchFunctionRequest{
			FilePath:   "f.go",
			Identifier: "F",
			Patches: []domain.FunctionPatch{
				{Op: domain.PatchOpReplace, Match: "reallylongtoken needs fixing", Replace: "x"},
			},
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "...", "long lines should be truncated with ...")
	})
}

func TestPatchFunction_DeleteMultiLineBlockWithEmbeddedQuotes(t *testing.T) {
	ctx := context.Background()
	h, fs := newPatchHandler()

	setFile(fs, "main.go", `package main

import "fmt"

func main() {
	fmt.Println("a")
	if err := foo(); err == nil {
		panic("forcing YAML on JSON payload should fail")
	}
	fmt.Println("b")
}

func foo() error { return nil }
`)

	matchText := `if err := foo(); err == nil {
		panic("forcing YAML on JSON payload should fail")
	}`

	res, err := h.PatchFunction(ctx, domain.PatchFunctionRequest{
		FilePath:   "main.go",
		Identifier: "main",
		Patches: []domain.FunctionPatch{
			{Op: domain.PatchOpDelete, Match: matchText},
		},
	})
	require.NoError(t, err, "multi-line delete with embedded quotes should match")
	assert.Equal(t, 1, res.Applied)

	content := getFile(fs, "main.go")
	assert.NotContains(t, content, "forcing YAML on JSON payload should fail")
}

func TestPatchFunction_LineBasedTargeting(t *testing.T) {
	const sampleBody = `package p

func Greet(name string) string {
	if name == "" {
		return "hello"
	}
	return "hello " + name
}
`

	t.Run("at_line replaces the single line", func(t *testing.T) {
		fs := &mockFS{files: map[string][]byte{"p.go": []byte(sampleBody)}}
		h := commands.NewExecutePlanHandler(fs)
		// Greet: func at L3, body lines inside at L4-L8, "return \"hello\"" at L5.
		res, err := h.PatchFunction(context.Background(), domain.PatchFunctionRequest{
			FilePath:   "p.go",
			Identifier: "Greet",
			Patches: []domain.FunctionPatch{{
				Op:      domain.PatchOpReplace,
				AtLine:  5,
				Replace: "return \"hi\"",
			}},
		})
		require.NoError(t, err)
		assert.Equal(t, 1, res.Applied)
		assert.Contains(t, string(fs.files["p.go"]), "return \"hi\"")
		assert.NotContains(t, string(fs.files["p.go"]), "return \"hello\"\n")
	})

	t.Run("from_line/to_line deletes a range", func(t *testing.T) {
		fs := &mockFS{files: map[string][]byte{"p.go": []byte(sampleBody)}}
		h := commands.NewExecutePlanHandler(fs)
		// Delete the `if name == "" { return "hello" }` block (lines 4-6).
		_, err := h.PatchFunction(context.Background(), domain.PatchFunctionRequest{
			FilePath:   "p.go",
			Identifier: "Greet",
			Patches: []domain.FunctionPatch{{
				Op:       domain.PatchOpDelete,
				FromLine: 4,
				ToLine:   6,
			}},
		})
		require.NoError(t, err)
		updated := string(fs.files["p.go"])
		assert.NotContains(t, updated, "if name ==")
		assert.Contains(t, updated, "return \"hello \" + name")
	})

	t.Run("at_line with match rejected", func(t *testing.T) {
		fs := &mockFS{files: map[string][]byte{"p.go": []byte(sampleBody)}}
		h := commands.NewExecutePlanHandler(fs)
		_, err := h.PatchFunction(context.Background(), domain.PatchFunctionRequest{
			FilePath:   "p.go",
			Identifier: "Greet",
			Patches: []domain.FunctionPatch{{
				Op:     domain.PatchOpReplace,
				AtLine: 5,
				Match:  "hello",
			}},
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "mutually exclusive")
	})

	t.Run("out-of-range line errors cleanly", func(t *testing.T) {
		fs := &mockFS{files: map[string][]byte{"p.go": []byte(sampleBody)}}
		h := commands.NewExecutePlanHandler(fs)
		_, err := h.PatchFunction(context.Background(), domain.PatchFunctionRequest{
			FilePath:   "p.go",
			Identifier: "Greet",
			Patches: []domain.FunctionPatch{{
				Op:      domain.PatchOpReplace,
				AtLine:  999,
				Replace: "return \"\"",
			}},
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "out of")
	})
}

func TestPatchFunction_OccurrenceLeftoverWarning(t *testing.T) {
	// Body contains three occurrences of `x := 1`. Asking to replace
	// occurrence=2 should succeed and emit a warning pointing at the
	// other two line numbers.
	src := `package p

func Count() int {
	x := 1
	x := 1
	x := 1
	return x
}
`
	fs := &mockFS{files: map[string][]byte{"p.go": []byte(src)}}
	h := commands.NewExecutePlanHandler(fs)

	res, err := h.PatchFunction(context.Background(), domain.PatchFunctionRequest{
		FilePath:   "p.go",
		Identifier: "Count",
		Patches: []domain.FunctionPatch{{
			Op:         domain.PatchOpReplace,
			Match:      "x := 1",
			Replace:    "x := 2",
			Occurrence: 2,
		}},
	})
	require.NoError(t, err)
	require.Len(t, res.Warnings, 1, "expected exactly one leftover-matches warning")
	assert.Contains(t, res.Warnings[0], "2 more match")
	assert.Regexp(t, `L\d+, L\d+`, res.Warnings[0])
}

func TestPatchFunction_TokenBasedFallback(t *testing.T) {
	ctx := context.Background()

	t.Run("match ignores inline comment in body", func(t *testing.T) {
		h, fs := newPatchHandler()
		// Body has an inline comment that the match string omits.
		// Steps 1-3 (whitespace normalization) cannot handle this because
		// comments are not whitespace. Step 4 (token matching) succeeds
		// because go/scanner skips comments.
		setFile(fs, "f.go", `package p

func Build() {
	req := CreateRequest{
		Title: /* required */ "Clean Code",
	}
	_ = req
}
`)
		_, err := h.PatchFunction(ctx, domain.PatchFunctionRequest{
			FilePath:   "f.go",
			Identifier: "Build",
			Patches: []domain.FunctionPatch{{
				Op:      domain.PatchOpReplace,
				Match:   "req := CreateRequest{\n\tTitle: \"Clean Code\",\n}",
				Replace: "req := CreateRequest{\n\tTitle: \"Clean Architecture\",\n}",
			}},
		})
		require.NoError(t, err)
		assert.Contains(t, getFile(fs, "f.go"), "Clean Architecture")
		assert.NotContains(t, getFile(fs, "f.go"), "Clean Code")
	})

	t.Run("match ignores block comment spanning lines", func(t *testing.T) {
		h, fs := newPatchHandler()
		setFile(fs, "f.go", `package p

func Do() {
	x := 1
	/* TODO: remove this later */
	y := 2
	_ = x + y
}
`)
		_, err := h.PatchFunction(ctx, domain.PatchFunctionRequest{
			FilePath:   "f.go",
			Identifier: "Do",
			Patches: []domain.FunctionPatch{{
				Op:      domain.PatchOpReplace,
				Match:   "x := 1\ny := 2",
				Replace: "x := 10\ny := 20",
			}},
		})
		require.NoError(t, err)
		got := getFile(fs, "f.go")
		assert.Contains(t, got, "x := 10")
		assert.Contains(t, got, "y := 20")
	})
}

func TestFindTokenMatches(t *testing.T) {
	t.Run("basic single-line", func(t *testing.T) {
		body := "\tx := 1\n"
		match := "x := 1"
		hits := commands.FindTokenMatches(body, match)
		require.Len(t, hits, 1)
		assert.Equal(t, body[hits[0][0]:hits[0][1]], "\tx := 1")
	})

	t.Run("multi-line with different indentation", func(t *testing.T) {
		body := "\treq := Req{\n\t\tFoo: 1,\n\t}\n"
		match := "req := Req{\n    Foo: 1,\n}"
		hits := commands.FindTokenMatches(body, match)
		require.Len(t, hits, 1)
		assert.Contains(t, body[hits[0][0]:hits[0][1]], "req := Req{")
		assert.Contains(t, body[hits[0][0]:hits[0][1]], "Foo: 1,")
	})

	t.Run("skips comments in body", func(t *testing.T) {
		body := "\ta := /* init */ 1\n"
		match := "a := 1"
		hits := commands.FindTokenMatches(body, match)
		require.Len(t, hits, 1)
	})

	t.Run("no match returns nil", func(t *testing.T) {
		body := "\tx := 1\n"
		match := "y := 2"
		hits := commands.FindTokenMatches(body, match)
		assert.Nil(t, hits)
	})

	t.Run("multiple matches", func(t *testing.T) {
		body := "\tx := 1\n\ty := 2\n\tx := 1\n"
		match := "x := 1"
		hits := commands.FindTokenMatches(body, match)
		assert.Len(t, hits, 2)
	})
}

func TestPatchFunction_AutoLift_InsertInForLoop(t *testing.T) {
	// insert_after whose anchor is inside a for-loop body should auto-lift
	// the insertion to the top-level for-statement in the function body.
	h, fs := newPatchHandler()
	setFile(fs, "p.go", `package p

func Walk(items []int) int {
	total := 0
	for _, it := range items {
		total += it
		_ = it
	}
	return total
}
`)

	res, err := h.PatchFunction(context.Background(), domain.PatchFunctionRequest{
		FilePath:   "p.go",
		Identifier: "Walk",
		Patches: []domain.FunctionPatch{{
			Op:    domain.PatchOpInsertAfter,
			Match: "total += it",
			Code:  "total++",
		}},
	})
	require.NoError(t, err)
	require.Len(t, res.AutoLifts, 1, "expected a single auto-lift record")
	assert.Equal(t, 1, res.AutoLifts[0].PatchIndex)
	assert.Contains(t, res.AutoLifts[0].LiftedFrom, "for-loop body")
	assert.Contains(t, res.AutoLifts[0].LiftedTo, "function body")
	assert.NotEmpty(t, res.AutoLifts[0].Context, "auto-lift should carry context lines")
	// The inserted statement must land at the function's top level, not
	// inside the loop body: its line should not be indented with the
	// two-tab indent used inside the for body.
	out := getFile(fs, "p.go")
	assert.Regexp(t, `(?m)^\ttotal\+\+$`, out, "total++ must be inserted at top-level indentation")
}

func TestPatchFunction_AutoLift_InsertAtTopLevelIsSilent(t *testing.T) {
	// insert_after at the function's top level (not inside any inner block)
	// must not record an auto-lift.
	h, fs := newPatchHandler()
	setFile(fs, "p.go", `package p

func Walk(items []int) int {
	total := 0
	for _, it := range items {
		total += it
	}
	return total
}
`)

	res, err := h.PatchFunction(context.Background(), domain.PatchFunctionRequest{
		FilePath:   "p.go",
		Identifier: "Walk",
		Patches: []domain.FunctionPatch{{
			Op:    domain.PatchOpInsertAfter,
			Match: "total := 0",
			Code:  "total++",
		}},
	})
	require.NoError(t, err)
	assert.Empty(t, res.AutoLifts, "top-level insert should not auto-lift")
}

func TestPatchFunction_AutoLift_IfBranch(t *testing.T) {
	h, fs := newPatchHandler()
	setFile(fs, "p.go", `package p

func Pick(x int) int {
	if x > 0 {
		x = x * 2
	}
	return x
}
`)

	res, err := h.PatchFunction(context.Background(), domain.PatchFunctionRequest{
		FilePath:   "p.go",
		Identifier: "Pick",
		Patches: []domain.FunctionPatch{{
			Op:    domain.PatchOpInsertBefore,
			Match: "x = x * 2",
			Code:  "x++",
		}},
	})
	require.NoError(t, err)
	require.Len(t, res.AutoLifts, 1)
	assert.Contains(t, res.AutoLifts[0].LiftedFrom, "if/else branch")
	// insert_before on the if-stmt should place x++ BEFORE the if.
	out := getFile(fs, "p.go")
	ixStmt := strings.Index(out, "x++")
	ixIf := strings.Index(out, "if x > 0")
	require.GreaterOrEqual(t, ixStmt, 0)
	require.GreaterOrEqual(t, ixIf, 0)
	assert.Less(t, ixStmt, ixIf, "x++ should appear before the if statement")
}

func TestPatchFunction_AutoLift_Closure(t *testing.T) {
	// Anchor matches inside a func-literal closure — must lift to the
	// top-level statement that owns the closure, not stay inside it.
	h, fs := newPatchHandler()
	setFile(fs, "p.go", `package p

func Runner() {
	doStart()
	once(func() {
		registerTools()
	})
	doEnd()
}

func doStart()      {}
func doEnd()        {}
func registerTools() {}
func once(fn func()) { fn() }
`)

	res, err := h.PatchFunction(context.Background(), domain.PatchFunctionRequest{
		FilePath:   "p.go",
		Identifier: "Runner",
		// IncludeNested=true restores the legacy behavior where a text
		// match inside a closure is considered and then auto-lifted to the
		// top-level statement. With the default top-level-only scope
		// (task #26), the match would be filtered out entirely.
		IncludeNested: true,
		Patches: []domain.FunctionPatch{{
			Op:    domain.PatchOpInsertAfter,
			Match: "registerTools()",
			Code:  "registerMore()",
		}},
	})
	require.NoError(t, err)
	require.Len(t, res.AutoLifts, 1)
	assert.Contains(t, res.AutoLifts[0].LiftedFrom, "closure body")
	out := getFile(fs, "p.go")
	// registerMore() must land AFTER the once(...) call (top-level), BEFORE doEnd(),
	// and at single-tab indentation (top level of Runner).
	ixOnce := strings.Index(out, "once(")
	ixMore := strings.Index(out, "registerMore()")
	ixEnd := strings.Index(out, "doEnd()")
	require.GreaterOrEqual(t, ixOnce, 0)
	require.GreaterOrEqual(t, ixMore, 0)
	require.GreaterOrEqual(t, ixEnd, 0)
	assert.Greater(t, ixMore, ixOnce, "inserted call should appear after the once(...) block")
	assert.Less(t, ixMore, ixEnd, "inserted call should appear before doEnd()")
	assert.Regexp(t, `(?m)^\tregisterMore\(\)$`, out, "registerMore() should sit at top-level indentation")
}

func TestPatchFunction_AutoLift_AmbiguousAfterLift(t *testing.T) {
	// Anchor matches in two different nested scopes belonging to DIFFERENT
	// top-level statements. Auto-lift cannot pick one — refuse and list
	// the lift candidates.
	h, fs := newPatchHandler()
	setFile(fs, "p.go", `package p

func Run() {
	if flag() {
		doThing()
	}
	for i := 0; i < 3; i++ {
		doThing()
	}
}

func flag() bool { return true }
func doThing()   {}
`)

	_, err := h.PatchFunction(context.Background(), domain.PatchFunctionRequest{
		FilePath:   "p.go",
		Identifier: "Run",
		Patches: []domain.FunctionPatch{{
			Op:    domain.PatchOpInsertAfter,
			Match: "doThing()",
			Code:  "cleanup()",
		}},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "matched 2 times")
	assert.Contains(t, err.Error(), "Auto-lift candidates")
}

func TestPatchFunction_AutoLift_LineMode(t *testing.T) {
	// Same auto-lift when the insert is targeted via at_line rather than match.
	h, fs := newPatchHandler()
	setFile(fs, "p.go", `package p

func Walk(items []int) int {
	total := 0
	for _, it := range items {
		total += it
	}
	return total
}
`)

	res, err := h.PatchFunction(context.Background(), domain.PatchFunctionRequest{
		FilePath:   "p.go",
		Identifier: "Walk",
		Patches: []domain.FunctionPatch{{
			Op:     domain.PatchOpInsertAfter,
			AtLine: 6, // the `total += it` line
			Code:   "total++",
		}},
	})
	require.NoError(t, err)
	require.Len(t, res.AutoLifts, 1)
	assert.Contains(t, res.AutoLifts[0].LiftedFrom, "for-loop body")
	out := getFile(fs, "p.go")
	assert.Regexp(t, `(?m)^\ttotal\+\+$`, out, "total++ should be inserted at top-level indentation")
}

func TestPatchFunction_SetSignature_FreeFunction_ParamsOnly(t *testing.T) {
	h, fs := newPatchHandler()
	setFile(fs, "f.go", `package p

func Greet(name string) string {
	return "hi " + name
}
`)
	res, err := h.PatchFunction(context.Background(), domain.PatchFunctionRequest{
		FilePath:   "f.go",
		Identifier: "Greet",
		Patches: []domain.FunctionPatch{{
			Op:     domain.PatchOpSetSignature,
			Params: "(ctx context.Context, name string)",
		}},
	})
	require.NoError(t, err)
	assert.Equal(t, 1, res.Applied)
	content := getFile(fs, "f.go")
	assert.Contains(t, content, "func Greet(ctx context.Context, name string) string")
	// Body must be untouched.
	assert.Contains(t, content, `return "hi " + name`)
}

func TestPatchFunction_SetSignature_FreeFunction_ReturnsOnly(t *testing.T) {
	h, fs := newPatchHandler()
	setFile(fs, "f.go", `package p

func Load(name string) string {
	return name
}
`)
	res, err := h.PatchFunction(context.Background(), domain.PatchFunctionRequest{
		FilePath:   "f.go",
		Identifier: "Load",
		Patches: []domain.FunctionPatch{{
			Op:      domain.PatchOpSetSignature,
			Returns: "([]byte, error)",
		}},
	})
	require.NoError(t, err)
	assert.Equal(t, 1, res.Applied)
	content := getFile(fs, "f.go")
	assert.Contains(t, content, "func Load(name string) ([]byte, error)")
	// Existing params and body unchanged.
	assert.Contains(t, content, "name string")
	assert.Contains(t, content, "return name")
}

func TestPatchFunction_SetSignature_FreeFunction_Both(t *testing.T) {
	h, fs := newPatchHandler()
	setFile(fs, "f.go", `package p

func Do(x int) int {
	return x + 1
}
`)
	res, err := h.PatchFunction(context.Background(), domain.PatchFunctionRequest{
		FilePath:   "f.go",
		Identifier: "Do",
		Patches: []domain.FunctionPatch{{
			Op:      domain.PatchOpSetSignature,
			Params:  "(ctx context.Context, x int)",
			Returns: "(int, error)",
		}},
	})
	require.NoError(t, err)
	assert.Equal(t, 1, res.Applied)
	content := getFile(fs, "f.go")
	assert.Contains(t, content, "func Do(ctx context.Context, x int) (int, error)")
	assert.Contains(t, content, "return x + 1")
}

func TestPatchFunction_SetSignature_Method_PreservesReceiver(t *testing.T) {
	h, fs := newPatchHandler()
	setFile(fs, "f.go", `package p

type S struct{}

func (s *S) Run(x int) error {
	_ = x
	return nil
}
`)
	res, err := h.PatchFunction(context.Background(), domain.PatchFunctionRequest{
		FilePath:   "f.go",
		Identifier: "S.Run",
		Patches: []domain.FunctionPatch{{
			Op:      domain.PatchOpSetSignature,
			Params:  "(ctx context.Context, x int)",
			Returns: "error",
		}},
	})
	require.NoError(t, err)
	assert.Equal(t, 1, res.Applied)
	content := getFile(fs, "f.go")
	// Receiver must be intact.
	assert.Contains(t, content, "func (s *S) Run(ctx context.Context, x int) error")
	assert.Contains(t, content, "return nil")
}

func TestPatchFunction_SetSignature_Generic_PreservesTypeParams(t *testing.T) {
	h, fs := newPatchHandler()
	setFile(fs, "f.go", `package p

func Foo[T any](x T) T {
	return x
}
`)
	res, err := h.PatchFunction(context.Background(), domain.PatchFunctionRequest{
		FilePath:   "f.go",
		Identifier: "Foo",
		Patches: []domain.FunctionPatch{{
			Op:      domain.PatchOpSetSignature,
			Params:  "(x T, y T)",
			Returns: "T",
		}},
	})
	require.NoError(t, err)
	assert.Equal(t, 1, res.Applied)
	content := getFile(fs, "f.go")
	// Generic type params must still be there.
	assert.Contains(t, content, "func Foo[T any](x T, y T) T")
	assert.Contains(t, content, "return x")
}

func TestPatchFunction_SetSignature_InvalidParamsRejected(t *testing.T) {
	h, fs := newPatchHandler()
	setFile(fs, "f.go", `package p

func Foo(x int) int {
	return x
}
`)
	_, err := h.PatchFunction(context.Background(), domain.PatchFunctionRequest{
		FilePath:   "f.go",
		Identifier: "Foo",
		Patches: []domain.FunctionPatch{{
			Op:     domain.PatchOpSetSignature,
			Params: "(x int, ,)", // syntactically invalid
		}},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "set_signature")
	// File must not have been rewritten.
	assert.Contains(t, getFile(fs, "f.go"), "func Foo(x int) int")
}

func TestPatchFunction_SetSignature_RequiresParamsOrReturns(t *testing.T) {
	h, fs := newPatchHandler()
	setFile(fs, "f.go", `package p

func Foo(x int) int { return x }
`)
	_, err := h.PatchFunction(context.Background(), domain.PatchFunctionRequest{
		FilePath:   "f.go",
		Identifier: "Foo",
		Patches: []domain.FunctionPatch{{
			Op: domain.PatchOpSetSignature,
		}},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "at least one of params or returns")
}

func TestPatchFunction_SetSignature_ParamsNeedParens(t *testing.T) {
	h, fs := newPatchHandler()
	setFile(fs, "f.go", `package p

func Foo(x int) int { return x }
`)
	_, err := h.PatchFunction(context.Background(), domain.PatchFunctionRequest{
		FilePath:   "f.go",
		Identifier: "Foo",
		Patches: []domain.FunctionPatch{{
			Op:     domain.PatchOpSetSignature,
			Params: "x int",
		}},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "parens")
}

func TestPatchFunction_SetSignature_WithBodyPatch(t *testing.T) {
	h, fs := newPatchHandler()
	setFile(fs, "f.go", `package p

func Add(a int, b int) int {
	return a + b
}
`)
	res, err := h.PatchFunction(context.Background(), domain.PatchFunctionRequest{
		FilePath:   "f.go",
		Identifier: "Add",
		Patches: []domain.FunctionPatch{
			{
				Op:      domain.PatchOpSetSignature,
				Params:  "(a, b, c int)",
				Returns: "int",
			},
			{
				Op:      domain.PatchOpReplace,
				Match:   "return a + b",
				Replace: "return a + b + c",
			},
		},
	})
	require.NoError(t, err)
	assert.Equal(t, 2, res.Applied)
	content := getFile(fs, "f.go")
	assert.Contains(t, content, "func Add(a, b, c int) int")
	assert.Contains(t, content, "return a + b + c")
	assert.NotContains(t, content, "return a + b\n")
}

func TestPatchFunction_SetSignature_Preview(t *testing.T) {
	h, fs := newPatchHandler()
	original := `package p

func Foo(x int) int {
	return x
}
`
	setFile(fs, "f.go", original)
	res, err := h.PatchFunction(context.Background(), domain.PatchFunctionRequest{
		FilePath:   "f.go",
		Identifier: "Foo",
		Preview:    true,
		Patches: []domain.FunctionPatch{{
			Op:     domain.PatchOpSetSignature,
			Params: "(x, y int)",
		}},
	})
	require.NoError(t, err)
	assert.True(t, res.Preview)
	assert.NotEmpty(t, res.Diff)
	assert.Contains(t, res.Diff, "func Foo(x, y int) int")
	// File on disk is unchanged.
	assert.Equal(t, original, getFile(fs, "f.go"))
}

func TestPatchFunction_SetSignature_AddReturnsWhereNoneExisted(t *testing.T) {
	h, fs := newPatchHandler()
	setFile(fs, "f.go", `package p

func Foo(x int) {
	_ = x
}
`)
	res, err := h.PatchFunction(context.Background(), domain.PatchFunctionRequest{
		FilePath:   "f.go",
		Identifier: "Foo",
		Patches: []domain.FunctionPatch{{
			Op:      domain.PatchOpSetSignature,
			Returns: "error",
		}},
	})
	require.NoError(t, err)
	assert.Equal(t, 1, res.Applied)
	content := getFile(fs, "f.go")
	assert.Contains(t, content, "func Foo(x int) error")
}

// TestPatchFunction_TopLevelOnly_Default covers task #26: by default,
// patch_function restricts text matches to the target function's own
// top-level body, ignoring anything inside nested closures.
func TestPatchFunction_TopLevelOnly_Default(t *testing.T) {
	// Fixture: three closures + the outer body all contain the same
	// literal. The outer body has TWO hits of 'registerPatchTools(s, commands)';
	// each closure contributes one more if nested scanning is enabled.
	const fixture = `package p

func registerAll(s *server, commands cmds) {
	registerPatchTools(s, commands)
	run(func() {
		registerPatchTools(s, commands)
	})
	run(func() {
		registerPatchTools(s, commands)
	})
	run(func() {
		registerPatchTools(s, commands)
	})
	registerPatchTools(s, commands)
}

type server struct{}
type cmds struct{}

func run(fn func())                                   { fn() }
func registerPatchTools(_ *server, _ cmds)             {}
`

	t.Run("default scope errors with only outer hits listed", func(t *testing.T) {
		h, fs := newPatchHandler()
		setFile(fs, "p.go", fixture)
		_, err := h.PatchFunction(context.Background(), domain.PatchFunctionRequest{
			FilePath:   "p.go",
			Identifier: "registerAll",
			Patches: []domain.FunctionPatch{
				{Op: domain.PatchOpReplace, Match: "registerPatchTools(s, commands)", Replace: "registerOtherTools(s, commands)"},
			},
		})
		require.Error(t, err)
		// Only the two outer-body hits should be reported (not the three inside closures).
		assert.Contains(t, err.Error(), "matched 2 times", "default scope must hide closure hits; expected 2 outer hits, got: %s", err.Error())
		// The file must be unchanged (atomic failure).
		assert.Equal(t, fixture, getFile(fs, "p.go"))
	})

	t.Run("occurrence targets only outer hits; closures untouched", func(t *testing.T) {
		h, fs := newPatchHandler()
		setFile(fs, "p.go", fixture)
		res, err := h.PatchFunction(context.Background(), domain.PatchFunctionRequest{
			FilePath:   "p.go",
			Identifier: "registerAll",
			Patches: []domain.FunctionPatch{
				{Op: domain.PatchOpReplace, Match: "registerPatchTools(s, commands)", Occurrence: 1, Replace: "registerOtherTools(s, commands)"},
			},
		})
		require.NoError(t, err)
		assert.Equal(t, 1, res.Applied)
		content := getFile(fs, "p.go")
		// The first outer hit is replaced; the three closure hits and the second outer hit remain.
		assert.Equal(t, 1, strings.Count(content, "registerOtherTools(s, commands)"))
		assert.Equal(t, 4, strings.Count(content, "registerPatchTools(s, commands)"),
			"three closure hits + one remaining outer hit should survive")
	})

	t.Run("include_nested=true restores legacy scope: 5 total hits", func(t *testing.T) {
		h, fs := newPatchHandler()
		setFile(fs, "p.go", fixture)
		_, err := h.PatchFunction(context.Background(), domain.PatchFunctionRequest{
			FilePath:      "p.go",
			Identifier:    "registerAll",
			IncludeNested: true,
			Patches: []domain.FunctionPatch{
				{Op: domain.PatchOpReplace, Match: "registerPatchTools(s, commands)", Replace: "registerOtherTools(s, commands)"},
			},
		})
		require.Error(t, err)
		// With nested scanning enabled, all 5 occurrences (2 outer + 3 in closures) show up.
		assert.Contains(t, err.Error(), "matched 5 times", "include_nested=true must see all hits; got: %s", err.Error())
	})
}

// TestPatchFunction_ClosurePathIdentifier covers task #28: the
// identifier syntax 'Parent>closure[N]' targets the Nth *ast.FuncLit
// inside Parent (0-based, by AST appearance order). Out-of-range
// indices return a clear error.
func TestPatchFunction_ClosurePathIdentifier(t *testing.T) {
	const fixture = `package p

func Outer() {
	fn1 := func() {
		markerA()
	}
	fn2 := func() {
		markerB()
	}
	_ = fn1
	_ = fn2
}

func markerA() {}
func markerB() {}
`

	t.Run("closure[0] edits only fn1 body", func(t *testing.T) {
		h, fs := newPatchHandler()
		setFile(fs, "p.go", fixture)
		res, err := h.PatchFunction(context.Background(), domain.PatchFunctionRequest{
			FilePath:   "p.go",
			Identifier: "Outer>closure[0]",
			Patches: []domain.FunctionPatch{
				{Op: domain.PatchOpReplace, Match: "markerA()", Replace: "markerA_edited()"},
			},
		})
		require.NoError(t, err)
		assert.Equal(t, 1, res.Applied)
		content := getFile(fs, "p.go")
		assert.Contains(t, content, "markerA_edited()")
		assert.NotContains(t, content, "\tmarkerA()\n")
		// fn2 must be untouched.
		assert.Contains(t, content, "markerB()")
	})

	t.Run("closure[1] edits only fn2 body", func(t *testing.T) {
		h, fs := newPatchHandler()
		setFile(fs, "p.go", fixture)
		res, err := h.PatchFunction(context.Background(), domain.PatchFunctionRequest{
			FilePath:   "p.go",
			Identifier: "Outer>closure[1]",
			Patches: []domain.FunctionPatch{
				{Op: domain.PatchOpReplace, Match: "markerB()", Replace: "markerB_edited()"},
			},
		})
		require.NoError(t, err)
		assert.Equal(t, 1, res.Applied)
		content := getFile(fs, "p.go")
		assert.Contains(t, content, "markerB_edited()")
		assert.NotContains(t, content, "\tmarkerB()\n")
		// fn1 must be untouched.
		assert.Contains(t, content, "markerA()")
	})

	t.Run("closure[9] out of range errors clearly", func(t *testing.T) {
		h, fs := newPatchHandler()
		setFile(fs, "p.go", fixture)
		_, err := h.PatchFunction(context.Background(), domain.PatchFunctionRequest{
			FilePath:   "p.go",
			Identifier: "Outer>closure[9]",
			Patches: []domain.FunctionPatch{
				{Op: domain.PatchOpReplace, Match: "markerA()", Replace: "markerA_edited()"},
			},
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "closure index 9 out of range (0..1)", "expected range-error message; got: %s", err.Error())
	})

	t.Run("invalid closure segment reports parse error", func(t *testing.T) {
		h, fs := newPatchHandler()
		setFile(fs, "p.go", fixture)
		_, err := h.PatchFunction(context.Background(), domain.PatchFunctionRequest{
			FilePath:   "p.go",
			Identifier: "Outer>closure",
			Patches: []domain.FunctionPatch{
				{Op: domain.PatchOpReplace, Match: "markerA()", Replace: "x"},
			},
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "invalid closure segment")
	})
}
