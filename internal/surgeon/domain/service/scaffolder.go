package service

import (
	"context"

	"github.com/JLugagne/go-surgeon/internal/surgeon/domain"
)

// ScaffolderCommands defines the interface for scaffolding applications and features.
type ScaffolderCommands interface {
	Scaffold(ctx context.Context, commandName string, params map[string]string) error
	GetManifest(ctx context.Context) (domain.Manifest, error)
}
