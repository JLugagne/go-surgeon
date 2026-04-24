package domain

// PatchOp is the operation type for a function body patch.
type PatchOp string

const (
	PatchOpReplace      PatchOp = "replace"
	PatchOpInsertBefore PatchOp = "insert_before"
	PatchOpInsertAfter  PatchOp = "insert_after"
	PatchOpDelete       PatchOp = "delete"
	PatchOpWrap         PatchOp = "wrap"
	// PatchOpSetSignature rewrites only the parameter list and/or result list
	// of the targeted function or method. The body and the name (plus any
	// generic type parameters) are left intact.
	PatchOpSetSignature PatchOp = "set_signature"
)

// FunctionPatch describes one scoped edit inside a function body.
type FunctionPatch struct {
	Op         PatchOp
	Match      string // literal text; whitespace-normalized comparison
	MatchRegex string // regex alternative to Match; mutually exclusive
	Occurrence int    // 1-based; 0 means the match must be unique
	Replace    string // for replace: the replacement text
	Code       string // for insert_before / insert_after: the line to insert
	Wrap       string // for wrap: template with %s as the matched text
	// AtLine targets a specific line inside the function body (1-based, relative to the function, matching the line numbers shown by symbol body=true). When FromLine/ToLine are also set, AtLine is ignored. Mutually exclusive with Match/MatchRegex.
	AtLine int
	// FromLine is the first line of a line range (1-based, inclusive). Mutually exclusive with Match/MatchRegex.
	FromLine int
	// ToLine is the last line of a line range (1-based, inclusive). Must be >= FromLine.
	ToLine int
	// Params is the new parameter list for set_signature, including the surrounding parens, e.g. "(ctx context.Context, x int)". Empty means keep the existing params.
	Params string
	// Returns is the new results list for set_signature, e.g. "error" or "([]byte, error)". Empty means keep the existing returns.
	Returns string
}

// PatchFunctionRequest is the input to PatchFunction.
type PatchFunctionRequest struct {
	FilePath   string
	Identifier string
	Patches    []FunctionPatch
	Preview    bool // if true, return diff without writing
	// IncludeNested, when true, restores legacy behavior where text matches inside nested closures (*ast.FuncLit) are considered. Default (false) restricts matches to the target function's own body, stopping at every nested FuncLit.
	IncludeNested bool
}

// PatchFunctionResult is the output of PatchFunction.
type PatchFunctionResult struct {
	Diff         string
	Applied      int
	Preview      bool
	AddedImports []string
	// Warnings are non-fatal notes about this patch operation — e.g., "occurrence: N replaced, but the body still contains M more matches at lines X, Y."
	Warnings []string
	// AutoLifts records any insert_before/insert_after patches whose anchor
	// matched inside a nested scope (closure body, if-branch, for-loop,
	// switch case) and was auto-lifted to the enclosing top-level statement.
	AutoLifts []AutoLiftInfo
}

// AutoLiftInfo describes one insertion whose anchor landed in a nested scope
// and was auto-lifted to the outermost enclosing statement in the target
// function body. It is surfaced to the caller so the behaviour is observable
// rather than silent.
type AutoLiftInfo struct {
	PatchIndex int    // 1-based index into PatchFunctionRequest.Patches
	LiftedFrom string // description of the nested scope, e.g. "closure body at L241"
	LiftedTo   string // description of the top-level anchor, e.g. "function body at L225"
	Context    string // ±10 non-blank lines around the insertion with +markers on inserted lines
}

// StructPatchOp is the operation type for a struct declaration patch.
type StructPatchOp string

const (
	StructPatchOpAddField    StructPatchOp = "add_field"
	StructPatchOpRemoveField StructPatchOp = "remove_field"
	StructPatchOpRenameField StructPatchOp = "rename_field"
	StructPatchOpRetypeField StructPatchOp = "retype_field"
	StructPatchOpSetTag      StructPatchOp = "set_tag"
	StructPatchOpSetDoc      StructPatchOp = "set_doc"
)

// StructPatch describes one scoped edit on a struct's field list.
type StructPatch struct {
	Op       StructPatchOp
	Name     string // target field name (for most ops); embed type for embedded fields
	From     string // for rename_field: current field name
	To       string // for rename_field: new field name
	Type     string // for add_field / retype_field
	Tag      string // optional for add_field; required for set_tag
	Doc      string // optional for add_field / set_doc; empty string on set_doc clears the doc
	Before   string // anchor: insert before this field (add_field only)
	After    string // anchor: insert after this field (add_field only)
	Position string // "first" or "last" (add_field only); default is "last"
}

// PatchStructRequest is the input to PatchStruct.
type PatchStructRequest struct {
	FilePath   string
	Identifier string
	Patches    []StructPatch
	Preview    bool
}

// PatchStructResult is the output of PatchStruct.
type PatchStructResult struct {
	Diff         string
	Applied      int
	Preview      bool
	AddedImports []string
	// Warnings are non-fatal notes, mirroring PatchFunctionResult.Warnings for API symmetry. Currently always empty.
	Warnings []string
}

// InterfacePatchOp is the operation type for an interface declaration patch.
type InterfacePatchOp string

const (
	InterfacePatchOpAddMethod    InterfacePatchOp = "add_method"
	InterfacePatchOpRemoveMethod InterfacePatchOp = "remove_method"
	InterfacePatchOpRenameMethod InterfacePatchOp = "rename_method"
	InterfacePatchOpRetypeMethod InterfacePatchOp = "retype_method"
	InterfacePatchOpSetDoc       InterfacePatchOp = "set_doc"
	InterfacePatchOpEmbed        InterfacePatchOp = "embed"
	InterfacePatchOpRemoveEmbed  InterfacePatchOp = "remove_embed"
)

// InterfacePatch describes one scoped edit on an interface's method list.
type InterfacePatch struct {
	Op        InterfacePatchOp
	Name      string // method name (for most ops)
	From      string // for rename_method
	To        string // for rename_method
	Signature string // for add_method / retype_method (e.g. "Read(p []byte) (int, error)")
	Type      string // for embed / remove_embed (e.g. "io.Closer")
	Doc       string // optional for add_method / set_doc
	Before    string // anchor: insert before this method (add_method only)
	After     string // anchor: insert after this method (add_method only)
	Position  string // "first" or "last" (add_method only)
}

// PatchInterfaceRequest is the input to PatchInterface.
type PatchInterfaceRequest struct {
	FilePath   string
	Identifier string
	Patches    []InterfacePatch
	Preview    bool
	MockFile   string // optional: regenerate this mock when the method set changes
	MockName   string // optional: name of the mock struct to regenerate
}

// PatchInterfaceResult is the output of PatchInterface.
type PatchInterfaceResult struct {
	Diff         string
	Applied      int
	Preview      bool
	MockUpdated  bool // true if the mock was regenerated
	AddedImports []string
	// Warnings are non-fatal notes, mirroring PatchFunctionResult.Warnings for API symmetry. Currently always empty.
	Warnings []string
}

// FilePatch describes one whole-file text substitution. Exactly one of Match
// (literal, all occurrences) or MatchRegex (RE2, $1/$2 submatch substitutions
// in Replace) must be set. Patches run in order listed, each seeing the
// result of the previous one.
type FilePatch struct {
	Match      string // literal text; all occurrences are replaced
	MatchRegex string // RE2 alternative to Match; mutually exclusive
	Replace    string // replacement text; supports $1, $2, ... for MatchRegex
	// when true, regexp.QuoteMeta is applied to MatchRegex before compiling. Mutually exclusive with Match.
	MatchLiteral bool
	// 0 = replace all occurrences (default); N = replace only the Nth occurrence (1-based).
	Occurrence int
}

// PatchFileRequest is the input to PatchFile — whole-file text substitution
// with AST safety guarantees (re-parse and reject on syntax break, gofmt).
type PatchFileRequest struct {
	FilePath string
	Patches  []FilePatch
	Preview  bool // if true, return diff without writing
	// Scope restricts where substitutions may apply.
	// Valid values: ""/"all" (default — every occurrence, current behavior),
	// "code_only" (skip matches that fall inside comments or string literals),
	// "identifiers_only" (only match at *ast.Ident boundaries, exact length).
	Scope string
}

// PatchFileResult is the output of PatchFile.
type PatchFileResult struct {
	Diff         string
	Applied      int   // number of patches that ran (= len(req.Patches))
	Hits         []int // per-patch match count (parallel to req.Patches)
	Preview      bool
	AddedImports []string
	// Warnings collects non-fatal notes — e.g. "patch #N: zero matches."
	Warnings []string
}

// PatchDeclRequest is the input to PatchDecl. It targets the VALUE
// expression of a top-level const or var declaration. Patches reuse
// FunctionPatch (same ops, same match/line-based targeting).
type PatchDeclRequest struct {
	FilePath   string
	Identifier string
	Patches    []FunctionPatch
	Preview    bool // if true, return diff without writing
}

// PatchDeclResult is the output of PatchDecl.
type PatchDeclResult struct {
	Diff         string
	Applied      int
	Preview      bool
	AddedImports []string
	// Warnings are non-fatal notes about this patch operation — e.g.,
	// "occurrence: N replaced, but the value still contains M more matches at lines X, Y."
	Warnings []string
}

// PatchStructBulkItem is one (file, identifier, patches) target in a bulk struct patch call.
type PatchStructBulkItem struct {
	FilePath   string
	Identifier string
	Patches    []StructPatch
}

// PatchStructBulkRequest is the input to PatchStructBulk. It groups multiple
// (file, identifier, patches) items into one atomic call: if any item fails,
// no file is modified.
type PatchStructBulkRequest struct {
	Items   []PatchStructBulkItem
	Preview bool
}

// PatchStructBulkResult is the output of PatchStructBulk. Items is parallel to
// the input Items slice; Diff is the combined unified diff of all writes with
// per-item headers.
type PatchStructBulkResult struct {
	Items   []PatchStructResult
	Applied int
	Preview bool
	Diff    string
}

// PatchFunctionBulkItem is one (file, identifier, patches) target in a bulk
// function patch call.
type PatchFunctionBulkItem struct {
	FilePath      string
	Identifier    string
	Patches       []FunctionPatch
	IncludeNested bool
}

// PatchFunctionBulkRequest is the input to PatchFunctionBulk. It groups
// multiple (file, identifier, patches) items into one atomic call: if any item
// fails, no file is modified.
type PatchFunctionBulkRequest struct {
	Items   []PatchFunctionBulkItem
	Preview bool
}

// PatchFunctionBulkResult is the output of PatchFunctionBulk. Items is
// parallel to the input Items slice; Diff is the combined unified diff of all
// writes with per-item headers.
type PatchFunctionBulkResult struct {
	Items   []PatchFunctionResult
	Applied int
	Preview bool
	Diff    string
}
