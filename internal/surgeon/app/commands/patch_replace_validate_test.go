package commands

import (
	"strings"
	"testing"
)

func TestValidateReplaceApplied_HappyPath_PresentSubstring(t *testing.T) {
	src := []byte(`package p

func F() string {
	return "hello world"
}
`)
	checks := []replaceValidation{
		{Index: 1, Replacement: `"hello world"`},
	}
	if err := validateReplaceApplied(src, checks); err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
}

func TestValidateReplaceApplied_HappyPath_MultiLineReplacement(t *testing.T) {
	src := []byte(`package p

func F() error {
	if x == 1 {
		return errors.New("a")
	}
	return nil
}
`)
	checks := []replaceValidation{
		{Index: 2, Replacement: "if x == 1 {\n\t\treturn errors.New(\"a\")\n\t}"},
	}
	if err := validateReplaceApplied(src, checks); err != nil {
		t.Fatalf("multi-line replacement should validate when present: %v", err)
	}
}

func TestValidateReplaceApplied_HappyPath_EmptyReplacementSkipped(t *testing.T) {
	src := []byte(`package p
`)
	checks := []replaceValidation{
		{Index: 1, Replacement: ""},
	}
	if err := validateReplaceApplied(src, checks); err != nil {
		t.Fatalf("empty replacement must be a no-op: %v", err)
	}
}

func TestValidateReplaceApplied_HappyPath_WhitespaceTolerant(t *testing.T) {
	src := []byte(`package p

func F() {
	doWork()
}
`)
	// Replacement supplied with leading and trailing whitespace; the trimmed
	// payload is in the file, so validation must still pass.
	checks := []replaceValidation{
		{Index: 1, Replacement: "\n\tdoWork()\n"},
	}
	if err := validateReplaceApplied(src, checks); err != nil {
		t.Fatalf("trimmed substring match should pass: %v", err)
	}
}

func TestValidateReplaceApplied_NilChecks(t *testing.T) {
	src := []byte("package p\n")
	if err := validateReplaceApplied(src, nil); err != nil {
		t.Fatalf("nil checks must return nil: %v", err)
	}
}

func TestValidateReplaceApplied_FailureMode_MissingReplacementErrors(t *testing.T) {
	// The source intentionally lacks the replacement text. This simulates the
	// silent-data-loss bug from issue #3 where the splice removed the match
	// but the replacement was never inserted.
	src := []byte(`package p

func F() {
}
`)
	checks := []replaceValidation{
		{Index: 3, Replacement: "doImportantWork()"},
	}
	err := validateReplaceApplied(src, checks)
	if err == nil {
		t.Fatal("expected a PATCH_REPLACE_NOT_APPLIED error, got nil")
	}
	msg := err.Error()
	if !strings.Contains(msg, "patch #3") {
		t.Errorf("expected error to name patch index 3, got: %s", msg)
	}
	if !strings.Contains(msg, "doImportantWork()") {
		t.Errorf("expected error preview to include the missing replacement text, got: %s", msg)
	}
	if !strings.Contains(msg, "rolled back") {
		t.Errorf("expected error message to mention rollback semantics, got: %s", msg)
	}
}

func TestValidateReplaceApplied_FailureMode_FirstMissingWins(t *testing.T) {
	// When several replacements are checked and only the SECOND is missing,
	// the error must point at index 2 — not 1.
	src := []byte(`package p

func F() {
	doWork()
}
`)
	checks := []replaceValidation{
		{Index: 1, Replacement: "doWork()"},          // present
		{Index: 2, Replacement: "missingFunction()"}, // missing
	}
	err := validateReplaceApplied(src, checks)
	if err == nil {
		t.Fatal("expected error for the missing second replacement")
	}
	if !strings.Contains(err.Error(), "patch #2") {
		t.Errorf("expected error to name patch #2, got: %s", err.Error())
	}
}

func TestValidateReplaceApplied_FailureMode_TruncatesLongReplacement(t *testing.T) {
	// Long multi-line replacements should appear truncated in the message
	// so the error stays compact.
	long := strings.Repeat("a", 500)
	checks := []replaceValidation{
		{Index: 1, Replacement: long},
	}
	err := validateReplaceApplied([]byte("package p\n"), checks)
	if err == nil {
		t.Fatal("expected error")
	}
	msg := err.Error()
	if !strings.Contains(msg, "...") {
		t.Errorf("expected truncated preview to include ellipsis, got: %s", msg)
	}
	if strings.Count(msg, "a") > 200 {
		t.Errorf("preview should be capped well under the input length, got %d a-chars", strings.Count(msg, "a"))
	}
}

func TestTruncateReplacementPreview_FlattensNewlines(t *testing.T) {
	got := truncateReplacementPreview("a\nb\nc", 100)
	if got != `a\nb\nc` {
		t.Errorf("expected newlines to be escaped, got: %q", got)
	}
}

func TestTruncateReplacementPreview_TruncatesLong(t *testing.T) {
	got := truncateReplacementPreview(strings.Repeat("x", 200), 50)
	if !strings.HasSuffix(got, "...") {
		t.Errorf("expected long input to be suffixed with ..., got: %q", got)
	}
	// The truncated body is exactly 50 runes, plus the trailing "..." marker.
	if len(got) != 50+len("...") {
		t.Errorf("expected 50 chars + ellipsis, got len=%d", len(got))
	}
}
