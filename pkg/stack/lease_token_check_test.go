package stack_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/backup/blobguard/blobguardtest"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/secret/secrettest"
	"opendustpan.org/opendustpan/pkg/stack"
	"opendustpan.org/opendustpan/pkg/stack/stacktest"
)

// switchChecker is a TokenChecker that a test turns from passing to refusing,
// as the check of the maker of a service token does when the maker loses the
// act-as path during an update. It keeps the ID of the last token it checked.
type switchChecker struct {
	refuse atomic.Bool
	mu     sync.Mutex
	id     string
}

func (c *switchChecker) CheckActive(_ context.Context, id string) error {
	c.mu.Lock()
	c.id = id
	c.mu.Unlock()

	if c.refuse.Load() {
		return fmt.Errorf("%w: the maker of the access token has no act-as path to its principal",
			identity.ErrUnauthenticated)
	}

	return nil
}

func (c *switchChecker) last() string {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.id
}

// withTokens returns an Updates service over the stores of f with checker as
// its token checker.
func (f updatesFixture) withTokens(t *testing.T, checker stack.TokenChecker) *stack.Updates {
	t.Helper()

	svc, err := stack.NewUpdates(stack.UpdatesConfig{
		Membership:  stacktest.PlacedAll(),
		Sealer:      secrettest.NewMemorySealer(),
		BlobGuard:   blobguardtest.None{},
		ManualLocks: nil, Updates: f.store, Tokens: checker,
		Checkpoints: f.store,
		Events:      f.store, Namespaces: f.namespaces, Stacks: f.store, Blobs: f.blobs,
		Now: f.clock.Now, Logger: nil, MaxCheckpointBytes: 1024,
		Journal: nil, Replayer: nil, Refusals: f.refusals, OnDeleteWait: nil,
	})
	if err != nil {
		t.Fatalf("new updates: %v", err)
	}

	return svc
}

// leaseRequest is one lease request of an update, run against svc.
type leaseRequest struct {
	name string
	do   func(svc *stack.Updates, f updatesFixture, s stack.Started) error
}

func leaseRequests() []leaseRequest {
	ctx := context.Background()

	return []leaseRequest{
		{"renew", func(svc *stack.Updates, f updatesFixture, s stack.Started) error {
			_, err := svc.Renew(ctx, f.ref, s.Update.ID, s.Token, time.Minute)

			return err
		}},
		{"complete", func(svc *stack.Updates, f updatesFixture, s stack.Started) error {
			_, err := svc.Complete(ctx, f.ref, s.Update.ID, s.Token, stack.StatusSucceeded)

			return err
		}},
		{"checkpoint", func(svc *stack.Updates, f updatesFixture, s stack.Started) error {
			return svc.SaveCheckpoint(ctx, f.ref, s.Update.ID, s.Token, checkpoint(1, envelope(`{"a":1}`)))
		}},
		{"events", func(svc *stack.Updates, f updatesFixture, s stack.Started) error {
			return svc.AppendEvents(ctx, f.ref, s.Update.ID, s.Token, []stack.Event{})
		}},
	}
}

// startByToken creates an update on svc and starts it as the access token
// tok-svc does.
func (f updatesFixture) startByToken(t *testing.T, svc *stack.Updates) stack.Started {
	t.Helper()

	u, err := svc.Create(t.Context(), f.ref, stack.Plan{
		Kind: stack.KindUpdate, DryRun: false, CreatedBy: "", RestoredFrom: 0, GitHead: "",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	s, err := svc.StartBy(t.Context(), f.ref, u.ID, stack.StartedBy{TokenID: "tok-svc", JobID: ""}, 0)
	if err != nil {
		t.Fatalf("start: %v", err)
	}

	return s
}

// TestLeaseRequestsRefuseWhenTheTokenCheckFails sends each lease request of an
// update that a token started to a service whose token check refuses, as it
// does for a token whose maker lost the act-as path. Each request answers
// ErrUnauthenticated, and none changes the update. A request with a passing
// check does not answer ErrUnauthenticated.
func TestLeaseRequestsRefuseWhenTheTokenCheckFails(t *testing.T) {
	t.Parallel()

	for _, r := range leaseRequests() {
		t.Run(r.name, func(t *testing.T) {
			t.Parallel()

			f := newUpdates(t)
			checker := &switchChecker{refuse: atomic.Bool{}, mu: sync.Mutex{}, id: ""}
			svc := f.withTokens(t, checker)
			s := f.startByToken(t, svc)

			checkTokenRefusal(t, r, &tokenLease{f: f, svc: svc, checker: checker, s: s})
		})
	}
}

// tokenLease is an update that a token started, with the service and the
// checker that run its lease requests.
type tokenLease struct {
	f       updatesFixture
	svc     *stack.Updates
	checker *switchChecker
	s       stack.Started
}

// checkTokenRefusal runs r with a passing check, turns the check to refusing, and
// proves that r answers ErrUnauthenticated and changes nothing.
func checkTokenRefusal(t *testing.T, r leaseRequest, l *tokenLease) {
	t.Helper()

	f, svc, checker, s := l.f, l.svc, l.checker, l.s

	passErr := r.do(svc, f, s)
	if errors.Is(passErr, identity.ErrUnauthenticated) {
		t.Fatalf("%s with a passing check: %v", r.name, passErr)
	}

	before, getErr := svc.Get(t.Context(), f.ref, s.Update.ID)
	if getErr != nil {
		t.Fatalf("get before: %v", getErr)
	}

	checker.refuse.Store(true)

	refusedErr := r.do(svc, f, s)
	if !errors.Is(refusedErr, identity.ErrUnauthenticated) {
		t.Fatalf("%s with a refusing check = %v, want ErrUnauthenticated", r.name, refusedErr)
	}

	after, afterErr := svc.Get(t.Context(), f.ref, s.Update.ID)
	if afterErr != nil {
		t.Fatalf("get after: %v", afterErr)
	}

	if after.Revision != before.Revision || after.Status != before.Status {
		t.Fatalf("a refused %s changed the update: %+v -> %+v", r.name, before, after)
	}

	if got := checker.last(); got != "tok-svc" {
		t.Fatalf("the check ran for token %q, want %q", got, "tok-svc")
	}
}

// TestLeaseRequestsRefuseATokenUpdateWithNoChecker proves that a service with
// no token checker fails closed for an update that a token started: each
// lease request answers ErrAccessTokenUnchecked.
func TestLeaseRequestsRefuseATokenUpdateWithNoChecker(t *testing.T) {
	t.Parallel()

	f := newUpdates(t)
	s := f.startByToken(t, f.svc)

	_, err := f.svc.Renew(t.Context(), f.ref, s.Update.ID, s.Token, time.Minute)
	if !errors.Is(err, stack.ErrAccessTokenUnchecked) {
		t.Fatalf("renew with no checker = %v, want ErrAccessTokenUnchecked", err)
	}
}
