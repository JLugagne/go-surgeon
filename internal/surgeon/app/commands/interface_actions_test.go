package commands_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/JLugagne/go-surgeon/internal/surgeon/app/commands"
	"github.com/JLugagne/go-surgeon/internal/surgeon/domain"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const bookIfaceSrc = `type BookRepository interface {
	Create(ctx context.Context, id string) error
	FindByID(ctx context.Context, id string) (*Book, error)
}`

func TestAddInterface_WithMock(t *testing.T) {
	tmpDir := t.TempDir()
	ifaceDir := filepath.Join(tmpDir, "repo")
	mockDir := filepath.Join(tmpDir, "repotest")
	ifaceFile := filepath.Join(ifaceDir, "repo.go")
	mockFile := filepath.Join(mockDir, "mock.go")

	ifaceContent := []byte("package repo\n")
	fs := &mockFS{
		files: map[string][]byte{
			filepath.Join(ifaceDir, "repo.go"): ifaceContent,
			filepath.Join(mockDir, "base.go"):  []byte("package repotest\n"),
		},
	}
	handler := commands.NewExecutePlanHandler(fs)

	req := domain.InterfaceActionRequest{
		FilePath: ifaceFile,
		Content:  bookIfaceSrc,
		MockFile: mockFile,
		MockName: "MockBookRepository",
	}

	result, err := handler.AddInterface(context.Background(), req)
	require.NoError(t, err)
	assert.Contains(t, result, "BookRepository")
	assert.Contains(t, result, "MockBookRepository")

	// Interface was appended to the file
	updated := string(fs.files[ifaceFile])
	assert.Contains(t, updated, "type BookRepository interface")

	// Mock was generated
	mockContent := string(fs.files[mockFile])
	assert.Contains(t, mockContent, "type MockBookRepository struct")
	assert.Contains(t, mockContent, "CreateFunc func(ctx context.Context, id string) error")
	assert.Contains(t, mockContent, "var _ repo.BookRepository = (*MockBookRepository)(nil)")
}

func TestAddInterface_WithoutMock(t *testing.T) {
	tmpDir := t.TempDir()
	ifaceFile := filepath.Join(tmpDir, "svc.go")
	fs := &mockFS{
		files: map[string][]byte{
			ifaceFile: []byte("package svc\n"),
		},
	}
	handler := commands.NewExecutePlanHandler(fs)

	req := domain.InterfaceActionRequest{
		FilePath: ifaceFile,
		Content:  `type Doer interface { Do() error }`,
	}

	result, err := handler.AddInterface(context.Background(), req)
	require.NoError(t, err)
	assert.Contains(t, result, "Doer")

	updated := string(fs.files[ifaceFile])
	assert.Contains(t, updated, "type Doer interface")
}

func TestUpdateInterface_WithMock(t *testing.T) {
	tmpDir := t.TempDir()
	ifaceDir := filepath.Join(tmpDir, "repo")
	mockDir := filepath.Join(tmpDir, "repotest")
	ifaceFile := filepath.Join(ifaceDir, "repo.go")
	mockFile := filepath.Join(mockDir, "mock.go")

	initialIface := `package repo

type BookRepository interface {
	Create(ctx context.Context, id string) error
}
`
	fs := &mockFS{
		files: map[string][]byte{
			ifaceFile:                           []byte(initialIface),
			filepath.Join(mockDir, "base.go"):   []byte("package repotest\n"),
			mockFile:                            []byte("package repotest\n// old mock\n"),
		},
	}
	handler := commands.NewExecutePlanHandler(fs)

	updatedSrc := `type BookRepository interface {
	Create(ctx context.Context, id string) error
	Delete(ctx context.Context, id string) error
}`

	req := domain.InterfaceActionRequest{
		FilePath:   ifaceFile,
		Identifier: "BookRepository",
		Content:    updatedSrc,
		MockFile:   mockFile,
		MockName:   "MockBookRepository",
	}

	result, err := handler.UpdateInterface(context.Background(), req)
	require.NoError(t, err)
	assert.Contains(t, result, "Updated BookRepository")
	assert.Contains(t, result, "MockBookRepository")

	// Interface was replaced
	ifaceContent := string(fs.files[ifaceFile])
	assert.Contains(t, ifaceContent, "Delete")

	// Mock was regenerated with new methods
	mockContent := string(fs.files[mockFile])
	assert.Contains(t, mockContent, "DeleteFunc")
	assert.NotContains(t, mockContent, "// old mock")
}

func TestDeleteInterface(t *testing.T) {
	tmpDir := t.TempDir()
	ifaceFile := filepath.Join(tmpDir, "repo.go")

	initialContent := `package repo

type BookRepository interface {
	Create(ctx context.Context, id string) error
}

type OtherType struct{}
`
	fs := &mockFS{
		files: map[string][]byte{
			ifaceFile: []byte(initialContent),
		},
	}
	handler := commands.NewExecutePlanHandler(fs)

	req := domain.InterfaceActionRequest{
		FilePath:   ifaceFile,
		Identifier: "BookRepository",
	}

	result, err := handler.DeleteInterface(context.Background(), req)
	require.NoError(t, err)
	assert.Contains(t, result, "Deleted BookRepository")

	updated := string(fs.files[ifaceFile])
	assert.NotContains(t, updated, "BookRepository")
	assert.Contains(t, updated, "OtherType")
}

func TestDeleteInterface_NotFound(t *testing.T) {
	tmpDir := t.TempDir()
	ifaceFile := filepath.Join(tmpDir, "repo.go")

	fs := &mockFS{
		files: map[string][]byte{
			ifaceFile: []byte("package repo\n"),
		},
	}
	handler := commands.NewExecutePlanHandler(fs)

	req := domain.InterfaceActionRequest{
		FilePath:   ifaceFile,
		Identifier: "NonExistent",
	}

	_, err := handler.DeleteInterface(context.Background(), req)
	require.Error(t, err)
}
