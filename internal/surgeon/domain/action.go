package domain

import (
	"errors"
)

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
	ActionTypeAddInterface    ActionType = "add_interface"
	ActionTypeUpdateInterface ActionType = "update_interface"
	ActionTypeDeleteInterface ActionType = "delete_interface"
)

// Action represents a single modification to the codebase.
type Action struct {
	Action      ActionType `yaml:"action"`
	FilePath    string     `yaml:"file"`
	PackagePath string     `yaml:"package"`
	Identifier  string     `yaml:"identifier"`
	Content     string     `yaml:"content"`
	MockFile    string     `yaml:"mock_file"`
	MockName    string     `yaml:"mock_name"`
	Doc         string     `yaml:"doc"`
	StripDoc    bool       `yaml:"strip_doc"`
}

// PlanResult contains the outcome of executing a plan.
type PlanResult struct {
	FilesModified int
	Warnings      []string
}

// Plan is a collection of actions to be executed.
type Plan struct {
	Actions []Action
}

var (
	// ErrEmptyPlan is returned when a plan contains no actions.
	ErrEmptyPlan = errors.New("plan contains no actions")
)
