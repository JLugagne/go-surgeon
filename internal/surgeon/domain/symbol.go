package domain

// SymbolQuery represents the user's search query for a symbol.
type SymbolQuery struct {
	PackageName string // Optional: filter by package name
	Receiver    string // Empty if not a method
	Name        string // Function or Struct name
	Tests       bool   // Include _test.go files in the search
	Module      string // import path of a dependency to search in instead of the current project
}

// SymbolResult represents the extracted information for a symbol.
type SymbolResult struct {
	File        string
	LineStart   int
	LineEnd     int
	Name        string
	Receiver    string
	Signature   string
	Doc         string
	Code        string // Empty lines stripped
}
