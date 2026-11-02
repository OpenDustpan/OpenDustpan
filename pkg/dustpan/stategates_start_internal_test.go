package dustpan

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/api/stategate"
	"opendustpan.org/opendustpan/pkg/audit"
)

// errAppendDown is the error of an audit store that cannot append.
var errAppendDown = errors.New("audit store down")

// serverEntries keeps the entries of the installation scope.
type serverEntries struct {
	mu      sync.Mutex
	err     error
	entries []audit.Entry
}

// AppendServer implements audit.ServerStore.
func (s *serverEntries) AppendServer(_ context.Context, e audit.Entry) (audit.Entry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.err != nil {
		return audit.Entry{}, s.err
	}

	if err := e.Validate(); err != nil {
		return audit.Entry{}, err
	}

	s.entries = append(s.entries, e)

	return e, nil
}

// startServer returns a Server with only the fields that the record of the
// state.write gates reads, and its log.
func startServer(eff stategate.Effective, store audit.ServerStore) (*Server, *bytes.Buffer) {
	var log bytes.Buffer

	at := time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)

	srv := testServer(nil, "")
	srv.logger = slog.New(slog.NewTextHandler(&log, nil))
	srv.now = func() time.Time { return at }
	srv.stateWrite = stateWriteStart{effective: eff, audit: store}

	return srv, &log
}

// TestStateWriteGatesOffIsRecordedAtStart checks that a start with the gates
// off writes the start log line and one audit entry in the installation
// scope with the reason, and that a start with the gates on writes the log
// line and no entry.
func TestStateWriteGatesOffIsRecordedAtStart(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		eff     stategate.Effective
		line    string
		entries int
	}{
		{"off", stategate.Effective{Mode: stategate.ModeOff, Reason: reasonNoGateStore}, "state.write gates: off", 1},
		{"on", stategate.Effective{Mode: stategate.ModeOn, Reason: stategate.ReasonOn}, "state.write gates: on", 0},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			store := &serverEntries{mu: sync.Mutex{}, err: nil, entries: nil}
			srv, log := startServer(tc.eff, store)

			if err := srv.recordStateWriteGates(t.Context()); err != nil {
				t.Fatalf("recordStateWriteGates() = %v, want nil", err)
			}

			if !strings.Contains(log.String(), tc.line) || !strings.Contains(log.String(), tc.eff.Reason) {
				t.Fatalf("log = %q, want %q with the reason", log.String(), tc.line)
			}

			checkOffEntries(t, store.entries, tc.entries)
		})
	}
}

// checkOffEntries fails t unless entries holds want entries, each one the
// entry of the gates off with its reason in the installation scope.
func checkOffEntries(t *testing.T, entries []audit.Entry, want int) {
	t.Helper()

	if len(entries) != want {
		t.Fatalf("wrote %d audit entries, want %d", len(entries), want)
	}

	for i := range entries {
		e := &entries[i]
		if e.Namespace != audit.ServerScope || e.Reason != reasonNoGateStore || e.Channel != audit.ChannelStartup ||
			len(e.Changes) != 1 || e.Changes[0].Key != stateWriteGatesKey || e.Changes[0].New != "off" {
			t.Fatalf("entry = %+v, want the gates off with the reason in the installation scope", e)
		}
	}
}

// TestStateWriteGatesOffWithNoRecordStopsTheStart sends the gates off
// straight to the start step with no audit store, and with a store that
// fails, as if the option check had passed them. The start must fail: the
// gates are never off with no record.
func TestStateWriteGatesOffWithNoRecordStopsTheStart(t *testing.T) {
	t.Parallel()

	off := stategate.Effective{Mode: stategate.ModeOff, Reason: reasonNoGateStore}

	cases := []struct {
		name  string
		store audit.ServerStore
		want  error
	}{
		{"no audit store", nil, errStateWriteNoAudit},
		{"append fails", &serverEntries{mu: sync.Mutex{}, err: errAppendDown, entries: nil}, errAppendDown},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			srv, _ := startServer(off, tc.store)

			if err := srv.recordStateWriteGates(t.Context()); !errors.Is(err, tc.want) {
				t.Fatalf("recordStateWriteGates() = %v, want %v", err, tc.want)
			}
		})
	}
}
