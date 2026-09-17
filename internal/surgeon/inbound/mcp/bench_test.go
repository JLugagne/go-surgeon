package mcp_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	appcommands "github.com/JLugagne/go-surgeon/internal/surgeon/app/commands"
	appqueries "github.com/JLugagne/go-surgeon/internal/surgeon/app/queries"
	surgeonmcp "github.com/JLugagne/go-surgeon/internal/surgeon/inbound/mcp"
	outboundfs "github.com/JLugagne/go-surgeon/internal/surgeon/outbound/filesystem"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
)

// benchServer wires the real command/query handlers behind an in-memory
// MCP session so benchmarks measure the same pipeline a client hits,
// including JSON argument handling.
func benchServer(b *testing.B) *mcp.ClientSession {
	b.Helper()
	dir := b.TempDir()
	require.NoError(b, os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/bench\n\ngo 1.25\n"), 0644))
	require.NoError(b, os.MkdirAll(filepath.Join(dir, "sub"), 0755))
	require.NoError(b, os.WriteFile(filepath.Join(dir, "lib.go"), []byte(
		"package bench\n\nimport \"example.com/bench/sub\"\n\n// Add returns the sum via sub.Sum.\nfunc Add(a, b int) int { return sub.Sum(a, b) }\n\nfunc Helper() string { return \"h\" }\n"), 0644))
	require.NoError(b, os.WriteFile(filepath.Join(dir, "sub", "sub.go"), []byte(
		"package sub\n\nfunc Sum(a, b int) int { return a + b }\n\nfunc Stable() int { return 1 }\n"), 0644))

	oldwd, err := os.Getwd()
	require.NoError(b, err)
	require.NoError(b, os.Chdir(dir))
	b.Cleanup(func() { _ = os.Chdir(oldwd) })

	fs := outboundfs.NewFileSystem()
	commands := appcommands.NewExecutePlanHandler(fs)
	queries := appqueries.NewSurgeonQueriesHandler(fs)
	server := surgeonmcp.NewServer(commands, queries)

	clientTransport, serverTransport := mcp.NewInMemoryTransports()
	ctx := context.Background()
	ss, err := server.Connect(ctx, serverTransport, nil)
	require.NoError(b, err)
	b.Cleanup(func() { _ = ss.Close() })
	client := mcp.NewClient(&mcp.Implementation{Name: "bench-client", Version: "1.0.0"}, nil)
	cs, err := client.Connect(ctx, clientTransport, nil)
	require.NoError(b, err)
	b.Cleanup(func() { _ = cs.Close() })
	return cs
}

func benchCall(b *testing.B, cs *mcp.ClientSession, name string, args map[string]any) {
	b.Helper()
	ctx := context.Background()
	if _, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args}); err != nil {
		b.Fatal(err)
	}
}

func BenchmarkMCPRead(b *testing.B) {
	cs := benchServer(b)
	args := map[string]any{"file": "lib.go"}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		benchCall(b, cs, "read", args)
	}
}

func BenchmarkMCPSymbol(b *testing.B) {
	cs := benchServer(b)
	args := map[string]any{"query": "Add", "body": true}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		benchCall(b, cs, "symbol", args)
	}
}

func BenchmarkMCPOverview(b *testing.B) {
	cs := benchServer(b)
	args := map[string]any{"symbols": true}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		benchCall(b, cs, "overview", args)
	}
}

func BenchmarkMCPPatchPreview(b *testing.B) {
	cs := benchServer(b)
	args := map[string]any{
		"target":     "function",
		"file":       "sub/sub.go",
		"identifier": "Stable",
		"preview":    true,
		"patches": []map[string]any{{
			"op":      "replace",
			"match":   "return 1",
			"replace": "return 2",
		}},
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		benchCall(b, cs, "patch", args)
	}
}
