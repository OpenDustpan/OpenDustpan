package breakglass_test

import (
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/breakglass"
	"opendustpan.org/opendustpan/pkg/calendar"
)

func berlin(t *testing.T) (calendar.Resolved, *time.Location) {
	t.Helper()

	c := calendar.Default()
	c.ID = "cal-berlin"
	c.Name = "Berlin"
	c.Zone = "Europe/Berlin"

	loc, err := time.LoadLocation(c.Zone)
	if err != nil {
		t.Fatalf("load zone: %v", err)
	}

	return calendar.Resolved{Calendar: c, From: calendar.Scope{Namespace: "shop", Group: ""}}, loc
}

func use(at time.Time) breakglass.Use {
	return breakglass.Use{
		Kind:    breakglass.KindFreezeBreak,
		Ref:     "job-1",
		Scope:   "shop",
		Object:  "opentofu:shop/network",
		Subject: "alice",
		People:  []string{"alice", "carol"},
		Reason:  "Roll back the bad route table",
		At:      at,
		Until:   time.Time{},
	}
}

// session returns the use of a break-glass account session that signed in
// at at, with a time limit of two hours.
func session(at time.Time) breakglass.Use {
	u := use(at)
	u.Kind, u.Ref, u.Scope, u.Object, u.Until = breakglass.KindAccountSession, "ses-1", audit.ServerScope, "", at.Add(
		2*time.Hour,
	)
	u.People = []string{"alice", "erin"}

	return u
}

func TestUseValidate(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 10, 21, 10, 0, 0, 0, time.UTC)

	cases := []struct {
		name   string
		u      breakglass.Use
		change func(u *breakglass.Use)
		valid  bool
	}{
		{"a freeze break", use(at), func(*breakglass.Use) {}, true},
		{"a session", session(at), func(*breakglass.Use) {}, true},
		{"an unknown kind", use(at), func(u *breakglass.Use) { u.Kind = "other" }, false},
		{"no reason", use(at), func(u *breakglass.Use) { u.Reason = "" }, false},
		{"no subject in the people", use(at), func(u *breakglass.Use) { u.People = []string{"carol"} }, false},
		{"an empty person", use(at), func(u *breakglass.Use) { u.People = append(u.People, "") }, false},
		{"the subject with another case", use(at), func(u *breakglass.Use) { u.People = []string{"Alice"} }, true},
		{"a freeze break with no unit", use(at), func(u *breakglass.Use) { u.Object = "" }, false},
		{"a freeze break in the server scope", use(at), func(u *breakglass.Use) { u.Scope = audit.ServerScope }, false},
		{"a freeze break with a time limit", use(at), func(u *breakglass.Use) { u.Until = at.Add(time.Hour) }, false},
		{"a session in a namespace", session(at), func(u *breakglass.Use) { u.Scope = "shop" }, false},
		{"a session with a unit", session(at), func(u *breakglass.Use) { u.Object = "opentofu:shop/network" }, false},
		{"a session with no time limit", session(at), func(u *breakglass.Use) { u.Until = time.Time{} }, false},
		{"a session that ends at its sign-in", session(at), func(u *breakglass.Use) { u.Until = at }, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			u := tc.u
			tc.change(&u)

			err := u.Validate()
			if tc.valid && err != nil {
				t.Fatalf("Validate: %v", err)
			}

			if !tc.valid && !errors.Is(err, breakglass.ErrInvalid) {
				t.Fatalf("Validate: err = %v, want ErrInvalid", err)
			}
		})
	}
}

func TestOpenCountsASessionFromItsTimeLimit(t *testing.T) {
	t.Parallel()

	cal, loc := berlin(t)
	// A sign-in on Friday at 16:00 with a time limit of two hours ends
	// after the working hours, so the deadline is the end of Monday.
	friday := time.Date(2026, 10, 23, 16, 0, 0, 0, loc)

	r, err := breakglass.Open("bgr-1", session(friday), breakglass.DefaultSettings(), cal, friday)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	if want := time.Date(2026, 10, 26, 17, 0, 0, 0, loc); !r.DueAt.Equal(want) {
		t.Fatalf("DueAt = %s, want %s", r.DueAt, want)
	}

	// The first notice goes out at the sign-in, in business hours.
	if !r.NextNoticeAt.Equal(friday) {
		t.Fatalf("NextNoticeAt = %s, want %s", r.NextNoticeAt, friday)
	}
}

func TestOpenSetsTheDeadline(t *testing.T) {
	t.Parallel()

	cal, loc := berlin(t)
	friday := time.Date(2026, 10, 23, 22, 0, 0, 0, loc)

	r, err := breakglass.Open("bgr-1", use(friday), breakglass.DefaultSettings(), cal, friday)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	if want := time.Date(2026, 10, 26, 17, 0, 0, 0, loc); !r.DueAt.Equal(want) {
		t.Fatalf("DueAt = %s, want %s", r.DueAt, want)
	}

	// The first notice waits for business hours.
	if want := time.Date(2026, 10, 26, 9, 0, 0, 0, loc); !r.NextNoticeAt.Equal(want) {
		t.Fatalf("NextNoticeAt = %s, want %s", r.NextNoticeAt, want)
	}

	if r.Calendar != "cal-berlin" || r.CalendarFrom.Namespace != "shop" {
		t.Fatalf("calendar = %s from %s, want cal-berlin from the namespace", r.Calendar, r.CalendarFrom)
	}

	off := breakglass.DefaultSettings()
	off.Enabled = false

	r, err = breakglass.Open("bgr-2", use(friday), off, cal, friday)
	if err != nil || !r.DueAt.IsZero() || !r.NextNoticeAt.IsZero() {
		t.Fatalf("opt-in off: due %s, next %s (%v), want no deadline and no notice", r.DueAt, r.NextNoticeAt, err)
	}
}

func TestAdvanceSendsEachStep(t *testing.T) {
	t.Parallel()

	cal, loc := berlin(t)
	tuesday := func(d, h, m int) time.Time { return time.Date(2026, 10, d, h, m, 0, 0, loc) }
	s := breakglass.DefaultSettings()

	r, err := breakglass.Open("bgr-1", use(tuesday(20, 10, 0)), s, cal, tuesday(20, 10, 0))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	steps := []struct {
		at       time.Time
		step     string
		overdue  bool
		audience []breakglass.Audience
		next     time.Time
	}{
		{
			tuesday(20, 10, 0),
			"review-needed",
			false,
			[]breakglass.Audience{breakglass.AudienceReviewers},
			tuesday(21, 9, 0),
		},
		{
			tuesday(21, 9, 0),
			"reminder",
			false,
			[]breakglass.Audience{breakglass.AudienceReviewers, breakglass.AudienceUser},
			tuesday(21, 17, 0),
		},
		{
			tuesday(21, 17, 0),
			"overdue",
			true,
			[]breakglass.Audience{breakglass.AudienceReviewers, breakglass.AudienceUser, breakglass.AudienceOwners},
			tuesday(22, 13, 0),
		},
		{
			tuesday(22, 13, 0),
			"escalation",
			true,
			[]breakglass.Audience{breakglass.AudienceAdmins, breakglass.AudienceOnCall},
			tuesday(23, 13, 0),
		},
		{
			tuesday(23, 13, 0),
			"escalation",
			true,
			[]breakglass.Audience{breakglass.AudienceAdmins, breakglass.AudienceOnCall},
			tuesday(26, 13, 0),
		},
	}

	for _, want := range steps {
		var n *breakglass.Notice

		r, n, err = breakglass.Advance(r, s, cal.Calendar, want.at)
		if err != nil {
			t.Fatalf("Advance at %s: %v", want.at, err)
		}

		if n == nil || n.Step != want.step || n.Overdue != want.overdue || !slices.Equal(n.Audience, want.audience) {
			t.Fatalf(
				"Advance at %s = %+v, want step %s, overdue %t, audience %v",
				want.at,
				n,
				want.step,
				want.overdue,
				want.audience,
			)
		}

		if !r.NextNoticeAt.Equal(want.next) {
			t.Fatalf("after %s: NextNoticeAt = %s, want %s", want.step, r.NextNoticeAt, want.next)
		}
	}
}

func TestAdvanceJoinsMissedSteps(t *testing.T) {
	t.Parallel()

	cal, loc := berlin(t)
	start := time.Date(2026, 10, 20, 10, 0, 0, 0, loc)
	s := breakglass.DefaultSettings()

	r, err := breakglass.Open("bgr-1", use(start), s, cal, start)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	// The server was down from Tuesday to Thursday noon. One notice goes
	// out, with the audiences of the four steps that it missed.
	r, n, err := breakglass.Advance(r, s, cal.Calendar, time.Date(2026, 10, 22, 14, 0, 0, 0, loc))
	if err != nil || n == nil {
		t.Fatalf("Advance = %v (%v), want a notice", n, err)
	}

	if n.Step != "escalation" || len(n.Audience) != 5 || r.Step != 4 {
		t.Fatalf("notice = %+v, step %d; want the escalation with five audiences after four steps", n, r.Step)
	}

	_, again, err := breakglass.Advance(r, s, cal.Calendar, time.Date(2026, 10, 22, 14, 1, 0, 0, loc))
	if err != nil || again != nil {
		t.Fatalf("second Advance = %+v (%v), want no notice before the next step", again, err)
	}
}

func TestAcceptAndDispute(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 10, 21, 10, 0, 0, 0, time.UTC)
	fresh := breakglass.Reviewer{
		Subject: "bob", Agent: "", Session: "ses-bob", TokenID: "",
		Auth: breakglass.AuthMFA, AuthAt: now.Add(-time.Minute),
	}
	pending := pendingReview(use(now.Add(-time.Hour)))
	ended := pendingReview(session(now.Add(-3 * time.Hour)))
	active := pendingReview(session(now.Add(-time.Hour)))
	same := func(r breakglass.Reviewer) breakglass.Reviewer { return r }

	cases := []struct {
		name   string
		r      breakglass.Review
		rev    func(r breakglass.Reviewer) breakglass.Reviewer
		decide decision
		note   string
		want   error
	}{
		{
			"a second person accepts",
			pending,
			func(r breakglass.Reviewer) breakglass.Reviewer { return r },
			breakglass.Accept,
			"",
			nil,
		},
		{
			"a dispute with a note",
			pending,
			func(r breakglass.Reviewer) breakglass.Reviewer { return r },
			breakglass.Dispute,
			"No incident open",
			nil,
		},
		{
			"a dispute needs a note",
			pending,
			func(r breakglass.Reviewer) breakglass.Reviewer { return r },
			breakglass.Dispute,
			" ",
			breakglass.ErrInvalid,
		},
		{
			"the user cannot review",
			pending,
			func(r breakglass.Reviewer) breakglass.Reviewer { r.Subject = "Alice"; return r },
			breakglass.Accept,
			"",
			breakglass.ErrSelfReview,
		},
		{
			"an agent cannot review",
			pending,
			func(r breakglass.Reviewer) breakglass.Reviewer { r.Agent = "dusty"; return r },
			breakglass.Accept,
			"",
			breakglass.ErrAgentDenied,
		},
		{"an old authentication", pending, func(r breakglass.Reviewer) breakglass.Reviewer {
			r.AuthAt = now.Add(-breakglass.Freshness - time.Second)
			return r
		}, breakglass.Accept, "", breakglass.ErrAuthStale},
		{
			"no authentication",
			pending,
			func(r breakglass.Reviewer) breakglass.Reviewer { r.Auth = ""; return r },
			breakglass.Accept,
			"",
			breakglass.ErrAuthStale,
		},
		{
			"no reviewer",
			pending,
			func(r breakglass.Reviewer) breakglass.Reviewer { r.Subject = " "; return r },
			breakglass.Accept,
			"",
			breakglass.ErrInvalid,
		},
		{
			"a person who can act as the user cannot review",
			pending,
			func(r breakglass.Reviewer) breakglass.Reviewer { r.Subject = "Carol"; return r },
			breakglass.Accept,
			"",
			breakglass.ErrSelfReview,
		},
		{
			"the owner of the break-glass account cannot review",
			ended,
			func(r breakglass.Reviewer) breakglass.Reviewer { r.Subject = "erin"; return r },
			breakglass.Accept,
			"",
			breakglass.ErrSelfReview,
		},
		{"a second person accepts an ended session", ended, same, breakglass.Accept, "", nil},
		{"a session in its time limit", active, same, breakglass.Dispute, "Too long", breakglass.ErrInUse},
		{
			"an access token cannot review",
			pending,
			func(r breakglass.Reviewer) breakglass.Reviewer { r.TokenID = "tok-1"; return r },
			breakglass.Accept,
			"",
			breakglass.ErrNeedsSession,
		},
		{
			"a request with no console session",
			pending,
			func(r breakglass.Reviewer) breakglass.Reviewer { r.Session = ""; return r },
			breakglass.Accept,
			"",
			breakglass.ErrNeedsSession,
		},
		{
			"an authentication in the future",
			pending,
			func(r breakglass.Reviewer) breakglass.Reviewer { r.AuthAt = now.Add(time.Minute); return r },
			breakglass.Accept,
			"",
			breakglass.ErrAuthStale,
		},
		{
			"an unknown kind of authentication",
			pending,
			func(r breakglass.Reviewer) breakglass.Reviewer { r.Auth = "password"; return r },
			breakglass.Accept,
			"",
			breakglass.ErrAuthStale,
		},
		{
			"a note above the bound",
			pending,
			same,
			breakglass.Accept,
			strings.Repeat("x", breakglass.MaxNoteLen+1),
			breakglass.ErrInvalid,
		},
		{
			"a review with a verdict",
			func() breakglass.Review { r := pending; r.State = breakglass.StateAccepted; return r }(),
			func(r breakglass.Reviewer) breakglass.Reviewer { return r },
			breakglass.Accept,
			"",
			breakglass.ErrReviewed,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := tc.decide(tc.r, tc.rev(fresh), tc.note, now)
			if tc.want != nil {
				if !errors.Is(err, tc.want) {
					t.Fatalf("verdict: err = %v, want %v", err, tc.want)
				}

				return
			}

			if err != nil {
				t.Fatalf("verdict: %v", err)
			}

			if got.State == breakglass.StatePending || got.Verdict.Reviewer != "bob" || !got.NextNoticeAt.IsZero() {
				t.Fatalf("verdict = %+v, want a verdict by bob and no next notice", got)
			}
		})
	}
}

// decision is Accept or Dispute.
type decision func(breakglass.Review, breakglass.Reviewer, string, time.Time) (breakglass.Review, error)

// pendingReview returns a pending review of u with no deadline and no
// verdict.
func pendingReview(u breakglass.Use) breakglass.Review {
	var r breakglass.Review

	r.ID, r.Use, r.State = "bgr-1", u, breakglass.StatePending

	return r
}

func TestSettingsValidate(t *testing.T) {
	t.Parallel()

	if err := breakglass.DefaultSettings().Validate(); err != nil {
		t.Fatalf("default: %v", err)
	}

	bad := []func(s *breakglass.Settings){
		func(s *breakglass.Settings) { s.Repeat = time.Minute },
		func(s *breakglass.Settings) { s.Steps[0].Base = "later" },
		func(s *breakglass.Settings) { s.Steps[0].After = -time.Hour },
		func(s *breakglass.Settings) { s.Steps[0].Audience = nil },
		func(s *breakglass.Settings) { s.Steps[0].Audience = []breakglass.Audience{"everyone"} },
	}

	for i, change := range bad {
		s := breakglass.DefaultSettings()
		change(&s)

		if err := s.Validate(); !errors.Is(err, breakglass.ErrInvalid) {
			t.Fatalf("case %d: err = %v, want ErrInvalid", i, err)
		}
	}
}

func TestCheckVerdict(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 10, 21, 10, 0, 0, 0, time.UTC)
	pending := pendingReview(use(now.Add(-time.Hour)))
	good := breakglass.Verdict{Reviewer: "bob", Note: "", Auth: breakglass.AuthSSO, AuthAt: now, ReviewedAt: now}

	cases := []struct {
		name   string
		r      breakglass.Review
		state  breakglass.State
		change func(v *breakglass.Verdict)
		want   error
	}{
		{"a valid verdict", pending, breakglass.StateAccepted, func(*breakglass.Verdict) {}, nil},
		{"a pending state", pending, breakglass.StatePending, func(*breakglass.Verdict) {}, breakglass.ErrInvalid},
		{
			"no reviewer",
			pending,
			breakglass.StateAccepted,
			func(v *breakglass.Verdict) { v.Reviewer = "" },
			breakglass.ErrInvalid,
		},
		{"a person of the use", pending, breakglass.StateAccepted, func(v *breakglass.Verdict) {
			v.Reviewer = "CAROL"
		}, breakglass.ErrSelfReview},
		{
			"a dispute with no note",
			pending,
			breakglass.StateDisputed,
			func(*breakglass.Verdict) {},
			breakglass.ErrInvalid,
		},
		{"no time", pending, breakglass.StateAccepted, func(v *breakglass.Verdict) {
			v.ReviewedAt = time.Time{}
		}, breakglass.ErrInvalid},
		{"an old authentication", pending, breakglass.StateAccepted, func(v *breakglass.Verdict) {
			v.AuthAt = now.Add(-breakglass.Freshness - time.Second)
		}, breakglass.ErrInvalid},
		{"no kind of authentication", pending, breakglass.StateAccepted, func(v *breakglass.Verdict) {
			v.Auth = ""
		}, breakglass.ErrInvalid},
		{
			"a session in its time limit",
			pendingReview(session(now.Add(-time.Hour))),
			breakglass.StateAccepted,
			func(*breakglass.Verdict) {},
			breakglass.ErrInUse,
		},
		{
			"a review with a verdict",
			func() breakglass.Review { r := pending; r.State = breakglass.StateDisputed; return r }(),
			breakglass.StateAccepted,
			func(*breakglass.Verdict) {},
			breakglass.ErrReviewed,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			v := good
			tc.change(&v)

			err := breakglass.CheckVerdict(tc.r, tc.state, v)
			if (tc.want == nil && err != nil) || (tc.want != nil && !errors.Is(err, tc.want)) {
				t.Fatalf("CheckVerdict: err = %v, want %v", err, tc.want)
			}
		})
	}
}
