package domain

import (
	"errors"
)

// ActionType defines the type of action to be performed.
type ActionType string

const (
	ActionTypeCreateFile   ActionType = "create_file"
	ActionTypeReplaceFile  ActionType = "replace_file"
	ActionTypeUpdateFunc   ActionType = "update_func"
	ActionTypeAddFunc      ActionType = "add_func"
	ActionTypeAddStruct    ActionType = "add_struct"
	ActionTypeUpdateStruct ActionType = "update_struct"
	ActionTypeDeleteFunc   ActionType = "delete_func"
	ActionTypeDeleteStruct ActionType = "delete_struct"
)

// Manifest defines the structure of the scaffolding manifest.
type Manifest struct {
	Commands []Command `yaml:"commands"`
}

// Command represents a single scaffolding command.
type Command struct {
	Name        string
	Description string
	Parameters  []Parameter
	Files       []FileTemplate
}

// Parameter represents a parameter for a scaffolding command.
type Parameter struct {
	Name        string
	Description string
}

// FileTemplate represents a template for a file to be scaffolded.
type FileTemplate struct {
	Path     string
	Template string
}

// Action represents a single modification to the codebase.
type Action struct {
	Action      ActionType
	FilePath    string
	PackagePath string
	Identifier  string
	Content     string
}

// Plan is a collection of actions to be executed.
type Plan struct {
	Actions []Action
}

// MaxActions is the maximum number of actions allowed in a single plan.
const MaxActions = 5

var (
	// ErrPlanTooLarge is returned when a plan contains more than MaxActions.
	ErrPlanTooLarge = errors.New("plan contains too many actions")
	// ErrEmptyPlan is returned when a plan contains no actions.
	ErrEmptyPlan = errors.New("plan contains no actions")
)
