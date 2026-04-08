package converters

import (
	"strings"
	"testing"
)

func TestToDomainPlan_ValidYAML(t *testing.T) {
	yaml := `
actions:
  - action: update_func
    file: main.go
    identifier: Book.Validate
    content: |
      func (b *Book) Validate() error {
          return nil
      }
`
	plan, err := ToDomainPlan([]byte(yaml))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(plan.Actions) != 1 {
		t.Fatalf("expected 1 action, got %d", len(plan.Actions))
	}
	a := plan.Actions[0]
	if a.Action != "update_func" {
		t.Errorf("expected action update_func, got %s", a.Action)
	}
	if a.FilePath != "main.go" {
		t.Errorf("expected file main.go, got %s", a.FilePath)
	}
	if a.Identifier != "Book.Validate" {
		t.Errorf("expected identifier Book.Validate, got %s", a.Identifier)
	}
}

func TestToDomainPlan_UnknownTopLevelField(t *testing.T) {
	yaml := `
steps:
  - action: update_func
    file: main.go
    identifier: Foo
    content: "func Foo() {}"
`
	_, err := ToDomainPlan([]byte(yaml))
	if err == nil {
		t.Fatal("expected error for unknown top-level field 'steps', got nil")
	}
	if !strings.Contains(err.Error(), "steps") {
		t.Errorf("error should mention 'steps', got: %v", err)
	}
}

func TestToDomainPlan_UnknownActionField_Symbol(t *testing.T) {
	yaml := `
actions:
  - action: update_func
    file: main.go
    symbol: Book.Validate
    content: |
      func (b *Book) Validate() error { return nil }
`
	_, err := ToDomainPlan([]byte(yaml))
	if err == nil {
		t.Fatal("expected error for unknown field 'symbol' (typo for 'identifier'), got nil")
	}
	if !strings.Contains(err.Error(), "symbol") {
		t.Errorf("error should mention 'symbol', got: %v", err)
	}
}

func TestToDomainPlan_UnknownActionField_Body(t *testing.T) {
	yaml := `
actions:
  - action: update_func
    file: main.go
    identifier: Book.Validate
    body: |
      func (b *Book) Validate() error { return nil }
`
	_, err := ToDomainPlan([]byte(yaml))
	if err == nil {
		t.Fatal("expected error for unknown field 'body' (typo for 'content'), got nil")
	}
	if !strings.Contains(err.Error(), "body") {
		t.Errorf("error should mention 'body', got: %v", err)
	}
}

func TestToDomainPlan_MultipleUnknownFields(t *testing.T) {
	yaml := `
actions:
  - action: update_func
    file: main.go
    symbol: Book.Validate
    body: |
      func (b *Book) Validate() error { return nil }
`
	_, err := ToDomainPlan([]byte(yaml))
	if err == nil {
		t.Fatal("expected error for unknown fields, got nil")
	}
}

func TestToDomainPlan_AllValidFields(t *testing.T) {
	yaml := `
actions:
  - action: update_func
    file: main.go
    package: example.com/pkg
    identifier: Foo
    content: "func Foo() {}"
    mock_file: mock_foo.go
    mock_name: MockFoo
`
	plan, err := ToDomainPlan([]byte(yaml))
	if err != nil {
		t.Fatalf("unexpected error with all valid fields: %v", err)
	}
	a := plan.Actions[0]
	if a.PackagePath != "example.com/pkg" {
		t.Errorf("expected package example.com/pkg, got %s", a.PackagePath)
	}
	if a.MockFile != "mock_foo.go" {
		t.Errorf("expected mock_file mock_foo.go, got %s", a.MockFile)
	}
	if a.MockName != "MockFoo" {
		t.Errorf("expected mock_name MockFoo, got %s", a.MockName)
	}
}
