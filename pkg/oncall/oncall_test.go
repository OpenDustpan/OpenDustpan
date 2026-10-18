package oncall_test

import (
	"errors"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/calendar"
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
				},
			}
		},
		"hours that end before start": func(s *oncall.Schedule) { s.Layers[1].Hours.To = s.Layers[1].Hours.From },
	}

	for name, change := range bad {
		s := network(t)
		change(&s)

		if err := s.Validate(); !errors.Is(err, oncall.ErrInvalid) {
			t.Fatalf("%s: err = %v, want ErrInvalid", name, err)
		}
	}
}
