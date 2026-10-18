package oncall_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/calendar"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/oncall"
)

func loc(t *testing.T) *time.Location {
	t.Helper()

	l, err := time.LoadLocation("Europe/Berlin")
	if err != nil {
		t.Fatalf("load zone: %v", err)
	}

	return l
}

// network is a schedule with a weekly rotation of three people that
// changes on Monday at 09:00, and a layer for the working hours of Erin
// on Wednesdays.
func network(t *testing.T) oncall.Schedule {
	t.Helper()

	l := loc(t)

	return oncall.Schedule{
		ID:        "ocs-network",
		Namespace: "shop",
		Name:      "network",
		Zone:      "Europe/Berlin",
		Source:    oncall.SourceLocal,
		External:  oncall.External{Provider: "", Key: ""},
		Layers: []oncall.Layer{
			{
				Name:    "primary",
				Members: []string{"alice", "bob", "carol"},
				Start:   time.Date(2026, 10, 5, 9, 0, 0, 0, l),
				Turn:    7 * 24 * time.Hour,
				Hours:   nil,
			},
			{
				Name:    "wednesday",
				Members: []string{"erin"},
				Start:   time.Date(2026, 10, 5, 0, 0, 0, 0, l),
				Turn:    24 * time.Hour,
				Hours: &oncall.Hours{
					Days: calendar.Weekdays(time.Wednesday),
					From: calendar.ClockOf(9, 0),
					To:   calendar.ClockOf(17, 0),
				},
			},
		},
		Overrides: nil,
		Copied:    nil,
		Synced:    time.Time{},
		Version:   1,
	}
}

func TestAt(t *testing.T) {
	t.Parallel()

	l := loc(t)
	at := func(m time.Month, d, h int) time.Time { return time.Date(2026, m, d, h, 0, 0, 0, l) }

	cases := []struct {
		name       string
		t          time.Time
		who        string
		start, end time.Time
	}{
		{"the first turn", at(10, 6, 12), "alice", at(10, 5, 9), at(10, 7, 9)},
		{"before the handoff", at(10, 12, 8), "alice", at(10, 7, 17), at(10, 12, 9)},
		{"after the handoff", at(10, 12, 9), "bob", at(10, 12, 9), at(10, 14, 9)},
		{"the turn keeps 09:00 over the change to winter time", at(10, 26, 10), "alice", at(10, 26, 9), at(10, 28, 9)},
		{"a later layer wins in its hours", at(10, 7, 10), "erin", at(10, 7, 9), at(10, 7, 17)},
	}

	s := network(t)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := s.At(tc.t)
			if err != nil {
				t.Fatalf("At: %v", err)
			}

			if got.Principal != tc.who || !got.Start.Equal(tc.start) || !got.End.Equal(tc.end) {
				t.Fatalf("At(%s) = %s from %s to %s, want %s from %s to %s",
					tc.t, got.Principal, got.Start, got.End, tc.who, tc.start, tc.end)
			}
		})
	}

	if _, err := s.At(at(10, 1, 12)); !errors.Is(err, oncall.ErrNobodyOnCall) {
		t.Fatalf("before the start: err = %v, want ErrNobodyOnCall", err)
	}
}

func TestOverrideWins(t *testing.T) {
	t.Parallel()

	l := loc(t)
	s := network(t)
	s.Overrides = []oncall.Override{{
		ID: "ovr-1", Principal: "dave", Start: time.Date(2026, 10, 7, 12, 0, 0, 0, l),
		End: time.Date(2026, 10, 7, 14, 0, 0, 0, l), Reason: "Erin at the dentist", CreatedBy: "erin",
		CreatedAt: time.Date(2026, 10, 6, 12, 0, 0, 0, l),
	}}

	got, err := s.Timeline(time.Date(2026, 10, 7, 8, 0, 0, 0, l), time.Date(2026, 10, 7, 18, 0, 0, 0, l))
	if err != nil {
		t.Fatalf("Timeline: %v", err)
	}

	want := []struct {
		who   string
		layer string
		end   int
	}{{"alice", "primary", 9}, {"erin", "wednesday", 12}, {"dave", oncall.OriginOverride, 14}, {"erin", "wednesday", 17}, {"alice", "primary", 18}}

	if len(got) != len(want) {
		t.Fatalf("Timeline = %+v, want %d shifts", got, len(want))
	}

	for i, w := range want {
		if got[i].Principal != w.who || got[i].Layer != w.layer || got[i].End.In(l).Hour() != w.end {
			t.Fatalf("shift %d = %+v, want %s on %s until %d:00", i, got[i], w.who, w.layer, w.end)
		}
	}
}

func TestCurrentAndNext(t *testing.T) {
	t.Parallel()

	l := loc(t)
	s := network(t)
	s.Layers = s.Layers[:1]

	cur, next, err := s.CurrentAndNext(time.Date(2026, 10, 15, 22, 0, 0, 0, l))
	if err != nil {
		t.Fatalf("CurrentAndNext: %v", err)
	}

	if cur.Principal != "bob" || !cur.End.Equal(time.Date(2026, 10, 19, 9, 0, 0, 0, l)) {
		t.Fatalf("current = %+v, want bob until Monday 09:00", cur)
	}

	if next == nil || next.Principal != "carol" {
		t.Fatalf("next = %+v, want carol", next)
	}
}

func TestExternalCopy(t *testing.T) {
	t.Parallel()

	start := time.Date(2026, 10, 20, 0, 0, 0, 0, time.UTC)
	s := network(t)
	s.Source, s.External, s.Layers = oncall.SourceExternal, oncall.External{Provider: "pager", Key: "P123"}, nil
	s.Copied = []oncall.Shift{
		{Schedule: "", Principal: "", Display: "Frank Ext", Start: start, End: start.Add(12 * time.Hour), Layer: ""},
		{
			Schedule:  "",
			Principal: "alice",
			Display:   "Alice",
			Start:     start.Add(12 * time.Hour),
			End:       start.Add(24 * time.Hour),
			Layer:     "",
		},
	}

	cur, next, err := s.CurrentAndNext(start.Add(time.Hour))
	if err != nil {
		t.Fatalf("CurrentAndNext: %v", err)
	}

	if cur.Display != "Frank Ext" || cur.Layer != oncall.OriginExternal || next == nil || next.Principal != "alice" {
		t.Fatalf("current %+v, next %+v; want Frank then alice", cur, next)
	}
}

func TestParseRef(t *testing.T) {
	t.Parallel()

	cases := []struct {
		field string
		ok    bool
		want  oncall.Ref
	}{
		{"schedule:network", true, oncall.Ref{Namespace: "shop", Name: "network"}},
		{" schedule:platform/db ", true, oncall.Ref{Namespace: "platform", Name: "db"}},
		{"pager:network", false, oncall.Ref{Namespace: "", Name: ""}},
		{"#network-oncall", false, oncall.Ref{Namespace: "", Name: ""}},
		{"schedule:", false, oncall.Ref{Namespace: "", Name: ""}},
		{"schedule:Network", false, oncall.Ref{Namespace: "", Name: ""}},
	}

	for _, tc := range cases {
		got, ok := oncall.ParseRef(tc.field, "shop")
		if ok != tc.ok || got != tc.want {
			t.Fatalf("ParseRef(%q) = %+v, %t; want %+v, %t", tc.field, got, ok, tc.want, tc.ok)
		}
	}
}

func TestValidate(t *testing.T) {
	t.Parallel()

	bad := map[string]func(s *oncall.Schedule){
		"a name with capitals":     func(s *oncall.Schedule) { s.Name = "Network" },
		"an unknown zone":          func(s *oncall.Schedule) { s.Zone = "Mars/Olympus" },
		"a layer with no members":  func(s *oncall.Schedule) { s.Layers[0].Members = nil },
		"a turn below the minimum": func(s *oncall.Schedule) { s.Layers[0].Turn = time.Minute },
		"a layer named override":   func(s *oncall.Schedule) { s.Layers[0].Name = oncall.OriginOverride },
		"an external with no key":  func(s *oncall.Schedule) { s.Source, s.Layers = oncall.SourceExternal, nil },
		"an external with layers": func(s *oncall.Schedule) {
			s.Source, s.External = oncall.SourceExternal, oncall.External{Provider: "p", Key: "k"}
		},
		"an override with no reason": func(s *oncall.Schedule) {
			s.Overrides = []oncall.Override{
				{
					ID:        "o",
					Principal: "a",
					Start:     s.Layers[0].Start,
					End:       s.Layers[0].Start.Add(time.Hour),
					Reason:    "",
					CreatedBy: "a",
					CreatedAt: s.Layers[0].Start,
				},
			}
		},
		"hours that end before start": func(s *oncall.Schedule) { s.Layers[1].Hours.To = s.Layers[1].Hours.From },
		"the zone of the host":        func(s *oncall.Schedule) { s.Zone = "Local" },
		"an empty zone":               func(s *oncall.Schedule) { s.Zone = "" },
		"a zone path":                 func(s *oncall.Schedule) { s.Zone = "/etc/localtime" },
		"the localtime zone":          func(s *oncall.Schedule) { s.Zone = "localtime" },
		"the posixrules zone":         func(s *oncall.Schedule) { s.Zone = "posixrules" },
		"the Factory zone":            func(s *oncall.Schedule) { s.Zone = "Factory" },
		"a zone with dots":            func(s *oncall.Schedule) { s.Zone = "../zoneinfo/UTC" },
		"the server scope":            func(s *oncall.Schedule) { s.Namespace = "@server" },
		"no namespace":                func(s *oncall.Schedule) { s.Namespace = "" },
	}

	for name, change := range bad {
		s := network(t)
		change(&s)

		if err := s.Validate(); !errors.Is(err, oncall.ErrInvalid) {
			t.Fatalf("%s: err = %v, want ErrInvalid", name, err)
		}
	}
}

// rotation returns a schedule of one layer in Europe/Berlin with members
// that take turns of length turn from start.
func rotation(t *testing.T, start time.Time, turn time.Duration, members ...string) oncall.Schedule {
	t.Helper()

	s := network(t)
	s.Layers = []oncall.Layer{{Name: "primary", Members: members, Start: start, Turn: turn, Hours: nil}}

	return s
}

func TestYoungSchedule(t *testing.T) {
	t.Parallel()

	l := loc(t)
	at := func(m time.Month, d, h int) time.Time { return time.Date(2026, m, d, h, 0, 0, 0, l) }
	s := rotation(t, at(10, 5, 9), 7*24*time.Hour, "alice", "bob", "carol")

	cases := []struct {
		name       string
		t          time.Time
		who        string
		start, end time.Time
	}{
		{"the third turn", at(10, 20, 12), "carol", at(10, 19, 9), at(10, 26, 9)},
		{"the second turn", at(10, 13, 9), "bob", at(10, 12, 9), at(10, 19, 9)},
		{"the first turn", at(10, 5, 9), "alice", at(10, 5, 9), at(10, 12, 9)},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := s.At(tc.t)
			if err != nil {
				t.Fatalf("At: %v", err)
			}

			if got.Principal != tc.who || !got.Start.Equal(tc.start) || !got.End.Equal(tc.end) {
				t.Fatalf("At(%s) = %s from %s to %s, want %s from %s to %s",
					tc.t, got.Principal, got.Start, got.End, tc.who, tc.start, tc.end)
			}
		})
	}

	got, err := s.Timeline(at(10, 1, 0), at(10, 30, 0))
	if err != nil {
		t.Fatalf("Timeline: %v", err)
	}

	want := []struct {
		who        string
		start, end time.Time
	}{
		{"alice", at(10, 5, 9), at(10, 12, 9)},
		{"bob", at(10, 12, 9), at(10, 19, 9)},
		{"carol", at(10, 19, 9), at(10, 26, 9)},
		{"alice", at(10, 26, 9), at(10, 30, 0)},
	}

	if len(got) != len(want) {
		t.Fatalf("Timeline from before the start = %+v, want %d shifts", got, len(want))
	}

	for i, w := range want {
		if got[i].Principal != w.who || !got[i].Start.Equal(w.start) || !got[i].End.Equal(w.end) {
			t.Fatalf("shift %d = %+v, want %s from %s to %s", i, got[i], w.who, w.start, w.end)
		}
	}
}

func TestTurnsFollowTheWallClock(t *testing.T) {
	t.Parallel()

	l := loc(t)
	local := func(m time.Month, d, h int) time.Time { return time.Date(2026, m, d, h, 0, 0, 0, l) }
	utc := func(m time.Month, d, h, mi int) time.Time { return time.Date(2026, m, d, h, mi, 0, 0, time.UTC) }

	cases := []struct {
		name       string
		s          oncall.Schedule
		t          time.Time
		who        string
		start, end time.Time
	}{
		{
			"12-hour turns keep 08:00 after the change to winter time",
			rotation(t, local(10, 20, 8), 12*time.Hour, "alice", "bob"),
			local(10, 26, 10), "alice", local(10, 26, 8), local(10, 26, 20),
		},
		{
			"12-hour turns keep 20:00 after the change to winter time",
			rotation(t, local(10, 20, 8), 12*time.Hour, "alice", "bob"),
			local(10, 26, 21), "bob", local(10, 26, 20), local(10, 27, 8),
		},
		{
			"12-hour turns keep 08:00 after the change to summer time",
			rotation(t, local(3, 25, 8), 12*time.Hour, "alice", "bob"),
			local(3, 30, 9), "alice", local(3, 30, 8), local(3, 30, 20),
		},
		{
			"the turn before the skipped hour ends at its end",
			rotation(t, local(3, 29, 0), time.Hour, "alice", "bob", "carol"),
			utc(3, 29, 0, 30), "bob", utc(3, 29, 0, 0), utc(3, 29, 1, 0),
		},
		{
			"a turn in the skipped hour is empty",
			rotation(t, local(3, 29, 0), time.Hour, "alice", "bob", "carol"),
			utc(3, 29, 1, 30), "alice", utc(3, 29, 1, 0), utc(3, 29, 2, 0),
		},
		{
			"a turn that ends in the repeated hour ends at its second occurrence",
			rotation(t, local(10, 25, 0), time.Hour, "alice", "bob", "carol"),
			utc(10, 25, 0, 30), "bob", utc(10, 24, 23, 0), utc(10, 25, 1, 0),
		},
		{
			"the turn after the repeated hour starts at its second occurrence",
			rotation(t, local(10, 25, 0), time.Hour, "alice", "bob", "carol"),
			utc(10, 25, 1, 30), "carol", utc(10, 25, 1, 0), utc(10, 25, 2, 0),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := tc.s.At(tc.t)
			if err != nil {
				t.Fatalf("At: %v", err)
			}

			if got.Principal != tc.who || !got.Start.Equal(tc.start) || !got.End.Equal(tc.end) {
				t.Fatalf("At(%s) = %s from %s to %s, want %s from %s to %s",
					tc.t, got.Principal, got.Start, got.End, tc.who, tc.start, tc.end)
			}
		})
	}
}

// rotationIn returns rotation with turns of one hour in the zone of start.
func rotationIn(t *testing.T, start time.Time, members ...string) oncall.Schedule {
	t.Helper()

	s := rotation(t, start, time.Hour, members...)
	s.Zone = start.Location().String()

	return s
}

func zone(t *testing.T, name string) *time.Location {
	t.Helper()

	l, err := time.LoadLocation(name)
	if err != nil {
		t.Fatalf("load zone: %v", err)
	}

	return l
}

// TestTurnsAtAChangeOfTheClock checks the turns at a change of the clock
// in zones west of UTC and with a change of 30 minutes, where time.Date
// alone gives another instant for a skipped or a repeated wall time.
func TestTurnsAtAChangeOfTheClock(t *testing.T) {
	t.Parallel()

	ny, lh := zone(t, "America/New_York"), zone(t, "Australia/Lord_Howe")
	utc := func(m time.Month, d, h, mi int) time.Time { return time.Date(2026, m, d, h, mi, 0, 0, time.UTC) }
	abc := []string{"alice", "bob", "carol"}

	cases := []struct {
		name       string
		s          oncall.Schedule
		t          time.Time
		who        string
		start, end time.Time
	}{
		{
			"New York: the turn before the skipped hour keeps its hour",
			rotationIn(t, time.Date(2026, 3, 8, 0, 0, 0, 0, ny), abc...),
			utc(3, 8, 6, 30), "bob", utc(3, 8, 6, 0), utc(3, 8, 7, 0),
		},
		{
			"New York: the turn after the skipped hour starts at 03:00",
			rotationIn(t, time.Date(2026, 3, 8, 0, 0, 0, 0, ny), abc...),
			utc(3, 8, 7, 30), "alice", utc(3, 8, 7, 0), utc(3, 8, 8, 0),
		},
		{
			"New York: a turn from 02:30 starts at 03:00",
			rotationIn(t, time.Date(2026, 3, 7, 22, 30, 0, 0, ny), abc...),
			utc(3, 8, 7, 10), "bob", utc(3, 8, 7, 0), utc(3, 8, 7, 30),
		},
		{
			"New York: the turn that ends in the repeated hour gets the extra hour",
			rotationIn(t, time.Date(2026, 11, 1, 0, 0, 0, 0, ny), abc...),
			utc(11, 1, 5, 30), "alice", utc(11, 1, 4, 0), utc(11, 1, 6, 0),
		},
		{
			"New York: the turn from the repeated hour starts at its second occurrence",
			rotationIn(t, time.Date(2026, 11, 1, 0, 0, 0, 0, ny), abc...),
			utc(11, 1, 6, 30), "bob", utc(11, 1, 6, 0), utc(11, 1, 7, 0),
		},
		{
			"Lord Howe: a turn from the skipped half hour starts at its end",
			rotationIn(t, time.Date(2026, 10, 4, 0, 0, 0, 0, lh), abc...),
			utc(10, 3, 15, 45), "carol", utc(10, 3, 15, 30), utc(10, 3, 16, 0),
		},
		{
			"Lord Howe: the turn before the skipped half hour ends at the change",
			rotationIn(t, time.Date(2026, 10, 4, 0, 0, 0, 0, lh), abc...),
			utc(10, 3, 15, 0), "bob", utc(10, 3, 14, 30), utc(10, 3, 15, 30),
		},
		{
			"Lord Howe: a turn that ends at 01:30 gets the repeated half hour",
			rotationIn(t, time.Date(2026, 4, 4, 23, 30, 0, 0, lh), abc...),
			utc(4, 4, 14, 45), "bob", utc(4, 4, 13, 30), utc(4, 4, 15, 0),
		},
		{
			"Lord Howe: a turn from 01:30 starts at its second occurrence",
			rotationIn(t, time.Date(2026, 4, 4, 23, 30, 0, 0, lh), abc...),
			utc(4, 4, 15, 10), "carol", utc(4, 4, 15, 0), utc(4, 4, 16, 0),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := tc.s.At(tc.t)
			if err != nil {
				t.Fatalf("At: %v", err)
			}

			if got.Principal != tc.who || !got.Start.Equal(tc.start) || !got.End.Equal(tc.end) {
				t.Fatalf("At(%s) = %s from %s to %s, want %s from %s to %s",
					tc.t, got.Principal, got.Start.UTC(), got.End.UTC(), tc.who, tc.start, tc.end)
			}
		})
	}
}

var errAccess = errors.New("access rules unavailable")

// getter is a fake oncall.Getter. It counts its calls.
type getter struct {
	schedules map[oncall.Ref]oncall.Schedule
	err       error
	calls     *int
}

func (g getter) Get(_ context.Context, ref oncall.Ref) (oncall.Schedule, error) {
	*g.calls++

	if g.err != nil {
		return oncall.Schedule{}, g.err
	}

	s, ok := g.schedules[ref]
	if !ok {
		return s, fmt.Errorf("get: %w", oncall.ErrNotFound)
	}

	return s, nil
}

func TestResolve(t *testing.T) {
	t.Parallel()

	l := loc(t)
	now := time.Date(2026, 10, 15, 22, 0, 0, 0, l)

	own := network(t)
	own.Layers = own.Layers[:1]

	other := own
	other.Namespace, other.Name, other.ID = "platform", "db", "ocs-db"

	feed := own
	feed.Name, feed.ID, feed.Layers = "feed", "ocs-feed", nil
	feed.Source, feed.External = oncall.SourceExternal, oncall.External{Provider: "pager", Key: "P1"}
	feed.Synced = now.Add(-2 * time.Hour)
	feed.Copied = []oncall.Shift{
		{
			Schedule:  "",
			Principal: "",
			Display:   "Frank Ext",
			Start:     now.Add(-time.Hour),
			End:       now.Add(time.Hour),
			Layer:     "",
		},
	}

	young := own
	young.Name, young.ID = "young", "ocs-young"
	young.Layers = []oncall.Layer{{
		Name: "primary", Members: []string{"alice"}, Start: now.Add(time.Hour), Turn: 24 * time.Hour, Hours: nil,
	}}

	schedules := map[oncall.Ref]oncall.Schedule{
		{Namespace: "shop", Name: "network"}: own,
		{Namespace: "platform", Name: "db"}:  other,
		{Namespace: "shop", Name: "feed"}:    feed,
		{Namespace: "shop", Name: "young"}:   young,
	}

	allow := func(answer bool, err error) oncall.CanRead {
		return func(context.Context, namespace.Name) (bool, error) { return answer, err }
	}

	cases := []struct {
		name    string
		field   string
		canRead oncall.CanRead
		getErr  error
		status  oncall.Status
		who     string
		next    string
		stale   bool
		reads   int
		wantErr error
	}{
		{"text stays text", "pager:network", nil, nil, oncall.StatusText, "", "", false, 0, nil},
		{
			"a schedule of the own namespace needs no check", "schedule:network", nil, nil,
			oncall.StatusResolved, "bob", "carol", false, 1, nil,
		},
		{
			"a schedule of the own namespace by its full name", "schedule:shop/network", nil, nil,
			oncall.StatusResolved, "bob", "carol", false, 1, nil,
		},
		{
			"another namespace with namespace:read", "schedule:platform/db", allow(true, nil), nil,
			oncall.StatusResolved, "bob", "carol", false, 1, nil,
		},
		{
			"another namespace without namespace:read", "schedule:platform/db", allow(false, nil), nil,
			oncall.StatusDenied, "", "", false, 0, nil,
		},
		{
			"another namespace with no access rules", "schedule:platform/db", nil, nil,
			oncall.StatusDenied, "", "", false, 0, nil,
		},
		{
			"the access rules fail", "schedule:platform/db", allow(false, errAccess), nil,
			oncall.StatusDenied, "", "", false, 0, errAccess,
		},
		{"an unknown schedule", "schedule:none", nil, nil, oncall.StatusMissing, "", "", false, 1, nil},
		{"nobody on call", "schedule:young", nil, nil, oncall.StatusNobody, "", "", false, 1, nil},
		{"an old copy is stale", "schedule:feed", nil, nil, oncall.StatusResolved, "", "", true, 1, nil},
		{"the store fails", "schedule:network", nil, errAccess, oncall.StatusDenied, "", "", false, 1, errAccess},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			calls := 0
			g := getter{schedules: schedules, err: tc.getErr, calls: &calls}

			r := oncall.Resolver{Get: g, CanRead: tc.canRead}

			got, err := r.Resolve(t.Context(), tc.field, "shop", now)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("Resolve: err = %v, want %v", err, tc.wantErr)
			}

			if calls != tc.reads {
				t.Fatalf("Resolve read the store %d times, want %d", calls, tc.reads)
			}

			if tc.wantErr == nil {
				checkResolution(t, got, want{status: tc.status, who: tc.who, next: tc.next, stale: tc.stale})
			}
		})
	}
}

// want is the expected resolution. An empty who or next is not checked.
type want struct {
	status oncall.Status
	who    string
	next   string
	stale  bool
}

// checkResolution checks got against w.
func checkResolution(t *testing.T, got oncall.Resolution, w want) {
	t.Helper()

	if got.Status != w.status || got.Stale != w.stale {
		t.Fatalf("Resolve = %s, stale %t; want %s, stale %t", got.Status, got.Stale, w.status, w.stale)
	}

	if got.Status != oncall.StatusResolved && (got.Current != nil || got.Next != nil) {
		t.Fatalf("Resolve = %s with a person, want no person", got.Status)
	}

	if w.who != "" && (got.Current == nil || got.Current.Principal != w.who) {
		t.Fatalf("current = %+v, want %s", got.Current, w.who)
	}

	if w.next != "" && (got.Next == nil || got.Next.Principal != w.next) {
		t.Fatalf("next = %+v, want %s", got.Next, w.next)
	}
}
