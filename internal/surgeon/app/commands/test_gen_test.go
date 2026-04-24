package commands_test

import (
	"context"
	"strings"
	"testing"

	"github.com/JLugagne/go-surgeon/internal/surgeon/app/commands"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGenerateTest(t *testing.T) {
	fs := &mockFS{files: make(map[string][]byte)}
	handler := commands.NewExecutePlanHandler(fs)
	ctx := context.Background()

	fs.files["main.go"] = []byte(`package main
import "context"

type Service struct{}

func (s *Service) DoWork(ctx context.Context, id int, name string) (string, error) {
  return "", nil
}

func SimpleFunc() string {
  return "ok"
}
`)

	t.Run("generate test for method", func(t *testing.T) {
		testFile, err := handler.GenerateTest(ctx, "main.go", "(*Service).DoWork")
		require.NoError(t, err)
		assert.Equal(t, "main_test.go", testFile)

		testSrc := string(fs.files["main_test.go"])
		assert.Contains(t, testSrc, "func TestService_DoWork(t *testing.T)")
		assert.Contains(t, testSrc, "args struct {")
		assert.Contains(t, testSrc, "id")
		assert.Contains(t, testSrc, "int")
		assert.Contains(t, testSrc, "name string")
		// t.Run body is a single t.Fatal placeholder — agent must fill it in or delete.
		assert.Contains(t, testSrc, "TODO(go-surgeon): implement this test case")
		assert.Contains(t, testSrc, "TODO(go-surgeon): no test cases defined for TestService_DoWork")
	})

	t.Run("generate test for simple function", func(t *testing.T) {
		testFile, err := handler.GenerateTest(ctx, "main.go", "SimpleFunc")
		require.NoError(t, err)
		assert.Equal(t, "main_test.go", testFile)

		testSrc := string(fs.files["main_test.go"])
		assert.Contains(t, testSrc, "func TestSimpleFunc(t *testing.T)")
		// t.Run body is a placeholder — no call/assertion wiring.
		assert.Contains(t, testSrc, "TODO(go-surgeon): implement this test case")
		assert.NotContains(t, testSrc, "assert.Equal")

		// Ensure it didn't duplicate package statement
		assert.Equal(t, 1, strings.Count(testSrc, "package main_test"))
	})

	t.Run("unexported receiver uses white-box package", func(t *testing.T) {
		fs2 := &mockFS{files: make(map[string][]byte)}
		h2 := commands.NewExecutePlanHandler(fs2)

		fs2.files["pkg/repo.go"] = []byte(`package pg

type bookRepository struct{}

func (b *bookRepository) Create(id int) error {
  return nil
}
`)
		testFile, err := h2.GenerateTest(ctx, "pkg/repo.go", "(*bookRepository).Create")
		require.NoError(t, err)
		// Unexported receiver → white-box internal test file, same package name (no _test suffix in package decl).
		assert.Equal(t, "pkg/repo_internal_test.go", testFile)
		testSrc := string(fs2.files["pkg/repo_internal_test.go"])
		// White-box: package pg (not pg_test)
		assert.Contains(t, testSrc, "package pg\n")
		assert.NotContains(t, testSrc, "package pg_test")
		// Receiver type accessible without qualification
		assert.Contains(t, testSrc, "*bookRepository")
	})

	t.Run("detects testify from sibling test files", func(t *testing.T) {
		fs3 := &mockFS{files: make(map[string][]byte)}
		h3 := commands.NewExecutePlanHandler(fs3)

		// Sibling test file importing testify
		fs3.files["pkg/other_test.go"] = []byte(`package pg_test

import (
  "testing"
  "github.com/stretchr/testify/assert"
  "github.com/stretchr/testify/require"
)

func TestOther(t *testing.T) {}
`)
		fs3.files["pkg/service.go"] = []byte(`package pg

func Compute(x int) int {
  return x * 2
}
`)
		testFile, err := h3.GenerateTest(ctx, "pkg/service.go", "Compute")
		require.NoError(t, err)
		assert.Equal(t, "pkg/service_test.go", testFile)
		testSrc := string(fs3.files["pkg/service_test.go"])
		// t.Run body is a t.Fatal placeholder; we still drive the import header
		// off detectAssertLib so the file stays compilable once filled in.
		assert.Contains(t, testSrc, "testify/assert")
		assert.Contains(t, testSrc, "TODO(go-surgeon): implement this test case")
	})

	t.Run("exported receiver is package-qualified in black-box test", func(t *testing.T) {
		// Bug 003 (bookstore-8): exported receiver type must be pkg.Type in _test package.
		fs4 := &mockFS{files: make(map[string][]byte)}
		h4 := commands.NewExecutePlanHandler(fs4)

		fs4.files["app/book.go"] = []byte(`package app

type App struct{}

func (a *App) CreateBook(title string) error {
  return nil
}
`)
		testFile, err := h4.GenerateTest(ctx, "app/book.go", "App.CreateBook")
		require.NoError(t, err)
		assert.Equal(t, "app/book_test.go", testFile)

		testSrc := string(fs4.files["app/book_test.go"])
		assert.Contains(t, testSrc, "package app_test")
		// Receiver must be qualified: *app.App not *App
		assert.Contains(t, testSrc, "*app.App")
		assert.NotContains(t, testSrc, "\t\ta     *App")
	})

	t.Run("exported struct return type uses reflect.DeepEqual", func(t *testing.T) {
		// Bug 004 (bookstore-8) + bookstore-10: exported struct types are conservatively
		// treated as potentially containing slice/map fields → reflect.DeepEqual.
		fs5 := &mockFS{files: make(map[string][]byte)}
		h5 := commands.NewExecutePlanHandler(fs5)

		fs5.files["conv/book.go"] = []byte(`package converters

type BookListResponse struct{ Items []string }

func ToPublicBookList(ids []string) BookListResponse {
  return BookListResponse{}
}
`)
		_, err := h5.GenerateTest(ctx, "conv/book.go", "ToPublicBookList")
		require.NoError(t, err)

		testSrc := string(fs5.files["conv/book_test.go"])
		// Slice param must appear in args struct.
		assert.Contains(t, testSrc, "[]string")
		// Body is a t.Fatal placeholder — no call/assertion wiring.
		assert.Contains(t, testSrc, "TODO(go-surgeon): implement this test case")
		assert.NotContains(t, testSrc, "got != want")
	})

	t.Run("slice return type directly uses reflect.DeepEqual", func(t *testing.T) {
		fs6 := &mockFS{files: make(map[string][]byte)}
		h6 := commands.NewExecutePlanHandler(fs6)

		fs6.files["svc/book.go"] = []byte(`package svc

func ListIDs() []string {
  return nil
}
`)
		_, err := h6.GenerateTest(ctx, "svc/book.go", "ListIDs")
		require.NoError(t, err)

		testSrc := string(fs6.files["svc/book_test.go"])
		// Body is a t.Fatal placeholder — no reflect.DeepEqual wiring.
		assert.Contains(t, testSrc, "TODO(go-surgeon): implement this test case")
		assert.NotContains(t, testSrc, "got != want")
	})

	t.Run("unexported free function uses internal test file", func(t *testing.T) {
		fs7 := &mockFS{files: make(map[string][]byte)}
		h7 := commands.NewExecutePlanHandler(fs7)

		fs7.files["util/helper.go"] = []byte(`package util

func computeHash(s string) int {
  return len(s)
}
`)
		testFile, err := h7.GenerateTest(ctx, "util/helper.go", "computeHash")
		require.NoError(t, err)
		assert.Equal(t, "util/helper_internal_test.go", testFile)
		testSrc := string(fs7.files["util/helper_internal_test.go"])
		assert.Contains(t, testSrc, "package util\n")
		assert.NotContains(t, testSrc, "package util_test")
		// Body is a t.Fatal placeholder — no call wiring.
		assert.Contains(t, testSrc, "TODO(go-surgeon): implement this test case")
	})

	t.Run("test dedup: skips if test func already exists", func(t *testing.T) {
		fs8 := &mockFS{files: make(map[string][]byte)}
		h8 := commands.NewExecutePlanHandler(fs8)

		fs8.files["svc/foo.go"] = []byte(`package svc

func Greet(name string) string {
  return "hello " + name
}
`)
		fs8.files["svc/foo_test.go"] = []byte(`package svc_test

import "testing"

func TestGreet(t *testing.T) {}
`)
		testFile, err := h8.GenerateTest(ctx, "svc/foo.go", "Greet")
		require.NoError(t, err)
		// Returns existing file path without duplicating
		assert.Equal(t, "svc/foo_test.go", testFile)
		// File content unchanged (TestGreet not duplicated)
		assert.Equal(t, 1, strings.Count(string(fs8.files["svc/foo_test.go"]), "func TestGreet"))
	})
}
