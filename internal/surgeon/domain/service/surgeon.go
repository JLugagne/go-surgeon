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
	AddInterface(ctx context.Context, req domain.InterfaceActionRequest) (string, []string, error)
	UpdateInterface(ctx context.Context, req domain.InterfaceActionRequest) (string, []string, error)
	DeleteInterface(ctx context.Context, req domain.InterfaceActionRequest) (string, []string, error)
	GenerateTest(ctx context.Context, filePath, identifier string) (string, error)
	TagStruct(ctx context.Context, req domain.TagRequest) error
	ExtractInterface(ctx context.Context, req domain.ExtractInterfaceRequest) (string, error)
	PatchFunction(ctx context.Context, req domain.PatchFunctionRequest) (domain.PatchFunctionResult, error)
	PatchFunctionBulk(ctx context.Context, req domain.PatchFunctionBulkRequest) (domain.PatchFunctionBulkResult, error)
	PatchStruct(ctx context.Context, req domain.PatchStructRequest) (domain.PatchStructResult, error)
	PatchStructBulk(ctx context.Context, req domain.PatchStructBulkRequest) (domain.PatchStructBulkResult, error)
	PatchInterface(ctx context.Context, req domain.PatchInterfaceRequest) (domain.PatchInterfaceResult, error)
	PatchFile(ctx context.Context, req domain.PatchFileRequest) (domain.PatchFileResult, error)
	PatchDecl(ctx context.Context, req domain.PatchDeclRequest) (domain.PatchDeclResult, error)
	Rename(ctx context.Context, req domain.RenameRequest) (domain.RenameResult, error)
}

// SurgeonQueries defines the interface for querying the codebase AST.
type SurgeonQueries interface {
	FindSymbols(ctx context.Context, query domain.SymbolQuery, targetDir string) ([]domain.SymbolResult, error)
	Graph(ctx context.Context, opts domain.GraphOptions) ([]domain.GraphPackage, error)
	BuildCheck(ctx context.Context, req domain.BuildCheckRequest) (domain.BuildCheckResult, error)
	TestRun(ctx context.Context, req domain.TestRunRequest) (domain.TestRunResult, error)
	FindReferences(ctx context.Context, query domain.ReferencesQuery) (domain.ReferencesResult, error)
	FindDefinition(ctx context.Context, query domain.ReferencesQuery) (domain.ReferencesResult, error)
}
