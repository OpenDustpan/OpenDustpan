package removalalertsvc_test

import (
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/errcode"
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
		markErr    error
		want       error
	}{
		{"the count read fails", errMembers, nil, nil, nil, nil, errMembers},
		{"the removal log fails", nil, errLog, nil, nil, nil, errLog},
		{"the outbox fails", nil, nil, errOutbox, nil, nil, errOutbox},
		{"the audit log fails", nil, nil, nil, errAudit, nil, errAudit},
		{"the alert marker fails", nil, nil, nil, nil, errMark, errMark},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f := newFixture()
			// The count limit of one makes the first removal reach the limit,
			// so the outbox, the audit log and the marker run.
			f.limits = principal.RemovalAlertLimits{Count: 1, Percent: 20, Window: 15 * time.Minute}
			f.members.err, f.log.err = tc.membersErr, tc.logErr
			f.outbox.err, f.audit.err = tc.outboxErr, tc.auditErr
			f.log.markErr = tc.markErr

			err := f.service(t).Record(t.Context(), idpSource(), []string{"grp-a"})
			if !errors.Is(err, tc.want) {
				t.Fatalf("Record() = %v, want %v", err, tc.want)
			}

			if tc.outboxErr != nil && len(f.audit.stored()) != 0 {
				t.Fatalf("audit entries after an outbox error = %d, want 0", len(f.audit.stored()))
			}

			if (tc.outboxErr != nil || tc.auditErr != nil) && !f.log.alerted(idpSource()).IsZero() {
				t.Fatal("an alert that failed to write left a marker")
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

// TestRecordRetriesAFailedAlert proves that an alert that fails to write is
// tried again on the next removal of the source.
func TestRecordRetriesAFailedAlert(t *testing.T) {
	t.Parallel()

	f := newFixture()
	f.limits = principal.RemovalAlertLimits{Count: 2, Percent: 20, Window: 15 * time.Minute}
	svc := f.service(t)

	f.record(t, svc, idpSource(), "grp-a", 1)

	f.outbox.err = errOutbox

	err := svc.Record(t.Context(), idpSource(), []string{"grp-a"})
	if !errors.Is(err, errOutbox) {
		t.Fatalf("Record() = %v, want the outbox error", err)
	}

	if got := f.log.alerted(idpSource()); !got.IsZero() {
		t.Fatalf("alert marker after a failed alert = %s, want the zero time", got)
	}

	f.outbox.err = nil

	if recordErr := svc.Record(t.Context(), idpSource(), []string{"grp-a"}); recordErr != nil {
		t.Fatalf("Record() after the retry = %v", recordErr)
	}

	if got := len(f.outbox.stored()); got != 1 {
		t.Fatalf("notifications = %d, want 1", got)
	}

	if got := len(f.audit.stored()); got != 1 {
		t.Fatalf("audit entries = %d, want 1", got)
	}

	if got := f.log.alerted(idpSource()); !got.Equal(testAt()) {
		t.Fatalf("alert marker = %s, want %s", got, testAt())
	}
}

// TestRecordAlertsWhenTheLimitLowers proves that a limit lowered in the
// middle of a window alerts on the next removal.
func TestRecordAlertsWhenTheLimitLowers(t *testing.T) {
	t.Parallel()

	f := newFixture()
	f.limits = principal.RemovalAlertLimits{Count: 5, Percent: 20, Window: 15 * time.Minute}
	svc := f.service(t)

	f.record(t, svc, idpSource(), "grp-a", 3)

	if got := len(f.outbox.stored()); got != 0 {
		t.Fatalf("alerts below the raised limit = %d, want 0", got)
	}

	f.limits.Count = 2
	f.record(t, svc, idpSource(), "grp-a", 1)

	if got := len(f.outbox.stored()); got != 1 {
		t.Fatalf("alerts after the lowered limit = %d, want 1", got)
	}
}

// TestRecordNoSecondAlertInTheWindow proves that a written alert stops more
// alerts of the same source in its window.
func TestRecordNoSecondAlertInTheWindow(t *testing.T) {
	t.Parallel()

	f := newFixture()
	f.limits = principal.RemovalAlertLimits{Count: 2, Percent: 20, Window: 15 * time.Minute}
	svc := f.service(t)

	f.record(t, svc, idpSource(), "grp-a", 2)

	if got := len(f.outbox.stored()); got != 1 {
		t.Fatalf("alerts at the limit = %d, want 1", got)
	}

	f.record(t, svc, idpSource(), "grp-a", 1)

	if got := len(f.outbox.stored()); got != 1 {
		t.Fatalf("alerts above the limit in the same window = %d, want 1", got)
	}
}

// TestRecordFutureMarkerDoesNotSilenceTheAlert proves that a marker one year
// after the removal counts as no marker: the source alerts once, the server
// writes the entry of the bad marker and the WARN line, and the alert writes
// its own marker at the time of the removal.
func TestRecordFutureMarkerDoesNotSilenceTheAlert(t *testing.T) {
	t.Parallel()

	f := newFixture()
	f.limits = principal.RemovalAlertLimits{Count: 2, Percent: 20, Window: 15 * time.Minute}

	future := testAt().Add(365 * 24 * time.Hour)
	f.setMarker(idpSource(), future)

	f.record(t, f.service(t), idpSource(), "grp-a", 3)

	notes := f.outbox.stored()
	if len(notes) != 1 {
		t.Fatalf("alerts = %d, want 1", len(notes))
	}

	if notes[0].Type != notify.MembershipMassRemoval {
		t.Fatalf("alert type = %q, want the mass removal alert", notes[0].Type)
	}

	var invalid, alerts []audit.Entry

	for _, entry := range f.audit.stored() {
		if entry.Reason == errcode.PrincipalRemovalAlertMarkerInvalid {
			invalid = append(invalid, entry)
		} else {
			alerts = append(alerts, entry)
		}
	}

	if len(invalid) != 1 {
		t.Fatalf("entries of the invalid marker = %d, want 1", len(invalid))
	}

	if len(alerts) != 1 {
		t.Fatalf("entries of the alert = %d, want 1", len(alerts))
	}

	checkInvalidMarkerEntry(t, invalid[0], future, testAt())
	checkInvalidMarkerWarning(t, f, future, testAt())

	if marker := f.log.alerted(idpSource()); !marker.Equal(testAt()) {
		t.Fatalf("alert marker = %s, want %s", marker, testAt())
	}
}

// checkInvalidMarkerEntry fails unless e is the security entry of the bad
// marker of idp-1, with the source, the marker time and the removal time.
func checkInvalidMarkerEntry(t *testing.T, e audit.Entry, marker, at time.Time) {
	t.Helper()

	if e.Namespace != audit.ServerScope || e.Subject != "identity-provider:idp-1" ||
		e.Action != principal.AuditActionMassRemovalAlert || e.Object != "group:grp-a" {
		t.Fatalf("audit entry = %+v, want the bad marker of the source and the group", e)
	}

	if e.Kind != audit.KindSecurity || e.Outcome != audit.OutcomeFailure {
		t.Fatalf("audit entry kind, outcome = %q, %q, want security, failure", e.Kind, e.Outcome)
	}

	changes := make(map[string]string, len(e.Changes))
	for _, change := range e.Changes {
		changes[change.Key] = change.New
	}

	want := map[string]string{
		"source.kind": "identity-provider", "source.id": "idp-1",
		"alerted_at": marker.UTC().Format(time.RFC3339Nano), "at": at.UTC().Format(time.RFC3339Nano),
	}
	if len(changes) != len(want) {
		t.Fatalf("audit changes = %+v, want the source and the two times", e.Changes)
	}

	for key, value := range want {
		if changes[key] != value {
			t.Fatalf("audit change %s = %q, want %q", key, changes[key], value)
		}
	}
}

// checkInvalidMarkerWarning fails unless the log holds the one WARN line of
// the bad marker, with the source, the marker time and the removal time.
func checkInvalidMarkerWarning(t *testing.T, f *fixture, marker, at time.Time) {
	t.Helper()

	warnings := f.logs.warnings("after the time of the removal")
	if len(warnings) != 1 {
		t.Fatalf("WARN lines of the bad marker = %d, want 1", len(warnings))
	}

	attrs := make(map[string]string, warnings[0].NumAttrs())
	warnings[0].Attrs(func(a slog.Attr) bool {
		attrs[a.Key] = a.Value.String()

		return true
	})

	want := map[string]string{
		"code": errcode.PrincipalRemovalAlertMarkerInvalid, "source_kind": "identity-provider", "source_id": "idp-1",
		"alerted_at": marker.UTC().Format(time.RFC3339Nano), "at": at.UTC().Format(time.RFC3339Nano),
	}

	for key, value := range want {
		if attrs[key] != value {
			t.Fatalf("WARN attribute %s = %q, want %q", key, attrs[key], value)
		}
	}
}

// TestRecordMarkerAtTheRemovalTimeSilencesTheWindow proves that a marker equal
// to the time of the removal is a marker of this window: no alert and no entry
// of a bad marker.
func TestRecordMarkerAtTheRemovalTimeSilencesTheWindow(t *testing.T) {
	t.Parallel()

	f := newFixture()
	f.limits = principal.RemovalAlertLimits{Count: 2, Percent: 20, Window: 15 * time.Minute}
	f.setMarker(idpSource(), testAt())

	f.record(t, f.service(t), idpSource(), "grp-a", 2)

	if got := len(f.outbox.stored()); got != 0 {
		t.Fatalf("alerts = %d, want 0", got)
	}

	if got := len(f.audit.stored()); got != 0 {
		t.Fatalf("audit entries = %d, want 0", got)
	}

	if marker := f.log.alerted(idpSource()); !marker.Equal(testAt()) {
		t.Fatalf("alert marker = %s, want %s", marker, testAt())
	}
}

// TestRecordFutureMarkerBelowTheLimitWritesNoEntry proves that the service
// reads the marker only when a limit is reached: a removal below the limit
// writes no entry of a bad marker.
func TestRecordFutureMarkerBelowTheLimitWritesNoEntry(t *testing.T) {
	t.Parallel()

	f := newFixture()
	f.limits = principal.RemovalAlertLimits{Count: 2, Percent: 20, Window: 15 * time.Minute}
	f.setMarker(idpSource(), testAt().Add(365*24*time.Hour))

	f.record(t, f.service(t), idpSource(), "grp-a", 1)

	if got := len(f.audit.stored()); got != 0 {
		t.Fatalf("audit entries = %d, want 0", got)
	}

	if got := len(f.logs.warnings("after the time of the removal")); got != 0 {
		t.Fatalf("WARN lines of the bad marker = %d, want 0", got)
	}
}

// TestRecordFutureMarkerAuditFailureWritesNoAlert proves that the service
// fails closed when the entry of the bad marker cannot be written: Record
// returns the error, and no alert and no marker are written.
func TestRecordFutureMarkerAuditFailureWritesNoAlert(t *testing.T) {
	t.Parallel()

	f := newFixture()
	f.limits = principal.RemovalAlertLimits{Count: 2, Percent: 20, Window: 15 * time.Minute}

	future := testAt().Add(365 * 24 * time.Hour)
	f.setMarker(idpSource(), future)
	svc := f.service(t)

	f.record(t, svc, idpSource(), "grp-a", 1)

	f.audit.err = errAudit

	err := svc.Record(t.Context(), idpSource(), []string{"grp-a"})
	if !errors.Is(err, errAudit) {
		t.Fatalf("Record() = %v, want the audit error", err)
	}

	if got := len(f.outbox.stored()); got != 0 {
		t.Fatalf("alerts = %d, want 0", got)
	}

	if marker := f.log.alerted(idpSource()); !marker.Equal(future) {
		t.Fatalf("alert marker = %s, want the unchanged %s", marker, future)
	}
}

// TestRecordAlertsInTheNextWindow proves that the source alerts again once
// the window after its marker has passed.
func TestRecordAlertsInTheNextWindow(t *testing.T) {
	t.Parallel()

	f := newFixture()
	f.limits = principal.RemovalAlertLimits{Count: 1, Percent: 20, Window: 15 * time.Minute}
	svc := f.service(t)

	f.record(t, svc, idpSource(), "grp-a", 1)

	f.clock = f.clock.Add(15 * time.Minute)

	f.record(t, svc, idpSource(), "grp-a", 1)

	if got := len(f.outbox.stored()); got != 2 {
		t.Fatalf("alerts in two windows = %d, want 2", got)
	}
}

// TestRecordAuditFailureLeavesNoMarker proves that an audit failure leaves no
// marker, so the next removal tries the alert again.
func TestRecordAuditFailureLeavesNoMarker(t *testing.T) {
	t.Parallel()

	f := newFixture()
	f.limits = principal.RemovalAlertLimits{Count: 1, Percent: 20, Window: 15 * time.Minute}
	svc := f.service(t)

	f.audit.err = errAudit

	err := svc.Record(t.Context(), idpSource(), []string{"grp-a"})
	if !errors.Is(err, errAudit) {
		t.Fatalf("Record() = %v, want the audit error", err)
	}

	if got := f.log.alerted(idpSource()); !got.IsZero() {
		t.Fatalf("alert marker after an audit failure = %s, want the zero time", got)
	}

	f.audit.err = nil

	if recordErr := svc.Record(t.Context(), idpSource(), []string{"grp-a"}); recordErr != nil {
		t.Fatalf("Record() after the retry = %v", recordErr)
	}

	if got := len(f.outbox.stored()); got != 2 {
		t.Fatalf("notifications = %d, want 2", got)
	}

	if got := len(f.audit.stored()); got != 1 {
		t.Fatalf("audit entries = %d, want 1", got)
	}

	if got := f.log.alerted(idpSource()); !got.Equal(testAt()) {
		t.Fatalf("alert marker = %s, want %s", got, testAt())
	}
}

// TestRecordMarkerFailureReturnsAnError proves that a failed marker write
// makes Record return an error and keeps the warning out of the log.
func TestRecordMarkerFailureReturnsAnError(t *testing.T) {
	t.Parallel()

	f := newFixture()
	f.limits = principal.RemovalAlertLimits{Count: 1, Percent: 20, Window: 15 * time.Minute}
	f.log.markErr = errMark

	err := f.service(t).Record(t.Context(), idpSource(), []string{"grp-a"})
	if !errors.Is(err, errMark) {
		t.Fatalf("Record() = %v, want the marker error", err)
	}

	if got := f.log.alerted(idpSource()); !got.IsZero() {
		t.Fatalf("alert marker after a failed marker write = %s, want the zero time", got)
	}

	if strings.Contains(f.logs.text(), "removal alert") {
		t.Fatalf("log = %q, want no warning of an alert that failed to write", f.logs.text())
	}
}

// TestRecordCountWinsWhenBothReach proves that the count names the limit when
// both limits are at or above.
func TestRecordCountWinsWhenBothReach(t *testing.T) {
	t.Parallel()

	f := newFixture()
	f.limits = principal.RemovalAlertLimits{Count: 1, Percent: 1, Window: 15 * time.Minute}
	f.members.totals["grp-b"] = 4

	f.record(t, f.service(t), idpSource(), "grp-b", 1)

	notes := f.outbox.stored()
	if len(notes) != 1 {
		t.Fatalf("alerts = %d, want 1", len(notes))
	}

	if notes[0].Attributes["limit"] != "count" {
		t.Fatalf("limit = %q, want count", notes[0].Attributes["limit"])
	}
}
