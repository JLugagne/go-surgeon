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
		assert.Contains(t, testSrc, "got, err := tt.s.DoWork(tt.args.ctx, tt.args.id, tt.args.name)")
	})

	t.Run("generate test for simple function", func(t *testing.T) {
		testFile, err := handler.GenerateTest(ctx, "main.go", "SimpleFunc")
		require.NoError(t, err)
		assert.Equal(t, "main_test.go", testFile)

		testSrc := string(fs.files["main_test.go"])
		assert.Contains(t, testSrc, "func TestSimpleFunc(t *testing.T)")
		assert.Contains(t, testSrc, "got := SimpleFunc()")
		assert.Contains(t, testSrc, "assert.Equal(t, tt.want0, got)")
		
		// Ensure it didn't duplicate package statement
		assert.Equal(t, 1, strings.Count(testSrc, "package main_test"))
	})
}
