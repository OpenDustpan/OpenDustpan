package profiletest_test

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/profile"
	"opendustpan.org/opendustpan/pkg/secret"
)

// scopedSealer binds each ciphertext to its scope, as the real sealer does.
type scopedSealer struct{}

func (scopedSealer) Encrypt(_ context.Context, sc secret.Scope, in [][]byte) ([][]byte, error) {
	out := make([][]byte, len(in))
	for i, p := range in {
		out[i] = []byte(sc.String() + "|" + string(p))
	}

	return out, nil
}

func (scopedSealer) Decrypt(_ context.Context, sc secret.Scope, in [][]byte) ([][]byte, error) {
	out := make([][]byte, len(in))

	for i, c := range in {
		plain, ok := strings.CutPrefix(string(c), sc.String()+"|")
		if !ok {
			return nil, secret.ErrDecrypt
		}

		out[i] = []byte(plain)
	}

	return out, nil
}

// grants allows each subject the listed actions.
type grants map[string][]identity.Action

func (g grants) Authorize(_ context.Context, p identity.Principal, a identity.Action, o identity.Object) error {
	if slices.Contains(g[p.Subject], a) {
		return nil
	}

	return fmt.Errorf("%w: %s on %s", identity.ErrDenied, a, o)
}

func (grants) ListObjects(context.Context, identity.Principal, identity.Action) (identity.Scope, error) {
	return identity.Scope{Namespaces: nil, Collections: nil, Units: nil, Principals: nil}, nil
}

const (
	read   = identity.ActionProfileRead
	write  = identity.ActionProfileWrite
	open   = identity.ActionProfileOpen
	manage = identity.ActionProfileManage
)

type fixture struct {
	svc *profile.Service
	m   *mem
	ns  namespace.Name
}

func newFixture(t *testing.T) fixture {
	t.Helper()

	m := newMem()
	m.namespaces["acme"] = namespace.Namespace{
		ID: "", CreatedAt: epochTime(), Name: "acme", PrimaryInstallation: "", FailoverVersion: 1,
		Overrides: namespace.Overrides{Forced: false, Unlocked: false},
	}

	svc, err := profile.NewService(profile.Config{
		Store: m, Namespaces: memNamespaces{m: m}, Sealer: scopedSealer{}, Audit: memAudit{m: m},
		Authorizer: grants{
			"admin":      {read, write, open, manage},
			"writer":     {read, write},
			"reader":     {read},
			"nobody":     nil,
			"manager":    {read, manage},
			"writeonly":  {write},
			"manageonly": {manage},
		},
		Now: nil,
	})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}

	return fixture{svc: svc, m: m, ns: "acme"}
}

func actor(subject string) *profile.Actor {
	return &profile.Actor{
		Principal: identity.Principal{
			Subject: subject, DisplayName: "", Admin: false, TokenID: "", TokenExpiresAt: epochTime(),
			TemporaryAdministrator: false, TemporaryExpiresAt: epochTime(), Kind: "", Credential: "", JobID: "",
		},
		By: auditTemplate(),
	}
}

// auditTemplate is the audit entry that a caller hands to the service.
func auditTemplate() audit.Entry {
	return audit.Entry{
		At: time.Time{}, Namespace: "", Subject: "", IDPSubject: "", TokenID: "", Action: "", Object: "",
		Version: "", ValuePath: "", ValueCount: nil, Changes: nil, SourceIP: "", UserAgent: "",
		Channel: audit.ChannelAPI, Kind: audit.KindChange, Outcome: audit.OutcomeSuccess, RequestID: "",
		ObjectID: "", PrevHash: "", Hash: "", Sequence: 0, FenceVersion: 0, Reason: "", PrincipalKind: "", JobID: "",
	}
}

//nolint:gosec // A document for a test, not a credential.
const secretDoc = "schemaVersion: 1\nvalues:\n  region: eu-west-1\n  db:\n    password: { $secret: hunter2 }\n"

func (f fixture) target(ref profile.Ref) profile.Target {
	return profile.Target{Namespace: f.ns, Ref: ref}
}

func newProfileInput(name, doc, reason string) profile.NewProfile {
	return profile.NewProfile{Name: profile.Name(name), Document: []byte(doc), Reason: reason}
}

func (f fixture) create(t *testing.T) profile.Profile {
	t.Helper()

	p, _, err := f.svc.Create(t.Context(), actor("writer"), f.ns, newProfileInput("aws-prod", secretDoc, ""))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	return p
}

// readOf returns the options of a read. A reveal passes its fresh
// authentication check.
func readOf(reveal bool) profile.ReadOptions {
	return profile.ReadOptions{Fresh: func() error { return nil }, Revision: 0, Reveal: reveal}
}

// errNotFresh is the refusal of the fresh authentication check of a test.
var errNotFresh = errors.New("not fresh")

// TestRevealNeedsAFreshAuthentication proves a reveal with a refused check, or
// with no check, answers the refusal and no value, and writes one entry of the
// kind reveal with the outcome failure.
func TestRevealNeedsAFreshAuthentication(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct {
		fresh func() error
		want  error
	}{
		"a check that refuses": {fresh: func() error { return errNotFresh }, want: errNotFresh},
		"no check":             {fresh: nil, want: profile.ErrFreshRequired},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			f := newFixture(t)
			p := f.create(t)
			target := f.target(profile.RefByID(p.ID))
			before := len(f.m.entries[f.ns])

			v, err := f.svc.Read(t.Context(), actor("admin"), target,
				profile.ReadOptions{Fresh: tc.fresh, Revision: 0, Reveal: true})
			if !errors.Is(err, tc.want) {
				t.Fatalf("reveal: error = %v, want %v", err, tc.want)
			}

			if len(v.Secrets) != 0 {
				t.Errorf("secrets left with a refused reveal: %v", v.Secrets)
			}

			got := f.m.entries[f.ns][before:]
			if len(got) != 1 || got[0].Kind != audit.KindReveal || got[0].Outcome != audit.OutcomeFailure ||
				got[0].Action != "profile:open" || got[0].Subject != "admin" {
				t.Fatalf("entries of the refused reveal = %+v, want one failure of the kind reveal", got)
			}
		})
	}
}

func TestCreateSealsSecrets(t *testing.T) {
	t.Parallel()

	f := newFixture(t)

	p, r, err := f.svc.Create(t.Context(), actor("writer"), f.ns, newProfileInput("aws-prod", secretDoc, "first"))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if strings.Contains(string(r.Document), "hunter2") || !strings.Contains(string(r.Document), "$sealed") {
		t.Errorf("stored document = %s", r.Document)
	}

	stored, err := f.m.Revision(t.Context(), f.ns, p.ID, profile.At{Number: 1})
	if err != nil {
		t.Fatalf("Revision: %v", err)
	}

	if strings.Contains(string(stored.Document), "hunter2") {
		t.Error("the store holds the plain text")
	}

	for _, e := range f.m.entries[f.ns] {
		if strings.Contains(fmt.Sprintf("%+v", e), "hunter2") {
			t.Errorf("audit entry holds the plain text: %+v", e)
		}
	}

	written := f.m.entries[f.ns]
	if len(written) == 0 {
		t.Fatal("no audit entry")
	}

	if e := written[0]; e.Action != "profile:write" || e.ObjectID != string(p.ID) || e.Reason != "first" {
		t.Errorf("audit entry = %+v", e)
	}
}

func TestReadMasksAndRevealNeedsOpen(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	p := f.create(t)
	target := f.target(profile.RefByID(p.ID))

	v, err := f.svc.Read(t.Context(), actor("reader"), target, readOf(false))
	if err != nil {
		t.Fatalf("Read: %v", err)
	}

	if v.Secrets != nil {
		t.Errorf("a plain read holds secrets: %v", v.Secrets)
	}

	for _, e := range v.Entries {
		if e.Secret && e.Value != nil {
			t.Errorf("secret entry %s holds a value", e.Path)
		}
	}
}

func TestRevealNeedsOpenAndIsAudited(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	p := f.create(t)
	target := f.target(profile.RefByID(p.ID))

	_, err := f.svc.Read(t.Context(), actor("reader"), target, readOf(true))
	if !errors.Is(err, identity.ErrDenied) {
		t.Fatalf("reveal without profile:open: error = %v, want ErrDenied", err)
	}

	checkDeniedReveal(t, f.m.entries[f.ns])

	before := len(f.m.entries[f.ns])

	v, err := f.svc.Read(t.Context(), actor("admin"), target, readOf(true))
	if err != nil || v.Secrets["db.password"] != "hunter2" {
		t.Fatalf("reveal = %+v, %v", v.Secrets, err)
	}

	all := f.m.entries[f.ns]
	if len(all) < before {
		t.Fatalf("audit entries = %d, want at least %d", len(all), before)
	}

	got := all[before:]
	if len(got) != 1 || got[0].Kind != audit.KindReveal || got[0].ValueCount == nil || *got[0].ValueCount != 1 {
		t.Fatalf("reveal audit entries = %+v", got)
	}
}

// checkDeniedReveal fails the test unless the last entry is the audit entry
// of a reveal that profile:open refused.
func checkDeniedReveal(t *testing.T, all []audit.Entry) {
	t.Helper()

	if len(all) == 0 {
		t.Fatal("no audit entry for the denied reveal")
	}

	last := all[len(all)-1]
	if last.Kind != audit.KindReveal || last.Outcome != audit.OutcomeDenied || last.Subject != "reader" ||
		last.Action != "profile:open" {
		t.Fatalf("audit entry of the denied reveal = %+v", last)
	}
}

func TestRevealFailsClosedWhenAuditFails(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	p := f.create(t)
	target := f.target(profile.RefByID(p.ID))

	f.m.failAppend = true

	v, err := f.svc.Read(t.Context(), actor("admin"), target, readOf(true))
	if !errors.Is(err, errAppend) {
		t.Fatalf("reveal with a failing audit store: error = %v, want errAppend", err)
	}

	if len(v.Secrets) != 0 {
		t.Errorf("secrets left with a failed audit entry: %v", v.Secrets)
	}
}

func TestAuthorization(t *testing.T) {
	t.Parallel()

	minimal := []byte("schemaVersion: 1\n")
	list := profile.Filter{Namespace: "acme", Folder: "", After: "", Limit: 5}

	tests := []struct {
		name string
		who  string
		call func(f fixture, a *profile.Actor, target profile.Target) error
		want bool
	}{
		{"get needs read", "nobody", func(f fixture, a *profile.Actor, target profile.Target) error {
			_, err := f.svc.Get(t.Context(), a, target)

			return err
		}, false},
		{"get with read", "reader", func(f fixture, a *profile.Actor, target profile.Target) error {
			_, err := f.svc.Get(t.Context(), a, target)

			return err
		}, true},
		{"list needs read", "nobody", func(f fixture, a *profile.Actor, _ profile.Target) error {
			_, err := f.svc.List(t.Context(), a, list)

			return err
		}, false},
		{"create needs write", "reader", func(f fixture, a *profile.Actor, _ profile.Target) error {
			_, _, err := f.svc.Create(t.Context(), a, f.ns, newProfileInput("x", "schemaVersion: 1\n", ""))

			return err
		}, false},
		{"save needs write", "reader", func(f fixture, a *profile.Actor, target profile.Target) error {
			_, _, err := f.svc.Save(t.Context(), a, target, profile.Change{Base: 1, Document: minimal, Reason: ""})

			return err
		}, false},
		{"save with write only", "writeonly", func(f fixture, a *profile.Actor, target profile.Target) error {
			p, err := f.svc.Locate(t.Context(), a, target, write)
			if err != nil {
				return err
			}

			_, _, err = f.svc.Save(
				t.Context(),
				a,
				target,
				profile.Change{Base: p.Latest, Document: minimal, Reason: ""},
			)

			return err
		}, true},
		{"locate for write needs write", "reader", func(f fixture, a *profile.Actor, target profile.Target) error {
			_, err := f.svc.Locate(t.Context(), a, target, write)

			return err
		}, false},
		{"delete with manage only", "manageonly", func(f fixture, a *profile.Actor, target profile.Target) error {
			if _, err := f.svc.Locate(t.Context(), a, target, manage); err != nil {
				return err
			}

			return f.svc.Delete(t.Context(), a, target, profile.Removal{IfLatest: 0, Reason: ""})
		}, true},
		{"delete needs manage", "writer", func(f fixture, a *profile.Actor, target profile.Target) error {
			return f.svc.Delete(t.Context(), a, target, profile.Removal{IfLatest: 0, Reason: ""})
		}, false},
		{"delete with manage", "manager", func(f fixture, a *profile.Actor, target profile.Target) error {
			return f.svc.Delete(t.Context(), a, target, profile.Removal{IfLatest: 0, Reason: ""})
		}, true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f := newFixture(t)
			p := f.create(t)

			err := tc.call(f, actor(tc.who), f.target(profile.RefByID(p.ID)))
			if tc.want && err != nil || !tc.want && !errors.Is(err, identity.ErrDenied) {
				t.Errorf("error = %v, want allowed %v", err, tc.want)
			}
		})
	}
}

func TestRoutingChangeNeedsOpen(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	block := "schemaVersion: 1\nproviders:\n  v: { use: login/vault, with: { address: \"${ values.host }\" } }\n" +
		"values:\n  host: https://a\n  other: 1\n"

	_, _, err := f.svc.Create(t.Context(), actor("writer"), f.ns, newProfileInput("blocked", block, ""))

	var routing *profile.RoutingError
	if !errors.As(err, &routing) || !errors.Is(err, identity.ErrDenied) ||
		!strings.Contains(err.Error(), "providers.v") {
		t.Fatalf("create with a provider block without open: error = %v", err)
	}

	p, _, err := f.svc.Create(t.Context(), actor("admin"), f.ns, newProfileInput("routed", block, ""))
	if err != nil {
		t.Fatalf("create with open: %v", err)
	}

	target := f.target(profile.RefByID(p.ID))
	plain := profile.Change{Base: 1, Document: []byte(strings.Replace(block, "other: 1", "other: 2", 1)), Reason: ""}

	if _, _, err = f.svc.Save(t.Context(), actor("writer"), target, plain); err != nil {
		t.Fatalf("save of a plain change without open: %v", err)
	}

	moved := strings.Replace(string(plain.Document), "host: https://a", "host: https://evil", 1)
	_, _, err = f.svc.Save(
		t.Context(),
		actor("writer"),
		target,
		profile.Change{Base: 2, Document: []byte(moved), Reason: ""},
	)
	if !errors.Is(err, identity.ErrDenied) || !strings.Contains(err.Error(), "values.host") {
		t.Fatalf("change of a value that a block reads, without open: error = %v", err)
	}

	_, _, err = f.svc.Save(
		t.Context(),
		actor("admin"),
		target,
		profile.Change{Base: 2, Document: []byte(moved), Reason: ""},
	)
	if err != nil {
		t.Errorf("same change with open: %v", err)
	}

	got, err := f.m.Get(t.Context(), f.ns, profile.RefByID(p.ID))
	if err != nil || got.Latest != 3 {
		t.Errorf("latest = %d, %v, want 3: the refused save must leave no revision", got.Latest, err)
	}
}

func TestSaveRules(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	p := f.create(t)
	target := f.target(profile.RefByID(p.ID))

	save := func(base int, doc []byte, reason string) error {
		_, _, err := f.svc.Save(
			t.Context(),
			actor("writer"),
			target,
			profile.Change{Base: base, Document: doc, Reason: reason},
		)

		return err
	}

	if err := save(0, []byte(secretDoc), ""); !errors.Is(err, profile.ErrInvalid) {
		t.Errorf("save without a base: error = %v, want ErrInvalid", err)
	}

	if err := save(5, []byte(secretDoc), ""); !errors.Is(err, profile.ErrConflict) {
		t.Errorf("save on a stale base: error = %v, want ErrConflict", err)
	}

	// A $sealed value copied from another profile does not open here.
	other, _, err := f.svc.Create(t.Context(), actor("writer"), f.ns, newProfileInput("other", secretDoc, ""))
	if err != nil {
		t.Fatalf("Create other: %v", err)
	}

	theirs, err := f.m.Revision(t.Context(), f.ns, other.ID, profile.At{Number: 1})
	if err != nil {
		t.Fatalf("Revision: %v", err)
	}

	if err = save(1, theirs.Document, ""); !errors.Is(err, profile.ErrInvalid) {
		t.Errorf("save of a sealed value of another profile: error = %v, want ErrInvalid", err)
	}

	// The sealed text of this profile can be sent back.
	mine, err := f.m.Revision(t.Context(), f.ns, p.ID, profile.At{Number: 1})
	if err != nil {
		t.Fatalf("Revision: %v", err)
	}

	if err = save(1, mine.Document, "again"); err != nil {
		t.Errorf("save of the sealed text: %v", err)
	}

	// A $sealed value on create has no key to open with.
	_, _, err = f.svc.Create(t.Context(), actor("writer"), f.ns, newProfileInput("copy", string(theirs.Document), ""))
	if !errors.Is(err, profile.ErrInvalid) {
		t.Errorf("create with a copied sealed value: error = %v, want ErrInvalid", err)
	}
}

func TestDeleteAndRevisions(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	p := f.create(t)
	target := f.target(profile.RefByName(p.Name))

	revs, err := f.svc.Revisions(t.Context(), actor("reader"), target, profile.RevisionPage{Before: 0, Limit: 5})
	if err != nil || len(revs) != 1 {
		t.Fatalf("Revisions = %+v, %v", revs, err)
	}

	err = f.svc.Delete(t.Context(), actor("manager"), target, profile.Removal{IfLatest: 9, Reason: ""})
	if !errors.Is(err, profile.ErrConflict) {
		t.Errorf("delete on a stale revision: error = %v, want ErrConflict", err)
	}

	err = f.svc.Delete(t.Context(), actor("manager"), target, profile.Removal{IfLatest: 1, Reason: "gone"})
	if err != nil {
		t.Fatalf("Delete: %v", err)
	}

	if _, err = f.svc.Get(t.Context(), actor("reader"), target); !errors.Is(err, profile.ErrNotFound) {
		t.Errorf("Get after delete: error = %v, want ErrNotFound", err)
	}
}
