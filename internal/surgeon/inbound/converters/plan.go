package converters

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/JLugagne/go-surgeon/internal/surgeon/domain"
	"gopkg.in/yaml.v3"
)

// Format selects the plan encoding. "" means auto-detect.
const (
	FormatAuto = ""
	FormatJSON = "json"
	FormatYAML = "yaml"
)

// flexBool accepts true/false as native booleans or as the strings
// "true"/"false" (case-insensitive). Agents often emit stringified bools.
type flexBool bool

func (b *flexBool) UnmarshalJSON(data []byte) error {
	return unmarshalFlexBool(data, b, true)
}

func (b *flexBool) UnmarshalYAML(node *yaml.Node) error {
	var raw string
	switch node.Tag {
	case "!!bool":
		v, err := strconv.ParseBool(node.Value)
		if err != nil {
			return fmt.Errorf("invalid bool %q: %w", node.Value, err)
		}
		*b = flexBool(v)
		return nil
	case "!!str":
		raw = node.Value
	default:
		raw = node.Value
	}
	v, err := strconv.ParseBool(raw)
	if err != nil {
		return fmt.Errorf("invalid bool %q: expected true/false (or \"true\"/\"false\")", raw)
	}
	*b = flexBool(v)
	return nil
}

func unmarshalFlexBool(data []byte, b *flexBool, fromJSON bool) error {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 {
		return fmt.Errorf("empty bool value")
	}
	if trimmed[0] == '"' {
		var s string
		if err := json.Unmarshal(trimmed, &s); err != nil {
			return err
		}
		v, err := strconv.ParseBool(s)
		if err != nil {
			return fmt.Errorf("invalid bool %q: expected \"true\" or \"false\"", s)
		}
		*b = flexBool(v)
		return nil
	}
	v, err := strconv.ParseBool(string(trimmed))
	if err != nil {
		return fmt.Errorf("invalid bool %q: expected true/false (or \"true\"/\"false\")", string(trimmed))
	}
	*b = flexBool(v)
	return nil
}

// actionDTO mirrors domain.Action but uses flexBool for bool fields so string
// values are accepted. Kept in the inbound layer to avoid polluting the domain.
type actionDTO struct {
	Action      domain.ActionType     `yaml:"action" json:"action"`
	FilePath    string                `yaml:"file" json:"file"`
	PackagePath string                `yaml:"package" json:"package"`
	Identifier  string                `yaml:"identifier" json:"identifier"`
	Content     string                `yaml:"content" json:"content"`
	MockFile    string                `yaml:"mock_file" json:"mock_file"`
	MockName    string                `yaml:"mock_name" json:"mock_name"`
	Doc         string                `yaml:"doc" json:"doc"`
	StripDoc    flexBool              `yaml:"strip_doc" json:"strip_doc"`
	Position    domain.InsertPosition `yaml:"position" json:"position"`
	WithTest    flexBool              `yaml:"with_test" json:"with_test"`
	// Patch-action operation lists. Shapes mirror inbound/mcp/tools_plan.go so
	// the CLI `execute -f` workflow can express the same patch_* actions the MCP
	// execute_plan tool supports.
	PatchFunctionOps  []funcPatchOpDTO      `yaml:"patch_function_ops" json:"patch_function_ops"`
	PatchStructOps    []structPatchOpDTO    `yaml:"patch_struct_ops" json:"patch_struct_ops"`
	PatchInterfaceOps []interfacePatchOpDTO `yaml:"patch_interface_ops" json:"patch_interface_ops"`
	PatchFileOps      []filePatchOpDTO      `yaml:"patch_file_ops" json:"patch_file_ops"`
	PatchDeclOps      []funcPatchOpDTO      `yaml:"patch_decl_ops" json:"patch_decl_ops"`
}

// funcPatchOpDTO mirrors mcp.patchOpInput (shared by patch_function and
// patch_decl). Params is a list of parameter declarations without parens; it
// is joined into the parenthesised form domain.FunctionPatch.Params expects.
type funcPatchOpDTO struct {
	Op         domain.PatchOp `yaml:"op" json:"op"`
	Match      string         `yaml:"match" json:"match"`
	MatchRegex string         `yaml:"match_regex" json:"match_regex"`
	Occurrence int            `yaml:"occurrence" json:"occurrence"`
	Replace    string         `yaml:"replace" json:"replace"`
	Code       string         `yaml:"code" json:"code"`
	Wrap       string         `yaml:"wrap" json:"wrap"`
	AtLine     int            `yaml:"at_line" json:"at_line"`
	FromLine   int            `yaml:"from_line" json:"from_line"`
	ToLine     int            `yaml:"to_line" json:"to_line"`
	Params     []string       `yaml:"params" json:"params"`
	Returns    string         `yaml:"returns" json:"returns"`
}

// structPatchOpDTO mirrors mcp.structPatchOpInput.
type structPatchOpDTO struct {
	Op       domain.StructPatchOp `yaml:"op" json:"op"`
	Name     string               `yaml:"name" json:"name"`
	From     string               `yaml:"from" json:"from"`
	To       string               `yaml:"to" json:"to"`
	Type     string               `yaml:"type" json:"type"`
	Tag      string               `yaml:"tag" json:"tag"`
	Doc      string               `yaml:"doc" json:"doc"`
	Before   string               `yaml:"before" json:"before"`
	After    string               `yaml:"after" json:"after"`
	Position string               `yaml:"position" json:"position"`
}

// interfacePatchOpDTO mirrors mcp.interfacePatchOpInput.
type interfacePatchOpDTO struct {
	Op        domain.InterfacePatchOp `yaml:"op" json:"op"`
	Name      string                  `yaml:"name" json:"name"`
	From      string                  `yaml:"from" json:"from"`
	To        string                  `yaml:"to" json:"to"`
	Signature string                  `yaml:"signature" json:"signature"`
	Type      string                  `yaml:"type" json:"type"`
	Doc       string                  `yaml:"doc" json:"doc"`
	Before    string                  `yaml:"before" json:"before"`
	After     string                  `yaml:"after" json:"after"`
	Position  string                  `yaml:"position" json:"position"`
}

// filePatchOpDTO mirrors mcp.filePatchOpInput.
type filePatchOpDTO struct {
	Match        string `yaml:"match" json:"match"`
	MatchRegex   string `yaml:"match_regex" json:"match_regex"`
	Replace      string `yaml:"replace" json:"replace"`
	MatchLiteral bool   `yaml:"match_literal" json:"match_literal"`
	Occurrence   int    `yaml:"occurrence" json:"occurrence"`
	MatchMode    string `yaml:"match_mode" json:"match_mode"`
}

type planDTO struct {
	Actions []actionDTO `yaml:"actions" json:"actions"`
}

func (p planDTO) toDomain() domain.Plan {
	out := domain.Plan{Actions: make([]domain.Action, len(p.Actions))}
	for i, a := range p.Actions {
		out.Actions[i] = domain.Action{
			Action:      a.Action,
			FilePath:    a.FilePath,
			PackagePath: a.PackagePath,
			Identifier:  a.Identifier,
			Content:     a.Content,
			MockFile:    a.MockFile,
			MockName:    a.MockName,
			Doc:         a.Doc,
			StripDoc:    bool(a.StripDoc),
			Position:    a.Position,
			WithTest:    bool(a.WithTest),

			PatchFunctionOps:  toDomainFuncPatches(a.PatchFunctionOps),
			PatchStructOps:    toDomainStructPatches(a.PatchStructOps),
			PatchInterfaceOps: toDomainInterfacePatches(a.PatchInterfaceOps),
			PatchFileOps:      toDomainFilePatches(a.PatchFileOps),
			PatchDeclOps:      toDomainFuncPatches(a.PatchDeclOps),
		}
	}
	return out
}

// joinParams renders parameter declarations into the parenthesised list form
// expected by domain.FunctionPatch.Params. Empty input yields "" (keep current
// params). Mirrors mcp.joinParams.
func joinParams(parts []string) string {
	if len(parts) == 0 {
		return ""
	}
	return "(" + strings.Join(parts, ", ") + ")"
}

func toDomainFuncPatches(in []funcPatchOpDTO) []domain.FunctionPatch {
	if len(in) == 0 {
		return nil
	}
	out := make([]domain.FunctionPatch, len(in))
	for i, p := range in {
		out[i] = domain.FunctionPatch{
			Op:         p.Op,
			Match:      p.Match,
			MatchRegex: p.MatchRegex,
			Occurrence: p.Occurrence,
			Replace:    p.Replace,
			Code:       p.Code,
			Wrap:       p.Wrap,
			AtLine:     p.AtLine,
			FromLine:   p.FromLine,
			ToLine:     p.ToLine,
			Params:     joinParams(p.Params),
			Returns:    p.Returns,
		}
	}
	return out
}

func toDomainStructPatches(in []structPatchOpDTO) []domain.StructPatch {
	if len(in) == 0 {
		return nil
	}
	out := make([]domain.StructPatch, len(in))
	for i, p := range in {
		out[i] = domain.StructPatch{
			Op:       p.Op,
			Name:     p.Name,
			From:     p.From,
			To:       p.To,
			Type:     p.Type,
			Tag:      p.Tag,
			Doc:      p.Doc,
			Before:   p.Before,
			After:    p.After,
			Position: p.Position,
		}
	}
	return out
}

func toDomainInterfacePatches(in []interfacePatchOpDTO) []domain.InterfacePatch {
	if len(in) == 0 {
		return nil
	}
	out := make([]domain.InterfacePatch, len(in))
	for i, p := range in {
		out[i] = domain.InterfacePatch{
			Op:        p.Op,
			Name:      p.Name,
			From:      p.From,
			To:        p.To,
			Signature: p.Signature,
			Type:      p.Type,
			Doc:       p.Doc,
			Before:    p.Before,
			After:     p.After,
			Position:  p.Position,
		}
	}
	return out
}

func toDomainFilePatches(in []filePatchOpDTO) []domain.FilePatch {
	if len(in) == 0 {
		return nil
	}
	out := make([]domain.FilePatch, len(in))
	for i, p := range in {
		out[i] = domain.FilePatch{
			Match:        p.Match,
			MatchRegex:   p.MatchRegex,
			Replace:      p.Replace,
			MatchLiteral: p.MatchLiteral,
			Occurrence:   p.Occurrence,
			MatchMode:    p.MatchMode,
		}
	}
	return out
}

// detectFormat inspects the first non-whitespace byte: '{' or '[' means JSON,
// anything else (or empty) falls back to YAML.
func detectFormat(data []byte) string {
	for _, c := range data {
		switch c {
		case ' ', '\t', '\r', '\n':
			continue
		case '{', '[':
			return FormatJSON
		default:
			return FormatYAML
		}
	}
	return FormatYAML
}

// ToDomainPlan converts plan bytes into a domain.Plan.
//
// format may be "json", "yaml", or "" (auto-detect via the first non-whitespace
// byte: '{' or '[' → JSON, otherwise YAML). Unknown fields are rejected in both
// formats to catch typos like "symbol" instead of "identifier".
//
// Bool fields (strip_doc, with_test) accept both native booleans and the
// strings "true"/"false".
func ToDomainPlan(data []byte, format string) (domain.Plan, error) {
	if format == FormatAuto {
		format = detectFormat(data)
	}

	var dto planDTO
	switch format {
	case FormatJSON:
		dec := json.NewDecoder(bytes.NewReader(data))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&dto); err != nil {
			return domain.Plan{}, fmt.Errorf("failed to unmarshal JSON plan: %w", err)
		}
	case FormatYAML:
		dec := yaml.NewDecoder(bytes.NewReader(data))
		dec.KnownFields(true)
		if err := dec.Decode(&dto); err != nil {
			return domain.Plan{}, fmt.Errorf("failed to unmarshal YAML plan: %w", err)
		}
	default:
		return domain.Plan{}, fmt.Errorf("unknown plan format %q (expected \"json\", \"yaml\", or \"\" for auto-detect)", format)
	}

	return dto.toDomain(), nil
}
