package discovery_test

import (
	"encoding/json"
	"testing"

	"github.com/JLugagne/go-surgeon/internal/surgeon/discovery"
)

// TestCatalogExamples_ValidJSON guards against doc-string rot: every
// Example string in the catalog (top-level tools and per-op entries) that
// looks like a JSON object must actually parse. This is what an agent
// copies verbatim, so a syntax error here reaches a real tool call.
func TestCatalogExamples_ValidJSON(t *testing.T) {
	checkExample := func(t *testing.T, label, example string) {
		t.Helper()
		if example == "" || example[0] != '{' {
			return
		}
		var v map[string]any
		if err := json.Unmarshal([]byte(example), &v); err != nil {
			t.Errorf("%s: example is not valid JSON: %v\nexample: %s", label, err, example)
		}
	}

	for _, entry := range discovery.Catalog {
		checkExample(t, "Catalog["+entry.Name+"]", entry.Example)
		for opName, op := range entry.Ops {
			checkExample(t, "Catalog["+entry.Name+"]."+opName, op.Example)
		}
	}

	opMaps := map[string]map[string]discovery.ToolOpEntry{
		"PatchOps":          discovery.PatchOps,
		"PatchFunctionOps":  discovery.PatchFunctionOps,
		"PatchStructOps":    discovery.PatchStructOps,
		"PatchInterfaceOps": discovery.PatchInterfaceOps,
		"PatchDeclOps":      discovery.PatchDeclOps,
	}
	for mapName, ops := range opMaps {
		for opName, op := range ops {
			checkExample(t, mapName+"."+opName, op.Example)
		}
	}
}

// TestCatalogEmbedOps_UseTypeField guards issue #21: interface embed /
// remove_embed operate on the engine's Type field (domain.InterfacePatch.Type),
// not Name — the doc must tell agents to send "type", not "name".
func TestCatalogEmbedOps_UseTypeField(t *testing.T) {
	for _, opName := range []string{"embed", "remove_embed"} {
		op, ok := discovery.PatchInterfaceOps[opName]
		if !ok {
			t.Fatalf("missing PatchInterfaceOps[%q]", opName)
		}
		found := false
		for _, req := range op.Required {
			if req == "patches[].type" {
				found = true
			}
			if req == "patches[].name" {
				t.Errorf("PatchInterfaceOps[%q].Required lists patches[].name, but the engine reads Type — must be patches[].type", opName)
			}
		}
		if !found {
			t.Errorf("PatchInterfaceOps[%q].Required must list patches[].type", opName)
		}
	}
}

// TestCatalogSetDoc_InterfaceRequiresName guards issue #21: interface set_doc
// is a member-level op (domain patch_interface.go requires p.Name), not
// interface-level — the doc must require patches[].name.
func TestCatalogSetDoc_InterfaceRequiresName(t *testing.T) {
	op, ok := discovery.PatchInterfaceOps["set_doc"]
	if !ok {
		t.Fatal("missing PatchInterfaceOps[\"set_doc\"]")
	}
	found := false
	for _, req := range op.Required {
		if req == "patches[].name" {
			found = true
		}
	}
	if !found {
		t.Errorf("PatchInterfaceOps[\"set_doc\"].Required must list patches[].name (set_doc is member-level, not interface-level)")
	}
}
