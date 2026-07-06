package commands_test

import (
	"bytes"
	"context"
	"testing"

	"github.com/JLugagne/go-surgeon/internal/surgeon/domain"
	"github.com/JLugagne/go-surgeon/internal/surgeon/inbound/cli/commands"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeQueries is a minimal service.SurgeonQueries stub for CLI command tests.
type fakeQueries struct {
	findSymbolsFn func(ctx context.Context, query domain.SymbolQuery, targetDir string) ([]domain.SymbolResult, error)
}

func (f *fakeQueries) FindSymbols(ctx context.Context, query domain.SymbolQuery, targetDir string) ([]domain.SymbolResult, error) {
	return f.findSymbolsFn(ctx, query, targetDir)
}
func (f *fakeQueries) Graph(ctx context.Context, opts domain.GraphOptions) ([]domain.GraphPackage, error) {
	return nil, nil
}
func (f *fakeQueries) BuildCheck(ctx context.Context, req domain.BuildCheckRequest) (domain.BuildCheckResult, error) {
	return domain.BuildCheckResult{}, nil
}
func (f *fakeQueries) TestRun(ctx context.Context, req domain.TestRunRequest) (domain.TestRunResult, error) {
	return domain.TestRunResult{}, nil
}
func (f *fakeQueries) FindReferences(ctx context.Context, query domain.ReferencesQuery) (domain.ReferencesResult, error) {
	return domain.ReferencesResult{}, nil
}
func (f *fakeQueries) FindDefinition(ctx context.Context, query domain.ReferencesQuery) (domain.ReferencesResult, error) {
	return domain.ReferencesResult{}, nil
}

// TestSymbolCommand_QueryError_Surfaced confirms bug #30: the CLI symbol
// command swallowed FindSymbols errors (results, _ :=) and reported the
// misleading "No matches found" success text instead of the underlying
// failure (e.g. an invalid --dir).
func TestSymbolCommand_QueryError_Surfaced(t *testing.T) {
	queries := &fakeQueries{findSymbolsFn: func(_ context.Context, _ domain.SymbolQuery, _ string) ([]domain.SymbolResult, error) {
		return nil, &domain.Error{Code: "INVALID_ARGUMENT", Message: "directory does not exist: internal/nope"}
	}}
	cmd := commands.NewSymbolCommand(queries)
	cmd.SetArgs([]string{"Foo", "--dir", "internal/nope"})
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)

	err := cmd.ExecuteContext(context.Background())
	require.Error(t, err, "a failing query must not masquerade as 'no matches': %s", out.String())
	assert.Contains(t, err.Error(), "directory does not exist")
}
