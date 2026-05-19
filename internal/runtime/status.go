package runtime

import (
	"encoding/json"
	"net/http"
	"sync"
)

type Role string

const (
	RoleClient Role = "client"
	RoleServer Role = "server"
)

type ServiceStatus struct {
	Role       Role       `json:"role"`
	Live       bool       `json:"live"`
	Ready      bool       `json:"ready"`
	State      State      `json:"state"`
	ErrorClass ErrorClass `json:"error_class"`
	LastError  string     `json:"last_error"`
	Snapshot   Snapshot   `json:"snapshot"`
}

type ServiceStatusTracker struct {
	mu     sync.RWMutex
	role   Role
	status ServiceStatus
}

func NewServiceStatusTracker(role Role) *ServiceStatusTracker {
	return &ServiceStatusTracker{
		role: role,
		status: ServiceStatus{
			Role:       role,
			Live:       true,
			Ready:      false,
			State:      StateIdle,
			ErrorClass: ErrorClassNone,
		},
	}
}

func (t *ServiceStatusTracker) OnEvent(e Event) {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.status.State = e.State
	t.status.ErrorClass = e.ErrorClass
	if e.Cause != nil {
		t.status.LastError = e.Cause.Error()
	} else {
		t.status.LastError = ""
	}
	t.status.Snapshot = e.Snapshot

	switch e.State {
	case StateStopped:
		t.status.Live = false
		t.status.Ready = false
	case StateEstablished:
		t.status.Live = true
		t.status.Ready = true
	case StateListening, StateAccepted:
		t.status.Live = true
		if t.role == RoleServer {
			t.status.Ready = true
		}
	case StateReconnecting, StateDialing, StateHandshaking:
		t.status.Live = true
		t.status.Ready = false
	default:
		t.status.Live = true
	}
}

func (t *ServiceStatusTracker) Snapshot() ServiceStatus {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.status
}

func (t *ServiceStatusTracker) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/live", func(w http.ResponseWriter, _ *http.Request) {
		s := t.Snapshot()
		if !s.Live {
			http.Error(w, "not live", http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	mux.HandleFunc("/ready", func(w http.ResponseWriter, _ *http.Request) {
		s := t.Snapshot()
		if !s.Ready {
			http.Error(w, "not ready", http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	mux.HandleFunc("/status", func(w http.ResponseWriter, _ *http.Request) {
		s := t.Snapshot()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(s)
	})
	return mux
}
