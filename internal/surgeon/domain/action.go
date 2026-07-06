package domain

import "errors"

// ActionType defines the type of action to be performed.
type ActionType string

const (
	ActionTypeCreateFile      ActionType = "create_file"
	ActionTypeReplaceFile     ActionType = "replace_file"
	ActionTypeUpdateFunc      ActionType = "update_func"
	ActionTypeAddFunc         ActionType = "add_func"
	ActionTypeAddStruct       ActionType = "add_struct"
	ActionTypeUpdateStruct    ActionType = "update_struct"
	ActionTypeDeleteFunc      ActionType = "delete_func"
	ActionTypeDeleteStruct    ActionType = "delete_struct"
	ActionTypeDeleteFile      ActionType = "delete_file"
	ActionTypeUpdateDecl      ActionType = "update_decl"
	ActionTypeAddInterface    ActionType = "add_interface"
	ActionTypeUpdateInterface ActionType = "update_interface"
	ActionTypeDeleteInterface ActionType = "delete_interface"
	ActionTypeInsertCall      ActionType = "insert_call"
	ActionTypePatchFunction   ActionType = "patch_function"
	ActionTypePatchStruct     ActionType = "patch_struct"
	ActionTypePatchInterface  ActionType = "patch_interface"
	ActionTypePatchFile       ActionType = "patch_file"
	ActionTypePatchDecl       ActionType = "patch_decl"
)

// InsertPosition controls where insert_call places the statement inside the function body.
type InsertPosition string

const (
	// InsertBeforeReturn places the call immediately before the last return statement.
	InsertBeforeReturn InsertPosition = "before-return"
	// InsertEndOfBody places the call at the end of the function body, before the closing brace.
	InsertEndOfBody InsertPosition = "end-of-body"
	// after:<marker> (parsed directly as a string prefix, not a named constant)
	// places the call after the line containing the marker text.
)

// Action represents a single modification to the codebase.
type Action struct {
	Action      ActionType     `yaml:"action"`
	FilePath    string         `yaml:"file"`
	PackagePath string         `yaml:"package"`
	Identifier  string         `yaml:"identifier"`
	Content     string         `yaml:"content"`
	MockFile    string         `yaml:"mock_file"`
	MockName    string         `yaml:"mock_name"`
	Doc         string         `yaml:"doc"`
	StripDoc    bool           `yaml:"strip_doc"`
	Position    InsertPosition `yaml:"position"`
	WithTest    bool           `yaml:"with_test"`
	// PatchFunctionOps carries the scoped patch operations for ActionTypePatchFunction.
	PatchFunctionOps []FunctionPatch `yaml:"patch_function_ops"`
	// PatchStructOps carries the scoped patch operations for ActionTypePatchStruct.
	PatchStructOps []StructPatch `yaml:"patch_struct_ops"`
	// PatchInterfaceOps carries the scoped patch operations for ActionTypePatchInterface.
	PatchInterfaceOps []InterfacePatch `yaml:"patch_interface_ops"`
	// PatchFileOps carries the whole-file substitutions for ActionTypePatchFile.
	PatchFileOps []FilePatch `yaml:"patch_file_ops"`
	// PatchDeclOps carries the scoped patch operations for ActionTypePatchDecl (reuses FunctionPatch shape).
	PatchDeclOps []FunctionPatch `yaml:"patch_decl_ops"`
}

// PlanResult contains the outcome of executing a plan.
type PlanResult struct {
	FilesModified int
	// Files lists the file paths that were written during plan execution.
	Files    []string
	Warnings []string
	// AddedImports lists import paths that goimports added to any file written during plan execution (deduped across actions).
	AddedImports []string
	// Preview reports whether the plan executed in dry-run mode. When true, no files were written.
	Preview bool
	// Diff contains the unified diff of all would-be file changes when Preview is true.
	Diff string
}

// Plan is a collection of actions to be executed.
type Plan struct {
	Actions []Action
	// Preview, when true, requests a dry-run: the plan is applied to an in-memory filesystem and a unified diff is returned instead of writing to disk.
	Preview bool
}

var (
	// ErrEmptyPlan is returned when a plan contains no actions.
	ErrEmptyPlan = errors.New("plan contains no actions")
)
