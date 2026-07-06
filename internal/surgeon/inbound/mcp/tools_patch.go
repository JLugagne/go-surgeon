package mcp

import (
	"github.com/JLugagne/go-surgeon/internal/surgeon/domain/service"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// --- Patch tools ---

type patchOpInput struct {
	Op         string   `json:"op" jsonschema:"operation: replace, insert_before, insert_after, delete, wrap, set_signature (function only)"`
	Match      string   `json:"match,omitempty" jsonschema:"literal text to match inside the function body (whitespace-normalized)"`
	MatchRegex string   `json:"match_regex,omitempty" jsonschema:"regex alternative to match; mutually exclusive with match"`
	Occurrence int      `json:"occurrence,omitempty" jsonschema:"which match to operate on (1-based, default 0 = error-if-ambiguous for function). Use -1 to apply to all matches."`
	Replace    string   `json:"replace,omitempty" jsonschema:"replacement text (for replace op)"`
	Code       string   `json:"code,omitempty" jsonschema:"line of code to insert (for insert_before / insert_after ops)"`
	Wrap       string   `json:"wrap,omitempty" jsonschema:"wrap template with %s as the matched text (for wrap op)"`
	AtLine     int      `json:"at_line,omitempty" jsonschema:"target a single line by file-absolute line number (1-based, matches symbol body=true output). Mutually exclusive with match/match_regex."`
	FromLine   int      `json:"from_line,omitempty" jsonschema:"first line of a file-absolute line range (1-based, inclusive). Pair with to_line."`
	ToLine     int      `json:"to_line,omitempty" jsonschema:"last line of a file-absolute line range (1-based, inclusive). Must be >= from_line."`
	Params     []string `json:"params,omitempty" jsonschema:"for set_signature: new parameter list as an array of parameter declarations (without parens), e.g. [\"ctx context.Context\", \"x int\"]. Empty keeps the current params."`
	Returns    string   `json:"returns,omitempty" jsonschema:"for set_signature: new result list, e.g. 'error' or '([]byte, error)'. Empty keeps the current returns."`
}

func registerPatchTools(s *mcp.Server, commands service.SurgeonCommands) {
	registerPatchTool(s, commands)
}

// --- ops for patch target=file ---

type filePatchOpInput struct {
	Match        string `json:"match,omitempty" jsonschema:"literal text; all occurrences are replaced. Mutually exclusive with match_regex."`
	MatchRegex   string `json:"match_regex,omitempty" jsonschema:"RE2 regex alternative to match; use $1, $2, ... in replace for submatch substitution. Mutually exclusive with match."`
	Replace      string `json:"replace" jsonschema:"replacement text; supports $1/$2/... when match_regex is used"`
	MatchLiteral bool   `json:"match_literal,omitempty" jsonschema:"when true, treats match_regex as a literal string (applies regexp.QuoteMeta). Use when the pattern contains special characters like postgres://. Mutually exclusive with match."`
	Occurrence   int    `json:"occurrence,omitempty" jsonschema:"0 = replace all occurrences (default); N = replace only the Nth occurrence (1-based). Useful when two similar blocks need separate patches."`
	MatchMode    string `json:"match_mode,omitempty" jsonschema:"'exact' (default) or 'normalized' — normalized collapses whitespace runs before matching, useful for aligned struct/table code"`
}

type structPatchOpInput struct {
	Op       string `json:"op" jsonschema:"operation: add_field, remove_field, rename_field, retype_field, set_tag, set_doc, auto_tag"`
	Name     string `json:"name,omitempty" jsonschema:"target field name (most ops); embed type literal (e.g. io.Reader) for embedded fields"`
	From     string `json:"from,omitempty" jsonschema:"current field name (rename_field only)"`
	To       string `json:"to,omitempty" jsonschema:"new field name (rename_field only)"`
	Type     string `json:"type,omitempty" jsonschema:"field type (add_field / retype_field)"`
	Tag      string `json:"tag,omitempty" jsonschema:"struct tag content without backticks (e.g. json:\"email,omitempty\")"`
	Doc      string `json:"doc,omitempty" jsonschema:"doc comment text (set_doc / add_field); empty string clears the doc"`
	Before   string `json:"before,omitempty" jsonschema:"anchor: insert before this existing field (add_field only)"`
	After    string `json:"after,omitempty" jsonschema:"anchor: insert after this existing field (add_field only)"`
	Position string `json:"position,omitempty" jsonschema:"first or last (add_field only); default is last"`
	Format   string `json:"format,omitempty" jsonschema:"auto_tag only: tag format to generate, e.g. 'json' or 'bson'"`
}

type interfacePatchOpInput struct {
	Op        string `json:"op" jsonschema:"operation: add_method, remove_method, rename_method, retype_method, set_doc, embed, remove_embed"`
	Name      string `json:"name,omitempty" jsonschema:"target method name (most ops)"`
	From      string `json:"from,omitempty" jsonschema:"current method name (rename_method only)"`
	To        string `json:"to,omitempty" jsonschema:"new method name (rename_method only)"`
	Signature string `json:"signature,omitempty" jsonschema:"full method signature including name, e.g. 'Close() error' or 'Read(p []byte) (int, error)'"`
	Type      string `json:"type,omitempty" jsonschema:"embedded interface type, e.g. 'io.Closer' (embed / remove_embed)"`
	Doc       string `json:"doc,omitempty" jsonschema:"doc comment text (set_doc / add_method)"`
	Before    string `json:"before,omitempty" jsonschema:"anchor: insert before this existing member (add_method / embed)"`
	After     string `json:"after,omitempty" jsonschema:"anchor: insert after this existing member (add_method / embed)"`
	Position  string `json:"position,omitempty" jsonschema:"first or last (add_method / embed); default is last"`
}
