package quota_test

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/quota"
	"opendustpan.org/opendustpan/pkg/namespace"
)

const ns = namespace.Name("payments")

func units(scope quota.Scope, maxUnits int) quota.Rule {
	return quota.Rule{Kind: quota.KindUnits, Scope: scope, Expr: "", Message: "", Window: 0, Max: maxUnits}
}

func requests(scope quota.Scope, maxRequests int, window time.Duration) quota.Rule {
	return quota.Rule{Kind: quota.KindRequests, Scope: scope, Expr: "", Message: "", Window: window, Max: maxRequests}
}

func custom(rule, message string) quota.Rule {
	return quota.Rule{Kind: quota.KindExpr, Scope: "", Expr: rule, Message: message, Window: 0, Max: 0}
}

func TestRuleValidate(t *testing.T) {
	t.Parallel()

	withScope := custom("usage.units < 10", "Ten at most.")
	withScope.Scope = quota.ScopeNamespace

	unitsWithWindow := units(quota.ScopeNamespace, 1)
	unitsWithWindow.Window = time.Hour

	cases := []struct {
		name string
		rule quota.Rule
		ok   bool
	}{
		{"units in the namespace", units(quota.ScopeNamespace, 50), true},
		{"units of each requester", units(quota.ScopeRequester, 3), true},
		{"units of a group", units("group:dba", 10), true},
		{"zero units refuses every create", units(quota.ScopeNamespace, 0), true},
		{"requests in a day", requests(quota.ScopeRequester, 10, 24*time.Hour), true},
		{"requests in the shortest window", requests(quota.ScopeNamespace, 1, quota.MinWindow), true},
		{"requests in the longest window", requests(quota.ScopeNamespace, 1, quota.MaxWindow), true},
		{"a custom check", custom("usage.units < 10", "Ten at most."), true},
		{"unknown kind", quota.Rule{Kind: "cost", Scope: "", Expr: "", Message: "", Window: 0, Max: 1}, false},
		{"unknown scope", units("team", 1), false},
		{"empty group", units("group:", 1), false},
		{"max below 0", units(quota.ScopeNamespace, -1), false},
		{"units with a window", unitsWithWindow, false},
		{"requests with no window", requests(quota.ScopeNamespace, 1, 0), false},
		{"requests over the longest window", requests(quota.ScopeNamespace, 1, quota.MaxWindow+time.Second), false},
		{"a custom check with no message", custom("true", ""), false},
		{"a custom check with no expression", custom("", "Message."), false},
		{"a custom check over the limit", custom(strings.Repeat("1", quota.MaxExprBytes+1), "M."), false},
		{"a custom check with a scope", withScope, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := tc.rule.Validate()
			if tc.ok != (err == nil) || (err != nil && !errors.Is(err, quota.ErrInvalid)) {
				t.Fatalf("Validate() = %v, want ok %v", err, tc.ok)
			}
		})
	}
}

func TestKeyFor(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		rule   quota.Rule
		groups []string
		want   string
		ok     bool
	}{
		{"namespace", units(quota.ScopeNamespace, 1), nil, "payments/postgres/units/namespace", true},
		{"requester", units(quota.ScopeRequester, 1), nil, "payments/postgres/units/requester:alice", true},
		{
			"requests of a requester",
			requests(quota.ScopeRequester, 1, time.Hour), nil, "payments/postgres/requests/requester:alice", true,
		},
		{
			"a group of the requester",
			units("group:dba", 1),
			[]string{"dev", "dba"},
			"payments/postgres/units/group:dba",
			true,
		},
		{"a group that the requester is not in", units("group:dba", 1), []string{"dev"}, "", false},
		{"a custom check counts no key", custom("true", "M."), nil, "", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			k, ok := quota.KeyFor(ns, "postgres", tc.rule, "alice", tc.groups)
			if ok != tc.ok || (ok && k.String() != tc.want) {
				t.Fatalf("KeyFor() = %q, %v, want %q, %v", k.String(), ok, tc.want, tc.ok)
			}
		})
	}
}

func TestEvaluate(t *testing.T) {
	t.Parallel()

	a := quota.Key{Namespace: ns, Blueprint: "postgres", Kind: quota.KindUnits, Scope: "namespace"}
	b := quota.Key{Namespace: ns, Blueprint: "postgres", Kind: quota.KindUnits, Scope: "requester:alice"}
	checks := []quota.Check{{Key: a, Window: 0, Max: 10}, {Key: b, Window: 0, Max: 2}}

	cases := []struct {
		name   string
		usages []quota.Usage
		adding int
		want   []quota.Exceeded
	}{
		{"no usage passes", nil, 1, nil},
		{
			"settled plus reserved below the limits passes",
			[]quota.Usage{
				{Key: a, Settled: 7, Reserved: 2, Requests: 0},
				{Key: b, Settled: 1, Reserved: 0, Requests: 0},
			},
			1, nil,
		},
		{
			"the last unit of one key fails that key only",
			[]quota.Usage{
				{Key: a, Settled: 7, Reserved: 2, Requests: 0},
				{Key: b, Settled: 1, Reserved: 1, Requests: 0},
			},
			1,
			[]quota.Exceeded{{Key: b, Used: 2, Max: 2}},
		},
		{
			"a recheck adds nothing: its own reservation counts once",
			[]quota.Usage{
				{Key: a, Settled: 9, Reserved: 1, Requests: 0},
				{Key: b, Settled: 1, Reserved: 1, Requests: 0},
			},
			0, nil,
		},
		{
			"a lower limit than the usage fails",
			[]quota.Usage{{Key: a, Settled: 12, Reserved: 0, Requests: 0}},
			0,
			[]quota.Exceeded{{Key: a, Used: 12, Max: 10}},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := quota.Evaluate(checks, tc.usages, tc.adding); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("Evaluate() = %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestExceededError(t *testing.T) {
	t.Parallel()

	k := quota.Key{Namespace: ns, Blueprint: "postgres", Kind: quota.KindUnits, Scope: "namespace"}

	var err error = &quota.ExceededError{Exceeded: []quota.Exceeded{{Key: k, Used: 50, Max: 50}}}
	if !errors.Is(err, quota.ErrExceeded) {
		t.Fatalf("errors.Is(%v, ErrExceeded) = false", err)
	}

	if want := "quota exceeded: payments/postgres/units/namespace uses 50 of 50"; err.Error() != want {
		t.Fatalf("Error() = %q, want %q", err.Error(), want)
	}

	var ee *quota.ExceededError
	if !errors.As(err, &ee) || len(ee.Exceeded) != 1 {
		t.Fatalf("errors.As() = %v", ee)
	}
}

func TestReservationValidate(t *testing.T) {
	t.Parallel()

	k := quota.Key{Namespace: ns, Blueprint: "postgres", Kind: quota.KindUnits, Scope: "namespace"}
	other := quota.Key{Namespace: "other", Blueprint: "postgres", Kind: quota.KindUnits, Scope: "namespace"}
	req := quota.Key{Namespace: ns, Blueprint: "postgres", Kind: quota.KindRequests, Scope: "namespace"}
	end := time.Unix(1_790_000_000, 0)

	r := func(id quota.ReservationID, state quota.ReservationState, endsAt time.Time, keys ...quota.Key) quota.Reservation {
		return quota.Reservation{
			ID:        id,
			Namespace: ns,
			State:     state,
			Keys:      keys,
			At:        end.Add(-time.Hour),
			EndsAt:    endsAt,
		}
	}

	cases := []struct {
		name string
		r    quota.Reservation
		ok   bool
	}{
		{"one key", r("req-1/1", quota.ReservationOpen, end, k), true},
		{"no ID", r("", quota.ReservationOpen, end, k), false},
		{"not open", r("req-1/1", quota.ReservationSettled, end, k), false},
		{"no end time", r("req-1/1", quota.ReservationOpen, time.Time{}, k), false},
		{"an end time before the time", r("req-1/1", quota.ReservationOpen, end.Add(-2*time.Hour), k), false},
		{"no key", r("req-1/1", quota.ReservationOpen, end), false},
		{"a key of another namespace", r("req-1/1", quota.ReservationOpen, end, other), false},
		{"a key of requests", r("req-1/1", quota.ReservationOpen, end, req), false},
		{"a key twice", r("req-1/1", quota.ReservationOpen, end, k, k), false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := tc.r.Validate()
			if tc.ok != (err == nil) || (err != nil && !errors.Is(err, quota.ErrInvalid)) {
				t.Fatalf("Validate() = %v, want ok %v", err, tc.ok)
			}
		})
	}
}
