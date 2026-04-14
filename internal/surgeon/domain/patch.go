package domain

// PatchOp is the operation type for a function body patch.
type PatchOp string

const (
	PatchOpReplace      PatchOp = "replace"
	PatchOpInsertBefore PatchOp = "insert_before"
	PatchOpInsertAfter  PatchOp = "insert_after"
	PatchOpDelete       PatchOp = "delete"
	PatchOpWrap         PatchOp = "wrap"
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
}

// PatchFunctionRequest is the input to PatchFunction.
type PatchFunctionRequest struct {
	FilePath   string
	Identifier string
	Patches    []FunctionPatch
	Preview    bool // if true, return diff without writing
}

// PatchFunctionResult is the output of PatchFunction.
type PatchFunctionResult struct {
	Diff    string
	Applied int
	Preview bool
}
