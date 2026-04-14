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
	plan, err := ToDomainPlan([]byte(yaml), FormatAuto)
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
	_, err := ToDomainPlan([]byte(yaml), FormatAuto)
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
	_, err := ToDomainPlan([]byte(yaml), FormatAuto)
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
	_, err := ToDomainPlan([]byte(yaml), FormatAuto)
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
	_, err := ToDomainPlan([]byte(yaml), FormatAuto)
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
	plan, err := ToDomainPlan([]byte(yaml), FormatAuto)
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

func TestToDomainPlan_DocFields(t *testing.T) {
	t.Run("doc field is parsed", func(t *testing.T) {
		yaml := `
actions:
  - action: update_func
    file: main.go
    identifier: Foo
    content: "func Foo() {}"
    doc: "Foo does something."
`
		plan, err := ToDomainPlan([]byte(yaml), FormatAuto)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		a := plan.Actions[0]
		if a.Doc != "Foo does something." {
			t.Errorf("expected doc 'Foo does something.', got %q", a.Doc)
		}
		if a.StripDoc {
			t.Error("expected strip_doc to be false")
		}
	})

	t.Run("strip_doc field is parsed", func(t *testing.T) {
		yaml := `
actions:
  - action: update_struct
    file: main.go
    identifier: Bar
    content: "type Bar struct{}"
    strip_doc: true
`
		plan, err := ToDomainPlan([]byte(yaml), FormatAuto)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		a := plan.Actions[0]
		if !a.StripDoc {
			t.Error("expected strip_doc to be true")
		}
		if a.Doc != "" {
			t.Errorf("expected empty doc, got %q", a.Doc)
		}
	})
}

func TestToDomainPlan_JSON(t *testing.T) {
	j := `{"actions":[{"action":"update_func","file":"main.go","identifier":"Foo","content":"func Foo() {}"}]}`
	plan, err := ToDomainPlan([]byte(j), FormatAuto)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(plan.Actions) != 1 || plan.Actions[0].Identifier != "Foo" {
		t.Fatalf("unexpected plan: %+v", plan)
	}
}

func TestToDomainPlan_JSONAutoDetectWithLeadingWhitespace(t *testing.T) {
	j := "   \n  {\"actions\":[{\"action\":\"update_func\",\"file\":\"m.go\",\"identifier\":\"F\",\"content\":\"func F(){}\"}]}"
	if _, err := ToDomainPlan([]byte(j), FormatAuto); err != nil {
		t.Fatalf("expected auto-detect to succeed on whitespace-prefixed JSON, got: %v", err)
	}
}

func TestToDomainPlan_JSONUnknownField(t *testing.T) {
	j := `{"actions":[{"action":"update_func","file":"m.go","symbol":"Foo","content":"func Foo(){}"}]}`
	_, err := ToDomainPlan([]byte(j), FormatAuto)
	if err == nil {
		t.Fatal("expected error for unknown JSON field 'symbol'")
	}
}

func TestToDomainPlan_ForceFormat(t *testing.T) {
	j := `{"actions":[{"action":"update_func","file":"m.go","identifier":"F","content":"func F(){}"}]}`
	if _, err := ToDomainPlan([]byte(j), FormatJSON); err != nil {
		t.Fatalf("FormatJSON should parse JSON: %v", err)
	}
	y := "actions:\n  - action: update_func\n    file: m.go\n    identifier: F\n    content: \"func F(){}\"\n"
	if _, err := ToDomainPlan([]byte(y), FormatYAML); err != nil {
		t.Fatalf("FormatYAML should parse YAML: %v", err)
	}

}

func TestToDomainPlan_UnknownFormat(t *testing.T) {
	_, err := ToDomainPlan([]byte("{}"), "toml")
	if err == nil {
		t.Fatal("expected error for unknown format")
	}
}

func TestToDomainPlan_FlexBoolJSON(t *testing.T) {
	cases := []struct {
		name     string
		body     string
		strip    bool
		withTest bool
	}{
		{"native true", `{"actions":[{"action":"update_struct","file":"m.go","identifier":"B","content":"type B struct{}","strip_doc":true,"with_test":true}]}`, true, true},
		{"string true", `{"actions":[{"action":"update_struct","file":"m.go","identifier":"B","content":"type B struct{}","strip_doc":"true","with_test":"true"}]}`, true, true},
		{"string false", `{"actions":[{"action":"update_struct","file":"m.go","identifier":"B","content":"type B struct{}","strip_doc":"false","with_test":"false"}]}`, false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			plan, err := ToDomainPlan([]byte(tc.body), FormatAuto)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			a := plan.Actions[0]
			if a.StripDoc != tc.strip {
				t.Errorf("strip_doc: want %v, got %v", tc.strip, a.StripDoc)
			}
			if a.WithTest != tc.withTest {
				t.Errorf("with_test: want %v, got %v", tc.withTest, a.WithTest)
			}
		})
	}
}

func TestToDomainPlan_FlexBoolYAML(t *testing.T) {
	y := "actions:\n  - action: update_struct\n    file: m.go\n    identifier: B\n    content: \"type B struct{}\"\n    strip_doc: \"true\"\n    with_test: \"false\"\n"
	plan, err := ToDomainPlan([]byte(y), FormatAuto)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	a := plan.Actions[0]
	if !a.StripDoc || a.WithTest {
		t.Errorf("expected strip_doc=true with_test=false, got %+v", a)
	}
}

func TestToDomainPlan_FlexBoolInvalid(t *testing.T) {
	j := `{"actions":[{"action":"update_struct","file":"m.go","identifier":"B","content":"type B struct{}","strip_doc":"maybe"}]}`
	if _, err := ToDomainPlan([]byte(j), FormatAuto); err == nil {
		t.Fatal("expected error for non-bool string")
	}
}
