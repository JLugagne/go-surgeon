package domain

// SymbolQuery represents the user's search query for a symbol.
type SymbolQuery struct {
	Receiver string // Empty if not a method
	Name     string // Function or Struct name
	Tests    bool   // Include _test.go files in the search
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
