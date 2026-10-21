package durable_test

import (
	"errors"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/durable"
)

func TestStartRequestValidate(t *testing.T) {
	t.Parallel()

	cases := []struct {
		req  durable.StartRequest
		want error
	}{
		{durable.StartRequest{Key: "k", Kind: "backup", Version: 1, Input: nil}, nil},
		{durable.StartRequest{Key: "", Kind: "backup", Version: 0, Input: nil}, durable.ErrInvalid},
		{durable.StartRequest{Key: "k", Kind: "", Version: 0, Input: []byte("x")}, durable.ErrInvalid},
		{durable.StartRequest{Key: "k", Kind: "backup", Version: -1, Input: nil}, durable.ErrInvalid},
	}

	for _, tc := range cases {
		if err := tc.req.Validate(); !errors.Is(err, tc.want) {
			t.Errorf("Validate(%+v) = %v, want %v", tc.req, err, tc.want)
		}
	}
}

func TestClaimRequestValidate(t *testing.T) {
	t.Parallel()

	kinds := []durable.Kind{"backup"}

	cases := []struct {
		req  durable.ClaimRequest
		want error
	}{
		{
			durable.ClaimRequest{Owner: "a", Kinds: kinds, MaxVersions: nil, Lease: time.Second, TakeCanceled: false},
			nil,
		},
		{
			durable.ClaimRequest{Owner: "", Kinds: kinds, MaxVersions: nil, Lease: time.Second, TakeCanceled: false},
			durable.ErrInvalid,
		},
		{
			durable.ClaimRequest{Owner: "a", Kinds: nil, MaxVersions: nil, Lease: time.Second, TakeCanceled: false},
			durable.ErrInvalid,
		},
		{
			durable.ClaimRequest{
				Owner:        "a",
				Kinds:        []durable.Kind{"backup", ""},
				MaxVersions:  nil,
				Lease:        time.Second,
				TakeCanceled: false,
			},
			durable.ErrInvalid,
		},
		{
			durable.ClaimRequest{Owner: "a", Kinds: kinds, MaxVersions: nil, Lease: 0, TakeCanceled: false},
			durable.ErrInvalid,
		},
		{
			durable.ClaimRequest{Owner: "a", Kinds: kinds, MaxVersions: nil, Lease: -time.Second, TakeCanceled: false},
			durable.ErrInvalid,
		},
		{
			durable.ClaimRequest{
				Owner:        "a",
				Kinds:        kinds,
				MaxVersions:  map[durable.Kind]int{"backup": -1},
				Lease:        time.Second,
				TakeCanceled: false,
			},
			durable.ErrInvalid,
		},
		{
			durable.ClaimRequest{
				Owner:        "a",
				Kinds:        kinds,
				MaxVersions:  map[durable.Kind]int{"backup": 0},
				Lease:        time.Second,
				TakeCanceled: false,
			},
			nil,
		},
	}

	for _, tc := range cases {
		if err := tc.req.Validate(); !errors.Is(err, tc.want) {
			t.Errorf("Validate(%+v) = %v, want %v", tc.req, err, tc.want)
		}
	}
}

func TestStateTerminal(t *testing.T) {
	t.Parallel()

	for s, want := range map[durable.State]bool{
		durable.StatePending:   false,
		durable.StateRunning:   false,
		durable.StateSucceeded: true,
		durable.StateFailed:    true,
		durable.StateCanceled:  true,
	} {
		if got := s.Terminal(); got != want {
			t.Errorf("%s.Terminal() = %v, want %v", s, got, want)
		}
	}
}

func TestClaimStepAndOperationStep(t *testing.T) {
	t.Parallel()

	c := durable.Claim{Operation: "op-1", Owner: "a", Token: 7, ExpiresAt: time.Time{}}

	f := c.Step("export")
	if f != (durable.StepFence{Operation: "op-1", Token: 7, Step: "export"}) {
		t.Fatalf("Claim.Step = %+v, want the operation, the token and the step name", f)
	}

	op := durable.Operation{
		ID: "op-1", Key: "k", Kind: "backup", Version: 1, Input: nil, State: durable.StateRunning,
		Steps:   []durable.StepRecord{{Name: "export", Result: []byte("r"), DoneAt: time.Time{}}},
		Attempt: 1, Expiries: 0, Failures: 0, LastError: "", Claim: c, CancelRequested: false,
		NotBefore: time.Time{}, CreatedAt: time.Time{}, UpdatedAt: time.Time{},
	}

	if rec, ok := op.Step("export"); !ok || string(rec.Result) != "r" {
		t.Fatalf("Operation.Step(export) = %+v, %v; want the record", rec, ok)
	}

	if _, ok := op.Step("upload"); ok {
		t.Fatal("Operation.Step(upload) found a step that is not recorded")
	}
}

func TestClaimRequestAllows(t *testing.T) {
	t.Parallel()

	req := durable.ClaimRequest{
		Owner: "a", Kinds: []durable.Kind{"backup", "restore"},
		MaxVersions: map[durable.Kind]int{"backup": 2}, Lease: time.Second,
		TakeCanceled: false,
	}

	cases := []struct {
		kind    durable.Kind
		version int
		want    bool
	}{
		{"backup", 2, true},
		{"backup", 3, false},
		{"restore", 99, true},
		{"sweep", 0, false},
	}

	for _, tc := range cases {
		if got := req.Allows(tc.kind, tc.version); got != tc.want {
			t.Errorf("Allows(%s, %d) = %v, want %v", tc.kind, tc.version, got, tc.want)
		}
	}
}
