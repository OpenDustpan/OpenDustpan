package removalalertsvc_test

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/removalalertsvc"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/notify"
	"opendustpan.org/opendustpan/pkg/principal"
)

// The errors of the fakes of the tests.
var (
	errMembers = errors.New("the member count does not answer")
	errLog     = errors.New("the removal log does not answer")
	errOutbox  = errors.New("the outbox does not answer")
	errAudit   = errors.New("the audit log does not answer")
	errMark    = errors.New("the removal alert marker does not answer")
	errLimits  = errors.New("the settings do not answer")
)

// testAt is the time of the clock of the tests. No check reads the wall
// clock.
func testAt() time.Time { return time.Date(2026, time.November, 6, 12, 0, 0, 0, time.UTC) }

// idpSource is the source of the removals of the tests.
func idpSource() principal.RemovalSource {
	return principal.RemovalSource{Kind: principal.RemovalSourceIdentityProvider, ID: "idp-1"}
}

// fakeLog is a principal.RemovalLog in memory. It sums like the store: the
// totals cover the rows of the source in the window that ends at the
// removal time, the new row included.
type fakeLog struct {
	mu      sync.Mutex
	rows    []principal.Removal
	alerts  map[principal.RemovalSource]time.Time
	err     error
	markErr error
}

// AddRemoval implements principal.RemovalLog.
func (f *fakeLog) AddRemoval(
	_ context.Context, r principal.Removal, window time.Duration,
) (principal.RemovalTotals, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.err != nil {
		return principal.RemovalTotals{}, f.err
	}

	if err := r.Validate(); err != nil {
		return principal.RemovalTotals{}, err
	}

	if err := principal.ValidateRemovalWindow(window); err != nil {
		return principal.RemovalTotals{}, err
	}

	f.rows = append(f.rows, r)

	totals := principal.RemovalTotals{
		SourceRemoved: 0, GroupRemoved: 0, GroupMembers: 0, AlertedAt: f.alerts[r.Source],
	}

	for i := range f.rows {
		row := &f.rows[i]
		if row.Source != r.Source || !row.At.After(r.At.Add(-window)) || row.At.After(r.At) {
			continue
		}

		totals.SourceRemoved += row.Removed

		if row.GroupID == r.GroupID {
			totals.GroupRemoved += row.Removed
			totals.GroupMembers = max(totals.GroupMembers, row.MembersBefore)
		}
	}

	return totals, nil
}

// MarkRemovalAlert implements principal.RemovalLog.
func (f *fakeLog) MarkRemovalAlert(
	_ context.Context, source principal.RemovalSource, at time.Time,
) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.markErr != nil {
		return f.markErr
	}

	if err := principal.ValidateRemovalAlert(source, at); err != nil {
		return err
	}

	if f.alerts == nil {
		f.alerts = make(map[principal.RemovalSource]time.Time)
	}

	f.alerts[source] = at

	return nil
}

// alerted returns the time of the last alert of source.
func (f *fakeLog) alerted(source principal.RemovalSource) time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()

	return f.alerts[source]
}

// stored returns the rows of the log.
func (f *fakeLog) stored() []principal.Removal {
	f.mu.Lock()
	defer f.mu.Unlock()

	return append([]principal.Removal(nil), f.rows...)
}

// fakeMembers is a MemberCounter with one total for each group.
type fakeMembers struct {
	mu     sync.Mutex
	totals map[string]int64
	err    error
}

// ListMembers implements removalalertsvc.MemberCounter.
func (f *fakeMembers) ListMembers(
	_ context.Context, groupID string, _ principal.Query,
) ([]principal.Member, int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.err != nil {
		return nil, 0, f.err
	}

	total, ok := f.totals[groupID]
	if !ok {
		return nil, 0, principal.ErrNotFound
	}

	return nil, total, nil
}

// fakeOutbox is a notify.Outbox in memory. Append keeps the notifications;
// the read methods of the dispatcher answer empty.
type fakeOutbox struct {
	mu       sync.Mutex
	appended []notify.Notification
	err      error
}

// Append implements notify.Outbox.
func (f *fakeOutbox) Append(_ context.Context, _ namespace.Fence, ns []notify.Notification) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.err != nil {
		return f.err
	}

	f.appended = append(f.appended, ns...)

	return nil
}

// Scopes implements notify.Outbox.
func (f *fakeOutbox) Scopes(context.Context, string) ([]namespace.Name, error) { return nil, nil }

// After implements notify.Outbox.
func (f *fakeOutbox) After(context.Context, namespace.Name, int64, int) ([]notify.Notification, error) {
	return nil, nil
}

// Cursor implements notify.Outbox.
func (f *fakeOutbox) Cursor(context.Context, string, namespace.Name) (int64, error) { return 0, nil }

// MoveCursor implements notify.Outbox.
func (f *fakeOutbox) MoveCursor(context.Context, namespace.Fence, notify.CursorMove) error {
	return nil
}

// stored returns the notifications that the outbox holds.
func (f *fakeOutbox) stored() []notify.Notification {
	f.mu.Lock()
	defer f.mu.Unlock()

	return append([]notify.Notification(nil), f.appended...)
}

// fakeAudit is an audit.ServerStore in memory. It validates each entry like
// the store.
type fakeAudit struct {
	mu      sync.Mutex
	entries []audit.Entry
	err     error
}

// AppendServer implements audit.ServerStore.
func (f *fakeAudit) AppendServer(_ context.Context, e audit.Entry) (audit.Entry, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.err != nil {
		return audit.Entry{}, f.err
	}

	if err := e.Validate(); err != nil {
		return audit.Entry{}, err
	}

	f.entries = append(f.entries, e)

	return e, nil
}

// stored returns the entries that the audit log holds.
func (f *fakeAudit) stored() []audit.Entry {
	f.mu.Lock()
	defer f.mu.Unlock()

	return append([]audit.Entry(nil), f.entries...)
}

// capturedLog is a slog.Handler that keeps the records that the service
// writes.
type capturedLog struct {
	mu      sync.Mutex
	records []slog.Record
}

// Enabled implements slog.Handler.
func (h *capturedLog) Enabled(context.Context, slog.Level) bool { return true }

// Handle implements slog.Handler.
func (h *capturedLog) Handle(_ context.Context, r slog.Record) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	h.records = append(h.records, r.Clone())

	return nil
}

// WithAttrs implements slog.Handler.
func (h *capturedLog) WithAttrs([]slog.Attr) slog.Handler { return h }

// WithGroup implements slog.Handler.
func (h *capturedLog) WithGroup(string) slog.Handler { return h }

// text returns the messages and the attribute values of every record, so a
// check can look for a line end.
func (h *capturedLog) text() string {
	h.mu.Lock()
	defer h.mu.Unlock()

	var b strings.Builder

	for i := range h.records {
		b.WriteString(h.records[i].Message)

		h.records[i].Attrs(func(a slog.Attr) bool {
			b.WriteString(a.Value.String())

			return true
		})
	}

	return b.String()
}

// warnings returns the WARN records whose message holds text.
func (h *capturedLog) warnings(text string) []slog.Record {
	h.mu.Lock()
	defer h.mu.Unlock()

	var out []slog.Record

	for i := range h.records {
		if h.records[i].Level == slog.LevelWarn && strings.Contains(h.records[i].Message, text) {
			out = append(out, h.records[i].Clone())
		}
	}

	return out
}

// fixture holds the fakes and the clock of one test.
type fixture struct {
	log       *fakeLog
	members   *fakeMembers
	outbox    *fakeOutbox
	audit     *fakeAudit
	logs      *capturedLog
	clock     time.Time
	limits    principal.RemovalAlertLimits
	limitsErr error
	noLimits  bool
}

// newFixture returns the fakes of a test. The group grp-a holds 100 members.
func newFixture() *fixture {
	return &fixture{
		log: &fakeLog{
			mu: sync.Mutex{}, rows: nil, alerts: make(map[principal.RemovalSource]time.Time), err: nil, markErr: nil,
		},
		members: &fakeMembers{
			mu: sync.Mutex{}, totals: map[string]int64{"grp-a": 100, "grp-b": 4, "grp-c": 9}, err: nil,
		},
		outbox:    &fakeOutbox{mu: sync.Mutex{}, appended: nil, err: nil},
		audit:     &fakeAudit{mu: sync.Mutex{}, entries: nil, err: nil},
		logs:      &capturedLog{mu: sync.Mutex{}, records: nil},
		clock:     testAt(),
		limits:    principal.DefaultRemovalAlertLimits(),
		limitsErr: nil,
		noLimits:  false,
	}
}

// service returns the service over the fakes of f.
func (f *fixture) service(t *testing.T) *removalalertsvc.Service {
	t.Helper()

	var limits removalalertsvc.Limits
	if !f.noLimits {
		limits = func(context.Context) (principal.RemovalAlertLimits, error) { return f.limits, f.limitsErr }
	}

	svc, err := removalalertsvc.New(removalalertsvc.Config{
		Log: f.log, Members: f.members, Outbox: f.outbox, Audit: f.audit,
		Logger: slog.New(f.logs), Now: func() time.Time { return f.clock }, Limits: limits,
	})
	if err != nil {
		t.Fatalf("New() = %v", err)
	}

	return svc
}

// setMarker stores at as the alert marker of source.
func (f *fixture) setMarker(source principal.RemovalSource, at time.Time) {
	f.log.mu.Lock()
	defer f.log.mu.Unlock()

	f.log.alerts[source] = at
}

// record calls Record calls times with the source and one group.
func (f *fixture) record(
	t *testing.T,
	svc *removalalertsvc.Service,
	source principal.RemovalSource,
	group string,
	calls int,
) {
	t.Helper()

	for i := range calls {
		if err := svc.Record(t.Context(), source, []string{group}); err != nil {
			t.Fatalf("Record call %d = %v", i+1, err)
		}
	}
}
