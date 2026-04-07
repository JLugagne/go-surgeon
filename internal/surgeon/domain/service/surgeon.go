package service

import (
	"context"
	"github.com/JLugagne/go-surgeon/internal/surgeon/domain"
)

// SurgeonCommands defines the interface for executing surgery plans.
type SurgeonCommands interface {
	ExecutePlan(ctx context.Context, plan domain.Plan) (int, error)
	Implement(ctx context.Context, req domain.ImplementRequest) ([]domain.SymbolResult, error)
}

// SurgeonQueries defines the interface for querying the codebase AST.
type SurgeonQueries interface {
	FindSymbols(ctx context.Context, query domain.SymbolQuery, targetDir string) ([]domain.SymbolResult, error)
}
