package service

import (
	"context"

	"github.com/JLugagne/go-surgeon/internal/surgeon/domain"
)

// ScaffolderCommands defines the interface for scaffolding applications and features.
type ScaffolderCommands interface {
	ListTemplates(ctx context.Context) ([]domain.Template, error)
	GetTemplate(ctx context.Context, templateName string) (domain.Template, error)
	Execute(ctx context.Context, templateName, commandName string, params map[string]string) error
}
