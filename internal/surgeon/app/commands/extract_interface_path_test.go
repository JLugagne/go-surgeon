package commands_test

import (
	"context"
	"testing"

	"github.com/JLugagne/go-surgeon/internal/surgeon/app/commands"
	"github.com/JLugagne/go-surgeon/internal/surgeon/domain"
	"github.com/stretchr/testify/require"
)

// TestExtractInterface_ReturnsDestPath asserts the returned string is the
// interface's destination file path — both the CLI ("Extracted %s into %s")
// and the MCP structured field interface_file consume it as a path. Before
// the fix it returned AddInterface's prose message.
func TestExtractInterface_ReturnsDestPath(t *testing.T) {
	const path = "/tmp/extract_src.go"
	const out = "/tmp/extract_iface.go"
	src := "package svc\n\ntype Service struct{}\n\nfunc (s *Service) Get(id string) error { return nil }\n"
	fs := &mockFS{files: map[string][]byte{path: []byte(src)}}
	h := commands.NewExecutePlanHandler(fs)

	got, err := h.ExtractInterface(context.Background(), domain.ExtractInterfaceRequest{
		FilePath:      path,
		StructName:    "Service",
		InterfaceName: "ServicePort",
		OutPath:       out,
	})
	require.NoError(t, err)
	require.Equal(t, out, got, "ExtractInterface must return the destination file path")
}
