package profiletest_test

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/expr"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/profile"
	"opendustpan.org/opendustpan/pkg/secret"
)

// openFixture wires a Service with the memory store, the scoped sealer, the
// real expression engine and mutable grants.
type openFixture struct {
	svc    *profile.Service
	m      *mem
	ns     namespace.Name
	grants map[string][]identity.Action
}

func newOpenFixture(t *testing.T) openFixture {
	t.Helper()

	m := newMem()
	m.namespaces["acme"] = namespace.Namespace{
		ID: "", CreatedAt: epochTime(), Name: "acme", PrimaryInstallation: "", FailoverVersion: 1,
		Overrides: namespace.Overrides{Forced: false, Unlocked: false},
	}

	eval, err := expr.New(expr.NewLimiter())
	if err != nil {
		t.Fatalf("expr evaluator: %v", err)
	}

	grants := map[string][]identity.Action{
		"admin":  {read, write, open, manage},
		"reader": {read},
		"nobody": nil,
	}

	svc, err := profile.NewService(profile.Config{
		Store: m, Namespaces: memNamespaces{m: m}, Sealer: scopedSealer{}, Audit: memAudit{m: m},
		Authorizer: grantsFunc(grants), Sessions: memSessions{m: m}, Evaluator: eval, Now: nil,
	})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}

	return openFixture{svc: svc, m: m, ns: "acme", grants: grants}
}

// grantsFunc authorizes from a mutable map.
type grantsFunc map[string][]identity.Action

func (g grantsFunc) Authorize(
	_ context.Context, p identity.Principal, a identity.Action, o identity.Object,
) error {
	if slices.Contains(g[p.Subject], a) {
		return nil
	}

	return fmt.Errorf("%w: %s on %s", identity.ErrDenied, a, o)
}

func (grantsFunc) ListObjects(context.Context, identity.Principal, identity.Action) (identity.Scope, error) {
	return identity.Scope{Namespaces: nil, Collections: nil, Units: nil, Principals: nil}, nil
}

// openActor returns the actor of subject with a personal credential.
func openActor(subject string) *profile.Actor {
	return actor(subject)
}

// openJobActor returns the actor of subject with a job token.
func openJobActor(subject string) *profile.Actor {
	a := actor(subject)
	a.Principal.Credential, a.Principal.JobID = identity.CredentialJobToken, "job-1"

	return a
}

// createOpenProfile saves one profile with doc and returns it.
func createOpenProfile(t *testing.T, fx openFixture, name, doc string) profile.Profile {
	t.Helper()

	created, _, err := fx.svc.Create(t.Context(), openActor("admin"), fx.ns, profile.NewProfile{
		Name: profile.Name(name), Document: []byte(doc), Reason: "",
	})
	if err != nil {
		t.Fatalf("create profile %q: %v", name, err)
	}

	return created
}

// openSpec is one open of openAs: the ref, the paths, the consumer and the
// duration of the open.
type openSpec struct {
	ref      profile.Ref
	paths    []string
	consumer profile.OpenConsumer
	duration time.Duration
}

// openAs opens spec as subject.
func openAs(t *testing.T, fx openFixture, subject string, spec openSpec) (profile.Opened, error) {
	t.Helper()

	return fx.svc.Open(t.Context(), openActor(subject), profile.OpenInput{
		Namespace: fx.ns, Ref: spec.ref, Paths: spec.paths, Consumer: spec.consumer, Duration: spec.duration,
	})
}

// openRoot opens the root profile as admin with the API consumer.
func openRoot(t *testing.T, fx openFixture) (profile.Opened, error) {
	t.Helper()

	return openAs(t, fx, "admin", openSpec{
		ref: profile.RefByName("root"), paths: nil, consumer: profile.OpenConsumerAPI, duration: 0,
	})
}

// openRef opens the root profile as admin and reads its session.
func openRef(t *testing.T, fx openFixture) (profile.Opened, profile.SessionData) {
	t.Helper()

	const name = "root"

	opened, err := openAs(t, fx, "admin", openSpec{
		ref: profile.RefByName(profile.Name(name)), paths: nil, consumer: profile.OpenConsumerAPI, duration: 0,
	})
	if err != nil {
		t.Fatalf("open %q: %v", name, err)
	}

	data, err := fx.svc.ReadSession(t.Context(), openActor("admin"), fx.ns, opened.SessionID, nil)
	if err != nil {
		t.Fatalf("read session of %q: %v", name, err)
	}

	return opened, data
}

// accessEntries returns the access entries of the namespace.
func accessEntries(fx openFixture, ns namespace.Name) []audit.Entry {
	out := []audit.Entry{}

	for i := range fx.m.entries[ns] {
		if fx.m.entries[ns][i].Kind == audit.KindAccess {
			out = append(out, fx.m.entries[ns][i])
		}
	}

	return out
}

// entryText renders the entries for a leak check.
func entryText(t *testing.T, es []audit.Entry) string {
	t.Helper()

	return fmt.Sprintf("%+v", es)
}

const openSecret = "s3cr3t-open"

// secretDocWithRegion is the document of a root profile with one secret.
func docWithSecret() string {
	return "schemaVersion: 1\nvalues:\n  region: eu-west-1\n  db:\n    password: { $secret: " + openSecret + " }\n"
}

func TestOpenMergeInListOrder(t *testing.T) {
	t.Parallel()

	fx := newOpenFixture(t)

	createOpenProfile(t, fx, "base",
		"schemaVersion: 1\nvalues:\n  region: eu-west-1\n  tags:\n    team: a\n  list: [1]\n  gone: x\n")
	createOpenProfile(
		t,
		fx,
		"mid",
		"schemaVersion: 1\nimports: [base]\nvalues:\n  region: eu-west-2\n  tags:\n    owner: b\n  list: [2]\n  gone: null\n",
	)
	createOpenProfile(t, fx, "root", "schemaVersion: 1\nimports: [mid]\nvalues:\n  extra: yes\n")

	_, data := openRef(t, fx)

	want := map[string]any{
		"region": "eu-west-2", "tags.team": "a", "tags.owner": "b", "extra": "yes",
	}

	for path, value := range want {
		if data.Values[path] != value {
			t.Errorf("values[%q] = %v, want %v", path, data.Values[path], value)
		}
	}

	if got := data.Values["list"]; fmt.Sprint(got) != "[2]" {
		t.Errorf("values[list] = %v, want [2]", got)
	}

	if _, ok := data.Values["gone"]; ok {
		t.Errorf("values[gone] = %v, want no value: null removes", data.Values["gone"])
	}
}

func TestOpenFinal(t *testing.T) {
	t.Parallel()

	fx := newOpenFixture(t)

	createOpenProfile(t, fx, "base",
		"schemaVersion: 1\nvalues:\n  region: { $value: eu-west-1, $final: true }\n")
	createOpenProfile(t, fx, "root", "schemaVersion: 1\nimports: [base]\nvalues:\n  region: eu-west-2\n")

	_, err := openRoot(t, fx)
	if !errors.Is(err, profile.ErrFinal) {
		t.Fatalf("open error = %v, want ErrFinal", err)
	}

	if !strings.Contains(err.Error(), "region") || !strings.Contains(err.Error(), "base") {
		t.Errorf("open error = %v, want the path and both profiles", err)
	}

	if n := len(fx.m.sessions); n != 0 {
		t.Errorf("sessions = %d, want 0: a refused open stores nothing", n)
	}
}

func TestOpenNullRemovesFinal(t *testing.T) {
	t.Parallel()

	fx := newOpenFixture(t)

	createOpenProfile(t, fx, "base", "schemaVersion: 1\nvalues:\n  gone: { $value: x, $final: true }\n")
	createOpenProfile(t, fx, "root", "schemaVersion: 1\nimports: [base]\nvalues:\n  gone: null\n")

	_, err := openRoot(t, fx)
	if !errors.Is(err, profile.ErrFinal) {
		t.Fatalf("open error = %v, want ErrFinal", err)
	}
}

func TestOpenCycle(t *testing.T) {
	t.Parallel()

	fx := newOpenFixture(t)

	createOpenProfile(t, fx, "a", "schemaVersion: 1\nimports: [b]\nvalues:\n  x: 1\n")
	createOpenProfile(t, fx, "b", "schemaVersion: 1\nimports: [a]\nvalues:\n  y: 2\n")

	_, err := openAs(
		t,
		fx,
		"admin",
		openSpec{ref: profile.RefByName("a"), paths: nil, consumer: profile.OpenConsumerAPI, duration: 0},
	)
	if !errors.Is(err, profile.ErrImportCycle) {
		t.Fatalf("open error = %v, want ErrImportCycle", err)
	}

	if !strings.Contains(err.Error(), "a") || !strings.Contains(err.Error(), "b") {
		t.Errorf("open error = %v, want the cycle", err)
	}
}

func TestOpenLimits(t *testing.T) {
	t.Parallel()

	t.Run("depth 17", func(t *testing.T) {
		t.Parallel()

		fx := newOpenFixture(t)

		deep := "schemaVersion: 1\nvalues:\n  x: 1\n"
		createOpenProfile(t, fx, "deep-17", deep)

		for n := 16; n >= 0; n-- {
			createOpenProfile(t, fx, fmt.Sprintf("deep-%d", n),
				fmt.Sprintf("schemaVersion: 1\nimports: [deep-%d]\nvalues:\n  x: 1\n", n+1))
		}

		_, err := openAs(
			t,
			fx,
			"admin",
			openSpec{ref: profile.RefByName("deep-0"), paths: nil, consumer: profile.OpenConsumerAPI, duration: 0},
		)
		if !errors.Is(err, profile.ErrLimit) {
			t.Fatalf("open error = %v, want ErrLimit", err)
		}
	})

	t.Run("65 revisions", func(t *testing.T) {
		t.Parallel()

		fx := newOpenFixture(t)

		// A root with 32 mids, each with one leaf: 65 revisions at depth 2.
		mids := make([]string, 0, 32)

		for n := range 32 {
			leaf := fmt.Sprintf("leaf-%d", n)
			mid := fmt.Sprintf("mid-%d", n)
			createOpenProfile(t, fx, leaf, "schemaVersion: 1\nvalues:\n  x: 1\n")
			createOpenProfile(t, fx, mid, "schemaVersion: 1\nimports: ["+leaf+"]\nvalues:\n  x: 1\n")
			mids = append(mids, mid)
		}

		createOpenProfile(t, fx, "root",
			"schemaVersion: 1\nimports: ["+strings.Join(mids, ", ")+"]\nvalues:\n  x: 1\n")

		_, err := openRoot(t, fx)
		if !errors.Is(err, profile.ErrLimit) {
			t.Fatalf("open error = %v, want ErrLimit", err)
		}
	})

	t.Run("result above 4 MiB", func(t *testing.T) {
		t.Parallel()

		fx := newOpenFixture(t)

		big := strings.Repeat("a", 220<<10)

		imports := make([]string, 0, 20)

		for n := range 20 {
			name := fmt.Sprintf("big-%d", n)
			createOpenProfile(t, fx, name, "schemaVersion: 1\nvalues:\n  blob-"+name+": "+big+"\n")
			imports = append(imports, name)
		}

		createOpenProfile(t, fx, "root",
			"schemaVersion: 1\nimports: ["+strings.Join(imports, ", ")+"]\nvalues:\n  x: 1\n")

		_, err := openRoot(t, fx)
		if !errors.Is(err, profile.ErrLimit) {
			t.Fatalf("open error = %v, want ErrLimit", err)
		}
	})
}

func TestOpenExpressionsWholeString(t *testing.T) {
	t.Parallel()

	fx := newOpenFixture(t)

	createOpenProfile(t, fx, "root",
		"schemaVersion: 1\nvalues:\n  n: 3\n  double: ${values.n * 2}\n  text: prefix-${values.n}\n")

	_, data := openRef(t, fx)

	if got := data.Values["double"]; got != 6 && got != 6.0 {
		t.Errorf("values[double] = %v (%T), want 6", got, got)
	}

	if got := data.Values["text"]; got != "prefix-3" {
		t.Errorf("values[text] = %v, want prefix-3", got)
	}
}

func TestOpenExpressionsEscape(t *testing.T) {
	t.Parallel()

	fx := newOpenFixture(t)

	createOpenProfile(t, fx, "root", "schemaVersion: 1\nvalues:\n  t: $${values.n}\n  n: 1\n")

	_, data := openRef(t, fx)

	if got := data.Values["t"]; got != "${values.n}" {
		t.Errorf("values[t] = %v, want the literal", got)
	}
}

func TestOpenExpressionsSecretMark(t *testing.T) {
	t.Parallel()

	fx := newOpenFixture(t)

	createOpenProfile(t, fx, "root", docWithSecret()+
		"  dsn: ${values.db.password}\n  shown: prefix-${values.db.password}-suffix\n")

	_, data := openRef(t, fx)

	if got := data.Values["dsn"]; got != openSecret {
		t.Errorf("values[dsn] = %v, want the secret", got)
	}

	if !data.Secrets["dsn"] || !data.Secrets["shown"] {
		t.Errorf("secrets = %v, want dsn and shown marked secret", data.Secrets)
	}

	if data.Secrets["region"] {
		t.Error("secrets marks region: a plain read of a plain value stays plain")
	}
}

func TestOpenExpressionsOtherName(t *testing.T) {
	t.Parallel()

	fx := newOpenFixture(t)

	for name, doc := range map[string]string{
		"providers": "schemaVersion: 1\nvalues:\n  x: ${providers.db.password}\n",
		"imports":   "schemaVersion: 1\nvalues:\n  x: ${imports[\"base\"].values.x}\n",
		"profiles":  "schemaVersion: 1\nvalues:\n  x: ${profiles[\"base\"].values.x}\n",
		"context":   "schemaVersion: 1\nvalues:\n  x: ${context.namespace}\n",
		"env":       "schemaVersion: 1\nvalues:\n  x: ${$env.region}\n",
		"missing":   "schemaVersion: 1\nvalues:\n  x: ${values.absent}\n",
	} {
		createOpenProfile(t, fx, "root-"+name, doc)

		_, err := openAs(t, fx, "admin", openSpec{
			ref: profile.RefByName(profile.Name("root-" + name)), paths: nil,
			consumer: profile.OpenConsumerAPI, duration: 0,
		})
		if !errors.Is(err, profile.ErrReference) {
			t.Errorf("%s: open error = %v, want ErrReference", name, err)
		}
	}
}

func TestOpenExpressionsFailure(t *testing.T) {
	t.Parallel()

	fx := newOpenFixture(t)

	createOpenProfile(t, fx, "root", "schemaVersion: 1\nvalues:\n  n: 1\n  bad: ${values.n + \"x\"}\n")

	_, err := openRoot(t, fx)
	if !errors.Is(err, profile.ErrReference) {
		t.Fatalf("open error = %v, want ErrReference", err)
	}

	if strings.Contains(err.Error(), openSecret) {
		t.Errorf("open error names a value: %v", err)
	}
}

func TestOpenNeverStore(t *testing.T) {
	t.Parallel()

	doc := "schemaVersion: 1\nvalues:\n  region: eu-west-1\n" +
		"  token: { $secret: " + openSecret + ", $neverStore: true }\n" +
		"  derived: ${values.token}\n"

	t.Run("asked never-store path is refused", func(t *testing.T) {
		t.Parallel()

		fx := newOpenFixture(t)

		createOpenProfile(t, fx, "root", doc)

		_, err := openAs(
			t,
			fx,
			"admin",
			openSpec{
				ref:      profile.RefByName("root"),
				paths:    []string{"token"},
				consumer: profile.OpenConsumerAPI,
				duration: 0,
			},
		)
		if !errors.Is(err, profile.ErrNeverStore) {
			t.Fatalf("open error = %v, want ErrNeverStore", err)
		}

		if !strings.Contains(err.Error(), "token") {
			t.Errorf("open error = %v, want the path", err)
		}

		if n := len(fx.m.sessions); n != 0 {
			t.Errorf("sessions = %d, want 0", n)
		}
	})

	t.Run("computed never-store is refused", func(t *testing.T) {
		t.Parallel()

		fx := newOpenFixture(t)

		createOpenProfile(t, fx, "root", doc)

		_, err := openAs(
			t,
			fx,
			"admin",
			openSpec{
				ref:      profile.RefByName("root"),
				paths:    []string{"derived"},
				consumer: profile.OpenConsumerAPI,
				duration: 0,
			},
		)
		if !errors.Is(err, profile.ErrNeverStore) {
			t.Fatalf("open error = %v, want ErrNeverStore", err)
		}
	})

	t.Run("left out never-store succeeds without it", func(t *testing.T) {
		t.Parallel()

		fx := newOpenFixture(t)

		createOpenProfile(t, fx, "root", doc)

		_, data := openRef(t, fx)

		if _, ok := data.Values["token"]; ok {
			t.Error("session holds the never-store value")
		}

		if _, ok := data.Values["derived"]; ok {
			t.Error("session holds the computed never-store value")
		}

		if data.Values["region"] != "eu-west-1" {
			t.Errorf("values[region] = %v", data.Values["region"])
		}
	})
}

func TestOpenRefusalsProviderBlock(t *testing.T) {
	t.Parallel()

	fx := newOpenFixture(t)

	createOpenProfile(t, fx, "root",
		"schemaVersion: 1\nproviders:\n  vault:\n    use: hashivault\nvalues:\n  x: 1\n")

	_, err := openRoot(t, fx)
	if !errors.Is(err, profile.ErrProvider) {
		t.Fatalf("open error = %v, want ErrProvider", err)
	}

	if !strings.Contains(err.Error(), "vault") {
		t.Errorf("open error = %v, want the block", err)
	}

	if n := len(fx.m.sessions); n != 0 {
		t.Errorf("sessions = %d, want 0", n)
	}
}

func TestOpenRefusalsRuleInChecks(t *testing.T) {
	t.Parallel()

	fx := newOpenFixture(t)

	createOpenProfile(t, fx, "root",
		"schemaVersion: 1\nvalues:\n  x: 1\nchecks:\n  - rule: values.x == 1\n")

	_, err := openRoot(t, fx)
	if !errors.Is(err, profile.ErrInvalid) {
		t.Fatalf("open error = %v, want ErrInvalid", err)
	}

	if !strings.Contains(err.Error(), "root") {
		t.Errorf("open error = %v, want the profile", err)
	}
}

func TestOpenRefusalsRuleInAnImport(t *testing.T) {
	t.Parallel()

	fx := newOpenFixture(t)

	createOpenProfile(t, fx, "base",
		"schemaVersion: 1\nvalues:\n  x: 1\nchecks:\n  - rule: values.x == 1\n")
	createOpenProfile(t, fx, "root", "schemaVersion: 1\nimports: [base]\nvalues:\n  y: 2\n")

	_, err := openRoot(t, fx)
	if !errors.Is(err, profile.ErrInvalid) {
		t.Fatalf("open error = %v, want ErrInvalid", err)
	}

	if !strings.Contains(err.Error(), "base") {
		t.Errorf("open error = %v, want the import", err)
	}
}

func TestOpenRefusalsUnknownConsumer(t *testing.T) {
	t.Parallel()

	fx := newOpenFixture(t)

	createOpenProfile(t, fx, "root", "schemaVersion: 1\nvalues:\n  x: 1\n")

	for _, consumer := range []string{"run", "opentofu", "launcher", "observe", "other", ""} {
		_, err := openAs(
			t,
			fx,
			"admin",
			openSpec{ref: profile.RefByName("root"), paths: nil, consumer: profile.OpenConsumer(consumer), duration: 0},
		)
		if !errors.Is(err, profile.ErrInvalid) {
			t.Errorf("consumer %q: open error = %v, want ErrInvalid", consumer, err)
		}
	}

	for _, consumer := range []profile.OpenConsumer{profile.OpenConsumerAPI, profile.OpenConsumerConsole, profile.OpenConsumerPulumi} {
		_, err := openAs(t, fx, "admin", openSpec{
			ref: profile.RefByName("root"), paths: nil, consumer: consumer, duration: 0,
		})
		if err != nil {
			t.Errorf("consumer %q: open error = %v", consumer, err)
		}
	}
}

func TestOpenRefusalsDurationOutsideTheWindow(t *testing.T) {
	t.Parallel()

	fx := newOpenFixture(t)

	createOpenProfile(t, fx, "root", "schemaVersion: 1\nvalues:\n  x: 1\n")

	for _, d := range []time.Duration{59 * time.Second, 12*time.Hour + time.Second} {
		_, err := openAs(
			t,
			fx,
			"admin",
			openSpec{ref: profile.RefByName("root"), paths: nil, consumer: profile.OpenConsumerAPI, duration: d},
		)
		if !errors.Is(err, profile.ErrInvalid) {
			t.Errorf("duration %s: open error = %v, want ErrInvalid", d, err)
		}
	}
}

func TestOpenRefusalsDefaultDurationIs2Hours(t *testing.T) {
	t.Parallel()

	fx := newOpenFixture(t)

	createOpenProfile(t, fx, "root", "schemaVersion: 1\nvalues:\n  x: 1\n")

	before := time.Now()
	opened, err := openRoot(t, fx)
	if err != nil {
		t.Fatalf("open: %v", err)
	}

	if want := before.Add(2 * time.Hour); opened.ExpiresAt.Before(want.Add(-time.Minute)) ||
		opened.ExpiresAt.After(want.Add(time.Minute)) {
		t.Errorf("expiry = %s, want about %s", opened.ExpiresAt, want)
	}

	if !strings.HasPrefix(opened.SessionID, profile.SessionIDPrefix) {
		t.Errorf("session ID = %q, want the pos- prefix", opened.SessionID)
	}
}

func TestOpenRefusalsBadPaths(t *testing.T) {
	t.Parallel()

	fx := newOpenFixture(t)

	createOpenProfile(t, fx, "root", "schemaVersion: 1\nvalues:\n  x: 1\n")

	many := make([]string, profile.MaxSessionPaths+1)
	for i := range many {
		many[i] = "x"
	}

	for name, paths := range map[string][]string{
		"1025 items":   many,
		"empty path":   {""},
		"CR in path":   {"a\rb"},
		"LF in path":   {"a\nb"},
		"NUL in path":  {"a\x00b"},
		"DEL in path":  {"a\x7fb"},
		"C1 in path":   {"a\u0085b"},
		"bad UTF-8":    {"a\xffb"},
		"unknown path": {"absent"},
	} {
		_, err := openAs(
			t,
			fx,
			"admin",
			openSpec{ref: profile.RefByName("root"), paths: paths, consumer: profile.OpenConsumerAPI, duration: 0},
		)
		if !errors.Is(err, profile.ErrInvalid) {
			t.Errorf("%s: open error = %v, want ErrInvalid", name, err)
		}
	}
}

func TestOpenRefusalsUnknownAndDeletedProfiles(t *testing.T) {
	t.Parallel()

	fx := newOpenFixture(t)

	createOpenProfile(t, fx, "root", "schemaVersion: 1\nvalues:\n  x: 1\n")

	_, err := openAs(
		t,
		fx,
		"admin",
		openSpec{ref: profile.RefByName("absent"), paths: nil, consumer: profile.OpenConsumerAPI, duration: 0},
	)
	if !errors.Is(err, profile.ErrNotFound) {
		t.Errorf("unknown profile: open error = %v, want ErrNotFound", err)
	}

	got, err := fx.svc.Get(
		t.Context(),
		openActor("admin"),
		profile.Target{Namespace: fx.ns, Ref: profile.RefByName("root")},
	)
	if err != nil {
		t.Fatalf("get root: %v", err)
	}

	err = fx.svc.Delete(
		t.Context(),
		openActor("admin"),
		profile.Target{Namespace: fx.ns, Ref: profile.RefByID(got.ID)},
		profile.Removal{IfLatest: 0, Reason: ""},
	)
	if err != nil {
		t.Fatalf("delete root: %v", err)
	}

	_, err = openRoot(t, fx)
	if !errors.Is(err, profile.ErrNotFound) {
		t.Errorf("deleted profile: open error = %v, want ErrNotFound", err)
	}
}

func TestOpenDenialsNoGrantWritesOneDeniedEntryAndStoresNothing(t *testing.T) {
	t.Parallel()

	fx := newOpenFixture(t)

	createOpenProfile(t, fx, "root", docWithSecret())

	before := accessEntries(fx, fx.ns)

	_, err := openAs(
		t,
		fx,
		"reader",
		openSpec{ref: profile.RefByName("root"), paths: nil, consumer: profile.OpenConsumerAPI, duration: 0},
	)
	if !errors.Is(err, identity.ErrDenied) {
		t.Fatalf("open error = %v, want a denial", err)
	}

	denied := accessEntries(fx, fx.ns)[len(before):]
	if len(denied) != 1 {
		t.Fatalf("denied entries = %d, want 1", len(denied))
	}

	if denied[0].Outcome != audit.OutcomeDenied || denied[0].Kind != audit.KindAccess {
		t.Errorf("denied entry = %+v, want kind access with outcome denied", denied[0])
	}

	if text := entryText(t, denied); strings.Contains(text, openSecret) {
		t.Error("denied entry holds the secret")
	}

	if n := len(fx.m.sessions); n != 0 {
		t.Errorf("sessions = %d, want 0: a denied open stores nothing", n)
	}
}

func TestOpenDenialsJobTokenIsRefused(t *testing.T) {
	t.Parallel()

	fx := newOpenFixture(t)

	createOpenProfile(t, fx, "root", docWithSecret())

	before := accessEntries(fx, fx.ns)

	_, err := fx.svc.Open(t.Context(), openJobActor("admin"), profile.OpenInput{
		Namespace: fx.ns, Ref: profile.RefByName("root"), Paths: nil, Consumer: profile.OpenConsumerAPI,
		Duration: 0,
	})
	if !errors.Is(err, identity.ErrDenied) {
		t.Fatalf("open error = %v, want a denial", err)
	}

	denied := accessEntries(fx, fx.ns)[len(before):]
	if len(denied) != 1 || denied[0].Outcome != audit.OutcomeDenied {
		t.Fatalf("denied entries = %+v, want one denied entry", denied)
	}

	if n := len(fx.m.sessions); n != 0 {
		t.Errorf("sessions = %d, want 0", n)
	}
}

func TestOpenDenialsPersonalTokenNeedsNoFreshSignIn(t *testing.T) {
	t.Parallel()

	fx := newOpenFixture(t)

	createOpenProfile(t, fx, "root", docWithSecret())

	opened, err := openRoot(t, fx)
	if err != nil {
		t.Fatalf("open with a personal token and no fresh sign-in: %v", err)
	}

	if opened.SessionID == "" {
		t.Error("open answers no session ID")
	}
}

func TestOpenFaults(t *testing.T) {
	t.Parallel()

	t.Run("sealer fault stores nothing and names no value", func(t *testing.T) {
		t.Parallel()

		fx := newOpenFixture(t)

		createOpenProfile(t, fx, "root", docWithSecret())

		fx.svc = withSealer(t, fx, faultSealer{err: errSealFault})

		_, err := openRoot(t, fx)
		if !errors.Is(err, errSealFault) {
			t.Fatalf("open error = %v, want the sealer fault", err)
		}

		if strings.Contains(err.Error(), openSecret) {
			t.Errorf("open error holds the secret: %v", err)
		}

		if n := len(fx.m.sessions); n != 0 {
			t.Errorf("sessions = %d, want 0", n)
		}
	})

	t.Run("session store fault names no value", func(t *testing.T) {
		t.Parallel()

		fx := newOpenFixture(t)
		fx.svc = withSessions(t, fx, faultSessions{Sessions: memSessions{m: fx.m}, putErr: errPutFault, delErr: nil})

		createOpenProfile(t, fx, "root", docWithSecret())

		_, err := openRoot(t, fx)
		if !errors.Is(err, errPutFault) {
			t.Fatalf("open error = %v, want the store fault", err)
		}

		if strings.Contains(err.Error(), openSecret) {
			t.Errorf("open error holds the secret: %v", err)
		}
	})
}

// errSealFault and errPutFault are the faults that the fakes answer.
var (
	errSealFault = errors.New("sealer is down")
	errPutFault  = errors.New("session store is down")
	errDelFault  = errors.New("session delete is down")
)

// faultSealer fails Encrypt and Decrypt with err.
type faultSealer struct{ err error }

func (f faultSealer) Encrypt(context.Context, secret.Scope, [][]byte) ([][]byte, error) {
	return nil, f.err
}

func (f faultSealer) Decrypt(context.Context, secret.Scope, [][]byte) ([][]byte, error) {
	return nil, f.err
}

// faultSessions fails Put and Delete with its errors.
type faultSessions struct {
	profile.Sessions

	putErr error
	delErr error
}

func (f faultSessions) Put(
	ctx context.Context, s profile.Session, fence namespace.Fence, e []audit.Entry,
) error {
	if f.putErr != nil {
		return f.putErr
	}

	return f.Sessions.Put(ctx, s, fence, e)
}

func (f faultSessions) Delete(
	ctx context.Context, id string, owner profile.SessionOwner, fence namespace.Fence, e []audit.Entry,
) error {
	if f.delErr != nil {
		return f.delErr
	}

	return f.Sessions.Delete(ctx, id, owner, fence, e)
}

// withSealer rebuilds the service of fx with sealer.
func withSealer(t *testing.T, fx openFixture, sealer profile.Sealer) *profile.Service {
	t.Helper()

	eval, err := expr.New(expr.NewLimiter())
	if err != nil {
		t.Fatalf("expr evaluator: %v", err)
	}

	svc, err := profile.NewService(profile.Config{
		Store: fx.m, Namespaces: memNamespaces{m: fx.m}, Sealer: sealer, Audit: memAudit{m: fx.m},
		Authorizer: grantsFunc(fx.grants), Sessions: memSessions{m: fx.m}, Evaluator: eval, Now: nil,
	})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}

	return svc
}

// withSessions rebuilds the service of fx with sessions.
func withSessions(t *testing.T, fx openFixture, sessions profile.Sessions) *profile.Service {
	t.Helper()

	eval, err := expr.New(expr.NewLimiter())
	if err != nil {
		t.Fatalf("expr evaluator: %v", err)
	}

	svc, err := profile.NewService(profile.Config{
		Store: fx.m, Namespaces: memNamespaces{m: fx.m}, Sealer: scopedSealer{}, Audit: memAudit{m: fx.m},
		Authorizer: grantsFunc(fx.grants), Sessions: sessions, Evaluator: eval, Now: nil,
	})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}

	return svc
}

func TestReadSessionReadAnswersTheValuesWithTheSecrets(t *testing.T) {
	t.Parallel()

	fx := newOpenFixture(t)

	createOpenProfile(t, fx, "root", docWithSecret())

	opened, data := openRef(t, fx)

	if data.Values["region"] != "eu-west-1" || data.Values["db.password"] != openSecret {
		t.Errorf("values = %v", data.Values)
	}

	if !data.Secrets["db.password"] || data.Secrets["region"] {
		t.Errorf("secrets = %v", data.Secrets)
	}

	if opened.SessionID == "" || opened.ExpiresAt.IsZero() {
		t.Errorf("opened = %+v, want an ID and an end", opened)
	}
}

func TestReadSessionReadOnePath(t *testing.T) {
	t.Parallel()

	fx := newOpenFixture(t)

	createOpenProfile(t, fx, "root", docWithSecret())

	opened, err := openRoot(t, fx)
	if err != nil {
		t.Fatalf("open: %v", err)
	}

	data, err := fx.svc.ReadSession(t.Context(), openActor("admin"), fx.ns, opened.SessionID,
		[]string{"region"})
	if err != nil {
		t.Fatalf("read one path: %v", err)
	}

	if len(data.Values) != 1 || data.Values["region"] != "eu-west-1" {
		t.Errorf("values = %v, want only region", data.Values)
	}

	_, err = fx.svc.ReadSession(t.Context(), openActor("admin"), fx.ns, opened.SessionID,
		[]string{"absent"})
	if !errors.Is(err, profile.ErrNotFound) {
		t.Errorf("unknown path: read error = %v, want ErrNotFound", err)
	}
}

// getCounter counts the reads of a Sessions store.
type getCounter struct {
	profile.Sessions

	gets atomic.Int64
}

func (c *getCounter) Get(
	ctx context.Context, ns namespace.Name, id string, owner profile.SessionOwner, now time.Time,
) (profile.Session, error) {
	c.gets.Add(1)

	return c.Sessions.Get(ctx, ns, id, owner, now)
}

// TestReadSessionHostileIDsNeverReachTheStore fails when the ID check of
// ReadSession goes: the store reads a bad ID as not found too, so only a count
// of its reads shows the layer.
func TestReadSessionHostileIDsNeverReachTheStore(t *testing.T) {
	t.Parallel()

	fx := newOpenFixture(t)
	counter := &getCounter{Sessions: memSessions{m: fx.m}, gets: atomic.Int64{}}
	svc := withSessions(t, fx, counter)

	for _, id := range []string{"", "pos-", "ses-0001", "pos-%2F", "pos-../x", "pos-A", "pos-a\nb", "pos-a\x00"} {
		_, err := svc.ReadSession(t.Context(), openActor("admin"), fx.ns, id, nil)
		if !errors.Is(err, profile.ErrNotFound) {
			t.Errorf("id %q: read error = %v, want ErrNotFound", id, err)
		}
	}

	if n := counter.gets.Load(); n != 0 {
		t.Errorf("store reads = %d, want 0", n)
	}
}

// TestReadSessionJobTokenReadsNothing fails when the job token check of
// ReadSession goes: the job token holds the grant here, so only the check
// refuses it.
func TestReadSessionJobTokenReadsNothing(t *testing.T) {
	t.Parallel()

	fx := newOpenFixture(t)

	createOpenProfile(t, fx, "root", docWithSecret())

	opened, err := openRoot(t, fx)
	if err != nil {
		t.Fatalf("open: %v", err)
	}

	data, err := fx.svc.ReadSession(t.Context(), openJobActor("admin"), fx.ns, opened.SessionID, nil)
	if !errors.Is(err, identity.ErrDenied) {
		t.Fatalf("read error = %v, want a denial", err)
	}

	if len(data.Values) != 0 || len(data.Secrets) != 0 {
		t.Errorf("a refused read answered %v", data)
	}
}

// TestOpenRefusesBeforeAnyRead fails when the consumer check or the path count
// check of the resolver goes. The session constructor checks both again, but
// it runs after the reads and writes a failure entry, so a refusal of the
// resolver leaves no entry.
func TestOpenRefusesBeforeAnyRead(t *testing.T) {
	t.Parallel()

	fx := newOpenFixture(t)

	createOpenProfile(t, fx, "root", "schemaVersion: 1\nvalues:\n  x: 1\n")

	many := make([]string, profile.MaxSessionPaths+1)
	for i := range many {
		many[i] = "x"
	}

	for name, spec := range map[string]openSpec{
		"unknown consumer": {ref: profile.RefByName("root"), paths: nil, consumer: "other", duration: 0},
		"too many paths":   {ref: profile.RefByName("root"), paths: many, consumer: profile.OpenConsumerAPI, duration: 0},
	} {
		before := len(accessEntries(fx, fx.ns))

		if _, err := openAs(t, fx, "admin", spec); !errors.Is(err, profile.ErrInvalid) {
			t.Errorf("%s: open error = %v, want ErrInvalid", name, err)
		}

		if after := len(accessEntries(fx, fx.ns)); after != before {
			t.Errorf("%s: %d new entries, want 0", name, after-before)
		}
	}
}

// TestReadSessionHostilePaths sends hostile paths straight to the service,
// as if the handler did not check them. Each row fails when the path check of
// ReadSession goes.
func TestReadSessionHostilePaths(t *testing.T) {
	t.Parallel()

	fx := newOpenFixture(t)

	createOpenProfile(t, fx, "root", docWithSecret())

	opened, err := openRoot(t, fx)
	if err != nil {
		t.Fatalf("open: %v", err)
	}

	for name, path := range map[string]string{
		"empty":         "",
		"CR":            "a\rb",
		"LF":            "a\nb",
		"NUL":           "a\x00b",
		"DEL":           "a\x7fb",
		"C1":            "a\u0085b",
		"bad UTF-8":     "a\xffb",
		"empty segment": "a..b",
		"bad escape":    "a\\b",
	} {
		_, err = fx.svc.ReadSession(t.Context(), openActor("admin"), fx.ns, opened.SessionID, []string{path})
		if !errors.Is(err, profile.ErrInvalid) {
			t.Errorf("%s: read error = %v, want ErrInvalid", name, err)
		}
	}
}

func TestReadSessionReadByAnotherSubjectIsNotFoundAndKeepsTheSession(t *testing.T) {
	t.Parallel()

	fx := newOpenFixture(t)

	createOpenProfile(t, fx, "root", docWithSecret())

	fx.grants["reader"] = []identity.Action{open}

	opened, err := openRoot(t, fx)
	if err != nil {
		t.Fatalf("open: %v", err)
	}

	_, err = fx.svc.ReadSession(t.Context(), openActor("reader"), fx.ns, opened.SessionID, nil)
	if !errors.Is(err, profile.ErrNotFound) {
		t.Fatalf("read error = %v, want ErrNotFound", err)
	}

	_, err = fx.svc.ReadSession(
		t.Context(),
		openActor("admin"),
		fx.ns,
		opened.SessionID,
		nil,
	)
	if err != nil {
		t.Errorf("read by the owner after a refused read: %v", err)
	}
}

func TestReadSessionReadAfterRevokeIsDeniedAndDeletesTheSession(t *testing.T) {
	t.Parallel()

	fx := newOpenFixture(t)

	createOpenProfile(t, fx, "root", docWithSecret())

	opened, err := openRoot(t, fx)
	if err != nil {
		t.Fatalf("open: %v", err)
	}

	fx.grants["admin"] = []identity.Action{read}

	before := accessEntries(fx, fx.ns)

	_, err = fx.svc.ReadSession(t.Context(), openActor("admin"), fx.ns, opened.SessionID, nil)
	if !errors.Is(err, identity.ErrDenied) {
		t.Fatalf("read error = %v, want a denial", err)
	}

	if got := accessEntries(fx, fx.ns)[len(before):]; len(got) != 1 || got[0].Outcome != audit.OutcomeDenied {
		t.Errorf("denied entries = %+v, want one denied entry", got)
	}

	_, err = fx.svc.ReadSession(t.Context(), openActor("admin"), fx.ns, opened.SessionID, nil)
	if !errors.Is(err, identity.ErrDenied) {
		t.Errorf("second read error = %v, want the denial again, not a leak", err)
	}
}

func TestReadSessionDeleteFaultAfterADenialKeepsTheDenialAndNamesNoValue(t *testing.T) {
	t.Parallel()

	fx := newOpenFixture(t)

	createOpenProfile(t, fx, "root", docWithSecret())

	opened, err := openRoot(t, fx)
	if err != nil {
		t.Fatalf("open: %v", err)
	}

	fx.grants["admin"] = []identity.Action{read}
	fx.svc = withSessions(t, fx, faultSessions{
		Sessions: memSessions{m: fx.m}, putErr: nil, delErr: errDelFault,
	})

	_, err = fx.svc.ReadSession(t.Context(), openActor("admin"), fx.ns, opened.SessionID, nil)
	if !errors.Is(err, identity.ErrDenied) {
		t.Fatalf("read error = %v, want a denial", err)
	}

	if strings.Contains(err.Error(), openSecret) {
		t.Errorf("read error holds the secret: %v", err)
	}
}

func TestReadSessionEndedAndForgedSessionsAreNotFound(t *testing.T) {
	t.Parallel()

	fx := newOpenFixture(t)

	createOpenProfile(t, fx, "root", docWithSecret())

	opened, err := openRoot(t, fx)
	if err != nil {
		t.Fatalf("open: %v", err)
	}

	_, err = fx.svc.ReadSession(
		t.Context(),
		openActor("admin"),
		fx.ns,
		opened.SessionID,
		nil,
	)
	if err != nil {
		t.Fatalf("read live session: %v", err)
	}

	// An ended session reads as not found: the store below holds one
	// that ended an hour ago.
	got, err := fx.m.Get(t.Context(), fx.ns, profile.RefByName("root"))
	if err != nil {
		t.Fatalf("get root: %v", err)
	}

	full, err := memNamespaces{m: fx.m}.GetNamespace(t.Context(), fx.ns)
	if err != nil {
		t.Fatalf("get namespace: %v", err)
	}

	at := time.Now().UTC()
	entry := audit.Entry{
		At: at, Namespace: fx.ns, Subject: "admin", IDPSubject: "", TokenID: "", Action: "profile:open",
		Object: "namespace:" + fx.ns.String(), Version: "", ValuePath: "", ValueCount: nil, Changes: nil,
		SourceIP: "", UserAgent: "", Channel: audit.ChannelAPI, Kind: audit.KindAccess,
		Outcome: audit.OutcomeSuccess, RequestID: "", ObjectID: "", PrevHash: "", Hash: "", Sequence: 0,
		FenceVersion: 0, PrincipalKind: "", JobID: "", Reason: "", Origin: nil,
	}
	ended, err := profile.NewSession(profile.SessionParams{
		ID: "pos-ended00000000000001", Namespace: fx.ns, Root: got.ID,
		Owner: profile.PrincipalOwner("admin"), Consumer: profile.OpenConsumerAPI,
		Paths: nil, Sealed: []byte("sealed"), CreatedAt: at.Add(-2 * time.Hour),
		ExpiresAt: at.Add(-time.Hour),
	})
	if err != nil {
		t.Fatalf("make ended session: %v", err)
	}

	if err = (memSessions{m: fx.m}).Put(t.Context(), ended, full.Fence(), []audit.Entry{entry}); err != nil {
		t.Fatalf("put ended session: %v", err)
	}

	for _, id := range []string{"", "pos-", "ses-0001", "pos-%2F"} {
		_, err = fx.svc.ReadSession(t.Context(), openActor("admin"), fx.ns, id, nil)
		if !errors.Is(err, profile.ErrNotFound) {
			t.Errorf("id %q: read error = %v, want ErrNotFound", id, err)
		}
	}
}

func TestReadSessionDecryptFaultNamesNoValue(t *testing.T) {
	t.Parallel()

	fx := newOpenFixture(t)

	createOpenProfile(t, fx, "root", docWithSecret())

	opened, err := openRoot(t, fx)
	if err != nil {
		t.Fatalf("open: %v", err)
	}

	fx.svc = withSealer(t, fx, faultSealer{err: errSealFault})

	_, err = fx.svc.ReadSession(t.Context(), openActor("admin"), fx.ns, opened.SessionID, nil)
	if err == nil {
		t.Fatal("read error = nil, want the decrypt fault")
	}

	if strings.Contains(err.Error(), openSecret) {
		t.Errorf("read error holds the secret: %v", err)
	}
}

func TestReadSessionSuccessWritesAnAccessEntryWithNoValue(t *testing.T) {
	t.Parallel()

	fx := newOpenFixture(t)

	createOpenProfile(t, fx, "root", docWithSecret())

	before := accessEntries(fx, fx.ns)

	opened, err := openRoot(t, fx)
	if err != nil {
		t.Fatalf("open: %v", err)
	}

	got := accessEntries(fx, fx.ns)[len(before):]
	if len(got) != 1 {
		t.Fatalf("access entries = %d, want 1", len(got))
	}

	if got[0].Outcome != audit.OutcomeSuccess || got[0].Action != string(identity.ActionProfileOpen) {
		t.Errorf("access entry = %+v", got[0])
	}

	if text := entryText(t, got); strings.Contains(text, openSecret) {
		t.Error("access entry holds the secret")
	}

	// A read writes no entry.
	_, err = fx.svc.ReadSession(
		t.Context(),
		openActor("admin"),
		fx.ns,
		opened.SessionID,
		nil,
	)
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	if n := len(accessEntries(fx, fx.ns)) - len(before); n != 1 {
		t.Errorf("access entries after a read = %d, want 1: a read writes none", n)
	}
}
