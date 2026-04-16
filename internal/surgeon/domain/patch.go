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
	// AtLine targets a specific line inside the function body (1-based, relative to the function, matching the line numbers shown by symbol body=true). When FromLine/ToLine are also set, AtLine is ignored. Mutually exclusive with Match/MatchRegex.
	AtLine int
	// FromLine is the first line of a line range (1-based, inclusive). Mutually exclusive with Match/MatchRegex.
	FromLine int
	// ToLine is the last line of a line range (1-based, inclusive). Must be >= FromLine.
	ToLine int
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
	Diff         string
	Applied      int
	Preview      bool
	AddedImports []string
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
}
