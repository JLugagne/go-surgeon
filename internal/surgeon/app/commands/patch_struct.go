package commands

import (
	"context"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"strings"

	"github.com/JLugagne/go-surgeon/internal/surgeon/domain"
)

// PatchStruct applies one or more granular patches to a struct's field list.
// All patches are resolved against the original element list (name-stable),
// then applied atomically — if any resolution fails, nothing is written.
func (h *ExecutePlanHandler) PatchStruct(ctx context.Context, req domain.PatchStructRequest) (domain.PatchStructResult, error) {
	src, err := h.fs.ReadFile(ctx, req.FilePath)
	if err != nil {
		return domain.PatchStructResult{}, &domain.Error{Code: "READ_ERROR", Message: "failed to read file", Err: err}
	}

	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, req.FilePath, src, parser.ParseComments)
	if err != nil {
		return domain.PatchStructResult{}, &domain.Error{Code: "PARSE_ERROR", Message: "failed to parse file", Err: err}
	}

	typeSpec, structType, ok := findTargetStruct(f, req.Identifier)
	if !ok {
		return domain.PatchStructResult{}, &domain.Error{
			Code:    "NODE_NOT_FOUND",
			Message: fmt.Sprintf("struct %q not found in %s", req.Identifier, req.FilePath),
		}
	}

	elements, trailing := parseStructFields(fset, src, structType)

	// Phase 1: resolve and validate all patches against the ORIGINAL element list.
	working := make([]*element, len(elements))
	copy(working, elements)

	var errs []string
	for i, p := range req.Patches {
		if msg := applyStructPatch(&working, elements, p); msg != "" {
			errs = append(errs, fmt.Sprintf("patch #%d (%s): %s", i+1, p.Op, msg))
		}
	}

	if len(errs) > 0 {
		msg := strings.Join(errs, "\n")
		// Include the full struct definition with line numbers so the agent
		// can see current fields and retry without a symbol call.
		startOff := fset.Position(typeSpec.Pos()).Offset
		for startOff > 0 && src[startOff-1] != '\n' {
			startOff--
		}
		startLine := fset.Position(typeSpec.Pos()).Line
		endLine := fset.Position(structType.End()).Line
		endOff := fset.Position(structType.End()).Offset
		if body := formatNumberedSource(src, startOff, endOff, startLine); body != "" {
			msg += fmt.Sprintf("\n\nCurrent definition of %s (lines %d-%d):\n%s", req.Identifier, startLine, endLine, body)
		}
		msg += "\nHint: use the line numbers and field names above to correct your patch."
		return domain.PatchStructResult{}, &domain.Error{
			Code:    "PATCH_FAILED",
			Message: msg,
		}
	}

	// Phase 2: render the new struct body.
	lbraceOff := fset.Position(structType.Fields.Opening).Offset
	rbraceOff := fset.Position(structType.Fields.Closing).Offset
	indent := detectStructIndent(src, lbraceOff, rbraceOff)

	newBody := renderElements(working, indent, src)
	// Re-append free-standing lines that sat between the last field and the
	// closing brace (comments, blank lines) — they are not part of any element.
	if len(trailing) > 0 {
		trimmed := make([]string, len(trailing))
		for i, l := range trailing {
			trimmed[i] = strings.TrimRight(l, " \t")
		}
		if newBody == "" {
			newBody = strings.Join(trimmed, "\n")
		} else {
			newBody += "\n" + strings.Join(trimmed, "\n")
		}
	}

	newSrc := make([]byte, 0, len(src)+len(newBody))
	newSrc = append(newSrc, src[:lbraceOff+1]...)
	newSrc = append(newSrc, []byte("\n"+newBody+"\n")...)
	newSrc = append(newSrc, src[rbraceOff:]...)

	// Run gofmt on the result so the diff we return already shows the
	// final column alignment (goimports also runs on write, but by then
	// the agent has already seen the diff).
	if formatted, fmtErr := format.Source(newSrc); fmtErr == nil {
		newSrc = formatted
	}
	// Reject the patch before writing if it would produce invalid Go.
	if err := validateGoSource(req.FilePath, newSrc); err != nil {
		return domain.PatchStructResult{}, err
	}

	diff := diffStrings(req.FilePath, string(src), string(newSrc))

	if req.Preview {
		return domain.PatchStructResult{Diff: diff, Applied: len(req.Patches), Preview: true}, nil
	}

	addedImports, err := h.fs.WriteFile(ctx, req.FilePath, newSrc)
	if err != nil {
		return domain.PatchStructResult{}, &domain.Error{Code: "WRITE_ERROR", Message: "failed to write file", Err: err}
	}

	return domain.PatchStructResult{Diff: diff, Applied: len(req.Patches), AddedImports: addedImports}, nil
}

// element is the in-memory model of one field in a struct or one method/embed
// in an interface. It is shared by patch_struct and patch_interface because
// both re-use the same list-manipulation / rendering machinery.
type element struct {
	// name is the field name, method name, or embed type literal.
	name string
	// kind is "field", "method", or "embed".
	kind string
	// For fields: the type expression (e.g. "string", "map[string]int").
	// For methods: the signature (e.g. "(p []byte) (int, error)" — params + results, no name).
	typeExpr string
	// For fields: the tag literal with backticks (e.g. `` `json:"x"` ``), or empty.
	tag string
	// The doc comment as raw text (without // prefixes), or empty.
	doc string
	// rawLine is the original byte-range substring for this element; when the
	// element is unchanged by the patch, we reuse this exactly.
	rawLine string
	// dirty is true when the element has been mutated (so the renderer emits a
	// fresh line rather than copying rawLine).
	dirty bool
	// Trailing // comment on the same line as the field, without the leading //, or empty.
	inlineComment string
	// leading holds the free-standing lines (blank lines and comments not
	// attached to any field) found immediately above this element; they are
	// re-emitted verbatim before it.
	leading []string
	// group is shared by the members of one multi-name declaration
	// (x, y int); nil for ordinary elements.
	group *fieldGroup
}

// fieldGroup is the shared state of elements born from one multi-name field
// declaration. It lets the renderer keep the members grouped on one line and
// emit the shared doc / inline comment only once when the group is split.
type fieldGroup struct {
	size          int // original member count
	docEmitted    bool
	inlineEmitted bool
}

// findTargetStruct locates the target struct declaration by name.
func findTargetStruct(f *ast.File, identifier string) (*ast.TypeSpec, *ast.StructType, bool) {
	pkgTarget, nameTarget := parseIdentifier(identifier)
	if pkgTarget != "" && pkgTarget != f.Name.Name {
		return nil, nil, false
	}
	for _, decl := range f.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.TYPE {
			continue
		}
		for _, spec := range gen.Specs {
			ts, ok := spec.(*ast.TypeSpec)
			if !ok || ts.Name.Name != nameTarget {
				continue
			}
			st, ok := ts.Type.(*ast.StructType)
			if !ok {
				continue
			}
			return ts, st, true
		}
	}
	return nil, nil, false
}

// parseStructFields walks the struct's field list into an element slice.
// The second return value holds the free-standing lines (comments, blanks)
// between the last field and the closing brace, preserved verbatim.
func parseStructFields(fset *token.FileSet, src []byte, st *ast.StructType) ([]*element, []string) {
	var out []*element
	openPos := fset.Position(st.Fields.Opening)
	closeLine := fset.Position(st.Fields.Closing).Line
	multiLine := closeLine > openPos.Line

	// cursor tracks the offset just past the last consumed body line; the
	// text between cursor and the next field's first line is free-standing
	// (blank lines and comments not attached to any field).
	cursor := openPos.Offset + 1
	for cursor < len(src) && src[cursor-1] != '\n' {
		cursor++
	}

	for _, field := range st.Fields.List {
		typeExpr := extractSourceRange(src, fset, field.Type.Pos(), field.Type.End())
		var tag string
		if field.Tag != nil {
			tag = field.Tag.Value
		}
		var doc string
		if field.Doc != nil {
			doc = extractDocText(field.Doc)
		}

		// inlineComment is the // comment on the same line as the field declaration.
		// We store it as raw text (without the leading //) so the renderer can
		// re-emit it when the element becomes dirty.
		var inlineComment string
		if field.Comment != nil {
			inlineComment = extractDocText(field.Comment)
		}

		// rawLine captures the full original source lines for this element —
		// starting at the beginning of the line containing the doc (or the field,
		// if no doc) and ending at the end of the line containing the trailing
		// comment (or the field end). This preserves indentation AND inline
		// comments verbatim when the element is not mutated.
		rawStart := field.Pos()
		if field.Doc != nil {
			rawStart = field.Doc.Pos()
		}
		rawEnd := field.End()
		if field.Comment != nil {
			rawEnd = field.Comment.End()
		}
		startPos := fset.Position(rawStart)
		endPos := fset.Position(rawEnd)
		rawLine := extractLineRange(src, fset, rawStart, rawEnd)
		// A raw line sharing a line with either brace would drag the brace
		// (and the surrounding declaration text) into the re-rendered body —
		// e.g. single-line structs. Render such elements fresh instead.
		if startPos.Line == openPos.Line || endPos.Line == closeLine {
			rawLine = ""
		}

		var leading []string
		if multiLine {
			if startPos.Line > openPos.Line {
				lineStart := startPos.Offset
				for lineStart > 0 && src[lineStart-1] != '\n' {
					lineStart--
				}
				if cursor < lineStart {
					leading = strings.Split(strings.TrimSuffix(string(src[cursor:lineStart]), "\n"), "\n")
				}
			}
			lineEnd := endPos.Offset
			for lineEnd < len(src) && src[lineEnd] != '\n' {
				lineEnd++
			}
			if lineEnd+1 > cursor {
				cursor = lineEnd + 1
			}
		}

		if len(field.Names) == 0 {
			// Embedded field — the "name" is the bare type literal.
			out = append(out, &element{
				name:          typeExpr,
				kind:          "embed",
				typeExpr:      typeExpr,
				tag:           tag,
				doc:           doc,
				inlineComment: inlineComment,
				rawLine:       rawLine,
				leading:       leading,
			})
			continue
		}
		// A multi-name declaration (x, y int) becomes one element per name,
		// linked through a shared fieldGroup so the renderer can keep them
		// grouped (or split them without duplicating the shared comments).
		var g *fieldGroup
		if len(field.Names) > 1 {
			g = &fieldGroup{size: len(field.Names)}
		}
		for i, n := range field.Names {
			e := &element{
				name:          n.Name,
				kind:          "field",
				typeExpr:      typeExpr,
				tag:           tag,
				doc:           doc,
				inlineComment: inlineComment,
				group:         g,
			}
			// The raw line and leading lines cover the whole declaration;
			// attach them to the first member only.
			if i == 0 {
				e.rawLine = rawLine
				e.leading = leading
			}
			out = append(out, e)
		}
	}

	var trailing []string
	if multiLine {
		lineStart := fset.Position(st.Fields.Closing).Offset
		for lineStart > 0 && src[lineStart-1] != '\n' {
			lineStart--
		}
		if cursor < lineStart {
			trailing = strings.Split(strings.TrimSuffix(string(src[cursor:lineStart]), "\n"), "\n")
		}
	}
	return out, trailing
}

// applyStructPatch mutates *working in place according to one patch.
// Returns an empty string on success, or an error message on failure.
func applyStructPatch(working *[]*element, original []*element, p domain.StructPatch) string {
	switch p.Op {
	case domain.StructPatchOpAddField:
		if p.Name == "" {
			return "name is required"
		}
		if p.Type == "" {
			return "type is required"
		}
		if findElement(*working, p.Name) != -1 {
			return fmt.Sprintf("field %q already exists. Current fields: %s", p.Name, listNames(*working))
		}
		kind := "field"
		// A dotted name with the same value as the type (e.g. name="io.Reader",
		// type="io.Reader") denotes an embedded field.
		if strings.Contains(p.Name, ".") && p.Name == p.Type {
			kind = "embed"
		}
		newElem := &element{
			name:     p.Name,
			kind:     kind,
			typeExpr: p.Type,
			tag:      formatTag(p.Tag),
			doc:      p.Doc,
			dirty:    true,
		}
		return insertElement(working, original, p.Before, p.After, p.Position, newElem)

	case domain.StructPatchOpRemoveField:
		if p.Name == "" {
			return "name is required"
		}
		idx := findElement(*working, p.Name)
		if idx == -1 {
			return fieldNotFoundMsg(p.Name, *working)
		}
		*working = append((*working)[:idx], (*working)[idx+1:]...)
		return ""

	case domain.StructPatchOpRenameField:
		if p.From == "" || p.To == "" {
			return "from and to are required"
		}
		idx := findElement(*working, p.From)
		if idx == -1 {
			return fieldNotFoundMsg(p.From, *working)
		}
		if findElement(*working, p.To) != -1 {
			return fmt.Sprintf("field %q already exists — cannot rename %q to a colliding name", p.To, p.From)
		}
		(*working)[idx].name = p.To
		(*working)[idx].dirty = true
		return ""

	case domain.StructPatchOpRetypeField:
		if p.Name == "" || p.Type == "" {
			return "name and type are required"
		}
		idx := findElement(*working, p.Name)
		if idx == -1 {
			return fieldNotFoundMsg(p.Name, *working)
		}
		(*working)[idx].typeExpr = p.Type
		if p.Tag != "" {
			(*working)[idx].tag = formatTag(p.Tag)
		}
		(*working)[idx].dirty = true
		return ""

	case domain.StructPatchOpSetTag:
		if p.Name == "" {
			return "name is required"
		}
		idx := findElement(*working, p.Name)
		if idx == -1 {
			return fieldNotFoundMsg(p.Name, *working)
		}
		(*working)[idx].tag = formatTag(p.Tag)
		(*working)[idx].dirty = true
		return ""

	case domain.StructPatchOpSetDoc:
		if p.Name == "" {
			return "set_doc on a struct field requires name (the field whose doc you want to set); to set the struct's own doc comment, use 'update object=struct' with a doc field — see issue #15"
		}
		idx := findElement(*working, p.Name)
		if idx == -1 {
			return fieldNotFoundMsg(p.Name, *working)
		}
		(*working)[idx].doc = p.Doc
		(*working)[idx].dirty = true
		return ""

	default:
		return fmt.Sprintf("unknown op %q", p.Op)
	}
}

// findElement returns the index of the first element with the given name, or -1.
func findElement(elems []*element, name string) int {
	for i, e := range elems {
		if e.name == name {
			return i
		}
	}
	return -1
}

// listNames joins element names for error messages.
func listNames(elems []*element) string {
	names := make([]string, len(elems))
	for i, e := range elems {
		names[i] = e.name
	}
	return strings.Join(names, ", ")
}

// insertElement inserts newElem into *working at the position specified by
// before/after/position. Anchors are resolved against the ORIGINAL list so
// multiple add_field patches in a row behave predictably.
// insertElement inserts newElem into *working at the position specified by
// before/after/position. Anchors are resolved against the WORKING list
// (which already reflects previous patches in the same call), falling
// back to the ORIGINAL list for anchors that may have been removed by
// earlier patches — this way several add_field patches in a row can
// reference each other's newly-added fields as anchors.
func insertElement(working *[]*element, original []*element, before, after, position string, newElem *element) string {
	switch {
	case before != "":
		wIdx := findElement(*working, before)
		if wIdx == -1 {
			// Anchor may have been removed by an earlier patch; fall back to
			// the original list so the agent still sees it exists in intent.
			if findElement(original, before) == -1 {
				return fmt.Sprintf("anchor before=%q not found. Current fields: %s", before, listNames(*working))
			}
			// Removed-by-earlier-patch case: append at end as best-effort.
			*working = append(*working, newElem)
			return ""
		}
		*working = insertAt(*working, wIdx, newElem)
	case after != "":
		wIdx := findElement(*working, after)
		if wIdx == -1 {
			if findElement(original, after) == -1 {
				return fmt.Sprintf("anchor after=%q not found. Current fields: %s", after, listNames(*working))
			}
			*working = append(*working, newElem)
			return ""
		}
		*working = insertAt(*working, wIdx+1, newElem)
	case position == "first":
		*working = insertAt(*working, 0, newElem)
	case position == "last", position == "":
		*working = append(*working, newElem)
	default:
		return fmt.Sprintf("invalid position %q (must be 'first' or 'last')", position)
	}
	return ""
}

func insertAt(elems []*element, idx int, e *element) []*element {
	if idx < 0 {
		idx = 0
	}
	if idx > len(elems) {
		idx = len(elems)
	}
	out := make([]*element, 0, len(elems)+1)
	out = append(out, elems[:idx]...)
	out = append(out, e)
	out = append(out, elems[idx:]...)
	return out
}

// formatTag wraps a tag in backticks, unless it already has them or is empty.
func formatTag(tag string) string {
	tag = strings.TrimSpace(tag)
	if tag == "" {
		return ""
	}
	if len(tag) >= 2 && tag[0] == '`' && tag[len(tag)-1] == '`' {
		return tag
	}
	return "`" + tag + "`"
}

// extractSourceRange returns the substring of src corresponding to the given AST range.
func extractSourceRange(src []byte, fset *token.FileSet, start, end token.Pos) string {
	s := fset.Position(start).Offset
	e := fset.Position(end).Offset
	if s < 0 || e > len(src) || s >= e {
		return ""
	}
	return string(src[s:e])
}

// extractDocText joins the raw text lines of a CommentGroup (without // prefixes).
func extractDocText(g *ast.CommentGroup) string {
	var lines []string
	for _, c := range g.List {
		text := c.Text
		switch {
		case strings.HasPrefix(text, "// "):
			lines = append(lines, text[3:])
		case strings.HasPrefix(text, "//"):
			lines = append(lines, text[2:])
		default:
			lines = append(lines, text)
		}
	}
	return strings.Join(lines, "\n")
}

// detectStructIndent returns the indentation to use for field lines inside the
// struct braces. Defaults to a single tab if no existing line is found.
func detectStructIndent(src []byte, lbraceOff, rbraceOff int) string {
	body := string(src[lbraceOff+1 : rbraceOff])
	for _, line := range strings.Split(body, "\n") {
		trimmed := strings.TrimLeft(line, " \t")
		if trimmed == "" {
			continue
		}
		return line[:len(line)-len(trimmed)]
	}
	return "\t"
}

// renderElements emits the body content between the braces (without the braces
// themselves), one element per line, using src to preserve unchanged lines.
// Free-standing lines captured in element.leading are re-emitted verbatim,
// and members of a multi-name declaration are rendered together.
func renderElements(elems []*element, indent string, src []byte) string {
	var lines []string
	for i := 0; i < len(elems); {
		e := elems[i]
		for _, l := range e.leading {
			lines = append(lines, strings.TrimRight(l, " \t"))
		}
		if e.group == nil {
			if !e.dirty && e.rawLine != "" {
				// Preserve the original raw line verbatim (including any leading doc).
				// Trim any trailing whitespace but keep inner structure.
				lines = append(lines, strings.TrimRight(e.rawLine, " \t"))
			} else {
				lines = append(lines, renderElement(e, indent))
			}
			i++
			continue
		}
		// Collect the maximal run of consecutive members of the same group.
		j := i + 1
		for j < len(elems) && elems[j].group == e.group {
			j++
		}
		lines = append(lines, renderGroupRun(elems[i:j], indent)...)
		i = j
	}
	return strings.Join(lines, "\n")
}

// renderGroupRun renders consecutive members of one multi-name declaration.
// An untouched, complete group is copied verbatim; members that still share
// type/tag/doc are re-grouped on one line; diverged members get one line
// each, with the shared doc and inline comment emitted only once.
func renderGroupRun(run []*element, indent string) []string {
	g := run[0].group
	first := run[0]
	clean, uniform := true, true
	for _, m := range run {
		if m.dirty {
			clean = false
		}
		if m.typeExpr != first.typeExpr || m.tag != first.tag ||
			m.doc != first.doc || m.inlineComment != first.inlineComment {
			uniform = false
		}
	}
	if len(run) == g.size && clean && first.rawLine != "" {
		g.docEmitted = true
		g.inlineEmitted = true
		return []string{strings.TrimRight(first.rawLine, " \t")}
	}
	takeDoc := func(m *element) string {
		if g.docEmitted {
			return ""
		}
		g.docEmitted = true
		return m.doc
	}
	takeInline := func(m *element) string {
		if g.inlineEmitted {
			return ""
		}
		g.inlineEmitted = true
		return m.inlineComment
	}
	if uniform {
		names := make([]string, len(run))
		for i, m := range run {
			names[i] = m.name
		}
		return []string{renderElement(&element{
			name:          strings.Join(names, ", "),
			kind:          "field",
			typeExpr:      first.typeExpr,
			tag:           first.tag,
			doc:           takeDoc(first),
			inlineComment: takeInline(first),
		}, indent)}
	}
	out := make([]string, 0, len(run))
	for _, m := range run {
		cp := *m
		cp.doc = takeDoc(m)
		cp.inlineComment = takeInline(m)
		cp.rawLine = ""
		out = append(out, renderElement(&cp, indent))
	}
	return out
}

// renderElement returns the text for one element, including its doc comment
// (one or more // lines above) and the field/method line itself.
func renderElement(e *element, indent string) string {
	var b strings.Builder
	if e.doc != "" {
		for _, line := range strings.Split(e.doc, "\n") {
			b.WriteString(indent)
			if line == "" {
				b.WriteString("//")
			} else {
				b.WriteString("// ")
				b.WriteString(line)
			}
			b.WriteByte('\n')
		}
	}
	b.WriteString(indent)
	switch e.kind {
	case "embed":
		b.WriteString(e.typeExpr)
	case "method":
		b.WriteString(e.name)
		// Method signatures start with '(' — no space between name and params.
		if !strings.HasPrefix(e.typeExpr, "(") {
			b.WriteByte(' ')
		}
		b.WriteString(e.typeExpr)
	default:
		b.WriteString(e.name)
		b.WriteByte(' ')
		b.WriteString(e.typeExpr)
	}
	if e.tag != "" {
		b.WriteByte(' ')
		b.WriteString(e.tag)
	}
	// Re-emit the trailing inline comment so it survives rendering of a dirty
	// element (e.g. after rename/retype/set_tag).
	if e.inlineComment != "" {
		b.WriteString(" // ")
		b.WriteString(e.inlineComment)
	}
	return b.String()
}

// extractLineRange returns the substring of src spanning from the start of the
// line containing `start` to the end of the line containing `end`. This
// preserves leading indentation and trailing inline comments that
// extractSourceRange drops.
func extractLineRange(src []byte, fset *token.FileSet, start, end token.Pos) string {
	s := fset.Position(start).Offset
	e := fset.Position(end).Offset
	if s < 0 || e > len(src) || s >= e {
		return ""
	}
	// Extend s back to the start of its line.
	for s > 0 && src[s-1] != '\n' {
		s--
	}
	// Extend e forward to the end of its line (but not past the newline).
	for e < len(src) && src[e] != '\n' {
		e++
	}
	return string(src[s:e])
}

// fieldNotFoundMsg formats the standard "field not found" error used by
// applyStructPatch across remove/rename/retype/set_tag/set_doc branches.
// Keeping one format string makes sure the error message stays consistent
// whenever the hint format evolves.
func fieldNotFoundMsg(name string, working []*element) string {
	return fmt.Sprintf("field %q not found. Current fields: %s", name, listNames(working))
}
