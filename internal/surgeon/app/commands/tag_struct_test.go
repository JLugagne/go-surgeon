package commands_test

import (
	"context"
	"testing"

	"github.com/JLugagne/go-surgeon/internal/surgeon/app/commands"
	"github.com/JLugagne/go-surgeon/internal/surgeon/domain"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTagStruct(t *testing.T) {
	fs := &mockFS{files: make(map[string][]byte)}
	handler := commands.NewExecutePlanHandler(fs)
	ctx := context.Background()

	fs.files["main.go"] = []byte(`package main

type User struct {
	ID        string
	FirstName string
	lastName  string
	Email     string ` + "`" + `json:"email,omitempty"` + "`" + `
}
`)

	t.Run("specific field exact tag", func(t *testing.T) {
		req := domain.TagRequest{
			FilePath:   "main.go",
			StructName: "User",
			FieldName:  "FirstName",
			SetTag:     `json:"first_name"`,
		}
		err := handler.TagStruct(ctx, req)
		require.NoError(t, err)

		src := string(fs.files["main.go"])
		assert.Contains(t, src, "FirstName string `json:\"first_name\"`")
	})

	t.Run("auto tag all exported fields", func(t *testing.T) {
		req := domain.TagRequest{
			FilePath:   "main.go",
			StructName: "User",
			AutoFormat: "bson",
		}
		err := handler.TagStruct(ctx, req)
		require.NoError(t, err)

		src := string(fs.files["main.go"])
		assert.Contains(t, src, "ID        string `bson:\"id\"`")
		assert.Contains(t, src, "FirstName string `json:\"first_name\" bson:\"first_name\"`")
		assert.Contains(t, src, "Email     string `json:\"email,omitempty\" bson:\"email\"`")
		// unexported should be skipped
		assert.Contains(t, src, "lastName  string\n")
	})

	t.Run("do not overwrite existing tag", func(t *testing.T) {
		req := domain.TagRequest{
			FilePath:   "main.go",
			StructName: "User",
			AutoFormat: "json",
		}
		err := handler.TagStruct(ctx, req)
		require.NoError(t, err)

		src := string(fs.files["main.go"])
		// Should add JSON where missing but leave existing alone
		assert.Contains(t, src, "ID        string `bson:\"id\" json:\"id\"`")
		assert.Contains(t, src, "FirstName string `json:\"first_name\" bson:\"first_name\"`")
		assert.Contains(t, src, "Email     string `json:\"email,omitempty\" bson:\"email\"`")
	})
}
