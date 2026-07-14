package mcpserv

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/charmbracelet/log"

	"github.com/awhitty/bb/internal/agentapi"
)

// The /ingest route is the hook-ingest seam: the Claude Code Stop hook POSTs a
// name-drop batch (the ended session's id, its conversation title, and the bead
// ids it named) to the SAME localhost port + bearer token, and the handler
// marshals a NameDropAction onto the UI loop — which registers/refreshes that
// session's channel. This drives the transport: a real batch reaches the sender
// carrying the session id + title; an empty batch is a quiet no-op.
func TestIngestHandlerRegistersSession(t *testing.T) {
	var got *agentapi.NameDropAction
	s := &Server{
		logger: log.New(io.Discard),
		send: func(a agentapi.Action) (agentapi.Response, error) {
			nd, ok := a.(agentapi.NameDropAction)
			if !ok {
				t.Fatalf("ingest dispatched a %T, want NameDropAction", a)
			}
			got = &nd
			return agentapi.Response{Text: "ok"}, nil
		},
	}

	post := func(body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/ingest", bytes.NewReader([]byte(body)))
		rec := httptest.NewRecorder()
		s.ingestHandler(rec, req)
		return rec
	}

	// A real name-drop batch: session id + conversation title + ids reach the sender.
	rec := post(`{"session_id":"sess-7","convo_name":"reporting cleanup","ts":"2026-07-08T10:00:00Z","ids":["a","b"]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("ingest status = %d, want 200", rec.Code)
	}
	if got == nil {
		t.Fatal("a real batch did not reach the sender")
	}
	if got.SessionID != "sess-7" {
		t.Fatalf("registered session id = %q, want sess-7", got.SessionID)
	}
	if got.ConvoName != "reporting cleanup" {
		t.Fatalf("registered title = %q, want the conversation name", got.ConvoName)
	}
	if len(got.IDs) != 2 || got.IDs[0] != "a" || got.IDs[1] != "b" {
		t.Fatalf("registered ids = %v, want [a b]", got.IDs)
	}

	// An empty batch (no ids) is a quiet no-op — never a dispatch.
	got = nil
	rec = post(`{"session_id":"sess-7","ts":"2026-07-08T10:00:00Z","ids":[]}`)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("empty ingest status = %d, want 204", rec.Code)
	}
	if got != nil {
		t.Fatalf("empty batch must not dispatch, got %+v", got)
	}
}
