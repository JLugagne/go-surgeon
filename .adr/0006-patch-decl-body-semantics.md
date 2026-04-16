# ADR 0006 — `patch_decl` body-extraction semantics

## Status
Accepted — 2026-04-16.

## Context
Sprint B #1 of the go-surgeon v2 roadmap adds `patch_decl`, a new
MCP tool that text-patches the VALUE of a top-level `const` or `var`
declaration. It is the symmetric counterpart to `patch_function`:
same ops (`replace` / `insert_before` / `insert_after` / `delete` /
`wrap`), same targeting modes (`match` / `match_regex` / `at_line` /
`from_line+to_line`), same atomic two-phase application.

`patch_function` targets the text between the `{` and `}` of a
function body. For `patch_decl`, the natural analogue is "the value
expression of the declaration" — but there is no single obvious
textual boundary:

```go
const Foo = "value"           // value is a BasicLit; body = `"value"`
const Bar = `raw
multi-line`                   // value is a BasicLit; body = backticked text
var Baz = []int{1, 2, 3}      // value is a CompositeLit; body = `[]int{1, 2, 3}`
var Qux int                   // no value at all
const (A = 1; B = 2)          // grouped: each ValueSpec has its own value
```

The friction point that motivated this feature (pass-4 P4-2,
pass-7) is multi-line raw-string constants like `serverInstructions`
in `internal/surgeon/inbound/mcp/server.go`, where the agent wants
to replace a line or two inside the backticked text without
rewriting the whole 40-line const.

Two candidate boundaries:

1. **Whole value expression.** `origBody = src[valueExprStart:valueExprEnd]`.
   This is the most general: it covers every initializer (literals,
   composite literals, function calls, expressions). Downside for
   the string-literal case: the agent would need to include the
   surrounding backticks (or quotes) in its `match` or `replace`.

2. **Inner content for single string BasicLit.** When the value is a
   single `*ast.BasicLit` of `token.STRING`, strip the delimiters
   (first and last byte: `` ` `` or `"`) and patch against the raw
   content. Every other value expression still falls back to mode 1.

## Decision
Use **mode 2**: when the value is a single `*ast.BasicLit` of string
kind (including raw strings), `origBody` is the *content* between the
quotes. For every other value expression, `origBody` is the full
value-expression text (mode 1).

Rationale:
- The overwhelming motivating case is multi-line raw strings. Asking
  the agent to produce `match: "` + "`" + "line 1..." + "`" patterns is
  awkward and error-prone (raw strings can't contain backticks at
  all, forcing the caller to build a concatenation).
- Composite literals and expressions are rarer targets; when they do
  come up, the caller can freely reference `[]` / `{` / `)` as part
  of the match, which is natural.
- String-escape handling: for interpreted strings (`"..."`), the raw
  source bytes between the quotes are used, not the unescaped Go
  value. This preserves 1:1 correspondence between file bytes and
  matches, so patches don't have to think about `\n` vs. a literal
  newline inside the body.

The tool description documents the distinction explicitly:

> `patch_decl` targets the VALUE expression of a top-level const/var.
> For a single-string-literal value, matches apply to the text INSIDE
> the quotes/backticks (delimiters are preserved automatically). For
> any other value (composite literal, expression, number, etc.),
> matches apply to the full value-expression text as it appears in
> source.

## Alternatives considered

### A. Mode 1 only (always full value expression)
Rejected. Forces the agent to reproduce backticks/quotes in every
match, and makes `insert_before` / `insert_after` on the first or
last line of a raw string surprising (the delimiter is part of the
body but has no indent of its own).

### B. Strip delimiters for every BasicLit, including numbers
Rejected. Numbers, chars, imag/float literals don't have delimiters
to strip, so a uniform rule would only apply to strings — which is
exactly mode 2 anyway. Not worth a separate abstraction.

### C. Treat the declaration body as the whole ValueSpec (name + value)
Rejected. That gives the caller power to rename the identifier via a
patch, which defeats the "identifier-targeted" contract. The
existing `update` tool covers whole-decl rewrites.

### D. Reject grouped decls (`const (…)`) entirely
Rejected. Grouped decls are a common Go idiom (error codes, HTTP
statuses, enum constants). We support them by finding the matching
`ValueSpec` inside the `GenDecl` by name.

## Consequences

**Positive**
- The primary use case (string-literal patching) reads naturally:
  `match: "line 2"`, not `match: "` + "`" + `... line 2 ...` + "`" + `"`.
- Line-mode targeting works identically in both modes, since the
  line math is body-relative in either case.
- Implementation is a six-line special case at body extraction time
  (detect single-BasicLit, check Kind == token.STRING, shift offsets
  by 1 byte on each end).

**Negative**
- Two modes means the agent needs to know which one applies to their
  target. The tool description calls this out, and the error path
  for "no match found" already prints the current body with line
  numbers — so a failed assumption is recoverable in one round-trip.
- Typed vars without an initializer (`var x int`) have no value to
  patch. They return `NODE_NOT_FOUND` with the message "declaration
  X has no value expression". The caller is expected to use
  `update` if they want to add an initializer.

## Scope
MCP tool `patch_decl` and its handler. The domain `FunctionPatch`
struct is reused as-is (same ops, same fields). New domain types:
`PatchDeclRequest`, `PatchDeclResult`.
