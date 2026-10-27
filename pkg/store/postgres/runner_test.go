package postgres_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/durable"
	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/runner"
	"opendustpan.org/opendustpan/pkg/runner/runnertest"
	"opendustpan.org/opendustpan/pkg/store/postgres"
)

// runnerDomain makes the rows that the runner stores need, on the real
// durable store and the real namespaces of one PostgreSQL store.
type runnerDomain struct {
	t       *testing.T
	s       *postgres.Store
	counter atomic.Int64
}

var _ runnertest.Domain = (*runnerDomain)(nil)

func (d *runnerDomain) CreateNamespace(ctx context.Context, name namespace.Name) (namespace.Fence, error) {
	ns := namespace.Namespace{
		ID:                  namespace.NewID(),
		CreatedAt:           time.UnixMilli(1_700_000_000_000).UTC(),
		Name:                name,
		PrimaryInstallation: "eu-west",
		FailoverVersion:     1,
		Overrides:           namespace.Overrides{Forced: false, Unlocked: false},
	}

	if err := d.s.CreateNamespace(ctx, ns); err != nil {
		return namespace.Fence{}, fmt.Errorf("create namespace: %w", err)
	}

	return ns.Fence(), nil
}

func (d *runnerDomain) BumpFence(ctx context.Context, fence namespace.Fence) (namespace.Fence, error) {
	if _, err := d.s.BumpFailoverVersions(ctx); err != nil {
		return namespace.Fence{}, fmt.Errorf("bump failover versions: %w", err)
	}

	ns, err := d.s.GetNamespace(ctx, fence.Namespace)
	if err != nil {
		return namespace.Fence{}, fmt.Errorf("get namespace: %w", err)
	}

	return ns.Fence(), nil
}

func (d *runnerDomain) ClaimLease(
	ctx context.Context, kind durable.Kind, owner string, lease time.Duration,
) (durable.Claim, error) {
	ops := d.s.DurableOperations()

	key := fmt.Sprintf("op-%d", d.counter.Add(1))
	if _, _, err := ops.Start(ctx, durable.StartRequest{Key: key, Kind: kind, Version: 0, Input: nil}); err != nil {
		return durable.Claim{}, fmt.Errorf("start operation: %w", err)
	}

	op, err := ops.Claim(ctx, durable.ClaimRequest{
		Owner: owner, Kinds: []durable.Kind{kind}, MaxVersions: nil, Lease: lease,
		TakeCanceled: false,
	})
	if err != nil {
		return durable.Claim{}, fmt.Errorf("claim operation: %w", err)
	}

	return op.Claim, nil
}

func (d *runnerDomain) Shorten(ctx context.Context, c durable.Claim, lease time.Duration) (durable.Claim, error) {
	shorter, err := d.s.DurableOperations().Extend(ctx, c, lease)
	if err != nil {
		return durable.Claim{}, fmt.Errorf("extend claim: %w", err)
	}

	return shorter, nil
}

func (d *runnerDomain) Takeover(ctx context.Context, c durable.Claim) (durable.Claim, error) {
	ops := d.s.DurableOperations()

	op, err := ops.Get(ctx, c.Operation)
	if err != nil {
		return durable.Claim{}, fmt.Errorf("get operation: %w", err)
	}

	if err = ops.Release(ctx, c); err != nil {
		return durable.Claim{}, fmt.Errorf("release claim: %w", err)
	}

	taken, err := ops.Claim(ctx, durable.ClaimRequest{
		Owner: c.Owner, Kinds: []durable.Kind{op.Kind}, MaxVersions: nil, Lease: time.Hour,
		TakeCanceled: false,
	})
	if err != nil {
		return durable.Claim{}, fmt.Errorf("claim operation again: %w", err)
	}

	return taken.Claim, nil
}

func (d *runnerDomain) End(ctx context.Context, c durable.Claim) error {
	if err := d.s.DurableOperations().Complete(ctx, c); err != nil {
		return fmt.Errorf("complete operation: %w", err)
	}

	return nil
}

func (d *runnerDomain) UsePool(ctx context.Context, _ namespace.Fence, id string) error {
	key := fmt.Sprintf("use-%s-%d", id, d.counter.Add(1))

	_, _, err := d.s.DurableOperations().Start(ctx, durable.StartRequest{
		Key: key, Kind: durable.Kind("job.runner." + id), Version: 0, Input: nil,
	})
	if err != nil {
		return fmt.Errorf("start operation: %w", err)
	}

	return nil
}

func (d *runnerDomain) AuditEntries(ctx context.Context, ns namespace.Name) ([]audit.Entry, error) {
	var out []audit.Entry

	for page := 1; ; page++ {
		listing, err := d.s.List(ctx, ns, audit.Page{Number: page, Size: audit.MaxPageSize})
		if err != nil {
			return nil, fmt.Errorf("list audit: %w", err)
		}

		out = append(out, listing.Entries...)

		if len(listing.Entries) < audit.MaxPageSize {
			break
		}
	}

	slices.SortFunc(out, func(a, b audit.Entry) int { return int(a.Sequence - b.Sequence) })

	return out, nil
}

func TestRunnersConformance(t *testing.T) {
	t.Parallel()

	base := os.Getenv(envURL)
	if base == "" {
		t.Skipf("set %s to run the PostgreSQL conformance suite", envURL)
	}

	runnertest.Run(t, func(t *testing.T) runnertest.Harness {
		t.Helper()

		s := open(t, base)
		r := s.Runners()

		return runnertest.Harness{
			Pools: r, Members: r, Sessions: r, Domain: &runnerDomain{t: t, s: s, counter: atomic.Int64{}},
		}
	})
}

func hexSum(s string) string {
	sum := sha256.Sum256([]byte(s))

	return hex.EncodeToString(sum[:])
}

// TestRunnersOpenSessionRace proves that of several concurrent opens of one
// handoff token, exactly one opens a session.
func TestRunnersOpenSessionRace(t *testing.T) {
	t.Parallel()

	base := os.Getenv(envURL)
	if base == "" {
		t.Skipf("set %s to run the PostgreSQL conformance suite", envURL)
	}

	ctx := t.Context()
	s := open(t, base)
	r := s.Runners()
	d := &runnerDomain{t: t, s: s, counter: atomic.Int64{}}
	at := time.Now().UTC().Truncate(time.Millisecond)

	fence, err := d.CreateNamespace(ctx, "acme")
	if err != nil {
		t.Fatalf("CreateNamespace: %v", err)
	}

	entry := func() []audit.Entry {
		return []audit.Entry{{
			At: at, Namespace: fence.Namespace, Subject: "alice", IDPSubject: "", TokenID: "",
			Action: "namespace:manage-runners", Object: "namespace:" + string(fence.Namespace), Version: "",
			ValuePath: "", ValueCount: nil, Changes: nil, SourceIP: "", UserAgent: "", Channel: audit.ChannelAPI,
			Kind: audit.KindChange, Outcome: audit.OutcomeSuccess, RequestID: "", ObjectID: "", PrevHash: "",
			Hash: "", Sequence: 0, FenceVersion: fence.FailoverVersion, PrincipalKind: "", JobID: "", Reason: "",
		}}
	}

	pool, err := r.CreatePool(ctx, runner.Pool{
		ID: "", Namespace: "", Name: "prod", Purpose: runner.PurposeJobs, Lease: 0, Concurrency: 0, Isolation: nil,
		GitCredentials: runner.GitCredentials{Mode: "", Helper: ""}, CreatedAt: time.Time{}, UpdatedAt: time.Time{},
	}, fence, entry())
	if err != nil {
		t.Fatalf("CreatePool: %v", err)
	}

	secret := runner.JoinTokenPrefix + "race"
	_, err = r.CreateJoinToken(ctx, runner.JoinToken{
		ID: "", Pool: pool.ID, SHA256: hexSum(secret), CreatedBy: "alice@example.com", CreatedAt: at,
		ExpiresAt: at.Add(time.Hour), UsedAt: time.Time{}, UsedBy: "", RevokedAt: time.Time{},
	}, fence, entry())
	if err != nil {
		t.Fatalf("CreateJoinToken: %v", err)
	}

	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}

	joined, err := r.Join(ctx, runner.Joining{
		TokenSHA256: hexSum(secret), At: at,
		Request: runner.JoinRequest{
			Token: "", Name: "a", Kind: runner.KindKubernetes, Isolation: runner.IsolationKubernetes, Labels: nil,
			Release: runner.Release{Major: 0, Minor: 0, Patch: 0, Pre: ""},
			Key:     runner.Key{Public: pub, CreatedAt: at}, Proof: nil,
		},
	}, fence, func(*runner.Joined) []audit.Entry { return entry() })
	if err != nil {
		t.Fatalf("Join: %v", err)
	}

	c, err := d.ClaimLease(ctx, job.RunnerKind(pool.ID), joined.Runner.ID, time.Hour)
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}

	tok := runner.HandoffToken{
		SHA256: hexSum("dph_race"), Operation: string(c.Operation), Token: c.Token, Runner: joined.Runner.ID,
		IssuedAt: at, ExpiresAt: at.Add(runner.HandoffTokenTTL), UsedAt: time.Time{},
	}
	if err = r.IssueHandoffToken(ctx, tok, c); err != nil {
		t.Fatalf("IssueHandoffToken: %v", err)
	}

	const racers = 8

	var (
		wg   sync.WaitGroup
		wins atomic.Int64
	)

	for range racers {
		wg.Go(func() {
			_, openErr := r.OpenSession(ctx, tok.SHA256, runner.Key{Public: pub, CreatedAt: at}, at)
			if openErr == nil {
				wins.Add(1)
			}
		})
	}

	wg.Wait()

	if got := wins.Load(); got != 1 {
		t.Fatalf("%d of %d opens of one handoff token succeeded, want 1", got, racers)
	}
}
