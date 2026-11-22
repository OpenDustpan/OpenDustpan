package profiletest_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"testing"

	"opendustpan.org/opendustpan/internal/expr"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/profile"
)

// rebuildOpenService makes the service of fx again, with the settings that
// mutate changes. The fixture keeps its stores and its grants.
func rebuildOpenService(t *testing.T, fx openFixture, mutate func(*profile.Config)) *profile.Service {
	t.Helper()

	eval, err := expr.NewWithTimeout(expr.NewLimiter(), expr.MaxTestTimeout)
	if err != nil {
		t.Fatalf("expr evaluator: %v", err)
	}

	cfg := profile.Config{
		Store: fx.m, Namespaces: memNamespaces{m: fx.m}, Sealer: scopedSealer{}, Audit: memAudit{m: fx.m},
		Authorizer: grantsFunc(fx.grants), Sessions: memSessions{m: fx.m}, Evaluator: eval, Now: openNow,
		Logger: nil,
	}
	mutate(&cfg)

	svc, err := profile.NewService(cfg)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}

	return svc
}

// nthAuthz grants like grantsFunc, and denies the call number denyAt. A
// denyAt of 0 denies no call.
type nthAuthz struct {
	grants grantsFunc

	mu     sync.Mutex
	calls  int
	denyAt int
}

func (a *nthAuthz) Authorize(ctx context.Context, p identity.Principal, act identity.Action, o identity.Object) error {
	a.mu.Lock()
	a.calls++
	n := a.calls
	a.mu.Unlock()

	if n == a.denyAt {
		return fmt.Errorf("%w: call %d", identity.ErrDenied, n)
	}

	return a.grants.Authorize(ctx, p, act, o)
}

func (a *nthAuthz) ListObjects(ctx context.Context, p identity.Principal, act identity.Action) (identity.Scope, error) {
	return a.grants.ListObjects(ctx, p, act)
}

func (a *nthAuthz) count() int {
	a.mu.Lock()
	defer a.mu.Unlock()

	return a.calls
}

// chainFixture makes a fixture with a chain of three profiles: root imports
// mid, and mid imports base. An open reads three revisions.
func chainFixture(t *testing.T) openFixture {
	t.Helper()

	fx := newOpenFixture(t)

	createOpenProfile(t, fx, "base", "schemaVersion: 1\nvalues:\n  region: eu-west-1\n")
	createOpenProfile(t, fx, "mid", "schemaVersion: 1\nimports: [base]\nvalues:\n  tier: gold\n")
	createOpenProfile(t, fx, "root", "schemaVersion: 1\nimports: [mid]\nvalues:\n  name: web\n")

	return fx
}

// entriesWithOutcome counts the access entries of the namespace with outcome.
func entriesWithOutcome(fx openFixture, outcome audit.Outcome) int {
	n := 0

	entries := accessEntries(fx, fx.ns)
	for i := range entries {
		if entries[i].Outcome == outcome {
			n++
		}
	}

	return n
}

// TestOpenChecksTheGrantBeforeEachRevisionRead denies the Nth call of the
// Authorizer for each N in one open. Each denial fails closed: no session,
// and one denied entry with no failure entry. It fails when the open checks
// profile:open once, before the first read, or when a denial in the middle of
// the open writes the outcome failure.
func TestOpenChecksTheGrantBeforeEachRevisionRead(t *testing.T) {
	t.Parallel()

	probe := chainFixture(t)
	count := &nthAuthz{grants: grantsFunc(probe.grants), mu: sync.Mutex{}, calls: 0, denyAt: 0}
	svc := rebuildOpenService(t, probe, func(c *profile.Config) { c.Authorizer = count })

	_, err := svc.Open(t.Context(), openActor("admin"), profile.OpenInput{
		Namespace: probe.ns, Ref: profile.RefByName("root"), Paths: nil, Consumer: profile.OpenConsumerAPI, Duration: 0,
	})
	if err != nil {
		t.Fatalf("open: %v", err)
	}

	// One check at the start and one before each of the three revisions.
	const revisions = 3

	total := count.count()
	if total < 1+revisions {
		t.Fatalf("the open made %d authorization calls, want at least %d", total, 1+revisions)
	}

	for n := 1; n <= total; n++ {
		fx := chainFixture(t)
		deny := &nthAuthz{grants: grantsFunc(fx.grants), mu: sync.Mutex{}, calls: 0, denyAt: n}
		denySvc := rebuildOpenService(t, fx, func(c *profile.Config) { c.Authorizer = deny })

		in := profile.OpenInput{
			Namespace: fx.ns, Ref: profile.RefByName("root"), Paths: nil,
			Consumer: profile.OpenConsumerAPI, Duration: 0,
		}

		_, err = denySvc.Open(t.Context(), openActor("admin"), in)
		if !errors.Is(err, identity.ErrDenied) {
			t.Errorf("deny call %d: open error = %v, want a denial", n, err)

			continue
		}

		if got := len(fx.m.sessions); got != 0 {
			t.Errorf("deny call %d: sessions = %d, want 0", n, got)
		}

		denied := entriesWithOutcome(fx, audit.OutcomeDenied)
		failed := entriesWithOutcome(fx, audit.OutcomeFailure)

		if denied != 1 || failed != 0 {
			t.Errorf("deny call %d: denied entries = %d, failure entries = %d, want 1 and 0", n, denied, failed)
		}
	}
}

// TestOpenOwnerIsTheCaller opens as alice. Alice reads her session. Admin
// holds profile:open too, and reads it as not found, and the session stays.
// It fails when the owner of the stored session is not the subject of the
// caller.
func TestOpenOwnerIsTheCaller(t *testing.T) {
	t.Parallel()

	fx := newOpenFixture(t)

	createOpenProfile(t, fx, "root", docWithSecret())

	fx.grants["alice"] = []identity.Action{open}

	opened, err := openAs(t, fx, "alice", openSpec{
		ref: profile.RefByName("root"), paths: nil, consumer: profile.OpenConsumerAPI, duration: 0,
	})
	if err != nil {
		t.Fatalf("open as alice: %v", err)
	}

	data, err := fx.svc.ReadSession(t.Context(), openActor("alice"), fx.ns, opened.SessionID, nil)
	if err != nil || data.Values["region"] != "eu-west-1" {
		t.Fatalf("alice reads her session: values = %v, error = %v", data.Values, err)
	}

	_, err = fx.svc.ReadSession(t.Context(), openActor("admin"), fx.ns, opened.SessionID, nil)
	if !errors.Is(err, profile.ErrNotFound) {
		t.Fatalf("admin reads the session of alice: error = %v, want ErrNotFound", err)
	}

	if _, err = fx.svc.ReadSession(t.Context(), openActor("alice"), fx.ns, opened.SessionID, nil); err != nil {
		t.Errorf("alice reads again after a refused read of admin: %v", err)
	}
}

// TestOpenResolveFailureWritesOneFailureEntry fails when a refusal after the
// merge starts writes no entry, writes more than one, or names a value.
func TestOpenResolveFailureWritesOneFailureEntry(t *testing.T) {
	t.Parallel()

	secretDoc := func(extra string) string {
		return "schemaVersion: 1\n" + extra + "values:\n  db:\n    password: { $secret: " + openSecret + " }\n"
	}

	for name, setup := range map[string]func(fx openFixture) string{
		"provider block": func(fx openFixture) string {
			createOpenProfile(t, fx, "root", secretDoc("providers:\n  vault:\n    use: hashivault\n"))

			return "root"
		},
		"import cycle": func(fx openFixture) string {
			createOpenProfile(t, fx, "a", secretDoc("imports: [b]\n"))
			createOpenProfile(t, fx, "b", secretDoc("imports: [a]\n"))

			return "a"
		},
		"final value": func(fx openFixture) string {
			createOpenProfile(t, fx, "base", "schemaVersion: 1\nvalues:\n  region: { $value: x, $final: true }\n"+
				"  db:\n    password: { $secret: "+openSecret+" }\n")
			createOpenProfile(t, fx, "root", "schemaVersion: 1\nimports: [base]\nvalues:\n  region: y\n")

			return "root"
		},
	} {
		fx := newOpenFixture(t)
		root := setup(fx)

		before := len(accessEntries(fx, fx.ns))

		_, err := openAs(t, fx, "admin", openSpec{
			ref: profile.RefByName(profile.Name(root)), paths: nil, consumer: profile.OpenConsumerAPI, duration: 0,
		})
		if err == nil {
			t.Errorf("%s: the open succeeded", name)

			continue
		}

		got := accessEntries(fx, fx.ns)[before:]
		if len(got) != 1 || got[0].Outcome != audit.OutcomeFailure {
			t.Errorf("%s: entries = %+v, want one failure entry", name, got)

			continue
		}

		if strings.Contains(entryText(t, got), openSecret) || strings.Contains(err.Error(), openSecret) {
			t.Errorf("%s: an entry or the error names a value", name)
		}
	}
}

// leakyEvaluator fails with a message that holds a value, as the engine does
// for int(values.password).
type leakyEvaluator struct{}

var errLeaky = errors.New("invalid operation: int(" + openSecret + ")")

func (leakyEvaluator) Eval(_ context.Context, _ string, _ map[string]any) (profile.Evaluation, error) {
	return profile.Evaluation{}, errLeaky
}

// TestOpenScrubsTheErrorOfTheEvaluator is the layer of the service. The
// evaluator here fails with a message that holds a secret: the open error
// must not hold it. It fails when the service passes the message on.
func TestOpenScrubsTheErrorOfTheEvaluator(t *testing.T) {
	t.Parallel()

	fx := newOpenFixture(t)

	createOpenProfile(t, fx, "root", "schemaVersion: 1\nvalues:\n  n: 1\n  derived: ${values.n}\n")

	svc := rebuildOpenService(t, fx, func(c *profile.Config) { c.Evaluator = leakyEvaluator{} })

	_, err := svc.Open(t.Context(), openActor("admin"), profile.OpenInput{
		Namespace: fx.ns, Ref: profile.RefByName("root"), Paths: nil, Consumer: profile.OpenConsumerAPI, Duration: 0,
	})
	if !errors.Is(err, profile.ErrReference) {
		t.Fatalf("open error = %v, want ErrReference", err)
	}

	if strings.Contains(err.Error(), openSecret) {
		t.Errorf("open error names a value: %v", err)
	}
}

// TestOpenRealEngineErrorNamesNoValue opens with expressions that fail when
// they run, on a profile that holds a secret. The error and the entries name
// no value.
func TestOpenRealEngineErrorNamesNoValue(t *testing.T) {
	t.Parallel()

	for _, expression := range []string{"int(values.db.password)", "float(values.db.password)"} {
		fx := newOpenFixture(t)

		createOpenProfile(t, fx, "root", "schemaVersion: 1\nvalues:\n  db:\n    password: { $secret: "+openSecret+
			" }\n  bad: ${"+expression+"}\n")

		_, err := openRoot(t, fx)
		if !errors.Is(err, profile.ErrReference) {
			t.Errorf("%s: open error = %v, want ErrReference", expression, err)

			continue
		}

		if strings.Contains(err.Error(), openSecret) ||
			strings.Contains(entryText(t, accessEntries(fx, fx.ns)), openSecret) {
			t.Errorf("%s: the error or an entry names a value", expression)
		}
	}
}

// TestReadSessionBoundsThePaths fails when ReadSession takes more than
// MaxSessionPaths paths or a path above MaxSessionPathBytes.
func TestReadSessionBoundsThePaths(t *testing.T) {
	t.Parallel()

	fx := newOpenFixture(t)

	createOpenProfile(t, fx, "root", docWithSecret())

	opened, err := openRoot(t, fx)
	if err != nil {
		t.Fatalf("open: %v", err)
	}

	many := make([]string, profile.MaxSessionPaths+1)
	for i := range many {
		many[i] = "region"
	}

	_, err = fx.svc.ReadSession(t.Context(), openActor("admin"), fx.ns, opened.SessionID, many)
	if !errors.Is(err, profile.ErrInvalid) {
		t.Errorf("%d paths: read error = %v, want ErrInvalid", len(many), err)
	}

	long := []string{strings.Repeat("a", profile.MaxSessionPathBytes+1)}
	_, err = fx.svc.ReadSession(t.Context(), openActor("admin"), fx.ns, opened.SessionID, long)
	if !errors.Is(err, profile.ErrInvalid) {
		t.Errorf("a path of %d bytes: read error = %v, want ErrInvalid", len(long[0]), err)
	}
}

// syncBuffer is a log sink that two goroutines may write.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	n, err := b.buf.Write(p)
	if err != nil {
		return n, fmt.Errorf("write log: %w", err)
	}

	return n, nil
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.buf.String()
}

var errDeleteFault = errors.New("delete fault with a driver detail")

// deniedReadFixture opens a session as admin, then removes profile:open from
// admin, so that the next read is a denial that deletes the session.
func deniedReadFixture(t *testing.T) (openFixture, profile.Opened) {
	t.Helper()

	fx := newOpenFixture(t)

	createOpenProfile(t, fx, "root", docWithSecret())

	opened, err := openRoot(t, fx)
	if err != nil {
		t.Fatalf("open: %v", err)
	}

	fx.grants["admin"] = []identity.Action{read}

	return fx, opened
}

// TestDeniedReadWritesTheEntryWhenTheDeleteFails fails when a failed delete
// leaves the denial with no audit entry.
func TestDeniedReadWritesTheEntryWhenTheDeleteFails(t *testing.T) {
	t.Parallel()

	fx, opened := deniedReadFixture(t)
	svc := rebuildOpenService(t, fx, func(c *profile.Config) {
		c.Sessions = faultSessions{Sessions: memSessions{m: fx.m}, putErr: nil, delErr: errDeleteFault}
	})

	before := entriesWithOutcome(fx, audit.OutcomeDenied)

	_, err := svc.ReadSession(t.Context(), openActor("admin"), fx.ns, opened.SessionID, nil)
	if !errors.Is(err, identity.ErrDenied) {
		t.Fatalf("read error = %v, want a denial", err)
	}

	if got := entriesWithOutcome(fx, audit.OutcomeDenied) - before; got != 1 {
		t.Errorf("denied entries = %d, want 1 from the fallback", got)
	}
}

// TestRefusalEntryFailureIsLoggedWithoutDetail fails when a refusal whose
// audit entry cannot be written leaves no warning, or a warning that holds a
// value or the text of an error.
func TestRefusalEntryFailureIsLoggedWithoutDetail(t *testing.T) {
	t.Parallel()

	t.Run("denied read, delete and entry both fail", func(t *testing.T) {
		t.Parallel()

		fx, opened := deniedReadFixture(t)
		logs := &syncBuffer{mu: sync.Mutex{}, buf: bytes.Buffer{}}
		svc := rebuildOpenService(t, fx, func(c *profile.Config) {
			c.Sessions = faultSessions{Sessions: memSessions{m: fx.m}, putErr: nil, delErr: errDeleteFault}
			c.Logger = slog.New(slog.NewTextHandler(logs, nil))
		})

		fx.m.failAppend = true

		_, err := svc.ReadSession(t.Context(), openActor("admin"), fx.ns, opened.SessionID, nil)
		if !errors.Is(err, identity.ErrDenied) {
			t.Fatalf("read error = %v, want a denial", err)
		}

		checkWarning(t, logs.String())
	})

	t.Run("denied open, entry fails", func(t *testing.T) {
		t.Parallel()

		fx := newOpenFixture(t)

		createOpenProfile(t, fx, "root", docWithSecret())

		logs := &syncBuffer{mu: sync.Mutex{}, buf: bytes.Buffer{}}
		svc := rebuildOpenService(t, fx, func(c *profile.Config) {
			c.Logger = slog.New(slog.NewTextHandler(logs, nil))
		})

		fx.m.failAppend = true

		_, err := svc.Open(t.Context(), openJobActor("admin"), profile.OpenInput{
			Namespace: fx.ns,
			Ref:       profile.RefByName("root"),
			Paths:     nil,
			Consumer:  profile.OpenConsumerAPI,
			Duration:  0,
		})
		if !errors.Is(err, identity.ErrDenied) {
			t.Fatalf("open error = %v, want a denial", err)
		}

		checkWarning(t, logs.String())
	})
}

// checkWarning checks that the log holds the warning of a missing entry, and
// no value and no text of an error.
func checkWarning(t *testing.T, log string) {
	t.Helper()

	if !strings.Contains(log, "audit entry of a refusal was not written") {
		t.Errorf("log = %q, want the warning of the missing entry", log)
	}

	for _, leak := range []string{openSecret, errAppend.Error(), errDeleteFault.Error(), "driver detail"} {
		if strings.Contains(log, leak) {
			t.Errorf("the log holds %q: %s", leak, log)
		}
	}
}
