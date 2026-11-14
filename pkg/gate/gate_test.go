package gate_test

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/gate"
)

// errOther is an error of another package.
var errOther = errors.New("other")

func TestParseDecisionPoint(t *testing.T) {
	t.Parallel()

	points := gate.DecisionPoints()
	if len(points) != 6 {
		t.Fatalf("DecisionPoints() has %d points, want 6", len(points))
	}

	for _, p := range points {
		got, err := gate.ParseDecisionPoint(string(p))
		if err != nil || got != p {
			t.Fatalf("ParseDecisionPoint(%q) = %q, %v", p, got, err)
		}
	}

	for _, in := range []string{"", "job.start", "self-service.request", "changeorder.review", "State.write", "access"} {
		if _, err := gate.ParseDecisionPoint(in); !errors.Is(err, gate.ErrInvalid) {
			t.Fatalf("ParseDecisionPoint(%q): err = %v, want ErrInvalid", in, err)
		}
	}
}

func TestParseModeAndOnError(t *testing.T) {
	t.Parallel()

	for _, in := range []string{"enforce", "evaluate"} {
		if m, err := gate.ParseMode(in); err != nil || string(m) != in {
			t.Fatalf("ParseMode(%q) = %q, %v", in, m, err)
		}
	}

	for _, in := range []string{"fail", "skip"} {
		if o, err := gate.ParseOnError(in); err != nil || string(o) != in {
			t.Fatalf("ParseOnError(%q) = %q, %v", in, o, err)
		}
	}

	for _, in := range []string{"", "audit", "Enforce", "dry-run"} {
		if _, err := gate.ParseMode(in); !errors.Is(err, gate.ErrInvalid) {
			t.Fatalf("ParseMode(%q): err = %v, want ErrInvalid", in, err)
		}

		if _, err := gate.ParseOnError(in); !errors.Is(err, gate.ErrInvalid) {
			t.Fatalf("ParseOnError(%q): err = %v, want ErrInvalid", in, err)
		}
	}
}

func TestParseOutcome(t *testing.T) {
	t.Parallel()

	cases := []struct {
		in   string
		want bool
		rule gate.ID
	}{
		{"deny", true, ""},
		{"warn", true, ""},
		{"approval:security", true, "security"},
		{"approval:", false, ""},
		{"approval:owners", false, ""},
		{"approval:default", false, ""},
		{"approval:Sec", false, ""},
		{"allow", false, ""},
		{"Deny", false, ""},
		{"", false, ""},
	}

	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			t.Parallel()

			if !tc.want {
				if _, err := gate.ParseOutcome(tc.in); !errors.Is(err, gate.ErrInvalid) {
					t.Fatalf("ParseOutcome(%q): err = %v, want ErrInvalid", tc.in, err)
				}

				return
			}

			checkOutcome(t, tc.in, tc.rule)
		})
	}

	if got := gate.ApprovalOutcome("dba"); got != "approval:dba" {
		t.Fatalf("ApprovalOutcome = %q", got)
	}
}

// checkOutcome checks ParseOutcome and Rule for a valid outcome in.
func checkOutcome(t *testing.T, in string, wantRule gate.ID) {
	t.Helper()

	o, err := gate.ParseOutcome(in)
	if err != nil || string(o) != in {
		t.Fatalf("ParseOutcome(%q) = %q, %v", in, o, err)
	}

	rule, ok := o.Rule()
	if rule != wantRule || ok != (wantRule != "") {
		t.Fatalf("Rule() = %q, %v, want %q", rule, ok, wantRule)
	}
}

func TestCode(t *testing.T) {
	t.Parallel()

	cases := []struct {
		err  error
		code string
	}{
		{gate.ErrDenied, "gate.denied"},
		{gate.ErrLimited, "gate.limited"},
		{gate.ErrNotApprover, "gate.not-approver"},
		{gate.ErrAuthorApproval, "gate.author-approval"},
		{gate.ErrRuleFailed, "gate.rule-failed"},
		{gate.ErrFinal, "gate.final"},
		{gate.ErrInvalid, "gate.invalid"},
		{gate.ErrLimit, "gate.limit"},
		{gate.ErrLimitGroupReach, "gate.limit-group-reach"},
		{gate.ErrLimitMembership, "gate.limit-membership"},
		{gate.ErrUnavailable, "gate.unavailable"},
		{fmt.Errorf("save: %w", gate.ErrFinal), "gate.final"},
		{&gate.RefusalError{Err: gate.ErrDenied, Record: "gdr-x", Messages: nil}, "gate.denied"},
		{&gate.CheckError{Diagnostics: nil}, "gate.invalid"},
	}

	for _, tc := range cases {
		t.Run(tc.code, func(t *testing.T) {
			t.Parallel()

			if got, ok := gate.Code(tc.err); !ok || got != tc.code {
				t.Fatalf("Code(%v) = %q, %v, want %q", tc.err, got, ok, tc.code)
			}
		})
	}

	for _, err := range []error{nil, gate.ErrNotFound, gate.ErrConflict, errOther} {
		if got, ok := gate.Code(err); ok {
			t.Fatalf("Code(%v) = %q, want no code", err, got)
		}
	}
}

func TestRefusal(t *testing.T) {
	t.Parallel()

	var err error = &gate.RefusalError{
		Err:      gate.ErrDenied,
		Record:   "gdr-abcdefghij0123456789",
		Messages: []string{"no destroy in prod"},
	}

	if !errors.Is(err, gate.ErrDenied) {
		t.Fatal("a refusal wraps its sentinel")
	}

	var r *gate.RefusalError
	if !errors.As(fmt.Errorf("lock: %w", err), &r) || r.Record != "gdr-abcdefghij0123456789" {
		t.Fatal("errors.As finds the refusal through a wrap")
	}

	msg := err.Error()
	for _, part := range []string{"a gate denies the change", "no destroy in prod", "gdr-abcdefghij0123456789"} {
		if !strings.Contains(msg, part) {
			t.Fatalf("Error() = %q, want it to hold %q", msg, part)
		}
	}
}

func TestCheckError(t *testing.T) {
	t.Parallel()

	p := gate.PolicyID{Scope: gate.NamespaceScope("acme"), ID: "cost"}
	err := &gate.CheckError{Diagnostics: []gate.Diagnostic{
		{Policy: p, Message: "unknown attribute risk"},
		{Policy: p, Message: "type error"},
	}}

	if !errors.Is(err, gate.ErrInvalid) {
		t.Fatal("a check error wraps ErrInvalid")
	}

	if got := err.Error(); !strings.Contains(got, "namespace/acme/cost: unknown attribute risk") ||
		!strings.Contains(got, "1 more") {
		t.Fatalf("Error() = %q", got)
	}
}
