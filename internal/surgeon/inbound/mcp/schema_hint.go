package mcp

import (
	"context"
	"encoding/json"

	"github.com/JLugagne/go-surgeon/internal/surgeon/domain"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// patchToolNames lists the tools whose 'patches' field must be an array and
// which are frequently serialized as a JSON-encoded string by buggy clients.
var patchToolNames = map[string]struct{}{
	"patch": {},
}

// schemaHintMiddleware intercepts tools/call before the SDK validates the
// arguments against the tool input schema. When the 'patches' field arrives
// as a JSON-encoded string instead of an array (a common client-side
// serialization bug), the SDK returns the opaque error
//
//	type: [...] has type "string", want one of "null, array"
//
// which is hard for the agent to act on. This middleware replaces that with an
// actionable message before the SDK validator ever sees the request.
func schemaHintMiddleware() mcp.Middleware {
	return func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			if method != "tools/call" {
				return next(ctx, method, req)
			}
			params, ok := req.GetParams().(*mcp.CallToolParamsRaw)
			if !ok || params == nil {
				return next(ctx, method, req)
			}
			if _, isPatchTool := patchToolNames[params.Name]; !isPatchTool {
				return next(ctx, method, req)
			}
			if fixed, ok := recoverDoubleEncodedPatches(params.Arguments); ok {
				params.Arguments = fixed
			} else if msg := detectPatchesStringMismatch(params.Arguments); msg != "" {
				return errorResultWithCode(msg, &domain.Error{Code: "INVALID_ARGUMENT", Message: msg}), nil
			}
			if msg := detectPatchOpFieldTypeMismatch(params.Arguments); msg != "" {
				return errorResultWithCode(msg, &domain.Error{Code: "INVALID_ARGUMENT", Message: msg}), nil
			}
			return next(ctx, method, req)
		}
	}
}

// detectPatchesStringMismatch returns an actionable error message when the
// 'patches' field of the raw arguments JSON is a string rather than an array.
// The new patch tool nests patches under items[*].patches; for backward
// compatibility with clients that still send a top-level 'patches' field
// (or for the unwrapped middleware test path), this helper also probes the
// top-level field. Returns "" when no mismatch is detected.
func detectPatchesStringMismatch(args json.RawMessage) string {
	if len(args) == 0 {
		return ""
	}
	// First check the top-level 'patches' field (legacy / direct shape).
	if msg := patchesStringMismatchFor(args); msg != "" {
		return msg
	}
	// Then descend into items[*].patches.
	var peek struct {
		Items []json.RawMessage `json:"items"`
	}
	if err := json.Unmarshal(args, &peek); err != nil {
		return ""
	}
	for _, item := range peek.Items {
		if msg := patchesStringMismatchFor(item); msg != "" {
			return msg
		}
	}
	return ""
}

// patchesStringMismatchFor reports the "patches is a string" error for one
// JSON object (the top-level args, or one items[] entry). Returns "" when
// there is no mismatch.
func patchesStringMismatchFor(obj json.RawMessage) string {
	if len(obj) == 0 {
		return ""
	}
	var peek struct {
		Patches json.RawMessage `json:"patches"`
	}
	if err := json.Unmarshal(obj, &peek); err != nil {
		return ""
	}
	if len(peek.Patches) == 0 {
		return ""
	}
	trimmed := skipJSONWhitespace(peek.Patches)
	if len(trimmed) == 0 || trimmed[0] != '"' {
		return ""
	}
	var inner string
	if err := json.Unmarshal(peek.Patches, &inner); err != nil {
		return ""
	}
	innerTrimmed := skipJSONWhitespace([]byte(inner))
	looksLikeArray := len(innerTrimmed) > 0 && innerTrimmed[0] == '['
	return formatPatchesMismatchError(looksLikeArray)
}

func skipJSONWhitespace(b []byte) []byte {
	for i, c := range b {
		if c != ' ' && c != '\t' && c != '\n' && c != '\r' {
			return b[i:]
		}
	}
	return nil
}

func formatPatchesMismatchError(doubleSerialized bool) string {
	header := "ERROR: 'patches' arrived as a JSON-encoded string instead of an array."
	if doubleSerialized {
		header += " The inner value does parse as an array, so the payload was serialized twice."
	}
	return header + "\n" +
		"Send 'patches' as a raw JSON array, not as a string containing JSON. " +
		"(If your client did send a raw array, this may be a server-side false positive — please file an issue.)\n" +
		"Workaround: retry the same call (some clients are intermittent), or fall back to " +
		"update_interface / update_struct / update with the full declaration."
}

// stringPatchFields lists the fields inside a single patch op that MUST
// arrive as JSON strings. When a buggy client serializes a multi-line value
// as a JSON array (e.g. one element per line) instead of a string with
// embedded newlines, the SDK's default validator returns the same opaque
// "type ... has type ... want string" message that triggered issue #3.
var stringPatchFields = []string{
	"replace",
	"match",
	"match_regex",
	"code",
	"wrap",
	"signature",
	"type",
	"tag",
	"doc",
	"name",
	"from",
	"to",
	"before",
	"after",
}

// detectPatchOpFieldTypeMismatch inspects every entry in the 'patches'
// array and reports the first patch op that has a non-string value in a
// field that is documented as a string. Returns "" when patches is missing
// or well-typed.
//
// The motivation is the silent-data-loss path described in issue #3:
// agents occasionally send `replace: ["line1", "line2"]` instead of the
// equivalent `replace: "line1\nline2"`, and the resulting validation error
// is opaque enough that they don't act on it. This helper short-circuits
// with an actionable message naming the offending patch index and field.
func detectPatchOpFieldTypeMismatch(args json.RawMessage) string {
	if len(args) == 0 {
		return ""
	}
	// Check top-level 'patches' first for legacy shape.
	if msg := patchOpFieldTypeMismatchFor(args); msg != "" {
		return msg
	}
	var peek struct {
		Items []json.RawMessage `json:"items"`
	}
	if err := json.Unmarshal(args, &peek); err != nil {
		return ""
	}
	for _, item := range peek.Items {
		if msg := patchOpFieldTypeMismatchFor(item); msg != "" {
			return msg
		}
	}
	return ""
}

// patchOpFieldTypeMismatchFor inspects the 'patches' array on a single JSON
// object (the top-level args, or one items[] entry) and reports the first
// patch op that has a non-string value in a field documented as a string.
func patchOpFieldTypeMismatchFor(obj json.RawMessage) string {
	if len(obj) == 0 {
		return ""
	}
	var peek struct {
		Patches json.RawMessage `json:"patches"`
	}
	if err := json.Unmarshal(obj, &peek); err != nil {
		return ""
	}
	if len(peek.Patches) == 0 {
		return ""
	}
	trimmed := skipJSONWhitespace(peek.Patches)
	if len(trimmed) == 0 || trimmed[0] != '[' {
		return ""
	}
	var ops []map[string]json.RawMessage
	if err := json.Unmarshal(peek.Patches, &ops); err != nil {
		return ""
	}
	for i, op := range ops {
		for _, field := range stringPatchFields {
			raw, present := op[field]
			if !present || len(raw) == 0 {
				continue
			}
			head := skipJSONWhitespace(raw)
			if len(head) == 0 {
				continue
			}
			if head[0] == '"' || (head[0] == 'n' && string(head) == "null") {
				continue
			}
			return formatPatchOpFieldTypeMismatchError(i+1, field, head[0])
		}
	}
	return ""
}

// formatPatchOpFieldTypeMismatchError builds the agent-facing message when
// a patch op field has the wrong JSON type. The first byte of the offending
// value is included to disambiguate array vs object vs number.
func formatPatchOpFieldTypeMismatchError(index int, field string, firstByte byte) string {
	kind := "value"
	switch firstByte {
	case '[':
		kind = "JSON array"
	case '{':
		kind = "JSON object"
	case 't', 'f':
		kind = "boolean"
	default:
		if firstByte >= '0' && firstByte <= '9' {
			kind = "number"
		} else if firstByte == '-' {
			kind = "number"
		}
	}
	return "ERROR: patch #" + itoa(index) + " field " + field + " arrived as a " + kind + ", but a string is required.\n" +
		"Send '" + field + "' as a JSON string. Multi-line values use embedded \\n escapes: \"line1\\nline2\". " +
		"Do NOT split lines into a JSON array."
}

// itoa is a tiny dependency-free integer formatter so this file stays
// limited to encoding/json + the SDK package.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

// recoverDoubleEncodedPatches detects the issue #21 false-positive scenario
// (and the legitimate double-encoding case) by checking whether 'patches' is
// a JSON string whose content parses as a JSON array. When it does, we treat
// the inner array as the intended payload, splice it back into the arguments
// JSON, and let the request continue normally — instead of rejecting it with
// an actionable-but-wrong "client serialization bug" error. This is robust
// against unknown client wrappers that re-encode complex args, and it is
// strictly an improvement: the previous behaviour was an outright rejection.
//
// Returns the rewritten arguments and ok=true on recovery, otherwise zero
// values. Callers must fall back to the existing detection path when ok=false
// so plain non-array strings (e.g. "patches": "label") still produce the
// original actionable error.
func recoverDoubleEncodedPatches(args json.RawMessage) (json.RawMessage, bool) {
	if len(args) == 0 {
		return nil, false
	}
	var top map[string]json.RawMessage
	if err := json.Unmarshal(args, &top); err != nil {
		return nil, false
	}
	changed := false
	// Top-level 'patches' (legacy / direct shape).
	if fixed, ok := tryDecodeStringEncodedArray(top["patches"]); ok {
		top["patches"] = fixed
		changed = true
	}
	// items[*].patches (new shape).
	if rawItems, present := top["items"]; present && len(rawItems) > 0 {
		var items []map[string]json.RawMessage
		if err := json.Unmarshal(rawItems, &items); err == nil {
			itemsChanged := false
			for _, it := range items {
				if fixed, ok := tryDecodeStringEncodedArray(it["patches"]); ok {
					it["patches"] = fixed
					itemsChanged = true
				}
			}
			if itemsChanged {
				if buf, err := json.Marshal(items); err == nil {
					top["items"] = buf
					changed = true
				}
			}
		}
	}
	if !changed {
		return nil, false
	}
	rewritten, err := json.Marshal(top)
	if err != nil {
		return nil, false
	}
	return rewritten, true
}

// tryDecodeStringEncodedArray decodes a JSON value that is a string whose
// contents parse as a JSON array, returning the inner array bytes and
// ok=true. Used to recover double-encoded 'patches' fields.
func tryDecodeStringEncodedArray(raw json.RawMessage) (json.RawMessage, bool) {
	if len(raw) == 0 {
		return nil, false
	}
	trimmed := skipJSONWhitespace(raw)
	if len(trimmed) == 0 || trimmed[0] != '"' {
		return nil, false
	}
	var inner string
	if err := json.Unmarshal(raw, &inner); err != nil {
		return nil, false
	}
	innerTrimmed := skipJSONWhitespace([]byte(inner))
	if len(innerTrimmed) == 0 || innerTrimmed[0] != '[' {
		return nil, false
	}
	var probe []json.RawMessage
	if err := json.Unmarshal([]byte(inner), &probe); err != nil {
		return nil, false
	}
	return json.RawMessage(inner), true
}
