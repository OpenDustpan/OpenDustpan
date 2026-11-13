package driftsvc_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/driftsvc"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/drift"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/job"
)

// recordingStarter is a JobStarter that keeps the requests that it gets.
type recordingStarter struct {
	requests []job.StartRequest
	actors   []job.Actor
	err      error
}

func newStarter(err error) *recordingStarter {
	return &recordingStarter{requests: nil, actors: nil, err: err}
}

func (r *recordingStarter) Start(_ context.Context, a job.Actor, req job.StartRequest) (job.Job, error) {
	r.requests = append(r.requests, req)
	r.actors = append(r.actors, a)

	if r.err != nil {
		return job.Job{}, r.err
	}

	return startedJob(), nil
}

// startedJob returns the job that the starter makes. Only its ID matters.
func startedJob() job.Job {
	return job.Job{
		ID: "job-started", NamespaceID: "", Spec: emptySpec(), State: job.StateQueued, Wait: job.WaitNone,
		Reason: "", Phases: nil, Current: 0, StartedBy: "", Source: job.SourceAPI, Agent: "", AgentMode: "",
		BreakGlass: nil, Delivery: nil, Closed: nil, Verification: job.VerificationNone, IdempotencyKey: "",
		RequestHash: "", Version: 1, Trigger: nil, CreatedAt: time.Time{}, UpdatedAt: time.Time{},
		StateChangedAt: time.Time{},
	}
}

func emptySpec() job.Spec {
	return job.Spec{
		Unit:           identity.Object{},
		Operation:      job.OperationRefresh,
		Purpose:        job.PurposeDriftCheck,
		Executor:       job.ExecutorRunner,
		Location:       noLocation(),
		LocationSource: "",
		Steps:          nil,
		Pool:           "",
		Profile:        "",
		Edits:          nil,
		Delivery:       "",
		Defaulted:      nil,
	}
}

func noLocation() job.Location {
	return job.Location{Connection: "", Repository: "", Ref: "", Directory: "", Revision: ""}
}

func actorAda() job.Actor {
	return job.Actor{
		Principal: identity.Principal{
			Subject: "ada", DisplayName: "", Admin: false, TokenID: "", TokenExpiresAt: time.Time{},
			TemporaryAdministrator: false, TemporaryExpiresAt: time.Time{}, Kind: "", Credential: "", JobID: "",
		},
		Agent: "", Mode: "", ConfirmAuth: "", AuthAt: time.Time{},
	}
}

// requestFor returns a request for the unit name, with the defaults of the job
// service.
func requestFor(t *testing.T, name string) drift.CheckRequest {
	t.Helper()

	ref, err := identity.UnitObject("opentofu", "acme", name)
	if err != nil {
		t.Fatalf("unit object: %v", err)
	}

	return drift.CheckRequest{
		Unit: ref, Location: noLocation(), Pool: "", Reason: "", Channel: "", SourceIP: "", UserAgent: "",
		RequestID: "",
	}
}

func newChecker(t *testing.T, starter driftsvc.JobStarter, now time.Time) *driftsvc.Service {
	t.Helper()

	svc, err := driftsvc.NewService(starter, func() time.Time { return now })
	if err != nil {
		t.Fatalf("NewService = %v", err)
	}

	return svc
}

func TestNewServiceRefusesNoStarter(t *testing.T) {
	t.Parallel()

	if _, err := driftsvc.NewService(nil, nil); !errors.Is(err, drift.ErrInvalid) {
		t.Fatalf("NewService(nil) = %v, want ErrInvalid", err)
	}
}

func TestCheckStartsADriftCheckJob(t *testing.T) {
	t.Parallel()

	starter := newStarter(nil)
	svc := newChecker(t, starter, time.UnixMilli(1_800_000_000_000).UTC())

	req := requestFor(t, "network")
	req.Pool, req.Reason, req.Channel = "pool-1", "after the incident", audit.ChannelAPI
	req.SourceIP, req.UserAgent, req.RequestID = "192.0.2.1", "test", "req-1"
	req.Location = job.Location{Connection: "", Repository: "acme/infra", Ref: "main", Directory: "", Revision: ""}

	id, err := svc.Check(t.Context(), actorAda(), req)
	if err != nil || id != "job-started" {
		t.Fatalf("Check = %q, %v; want job-started", id, err)
	}

	if len(starter.requests) != 1 {
		t.Fatalf("starts = %d, want 1", len(starter.requests))
	}

	checkFixedFields(t, &starter.requests[0])

	got := &starter.requests[0]
	if got.Pool != "pool-1" || got.Location.Repository != "acme/infra" || got.Reason != "after the incident" {
		t.Errorf("request = %+v, want the pool, the location and the reason of the caller", got)
	}

	if got.SourceIP != "192.0.2.1" || got.UserAgent != "test" || got.RequestID != "req-1" ||
		got.Channel != audit.ChannelAPI {
		t.Errorf("request = %+v, want the audit fields of the caller", got)
	}

	if starter.actors[0].Principal.Subject != "ada" {
		t.Errorf("actor = %+v, want ada", starter.actors[0])
	}
}

// checkFixedFields checks the fields that the service fixes: a caller never
// sets them.
func checkFixedFields(t *testing.T, got *job.StartRequest) {
	t.Helper()

	if got.Operation != job.OperationRefresh || got.Purpose != job.PurposeDriftCheck {
		t.Errorf("operation = %q, purpose = %q; want refresh and drift-check", got.Operation, got.Purpose)
	}

	if got.Source != job.SourceAPI || got.Executor != "" || got.BreakGlass != nil {
		t.Errorf("source = %q, executor = %q, break-glass = %v; want api, the default and none", got.Source,
			got.Executor, got.BreakGlass)
	}

	if len(got.Edits) != 0 || got.Delivery != "" {
		t.Error("a drift check has edits or a delivery")
	}
}

func TestCheckKey(t *testing.T) {
	t.Parallel()

	base := time.UnixMilli(1_800_000_000_000).UTC().Truncate(time.Minute)

	keyOf := func(name string, at time.Time) string {
		t.Helper()

		starter := newStarter(nil)

		if _, err := newChecker(t, starter, at).Check(t.Context(), actorAda(), requestFor(t, name)); err != nil {
			t.Fatalf("Check = %v", err)
		}

		return starter.requests[0].IdempotencyKey
	}

	first := keyOf("network", base)

	tests := []struct {
		name string
		got  string
		same bool
	}{
		{"same unit, same minute", keyOf("network", base.Add(59*time.Second)), true},
		{"same unit, next minute", keyOf("network", base.Add(time.Minute)), false},
		{"another unit, same minute", keyOf("storage", base), false},
	}

	for _, tt := range tests {
		if (tt.got == first) != tt.same {
			t.Errorf("%s: key %q, first %q, want same = %v", tt.name, tt.got, first, tt.same)
		}
	}

	printable := func(r rune) bool { return r < ' ' || r > '~' }
	if len(first) > job.MaxIdempotencyKey || strings.ContainsFunc(first, printable) {
		t.Errorf("key %q is not short printable ASCII", first)
	}
}

// TestCheckKeyReachesTheJobService proves: the key that the server makes for a
// drift check starts with the prefix that the routes reserve for the server,
// and the path from the check to the job service accepts it. Only a caller key
// is refused.
func TestCheckKeyReachesTheJobService(t *testing.T) {
	t.Parallel()

	starter := newStarter(nil)
	svc := newChecker(t, starter, time.UnixMilli(1_800_000_000_000).UTC())

	if _, err := svc.Check(t.Context(), actorAda(), requestFor(t, "network")); err != nil {
		t.Fatalf("Check = %v", err)
	}

	if len(starter.requests) != 1 {
		t.Fatalf("starts = %d, want 1", len(starter.requests))
	}

	key := starter.requests[0].IdempotencyKey
	if !strings.HasPrefix(key, driftsvc.CheckKeyPrefix) || !driftsvc.ReservedKey(key) {
		t.Errorf("key = %q, want one with the reserved prefix %q", key, driftsvc.CheckKeyPrefix)
	}
}

func TestCheckRefusesWhatIsNoUnit(t *testing.T) {
	t.Parallel()

	starter := newStarter(nil)

	req := requestFor(t, "network")
	req.Unit = identity.NamespaceObject("acme")

	_, err := newChecker(t, starter, time.Now()).Check(t.Context(), actorAda(), req)
	if !errors.Is(err, drift.ErrInvalid) || len(starter.requests) != 0 {
		t.Fatalf("Check of a namespace = %v with %d starts, want ErrInvalid and none", err, len(starter.requests))
	}
}

// TestCheckRefusesAHiddenRuneInTheReason sends a reason that the route
// refuses straight to the service, as if the route check had failed. The
// service refuses it too and starts no job.
func TestCheckRefusesAHiddenRuneInTheReason(t *testing.T) {
	t.Parallel()

	tests := []struct{ name, reason string }{
		{"line separator", "a\u2028b"},
		{"paragraph separator", "a\u2029b"},
		{"format character", "a\u200bb"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			starter := newStarter(nil)

			req := requestFor(t, "network")
			req.Reason = tt.reason

			_, err := newChecker(t, starter, time.Now()).Check(t.Context(), actorAda(), req)
			if !errors.Is(err, drift.ErrInvalid) || len(starter.requests) != 0 {
				t.Fatalf("Check with reason %q = %v with %d starts, want ErrInvalid and none",
					tt.reason, err, len(starter.requests))
			}
		})
	}
}

func TestCheckPassesTheErrorsOfTheJobService(t *testing.T) {
	t.Parallel()

	for _, want := range []error{job.ErrLimit, job.ErrUnsupported, job.ErrPolicyDenied, identity.ErrDenied} {
		id, err := newChecker(t, newStarter(want), time.Now()).Check(t.Context(), actorAda(),
			requestFor(t, "network"))
		if !errors.Is(err, want) || id != "" {
			t.Errorf("Check = %q, %v; want no ID and %v", id, err, want)
		}
	}
}
