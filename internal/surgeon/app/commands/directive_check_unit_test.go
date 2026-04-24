package commands

import (
	"testing"

	"github.com/JLugagne/go-surgeon/internal/surgeon/domain"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDirective_CheckDirectivesIntact_CleanSource_ReturnsNil(t *testing.T) {
	src := []byte(`package p

import "embed"

//go:embed *.sql
var migrationsFS embed.FS

//go:generate stringer -type=Foo
type Foo int
`)
	err := checkDirectivesIntact(src, "f.go")
	assert.NoError(t, err)
}

func TestDirective_CheckDirectivesIntact_DetachedEmbed_ReturnsError(t *testing.T) {
	src := []byte(`package p

import "embed"

//go:embed *.sql

var migrationsFS embed.FS
`)
	err := checkDirectivesIntact(src, "f.go")
	require.Error(t, err)
	var domErr *domain.Error
	require.ErrorAs(t, err, &domErr)
	assert.Equal(t, "PATCH_BREAKS_DIRECTIVE", domErr.Code)
}
