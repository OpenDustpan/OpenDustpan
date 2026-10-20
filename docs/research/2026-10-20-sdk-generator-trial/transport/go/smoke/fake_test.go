package trial_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

// seen records what the fake server got, so a test can check the headers
// and the body that the generated client and the transport sent.
type seen struct {
	mu      sync.Mutex
	headers map[string]http.Header
	bodies  map[string]string
	tries   map[string]int
	keys    []string
}

func (s *seen) record(r *http.Request) {
	b, _ := io.ReadAll(r.Body)
	s.mu.Lock()
	defer s.mu.Unlock()
	k := r.Method + " " + r.URL.Path
	s.headers[k] = r.Header.Clone()
	s.bodies[k] = string(b)
	s.tries[k]++
	if r.Method == http.MethodPost {
		s.keys = append(s.keys, r.Header.Get("Idempotency-Key"))
	}
}

func (s *seen) get(k string) (http.Header, string, int) {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.headers[k], s.bodies[k], s.tries[k]
}

const v1 = "/api/dustpan/v1"

// fakeServer answers the shapes of the fixture. It adds an unknown field to
// each unit, because a new answer field is an additive change that an older
// client must accept. Unit u3 has a tool value that the fixture does not
// list, which is how a client sees a new tool after an upgrade of the server.
func fakeServer(t *testing.T) (*httptest.Server, *seen) {
	t.Helper()
	s := &seen{headers: map[string]http.Header{}, bodies: map[string]string{}, tries: map[string]int{}}
	unit := func(id, tool string, details map[string]any) map[string]any {
		return map[string]any{
			"unitId": id, "namespace": "ns", "name": "app", "tool": tool,
			"lastChange": "2026-10-18T09:30:00Z", "resourceCount": nil, "lock": nil,
			"labels": map[string]string{"team": "a"}, "details": details, "futureField": 1,
		}
	}
	writeJSON := func(w http.ResponseWriter, status int, v any) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(v)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET "+v1+"/namespaces/ns/units/u1", func(w http.ResponseWriter, r *http.Request) {
		s.record(r)
		w.Header().Set("ETag", `"v1"`)
		writeJSON(w, 200, unit("u1", "pulumi", map[string]any{"tool": "pulumi", "project": "p", "stack": "dev"}))
	})
	mux.HandleFunc("GET "+v1+"/namespaces/ns/units/u3", func(w http.ResponseWriter, r *http.Request) {
		s.record(r)
		writeJSON(w, 200, unit("u3", "ansible", map[string]any{"tool": "ansible", "playbook": "x"}))
	})
	mux.HandleFunc("GET "+v1+"/namespaces/ns/units/u5", func(w http.ResponseWriter, r *http.Request) {
		s.record(r)
		writeJSON(w, 200, unit("u5", "ansible", map[string]any{"tool": "pulumi", "project": "p", "stack": "dev"}))
	})
	mux.HandleFunc("GET "+v1+"/namespaces/ns/units/u2", func(w http.ResponseWriter, r *http.Request) {
		s.record(r)
		writeJSON(w, 404, map[string]any{
			"code": 404, "message": "The unit u2 does not exist.", "errorCode": "unit.not-found",
			"errorArgs": map[string]any{"unitId": "u2", "limit": 3},
		})
	})
	mux.HandleFunc("GET "+v1+"/namespaces/ns/units", func(w http.ResponseWriter, r *http.Request) {
		s.record(r)
		next, id := "p2", "u1"
		if r.URL.Query().Get("pageToken") == "p2" {
			next, id = "", "u4"
		}
		writeJSON(w, 200, map[string]any{
			"units":         []any{unit(id, "opentofu", map[string]any{"tool": "opentofu", "workspace": "w", "terraformVersion": nil})},
			"nextPageToken": next,
		})
	})
	mux.HandleFunc("PATCH "+v1+"/me/preferences", func(w http.ResponseWriter, r *http.Request) {
		s.record(r)
		w.Header().Set("ETag", `"p2"`)
		writeJSON(w, 200, map[string]any{"theme": "dark", "language": nil})
	})
	mux.HandleFunc("DELETE "+v1+"/backups/b1", func(w http.ResponseWriter, r *http.Request) {
		s.record(r)
		if r.Header.Get("If-Match") != `"b1"` {
			writeJSON(w, 412, map[string]any{"code": 412, "message": "The ETag does not match.", "errorCode": "request.precondition-failed"})

			return
		}
		w.WriteHeader(204)
	})
	// The first POST answers 503, so the transport must retry with the same key.
	mux.HandleFunc("POST "+v1+"/backups", func(w http.ResponseWriter, r *http.Request) {
		s.record(r)
		if _, _, n := s.get("POST " + v1 + "/backups"); n == 1 {
			writeJSON(w, 503, map[string]any{"code": 503, "message": "Try again later."})

			return
		}
		writeJSON(w, 202, map[string]any{"id": "b9", "state": "pending", "createdAt": "2026-10-18T09:30:00Z", "finishedAt": nil})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	return srv, s
}
