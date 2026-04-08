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
