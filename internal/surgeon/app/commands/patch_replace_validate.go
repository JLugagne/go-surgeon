// validateReplaceApplied verifies that, after a text-splice patch has been
// computed, every op=replace patch's replacement text is actually present in
// the resulting source. This is a paranoid post-condition check that guards
// against silent data loss when an upstream serialization bug — for example
// a multi-line replacement being mishandled inside a nested array — causes
// the splice to remove the matched text without inserting the replacement.
//
// The helper compares the literal bytes of each replacement against the
// resulting source. When a replacement is non-empty but absent from the
// result, it returns a structured error so the caller can roll back the
// write. Trailing/leading whitespace differences are tolerated by trimming
// before comparison: callers (patch_function, patch_decl) sometimes
// re-indent or normalize the replacement, so we look for the trimmed
// payload as a substring rather than the exact bytes.
//
// Returns nil when every non-empty replacement is found, or when the
// replacements list is empty (nothing to validate).
package commands

import (
	"fmt"
	"strings"

	"github.com/JLugagne/go-surgeon/internal/surgeon/domain"
)

// replaceValidation describes one replacement to verify after the splice.
// Index is the 1-based patch index used in error messages so the agent can
// pinpoint which patch in the array did not land. Replacement holds the
// literal text the splice was supposed to insert.
type replaceValidation struct {
	Index       int
	Replacement string
}

// validateReplaceApplied checks every entry in replacements against newSrc.
// It returns nil if all non-empty replacements are present (substring
// match after whitespace trimming). Otherwise it returns a domain.Error
// with code PATCH_REPLACE_NOT_APPLIED and a message that names the
// offending patch index plus a truncated preview of the missing text.
//
// Empty replacements are skipped: an op=replace with replace="" is a
// legal "delete the match" pattern and there is nothing textual to
// verify post-splice.
func validateReplaceApplied(newSrc []byte, replacements []replaceValidation) error {
	if len(replacements) == 0 {
		return nil
	}
	src := string(newSrc)
	for _, rv := range replacements {
		needle := strings.TrimSpace(rv.Replacement)
		if needle == "" {
			continue
		}
		if strings.Contains(src, needle) {
			continue
		}
		return &domain.Error{
			Code:    "PATCH_REPLACE_NOT_APPLIED",
			Message: formatReplaceNotAppliedMessage(rv.Index, rv.Replacement),
		}
	}
	return nil
}

// formatReplaceNotAppliedMessage builds the agent-facing error text shown
// when a replacement disappears between resolution and the final source.
// The replacement is truncated to keep the message short while still
// giving the agent enough to recognize what went missing.
func formatReplaceNotAppliedMessage(index int, replacement string) string {
	preview := truncateReplacementPreview(replacement, 120)
	return fmt.Sprintf(
		"patch #%d (replace): result file does not contain the replacement text — write rolled back to prevent silent data loss. "+
			"Expected file to contain %q after splice but it does not. "+
			"This usually means the replacement was mangled in transport (a known multi-line/JSON-array client bug) — retry the call, "+
			"or fall back to update object=func / update object=file for whole-declaration rewrites.",
		index, preview,
	)
}

// truncateReplacementPreview returns at most max runes of s, appending an
// ellipsis when the input was shortened. Newlines are collapsed to "\\n"
// so the preview fits on a single line in the error message.
func truncateReplacementPreview(s string, max int) string {
	flat := strings.ReplaceAll(s, "\n", "\\n")
	if len([]rune(flat)) <= max {
		return flat
	}
	r := []rune(flat)
	return string(r[:max]) + "..."
}
