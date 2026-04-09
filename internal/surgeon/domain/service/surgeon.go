package service

import (
	"context"

	"github.com/JLugagne/go-surgeon/internal/surgeon/domain"
)

// SurgeonCommands defines the interface for executing surgery plans.
type SurgeonCommands interface {
	ExecutePlan(ctx context.Context, plan domain.Plan) (domain.PlanResult, error)
	Implement(ctx context.Context, req domain.ImplementRequest) ([]domain.SymbolResult, error)
	Mock(ctx context.Context, req domain.MockRequest) (string, error)
	AddInterface(ctx context.Context, req domain.InterfaceActionRequest) (string, error)
	UpdateInterface(ctx context.Context, req domain.InterfaceActionRequest) (string, error)
	DeleteInterface(ctx context.Context, req domain.InterfaceActionRequest) (string, error)
	GenerateTest(ctx context.Context, filePath, identifier string) (string, error)
	TagStruct(ctx context.Context, req domain.TagRequest) error
	ExtractInterface(ctx context.Context, req domain.ExtractInterfaceRequest) (string, error)
}

// SurgeonQueries defines the interface for querying the codebase AST.
type SurgeonQueries interface {
	FindSymbols(ctx context.Context, query domain.SymbolQuery, targetDir string) ([]domain.SymbolResult, error)
	Graph(ctx context.Context, opts domain.GraphOptions) ([]domain.GraphPackage, error)
}
