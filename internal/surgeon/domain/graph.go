package domain

// GraphPackage represents a Go package found in the project.
type GraphPackage struct {
	Path  string
	Files []GraphFile
}

// GraphFile represents a Go source file with its exported symbols.
type GraphFile struct {
	Path    string
	Symbols []string
}
