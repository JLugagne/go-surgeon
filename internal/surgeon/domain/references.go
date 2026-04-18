package domain

// SymbolRef uniquely identifies a symbol we want to look up. At least
// Name is required; Receiver disambiguates methods, Package disambiguates
// same-named symbols across packages, and File+Line together can pin
// down an exact declaration when the name alone is ambiguous.
type SymbolRef struct {
	// Name is the identifier of the symbol (function, method, type,
	// var, const, or struct field).
	Name string
	// Receiver is the bare receiver type name for methods ("", for
	// non-methods). Pointer stars are stripped: use "BookHandler"
	// even if the method is defined on "*BookHandler".
	Receiver string
	// Package is the package path (e.g. "github.com/foo/bar/baz") or
	// package name ("baz"); used to disambiguate when the same Name
	// exists in multiple packages. Empty matches any package.
	Package string
	// File pins the lookup to a specific file (relative or absolute
	// path). When set together with Line, it uniquely identifies one
	// declaration even in the face of shadowing or overloading.
	File string
	// Line is the 1-based line number of the declaration. Use together
	// with File. Zero means "not specified".
	Line int
}

// ReferencesQuery describes a references/definition lookup.
type ReferencesQuery struct {
	// Symbol is what we're searching for.
	Symbol SymbolRef
	// Dir is the directory to load packages from (defaults to ".").
	// The loader walks "./..." from this directory.
	Dir string
	// IncludeDefinition, when true, adds the defining site to
	// Result.Definition even for a "find references" call so callers
	// get both in one hop.
	IncludeDefinition bool
	// Tests, when true, also loads _test.go files.
	Tests bool
}

// Location is a file:line:column position inside a Go source file,
// optionally pointing to the exact byte range of the identifier so
// callers can rewrite it in place.
type Location struct {
	File   string
	Line   int
	Column int
	// Offset/EndOffset are byte offsets of the identifier token
	// (matching token.Position.Offset semantics). EndOffset == Offset
	// + len(name).
	Offset    int
	EndOffset int
	// LineText is the source line containing the reference, trimmed of
	// trailing newline, for human-readable output.
	LineText string
}

// ReferencesResult bundles the symbol's definition with every reference
// the loader was able to attribute to it across the loaded packages.
type ReferencesResult struct {
	// Symbol is the resolved symbol: Name is always populated;
	// Receiver/Package are filled from types.Object when available.
	Symbol SymbolRef
	// Kind is a short classification: "func", "method", "type",
	// "var", "const", or "field".
	Kind string
	// Definition points at the declaration site.
	Definition Location
	// References are every use site (excludes the declaration itself).
	References []Location
}

// RenameRequest renames Symbol to NewName across the loaded packages.
// A successful rename rewrites every file that contains a reference (or
// the definition) and returns the list of touched files.
type RenameRequest struct {
	// Symbol is the symbol to rename.
	Symbol SymbolRef
	// NewName is the replacement identifier. Must be a valid Go
	// identifier and different from Symbol.Name.
	NewName string
	// Dir is the directory to load packages from (defaults to ".").
	Dir string
	// Tests, when true, also rewrites _test.go files.
	Tests bool
	// DryRun, when true, computes the rename without writing files;
	// Result.Locations still lists what would have been changed.
	DryRun bool
	// AllowExportChange, when true, permits renames that flip the
	// identifier's export status (e.g. foo → Foo or Foo → foo). By
	// default such renames are rejected because they almost always
	// break callers; enabling this escape hatch asks the rename to
	// proceed anyway. The result will carry a warning describing the
	// flip so agents can notice.
	AllowExportChange bool
}

// RenameResult reports the outcome of a rename.
type RenameResult struct {
	// OldName / NewName echo the request for the caller's benefit.
	OldName string
	NewName string
	// Kind is the classification of the renamed symbol ("func",
	// "method", "type", "var", "const", "field").
	Kind string
	// FilesModified lists each file whose contents were rewritten,
	// in deterministic order (sorted by path).
	FilesModified []string
	// Locations lists every rewritten identifier across the touched
	// files (definition + all references), sorted by file/line.
	Locations []Location
	// DryRun echoes whether the caller asked for a preview.
	DryRun bool
	// Warnings carries non-fatal notices about the rename (for example
	// a message when the rename flipped export status under
	// AllowExportChange). Empty when nothing unusual happened.
	Warnings []string
}
