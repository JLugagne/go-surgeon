package commands_test

import (
	"context"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/JLugagne/go-surgeon/internal/surgeon/app/commands"
	"github.com/JLugagne/go-surgeon/internal/surgeon/domain"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// racyFS is a thread-safe in-memory filesystem that records the maximum
// number of overlapping operations targeting the same path. Reads and
// writes sleep briefly to widen the read-modify-write window, so an
// implementation that does not serialize same-file writes reliably
// interleaves and loses edits.
type racyFS struct {
	mu        sync.Mutex
	files     map[string][]byte
	active    map[string]int
	maxActive map[string]int
	delay     time.Duration
}

func newRacyFS(files map[string][]byte) *racyFS {
	return &racyFS{
		files:     files,
		active:    map[string]int{},
		maxActive: map[string]int{},
		delay:     3 * time.Millisecond,
	}
}

func (f *racyFS) enter(path string) {
	f.mu.Lock()
	f.active[path]++
	if f.active[path] > f.maxActive[path] {
		f.maxActive[path] = f.active[path]
	}
	f.mu.Unlock()
}

func (f *racyFS) leave(path string) {
	f.mu.Lock()
	f.active[path]--
	f.mu.Unlock()
}

func (f *racyFS) ReadFile(_ context.Context, path string) ([]byte, error) {
	f.enter(path)
	defer f.leave(path)
	time.Sleep(f.delay)
	f.mu.Lock()
	defer f.mu.Unlock()
	c, ok := f.files[path]
	if !ok {
		return nil, os.ErrNotExist
	}
	out := make([]byte, len(c))
	copy(out, c)
	return out, nil
}

func (f *racyFS) WriteFile(_ context.Context, path string, content []byte) ([]string, error) {
	f.enter(path)
	defer f.leave(path)
	time.Sleep(f.delay)
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]byte, len(content))
	copy(out, content)
	f.files[path] = out
	return nil, nil
}

func (f *racyFS) ReadDir(_ context.Context, _ string) ([]string, error) { return nil, nil }
func (f *racyFS) IsDir(_ context.Context, _ string) (bool, error)       { return false, nil }
func (f *racyFS) MkdirAll(_ context.Context, _ string) error            { return nil }
func (f *racyFS) DeleteFile(_ context.Context, _ string) error          { return nil }

func (f *racyFS) content(path string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return string(f.files[path])
}

func (f *racyFS) maxConcurrent(path string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.maxActive[path]
}

// TestConcurrentSameFileWrites_LoseNoEdits reproduces issue #33: several
// write operations target one file at once and every call reports success
// while last-writer-wins silently drops edits.
func TestConcurrentSameFileWrites_LoseNoEdits(t *testing.T) {
	const n = 6
	path := "/virtual/concurrent.go"

	var b strings.Builder
	b.WriteString("package p\n\n")
	for i := 0; i < n; i++ {
		b.WriteString("func f")
		b.WriteString(strconv.Itoa(i))
		b.WriteString("() int { return ")
		b.WriteString(strconv.Itoa(i))
		b.WriteString(" }\n")
	}
	fs := newRacyFS(map[string][]byte{path: []byte(b.String())})
	handler := commands.NewExecutePlanHandler(fs)

	start := make(chan struct{})
	var wg sync.WaitGroup
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			_, err := handler.PatchFunction(context.Background(), domain.PatchFunctionRequest{
				FilePath:   path,
				Identifier: "f" + strconv.Itoa(i),
				Patches: []domain.FunctionPatch{{
					Op:      domain.PatchOpReplace,
					Match:   "return " + strconv.Itoa(i),
					Replace: "return " + strconv.Itoa(i*100),
				}},
			})
			errs[i] = err
		}(i)
	}
	close(start)
	wg.Wait()

	for i := 0; i < n; i++ {
		require.NoErrorf(t, errs[i], "patch for f%d failed", i)
	}
	final := fs.content(path)
	for i := 0; i < n; i++ {
		assert.Containsf(t, final, "return "+strconv.Itoa(i*100), "edit for f%d was lost", i)
	}
	assert.Equalf(t, 1, fs.maxConcurrent(path), "writes to the same file must be serialized")
}
