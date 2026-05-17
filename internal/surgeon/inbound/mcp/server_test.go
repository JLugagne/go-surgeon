package mcp_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/JLugagne/go-surgeon/internal/surgeon/domain"
	surgeonmcp "github.com/JLugagne/go-surgeon/internal/surgeon/inbound/mcp"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- Mock service implementations ---

type mockCommands struct {
	executePlanFn       func(ctx context.Context, plan domain.Plan) (domain.PlanResult, error)
	implementFn         func(ctx context.Context, req domain.ImplementRequest) ([]domain.SymbolResult, error)
	mockFn              func(ctx context.Context, req domain.MockRequest) (string, error)
	addInterfaceFn      func(ctx context.Context, req domain.InterfaceActionRequest) (string, []string, error)
	updateInterfaceFn   func(ctx context.Context, req domain.InterfaceActionRequest) (string, []string, error)
	deleteInterfaceFn   func(ctx context.Context, req domain.InterfaceActionRequest) (string, []string, error)
	generateTestFn      func(ctx context.Context, filePath, identifier string) (string, error)
	tagStructFn         func(ctx context.Context, req domain.TagRequest) error
	extractInterfaceFn  func(ctx context.Context, req domain.ExtractInterfaceRequest) (string, error)
	patchFunctionFn     func(ctx context.Context, req domain.PatchFunctionRequest) (domain.PatchFunctionResult, error)
	patchFunctionBulkFn func(ctx context.Context, req domain.PatchFunctionBulkRequest) (domain.PatchFunctionBulkResult, error)
	patchStructFn       func(ctx context.Context, req domain.PatchStructRequest) (domain.PatchStructResult, error)
	patchStructBulkFn   func(ctx context.Context, req domain.PatchStructBulkRequest) (domain.PatchStructBulkResult, error)
	patchInterfaceFn    func(ctx context.Context, req domain.PatchInterfaceRequest) (domain.PatchInterfaceResult, error)
	patchFileFn         func(ctx context.Context, req domain.PatchFileRequest) (domain.PatchFileResult, error)
	patchDeclFn         func(ctx context.Context, req domain.PatchDeclRequest) (domain.PatchDeclResult, error)
	renameFn            func(ctx context.Context, req domain.RenameRequest) (domain.RenameResult, error)
}

func (m *mockCommands) ExecutePlan(ctx context.Context, plan domain.Plan) (domain.PlanResult, error) {
	if m.executePlanFn != nil {
		return m.executePlanFn(ctx, plan)
	}
	return domain.PlanResult{FilesModified: 1}, nil
}

func (m *mockCommands) Implement(ctx context.Context, req domain.ImplementRequest) ([]domain.SymbolResult, error) {
	if m.implementFn != nil {
		return m.implementFn(ctx, req)
	}
	return nil, nil
}

func (m *mockCommands) Mock(ctx context.Context, req domain.MockRequest) (string, error) {
	if m.mockFn != nil {
		return m.mockFn(ctx, req)
	}
	return "", nil
}

func (m *mockCommands) AddInterface(ctx context.Context, req domain.InterfaceActionRequest) (string, []string, error) {
	if m.addInterfaceFn != nil {
		return m.addInterfaceFn(ctx, req)
	}
	return "", nil, nil
}

func (m *mockCommands) UpdateInterface(ctx context.Context, req domain.InterfaceActionRequest) (string, []string, error) {
	if m.updateInterfaceFn != nil {
		return m.updateInterfaceFn(ctx, req)
	}
	return "", nil, nil
}

func (m *mockCommands) DeleteInterface(ctx context.Context, req domain.InterfaceActionRequest) (string, []string, error) {
	if m.deleteInterfaceFn != nil {
		return m.deleteInterfaceFn(ctx, req)
	}
	return "", nil, nil
}

func (m *mockCommands) GenerateTest(ctx context.Context, filePath, identifier string) (string, error) {
	if m.generateTestFn != nil {
		return m.generateTestFn(ctx, filePath, identifier)
	}
	return "", nil
}

func (m *mockCommands) TagStruct(ctx context.Context, req domain.TagRequest) error {
	if m.tagStructFn != nil {
		return m.tagStructFn(ctx, req)
	}
	return nil
}

func (m *mockCommands) ExtractInterface(ctx context.Context, req domain.ExtractInterfaceRequest) (string, error) {
	if m.extractInterfaceFn != nil {
		return m.extractInterfaceFn(ctx, req)
	}
	return "", nil
}

type mockQueries struct {
	findSymbolsFn    func(ctx context.Context, query domain.SymbolQuery, targetDir string) ([]domain.SymbolResult, error)
	graphFn          func(ctx context.Context, opts domain.GraphOptions) ([]domain.GraphPackage, error)
	buildCheckFn     func(ctx context.Context, req domain.BuildCheckRequest) (domain.BuildCheckResult, error)
	testRunFn        func(ctx context.Context, req domain.TestRunRequest) (domain.TestRunResult, error)
	findReferencesFn func(ctx context.Context, query domain.ReferencesQuery) (domain.ReferencesResult, error)
	findDefinitionFn func(ctx context.Context, query domain.ReferencesQuery) (domain.ReferencesResult, error)
}

func (m *mockQueries) FindSymbols(ctx context.Context, query domain.SymbolQuery, targetDir string) ([]domain.SymbolResult, error) {
	if m.findSymbolsFn != nil {
		return m.findSymbolsFn(ctx, query, targetDir)
	}
	return nil, nil
}

func (m *mockQueries) Graph(ctx context.Context, opts domain.GraphOptions) ([]domain.GraphPackage, error) {
	if m.graphFn != nil {
		return m.graphFn(ctx, opts)
	}
	return nil, nil
}

func (m *mockQueries) BuildCheck(ctx context.Context, req domain.BuildCheckRequest) (domain.BuildCheckResult, error) {
	if m.buildCheckFn != nil {
		return m.buildCheckFn(ctx, req)
	}
	return domain.BuildCheckResult{Success: true}, nil
}

func (m *mockQueries) TestRun(ctx context.Context, req domain.TestRunRequest) (domain.TestRunResult, error) {
	if m.testRunFn != nil {
		return m.testRunFn(ctx, req)
	}
	return domain.TestRunResult{}, nil
}

// --- Test helpers ---

func setupTest(t *testing.T, commands *mockCommands, queries *mockQueries) *mcp.ClientSession {
	t.Helper()
	ctx := context.Background()

	server := surgeonmcp.NewServer(commands, queries)

	clientTransport, serverTransport := mcp.NewInMemoryTransports()

	ss, err := server.Connect(ctx, serverTransport, nil)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, ss.Close()) })

	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "1.0.0"}, nil)
	cs, err := client.Connect(ctx, clientTransport, nil)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, cs.Close()) })

	return cs
}

func callTool(t *testing.T, cs *mcp.ClientSession, name string, args map[string]any) *mcp.CallToolResult {
	t.Helper()
	result, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      name,
		Arguments: args,
	})
	require.NoError(t, err)
	return result
}

func resultText(t *testing.T, result *mcp.CallToolResult) string {
	t.Helper()
	require.NotEmpty(t, result.Content, "expected at least one content item")
	tc, ok := result.Content[0].(*mcp.TextContent)
	require.True(t, ok, "expected TextContent, got %T", result.Content[0])
	return tc.Text
}

// --- Tool list test ---

func TestToolsList(t *testing.T) {
	cs := setupTest(t, &mockCommands{}, &mockQueries{})

	result, err := cs.ListTools(context.Background(), nil)
	require.NoError(t, err)

	names := make(map[string]bool)
	for _, tool := range result.Tools {
		names[tool.Name] = true
	}

	expected := []string{
		"overview", "symbol", "build_check", "test_run",
		"create", "update", "delete",
		"interface",
		"insert_call",
		"execute_plan", "scaffold", "test",
		"patch",
		"find_definition", "find_references", "rename_symbol",
		"batch_query",
	}
	for _, name := range expected {
		assert.True(t, names[name], "missing tool: %s", name)
	}
	assert.Equal(t, len(expected), len(result.Tools), "unexpected number of tools")
	// Verify every tool ships a non-nil InputSchema in ListTools so clients
	// never need a second round-trip to fetch the schema.
	for _, tool := range result.Tools {
		assert.NotNil(t, tool.InputSchema, "tool %q has nil InputSchema in ListTools response", tool.Name)
	}
}

// --- Graph tool tests ---

func TestOverview_ListsPackages(t *testing.T) {
	queries := &mockQueries{
		graphFn: func(_ context.Context, opts domain.GraphOptions) ([]domain.GraphPackage, error) {
			assert.Equal(t, ".", opts.Dir)
			return []domain.GraphPackage{
				{Path: "cmd/app"},
				{Path: "internal/domain"},
			}, nil
		},
	}
	cs := setupTest(t, &mockCommands{}, queries)

	result := callTool(t, cs, "overview", map[string]any{})
	text := resultText(t, result)
	assert.Contains(t, text, "cmd/app")
	assert.Contains(t, text, "internal/domain")
	assert.False(t, result.IsError)
}

func TestOverview_Error(t *testing.T) {
	queries := &mockQueries{
		graphFn: func(_ context.Context, _ domain.GraphOptions) ([]domain.GraphPackage, error) {
			return nil, errors.New("walk failed")
		},
	}
	cs := setupTest(t, &mockCommands{}, queries)

	result := callTool(t, cs, "overview", map[string]any{})
	assert.True(t, result.IsError)
	assert.Contains(t, resultText(t, result), "walk failed")
}

func TestOverview_FocusImpliesFlags(t *testing.T) {
	queries := &mockQueries{
		graphFn: func(_ context.Context, opts domain.GraphOptions) ([]domain.GraphPackage, error) {
			assert.True(t, opts.Symbols, "focus should imply symbols")
			assert.True(t, opts.Summary, "focus should imply summary")
			assert.True(t, opts.Recursive, "focus should imply recursive")
			assert.Equal(t, "internal/domain", opts.Focus)
			return nil, nil
		},
	}
	cs := setupTest(t, &mockCommands{}, queries)

	callTool(t, cs, "overview", map[string]any{"focus": "internal/domain"})
}

// --- Symbol tool tests ---

func TestSymbol_FindsFunction(t *testing.T) {
	queries := &mockQueries{
		findSymbolsFn: func(_ context.Context, query domain.SymbolQuery, dir string) ([]domain.SymbolResult, error) {
			if query.Name == "NewBook" {
				return []domain.SymbolResult{{
					Name:      "NewBook",
					File:      "internal/domain/book.go",
					LineStart: 10,
					LineEnd:   15,
					Signature: "func NewBook(title string) *Book",
				}}, nil
			}
			return nil, nil
		},
	}
	cs := setupTest(t, &mockCommands{}, queries)

	result := callTool(t, cs, "symbol", map[string]any{"query": "NewBook"})
	text := resultText(t, result)
	assert.Contains(t, text, "Symbol: NewBook")
	assert.Contains(t, text, "book.go:10-15")
	assert.False(t, result.IsError)
}

func TestSymbol_ShowsBody(t *testing.T) {
	queries := &mockQueries{
		findSymbolsFn: func(_ context.Context, query domain.SymbolQuery, _ string) ([]domain.SymbolResult, error) {
			if query.Name == "NewBook" {
				return []domain.SymbolResult{{
					Name:      "NewBook",
					File:      "internal/domain/book.go",
					LineStart: 10,
					LineEnd:   12,
					Signature: "func NewBook(title string) *Book",
					Code:      "10: func NewBook(title string) *Book {\n11:     return &Book{Title: title}\n12: }",
				}}, nil
			}
			return nil, nil
		},
	}
	cs := setupTest(t, &mockCommands{}, queries)

	result := callTool(t, cs, "symbol", map[string]any{"query": "NewBook", "body": true})
	text := resultText(t, result)
	assert.Contains(t, text, "Code (Empty lines stripped; line numbers are file-absolute):")
	assert.Contains(t, text, "return &Book{Title: title}")
}

func TestSymbol_NotFound(t *testing.T) {
	cs := setupTest(t, &mockCommands{}, &mockQueries{})

	result := callTool(t, cs, "symbol", map[string]any{"query": "NonExistent"})
	text := resultText(t, result)
	assert.Contains(t, text, "No matches found")
	assert.False(t, result.IsError)
}

func TestSymbol_MultipleMatches(t *testing.T) {
	queries := &mockQueries{
		findSymbolsFn: func(_ context.Context, query domain.SymbolQuery, _ string) ([]domain.SymbolResult, error) {
			if query.Name == "Validate" {
				return []domain.SymbolResult{
					{Name: "Validate", Receiver: "Book", File: "book.go", LineStart: 10, LineEnd: 15},
					{Name: "Validate", Receiver: "Author", File: "author.go", LineStart: 20, LineEnd: 25},
				}, nil
			}
			return nil, nil
		},
	}
	cs := setupTest(t, &mockCommands{}, queries)

	result := callTool(t, cs, "symbol", map[string]any{"query": "Validate"})
	text := resultText(t, result)
	assert.Contains(t, text, "Found 2 matches")
	assert.Contains(t, text, "Matches (Methods):")
}

// --- build_check tool tests ---

func TestBuildCheck_SuccessRendersSummary(t *testing.T) {
	var received domain.BuildCheckRequest
	queries := &mockQueries{
		buildCheckFn: func(_ context.Context, req domain.BuildCheckRequest) (domain.BuildCheckResult, error) {
			received = req
			return domain.BuildCheckResult{Success: true, DurationMs: 12}, nil
		},
	}
	cs := setupTest(t, &mockCommands{}, queries)

	result := callTool(t, cs, "build_check", map[string]any{"dir": "internal/foo"})
	text := resultText(t, result)
	assert.Contains(t, text, "SUCCESS (build_check)")
	assert.False(t, result.IsError)
	assert.Equal(t, "internal/foo", received.Dir)
}

func TestBuildCheck_FailureListsDiagnostics(t *testing.T) {
	queries := &mockQueries{
		buildCheckFn: func(_ context.Context, _ domain.BuildCheckRequest) (domain.BuildCheckResult, error) {
			return domain.BuildCheckResult{
				Success:  false,
				ExitCode: 1,
				Diagnostics: []domain.BuildDiagnostic{
					{File: "pkg/a.go", Line: 10, Column: 5, Message: "undefined: Foo"},
					{File: "pkg/a.go", Line: 12, Column: 3, Message: "expected ';'"},
				},
				DurationMs: 8,
			}, nil
		},
	}
	cs := setupTest(t, &mockCommands{}, queries)

	result := callTool(t, cs, "build_check", map[string]any{})
	text := resultText(t, result)
	assert.Contains(t, text, "FAILED (build_check)")
	assert.Contains(t, text, "pkg/a.go")
	assert.Contains(t, text, "10:5")
	assert.Contains(t, text, "undefined: Foo")
}

func TestBuildCheck_TimedOutRendersTimeoutHeader(t *testing.T) {
	queries := &mockQueries{
		buildCheckFn: func(_ context.Context, _ domain.BuildCheckRequest) (domain.BuildCheckResult, error) {
			return domain.BuildCheckResult{TimedOut: true, ExitCode: -1, DurationMs: 60000}, nil
		},
	}
	cs := setupTest(t, &mockCommands{}, queries)

	result := callTool(t, cs, "build_check", map[string]any{"timeout_seconds": 60})
	text := resultText(t, result)
	assert.Contains(t, text, "TIMED OUT")
}

// --- Create tool tests ---

func TestCreate_Func(t *testing.T) {
	var receivedPlan domain.Plan
	commands := &mockCommands{
		executePlanFn: func(_ context.Context, plan domain.Plan) (domain.PlanResult, error) {
			receivedPlan = plan
			return domain.PlanResult{FilesModified: 1}, nil
		},
	}
	cs := setupTest(t, commands, &mockQueries{})

	result := callTool(t, cs, "create", map[string]any{
		"object":  "func",
		"file":    "internal/domain/book.go",
		"content": "func NewBook(title string) *Book { return &Book{Title: title} }",
	})

	text := resultText(t, result)
	assert.Contains(t, text, "SUCCESS (create func)")
	assert.False(t, result.IsError)

	require.Len(t, receivedPlan.Actions, 1)
	assert.Equal(t, domain.ActionTypeAddFunc, receivedPlan.Actions[0].Action)
	assert.Equal(t, "internal/domain/book.go", receivedPlan.Actions[0].FilePath)
}

func TestCreate_File(t *testing.T) {
	var receivedPlan domain.Plan
	commands := &mockCommands{
		executePlanFn: func(_ context.Context, plan domain.Plan) (domain.PlanResult, error) {
			receivedPlan = plan
			return domain.PlanResult{FilesModified: 1}, nil
		},
	}
	cs := setupTest(t, commands, &mockQueries{})

	callTool(t, cs, "create", map[string]any{
		"object":  "file",
		"file":    "internal/domain/book.go",
		"content": "type Book struct { Title string }",
	})

	require.Len(t, receivedPlan.Actions, 1)
	assert.Equal(t, domain.ActionTypeCreateFile, receivedPlan.Actions[0].Action)
}

func TestCreate_Struct(t *testing.T) {
	var receivedPlan domain.Plan
	commands := &mockCommands{
		executePlanFn: func(_ context.Context, plan domain.Plan) (domain.PlanResult, error) {
			receivedPlan = plan
			return domain.PlanResult{FilesModified: 1}, nil
		},
	}
	cs := setupTest(t, commands, &mockQueries{})

	callTool(t, cs, "create", map[string]any{
		"object":  "struct",
		"file":    "internal/domain/book.go",
		"content": "type Book struct { Title string }",
	})

	require.Len(t, receivedPlan.Actions, 1)
	assert.Equal(t, domain.ActionTypeAddStruct, receivedPlan.Actions[0].Action)
}

func TestCreate_InvalidObject(t *testing.T) {
	cs := setupTest(t, &mockCommands{}, &mockQueries{})

	result := callTool(t, cs, "create", map[string]any{
		"object":  "method",
		"file":    "book.go",
		"content": "whatever",
	})
	assert.True(t, result.IsError)
	assert.Contains(t, resultText(t, result), "invalid object")
}

func TestCreate_Error(t *testing.T) {
	commands := &mockCommands{
		executePlanFn: func(_ context.Context, _ domain.Plan) (domain.PlanResult, error) {
			return domain.PlanResult{}, errors.New("file exists")
		},
	}
	cs := setupTest(t, commands, &mockQueries{})

	result := callTool(t, cs, "create", map[string]any{
		"object":  "func",
		"file":    "book.go",
		"content": "func Foo() {}",
	})
	assert.True(t, result.IsError)
	assert.Contains(t, resultText(t, result), "file exists")
}

// --- Update tool tests ---

func TestUpdate_Func(t *testing.T) {
	var receivedPlan domain.Plan
	commands := &mockCommands{
		executePlanFn: func(_ context.Context, plan domain.Plan) (domain.PlanResult, error) {
			receivedPlan = plan
			return domain.PlanResult{FilesModified: 1}, nil
		},
	}
	cs := setupTest(t, commands, &mockQueries{})

	result := callTool(t, cs, "update", map[string]any{
		"object":     "func",
		"file":       "internal/domain/book.go",
		"identifier": "NewBook",
		"content":    "func NewBook(title, author string) *Book { return &Book{Title: title, Author: author} }",
	})

	text := resultText(t, result)
	assert.Contains(t, text, "SUCCESS (update func)")

	require.Len(t, receivedPlan.Actions, 1)
	assert.Equal(t, domain.ActionTypeUpdateFunc, receivedPlan.Actions[0].Action)
	assert.Equal(t, "NewBook", receivedPlan.Actions[0].Identifier)
}

func TestUpdate_File(t *testing.T) {
	var receivedPlan domain.Plan
	commands := &mockCommands{
		executePlanFn: func(_ context.Context, plan domain.Plan) (domain.PlanResult, error) {
			receivedPlan = plan
			return domain.PlanResult{FilesModified: 1}, nil
		},
	}
	cs := setupTest(t, commands, &mockQueries{})

	callTool(t, cs, "update", map[string]any{
		"object":  "file",
		"file":    "internal/domain/book.go",
		"content": "type Book struct { Title string; Author string }",
	})

	require.Len(t, receivedPlan.Actions, 1)
	assert.Equal(t, domain.ActionTypeReplaceFile, receivedPlan.Actions[0].Action)
}

func TestUpdate_InvalidObject(t *testing.T) {
	cs := setupTest(t, &mockCommands{}, &mockQueries{})

	result := callTool(t, cs, "update", map[string]any{
		"object":  "interface",
		"file":    "book.go",
		"content": "whatever",
	})
	assert.True(t, result.IsError)
	assert.Contains(t, resultText(t, result), "invalid object")
}

// --- Delete tool tests ---

func TestDelete_Func(t *testing.T) {
	var receivedPlan domain.Plan
	commands := &mockCommands{
		executePlanFn: func(_ context.Context, plan domain.Plan) (domain.PlanResult, error) {
			receivedPlan = plan
			return domain.PlanResult{FilesModified: 1}, nil
		},
	}
	cs := setupTest(t, commands, &mockQueries{})

	result := callTool(t, cs, "delete", map[string]any{
		"object":     "func",
		"file":       "internal/domain/book.go",
		"identifier": "NewBook",
	})

	text := resultText(t, result)
	assert.Contains(t, text, "SUCCESS (delete func)")

	require.Len(t, receivedPlan.Actions, 1)
	assert.Equal(t, domain.ActionTypeDeleteFunc, receivedPlan.Actions[0].Action)
	assert.Equal(t, "NewBook", receivedPlan.Actions[0].Identifier)
}

func TestDelete_Struct(t *testing.T) {
	var receivedPlan domain.Plan
	commands := &mockCommands{
		executePlanFn: func(_ context.Context, plan domain.Plan) (domain.PlanResult, error) {
			receivedPlan = plan
			return domain.PlanResult{FilesModified: 1}, nil
		},
	}
	cs := setupTest(t, commands, &mockQueries{})

	callTool(t, cs, "delete", map[string]any{
		"object":     "struct",
		"file":       "internal/domain/book.go",
		"identifier": "Book",
	})

	require.Len(t, receivedPlan.Actions, 1)
	assert.Equal(t, domain.ActionTypeDeleteStruct, receivedPlan.Actions[0].Action)
}

func TestDelete_InvalidObject(t *testing.T) {
	cs := setupTest(t, &mockCommands{}, &mockQueries{})

	result := callTool(t, cs, "delete", map[string]any{
		"object":     "interface",
		"file":       "book.go",
		"identifier": "whatever",
	})
	assert.True(t, result.IsError)
	assert.Contains(t, resultText(t, result), "invalid object")
}

// --- Execute plan tool tests ---

func TestExecutePlan_Success(t *testing.T) {
	var receivedPlan domain.Plan
	commands := &mockCommands{
		executePlanFn: func(_ context.Context, plan domain.Plan) (domain.PlanResult, error) {
			receivedPlan = plan
			return domain.PlanResult{FilesModified: 2}, nil
		},
	}
	cs := setupTest(t, commands, &mockQueries{})

	result := callTool(t, cs, "execute_plan", map[string]any{
		"actions": []map[string]any{
			{"action": "add_func", "file": "book.go", "content": "func NewBook() *Book { return &Book{} }\n"},
			{"action": "add_struct", "file": "book.go", "content": "type Book struct { Title string }\n"},
		},
	})
	text := resultText(t, result)
	assert.Contains(t, text, "SUCCESS: 2 files modified")
	assert.False(t, result.IsError)
	assert.Len(t, receivedPlan.Actions, 2)
}

func TestExecutePlan_InvalidFile(t *testing.T) {
	cs := setupTest(t, &mockCommands{}, &mockQueries{})

	result := callTool(t, cs, "execute_plan", map[string]any{
		"actions": []map[string]any{
			{"action": "add_func", "file": "book.txt", "content": "func Foo() {}"},
		},
	})
	assert.True(t, result.IsError)
	assert.Contains(t, resultText(t, result), "not a Go file")
}

func TestExecutePlan_ExecutionError(t *testing.T) {
	commands := &mockCommands{
		executePlanFn: func(_ context.Context, _ domain.Plan) (domain.PlanResult, error) {
			return domain.PlanResult{}, errors.New("node not found")
		},
	}
	cs := setupTest(t, commands, &mockQueries{})

	result := callTool(t, cs, "execute_plan", map[string]any{
		"actions": []map[string]any{
			{"action": "update_func", "file": "book.go", "identifier": "Missing", "content": "func Missing() {}"},
		},
	})
	assert.True(t, result.IsError)
	assert.Contains(t, resultText(t, result), "node not found")
}

func TestExecutePlan_Warnings(t *testing.T) {
	commands := &mockCommands{
		executePlanFn: func(_ context.Context, _ domain.Plan) (domain.PlanResult, error) {
			return domain.PlanResult{FilesModified: 1, Warnings: []string{"identifier not found in file, appended"}}, nil
		},
	}
	cs := setupTest(t, commands, &mockQueries{})

	result := callTool(t, cs, "execute_plan", map[string]any{
		"actions": []map[string]any{
			{"action": "update_func", "file": "book.go", "identifier": "Foo", "content": "func Foo() {}"},
		},
	})
	text := resultText(t, result)
	assert.Contains(t, text, "WARNING:")
	assert.Contains(t, text, "SUCCESS:")
	assert.False(t, result.IsError)
}

// --- Interface tool tests ---

func TestAddInterface(t *testing.T) {
	var receivedReq domain.InterfaceActionRequest
	commands := &mockCommands{
		addInterfaceFn: func(_ context.Context, req domain.InterfaceActionRequest) (string, []string, error) {
			receivedReq = req
			return "SUCCESS: Created interface Repository in repo.go", nil, nil
		},
	}
	cs := setupTest(t, commands, &mockQueries{})

	result := callTool(t, cs, "interface", map[string]any{
		"action":    "add",
		"file":      "internal/domain/repo.go",
		"content":   "type Repository interface { FindByID(id string) (*Entity, error) }",
		"mock_file": "internal/domain/repotest/mock.go",
		"mock_name": "MockRepository",
	})

	text := resultText(t, result)
	assert.Contains(t, text, "SUCCESS")
	assert.Equal(t, "internal/domain/repo.go", receivedReq.FilePath)
	assert.Equal(t, "internal/domain/repotest/mock.go", receivedReq.MockFile)
	assert.Equal(t, "MockRepository", receivedReq.MockName)
}

func TestUpdateInterface(t *testing.T) {
	commands := &mockCommands{
		updateInterfaceFn: func(_ context.Context, req domain.InterfaceActionRequest) (string, []string, error) {
			assert.Equal(t, "Repository", req.Identifier)
			return "SUCCESS: Updated interface Repository", nil, nil
		},
	}
	cs := setupTest(t, commands, &mockQueries{})

	result := callTool(t, cs, "interface", map[string]any{
		"action":     "update",
		"file":       "repo.go",
		"identifier": "Repository",
		"content":    "type Repository interface { FindByID(id string) (*Entity, error); Delete(id string) error }",
	})
	assert.Contains(t, resultText(t, result), "SUCCESS")
}

func TestDeleteInterface(t *testing.T) {
	commands := &mockCommands{
		deleteInterfaceFn: func(_ context.Context, req domain.InterfaceActionRequest) (string, []string, error) {
			assert.Equal(t, "Repository", req.Identifier)
			return "SUCCESS: Deleted interface Repository", nil, nil
		},
	}
	cs := setupTest(t, commands, &mockQueries{})

	result := callTool(t, cs, "interface", map[string]any{
		"action":     "delete",
		"file":       "repo.go",
		"identifier": "Repository",
	})
	assert.Contains(t, resultText(t, result), "SUCCESS")
}

func TestAddInterface_Error(t *testing.T) {
	commands := &mockCommands{
		addInterfaceFn: func(_ context.Context, _ domain.InterfaceActionRequest) (string, []string, error) {
			return "", nil, errors.New("duplicate interface")
		},
	}
	cs := setupTest(t, commands, &mockQueries{})

	result := callTool(t, cs, "interface", map[string]any{
		"action":  "add",
		"file":    "repo.go",
		"content": "type Repository interface {}",
	})
	assert.True(t, result.IsError)
	assert.Contains(t, resultText(t, result), "duplicate interface")
}

// --- Interface action discriminator validation ---

func TestInterface_MissingAction(t *testing.T) {
	// The schema marks action as required; sending it as an empty string
	// bypasses the SDK pre-validation and exercises our handler-level guard.
	cs := setupTest(t, &mockCommands{}, &mockQueries{})
	result := callTool(t, cs, "interface", map[string]any{
		"action":  "",
		"file":    "repo.go",
		"content": "type Repository interface {}",
	})
	assert.True(t, result.IsError)
	assert.Contains(t, resultText(t, result), "action is required")
}

func TestInterface_UnknownAction(t *testing.T) {
	cs := setupTest(t, &mockCommands{}, &mockQueries{})
	result := callTool(t, cs, "interface", map[string]any{
		"action": "rename",
		"file":   "repo.go",
	})
	assert.True(t, result.IsError)
	assert.Contains(t, resultText(t, result), "unknown action")
}

func TestInterface_DeleteMockRequiresMockFileAndName(t *testing.T) {
	cs := setupTest(t, &mockCommands{}, &mockQueries{})
	result := callTool(t, cs, "interface", map[string]any{
		"action":      "delete",
		"file":        "repo.go",
		"identifier":  "Repository",
		"delete_mock": true,
	})
	assert.True(t, result.IsError)
	assert.Contains(t, resultText(t, result), "delete_mock=true requires both mock_file and mock_name")
}

func TestInterface_DeleteMockRejectedOnAdd(t *testing.T) {
	cs := setupTest(t, &mockCommands{}, &mockQueries{})
	result := callTool(t, cs, "interface", map[string]any{
		"action":      "add",
		"file":        "repo.go",
		"content":     "type Repository interface {}",
		"delete_mock": true,
		"mock_file":   "mock.go",
		"mock_name":   "MockRepository",
	})
	assert.True(t, result.IsError)
	assert.Contains(t, resultText(t, result), "delete_mock is only valid with action=delete")
}

func TestInterface_StripDocRejectedOnDelete(t *testing.T) {
	cs := setupTest(t, &mockCommands{}, &mockQueries{})
	result := callTool(t, cs, "interface", map[string]any{
		"action":     "delete",
		"file":       "repo.go",
		"identifier": "Repository",
		"strip_doc":  true,
	})
	assert.True(t, result.IsError)
	assert.Contains(t, resultText(t, result), "strip_doc is only valid with action=update")
}

func TestInterface_AddRequiresContent(t *testing.T) {
	cs := setupTest(t, &mockCommands{}, &mockQueries{})
	result := callTool(t, cs, "interface", map[string]any{
		"action": "add",
		"file":   "repo.go",
	})
	assert.True(t, result.IsError)
	assert.Contains(t, resultText(t, result), "content is required for action=add")
}

func TestInterface_UpdateRequiresSomethingToUpdate(t *testing.T) {
	cs := setupTest(t, &mockCommands{}, &mockQueries{})
	result := callTool(t, cs, "interface", map[string]any{
		"action":     "update",
		"file":       "repo.go",
		"identifier": "Repository",
	})
	assert.True(t, result.IsError)
	assert.Contains(t, resultText(t, result), "requires at least one of content, doc, or strip_doc")
}

// --- Implement tool tests ---

func TestImplement_GeneratesStubs(t *testing.T) {
	commands := &mockCommands{
		implementFn: func(_ context.Context, req domain.ImplementRequest) ([]domain.SymbolResult, error) {
			assert.Equal(t, "io.Reader", req.Interface)
			assert.Equal(t, "*MyReader", req.Receiver)
			return []domain.SymbolResult{{
				Name:      "Read",
				Receiver:  "*MyReader",
				File:      "reader.go",
				LineStart: 10,
				LineEnd:   14,
				Code:      "func (r *MyReader) Read(p []byte) (int, error) {\n\tpanic(\"not implemented\")\n}",
			}}, nil
		},
	}
	cs := setupTest(t, commands, &mockQueries{})

	result := callTool(t, cs, "scaffold", map[string]any{
		"kind":   "impl_from_interface",
		"source": "io.Reader",
		"target": "*MyReader",
		"file":   "reader.go",
	})

	text := resultText(t, result)
	assert.Contains(t, text, "Generated 1 missing methods")
	assert.Contains(t, text, "Symbol: Read")
}

func TestImplement_AllImplemented(t *testing.T) {
	commands := &mockCommands{
		implementFn: func(_ context.Context, _ domain.ImplementRequest) ([]domain.SymbolResult, error) {
			return nil, nil
		},
	}
	cs := setupTest(t, commands, &mockQueries{})

	result := callTool(t, cs, "scaffold", map[string]any{
		"kind":   "impl_from_interface",
		"source": "io.Reader",
		"target": "*MyReader",
		"file":   "reader.go",
	})
	assert.Contains(t, resultText(t, result), "All methods are already implemented")
}

func TestImplement_Error(t *testing.T) {
	commands := &mockCommands{
		implementFn: func(_ context.Context, _ domain.ImplementRequest) ([]domain.SymbolResult, error) {
			return nil, errors.New("interface not found")
		},
	}
	cs := setupTest(t, commands, &mockQueries{})

	result := callTool(t, cs, "scaffold", map[string]any{
		"kind":   "impl_from_interface",
		"source": "pkg.Missing",
		"target": "*MyStruct",
		"file":   "file.go",
	})
	assert.True(t, result.IsError)
	assert.Contains(t, resultText(t, result), "interface not found")
}

// --- Mock tool tests ---

func TestMock_Success(t *testing.T) {
	commands := &mockCommands{
		mockFn: func(_ context.Context, req domain.MockRequest) (string, error) {
			assert.Equal(t, "io.Writer", req.Interface)
			assert.Equal(t, "MockWriter", req.Receiver)
			assert.Equal(t, "mocks/writer.go", req.FilePath)
			return "Generated mock MockWriter in mocks/writer.go", nil
		},
	}
	cs := setupTest(t, commands, &mockQueries{})

	result := callTool(t, cs, "scaffold", map[string]any{
		"kind":   "mock_from_interface",
		"source": "io.Writer",
		"target": "MockWriter",
		"file":   "mocks/writer.go",
	})
	assert.Contains(t, resultText(t, result), "MockWriter")
}

func TestMock_Error(t *testing.T) {
	commands := &mockCommands{
		mockFn: func(_ context.Context, _ domain.MockRequest) (string, error) {
			return "", errors.New("cannot resolve interface")
		},
	}
	cs := setupTest(t, commands, &mockQueries{})

	result := callTool(t, cs, "scaffold", map[string]any{
		"kind":   "mock_from_interface",
		"source": "pkg.Missing",
		"target": "MockMissing",
		"file":   "mock.go",
	})
	assert.True(t, result.IsError)
	assert.Contains(t, resultText(t, result), "cannot resolve interface")
}

// --- Test tool tests ---

func TestTestGen_Success(t *testing.T) {
	commands := &mockCommands{
		generateTestFn: func(_ context.Context, filePath, identifier string) (string, error) {
			assert.Equal(t, "book.go", filePath)
			assert.Equal(t, "NewBook", identifier)
			return "book_test.go", nil
		},
	}
	cs := setupTest(t, commands, &mockQueries{})

	result := callTool(t, cs, "test", map[string]any{
		"file":       "book.go",
		"identifier": "NewBook",
	})
	text := resultText(t, result)
	assert.Contains(t, text, "SUCCESS")
	assert.Contains(t, text, "book_test.go")
}

func TestTestGen_Error(t *testing.T) {
	commands := &mockCommands{
		generateTestFn: func(_ context.Context, _, _ string) (string, error) {
			return "", errors.New("function not found")
		},
	}
	cs := setupTest(t, commands, &mockQueries{})

	result := callTool(t, cs, "test", map[string]any{
		"file":       "book.go",
		"identifier": "Missing",
	})
	assert.True(t, result.IsError)
	assert.Contains(t, resultText(t, result), "function not found")
}

// --- patch target=struct op=auto_tag tests (migrated from the deleted `tag` tool) ---

func TestPatchStruct_AutoTagJson(t *testing.T) {
	commands := &mockCommands{
		tagStructFn: func(_ context.Context, req domain.TagRequest) error {
			assert.Equal(t, "book.go", req.FilePath)
			assert.Equal(t, "Book", req.StructName)
			assert.Equal(t, "json", req.AutoFormat)
			return nil
		},
	}
	cs := setupTest(t, commands, &mockQueries{})

	result := callTool(t, cs, "patch", map[string]any{
		"target": "struct",
		"items": []map[string]any{
			{
				"file":       "book.go",
				"identifier": "Book",
				"patches": []map[string]any{
					{"op": "auto_tag", "format": "json"},
				},
			},
		},
	})
	assert.False(t, result.IsError)
	text := resultText(t, result)
	assert.Contains(t, text, "OK")
}

func TestPatchStruct_SetTag_SingleField(t *testing.T) {
	commands := &mockCommands{
		patchStructFn: func(_ context.Context, req domain.PatchStructRequest) (domain.PatchStructResult, error) {
			require.Len(t, req.Patches, 1)
			assert.Equal(t, domain.StructPatchOp("set_tag"), req.Patches[0].Op)
			assert.Equal(t, "Title", req.Patches[0].Name)
			assert.Equal(t, `json:"book_title"`, req.Patches[0].Tag)
			return domain.PatchStructResult{Applied: 1}, nil
		},
	}
	cs := setupTest(t, commands, &mockQueries{})

	result := callTool(t, cs, "patch", map[string]any{
		"target": "struct",
		"items": []map[string]any{
			{
				"file":       "book.go",
				"identifier": "Book",
				"patches": []map[string]any{
					{"op": "set_tag", "name": "Title", "tag": `json:"book_title"`},
				},
			},
		},
	})
	assert.False(t, result.IsError)
}

func TestPatchStruct_AutoTag_Error(t *testing.T) {
	commands := &mockCommands{
		tagStructFn: func(_ context.Context, _ domain.TagRequest) error {
			return errors.New("struct not found")
		},
	}
	cs := setupTest(t, commands, &mockQueries{})

	result := callTool(t, cs, "patch", map[string]any{
		"target": "struct",
		"items": []map[string]any{
			{
				"file":       "book.go",
				"identifier": "Missing",
				"patches": []map[string]any{
					{"op": "auto_tag", "format": "json"},
				},
			},
		},
	})
	assert.True(t, result.IsError)
	assert.Contains(t, resultText(t, result), "struct not found")
}

func TestPatchStruct_AutoTag_RequiresFormat(t *testing.T) {
	cs := setupTest(t, &mockCommands{}, &mockQueries{})

	result := callTool(t, cs, "patch", map[string]any{
		"target": "struct",
		"items": []map[string]any{
			{
				"file":       "book.go",
				"identifier": "Book",
				"patches": []map[string]any{
					{"op": "auto_tag"},
				},
			},
		},
	})
	assert.True(t, result.IsError)
	assert.Contains(t, resultText(t, result), "format")
}

func TestPatchStruct_AutoTag_RejectsMixedOps(t *testing.T) {
	cs := setupTest(t, &mockCommands{}, &mockQueries{})

	result := callTool(t, cs, "patch", map[string]any{
		"target": "struct",
		"items": []map[string]any{
			{
				"file":       "book.go",
				"identifier": "Book",
				"patches": []map[string]any{
					{"op": "auto_tag", "format": "json"},
					{"op": "remove_field", "name": "Foo"},
				},
			},
		},
	})
	assert.True(t, result.IsError)
	assert.Contains(t, resultText(t, result), "auto_tag")
}

// --- Extract interface tool tests ---

func TestExtractInterface_Success(t *testing.T) {
	commands := &mockCommands{
		extractInterfaceFn: func(_ context.Context, req domain.ExtractInterfaceRequest) (string, error) {
			assert.Equal(t, "service.go", req.FilePath)
			assert.Equal(t, "Service", req.StructName)
			assert.Equal(t, "ServiceInterface", req.InterfaceName)
			assert.Equal(t, "iface.go", req.OutPath)
			return "iface.go", nil
		},
	}
	cs := setupTest(t, commands, &mockQueries{})

	result := callTool(t, cs, "scaffold", map[string]any{
		"kind":       "interface_from_type",
		"file":       "service.go",
		"identifier": "Service",
		"target":     "ServiceInterface",
		"out":        "iface.go",
	})
	text := resultText(t, result)
	assert.Contains(t, text, "SUCCESS")
	assert.Contains(t, text, "ServiceInterface")
	assert.Contains(t, text, "iface.go")
}

func TestExtractInterface_Error(t *testing.T) {
	commands := &mockCommands{
		extractInterfaceFn: func(_ context.Context, _ domain.ExtractInterfaceRequest) (string, error) {
			return "", errors.New("struct has no exported methods")
		},
	}
	cs := setupTest(t, commands, &mockQueries{})

	result := callTool(t, cs, "scaffold", map[string]any{
		"kind":       "interface_from_type",
		"file":       "service.go",
		"identifier": "Service",
		"target":     "ServiceInterface",
	})
	assert.True(t, result.IsError)
	assert.Contains(t, resultText(t, result), "no exported methods")
}

// --- Graph formatting edge cases ---

func TestOverview_WithSymbols(t *testing.T) {
	queries := &mockQueries{
		graphFn: func(_ context.Context, opts domain.GraphOptions) ([]domain.GraphPackage, error) {
			return []domain.GraphPackage{{
				Path: "internal/domain",
				Files: []domain.GraphFile{{
					Path:    "internal/domain/book.go",
					Symbols: []string{"type Book struct", "func NewBook(title string) *Book"},
				}},
			}}, nil
		},
	}
	cs := setupTest(t, &mockCommands{}, queries)

	result := callTool(t, cs, "overview", map[string]any{"symbols": true, "dir": "internal/domain"})
	text := resultText(t, result)
	assert.Contains(t, text, "book.go")
	assert.Contains(t, text, "type Book struct")
	assert.Contains(t, text, "func NewBook")
}

func TestOverview_EmptyResult(t *testing.T) {
	queries := &mockQueries{
		graphFn: func(_ context.Context, _ domain.GraphOptions) ([]domain.GraphPackage, error) {
			return nil, nil
		},
	}
	cs := setupTest(t, &mockCommands{}, queries)

	result := callTool(t, cs, "overview", map[string]any{})
	text := resultText(t, result)
	assert.Contains(t, text, "No Go packages found")
}

func TestOverview_WithSummaryAndDeps(t *testing.T) {
	queries := &mockQueries{
		graphFn: func(_ context.Context, opts domain.GraphOptions) ([]domain.GraphPackage, error) {
			assert.True(t, opts.Summary)
			assert.True(t, opts.Deps)
			return []domain.GraphPackage{{
				Path:    "internal/domain",
				Summary: "Core domain types",
				Deps:    []string{"internal/util"},
			}}, nil
		},
	}
	cs := setupTest(t, &mockCommands{}, queries)

	result := callTool(t, cs, "overview", map[string]any{"summary": true, "deps": true})
	text := resultText(t, result)
	assert.True(t, strings.Contains(text, "Core domain types"))
	assert.True(t, strings.Contains(text, "internal/util"))
}

func (m *mockCommands) PatchFunction(ctx context.Context, req domain.PatchFunctionRequest) (domain.PatchFunctionResult, error) {
	if m.patchFunctionFn != nil {
		return m.patchFunctionFn(ctx, req)
	}
	return domain.PatchFunctionResult{}, nil
}

func (m *mockCommands) PatchStruct(ctx context.Context, req domain.PatchStructRequest) (domain.PatchStructResult, error) {
	if m.patchStructFn != nil {
		return m.patchStructFn(ctx, req)
	}
	return domain.PatchStructResult{}, nil
}

func (m *mockCommands) PatchInterface(ctx context.Context, req domain.PatchInterfaceRequest) (domain.PatchInterfaceResult, error) {
	if m.patchInterfaceFn != nil {
		return m.patchInterfaceFn(ctx, req)
	}
	return domain.PatchInterfaceResult{}, nil
}

func (m *mockCommands) PatchFile(ctx context.Context, req domain.PatchFileRequest) (domain.PatchFileResult, error) {
	if m.patchFileFn != nil {
		return m.patchFileFn(ctx, req)
	}
	return domain.PatchFileResult{}, nil
}

func (m *mockCommands) PatchDecl(ctx context.Context, req domain.PatchDeclRequest) (domain.PatchDeclResult, error) {
	if m.patchDeclFn != nil {
		return m.patchDeclFn(ctx, req)
	}
	return domain.PatchDeclResult{}, nil
}

func TestTestRun_Success(t *testing.T) {
	queries := &mockQueries{
		testRunFn: func(_ context.Context, req domain.TestRunRequest) (domain.TestRunResult, error) {
			assert.Equal(t, "internal/foo", req.Dir)
			assert.Equal(t, "TestFoo", req.Run)
			assert.Equal(t, 3, req.Count)
			assert.True(t, req.Race)
			return domain.TestRunResult{
				Success: true,
				Tests: []domain.TestCaseResult{
					{Package: "testmod", Name: "TestFoo", Status: "pass", ElapsedMS: 12},
				},
				Summary:    "1 passed, 0 failed in 1 package (0.0s)",
				DurationMS: 42,
			}, nil
		},
	}
	cs := setupTest(t, &mockCommands{}, queries)

	result := callTool(t, cs, "test_run", map[string]any{
		"dir":   "internal/foo",
		"run":   "TestFoo",
		"count": 3,
		"race":  true,
	})
	text := resultText(t, result)
	assert.Contains(t, text, "SUCCESS")
	assert.Contains(t, text, "1 passed")
	assert.False(t, result.IsError)
}

func TestTestRun_FailureShowsFileLine(t *testing.T) {
	queries := &mockQueries{
		testRunFn: func(_ context.Context, _ domain.TestRunRequest) (domain.TestRunResult, error) {
			return domain.TestRunResult{
				Success: false,
				Tests: []domain.TestCaseResult{{
					Package: "testmod", Name: "TestBad", Status: "fail",
					FailureFile:    "math_test.go",
					FailureLine:    6,
					FailureMessage: "unexpected result: 42",
				}},
				Summary: "0 passed, 1 failed in 1 package (0.0s)",
			}, nil
		},
	}
	cs := setupTest(t, &mockCommands{}, queries)

	result := callTool(t, cs, "test_run", map[string]any{})
	text := resultText(t, result)
	assert.Contains(t, text, "FAIL")
	assert.Contains(t, text, "math_test.go:6")
	assert.Contains(t, text, "unexpected result: 42")
}

func TestTestRun_Error(t *testing.T) {
	queries := &mockQueries{
		testRunFn: func(_ context.Context, _ domain.TestRunRequest) (domain.TestRunResult, error) {
			return domain.TestRunResult{}, errors.New("invalid build tags")
		},
	}
	cs := setupTest(t, &mockCommands{}, queries)

	result := callTool(t, cs, "test_run", map[string]any{"tags": "not;valid"})
	assert.True(t, result.IsError)
	assert.Contains(t, resultText(t, result), "invalid build tags")
}

// TestPatchDecl_ToolRoutesToHandler verifies that the patch_decl tool is
// registered, accepts the expected arguments schema, and routes to the
// PatchDecl command handler with correctly-mapped fields.
func TestPatchDecl_ToolRoutesToHandler(t *testing.T) {
	var received domain.PatchDeclRequest
	commands := &mockCommands{
		patchDeclFn: func(_ context.Context, req domain.PatchDeclRequest) (domain.PatchDeclResult, error) {
			received = req
			return domain.PatchDeclResult{Applied: 1, Diff: "diff"}, nil
		},
	}
	cs := setupTest(t, commands, &mockQueries{})

	result := callTool(t, cs, "patch", map[string]any{
		"target": "decl",
		"items": []map[string]any{
			{
				"file":       "foo.go",
				"identifier": "serverInstructions",
				"patches": []map[string]any{
					{"op": "replace", "match": "hello", "replace": "hi"},
				},
			},
		},
	})
	require.False(t, result.IsError, resultText(t, result))
	assert.Equal(t, "foo.go", received.FilePath)
	assert.Equal(t, "serverInstructions", received.Identifier)
	require.Len(t, received.Patches, 1)
	assert.Equal(t, domain.PatchOpReplace, received.Patches[0].Op)
	assert.Equal(t, "hello", received.Patches[0].Match)
	assert.Equal(t, "hi", received.Patches[0].Replace)
}

func (m *mockCommands) Rename(ctx context.Context, req domain.RenameRequest) (domain.RenameResult, error) {
	if m.renameFn != nil {
		return m.renameFn(ctx, req)
	}
	return domain.RenameResult{}, nil
}

func (m *mockQueries) FindReferences(ctx context.Context, query domain.ReferencesQuery) (domain.ReferencesResult, error) {
	if m.findReferencesFn != nil {
		return m.findReferencesFn(ctx, query)
	}
	return domain.ReferencesResult{}, nil
}

func (m *mockQueries) FindDefinition(ctx context.Context, query domain.ReferencesQuery) (domain.ReferencesResult, error) {
	if m.findDefinitionFn != nil {
		return m.findDefinitionFn(ctx, query)
	}
	return domain.ReferencesResult{}, nil
}

// structuredErrorCode extracts the "code" field from a CallToolResult's
// StructuredContent regardless of whether it arrived as a map (post-JSON
// round-trip on the client side) or as the original errorOutput struct.
func structuredErrorCode(t *testing.T, result *mcp.CallToolResult) string {
	t.Helper()
	require.NotNil(t, result.StructuredContent, "expected StructuredContent on error result")
	// After JSON wire transport the any type is decoded into map[string]any.
	if m, ok := result.StructuredContent.(map[string]any); ok {
		code, _ := m["code"].(string)
		return code
	}
	// Fallback: marshal + unmarshal in case the SDK ever surfaces the raw
	// concrete type (e.g. json.RawMessage or the original struct).
	data, err := json.Marshal(result.StructuredContent)
	require.NoError(t, err)
	var decoded struct {
		Code string `json:"code"`
	}
	require.NoError(t, json.Unmarshal(data, &decoded))
	return decoded.Code
}

// TestErrorResultWithCode_DomainErrorSurfacesCode verifies that when a
// handler returns a *domain.Error, the tool result exposes the code in
// StructuredContent so agents can branch on it without string-matching.
func TestErrorResultWithCode_DomainErrorSurfacesCode(t *testing.T) {
	commands := &mockCommands{
		renameFn: func(_ context.Context, _ domain.RenameRequest) (domain.RenameResult, error) {
			return domain.RenameResult{}, &domain.Error{
				Code:    "CONFLICT",
				Message: "target name already exists",
			}
		},
	}
	cs := setupTest(t, commands, &mockQueries{})

	result := callTool(t, cs, "rename_symbol", map[string]any{
		"name":     "Foo",
		"new_name": "Bar",
	})
	require.True(t, result.IsError)
	// Text content must still mirror the legacy errorResult format so existing
	// agent UIs continue to render it.
	assert.Contains(t, resultText(t, result), "rename_symbol:")
	assert.Contains(t, resultText(t, result), "CONFLICT")
	assert.Equal(t, "CONFLICT", structuredErrorCode(t, result))
}

// TestErrorResultWithCode_PlainErrorSurfacesUnknown verifies that errors
// which are NOT *domain.Error fall back to code="UNKNOWN" so agents have
// a stable discriminator even for unexpected failures.
func TestErrorResultWithCode_PlainErrorSurfacesUnknown(t *testing.T) {
	queries := &mockQueries{
		findDefinitionFn: func(_ context.Context, _ domain.ReferencesQuery) (domain.ReferencesResult, error) {
			return domain.ReferencesResult{}, errors.New("boom")
		},
	}
	cs := setupTest(t, &mockCommands{}, queries)

	result := callTool(t, cs, "find_definition", map[string]any{"name": "Foo"})
	require.True(t, result.IsError)
	assert.Contains(t, resultText(t, result), "find_definition:")
	assert.Contains(t, resultText(t, result), "boom")
	assert.Equal(t, "UNKNOWN", structuredErrorCode(t, result))
}

// TestErrorResult_PlainStringUsesGenericCode verifies that argument-validation
// errors (which go through errorResult, not errorResultWithCode) expose a
// generic "ERROR" code so every error path still produces structured data.
func TestErrorResult_PlainStringUsesGenericCode(t *testing.T) {
	cs := setupTest(t, &mockCommands{}, &mockQueries{})

	// symbol with both query and pattern triggers the plain errorResult
	// branch (no underlying error, just a validation string).
	result := callTool(t, cs, "symbol", map[string]any{
		"query":   "Foo",
		"pattern": "Bar",
	})
	require.True(t, result.IsError)
	assert.Contains(t, resultText(t, result), "mutually exclusive")
	assert.Equal(t, "ERROR", structuredErrorCode(t, result))
}

// TestRenameSymbol_ExportChange_RejectedByDefault verifies that a case-flip
// rename (foo -> Foo) is rejected by default and the error message points
// the caller at the allow_export_change escape hatch.
func TestRenameSymbol_ExportChange_RejectedByDefault(t *testing.T) {
	var captured domain.RenameRequest
	commands := &mockCommands{
		renameFn: func(_ context.Context, req domain.RenameRequest) (domain.RenameResult, error) {
			captured = req
			return domain.RenameResult{}, &domain.Error{
				Code:    "INVALID_ARGUMENT",
				Message: "rename: changing export status (\"Foo\" \u2192 \"foo\") is not allowed; rename in two steps if intentional, or pass allow_export_change=true",
			}
		},
	}
	cs := setupTest(t, commands, &mockQueries{})

	result := callTool(t, cs, "rename_symbol", map[string]any{
		"name":     "Foo",
		"new_name": "foo",
	})
	require.True(t, result.IsError)
	text := resultText(t, result)
	assert.Contains(t, text, "INVALID_ARGUMENT")
	assert.Contains(t, text, "allow_export_change")
	assert.Equal(t, "INVALID_ARGUMENT", structuredErrorCode(t, result))
	// The flag defaults to false when the caller omits it.
	assert.False(t, captured.AllowExportChange)
}

// TestRenameSymbol_ExportChange_AllowedWithFlag verifies that
// allow_export_change=true is forwarded to the domain request, that the
// warning returned by the handler surfaces in both the structured output
// and (prominently prefixed) in the text output, so agents can notice.
func TestRenameSymbol_ExportChange_AllowedWithFlag(t *testing.T) {
	var captured domain.RenameRequest
	commands := &mockCommands{
		renameFn: func(_ context.Context, req domain.RenameRequest) (domain.RenameResult, error) {
			captured = req
			return domain.RenameResult{
				OldName:       "Foo",
				NewName:       "foo",
				Kind:          "type",
				FilesModified: []string{"a.go"},
				Warnings:      []string{"export status changed: \"Foo\" (exported) \u2192 \"foo\" (unexported)"},
			}, nil
		},
	}
	cs := setupTest(t, commands, &mockQueries{})

	result := callTool(t, cs, "rename_symbol", map[string]any{
		"name":                "Foo",
		"new_name":            "foo",
		"allow_export_change": true,
	})
	require.False(t, result.IsError, resultText(t, result))
	assert.True(t, captured.AllowExportChange, "flag should be forwarded to domain")

	text := resultText(t, result)
	// The banner must LEAD the text output so agents notice even when they
	// only read the first line.
	assert.True(t, strings.HasPrefix(text, "\u26a0 EXPORT CHANGED:"), "text must start with export-changed banner; got: %q", text)
	assert.Contains(t, text, "export status changed")
	assert.Contains(t, text, "exported")
	assert.Contains(t, text, "unexported")

	// And the structured content must carry the warning too so machine
	// callers can branch without parsing the text.
	require.NotNil(t, result.StructuredContent)
	data, err := json.Marshal(result.StructuredContent)
	require.NoError(t, err)
	var decoded struct {
		Warnings []string `json:"warnings"`
	}
	require.NoError(t, json.Unmarshal(data, &decoded))
	require.Len(t, decoded.Warnings, 1)
	assert.Contains(t, decoded.Warnings[0], "export status changed")
}

// TestPatchFunction_AutoLiftBannerLeadsText verifies that when the handler's
// mocked result carries a non-empty AutoLifts slice, the TEXT content of the
// tool result LEADS with a loud banner (not the usual "OK: ..." prefix). This
// is what agents see when they only inspect text and never parse
// StructuredContent — they must notice the lift.
//
// The banner is prepended BEFORE the OK/PREVIEW line; the per-patch
// "AUTO_LIFTED patch #K: from X -> Y" lines are still kept afterward.
func TestPatchFunction_AutoLiftBannerLeadsText(t *testing.T) {
	commands := &mockCommands{
		patchFunctionFn: func(_ context.Context, _ domain.PatchFunctionRequest) (domain.PatchFunctionResult, error) {
			// Mirror what patch_function returns when an insert_before anchor
			// lands inside an `if { ... }` block inside a func body: the patch
			// is applied but auto-lifted to the enclosing top-level statement.
			return domain.PatchFunctionResult{
				Applied: 1,
				AutoLifts: []domain.AutoLiftInfo{{
					PatchIndex: 1,
					LiftedFrom: "if/else branch at L12",
					LiftedTo:   "function body at L10",
					Context:    "  line with +marker",
				}},
			}, nil
		},
	}
	cs := setupTest(t, commands, &mockQueries{})

	result := callTool(t, cs, "patch", map[string]any{
		"target": "function",
		"items": []map[string]any{
			{
				"file":       "foo.go",
				"identifier": "Foo",
				"patches": []map[string]any{
					{"op": "insert_before", "match": "anchor", "code": "log.Println(\"hi\")"},
				},
			},
		},
	})

	require.False(t, result.IsError, resultText(t, result))
	text := resultText(t, result)

	// 1) The TEXT must LEAD with the warning banner — agents that only read
	//    text and never inspect StructuredContent must see it immediately.
	require.True(t, strings.HasPrefix(text, "\u26a0 AUTO-LIFTED:"),
		"text must lead with the auto-lift banner, got: %q", text)
	assert.Contains(t, text, "1 patch(es) moved to the enclosing top-level statement")

	// 2) The legacy "OK: ..." line is still present, just no longer first.
	assert.Contains(t, text, "OK: 1 patch(es) applied")

	// 3) The per-patch AUTO_LIFTED detail line is still kept.
	assert.Contains(t, text, "AUTO_LIFTED patch #1: from if/else branch at L12 -> function body at L10")

	// 4) The banner must come before the OK line in the text.
	bannerIdx := strings.Index(text, "\u26a0 AUTO-LIFTED")
	okIdx := strings.Index(text, "OK: 1 patch(es) applied")
	require.GreaterOrEqual(t, bannerIdx, 0)
	require.GreaterOrEqual(t, okIdx, 0)
	assert.Less(t, bannerIdx, okIdx, "banner must lead the text output")
}

func (m *mockCommands) PatchStructBulk(ctx context.Context, req domain.PatchStructBulkRequest) (domain.PatchStructBulkResult, error) {
	if m.patchStructBulkFn != nil {
		return m.patchStructBulkFn(ctx, req)
	}
	return domain.PatchStructBulkResult{}, nil
}

func (m *mockCommands) PatchFunctionBulk(ctx context.Context, req domain.PatchFunctionBulkRequest) (domain.PatchFunctionBulkResult, error) {
	if m.patchFunctionBulkFn != nil {
		return m.patchFunctionBulkFn(ctx, req)
	}
	return domain.PatchFunctionBulkResult{}, nil
}

func TestDelete_File(t *testing.T) {
	var receivedPlan domain.Plan
	commands := &mockCommands{
		executePlanFn: func(_ context.Context, plan domain.Plan) (domain.PlanResult, error) {
			receivedPlan = plan
			return domain.PlanResult{FilesModified: 1}, nil
		},
	}
	cs := setupTest(t, commands, &mockQueries{})

	result := callTool(t, cs, "delete", map[string]any{
		"object": "file",
		"file":   "internal/domain/book.go",
	})

	text := resultText(t, result)
	assert.Contains(t, text, "SUCCESS (delete file)")

	require.Len(t, receivedPlan.Actions, 1)
	assert.Equal(t, domain.ActionTypeDeleteFile, receivedPlan.Actions[0].Action)
	assert.Equal(t, "internal/domain/book.go", receivedPlan.Actions[0].FilePath)
}

func TestCreate_Auto_FuncContent(t *testing.T) {
	var receivedPlan domain.Plan
	commands := &mockCommands{
		executePlanFn: func(_ context.Context, plan domain.Plan) (domain.PlanResult, error) {
			receivedPlan = plan
			return domain.PlanResult{FilesModified: 1}, nil
		},
	}
	cs := setupTest(t, commands, &mockQueries{})

	result := callTool(t, cs, "create", map[string]any{
		"object":  "auto",
		"file":    "internal/domain/book.go",
		"content": "func NewBook(title string) *Book { return &Book{Title: title} }",
	})

	assert.False(t, result.IsError)
	require.Len(t, receivedPlan.Actions, 1)
	assert.Equal(t, domain.ActionTypeAddFunc, receivedPlan.Actions[0].Action)
}

func TestCreate_Auto_StructContent(t *testing.T) {
	var receivedPlan domain.Plan
	commands := &mockCommands{
		executePlanFn: func(_ context.Context, plan domain.Plan) (domain.PlanResult, error) {
			receivedPlan = plan
			return domain.PlanResult{FilesModified: 1}, nil
		},
	}
	cs := setupTest(t, commands, &mockQueries{})

	result := callTool(t, cs, "create", map[string]any{
		"object":  "auto",
		"file":    "internal/domain/book.go",
		"content": "type Book struct {\n\tTitle string\n}",
	})

	assert.False(t, result.IsError)
	require.Len(t, receivedPlan.Actions, 1)
	assert.Equal(t, domain.ActionTypeAddStruct, receivedPlan.Actions[0].Action)
}

func TestCreate_Auto_FileContent(t *testing.T) {
	var receivedPlan domain.Plan
	commands := &mockCommands{
		executePlanFn: func(_ context.Context, plan domain.Plan) (domain.PlanResult, error) {
			receivedPlan = plan
			return domain.PlanResult{FilesModified: 1}, nil
		},
	}
	cs := setupTest(t, commands, &mockQueries{})

	result := callTool(t, cs, "create", map[string]any{
		"object":  "auto",
		"file":    "internal/domain/book.go",
		"content": "package domain\n\nvar ErrNotFound = errors.New(\"not found\")",
	})

	assert.False(t, result.IsError)
	require.Len(t, receivedPlan.Actions, 1)
	assert.Equal(t, domain.ActionTypeCreateFile, receivedPlan.Actions[0].Action)
}

func TestUpdate_Auto_FuncContent(t *testing.T) {
	var receivedPlan domain.Plan
	commands := &mockCommands{
		executePlanFn: func(_ context.Context, plan domain.Plan) (domain.PlanResult, error) {
			receivedPlan = plan
			return domain.PlanResult{FilesModified: 1}, nil
		},
	}
	cs := setupTest(t, commands, &mockQueries{})

	result := callTool(t, cs, "update", map[string]any{
		"object":  "auto",
		"file":    "internal/domain/book.go",
		"content": "func NewBook(title string) *Book { return &Book{Title: title} }",
	})

	assert.False(t, result.IsError)
	require.Len(t, receivedPlan.Actions, 1)
	assert.Equal(t, domain.ActionTypeUpdateFunc, receivedPlan.Actions[0].Action)
}

func TestUpdate_Auto_StructContent(t *testing.T) {
	var receivedPlan domain.Plan
	commands := &mockCommands{
		executePlanFn: func(_ context.Context, plan domain.Plan) (domain.PlanResult, error) {
			receivedPlan = plan
			return domain.PlanResult{FilesModified: 1}, nil
		},
	}
	cs := setupTest(t, commands, &mockQueries{})

	result := callTool(t, cs, "update", map[string]any{
		"object":  "auto",
		"file":    "internal/domain/book.go",
		"content": "type Book struct {\n\tTitle string\n}",
	})

	assert.False(t, result.IsError)
	require.Len(t, receivedPlan.Actions, 1)
	assert.Equal(t, domain.ActionTypeUpdateStruct, receivedPlan.Actions[0].Action)
}

func TestUpdate_Auto_ConstContent(t *testing.T) {
	var receivedPlan domain.Plan
	commands := &mockCommands{
		executePlanFn: func(_ context.Context, plan domain.Plan) (domain.PlanResult, error) {
			receivedPlan = plan
			return domain.PlanResult{FilesModified: 1}, nil
		},
	}
	cs := setupTest(t, commands, &mockQueries{})

	result := callTool(t, cs, "update", map[string]any{
		"object":     "auto",
		"file":       "internal/domain/book.go",
		"identifier": "BookTitle",
		"content":    "const BookTitle = \"Updated Library\"",
	})

	assert.False(t, result.IsError)
	require.Len(t, receivedPlan.Actions, 1)
	assert.Equal(t, domain.ActionTypeUpdateDecl, receivedPlan.Actions[0].Action)
	assert.Equal(t, "BookTitle", receivedPlan.Actions[0].Identifier)
}

func TestUpdate_Auto_VarContent(t *testing.T) {
	var receivedPlan domain.Plan
	commands := &mockCommands{
		executePlanFn: func(_ context.Context, plan domain.Plan) (domain.PlanResult, error) {
			receivedPlan = plan
			return domain.PlanResult{FilesModified: 1}, nil
		},
	}
	cs := setupTest(t, commands, &mockQueries{})

	result := callTool(t, cs, "update", map[string]any{
		"object":     "auto",
		"file":       "internal/domain/book.go",
		"identifier": "ErrNotFound",
		"content":    "var ErrNotFound = errors.New(\"updated not found\")",
	})

	assert.False(t, result.IsError)
	require.Len(t, receivedPlan.Actions, 1)
	assert.Equal(t, domain.ActionTypeUpdateDecl, receivedPlan.Actions[0].Action)
	assert.Equal(t, "ErrNotFound", receivedPlan.Actions[0].Identifier)
}

func TestUpdate_Auto_FileContent(t *testing.T) {
	var receivedPlan domain.Plan
	commands := &mockCommands{
		executePlanFn: func(_ context.Context, plan domain.Plan) (domain.PlanResult, error) {
			receivedPlan = plan
			return domain.PlanResult{FilesModified: 1}, nil
		},
	}
	cs := setupTest(t, commands, &mockQueries{})

	result := callTool(t, cs, "update", map[string]any{
		"object":  "auto",
		"file":    "internal/domain/book.go",
		"content": "package domain\n\nvar ErrNotFound = errors.New(\"not found\")",
	})

	assert.False(t, result.IsError)
	require.Len(t, receivedPlan.Actions, 1)
	assert.Equal(t, domain.ActionTypeReplaceFile, receivedPlan.Actions[0].Action)
}

// --- scaffold tool validation tests ---

func TestDerive_MissingKind(t *testing.T) {
	cs := setupTest(t, &mockCommands{}, &mockQueries{})
	result := callTool(t, cs, "scaffold", map[string]any{"file": "f.go"})
	require.True(t, result.IsError)
	// Schema-level rejection mentions the missing 'kind' field; an empty
	// kind passed explicitly would also be rejected by the runtime check
	// with "kind is required".
	assert.Contains(t, resultText(t, result), "kind")
}

func TestDerive_EmptyKind(t *testing.T) {
	cs := setupTest(t, &mockCommands{}, &mockQueries{})
	result := callTool(t, cs, "scaffold", map[string]any{"kind": "", "file": "f.go"})
	require.True(t, result.IsError)
	assert.Contains(t, resultText(t, result), "kind is required")
}

func TestDerive_UnknownKind(t *testing.T) {
	cs := setupTest(t, &mockCommands{}, &mockQueries{})
	result := callTool(t, cs, "scaffold", map[string]any{"kind": "bogus", "file": "f.go"})
	require.True(t, result.IsError)
	assert.Contains(t, resultText(t, result), "unknown kind")
}

func TestDerive_InterfaceFromType_MissingIdentifier(t *testing.T) {
	cs := setupTest(t, &mockCommands{}, &mockQueries{})
	result := callTool(t, cs, "scaffold", map[string]any{
		"kind":   "interface_from_type",
		"file":   "service.go",
		"target": "ServiceAPI",
	})
	require.True(t, result.IsError)
	assert.Contains(t, resultText(t, result), "identifier")
}

func TestDerive_InterfaceFromType_MissingTarget(t *testing.T) {
	cs := setupTest(t, &mockCommands{}, &mockQueries{})
	result := callTool(t, cs, "scaffold", map[string]any{
		"kind":       "interface_from_type",
		"file":       "service.go",
		"identifier": "Service",
	})
	require.True(t, result.IsError)
	assert.Contains(t, resultText(t, result), "target")
}

func TestDerive_ImplFromInterface_MissingSource(t *testing.T) {
	cs := setupTest(t, &mockCommands{}, &mockQueries{})
	result := callTool(t, cs, "scaffold", map[string]any{
		"kind":   "impl_from_interface",
		"file":   "f.go",
		"target": "*Foo",
	})
	require.True(t, result.IsError)
	assert.Contains(t, resultText(t, result), "source")
}

func TestDerive_ImplFromInterface_MissingTarget(t *testing.T) {
	cs := setupTest(t, &mockCommands{}, &mockQueries{})
	result := callTool(t, cs, "scaffold", map[string]any{
		"kind":   "impl_from_interface",
		"file":   "f.go",
		"source": "io.Reader",
	})
	require.True(t, result.IsError)
	assert.Contains(t, resultText(t, result), "target")
}

func TestDerive_MockFromInterface_MissingSource(t *testing.T) {
	cs := setupTest(t, &mockCommands{}, &mockQueries{})
	result := callTool(t, cs, "scaffold", map[string]any{
		"kind":   "mock_from_interface",
		"file":   "mock.go",
		"target": "MockReader",
	})
	require.True(t, result.IsError)
	assert.Contains(t, resultText(t, result), "source")
}

func TestDerive_MockFromInterface_MissingTarget(t *testing.T) {
	cs := setupTest(t, &mockCommands{}, &mockQueries{})
	result := callTool(t, cs, "scaffold", map[string]any{
		"kind":   "mock_from_interface",
		"file":   "mock.go",
		"source": "io.Reader",
	})
	require.True(t, result.IsError)
	assert.Contains(t, resultText(t, result), "target")
}

func TestDerive_ImplFromInterface_RejectsOut(t *testing.T) {
	cs := setupTest(t, &mockCommands{}, &mockQueries{})
	result := callTool(t, cs, "scaffold", map[string]any{
		"kind":   "impl_from_interface",
		"file":   "f.go",
		"source": "io.Reader",
		"target": "*Foo",
		"out":    "iface.go",
	})
	require.True(t, result.IsError)
	assert.Contains(t, resultText(t, result), "out is not allowed")
}

func TestDerive_MockFromInterface_RejectsMockFile(t *testing.T) {
	cs := setupTest(t, &mockCommands{}, &mockQueries{})
	result := callTool(t, cs, "scaffold", map[string]any{
		"kind":      "mock_from_interface",
		"file":      "mock.go",
		"source":    "io.Reader",
		"target":    "MockReader",
		"mock_file": "other.go",
	})
	require.True(t, result.IsError)
	assert.Contains(t, resultText(t, result), "mock_file")
}

func TestDerive_InterfaceFromType_MockFileWithoutMockName(t *testing.T) {
	cs := setupTest(t, &mockCommands{}, &mockQueries{})
	result := callTool(t, cs, "scaffold", map[string]any{
		"kind":       "interface_from_type",
		"file":       "service.go",
		"identifier": "Service",
		"target":     "ServiceAPI",
		"mock_file":  "mock.go",
	})
	require.True(t, result.IsError)
	assert.Contains(t, resultText(t, result), "mock_file and mock_name")
}

func TestDerive_InterfaceFromType_MockNameWithoutMockFile(t *testing.T) {
	cs := setupTest(t, &mockCommands{}, &mockQueries{})
	result := callTool(t, cs, "scaffold", map[string]any{
		"kind":       "interface_from_type",
		"file":       "service.go",
		"identifier": "Service",
		"target":     "ServiceAPI",
		"mock_name":  "MockService",
	})
	require.True(t, result.IsError)
	assert.Contains(t, resultText(t, result), "mock_file and mock_name")
}
