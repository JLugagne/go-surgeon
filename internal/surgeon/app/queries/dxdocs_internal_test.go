package queries

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// Item 31: the test_run summary must not double-count a parent test's elapsed
// time together with its subtests' elapsed time. `go test -json` reports the
// parent's Elapsed as the sum that already includes every subtest, so summing
// both inflates the reported total.
func TestParseTestRunOutput_DoesNotDoubleCountSubtestSeconds(t *testing.T) {
	// Parent TestFoo (3.0s) contains sub1 (1.0s) and sub2 (2.0s).
	stream := strings.Join([]string{
		`{"Action":"run","Package":"p","Test":"TestFoo"}`,
		`{"Action":"run","Package":"p","Test":"TestFoo/sub1"}`,
		`{"Action":"pass","Package":"p","Test":"TestFoo/sub1","Elapsed":1.0}`,
		`{"Action":"run","Package":"p","Test":"TestFoo/sub2"}`,
		`{"Action":"pass","Package":"p","Test":"TestFoo/sub2","Elapsed":2.0}`,
		`{"Action":"pass","Package":"p","Test":"TestFoo","Elapsed":3.0}`,
		`{"Action":"pass","Package":"p","Elapsed":3.0}`,
	}, "\n")

	res := parseTestRunOutput([]byte(stream))

	// Correct wall-time is the top-level total, 3.0s — not 1+2+3 = 6.0s.
	if !strings.Contains(res.Summary, "(3.0s)") {
		t.Fatalf("expected summary to report 3.0s (no double counting), got: %q", res.Summary)
	}
	if strings.Contains(res.Summary, "6.0s") {
		t.Fatalf("summary double-counts parent+subtest seconds: %q", res.Summary)
	}
}

// Item 31: limitedBuffer must not cut a multi-byte rune at the byte limit,
// which would leave an invalid UTF-8 tail that renders as U+FFFD.
func TestLimitedBuffer_DoesNotSplitMultiByteRune(t *testing.T) {
	var b limitedBuffer
	b.limit = 2 // cut falls in the middle of the 3-byte '€'
	// "€a" is E2 82 AC 61 — a naive p[:2] keeps E2 82, an invalid rune.
	b.Write([]byte("€a"))

	if !utf8.ValidString(b.String()) {
		t.Fatalf("limitedBuffer split a multi-byte rune: % x", []byte(b.String()))
	}
	if !b.truncated {
		t.Fatalf("expected truncated flag to be set")
	}
}

// Item 31: truncateRawOutput must not cut a multi-byte rune at the char limit.
func TestTruncateRawOutput_DoesNotSplitMultiByteRune(t *testing.T) {
	// 3999 ASCII bytes then a run of 3-byte runes; byte index rawOutputCharLimit
	// (4000) lands inside the first '€'.
	s := strings.Repeat("a", rawOutputCharLimit-1) + strings.Repeat("€", 100)
	out := truncateRawOutput(s)

	if !utf8.ValidString(out) {
		t.Fatalf("truncateRawOutput split a multi-byte rune: tail=% x", []byte(out))
	}
	if !strings.Contains(out, "truncated") {
		t.Fatalf("expected truncation marker in output")
	}
}
