package domain

// SymbolQuery represents the user's search query for a symbol.
type SymbolQuery struct {
	// Optional: filter by package name
	PackageName string
	// Empty if not a method
	Receiver string
	// Function or Struct name
	Name string
	// Include _test.go files in the search
	Tests bool
	// import path of a dependency to search in instead of the current project
	Module string
	// Optional: regex on declaration names; when set, Name is ignored
	Pattern string
}

// SymbolResult represents the extracted information for a symbol.
type SymbolResult struct {
	File string
	// Package is the package name of the file declaring this symbol.
	Package string
	// Imports lists the import paths of the file declaring this symbol.
	Imports   []string
	LineStart int
	LineEnd   int
	Name      string
	Receiver  string
	Signature string
	Doc       string
	Code      string // Empty lines stripped
}
