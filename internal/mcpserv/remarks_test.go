package mcpserv

import (
	"context"
	"io"
	"testing"

	"github.com/charmbracelet/log"
	"github.com/mark3labs/mcp-go/mcp"

	"github.com/awhitty/bb/internal/agentapi"
)

// set_view carries per-bead remarks onto the SpecAction; a spec that omits them
// leaves the field nil (additive — old callers are unaffected).
func TestSetViewCarriesRemarks(t *testing.T) {
	var got agentapi.SpecAction
	s := capturingServer(t, &got)
	callSetView(t, s, map[string]any{
		"ids":     []any{"a", "b"},
		"remarks": map[string]any{"a": "why a", "b": "why b"},
		"title":   "review",
	})
	if got.Remarks["a"] != "why a" || got.Remarks["b"] != "why b" {
		t.Fatalf("remarks not decoded: %+v", got.Remarks)
	}

	got = agentapi.SpecAction{}
	callSetView(t, s, map[string]any{"mode": "list"})
	if got.Remarks != nil {
		t.Fatalf("a spec without remarks should leave Remarks nil, got %+v", got.Remarks)
	}

	// Empty / whitespace entries are dropped, so a blank note never draws a callout.
	got = agentapi.SpecAction{}
	callSetView(t, s, map[string]any{"mode": "list", "remarks": map[string]any{"a": "   ", "b": ""}})
	if got.Remarks != nil {
		t.Fatalf("blank remarks should decode to nil, got %+v", got.Remarks)
	}
}

// show carries per-bead remarks onto the ShowAction.
func TestShowCarriesRemarks(t *testing.T) {
	var got agentapi.ShowAction
	s := &Server{
		logger: log.New(io.Discard),
		send: func(a agentapi.Action) (agentapi.Response, error) {
			sa, ok := a.(agentapi.ShowAction)
			if !ok {
				t.Fatalf("show dispatched a %T, want ShowAction", a)
			}
			got = sa
			return agentapi.Response{Text: "ok"}, nil
		},
	}
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{
		"ids":     []any{"a"},
		"remarks": map[string]any{"a": "picked for review"},
		"title":   "review",
	}
	if _, err := s.showTool(context.Background(), req); err != nil {
		t.Fatalf("showTool err: %v", err)
	}
	if got.Remarks["a"] != "picked for review" {
		t.Fatalf("show remarks not decoded: %+v", got.Remarks)
	}
}
