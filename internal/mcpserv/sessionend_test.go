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

// The /session-end route decodes the ended SessionID and marshals a
// SessionEndAction onto the UI loop — the same bearer+localhost seam /ingest
// uses. A missing/empty id is a quiet no-op, never a dispatch.
func TestSessionEndHandler(t *testing.T) {
	var got *agentapi.SessionEndAction
	s := &Server{
		logger: log.New(io.Discard),
		send: func(a agentapi.Action) (agentapi.Response, error) {
			sa, ok := a.(agentapi.SessionEndAction)
			if !ok {
				t.Fatalf("session-end dispatched a %T, want SessionEndAction", a)
			}
			got = &sa
			return agentapi.Response{Text: "ok"}, nil
		},
	}

	post := func(body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/session-end", bytes.NewReader([]byte(body)))
		rec := httptest.NewRecorder()
		s.sessionEndHandler(rec, req)
		return rec
	}

	// A real end: the SessionID reaches the sender and the route acks 200.
	rec := post(`{"session_id":"sess-42"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("session-end status = %d, want 200", rec.Code)
	}
	if got == nil || got.SessionID != "sess-42" {
		t.Fatalf("dispatched action = %+v, want SessionID sess-42", got)
	}

	// An empty id is a no-op (204), never a dispatch.
	got = nil
	rec = post(`{"session_id":""}`)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("empty session-end status = %d, want 204", rec.Code)
	}
	if got != nil {
		t.Fatalf("empty session-end must not dispatch, got %+v", got)
	}
}
