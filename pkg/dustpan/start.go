package dustpan

import (
	"context"
	"log/slog"
	"net/http"
	"sync"
	"sync/atomic"
)

// liveProbePath is the path of the liveness probe. It checks nothing, so the gate
// lets it pass before Start.
const liveProbePath = "/health/live"

// startMessage is the text of every refusal before Start has succeeded. Each
// adapter renders it in its own error shape.
const startMessage = "The server has not started. The program that embeds it must call Start before it serves requests."

// startState records whether Start has succeeded. The mutex lets one Start
// run at a time. The flag is read by the gate in front of the handler on each
// request.
type startState struct {
	mu   sync.Mutex
	done atomic.Bool
}

// newStartState returns a state that records no successful Start.
func newStartState() *startState {
	return &startState{mu: sync.Mutex{}, done: atomic.Bool{}}
}

// Start runs the start-up steps in order: the claim of the installation kind,
// the check of the key-encryption key against every wrapped key in the
// database, the failure of every backup and restore that was running when the
// server stopped (with the fence of the namespaces after a restore that
// stopped), the check of the audit chain, the record of the effective value
// of the state.write gates and the binding of the sign-in providers. It
// opens no listener. Run calls Start. A program that serves Handler by itself
// must call Start before it serves a request: until Start has succeeded,
// Handler answers 503 on each route except GET /health/live.
//
// Start runs the steps once. After a Start that succeeded, Start and Run
// return at once and do not repeat them. A Start that failed does not mark
// the server as started: the next call runs the steps again. Start is safe
// for use by several goroutines. When ctx is done before the steps end, Start
// returns the error of the step and the server stays not started.
func (s *Server) Start(ctx context.Context) error {
	s.started.mu.Lock()
	defer s.started.mu.Unlock()

	if s.started.done.Load() {
		return nil
	}

	if err := s.startSteps(ctx); err != nil {
		return err
	}

	s.started.done.Store(true)

	return nil
}

// startSteps runs the start-up steps of Start in order. The first error stops
// them.
func (s *Server) startSteps(ctx context.Context) error {
	steps := []func(context.Context) error{
		s.startUp,
		s.startUpAuditChain,
		s.recordStateWriteGates,
		s.bindSignInProviders,
	}

	for _, step := range steps {
		if err := step(ctx); err != nil {
			return err
		}
	}

	return nil
}

// withStartGate answers 503 on each request until Start has succeeded, in the
// error shape of the adapter of the path. Only GET /health/live passes: it
// checks nothing, and a process manager can tell that the process runs. The
// gate is the outermost layer of the handler, so no route passes it by a
// layer below. The gate fails closed: a program that forgets Start gets 503 at
// the first request.
func withStartGate(next http.Handler, state *startState, logger *slog.Logger) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if state.done.Load() || isLiveProbe(r) {
			next.ServeHTTP(w, r)

			return
		}

		shape, _ := chainGateShape(r)

		writeRefusal(r.Context(), w, shape, logger, startMessage)
	})
}

// isLiveProbe reports whether r is the liveness probe. It compares the escaped
// path, the same form that the router matches, so an encoded path such as
// /health%2Flive does not pass as the probe. Only GET passes, as the API
// description says.
func isLiveProbe(r *http.Request) bool {
	return r.Method == http.MethodGet && r.URL.EscapedPath() == liveProbePath
}
