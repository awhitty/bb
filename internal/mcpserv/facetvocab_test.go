package mcpserv

import (
	"reflect"
	"testing"

	"github.com/awhitty/bb/internal/agentapi"
	"github.com/awhitty/bb/internal/rollup"
	"github.com/awhitty/bb/internal/ui"
)

// TestSetViewAcceptsDepthAndPriority is the demo-rst.11 regression: group=depth
// (the facet added after the MCP's old hand-written enum) once returned "group
// must be one of …". It now publishes onto the spec, alongside lane=priority.
func TestSetViewAcceptsDepthAndPriority(t *testing.T) {
	var got agentapi.SpecAction
	s := capturingServer(t, &got)
	res := callSetView(t, s, map[string]any{
		"group": "depth", "lane": "priority", "title": "tech tree by priority",
	})
	if res == nil || res.IsError {
		t.Fatalf("group=depth lane=priority rejected: %+v", res)
	}
	if got.Group != "depth" || got.Lane != "priority" {
		t.Fatalf("depth/priority not carried onto the published spec: group=%q lane=%q", got.Group, got.Lane)
	}
}

// TestSetViewFacetSetMatchesUI is the anti-drift lock: the facets set_view accepts
// are EXACTLY the facets the UI grouper/lane controls offer, because both derive
// from rollup.FacetBindings. Each advertised token is driven through the real
// set_view handler (group AND lane) to prove acceptance, then the resolved facet
// sets are compared.
func TestSetViewFacetSetMatchesUI(t *testing.T) {
	uiFacets := map[rollup.Facet]bool{}
	for _, f := range ui.OfferedFacets() {
		uiFacets[f] = true
	}

	var got agentapi.SpecAction
	s := capturingServer(t, &got)

	mcpFacets := map[rollup.Facet]bool{}
	for _, name := range rollup.FacetNames() {
		f, ok := rollup.FacetFromName(name)
		if !ok {
			t.Fatalf("advertised facet token %q does not resolve", name)
		}
		mcpFacets[f] = true

		got = agentapi.SpecAction{}
		if res := callSetView(t, s, map[string]any{"group": name}); res == nil || res.IsError {
			t.Fatalf("group=%q rejected by set_view: %+v", name, res)
		}
		if got.Group != name {
			t.Fatalf("group=%q not carried onto the spec: %q", name, got.Group)
		}

		got = agentapi.SpecAction{}
		if res := callSetView(t, s, map[string]any{"lane": name}); res == nil || res.IsError {
			t.Fatalf("lane=%q rejected by set_view: %+v", name, res)
		}
		if got.Lane != name {
			t.Fatalf("lane=%q not carried onto the spec: %q", name, got.Lane)
		}
	}

	if !reflect.DeepEqual(uiFacets, mcpFacets) {
		t.Fatalf("facet drift: UI offers %v, MCP accepts %v", uiFacets, mcpFacets)
	}
}

// TestSetViewLegacyFacetsStillAccepted keeps the pre-registry vocabulary — every
// value the old hand-written enum accepted, including the "root" alias — working.
func TestSetViewLegacyFacetsStillAccepted(t *testing.T) {
	var got agentapi.SpecAction
	s := capturingServer(t, &got)
	for _, tok := range []string{"none", "status", "type", "ancestor", "root", "blockers", "label", "priority"} {
		got = agentapi.SpecAction{}
		if res := callSetView(t, s, map[string]any{"group": tok}); res == nil || res.IsError {
			t.Fatalf("legacy group=%q rejected: %+v", tok, res)
		}
		if got.Group != tok {
			t.Fatalf("legacy group=%q not carried: %q", tok, got.Group)
		}
	}
}
