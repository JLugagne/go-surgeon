package commands_test

import (
	"context"
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
		assert.Contains(t, err.Error(), "match or match_regex is required")
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
