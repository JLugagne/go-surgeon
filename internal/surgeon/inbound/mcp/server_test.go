package mcp_test

import (
	"context"
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
	executePlanFn      func(ctx context.Context, plan domain.Plan) (domain.PlanResult, error)
	implementFn        func(ctx context.Context, req domain.ImplementRequest) ([]domain.SymbolResult, error)
	mockFn             func(ctx context.Context, req domain.MockRequest) (string, error)
	addInterfaceFn     func(ctx context.Context, req domain.InterfaceActionRequest) (string, []string, error)
	updateInterfaceFn  func(ctx context.Context, req domain.InterfaceActionRequest) (string, []string, error)
	deleteInterfaceFn  func(ctx context.Context, req domain.InterfaceActionRequest) (string, []string, error)
	generateTestFn     func(ctx context.Context, filePath, identifier string) (string, error)
	tagStructFn        func(ctx context.Context, req domain.TagRequest) error
	extractInterfaceFn func(ctx context.Context, req domain.ExtractInterfaceRequest) (string, error)
	patchFunctionFn    func(ctx context.Context, req domain.PatchFunctionRequest) (domain.PatchFunctionResult, error)
	patchStructFn      func(ctx context.Context, req domain.PatchStructRequest) (domain.PatchStructResult, error)
	patchInterfaceFn   func(ctx context.Context, req domain.PatchInterfaceRequest) (domain.PatchInterfaceResult, error)
	patchFileFn        func(ctx context.Context, req domain.PatchFileRequest) (domain.PatchFileResult, error)
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
	findSymbolsFn func(ctx context.Context, query domain.SymbolQuery, targetDir string) ([]domain.SymbolResult, error)
	graphFn       func(ctx context.Context, opts domain.GraphOptions) ([]domain.GraphPackage, error)
	buildCheckFn  func(ctx context.Context, req domain.BuildCheckRequest) (domain.BuildCheckResult, error)
	testRunFn     func(ctx context.Context, req domain.TestRunRequest) (domain.TestRunResult, error)
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
		"add_interface", "update_interface", "delete_interface",
		"insert_call",
		"execute_plan", "implement", "mock", "test", "tag", "extract_interface",
		"patch_function", "patch_struct", "patch_interface", "patch_file",
	}
	for _, name := range expected {
		assert.True(t, names[name], "missing tool: %s", name)
	}
	assert.Equal(t, len(expected), len(result.Tools), "unexpected number of tools")
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
	assert.Contains(t, text, "Code (Empty lines stripped):")
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
		"object":     "file",
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

	result := callTool(t, cs, "add_interface", map[string]any{
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

	result := callTool(t, cs, "update_interface", map[string]any{
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

	result := callTool(t, cs, "delete_interface", map[string]any{
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

	result := callTool(t, cs, "add_interface", map[string]any{
		"file":    "repo.go",
		"content": "type Repository interface {}",
	})
	assert.True(t, result.IsError)
	assert.Contains(t, resultText(t, result), "duplicate interface")
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

	result := callTool(t, cs, "implement", map[string]any{
		"interface": "io.Reader",
		"receiver":  "*MyReader",
		"file":      "reader.go",
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

	result := callTool(t, cs, "implement", map[string]any{
		"interface": "io.Reader",
		"receiver":  "*MyReader",
		"file":      "reader.go",
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

	result := callTool(t, cs, "implement", map[string]any{
		"interface": "pkg.Missing",
		"receiver":  "*MyStruct",
		"file":      "file.go",
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

	result := callTool(t, cs, "mock", map[string]any{
		"interface": "io.Writer",
		"mock_name": "MockWriter",
		"file":      "mocks/writer.go",
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

	result := callTool(t, cs, "mock", map[string]any{
		"interface": "pkg.Missing",
		"mock_name": "MockMissing",
		"file":      "mock.go",
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

// --- Tag tool tests ---

func TestTag_AutoJson(t *testing.T) {
	commands := &mockCommands{
		tagStructFn: func(_ context.Context, req domain.TagRequest) error {
			assert.Equal(t, "book.go", req.FilePath)
			assert.Equal(t, "Book", req.StructName)
			assert.Equal(t, "json", req.AutoFormat)
			return nil
		},
	}
	cs := setupTest(t, commands, &mockQueries{})

	result := callTool(t, cs, "tag", map[string]any{
		"file":       "book.go",
		"identifier": "Book",
		"auto":       "json",
	})
	text := resultText(t, result)
	assert.Contains(t, text, "SUCCESS")
	assert.Contains(t, text, "Book")
}

func TestTag_SetField(t *testing.T) {
	commands := &mockCommands{
		tagStructFn: func(_ context.Context, req domain.TagRequest) error {
			assert.Equal(t, "Title", req.FieldName)
			assert.Equal(t, `json:"book_title"`, req.SetTag)
			return nil
		},
	}
	cs := setupTest(t, commands, &mockQueries{})

	result := callTool(t, cs, "tag", map[string]any{
		"file":       "book.go",
		"identifier": "Book",
		"field":      "Title",
		"set":        `json:"book_title"`,
	})
	assert.False(t, result.IsError)
}

func TestTag_Error(t *testing.T) {
	commands := &mockCommands{
		tagStructFn: func(_ context.Context, _ domain.TagRequest) error {
			return errors.New("struct not found")
		},
	}
	cs := setupTest(t, commands, &mockQueries{})

	result := callTool(t, cs, "tag", map[string]any{
		"file":       "book.go",
		"identifier": "Missing",
		"auto":       "json",
	})
	assert.True(t, result.IsError)
	assert.Contains(t, resultText(t, result), "struct not found")
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

	result := callTool(t, cs, "extract_interface", map[string]any{
		"file":       "service.go",
		"identifier": "Service",
		"name":       "ServiceInterface",
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

	result := callTool(t, cs, "extract_interface", map[string]any{
		"file":       "service.go",
		"identifier": "Service",
		"name":       "ServiceInterface",
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
