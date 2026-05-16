package commands

import (
	"strings"
	"testing"
)

// TestValidateNoDroppedDecls_SingleLineSkipped: single-line replacements are
// out of scope for the dropped-content check; they cannot drop a whole decl.
func TestValidateNoDroppedDecls_SingleLineSkipped(t *testing.T) {
	src := []byte("package p\nfunc F() {}\n")
	if err := validateNoDroppedDecls("f.go", "func G() {}", src); err != nil {
		t.Fatalf("single-line replacement must be skipped, got: %v", err)
	}
}

// TestValidateNoDroppedDecls_HappyPath: the replacement's decl is present in
// the post-source — no error.
func TestValidateNoDroppedDecls_HappyPath(t *testing.T) {
	postSrc := []byte("package p\n\ntype KMS = crypto.KMS\n")
	repl := "type KMS = crypto.KMS\n// trailing\n"
	if err := validateNoDroppedDecls("f.go", repl, postSrc); err != nil {
		t.Fatalf("expected nil, got: %v", err)
	}
}

// TestValidateNoDroppedDecls_MissingDeclErrors: the replacement claims to
// introduce two functions but only one made it into the post-source. The
// validator must refuse with a PATCH_DROPPED_CONTENT error that names the
// missing decl.
func TestValidateNoDroppedDecls_MissingDeclErrors(t *testing.T) {
	postSrc := []byte("package p\n\nfunc Kept() {}\n")
	repl := "func Kept() {}\nfunc Dropped() {}\n"
	err := validateNoDroppedDecls("f.go", repl, postSrc)
	if err == nil {
		t.Fatal("expected a PATCH_DROPPED_CONTENT error, got nil")
	}
	msg := err.Error()
	if !strings.Contains(msg, "PATCH_DROPPED_CONTENT") {
		t.Errorf("expected error code PATCH_DROPPED_CONTENT in: %s", msg)
	}
	if !strings.Contains(msg, "Dropped") {
		t.Errorf("expected error to name the missing decl 'Dropped', got: %s", msg)
	}
	if !strings.Contains(msg, "rolled back") {
		t.Errorf("expected error to mention rollback semantics, got: %s", msg)
	}
	if !strings.Contains(msg, "update object=file") {
		t.Errorf("expected error to suggest update object=file, got: %s", msg)
	}
}

// TestValidateNoDroppedDecls_NonParseableReplacementSkipped: when the
// replacement does not parse as Go top-level decls (e.g. just a comment or
// raw text), the check is skipped — fall back to the substring validator.
func TestValidateNoDroppedDecls_NonParseableReplacementSkipped(t *testing.T) {
	postSrc := []byte("package p\n")
	// Two raw lines that don't parse as decls — skip silently.
	if err := validateNoDroppedDecls("f.go", "// hello\n// world", postSrc); err != nil {
		t.Fatalf("non-parseable replacement must be skipped, got: %v", err)
	}
}

// TestValidateNoDroppedDecls_NonParseablePostSrcSkipped: the post-source is
// itself broken — defer to the standard go/parser validator to surface the
// error rather than masking it with a dropped-content failure.
func TestValidateNoDroppedDecls_NonParseablePostSrcSkipped(t *testing.T) {
	postSrc := []byte("package p\nfunc F() { // unclosed\n")
	repl := "func A() {}\nfunc B() {}\n"
	if err := validateNoDroppedDecls("f.go", repl, postSrc); err != nil {
		t.Fatalf("non-parseable post-source must be skipped, got: %v", err)
	}
}

// TestValidateNoDroppedDecls_ReplacementWithoutDeclsSkipped: when the
// replacement parses but contributes no named top-level decl (e.g. it is
// purely an import block), skip — there is nothing to check.
func TestValidateNoDroppedDecls_ReplacementWithoutDeclsSkipped(t *testing.T) {
	postSrc := []byte("package p\n")
	repl := "import \"fmt\"\n_ = fmt.Sprintf"
	if err := validateNoDroppedDecls("f.go", repl, postSrc); err != nil {
		t.Fatalf("replacement without named decls must be skipped, got: %v", err)
	}
}

// TestValidateNoDroppedDecls_TypeAliasPresent: the canonical issue #14 case
// — the user collapses a 9-line interface into a 2-line type alias and the
// alias does land. The validator must NOT fire when the decl is present.
func TestValidateNoDroppedDecls_TypeAliasPresent(t *testing.T) {
	postSrc := []byte(`package p

type KMS = crypto.KMS

func Use(k KMS) {}
`)
	repl := "type KMS = crypto.KMS\n"
	if err := validateNoDroppedDecls("f.go", repl, postSrc); err != nil {
		t.Fatalf("expected nil when alias landed, got: %v", err)
	}
}

// TestValidateNoDroppedDecls_TypeAliasMissing: the user collapses a 9-line
// interface into a 2-line type alias but the splice silently dropped the
// alias from the result. The validator must fire.
func TestValidateNoDroppedDecls_TypeAliasMissing(t *testing.T) {
	postSrc := []byte(`package p

func Use() {}
`)
	repl := "type KMS = crypto.KMS\n// alias for compat\n"
	err := validateNoDroppedDecls("f.go", repl, postSrc)
	if err == nil {
		t.Fatal("expected error when alias is missing from post-source")
	}
	if !strings.Contains(err.Error(), "KMS") {
		t.Errorf("expected error to name missing 'KMS', got: %s", err.Error())
	}
}

// TestValidateNoDroppedStmts_SingleLineSkipped: single-line replacements are
// out of scope.
func TestValidateNoDroppedStmts_SingleLineSkipped(t *testing.T) {
	if err := validateNoDroppedStmts("doX()", "doY()", "doY()\nz()", "doX()\nz()"); err != nil {
		t.Fatalf("single-line replacement must be skipped, got: %v", err)
	}
}

// TestValidateNoDroppedStmts_HappyPath: the math works out — pre=3,
// matched=2, repl=2 → expected=3, post=3.
func TestValidateNoDroppedStmts_HappyPath(t *testing.T) {
	preBody := "a()\nb()\nc()"
	matched := "a()\nb()"
	repl := "a2()\nb2()"
	postBody := "a2()\nb2()\nc()"
	if err := validateNoDroppedStmts(repl, matched, preBody, postBody); err != nil {
		t.Fatalf("expected nil, got: %v", err)
	}
}

// TestValidateNoDroppedStmts_DroppedStmtErrors: replacement says 2 stmts,
// match consumed 3 stmts, pre had 5; expected post=4. If post=3, one stmt
// was dropped silently — refuse.
func TestValidateNoDroppedStmts_DroppedStmtErrors(t *testing.T) {
	preBody := "a()\nb()\nc()\nd()\ne()"
	matched := "a()\nb()\nc()"
	repl := "a2()\nb2()"
	postBody := "a2()\nd()\ne()" // bug: dropped b2()
	err := validateNoDroppedStmts(repl, matched, preBody, postBody)
	if err == nil {
		t.Fatal("expected PATCH_DROPPED_CONTENT error")
	}
	if !strings.Contains(err.Error(), "PATCH_DROPPED_CONTENT") {
		t.Errorf("expected error code PATCH_DROPPED_CONTENT, got: %s", err.Error())
	}
	if !strings.Contains(err.Error(), "update object=func") {
		t.Errorf("expected message to suggest update object=func, got: %s", err.Error())
	}
}

// TestValidateNoDroppedStmts_NonParseableMatchedSkipped: when matched does
// not parse as a statement list (e.g. it is an expression fragment), skip
// the check — there's no useful delta to compute.
func TestValidateNoDroppedStmts_NonParseableMatchedSkipped(t *testing.T) {
	preBody := "a()\nb()"
	matched := "+ 1)" // junk
	repl := "x()\ny()"
	postBody := "x()\ny()" // worst case but match unparseable so skip
	if err := validateNoDroppedStmts(repl, matched, preBody, postBody); err != nil {
		t.Fatalf("non-parseable match must be skipped, got: %v", err)
	}
}

// TestValidateNoDroppedStmts_SingleStmtReplacementSkipped: replacements with
// fewer than 2 statements are skipped — the check is meant to catch the
// multi-statement shrinking-replace edge case only.
func TestValidateNoDroppedStmts_SingleStmtReplacementSkipped(t *testing.T) {
	preBody := "a()\nb()\nc()"
	matched := "a()\nb()"
	repl := "x()" // 1 stmt → skip
	postBody := "x()\nc()"
	if err := validateNoDroppedStmts(repl, matched, preBody, postBody); err != nil {
		t.Fatalf("single-stmt replacement must be skipped, got: %v", err)
	}
}

// TestParseStmtCount_FunctionBody: the helper counts top-level statements
// in the wrapped body. If/return blocks are single statements.
func TestParseStmtCount_FunctionBody(t *testing.T) {
	body := `if x {
    return 1
}
return 0`
	got, ok := parseStmtCount(body)
	if !ok {
		t.Fatal("expected ok=true")
	}
	if got != 2 {
		t.Errorf("expected 2 stmts (if + return), got %d", got)
	}
}

// TestParseStmtCount_NotParseable: junk input returns ok=false.
func TestParseStmtCount_NotParseable(t *testing.T) {
	if _, ok := parseStmtCount("+ 1)"); ok {
		t.Error("expected ok=false for unparseable input")
	}
}

// TestCollectDeclNames_MixedDecls: verify the helper extracts func, type,
// var, and const names from a mixed file.
func TestCollectDeclNames_MixedDecls(t *testing.T) {
	repl := `func F() {}
type T struct{}
var V = 1
const C = "x"
`
	names, ok := parseReplacementDeclNames(repl)
	if !ok {
		t.Fatal("expected ok=true")
	}
	want := map[string]bool{"F": true, "T": true, "V": true, "C": true}
	for _, n := range names {
		if !want[n] {
			t.Errorf("unexpected name in result: %q", n)
		}
		delete(want, n)
	}
	if len(want) > 0 {
		t.Errorf("missing names: %v", want)
	}
}

// TestCollectDeclNames_MethodWithReceiver: methods are keyed by Recv.Method
// so they don't collide with same-named top-level functions in another
// package.
func TestCollectDeclNames_MethodWithReceiver(t *testing.T) {
	repl := `func (h *Handler) Serve() {}`
	names, ok := parseReplacementDeclNames(repl)
	if !ok {
		t.Fatal("expected ok=true")
	}
	if len(names) != 1 {
		t.Fatalf("expected 1 name, got %d: %v", len(names), names)
	}
	if names[0] != "Handler.Serve" {
		t.Errorf("expected 'Handler.Serve', got %q", names[0])
	}
}

// TestParseReplacementImports_HappyPath: valid import block in replacement
func TestParseReplacementImports_HappyPath(t *testing.T) {
	repl := `import (
	"fmt"
	"os"
)`
	imports, ok := parseReplacementImports(repl)
	if !ok {
		t.Fatal("expected ok=true for valid import block")
	}
	if len(imports) != 2 {
		t.Fatalf("expected 2 imports, got %d: %v", len(imports), imports)
	}
	// Order may vary, so check set membership
	importSet := make(map[string]bool)
	for _, imp := range imports {
		importSet[imp] = true
	}
	if !importSet["fmt"] || !importSet["os"] {
		t.Errorf("expected fmt and os in imports, got: %v", imports)
	}
}

// TestParseReplacementImports_NoImports: replacement without imports returns ok=false
func TestParseReplacementImports_NoImports(t *testing.T) {
	repl := "func F() {}"
	_, ok := parseReplacementImports(repl)
	if ok {
		t.Error("expected ok=false for replacement without imports")
	}
}

// TestParseReplacementImports_InvalidGo: invalid Go code returns ok=false
func TestParseReplacementImports_InvalidGo(t *testing.T) {
	repl := "import \"fmt\"\n_ = fmt.Sprintf" // invalid top-level
	_, ok := parseReplacementImports(repl)
	if ok {
		t.Error("expected ok=false for invalid Go code")
	}
}

// TestParseFileImports_HappyPath: valid file with imports
func TestParseFileImports_HappyPath(t *testing.T) {
	src := []byte(`package p

import (
	"fmt"
	"os"
)

func F() {}
`)
	imports, ok := parseFileImports("f.go", src)
	if !ok {
		t.Fatal("expected ok=true for valid file")
	}
	if len(imports) != 2 {
		t.Fatalf("expected 2 imports, got %d: %v", len(imports), imports)
	}
	importSet := make(map[string]bool)
	for _, imp := range imports {
		importSet[imp] = true
	}
	if !importSet["fmt"] || !importSet["os"] {
		t.Errorf("expected fmt and os in imports, got: %v", imports)
	}
}

// TestParseFileImports_NoImports: file without imports returns empty slice with ok=true
func TestParseFileImports_NoImports(t *testing.T) {
	src := []byte("package p\n\nfunc F() {}\n")
	imports, ok := parseFileImports("f.go", src)
	if !ok {
		t.Error("expected ok=true for valid file without imports")
	}
	if len(imports) != 0 {
		t.Errorf("expected 0 imports, got %d: %v", len(imports), imports)
	}
}

// TestValidateNoDroppedDecls_ImportBlockHappyPath: replacement with import
// block that lands correctly in post-source passes validation.
func TestValidateNoDroppedDecls_ImportBlockHappyPath(t *testing.T) {
	postSrc := []byte(`package p

import (
	"fmt"
	"os"
)

func F() {}
`)
	repl := `import (
	"fmt"
	"os"
)
`
	if err := validateNoDroppedDecls("f.go", repl, postSrc); err != nil {
		t.Fatalf("expected nil for valid import replacement, got: %v", err)
	}
}

// TestValidateNoDroppedDecls_ImportBlockMissingImports: replacement with
// import block but post-source is missing some imports triggers error.
func TestValidateNoDroppedDecls_ImportBlockMissingImports(t *testing.T) {
	postSrc := []byte(`package p

import "fmt"

func F() {}
`)
	repl := `import (
	"fmt"
	"os"
)
`
	err := validateNoDroppedDecls("f.go", repl, postSrc)
	if err == nil {
		t.Fatal("expected error for missing imports, got nil")
	}
	if !strings.Contains(err.Error(), "PATCH_DROPPED_CONTENT") {
		t.Errorf("expected PATCH_DROPPED_CONTENT error, got: %s", err.Error())
	}
	if !strings.Contains(err.Error(), "os") {
		t.Errorf("expected error to name missing import 'os', got: %s", err.Error())
	}
}
