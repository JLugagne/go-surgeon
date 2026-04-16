package mcp

import (
	"context"
	"encoding/json"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// patchToolNames lists the tools whose 'patches' field must be an array and
// which are frequently serialized as a JSON-encoded string by buggy clients.
var patchToolNames = map[string]struct{}{
	"patch_function":  {},
	"patch_struct":    {},
	"patch_interface": {},
	"patch_decl":      {},
}

// schemaHintMiddleware intercepts tools/call before the SDK validates the
// arguments against the tool input schema. When the 'patches' field arrives
// as a JSON-encoded string instead of an array (a common client-side
// serialization bug), the SDK returns the opaque error
//   type: [...] has type "string", want one of "null, array"
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
			if msg := detectPatchesStringMismatch(params.Arguments); msg != "" {
				res := &mcp.CallToolResult{
					Content: []mcp.Content{&mcp.TextContent{Text: msg}},
					IsError: true,
				}
				return res, nil
			}
			return next(ctx, method, req)
		}
	}
}

// detectPatchesStringMismatch returns an actionable error message when the
// 'patches' field of the raw arguments JSON is a string rather than an array.
// Returns "" if no mismatch is detected, or if the arguments are not valid
// JSON (the SDK's own validator will surface that separately).
func detectPatchesStringMismatch(args json.RawMessage) string {
	if len(args) == 0 {
		return ""
	}
	// Decode only the 'patches' field, leaving others as json.RawMessage.
	var peek struct {
		Patches json.RawMessage `json:"patches"`
	}
	if err := json.Unmarshal(args, &peek); err != nil {
		return ""
	}
	if len(peek.Patches) == 0 {
		return ""
	}
	trimmed := skipJSONWhitespace(peek.Patches)
	if len(trimmed) == 0 || trimmed[0] != '"' {
		return ""
	}
	// Confirm the string payload is itself a JSON array (double-serialization),
	// not just an unrelated string like "patches": "hello".
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
		"This is a client-side serialization issue: send 'patches' as a raw JSON array, not as a string containing JSON.\n" +
		"Workaround: retry the same call (some clients are intermittent), or fall back to " +
		"update_interface / update_struct / update with the full declaration."
}
