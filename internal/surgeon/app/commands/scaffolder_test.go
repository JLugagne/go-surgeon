package commands_test

import (
	"context"
	"os"
	"testing"

	"github.com/JLugagne/go-surgeon/internal/surgeon/app/commands"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupTestTemplates(t *testing.T, fs *mockFS) {
	err := os.MkdirAll(".surgeon-templates/hexagonal", 0755)
	require.NoError(t, err)

	manifest := `name: hexagonal
description: A test template
commands:
  - command: bootstrap
    description: bootstraps
    variables:
      - key: AppName
        description: app name
    files:
      - source: main.go.tmpl
        destination: cmd/{{ .AppName }}/main.go
    post_commands:
      - add_app
    hint: Bootstrapped

  - command: add_app
    description: adds app
    variables:
      - key: AppName
        description: app name
    files:
      - source: app.go.tmpl
        destination: internal/{{ .AppName }}/app.go
    hint: App added
`
	fs.files[".surgeon-templates/hexagonal/manifest.yaml"] = []byte(manifest)
	fs.files[".surgeon-templates/hexagonal/main.go.tmpl"] = []byte("package main\nfunc main() {}")
	fs.files[".surgeon-templates/hexagonal/app.go.tmpl"] = []byte("package app\ntype App struct{}")
}

func setupCycleTemplate(t *testing.T, fs *mockFS) {
	err := os.MkdirAll(".surgeon-templates/cycle", 0755)
	require.NoError(t, err)

	manifest := `name: cycle
commands:
  - command: A
    post_commands: [B]
  - command: B
    post_commands: [C]
  - command: C
    post_commands: [A]
`
	fs.files[".surgeon-templates/cycle/manifest.yaml"] = []byte(manifest)
}

func TestScaffolder_GetTemplate(t *testing.T) {
	fs := &mockFS{files: make(map[string][]byte)}
	setupTestTemplates(t, fs)
	setupCycleTemplate(t, fs)

	handler := commands.NewScaffolderHandler(fs)

	t.Run("Valid Manifest", func(t *testing.T) {
		tmpl, err := handler.GetTemplate(context.Background(), "hexagonal")
		require.NoError(t, err)
		assert.Equal(t, "hexagonal", tmpl.Name)
		assert.Len(t, tmpl.Commands, 2)
	})

	t.Run("Cyclic Manifest", func(t *testing.T) {
		_, err := handler.GetTemplate(context.Background(), "cycle")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "cycle detected")
	})
}

func TestScaffolder_Execute(t *testing.T) {
	fs := &mockFS{files: make(map[string][]byte)}
	setupTestTemplates(t, fs)

	handler := commands.NewScaffolderHandler(fs)
	ctx := context.Background()

	t.Run("Execute Chain", func(t *testing.T) {
		params := map[string]string{"AppName": "testapp"}
		err := handler.Execute(ctx, "hexagonal", "bootstrap", params)
		require.NoError(t, err)

		// Check files were created in mock FS
		_, okMain := fs.files["cmd/testapp/main.go"]
		assert.True(t, okMain, "main.go should be created")

		_, okApp := fs.files["internal/testapp/app.go"]
		assert.True(t, okApp, "app.go should be created")
	})

	t.Run("Pre-flight Check Failure", func(t *testing.T) {
		// Create the file beforehand so it fails pre-flight
		fs.files["cmd/conflict/main.go"] = []byte("exists")

		params := map[string]string{"AppName": "conflict"}
		err := handler.Execute(ctx, "hexagonal", "bootstrap", params)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "already exists")
	})
}
