package mcpserv

import (
	"context"
	"io"
	"net/http"
	"testing"

	"github.com/charmbracelet/log"
	"github.com/mark3labs/mcp-go/mcp"

	"github.com/awhitty/bb/internal/agentapi"
)

// capturingServer runs setViewTool without a live UI: the Sender records the
// SpecAction the tool builds so we can assert the schema→action translation
// (the converged algebra AND the backward-compat aliases).
func capturingServer(t *testing.T, got *agentapi.SpecAction) *Server {
	t.Helper()
	return &Server{
		logger: log.New(io.Discard),
		send: func(a agentapi.Action) (agentapi.Response, error) {
			sa, ok := a.(agentapi.SpecAction)
			if !ok {
				t.Fatalf("set_view dispatched a %T, want SpecAction", a)
			}
			*got = sa
			return agentapi.Response{Text: "ok"}, nil
		},
	}
}

func callSetView(t *testing.T, s *Server, args map[string]any) *mcp.CallToolResult {
	t.Helper()
	req := mcp.CallToolRequest{}
	req.Params.Arguments = args
	res, err := s.setViewTool(context.Background(), req)
	if err != nil {
		t.Fatalf("setViewTool err: %v", err)
	}
	return res
}

func TestSetViewConvergedAlgebra(t *testing.T) {
	var got agentapi.SpecAction
	s := capturingServer(t, &got)
	callSetView(t, s, map[string]any{
		"mode":     "kanban",
		"traverse": "deps",
		"group":    "label",
		"sort":     map[string]any{"key": "priority", "dir": "asc"},
		"title":    "labels board",
	})
	if got.Mode != "kanban" || got.Traverse != "deps" || got.Group != "label" {
		t.Fatalf("converged vocab lost: %+v", got)
	}
	if got.SortKey != "priority" || got.SortDir != "asc" {
		t.Fatalf("sort object not parsed: key=%q dir=%q", got.SortKey, got.SortDir)
	}
	if got.Title != "labels board" {
		t.Fatalf("title = %q", got.Title)
	}
}

func TestSetViewLegacyAliases(t *testing.T) {
	// mode=relationship still threads root; sort_key/sort_dir still route.
	var got agentapi.SpecAction
	s := capturingServer(t, &got)
	callSetView(t, s, map[string]any{
		"mode": "relationship", "root": "a",
		"sort_key": "subtree-size", "sort_dir": "desc",
	})
	if got.Mode != "relationship" || got.Root != "a" {
		t.Fatalf("legacy relationship alias lost root: %+v", got)
	}
	if got.SortKey != "subtree-size" || got.SortDir != "desc" {
		t.Fatalf("legacy sort_key alias lost: %+v", got)
	}

	// mode=status is a legacy grouped-board alias — still accepted.
	got = agentapi.SpecAction{}
	callSetView(t, s, map[string]any{"mode": "status"})
	if got.Mode != "status" {
		t.Fatalf("legacy status alias rejected: %+v", got)
	}
}

// TestSetViewAdditiveLaneScope covers the additive schema extension: the new lane
// and scope fields decode onto the SpecAction, while an old spec that omits them
// still translates exactly as before (they default to zero — the flat, unscoped
// board).
func TestSetViewAdditiveLaneScope(t *testing.T) {
	// The new fields decode.
	var got agentapi.SpecAction
	s := capturingServer(t, &got)
	callSetView(t, s, map[string]any{
		"mode": "kanban", "group": "status", "lane": "type", "scope": "g-1",
		"title": "cross-tab around g-1",
	})
	if got.Lane != "type" {
		t.Fatalf("lane not decoded: %+v", got)
	}
	if got.Scope != "g-1" {
		t.Fatalf("scope not decoded: %+v", got)
	}

	// An old spec (mode/group/sort, no lane/scope) still decodes with the new
	// fields left empty — backward compatible.
	got = agentapi.SpecAction{}
	callSetView(t, s, map[string]any{
		"mode": "kanban", "group": "type", "sort_key": "title",
	})
	if got.Mode != "kanban" || got.Group != "type" || got.SortKey != "title" {
		t.Fatalf("old spec lost a field: %+v", got)
	}
	if got.Lane != "" || got.Scope != "" {
		t.Fatalf("old spec should leave lane/scope empty: lane=%q scope=%q", got.Lane, got.Scope)
	}

	// An unknown lane facet is rejected, like the group vocabulary.
	res := callSetView(t, s, map[string]any{"lane": "phase"})
	if res == nil || !res.IsError {
		t.Fatal("expected a validation error for lane=phase")
	}
}

// The `session` argument is carried onto the SpecAction so the push attributes
// to the originating Claude Code session; absent it, the request-scoped context
// session (the header plumbed via WithHTTPContextFunc) is the fallback, and an
// explicit arg wins over the context.
func TestSetViewCarriesSession(t *testing.T) {
	var got agentapi.SpecAction
	s := capturingServer(t, &got)

	callSetView(t, s, map[string]any{"mode": "tree", "session": "sess-42"})
	if got.Session != "sess-42" {
		t.Fatalf("session arg not carried: %q", got.Session)
	}

	got = agentapi.SpecAction{}
	ctx := context.WithValue(context.Background(), sessionCtxKey{}, "ctx-sess")
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{"mode": "tree"}
	if _, err := s.setViewTool(ctx, req); err != nil {
		t.Fatalf("setViewTool err: %v", err)
	}
	if got.Session != "ctx-sess" {
		t.Fatalf("context session not used as fallback: %q", got.Session)
	}

	got = agentapi.SpecAction{}
	req.Params.Arguments = map[string]any{"mode": "tree", "session": "arg-wins"}
	if _, err := s.setViewTool(ctx, req); err != nil {
		t.Fatalf("setViewTool err: %v", err)
	}
	if got.Session != "arg-wins" {
		t.Fatalf("explicit session arg should win over context: %q", got.Session)
	}
}

// sessionContextFunc lifts the X-Beads-Session header into the request context;
// an absent header leaves the context session empty (the unattributed fallback).
func TestSessionContextFuncReadsHeader(t *testing.T) {
	r, _ := http.NewRequest(http.MethodPost, "/mcp", nil)
	r.Header.Set("X-Beads-Session", "hdr-sess")
	if got := sessionFromContext(sessionContextFunc(context.Background(), r)); got != "hdr-sess" {
		t.Fatalf("header not lifted into context: %q", got)
	}

	r2, _ := http.NewRequest(http.MethodPost, "/mcp", nil)
	if got := sessionFromContext(sessionContextFunc(context.Background(), r2)); got != "" {
		t.Fatalf("absent header should leave the context session empty, got %q", got)
	}
}

func TestSetViewRejectsUnknownTokens(t *testing.T) {
	var got agentapi.SpecAction
	s := capturingServer(t, &got)
	for _, args := range []map[string]any{
		{"mode": "nonsense"},
		{"traverse": "sideways"},
		{"group": "phase"},
		{"sort_key": "vibes"},
	} {
		res := callSetView(t, s, args)
		if res == nil || !res.IsError {
			t.Fatalf("expected a validation error for %v", args)
		}
	}
}
