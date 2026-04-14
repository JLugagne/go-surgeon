package converters

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"

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
