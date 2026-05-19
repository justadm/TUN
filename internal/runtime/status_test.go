package runtime

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestServiceStatusTrackerClientReadiness(t *testing.T) {
	tr := NewServiceStatusTracker(RoleClient)
	tr.OnEvent(Event{State: StateDialing, ErrorClass: ErrorClassNone})
	if tr.Snapshot().Ready {
		t.Fatalf("client should not be ready while dialing")
	}
	tr.OnEvent(Event{State: StateEstablished, ErrorClass: ErrorClassNone})
	if !tr.Snapshot().Ready {
		t.Fatalf("client should be ready in established state")
	}
	tr.OnEvent(Event{State: StateReconnecting, ErrorClass: ErrorClassTransport})
	if tr.Snapshot().Ready {
		t.Fatalf("client should not be ready while reconnecting")
	}
}

func TestServiceStatusTrackerServerReadiness(t *testing.T) {
	tr := NewServiceStatusTracker(RoleServer)
	tr.OnEvent(Event{State: StateListening, ErrorClass: ErrorClassNone})
	if !tr.Snapshot().Ready {
		t.Fatalf("server should be ready in listening state")
	}
	tr.OnEvent(Event{State: StateStopped, ErrorClass: ErrorClassContext})
	s := tr.Snapshot()
	if s.Live || s.Ready {
		t.Fatalf("server should be non-live/non-ready when stopped")
	}
}

func TestServiceStatusTrackerHandler(t *testing.T) {
	tr := NewServiceStatusTracker(RoleClient)
	h := tr.Handler()

	req := httptest.NewRequest(http.MethodGet, "/ready", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 before ready, got %d", w.Code)
	}

	tr.OnEvent(Event{State: StateEstablished, ErrorClass: ErrorClassNone})
	req = httptest.NewRequest(http.MethodGet, "/ready", nil)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 when ready, got %d", w.Code)
	}
}
