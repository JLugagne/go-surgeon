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
	// Context controls how much ambient information surrounds the matched symbol.
	// "" (default): just the symbol, its package, and imports.
	// "file": additionally populate FileOutline with sibling declaration signatures, so the agent sees the whole file's structure in one call.
	Context string
	// AtLine, when > 0, resolves the outermost named declaration that spans
	// that line in File. Mutually exclusive with Name/Pattern/Receiver.
	File   string
	AtLine int
}

// SymbolResult represents the extracted information for a symbol.
type SymbolResult struct {
	File string
	// Package is the package name of the file declaring this symbol.
	Package string
	// Imports lists the import paths of the file declaring this symbol.
	Imports []string
	// FileOutline, when populated (Context="file"), lists signatures of every top-level declaration in the same file, including the matched symbol. Bodies are omitted to keep it compact.
	FileOutline []OutlineEntry
	LineStart   int
	LineEnd     int
	Name        string
	Receiver    string
	Signature   string
	Doc         string
	Code        string // Empty lines stripped
}

// OutlineEntry is one declaration's summary inside FileOutline: what
// kind of declaration it is, its name, its signature (one line), and
// the line range it spans in the file. Bodies are intentionally
// excluded so the outline stays compact.
type OutlineEntry struct {
	Kind      string // "func", "method", "type", "var", "const"
	Name      string
	Receiver  string // empty for non-methods
	Signature string
	LineStart int
	LineEnd   int
}
