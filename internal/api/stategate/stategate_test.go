package stategate_test

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/api/stategate"
	"opendustpan.org/opendustpan/internal/errcode"
	"opendustpan.org/opendustpan/pkg/gate"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/store/schema"
)

// errEngineDown is a guard error that is not a decision.
var errEngineDown = errors.New("engine down")

// errLineBreak is a guard error whose text adds a line.
var errLineBreak = errors.New("down\r\nFAKE: allowed")

// errNewSentinel is a sentinel that package gate does not know.
var errNewSentinel = errors.New("a sentinel of a later version")

// fakeGuard answers each question with its decision and its error, and keeps
// the last question.
type fakeGuard struct {
	mu       sync.Mutex
	decision gate.Decision
	err      error
	got      []gate.StateWrite
}

// GuardStateWrite implements stategate.Guard.
func (g *fakeGuard) GuardStateWrite(_ context.Context, w gate.StateWrite) (gate.Decision, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	g.got = append(g.got, w)

	return g.decision, g.err
}

// asked returns the questions that the guard got.
func (g *fakeGuard) asked() []gate.StateWrite {
	g.mu.Lock()
	defer g.mu.Unlock()

	return append([]gate.StateWrite(nil), g.got...)
}

// recorder keeps the reasons of the audit entries of the refusals.
type recorder struct {
	mu      sync.Mutex
	reasons []string
}

// record implements stategate.Recorder.
func (r *recorder) record(_ context.Context, reason string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.reasons = append(r.reasons, reason)
}

// entries returns the reasons of the recorded refusals.
func (r *recorder) entries() []string {
	r.mu.Lock()
	defer r.mu.Unlock()

	return append([]string(nil), r.reasons...)
}

// decision returns a decision with the result and the record ID.
func decision(result gate.Result, rec gate.RecordID) gate.Decision {
	return gate.Decision{Record: rec, Result: result, Requirements: nil, Warnings: nil, AutoApply: false}
}

// principal returns a principal with the credential and the job ID.
func principal(credential identity.Credential, jobID string) identity.Principal {
	return identity.Principal{
		Subject: "bo@example.com", DisplayName: "", Admin: false, TokenID: "tok-1", TokenExpiresAt: time.Time{},
		TemporaryAdministrator: false, TemporaryExpiresAt: time.Time{}, Kind: identity.KindPerson,
		Credential: credential, JobID: jobID,
	}
}

// write returns a write of the unit opentofu:acme/web.
func write(t *testing.T, op gate.OperationKind, p identity.Principal) stategate.Write {
	t.Helper()

	unit, err := identity.UnitObject("opentofu", "acme", "web")
	if err != nil {
		t.Fatalf("unit: %v", err)
	}

	return stategate.Write{
		Principal: p, Unit: unit, Operation: op, Action: identity.ActionUnitWrite, Source: gate.SourceCLI,
	}
}

// newAdmitter returns the Admitter of guard.
func newAdmitter(t *testing.T, guard stategate.Guard) *stategate.Admitter {
	t.Helper()

	a, err := stategate.New(guard, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	return a
}

// checkRefused fails t unless out refuses the write with code and one audit
// entry.
func checkRefused(t *testing.T, out stategate.Outcome, rec *recorder, code string) {
	t.Helper()

	if out.Allowed || out.Unavailable != nil || out.Refusal == nil {
		t.Fatalf("outcome = %+v, want a refusal", out)
	}

	if out.Code != code {
		t.Fatalf("code = %q, want %q", out.Code, code)
	}

	if !strings.Contains(out.Detail, "(error "+code) {
		t.Fatalf("detail = %q, want the code %q", out.Detail, code)
	}

	if got := rec.entries(); len(got) != 1 || got[0] != "state.write refused with "+code {
		t.Fatalf("audit entries = %q, want one refusal with %q", got, code)
	}
}

// TestNewRefusesANilGuard checks the start-time step: an adapter with no
// guard does not start.
func TestNewRefusesANilGuard(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		guard stategate.Guard
	}{
		{"nil guard", nil},
		{"zero off guard", &stategate.OffGuard{}},
		{"nil off guard", (*stategate.OffGuard)(nil)},
		{"off guard with an empty reason", stategate.Off("")},
		{"off guard with a blank reason", stategate.Off(" \t\n")},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			a, err := stategate.New(tc.guard, nil)
			if err == nil || a != nil {
				t.Fatalf("New(%v) = %v, %v, want an error and no Admitter", tc.guard, a, err)
			}
		})
	}

	if _, err := stategate.New(nil, nil); !errors.Is(err, stategate.ErrNoGuard) {
		t.Fatalf("New(nil) error = %v, want ErrNoGuard", err)
	}
}

// TestAdmitWithNoGuardRefuses sends a write straight to an Admitter that has
// no guard, as if New and the start check had failed. The write is refused
// and recorded.
func TestAdmitWithNoGuardRefuses(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		admit *stategate.Admitter
	}{
		{"nil Admitter", nil},
		{"zero Admitter", &stategate.Admitter{}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			rec := &recorder{mu: sync.Mutex{}, reasons: nil}
			out := tc.admit.Admit(t.Context(), write(t, gate.OperationLock, principal("", "")), rec.record)

			checkRefused(t, out, rec, errcode.GateUnavailable)

			if !strings.HasPrefix(out.Detail, stategate.NoGuardText) {
				t.Fatalf("detail = %q, want it to start with %q", out.Detail, stategate.NoGuardText)
			}
		})
	}
}

// TestAdmitReadsTheResult sends each result straight to the check of the
// result, as if the gate service had given no error for a result that is not
// allow or warn.
func TestAdmitReadsTheResult(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		result  gate.Result
		allowed bool
	}{
		{"allow", gate.ResultAllow, true},
		{"warn", gate.ResultWarn, true},
		{"deny with no error", gate.ResultDeny, false},
		{"error with no error", gate.ResultError, false},
		{"empty", "", false},
		{"unknown", "maybe", false},
		{"unknown with a line break", "allow\r\nwarn", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			guard := &fakeGuard{mu: sync.Mutex{}, decision: decision(tc.result, "rec-1"), err: nil, got: nil}
			rec := &recorder{mu: sync.Mutex{}, reasons: nil}

			out := newAdmitter(t, guard).Admit(t.Context(), write(t, gate.OperationLock, principal("", "")), rec.record)

			if tc.allowed {
				if !out.Allowed || len(rec.entries()) != 0 {
					t.Fatalf("outcome = %+v, entries %q, want allowed with no entry", out, rec.entries())
				}

				return
			}

			checkRefused(t, out, rec, errcode.GateUnavailable)

			if out.Refusal.Record != "rec-1" || !strings.HasPrefix(out.Detail, stategate.UnknownResultText) {
				t.Fatalf("refusal = %+v, detail %q, want the record and the text of an unknown result",
					out.Refusal, out.Detail)
			}
		})
	}
}

// TestAdmitRefusesEachGuardError sends each kind of guard error straight to
// the admission, as if the gate service had failed. Each one refuses with
// its code and one audit entry.
func TestAdmitRefusesEachGuardError(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		err  error
		code string
	}{
		{"plain error", errEngineDown, errcode.GateUnavailable},
		{"wrapped plain error", fmt.Errorf("read gates: %w", errEngineDown), errcode.GateUnavailable},
		{"credential denied", fmt.Errorf("job token: %w", identity.ErrDenied), errcode.GateDenied},
		{
			"gate deny",
			&gate.RefusalError{Err: gate.ErrDenied, Record: "rec-2", Messages: []string{"Frozen."}},
			"gate.denied",
		},
		{"access limit", &gate.RefusalError{Err: gate.ErrLimited, Record: "", Messages: nil}, "gate.limited"},
		{"rule failed", &gate.RefusalError{Err: gate.ErrRuleFailed, Record: "", Messages: nil}, "gate.rule-failed"},
		{"refusal with no sentinel", &gate.RefusalError{Err: nil, Record: "", Messages: nil}, errcode.GateUnavailable},
		{
			"refusal with an unknown sentinel",
			&gate.RefusalError{Err: errNewSentinel, Record: "", Messages: []string{"New."}}, errcode.GateUnavailable,
		},
		{"nil refusal", (*gate.RefusalError)(nil), errcode.GateUnavailable},
		{"guard error with a line break", errLineBreak, errcode.GateUnavailable},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			// A guard that also says allow: the error must win.
			guard := &fakeGuard{mu: sync.Mutex{}, decision: decision(gate.ResultAllow, ""), err: tc.err, got: nil}
			rec := &recorder{mu: sync.Mutex{}, reasons: nil}

			out := newAdmitter(t, guard).Admit(t.Context(), write(t, gate.OperationLock, principal("", "")), rec.record)

			checkRefused(t, out, rec, tc.code)
		})
	}
}

// TestAdmitAnswersAStoreFaultAsUnavailable checks that a store that cannot
// take the decision record is not a refusal that the client must keep, and
// that its refusal is still recorded.
func TestAdmitAnswersAStoreFaultAsUnavailable(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		err  error
	}{
		{"namespace unavailable", fmt.Errorf("fence: %w", namespace.ErrUnavailable)},
		{"binary below minimum", schema.ErrBinaryBelowMinimum},
		{
			"inside a refusal",
			&gate.RefusalError{
				Err: fmt.Errorf("%w: %w", gate.ErrUnavailable, namespace.ErrUnavailable), Record: "", Messages: nil,
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			guard := &fakeGuard{mu: sync.Mutex{}, decision: decision(gate.ResultAllow, ""), err: tc.err, got: nil}
			rec := &recorder{mu: sync.Mutex{}, reasons: nil}

			out := newAdmitter(t, guard).Admit(t.Context(), write(t, gate.OperationLock, principal("", "")), rec.record)

			if out.Allowed || out.Refusal != nil || !errors.Is(out.Unavailable, tc.err) {
				t.Fatalf("outcome = %+v, want Unavailable with %v", out, tc.err)
			}

			if got := rec.entries(); len(got) != 1 {
				t.Fatalf("audit entries = %q, want one", got)
			}
		})
	}
}

// TestAdmitWithNoRecorderRefuses sends a write with no recorder straight to
// the admission, as if the adapter had lost the audit step. The write is
// refused, also when the guard allows it, and the guard is not asked.
func TestAdmitWithNoRecorderRefuses(t *testing.T) {
	t.Parallel()

	guard := &fakeGuard{mu: sync.Mutex{}, decision: decision(gate.ResultAllow, ""), err: nil, got: nil}

	out := newAdmitter(t, guard).Admit(t.Context(), write(t, gate.OperationLock, principal("", "")), nil)

	if out.Allowed || out.Refusal == nil || out.Code != errcode.GateUnavailable {
		t.Fatalf("outcome = %+v, want a refusal with %q", out, errcode.GateUnavailable)
	}

	if !strings.HasPrefix(out.Detail, stategate.NoRecorderText) {
		t.Fatalf("detail = %q, want it to start with %q", out.Detail, stategate.NoRecorderText)
	}

	if got := guard.asked(); len(got) != 0 {
		t.Fatalf("the guard got %d questions, want none", len(got))
	}
}

// TestOffGuard checks the explicit guard of the gates off: it lets a write go
// on only with a reason, and its effective value names the reason.
func TestOffGuard(t *testing.T) {
	t.Parallel()

	off := stategate.Off("the store keeps no gates")

	rec := &recorder{mu: sync.Mutex{}, reasons: nil}

	admit := newAdmitter(t, off)

	out := admit.Admit(t.Context(), write(t, gate.OperationLock, principal("", "")), rec.record)
	if !out.Allowed {
		t.Fatalf("outcome = %+v, want allowed", out)
	}

	if eff := admit.Effective(); eff.Mode != stategate.ModeOff || eff.Reason != "the store keeps no gates" {
		t.Fatalf("Effective() = %+v, want off with the reason of Off", eff)
	}

	on := stategate.Effective{
		Mode: stategate.ModeOn, Reason: stategate.ReasonOn,
		Checked: stategate.CheckedPaths(), Unchecked: stategate.UncheckedPaths(),
	}
	offValue := stategate.Effective{
		Mode: stategate.ModeOff, Reason: "the store keeps no gates", Checked: []stategate.WritePath{},
		Unchecked: append(stategate.CheckedPaths(), stategate.UncheckedPaths()...),
	}

	cases := []struct {
		name  string
		guard stategate.Guard
		want  stategate.Effective
	}{
		{"off", off, offValue},
		{"zero off guard", &stategate.OffGuard{}, on},
		{"nil off guard", (*stategate.OffGuard)(nil), on},
		{"off guard with a blank reason", stategate.Off(" \t"), on},
		{"nil", nil, on},
		{"a guard", &fakeGuard{mu: sync.Mutex{}, decision: decision(gate.ResultAllow, ""), err: nil, got: nil}, on},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := stategate.EffectiveOf(tc.guard)
			if got.Mode != tc.want.Mode || got.Reason != tc.want.Reason ||
				!slices.Equal(got.Checked, tc.want.Checked) || !slices.Equal(got.Unchecked, tc.want.Unchecked) {
				t.Fatalf("EffectiveOf = %+v, want %+v", got, tc.want)
			}
		})
	}
}

// TestWritePathsAreDistinct checks that no write path is both checked and
// unchecked, and that no name is listed twice.
func TestWritePathsAreDistinct(t *testing.T) {
	t.Parallel()

	seen := map[stategate.WritePath]bool{}

	for _, p := range append(stategate.CheckedPaths(), stategate.UncheckedPaths()...) {
		if seen[p] {
			t.Fatalf("the write path %q is listed twice", p)
		}

		seen[p] = true
	}
}

// TestOffGuardWithNoReasonRefuses sends a write straight to an OffGuard with
// no reason, as if New had been skipped. The guard must not let the write
// through, and its effective value is not off.
func TestOffGuardWithNoReasonRefuses(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		guard *stategate.OffGuard
	}{
		{"zero", &stategate.OffGuard{}},
		{"nil", nil},
		{"empty", stategate.Off("")},
		{"blank", stategate.Off(" \r\n\t")},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, err := tc.guard.GuardStateWrite(t.Context(), stategate.StateWrite(write(t, gate.OperationLock,
				principal("", ""))))
			if err == nil {
				t.Fatal("an OffGuard with no reason allowed a write, want an error")
			}

			if eff := stategate.EffectiveOf(tc.guard); eff.Mode != stategate.ModeOn {
				t.Fatalf("EffectiveOf = %+v, want on", eff)
			}
		})
	}
}

// TestStateWriteComputesPlanLock checks that the admission sets PlanLock in
// one way for every adapter: only for the lock of a job token.
func TestStateWriteComputesPlanLock(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		op   gate.OperationKind
		p    identity.Principal
		want bool
	}{
		{"lock with a job token", gate.OperationLock, principal(identity.CredentialJobToken, "job-1"), true},
		{"lock with an access token", gate.OperationLock, principal(identity.CredentialAccessToken, ""), false},
		{
			"lock with a job ID and no job token",
			gate.OperationLock,
			principal(identity.CredentialSession, "job-1"),
			false,
		},
		{"rollback with a job token", gate.OperationRollback, principal(identity.CredentialJobToken, "job-1"), false},
		{"unlock with a job token", gate.OperationUnlock, principal(identity.CredentialJobToken, "job-1"), false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			guard := &fakeGuard{mu: sync.Mutex{}, decision: decision(gate.ResultAllow, ""), err: nil, got: nil}
			rec := &recorder{mu: sync.Mutex{}, reasons: nil}
			w := write(t, tc.op, tc.p)

			newAdmitter(t, guard).Admit(t.Context(), w, rec.record)

			got := guard.asked()
			if len(got) != 1 {
				t.Fatalf("the guard got %d questions, want 1", len(got))
			}

			if got[0].PlanLock != tc.want {
				t.Fatalf("PlanLock = %v, want %v", got[0].PlanLock, tc.want)
			}

			if got[0].Unit != w.Unit || got[0].Operation != tc.op || got[0].Source != w.Source {
				t.Fatalf("question = %+v, want the unit, the operation and the source of the write", got[0])
			}
		})
	}
}

// TestDetailIsOneLine sends hostile messages and a hostile record ID straight
// to Detail. The text must stay on one line.
func TestDetailIsOneLine(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		refused *gate.RefusalError
	}{
		{"message with CRLF", &gate.RefusalError{Err: gate.ErrDenied, Record: "", Messages: []string{"a\r\nFAKE: b"}}},
		{"record with LF", &gate.RefusalError{Err: gate.ErrDenied, Record: "rec\nFAKE", Messages: nil}},
		{"several messages", &gate.RefusalError{Err: gate.ErrDenied, Record: "", Messages: []string{"a\n", "\rb"}}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := stategate.Detail(tc.refused, "gate.denied\n")
			if strings.ContainsAny(got, "\r\n") {
				t.Fatalf("Detail = %q, want one line", got)
			}
		})
	}
}
