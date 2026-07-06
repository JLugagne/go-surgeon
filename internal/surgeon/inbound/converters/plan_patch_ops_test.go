package converters_test

import (
	"testing"

	"github.com/JLugagne/go-surgeon/internal/surgeon/domain"
	"github.com/JLugagne/go-surgeon/internal/surgeon/inbound/converters"
)

// TestToDomainPlanYAMLPatchFunctionOps proves a YAML plan carrying a
// patch_function action with patch_function_ops is accepted and mapped to
// domain.Action.PatchFunctionOps. Before the fix, KnownFields(true) rejects
// patch_function_ops as an unknown field.
func TestToDomainPlanYAMLPatchFunctionOps(t *testing.T) {
	yamlPlan := `actions:
  - action: patch_function
    file: internal/foo/bar.go
    identifier: Foo.Bar
    patch_function_ops:
      - op: replace
        match: "x := 1"
        replace: "x := 2"
      - op: set_signature
        params:
          - "ctx context.Context"
          - "n int"
        returns: error
`
	plan, err := converters.ToDomainPlan([]byte(yamlPlan), converters.FormatYAML)
	if err != nil {
		t.Fatalf("ToDomainPlan(YAML) returned error: %v", err)
	}
	if len(plan.Actions) != 1 {
		t.Fatalf("expected 1 action, got %d", len(plan.Actions))
	}
	a := plan.Actions[0]
	if a.Action != domain.ActionTypePatchFunction {
		t.Fatalf("expected action %q, got %q", domain.ActionTypePatchFunction, a.Action)
	}
	if len(a.PatchFunctionOps) != 2 {
		t.Fatalf("expected 2 patch ops, got %d", len(a.PatchFunctionOps))
	}
	if a.PatchFunctionOps[0].Op != domain.PatchOpReplace ||
		a.PatchFunctionOps[0].Match != "x := 1" ||
		a.PatchFunctionOps[0].Replace != "x := 2" {
		t.Fatalf("op[0] mismatch: %+v", a.PatchFunctionOps[0])
	}
	if a.PatchFunctionOps[1].Op != domain.PatchOpSetSignature ||
		a.PatchFunctionOps[1].Params != "(ctx context.Context, n int)" ||
		a.PatchFunctionOps[1].Returns != "error" {
		t.Fatalf("op[1] mismatch: %+v", a.PatchFunctionOps[1])
	}
}

// TestToDomainPlanJSONPatchFunctionOps proves the JSON plan path accepts
// patch_function_ops. Before the fix, DisallowUnknownFields rejects it.
func TestToDomainPlanJSONPatchFunctionOps(t *testing.T) {
	jsonPlan := `{
  "actions": [
    {
      "action": "patch_function",
      "file": "internal/foo/bar.go",
      "identifier": "Foo.Bar",
      "patch_function_ops": [
        {"op": "replace", "match": "x := 1", "replace": "x := 2"},
        {"op": "set_signature", "params": ["ctx context.Context", "n int"], "returns": "error"}
      ]
    }
  ]
}`
	plan, err := converters.ToDomainPlan([]byte(jsonPlan), converters.FormatJSON)
	if err != nil {
		t.Fatalf("ToDomainPlan(JSON) returned error: %v", err)
	}
	if len(plan.Actions) != 1 {
		t.Fatalf("expected 1 action, got %d", len(plan.Actions))
	}
	a := plan.Actions[0]
	if len(a.PatchFunctionOps) != 2 {
		t.Fatalf("expected 2 patch ops, got %d", len(a.PatchFunctionOps))
	}
	if a.PatchFunctionOps[1].Params != "(ctx context.Context, n int)" {
		t.Fatalf("expected joined params, got %q", a.PatchFunctionOps[1].Params)
	}
}

// TestToDomainPlanYAMLPatchStructAndFileOps covers the other patch action
// families to ensure their ops fields are wired through the DTO as well.
func TestToDomainPlanYAMLPatchStructAndFileOps(t *testing.T) {
	yamlPlan := `actions:
  - action: patch_struct
    file: internal/foo/bar.go
    identifier: Config
    patch_struct_ops:
      - op: add_field
        name: Timeout
        type: time.Duration
        tag: 'json:"timeout"'
  - action: patch_file
    file: internal/foo/bar.go
    patch_file_ops:
      - match: oldName
        replace: newName
  - action: patch_interface
    file: internal/foo/bar.go
    identifier: Store
    patch_interface_ops:
      - op: add_method
        signature: Close() error
  - action: patch_decl
    file: internal/foo/bar.go
    identifier: defaults
    patch_decl_ops:
      - op: replace
        match: "1"
        replace: "2"
`
	plan, err := converters.ToDomainPlan([]byte(yamlPlan), converters.FormatYAML)
	if err != nil {
		t.Fatalf("ToDomainPlan(YAML) returned error: %v", err)
	}
	if len(plan.Actions) != 4 {
		t.Fatalf("expected 4 actions, got %d", len(plan.Actions))
	}
	if len(plan.Actions[0].PatchStructOps) != 1 ||
		plan.Actions[0].PatchStructOps[0].Op != domain.StructPatchOpAddField ||
		plan.Actions[0].PatchStructOps[0].Type != "time.Duration" {
		t.Fatalf("patch_struct_ops mismatch: %+v", plan.Actions[0].PatchStructOps)
	}
	if len(plan.Actions[1].PatchFileOps) != 1 ||
		plan.Actions[1].PatchFileOps[0].Match != "oldName" ||
		plan.Actions[1].PatchFileOps[0].Replace != "newName" {
		t.Fatalf("patch_file_ops mismatch: %+v", plan.Actions[1].PatchFileOps)
	}
	if len(plan.Actions[2].PatchInterfaceOps) != 1 ||
		plan.Actions[2].PatchInterfaceOps[0].Signature != "Close() error" {
		t.Fatalf("patch_interface_ops mismatch: %+v", plan.Actions[2].PatchInterfaceOps)
	}
	if len(plan.Actions[3].PatchDeclOps) != 1 ||
		plan.Actions[3].PatchDeclOps[0].Replace != "2" {
		t.Fatalf("patch_decl_ops mismatch: %+v", plan.Actions[3].PatchDeclOps)
	}
}
