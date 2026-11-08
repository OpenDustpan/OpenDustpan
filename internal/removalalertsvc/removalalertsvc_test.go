package removalalertsvc_test

import (
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/removalalertsvc"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/notify"
	"opendustpan.org/opendustpan/pkg/principal"
)

// wantAttributes returns the attributes of the alert of the count crossing
// of grp-a. The group holds 100 members, so it held 101 before the tenth
// removal: ten of 101 members are 9 percent, rounded down.
func wantAttributes() map[string]string {
	return map[string]string{
		"source.kind": "identity-provider", "source.id": "idp-1", "group.id": "grp-a",
		"removed": "10", "percent": "9", "window": "15m0s", "limit": "count",
	}
}

// TestRecordFiresOnceOnTheCount proves that twelve removals of one group of
// 100 members give one alert, on the count, with the source, the count, the
// percent and the window in the notification, the audit entry and the log.
func TestRecordFiresOnceOnTheCount(t *testing.T) {
	t.Parallel()

	f := newFixture()
	f.record(t, f.service(t), idpSource(), "grp-a", 12)

	notes := f.outbox.stored()
	if len(notes) != 1 {
		t.Fatalf("alerts = %d, want 1", len(notes))
	}

	checkAlert(t, notes[0])
	checkAlertEntry(t, f.audit.stored())

	if !strings.Contains(f.logs.text(), "idp-1") {
		t.Fatalf("log = %q, want the source ID", f.logs.text())
	}
}

// checkAlert fails unless n is the warning of the server scope for the
// administrators with the summary and the attributes of the count crossing.
func checkAlert(t *testing.T, n notify.Notification) {
	t.Helper()

	if n.Type != notify.MembershipMassRemoval || n.Scope != audit.ServerScope ||
		n.Severity != notify.SeverityWarning || len(n.Recipients) != 1 || !n.Recipients[0].Admins {
		t.Fatalf("alert = %+v, want a warning of the server scope for the administrators", n)
	}

	for key, value := range wantAttributes() {
		if n.Attributes[key] != value {
			t.Fatalf("alert attribute %s = %q, want %q", key, n.Attributes[key], value)
		}
	}

	for _, text := range []string{"identity-provider", "idp-1", "grp-a", "15m0s"} {
		if !strings.Contains(n.Summary, text) {
			t.Fatalf("summary = %q, want it to hold %q", n.Summary, text)
		}
	}
}

// checkAlertEntry fails unless entries holds the one audit entry of the
// count crossing.
func checkAlertEntry(t *testing.T, entries []audit.Entry) {
	t.Helper()

	if len(entries) != 1 {
		t.Fatalf("audit entries = %d, want 1", len(entries))
	}

	e := entries[0]
	if e.Action != principal.AuditActionMassRemovalAlert || e.Namespace != audit.ServerScope ||
		e.Kind != audit.KindChange || e.Outcome != audit.OutcomeSuccess || e.Channel != audit.ChannelAPI {
		t.Fatalf("audit entry = %+v, want a change of the mass removal alert", e)
	}

	if e.Subject != "identity-provider:idp-1" || e.Object != "group:grp-a" {
		t.Fatalf("audit entry names %q of %q, want the source and the group", e.Subject, e.Object)
	}

	if len(e.Changes) != len(wantAttributes()) {
		t.Fatalf("audit changes = %+v, want the attributes of the alert", e.Changes)
	}

	for i := range e.Changes {
		want := wantAttributes()[e.Changes[i].Key]
		if e.Changes[i].New != want {
			t.Fatalf("audit change %s = %q, want %q", e.Changes[i].Key, e.Changes[i].New, want)
		}
	}
}

// TestRecordFiresOnTheTenthRemoval proves that ten removals fire once, on
// the tenth, and that the eleventh removal fires no second alert.
func TestRecordFiresOnTheTenthRemoval(t *testing.T) {
	t.Parallel()

	f := newFixture()
	svc := f.service(t)

	f.record(t, svc, idpSource(), "grp-a", 10)

	if got := len(f.outbox.stored()); got != 1 {
		t.Fatalf("alerts after ten removals = %d, want 1", got)
	}

	f.record(t, svc, idpSource(), "grp-a", 1)

	if got := len(f.outbox.stored()); got != 1 {
		t.Fatalf("alerts after eleven removals = %d, want 1", got)
	}
}

// TestRecordBelowTheLimit proves that nine removals give no alert.
func TestRecordBelowTheLimit(t *testing.T) {
	t.Parallel()

	f := newFixture()
	f.record(t, f.service(t), idpSource(), "grp-a", 9)

	if got := len(f.outbox.stored()); got != 0 {
		t.Fatalf("alerts after nine removals = %d, want 0", got)
	}
}

// TestRecordFiresOnThePercent proves that the first removal from a group of
// five members fires on the percent limit, before ten members.
func TestRecordFiresOnThePercent(t *testing.T) {
	t.Parallel()

	f := newFixture()
	// The group holds four members now: it held five before the removal.
	f.members.totals["grp-b"] = 4

	f.record(t, f.service(t), idpSource(), "grp-b", 1)

	notes := f.outbox.stored()
	if len(notes) != 1 {
		t.Fatalf("alerts = %d, want 1", len(notes))
	}

	if notes[0].Attributes["limit"] != "percent" || notes[0].Attributes["percent"] != "20" ||
		notes[0].Attributes["removed"] != "1" {
		t.Fatalf("alert attributes = %+v, want the percent limit of 20 percent", notes[0].Attributes)
	}
}

// TestRecordWindow proves that removals older than the window do not count.
func TestRecordWindow(t *testing.T) {
	t.Parallel()

	f := newFixture()
	svc := f.service(t)

	f.record(t, svc, idpSource(), "grp-a", 9)

	// Move the clock past the window of 15 minutes, so the nine removals
	// are outside it.
	f.clock = f.clock.Add(16 * time.Minute)

	f.record(t, svc, idpSource(), "grp-a", 1)

	if got := len(f.outbox.stored()); got != 0 {
		t.Fatalf("alerts after the window = %d, want 0", got)
	}
}

// TestRecordOtherSourceKeepsItsOwnCount proves that the totals of one source
// do not count for another source.
func TestRecordOtherSourceKeepsItsOwnCount(t *testing.T) {
	t.Parallel()

	f := newFixture()
	svc := f.service(t)
	other := principal.RemovalSource{Kind: principal.RemovalSourceSCIMToken, ID: "tok-2"}

	f.record(t, svc, idpSource(), "grp-a", 9)
	f.record(t, svc, other, "grp-a", 9)

	if got := len(f.outbox.stored()); got != 0 {
		t.Fatalf("alerts of two sources = %d, want 0", got)
	}

	f.record(t, svc, idpSource(), "grp-a", 1)

	if got := len(f.outbox.stored()); got != 1 {
		t.Fatalf("alerts of the first source = %d, want 1", got)
	}
}

// TestRecordDedupesTheGroups proves that a group ID that a call names twice
// stores one removal.
func TestRecordDedupesTheGroups(t *testing.T) {
	t.Parallel()

	f := newFixture()
	svc := f.service(t)

	if err := svc.Record(t.Context(), idpSource(), []string{"grp-a", "grp-a"}); err != nil {
		t.Fatalf("Record() = %v", err)
	}

	if rows := f.log.stored(); len(rows) != 1 {
		t.Fatalf("stored removals = %d, want 1", len(rows))
	}
}

// TestRecordDefaultsWhenTheReaderFails proves that a reader that fails gives
// the default limits, never no limit. A reader that fails answers the zero
// value with its error.
func TestRecordDefaultsWhenTheReaderFails(t *testing.T) {
	t.Parallel()

	f := newFixture()
	f.limits = principal.RemovalAlertLimits{Count: 0, Percent: 0, Window: 0}
	f.limitsErr = errLimits

	f.record(t, f.service(t), idpSource(), "grp-a", 10)

	if got := len(f.outbox.stored()); got != 1 {
		t.Fatalf("alerts = %d, want 1", got)
	}
}

// TestRecordDefaultsWithNoReader proves that no reader gives the default
// limits.
func TestRecordDefaultsWithNoReader(t *testing.T) {
	t.Parallel()

	f := newFixture()
	f.noLimits = true

	f.record(t, f.service(t), idpSource(), "grp-a", 10)

	if got := len(f.outbox.stored()); got != 1 {
		t.Fatalf("alerts = %d, want 1", got)
	}
}

// TestRecordDefaults proves that a value outside the bounds gives the
// default limits, and that a value inside them is used.
func TestRecordDefaults(t *testing.T) {
	t.Parallel()

	window := 15 * time.Minute

	cases := []struct {
		limits  principal.RemovalAlertLimits
		name    string
		group   string
		calls   int
		members int64
		want    int
	}{
		{
			name:   "a count below the bounds",
			limits: principal.RemovalAlertLimits{Count: 0, Percent: 20, Window: window},
			group:  "grp-a", members: 100, calls: 10, want: 1,
		},
		{
			name:   "a count above the bounds",
			limits: principal.RemovalAlertLimits{Count: 10001, Percent: 20, Window: window},
			group:  "grp-a", members: 100, calls: 10, want: 1,
		},
		{
			name:   "a percent below the bounds",
			limits: principal.RemovalAlertLimits{Count: 10, Percent: 0, Window: window},
			group:  "grp-a", members: 100, calls: 10, want: 1,
		},
		{
			name:   "a percent above the bounds",
			limits: principal.RemovalAlertLimits{Count: 10, Percent: 101, Window: window},
			group:  "grp-a", members: 100, calls: 10, want: 1,
		},
		{
			name:   "a window below the bounds",
			limits: principal.RemovalAlertLimits{Count: 10, Percent: 20, Window: 0},
			group:  "grp-a", members: 100, calls: 10, want: 1,
		},
		{
			name:   "a window above the bounds",
			limits: principal.RemovalAlertLimits{Count: 10, Percent: 20, Window: 25 * time.Hour},
			group:  "grp-a", members: 100, calls: 10, want: 1,
		},
		{
			name:   "a count of five",
			limits: principal.RemovalAlertLimits{Count: 5, Percent: 20, Window: window},
			group:  "grp-a", members: 100, calls: 5, want: 1,
		},
		{
			name:   "a count of five is not reached",
			limits: principal.RemovalAlertLimits{Count: 5, Percent: 20, Window: window},
			group:  "grp-a", members: 100, calls: 4, want: 0,
		},
		{
			// One removal from a group of six members is above one percent
			// and below the default of 20 percent, so only the reader fires.
			name:   "a percent of one",
			limits: principal.RemovalAlertLimits{Count: 100, Percent: 1, Window: window},
			group:  "grp-b", members: 5, calls: 1, want: 1,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f := newFixture()
			f.limits = tc.limits
			f.members.totals[tc.group] = tc.members
			f.record(t, f.service(t), idpSource(), tc.group, tc.calls)

			if got := len(f.outbox.stored()); got != tc.want {
				t.Fatalf("alerts = %d, want %d", got, tc.want)
			}
		})
	}
}

// TestRecordPortErrors proves that each port error makes Record return an
// error that wraps it, and that the outbox error stops the audit append.
func TestRecordPortErrors(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name       string
		membersErr error
		logErr     error
		outboxErr  error
		auditErr   error
		want       error
	}{
		{"the count read fails", errMembers, nil, nil, nil, errMembers},
		{"the removal log fails", nil, errLog, nil, nil, errLog},
		{"the outbox fails", nil, nil, errOutbox, nil, errOutbox},
		{"the audit log fails", nil, nil, nil, errAudit, errAudit},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f := newFixture()
			// The count limit of one makes the first removal cross, so the
			// outbox and the audit log run.
			f.limits = principal.RemovalAlertLimits{Count: 1, Percent: 20, Window: 15 * time.Minute}
			f.members.err, f.log.err = tc.membersErr, tc.logErr
			f.outbox.err, f.audit.err = tc.outboxErr, tc.auditErr

			err := f.service(t).Record(t.Context(), idpSource(), []string{"grp-a"})
			if !errors.Is(err, tc.want) {
				t.Fatalf("Record() = %v, want %v", err, tc.want)
			}

			if tc.outboxErr != nil && len(f.audit.stored()) != 0 {
				t.Fatalf("audit entries after an outbox error = %d, want 0", len(f.audit.stored()))
			}
		})
	}
}

// TestRecordStripsLineEnds proves that a source ID and a group ID with a CR
// or an LF give a log line, a summary and attributes without one.
func TestRecordStripsLineEnds(t *testing.T) {
	t.Parallel()

	f := newFixture()
	f.limits = principal.RemovalAlertLimits{Count: 1, Percent: 20, Window: 15 * time.Minute}

	source := principal.RemovalSource{Kind: principal.RemovalSourceIdentityProvider, ID: "idp-\r\n1"}
	if err := f.service(t).Record(t.Context(), source, []string{"grp-\r\na"}); err != nil {
		t.Fatalf("Record() = %v", err)
	}

	notes := f.outbox.stored()
	if len(notes) != 1 {
		t.Fatalf("alerts = %d, want 1", len(notes))
	}

	if strings.ContainsAny(notes[0].Summary, "\r\n") {
		t.Fatalf("summary = %q, want no CR or LF", notes[0].Summary)
	}

	for key, value := range notes[0].Attributes {
		if strings.ContainsAny(key+value, "\r\n") {
			t.Fatalf("attribute %q = %q, want no CR or LF", key, value)
		}
	}

	if strings.ContainsAny(f.logs.text(), "\r\n") {
		t.Fatalf("log = %q, want no CR or LF", f.logs.text())
	}

	rows := f.log.stored()
	if len(rows) != 1 || strings.ContainsAny(rows[0].Source.ID+rows[0].GroupID, "\r\n") {
		t.Fatalf("stored removal = %+v, want IDs without a CR or an LF", rows)
	}

	entries := f.audit.stored()
	if len(entries) != 1 || strings.ContainsAny(entries[0].Subject+entries[0].Object, "\r\n") {
		t.Fatalf("audit entry = %+v, want text without a CR or an LF", entries)
	}
}

// TestNewRefusesAMissingPort proves that each port, the logger and the clock
// are required.
func TestNewRefusesAMissingPort(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		edit func(*removalalertsvc.Config)
	}{
		{"the removal log", func(c *removalalertsvc.Config) { c.Log = nil }},
		{"the member counter", func(c *removalalertsvc.Config) { c.Members = nil }},
		{"the outbox", func(c *removalalertsvc.Config) { c.Outbox = nil }},
		{"the audit log", func(c *removalalertsvc.Config) { c.Audit = nil }},
		{"the logger", func(c *removalalertsvc.Config) { c.Logger = nil }},
		{"the clock", func(c *removalalertsvc.Config) { c.Now = nil }},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f := newFixture()
			cfg := removalalertsvc.Config{
				Log: f.log, Members: f.members, Outbox: f.outbox, Audit: f.audit,
				Logger: slog.New(f.logs), Now: func() time.Time { return f.clock }, Limits: nil,
			}
			tc.edit(&cfg)

			if _, err := removalalertsvc.New(cfg); !errors.Is(err, removalalertsvc.ErrMissingDependency) {
				t.Fatalf("New() = %v, want ErrMissingDependency", err)
			}
		})
	}
}
