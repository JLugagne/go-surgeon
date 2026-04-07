package commands

import (
	"bytes"
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"text/template"

	"github.com/JLugagne/go-surgeon/internal/surgeon/domain"
	"github.com/JLugagne/go-surgeon/internal/surgeon/domain/repositories/filesystem"
	"gopkg.in/yaml.v3"
)

// ScaffolderHandler handles manifest-driven scaffolding.
type ScaffolderHandler struct {
	fs filesystem.FileSystem
}

// NewScaffolderHandler creates a new ScaffolderHandler.
func NewScaffolderHandler(fs filesystem.FileSystem) *ScaffolderHandler {
	return &ScaffolderHandler{fs: fs}
}

// GetManifest reads the .templates/manifest.yaml file.
func (h *ScaffolderHandler) GetManifest(ctx context.Context) (domain.Manifest, error) {
	var manifest domain.Manifest
	data, err := h.fs.ReadFile(ctx, ".templates/manifest.yaml")
	if err != nil {
		return manifest, fmt.Errorf("failed to read manifest.yaml: %w", err)
	}
	if err := yaml.Unmarshal(data, &manifest); err != nil {
		return manifest, fmt.Errorf("failed to parse manifest.yaml: %w", err)
	}
	return manifest, nil
}

// Scaffold executes a scaffolding command defined in the manifest.
func (h *ScaffolderHandler) Scaffold(ctx context.Context, commandName string, params map[string]string) error {
	manifest, err := h.GetManifest(ctx)
	if err != nil {
		return err
	}

	var targetCmd *domain.Command
	for _, cmd := range manifest.Commands {
		if cmd.Name == commandName {
			targetCmd = &cmd
			break
		}
	}

	if targetCmd == nil {
		return fmt.Errorf("command '%s' not found in manifest", commandName)
	}

	funcMap := template.FuncMap{
		"lower": strings.ToLower,
		"upper": strings.ToUpper,
		"title": strings.Title,
	}

	var createdFiles []string

	for _, fileTmpl := range targetCmd.Files {
		// 1. Resolve target path
		pathTmpl, err := template.New("path").Funcs(funcMap).Parse(fileTmpl.Path)
		if err != nil {
			return err
		}
		var pathBuf bytes.Buffer
		if err := pathTmpl.Execute(&pathBuf, params); err != nil {
			return err
		}
		targetPath := pathBuf.String()

		// 2. Create directories
		dir := filepath.Dir(targetPath)
		if err := h.fs.MkdirAll(ctx, dir); err != nil {
			return err
		}

		// 3. Write content
		content := []byte("")
		if fileTmpl.Template != "" {
			tmplData, err := h.fs.ReadFile(ctx, filepath.Join(".templates", fileTmpl.Template))
			if err != nil {
				return fmt.Errorf("failed to read template %s: %w", fileTmpl.Template, err)
			}
			contentTmpl, err := template.New("content").Funcs(funcMap).Parse(string(tmplData))
			if err != nil {
				return err
			}
			var contentBuf bytes.Buffer
			if err := contentTmpl.Execute(&contentBuf, params); err != nil {
				return err
			}
			content = contentBuf.Bytes()
		}

		if err := h.fs.WriteFile(ctx, targetPath, content); err != nil {
			return err
		}
		
		createdFiles = append(createdFiles, targetPath)
	}

	// 4. Run goimports on created .go files
	var goFiles []string
	for _, f := range createdFiles {
		if strings.HasSuffix(f, ".go") {
			goFiles = append(goFiles, f)
		}
	}
	
	if len(goFiles) > 0 {
		return h.fs.ExecuteGoImports(ctx, goFiles)
	}

	return nil
}
