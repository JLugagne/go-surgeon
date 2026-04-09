package domain

type GraphPackage struct {
	Path    string
	Files   []GraphFile
	Summary string
	Deps    []string
}

// GraphFile represents a Go source file with its exported symbols.
type GraphFile struct {
	Path    string
	Symbols []string
}

// GraphOptions configures how the graph query walks and presents packages.
type GraphOptions struct {
	Dir         string
	Symbols     bool
	Summary     bool
	Deps        bool
	Recursive   bool
	Tests       bool
	Depth       int      // max directory depth relative to Dir (0 = unlimited)
	Focus       string   // package path for full detail; others show path only
	Exclude     []string // glob patterns to skip during walk
	TokenBudget int      // approximate max tokens in output (0 = unlimited)
}
