package storetest

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/access"
	"opendustpan.org/opendustpan/pkg/accesstoken"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/backup"
	"opendustpan.org/opendustpan/pkg/durable"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/maintenance"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/principal"
	"opendustpan.org/opendustpan/pkg/runner"
	"opendustpan.org/opendustpan/pkg/session"
	"opendustpan.org/opendustpan/pkg/tempadmin"
)

// RawRevocation is one row of the revocation journal for a raw write.
type RawRevocation struct {
	Kind   string
	Target string
	Actor  string
	Object string
	AtMs   int64
}

// RevocationStore is every port the revocation suite uses: the stores of
// sessions, sign-in providers, access tokens, principals, grants, namespaces,
// runner pools and temporary administrators that it revokes through, the
// audit store that it reads the entries from, and the portable export and
// stage that the restore cases swap through.
//
//nolint:interfacebloat // One contract for the suite: each embedded port is a write that the suite revokes through or reads back.
type RevocationStore interface {
	PortableStore
	backup.Applier
	audit.Store
	session.Store
	session.ProviderConfigStore
	accesstoken.Store
	principal.RecordStore
	tempadmin.Store
	access.GrantStore
	namespace.Store

	// RunnerPools returns the pool port of the same store.
	RunnerPools() runner.PoolStore
	// RunnerPorts returns the pool and runner ports of the same store.
	RunnerPorts() RunnerPorts
	// DurableOperationsPort returns the durable operations of the same store,
	// where a runner holds its claims.
	DurableOperationsPort() durable.Store
	// InsertRawRevocation writes one row of the revocation journal with no
	// check of the kind, as a write to the database itself would. The hostile
	// journal case uses it to send a row that the store never writes.
	InsertRawRevocation(ctx context.Context, row RawRevocation) error
	// KeepsUnknownKinds reports whether a row of an unknown kind survives
	// until the swap. It does not when the restore copies the journal into a
	// table that holds the CHECK on the kind: the copy fails, and so does the
	// restore, before any replay.
	KeepsUnknownKinds() bool
}

// RevocationFactory returns a fresh, empty store for one subtest.
type RevocationFactory func(t *testing.T) RevocationStore

// RunRevocations executes the suite of the writes that only reduce access.
// In maintenance mode they run (a sign-out, a session end, a token revocation
// and the disable of a principal), each with its audit entry; the writes that
// grant or widen access stay refused; and a revocation made during the stay
// survives the restore of older access data.
func RunRevocations(t *testing.T, newStore RevocationFactory) {
	t.Helper()

	cases := []struct {
		test func(t *testing.T, s RevocationStore)
		name string
	}{
		{name: "a write that grants access is refused in maintenance mode", test: testGrantsRefused},
		{name: "a revocation refuses hostile input in maintenance mode", test: testRevocationsHostileInput},
		{name: "a revocation made in maintenance mode survives a restore", test: testRevocationSurvivesRestore},
		{name: "a refused delete of a provider journals nothing", test: testRefusedProviderDeleteJournalsNothing},
		{name: "the replay of a runner retire ends the claim of the runner", test: testRunnerRetireEndsClaim},
		{name: "a restore applies only the revocations of its own stay", test: testRevocationStayIsBounded},
		{name: "a restore refuses hostile journal rows and applies the rest", test: testReplayRefusesHostileRows},
		{name: "the sweep of temporary administrators expires in maintenance mode", test: testTempAdminSweepRuns},
		{name: "the last break-glass account stays in maintenance mode", test: testBreakGlassInMaintenance},
		{name: "a restore replays a disable but keeps one break-glass account", test: testRestoreKeepsOneBreakGlass},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tc.test(t, newStore(t))
		})
	}

	for _, tc := range revocationCases() {
		t.Run("in maintenance mode: "+tc.name, func(t *testing.T) {
			t.Parallel()
			testRevocationRuns(t, newStore(t), tc)
		})
	}
}

// Identities of the fixture of the revocation suite.
const (
	revPerson        = "prn-person"
	revService       = "prn-service"
	revTempAdmin     = "prn-temp"
	revSessionOne    = "ses-1"
	revSessionTwo    = "ses-2"
	revPersonalToken = "tok-personal"
	revServiceToken  = "tok-service"
	revTempToken     = "tok-temp"
	revKeepToken     = "tok-keep"
	revIdpPerson     = "prn-idp"
	revSessionIdp    = "ses-idp"
	revProvider      = "corp-idp"
	revSpareProvider = "spare-idp"
	revNamespace     = namespace.Name("acme")
	revGrantServer   = "grt-server"
	revGrantSpace    = "grt-space"
)

// revNoFence returns the zero fence of a write on the installation.
func revNoFence() namespace.Fence {
	return namespace.Fence{Namespace: "", FailoverVersion: 0}
}

// revFixture holds the rows of the suite that the store names: the fence of
// the namespace, the pool, the join token and the runner that joined with
// another join token.
type revFixture struct {
	fence     namespace.Fence
	pool      runner.Pool
	joinToken runner.JoinToken
	runnerID  string
}

// revAt is a fixed, millisecond-precision instant of the suite: stores keep
// milliseconds.
func revAt(minute int) time.Time {
	return time.Date(2026, time.October, 15, 9, minute, 0, 0, time.UTC)
}

// revEntry returns a valid audit entry of the kind change in the scope
// @server for object, as a service builds it.
func revEntry(object string, when time.Time) audit.Entry {
	return audit.Entry{
		At:            when,
		Namespace:     audit.ServerScope,
		Subject:       "alice",
		IDPSubject:    "",
		TokenID:       "",
		Action:        "server:manage-tokens",
		Object:        object,
		Version:       "",
		ValuePath:     "",
		ValueCount:    nil,
		Changes:       nil,
		SourceIP:      "192.0.2.1",
		UserAgent:     "storetest",
		Channel:       audit.ChannelAPI,
		Kind:          audit.KindChange,
		Outcome:       audit.OutcomeSuccess,
		RequestID:     "",
		ObjectID:      "",
		PrevHash:      "",
		Hash:          "",
		Sequence:      0,
		FenceVersion:  0,
		Reason:        "",
		PrincipalKind: "",
		JobID:         "",
	}
}

// revDigest returns a distinct digest for n.
func revDigest(n int) [sha256.Size]byte {
	return sha256.Sum256([]byte("revocation " + strconv.Itoa(n)))
}

// revMustPrincipal creates the principal id of kind.
func revMustPrincipal(t *testing.T, s RevocationStore, id string, kind principal.Kind) {
	t.Helper()

	p := principal.Principal{
		CreatedAt:   revAt(0),
		UpdatedAt:   revAt(0),
		DisabledAt:  time.Time{},
		ID:          id,
		Subject:     "subject-" + id,
		DisplayName: "Name of " + id,
		Kind:        kind,
		Source:      principal.SourceLocal,
		Namespace:   "",
		GroupCount:  0,
		BreakGlass:  false,
	}

	if err := s.CreatePrincipal(t.Context(), p, revEntry(principal.Ref(id), revAt(0))); err != nil {
		t.Fatalf("create principal %q: %v", id, err)
	}
}

// revMustSession creates the session id of principalID.
func revMustSession(t *testing.T, s RevocationStore, id, principalID string, n int) {
	t.Helper()

	created := revAt(1)
	ss := session.Session{
		CreatedAt:   created,
		LastSeenAt:  created,
		AuthTime:    created.Add(-time.Minute),
		EndedAt:     time.Time{},
		ID:          id,
		PrincipalID: principalID,
		Provider:    "corp-idp",
		EndReason:   "",
		SourceIP:    "192.0.2.10",
		UserAgent:   "storetest",
	}

	e := revEntry(session.FullRef(principalID, id), created)

	if err := s.CreateSession(t.Context(), ss, revDigest(n), e); err != nil {
		t.Fatalf("create session %q: %v", id, err)
	}
}

// revToken returns the token id of principalID with kind.
func revToken(id, principalID string, kind accesstoken.Kind) accesstoken.Token {
	return accesstoken.Token{
		CreatedAt:   revAt(1),
		ExpiresAt:   revAt(1).Add(30 * 24 * time.Hour),
		LastUsedAt:  time.Time{},
		RevokedAt:   time.Time{},
		DisabledAt:  time.Time{},
		ID:          id,
		PrincipalID: principalID,
		Name:        "token " + id,
		Kind:        kind,
		ReplacedBy:  "",
		CreatedBy:   "admin",
	}
}

// revMustToken creates tok with the digest of n.
func revMustToken(t *testing.T, s RevocationStore, tok accesstoken.Token, n int) {
	t.Helper()

	if err := s.CreateToken(t.Context(), tok, revDigest(n), revEntry(accesstoken.Ref(tok.ID), revAt(1))); err != nil {
		t.Fatalf("create token %q: %v", tok.ID, err)
	}
}

// seedAccess creates the access data of the suite: a person with two
// sessions and a personal token, a service principal with a service token, a
// person who is a temporary administrator and holds a token, a person of the
// identity provider revProvider with a session, a grant on the installation
// and one in the namespace revNamespace, and a join token of a runner pool.
func seedAccess(t *testing.T, s RevocationStore) revFixture {
	t.Helper()

	revMustPrincipal(t, s, revPerson, principal.KindPerson)
	revMustPrincipal(t, s, revService, principal.KindService)
	revMustPrincipal(t, s, revTempAdmin, principal.KindPerson)

	revMustSession(t, s, revSessionOne, revPerson, 1)
	revMustSession(t, s, revSessionTwo, revPerson, 2)
	revMustToken(t, s, revToken(revPersonalToken, revPerson, accesstoken.KindPersonal), 3)
	revMustToken(t, s, revToken(revKeepToken, revPerson, accesstoken.KindPersonal), 4)
	revMustToken(t, s, revToken(revServiceToken, revService, accesstoken.KindService), 5)
	revMustToken(t, s, revToken(revTempToken, revTempAdmin, accesstoken.KindPersonal), 6)

	record := tempadmin.TempAdmin{
		CreatedAt:   revAt(2),
		ExpiresAt:   revAt(2).Add(tempadmin.Lifetime),
		EndedAt:     time.Time{},
		PrincipalID: revTempAdmin,
		Subject:     "subject-" + revTempAdmin,
		CreatedBy:   "admin",
		Reason:      "every administrator lost access",
	}

	if err := s.CreateTempAdmin(t.Context(), record, revEntry(tempadmin.Ref(revTempAdmin), revAt(2))); err != nil {
		t.Fatalf("create temporary administrator: %v", err)
	}

	revMustProvider(t, s)

	fence := revMustNamespace(t, s)

	revMustGrant(t, s, revGrantServer, identity.ServerObject(), revNoFence())
	revMustGrant(t, s, revGrantSpace, identity.NamespaceObject(revNamespace), fence)

	pool, token := revMustJoinToken(t, s, fence)

	return revFixture{fence: fence, pool: pool, joinToken: token, runnerID: revMustRunner(t, s, fence, pool)}
}

// revEntryIn returns revEntry in the audit scope ns.
func revEntryIn(ns namespace.Name, object string, when time.Time) audit.Entry {
	e := revEntry(object, when)
	e.Namespace = ns

	return e
}

// revMustProviderConfig creates the enabled sign-in provider id.
func revMustProviderConfig(t *testing.T, s RevocationStore, id string) {
	t.Helper()

	p := session.ProviderConfig{
		CreatedAt: revAt(0), UpdatedAt: revAt(0), ID: id, Mode: session.ModeOIDC,
		Issuer: "https://" + id + ".idp.example", DisplayName: "Corporate", ClientID: "dustpan-" + id,
		SealedSecret: []byte("sealed secret"), Scopes: []string{"profile", "email"},
		OAuth: session.OAuthSettings{
			AuthorizationURL: "", TokenURL: "", UserinfoURL: "", SubjectClaim: "", NameClaim: "",
			UsernameClaim: "", EmailClaim: "",
		},
		Version: 1, AutoCreate: true, Enabled: true,
	}

	e := revEntry("sign-in-provider:"+id, revAt(0))
	e.Action = "server:manage-sign-in"

	if err := s.CreateProviderConfig(t.Context(), p, e); err != nil {
		t.Fatalf("create sign-in provider %q: %v", id, err)
	}
}

// revMustProvider creates the sign-in provider revProvider, a person of it and
// a session of the person. It also creates revSpareProvider, which no person
// uses, so that a delete of it is allowed.
func revMustProvider(t *testing.T, s RevocationStore) {
	t.Helper()

	revMustProviderConfig(t, s, revProvider)
	revMustProviderConfig(t, s, revSpareProvider)

	person := principal.Principal{
		CreatedAt: revAt(0), UpdatedAt: revAt(0), DisabledAt: time.Time{}, ID: revIdpPerson,
		Subject: revProvider + ":sam", DisplayName: "Sam", Kind: principal.KindPerson,
		Source: principal.SourceLocal, Namespace: "", GroupCount: 0, BreakGlass: false,
	}

	if err := s.CreatePrincipal(t.Context(), person, revEntry(principal.Ref(revIdpPerson), revAt(0))); err != nil {
		t.Fatalf("create principal %q: %v", revIdpPerson, err)
	}

	revMustSession(t, s, revSessionIdp, revIdpPerson, 7)
}

// revMustNamespace creates the namespace revNamespace and returns its fence.
func revMustNamespace(t *testing.T, s RevocationStore) namespace.Fence {
	t.Helper()

	ns := namespace.Namespace{
		ID:                  namespace.NewID(),
		CreatedAt:           revAt(0),
		Name:                revNamespace,
		PrimaryInstallation: "eu-west",
		FailoverVersion:     1,
		Overrides:           namespace.Overrides{Forced: false, Unlocked: false},
	}

	if err := s.CreateNamespace(t.Context(), ns); err != nil {
		t.Fatalf("create namespace: %v", err)
	}

	return ns.Fence()
}

// revMustGrant gives the person revPerson the role writer on obj.
func revMustGrant(t *testing.T, s RevocationStore, id string, obj identity.Object, fence namespace.Fence) {
	t.Helper()

	g := access.Grant{
		CreatedAt: revAt(0), Condition: nil, Object: obj, ID: id, Role: access.RoleWriter, CreatedBy: "admin",
		Subject: access.Subject{Kind: access.SubjectPrincipal, ID: revPerson},
	}

	e := revEntryIn(fence.Namespace, obj.String(), revAt(0))
	if fence.Namespace == "" {
		e.Namespace = audit.ServerScope
	}

	if err := s.CreateGrant(t.Context(), g, fence, e, nil); err != nil {
		t.Fatalf("create grant %q: %v", id, err)
	}
}

// revMustJoinToken creates a runner pool and one join token of it in the
// namespace of fence.
func revMustJoinToken(t *testing.T, s RevocationStore, fence namespace.Fence) (runner.Pool, runner.JoinToken) {
	t.Helper()

	ctx := t.Context()
	entries := []audit.Entry{revEntryIn(fence.Namespace, "namespace:"+fence.Namespace.String(), revAt(3))}

	pool, err := s.RunnerPools().CreatePool(ctx, runner.Pool{
		ID: "", Namespace: fence.Namespace, Name: "prod", Purpose: runner.PurposeJobs, Lease: 0, Concurrency: 0,
		Isolation: nil, GitCredentials: runner.GitCredentials{Mode: "", Helper: ""}, CreatedAt: revAt(3),
		UpdatedAt: revAt(3),
	}, fence, entries)
	if err != nil {
		t.Fatalf("create runner pool: %v", err)
	}

	sum := sha256.Sum256([]byte(runner.JoinTokenPrefix + "revocation suite"))
	token, err := s.RunnerPools().CreateJoinToken(ctx, runner.JoinToken{
		ID: "", Pool: pool.ID, SHA256: hex.EncodeToString(sum[:]), CreatedBy: "alice", CreatedAt: revAt(3),
		ExpiresAt: revAt(3).Add(time.Hour), UsedAt: time.Time{}, UsedBy: "", RevokedAt: time.Time{},
	}, fence, entries)
	if err != nil {
		t.Fatalf("create join token: %v", err)
	}

	return pool, token
}

// revMustRunner creates a second join token of pool and joins a runner with
// it, so that the first token stays unused for the revoke cases. It returns
// the ID of the runner.
func revMustRunner(t *testing.T, s RevocationStore, fence namespace.Fence, pool runner.Pool) string {
	t.Helper()

	ctx := t.Context()
	entries := []audit.Entry{revEntryIn(fence.Namespace, "namespace:"+fence.Namespace.String(), revAt(3))}
	sum := sha256.Sum256([]byte(runner.JoinTokenPrefix + "revocation suite runner"))

	token, err := s.RunnerPools().CreateJoinToken(ctx, runner.JoinToken{
		ID: "", Pool: pool.ID, SHA256: hex.EncodeToString(sum[:]), CreatedBy: "alice", CreatedAt: revAt(3),
		ExpiresAt: revAt(3).Add(time.Hour), UsedAt: time.Time{}, UsedBy: "", RevokedAt: time.Time{},
	}, fence, entries)
	if err != nil {
		t.Fatalf("create join token of the runner: %v", err)
	}

	pub, _, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("generate runner key: %v", err)
	}

	joined, err := s.RunnerPorts().Members.Join(ctx, runner.Joining{
		TokenSHA256: token.SHA256,
		Request: runner.JoinRequest{
			Token: "", Name: "linux-1", Kind: runner.KindKubernetes, Isolation: runner.IsolationKubernetes,
			Labels:  map[string]string{},
			Release: runner.Release{Major: 0, Minor: 14, Patch: 0, Pre: ""},
			Key:     runner.Key{Public: pub, CreatedAt: time.Time{}}, Proof: nil,
		},
		At: revAt(4),
	}, fence, func(j *runner.Joined) []audit.Entry {
		e := revEntryIn(fence.Namespace, "namespace:"+fence.Namespace.String(), revAt(4))
		e.ObjectID = j.Runner.ID

		return []audit.Entry{e}
	})
	if err != nil {
		t.Fatalf("join runner: %v", err)
	}

	return joined.Runner.ID
}

// revRunner returns the runner of f.
func revRunner(t *testing.T, s RevocationStore, f revFixture) runner.Runner {
	t.Helper()

	rn, err := s.RunnerPorts().Members.Runner(t.Context(), f.runnerID)
	if err != nil {
		t.Fatalf("read runner %s: %v", f.runnerID, err)
	}

	return rn
}

// revRunnerRetired fails the test unless the runner of f is retired.
func revRunnerRetired(t *testing.T, s RevocationStore, f revFixture) {
	t.Helper()

	if revRunner(t, s, f).RetiredAt.IsZero() {
		t.Fatal("runner is live, want it retired")
	}
}

// revEnter moves the installation to maintenance mode.
func revEnter(t *testing.T, s RevocationStore) {
	t.Helper()

	if err := s.SetStateIf(t.Context(), maintenance.StateNormal, maintenance.StateMaintenance); err != nil {
		t.Fatalf("enter maintenance mode: %v", err)
	}
}

// revLeave moves the installation back to normal operation.
func revLeave(t *testing.T, s RevocationStore) {
	t.Helper()

	if err := s.SetStateIf(t.Context(), maintenance.StateMaintenance, maintenance.StateNormal); err != nil {
		t.Fatalf("leave maintenance mode: %v", err)
	}
}

// revAuditTotal returns the count of the entries in the scope @server.
func revAuditTotal(t *testing.T, s RevocationStore) int64 {
	t.Helper()

	return revAuditTotalIn(t, s, audit.ServerScope)
}

// revAuditTotalIn returns the count of the entries in the scope ns.
func revAuditTotalIn(t *testing.T, s RevocationStore, ns namespace.Name) int64 {
	t.Helper()

	listing, err := s.List(t.Context(), ns, audit.Page{Number: 1, Size: 1})
	if err != nil {
		t.Fatalf("list the audit entries of %q: %v", ns, err)
	}

	return listing.Total
}

// revReplayEntries returns the entries that a restore wrote for the
// revocations that it applied again, in the scope @server, in order.
func revReplayEntries(t *testing.T, s RevocationStore) []audit.Entry {
	t.Helper()

	var out []audit.Entry

	for page := 1; ; page++ {
		listing, err := s.List(t.Context(), audit.ServerScope, audit.Page{Number: page, Size: audit.MaxPageSize})
		if err != nil {
			t.Fatalf("list the server audit entries: %v", err)
		}

		for i := range listing.Entries {
			if listing.Entries[i].Action == maintenance.ActionReplayRevocation {
				out = append(out, listing.Entries[i])
			}
		}

		if len(listing.Entries) < audit.MaxPageSize {
			slices.SortFunc(out, func(a, b audit.Entry) int { return int(a.Sequence - b.Sequence) })

			return out
		}
	}
}

// revSessionEnded fails the test unless the session id has ended with reason.
func revSessionEnded(t *testing.T, s RevocationStore, id string, reason session.EndReason) {
	t.Helper()

	got, err := s.GetSession(t.Context(), id)
	if err != nil {
		t.Fatalf("get session %q: %v", id, err)
	}

	if got.EndedAt.IsZero() || got.EndReason != reason {
		t.Fatalf("session %q = ended %v for %q, want ended for %q", id, got.EndedAt, got.EndReason, reason)
	}
}

// revSessionActive fails the test when the session id has ended.
func revSessionActive(t *testing.T, s RevocationStore, id string) {
	t.Helper()

	got, err := s.GetSession(t.Context(), id)
	if err != nil {
		t.Fatalf("get session %q: %v", id, err)
	}

	if !got.EndedAt.IsZero() {
		t.Fatalf("session %q ended at %v, want it active", id, got.EndedAt)
	}
}

// revTokenRevoked fails the test unless the token id is revoked.
func revTokenRevoked(t *testing.T, s RevocationStore, id string) {
	t.Helper()

	got, err := s.GetToken(t.Context(), id)
	if err != nil {
		t.Fatalf("get token %q: %v", id, err)
	}

	if got.RevokedAt.IsZero() {
		t.Fatalf("token %q is not revoked, want it revoked", id)
	}
}

// revTokenActive fails the test when the token id is revoked.
func revTokenActive(t *testing.T, s RevocationStore, id string) {
	t.Helper()

	got, err := s.GetToken(t.Context(), id)
	if err != nil {
		t.Fatalf("get token %q: %v", id, err)
	}

	if !got.RevokedAt.IsZero() {
		t.Fatalf("token %q was revoked at %v, want it active", id, got.RevokedAt)
	}
}

// revPrincipalDisabled fails the test unless the principal id is disabled.
func revPrincipalDisabled(t *testing.T, s RevocationStore, id string) {
	t.Helper()

	got, err := s.GetPrincipal(t.Context(), id)
	if err != nil {
		t.Fatalf("get principal %q: %v", id, err)
	}

	if got.DisabledAt.IsZero() {
		t.Fatalf("principal %q is enabled, want it disabled", id)
	}
}

// revPrincipalEnabled fails the test when the principal id is disabled.
func revPrincipalEnabled(t *testing.T, s RevocationStore, id string) {
	t.Helper()

	got, err := s.GetPrincipal(t.Context(), id)
	if err != nil {
		t.Fatalf("get principal %q: %v", id, err)
	}

	if !got.DisabledAt.IsZero() {
		t.Fatalf("principal %q was disabled at %v, want it enabled", id, got.DisabledAt)
	}
}

// revocationCase is one write that only reduces access, with a check of its
// effect. scope is the audit scope that holds its entry.
type revocationCase struct {
	run   func(t *testing.T, s RevocationStore, f revFixture) error
	check func(t *testing.T, s RevocationStore, f revFixture)
	name  string
	scope namespace.Name
}

// revGrantRevoked fails the test unless the grant id is revoked.
func revGrantRevoked(t *testing.T, s RevocationStore, id string) {
	t.Helper()

	if _, err := s.GetGrant(t.Context(), id); !errors.Is(err, access.ErrNotFound) {
		t.Fatalf("get revoked grant %q: err = %v, want ErrNotFound", id, err)
	}
}

// revGrantLive fails the test unless the grant id is current.
func revGrantLive(t *testing.T, s RevocationStore, id string) {
	t.Helper()

	if _, err := s.GetGrant(t.Context(), id); err != nil {
		t.Fatalf("get live grant %q: %v", id, err)
	}
}

// revJoinToken returns the join token of f, the one that no runner used.
func revJoinToken(t *testing.T, s RevocationStore, f revFixture) runner.JoinToken {
	t.Helper()

	tokens, err := s.RunnerPools().JoinTokens(t.Context(), f.pool.ID)
	if err != nil {
		t.Fatalf("join tokens: %v", err)
	}

	i := slices.IndexFunc(tokens, func(tok runner.JoinToken) bool { return tok.ID == f.joinToken.ID })
	if i < 0 {
		t.Fatalf("join tokens = %+v, want the token %s", tokens, f.joinToken.ID)
	}

	return tokens[i]
}

// revJoinTokenRevoked fails the test unless the join token of f is revoked.
func revJoinTokenRevoked(t *testing.T, s RevocationStore, f revFixture) {
	t.Helper()

	if revJoinToken(t, s, f).RevokedAt.IsZero() {
		t.Fatal("join token is live, want it revoked")
	}
}

// revJoinTokenLive fails the test when the join token of f is revoked.
func revJoinTokenLive(t *testing.T, s RevocationStore, f revFixture) {
	t.Helper()

	if got := revJoinToken(t, s, f).RevokedAt; !got.IsZero() {
		t.Fatalf("join token was revoked at %v, want it live", got)
	}
}

// revProviderConfigOf reads the sign-in provider id.
func revProviderConfigOf(t *testing.T, s RevocationStore, id string) session.ProviderConfig {
	t.Helper()

	p, err := s.GetProviderConfig(t.Context(), id)
	if err != nil {
		t.Fatalf("get sign-in provider: %v", err)
	}

	return p
}

// revProviderConfig reads the sign-in provider revProvider.
func revProviderConfig(t *testing.T, s RevocationStore) session.ProviderConfig {
	t.Helper()

	return revProviderConfigOf(t, s, revProvider)
}

// revSpareProviderGone fails the test unless the provider revSpareProvider
// does not exist.
func revSpareProviderGone(t *testing.T, s RevocationStore) {
	t.Helper()

	_, err := s.GetProviderConfig(t.Context(), revSpareProvider)
	if !errors.Is(err, session.ErrProviderNotFound) {
		t.Fatalf("get the deleted provider: err = %v, want ErrProviderNotFound", err)
	}
}

// revSpareProviderDisabled fails the test unless the provider
// revSpareProvider is disabled at version 2.
func revSpareProviderDisabled(t *testing.T, s RevocationStore) {
	t.Helper()

	if p := revProviderConfigOf(t, s, revSpareProvider); p.Enabled || p.Version != 2 {
		t.Fatalf("spare provider = enabled %v at version %d, want disabled at version 2", p.Enabled, p.Version)
	}
}

// revProviderDisabled fails the test unless the provider revProvider is
// disabled at the version version.
func revProviderDisabled(t *testing.T, s RevocationStore, version int64) {
	t.Helper()

	if p := revProviderConfig(t, s); p.Enabled || p.Version != version {
		t.Fatalf("provider = enabled %v at version %d, want disabled at version %d", p.Enabled, p.Version, version)
	}
}

// revProviderUntouched fails the test unless the provider revProvider is
// enabled at the version 1, as the fixture creates it.
func revProviderUntouched(t *testing.T, s RevocationStore) {
	t.Helper()

	if p := revProviderConfig(t, s); !p.Enabled || p.Version != 1 {
		t.Fatalf("provider = enabled %v at version %d, want enabled at version 1", p.Enabled, p.Version)
	}
}

// revocationCases lists every write that only reduces access.
func revocationCases() []revocationCase {
	return []revocationCase{
		{
			name: "sign-out ends the session", scope: audit.ServerScope,
			run: func(t *testing.T, s RevocationStore, _ revFixture) error {
				t.Helper()

				e := revEntry(session.FullRef(revPerson, revSessionOne), revAt(10))

				return s.EndSession(t.Context(), revSessionOne, session.ReasonSignOut, e)
			},
			check: func(t *testing.T, s RevocationStore, _ revFixture) {
				t.Helper()
				revSessionEnded(t, s, revSessionOne, session.ReasonSignOut)
				revSessionActive(t, s, revSessionTwo)
			},
		},
		{
			name: "an administrator ends the session", scope: audit.ServerScope,
			run: func(t *testing.T, s RevocationStore, _ revFixture) error {
				t.Helper()

				e := revEntry(session.FullRef(revPerson, revSessionOne), revAt(10))

				return s.EndSession(t.Context(), revSessionOne, session.ReasonEnded, e)
			},
			check: func(t *testing.T, s RevocationStore, _ revFixture) {
				t.Helper()
				revSessionEnded(t, s, revSessionOne, session.ReasonEnded)
			},
		},
		{
			name: "revoking an access token", scope: audit.ServerScope,
			run: func(t *testing.T, s RevocationStore, _ revFixture) error {
				t.Helper()

				return s.RevokeToken(
					t.Context(),
					revPersonalToken,
					revEntry(accesstoken.Ref(revPersonalToken), revAt(10)),
				)
			},
			check: func(t *testing.T, s RevocationStore, _ revFixture) {
				t.Helper()
				revTokenRevoked(t, s, revPersonalToken)
				revTokenActive(t, s, revKeepToken)
			},
		},
		{
			name: "revoking a service token", scope: audit.ServerScope,
			run: func(t *testing.T, s RevocationStore, _ revFixture) error {
				t.Helper()

				return s.RevokeToken(
					t.Context(),
					revServiceToken,
					revEntry(accesstoken.Ref(revServiceToken), revAt(10)),
				)
			},
			check: func(t *testing.T, s RevocationStore, _ revFixture) {
				t.Helper()
				revTokenRevoked(t, s, revServiceToken)
			},
		},
		{
			name: "disabling a principal", scope: audit.ServerScope,
			run: func(t *testing.T, s RevocationStore, _ revFixture) error {
				t.Helper()

				return s.DisablePrincipal(t.Context(), revPerson, revEntry(principal.Ref(revPerson), revAt(10)))
			},
			check: func(t *testing.T, s RevocationStore, _ revFixture) {
				t.Helper()
				revPrincipalDisabled(t, s, revPerson)
				revPrincipalEnabled(t, s, revService)
				// The disable keeps the grants and the tokens: only the enable
				// gives the principal their use again.
				revTokenActive(t, s, revPersonalToken)
				revGrantLive(t, s, revGrantServer)
			},
		},
		{
			name: "expiring a temporary administrator", scope: audit.ServerScope,
			run: func(t *testing.T, s RevocationStore, _ revFixture) error {
				t.Helper()

				return s.ExpireTempAdmin(t.Context(), revTempAdmin, revEntry(tempadmin.Ref(revTempAdmin), revAt(10)))
			},
			check: func(t *testing.T, s RevocationStore, _ revFixture) {
				t.Helper()
				revPrincipalDisabled(t, s, revTempAdmin)
				revTokenRevoked(t, s, revTempToken)
				revPrincipalEnabled(t, s, revPerson)
			},
		},
		{
			name: "revoking a grant on the installation", scope: audit.ServerScope,
			run: func(t *testing.T, s RevocationStore, _ revFixture) error {
				t.Helper()

				e := revEntry(identity.ServerObject().String(), revAt(10))

				return s.RevokeGrant(t.Context(), revGrantServer, revNoFence(), e, nil)
			},
			check: func(t *testing.T, s RevocationStore, _ revFixture) {
				t.Helper()
				revGrantRevoked(t, s, revGrantServer)
				revGrantLive(t, s, revGrantSpace)
			},
		},
		{
			name: "revoking a grant in a namespace", scope: revNamespace,
			run: func(t *testing.T, s RevocationStore, f revFixture) error {
				t.Helper()

				e := revEntryIn(revNamespace, identity.NamespaceObject(revNamespace).String(), revAt(10))

				return s.RevokeGrant(t.Context(), revGrantSpace, f.fence, e, nil)
			},
			check: func(t *testing.T, s RevocationStore, _ revFixture) {
				t.Helper()
				revGrantRevoked(t, s, revGrantSpace)
				revGrantLive(t, s, revGrantServer)
			},
		},
		{
			name: "revoking a join token", scope: revNamespace,
			run: func(t *testing.T, s RevocationStore, f revFixture) error {
				t.Helper()

				e := []audit.Entry{revEntryIn(revNamespace, "namespace:"+revNamespace.String(), revAt(10))}

				return s.RunnerPools().RevokeJoinToken(t.Context(), f.joinToken.ID, f.fence, e)
			},
			check: func(t *testing.T, s RevocationStore, f revFixture) {
				t.Helper()
				revJoinTokenRevoked(t, s, f)
			},
		},
		{
			name: "retiring a runner", scope: revNamespace,
			run: func(t *testing.T, s RevocationStore, f revFixture) error {
				t.Helper()

				e := []audit.Entry{revEntryIn(revNamespace, "namespace:"+revNamespace.String(), revAt(10))}

				return s.RunnerPorts().Members.Retire(t.Context(), f.runnerID, revAt(10), f.fence, e)
			},
			check: func(t *testing.T, s RevocationStore, f revFixture) {
				t.Helper()
				revRunnerRetired(t, s, f)
			},
		},
		{
			name: "disabling an identity provider", scope: audit.ServerScope,
			run: func(t *testing.T, s RevocationStore, _ revFixture) error {
				t.Helper()

				e := revEntry("sign-in-provider:"+revProvider, revAt(10))
				e.Action = "server:manage-sign-in"

				return s.DisableProviderConfig(t.Context(), revProvider, 1, revAt(10), e)
			},
			check: func(t *testing.T, s RevocationStore, _ revFixture) {
				t.Helper()
				revProviderDisabled(t, s, 2)
				revSessionEnded(t, s, revSessionIdp, session.ReasonEnded)
				revSessionActive(t, s, revSessionOne)
			},
		},
		{
			name: "deleting an identity provider", scope: audit.ServerScope,
			run: func(t *testing.T, s RevocationStore, _ revFixture) error {
				t.Helper()

				e := revEntry("sign-in-provider:"+revSpareProvider, revAt(10))
				e.Action = "server:manage-sign-in"

				return s.DeleteProviderConfig(t.Context(), revSpareProvider, e)
			},
			check: func(t *testing.T, s RevocationStore, _ revFixture) {
				t.Helper()

				revSpareProviderGone(t, s)

				revProviderUntouched(t, s)
			},
		},
	}
}

// testRevocationRuns proves that one write that only reduces access runs in
// maintenance mode, changes the row, and appends its audit entry in the same
// transaction: one entry in its scope, and none in another.
func testRevocationRuns(t *testing.T, s RevocationStore, tc revocationCase) {
	t.Helper()

	f := seedAccess(t, s)
	revEnter(t, s)

	other := revNamespace
	if tc.scope == revNamespace {
		other = audit.ServerScope
	}

	before := revAuditTotalIn(t, s, tc.scope)
	beforeOther := revAuditTotalIn(t, s, other)

	if err := tc.run(t, s, f); err != nil {
		t.Fatalf("%s in maintenance mode: %v", tc.name, err)
	}

	tc.check(t, s, f)

	if got := revAuditTotalIn(t, s, tc.scope); got != before+1 {
		t.Fatalf("%s in maintenance mode: audit entries in %q = %d, want %d", tc.name, tc.scope, got, before+1)
	}

	if got := revAuditTotalIn(t, s, other); got != beforeOther {
		t.Fatalf("%s in maintenance mode: audit entries in %q = %d, want %d", tc.name, other, got, beforeOther)
	}
}

// grantAttempt is one write that grants or widens access.
type grantAttempt struct {
	run  func(t *testing.T, s RevocationStore) error
	name string
}

// testGrantsRefused proves that the writes which grant or widen access stay
// checked: in maintenance mode each answers maintenance.ErrInMaintenance and
// changes nothing. The writes that could widen access under a limit or a
// forbid rule stay checked too: the removal of a member, the delete of a
// principal and the inactivity sweep.
func testGrantsRefused(t *testing.T, s RevocationStore) {
	t.Helper()

	f := seedAccess(t, s)

	// A disabled principal to enable: a temporary administrator that expired.
	expire := revEntry(tempadmin.Ref(revTempAdmin), revAt(5))

	if err := s.ExpireTempAdmin(t.Context(), revTempAdmin, expire); err != nil {
		t.Fatalf("expire the temporary administrator: %v", err)
	}

	revEnter(t, s)

	attempts := []grantAttempt{
		{
			name: "create a session",
			run: func(t *testing.T, s RevocationStore) error {
				t.Helper()

				ss := session.Session{
					CreatedAt: revAt(20), LastSeenAt: revAt(20), AuthTime: revAt(19), EndedAt: time.Time{},
					ID: "ses-new", PrincipalID: revPerson, Provider: "corp-idp", EndReason: "",
					SourceIP: "192.0.2.10", UserAgent: "storetest",
				}

				return s.CreateSession(
					t.Context(),
					ss,
					revDigest(100),
					revEntry(session.FullRef(revPerson, "ses-new"), revAt(20)),
				)
			},
		},
		{
			name: "create an access token",
			run: func(t *testing.T, s RevocationStore) error {
				t.Helper()

				tok := accesstoken.Token{
					CreatedAt: revAt(20), ExpiresAt: revAt(20).Add(time.Hour), LastUsedAt: time.Time{},
					RevokedAt: time.Time{}, DisabledAt: time.Time{}, ID: "tok-new", PrincipalID: revPerson,
					Name: "new", Kind: accesstoken.KindPersonal, ReplacedBy: "", CreatedBy: "admin",
				}

				return s.CreateToken(t.Context(), tok, revDigest(101), revEntry(accesstoken.Ref("tok-new"), revAt(20)))
			},
		},
		{
			name: "enable a disabled principal",
			run: func(t *testing.T, s RevocationStore) error {
				t.Helper()

				return s.EnablePrincipal(t.Context(), revTempAdmin, revEntry(principal.Ref(revTempAdmin), revAt(20)))
			},
		},
		{
			name: "create a grant",
			run: func(t *testing.T, s RevocationStore) error {
				t.Helper()

				g := access.Grant{
					CreatedAt: revAt(20), Condition: nil, Object: identity.ServerObject(), ID: "grt-new",
					Role: access.RoleWriter, CreatedBy: "admin",
					Subject: access.Subject{Kind: access.SubjectPrincipal, ID: revService},
				}

				return s.CreateGrant(
					t.Context(),
					g,
					revNoFence(),
					revEntry(identity.ServerObject().String(), revAt(20)),
					nil,
				)
			},
		},
		{
			name: "update an identity provider, which can enable it",
			run: func(t *testing.T, s RevocationStore) error {
				t.Helper()

				p, err := s.GetProviderConfig(t.Context(), revProvider)
				if err != nil {
					return err
				}

				p.Version++

				e := revEntry("sign-in-provider:"+revProvider, revAt(20))
				e.Action = "server:manage-sign-in"

				return s.UpdateProviderConfig(t.Context(), p, e)
			},
		},
		{
			name: "delete a principal, which ends a limit that it holds",
			run: func(t *testing.T, s RevocationStore) error {
				t.Helper()

				return s.DeletePrincipal(t.Context(), revService, revEntry(principal.Ref(revService), revAt(20)))
			},
		},
		{
			name: "sweep the inactive records",
			run: func(t *testing.T, s RevocationStore) error {
				t.Helper()

				_, err := s.SweepInactive(
					t.Context(),
					accesstoken.Sweep{Inactivity: time.Hour, Tokens: true, Accounts: true},
					func(at time.Time, object string) audit.Entry { return revEntry(object, at) },
				)

				return err
			},
		},
	}

	before := revAuditTotal(t, s)

	for _, tc := range attempts {
		if err := tc.run(t, s); !errors.Is(err, maintenance.ErrInMaintenance) {
			t.Fatalf("%s in maintenance mode: err = %v, want ErrInMaintenance", tc.name, err)
		}
	}

	if _, err := s.GetSession(t.Context(), "ses-new"); !errors.Is(err, session.ErrNotFound) {
		t.Fatalf("get the refused session: err = %v, want ErrNotFound", err)
	}

	if _, err := s.GetToken(t.Context(), "tok-new"); !errors.Is(err, accesstoken.ErrNotFound) {
		t.Fatalf("get the refused token: err = %v, want ErrNotFound", err)
	}

	revPrincipalDisabled(t, s, revTempAdmin)
	revPrincipalEnabled(t, s, revService)
	revProviderUntouched(t, s)
	revJoinTokenLive(t, s, f)

	if got := revAuditTotal(t, s); got != before {
		t.Fatalf("audit entries after the refused writes = %d, want %d", got, before)
	}
}

// testRevocationsHostileInput proves that the control path keeps its own
// checks as if the layers above it had failed: an invalid end reason, an
// invalid audit entry and an unknown or ended target each fail, change
// nothing and append no audit entry, also in maintenance mode.
func testRevocationsHostileInput(t *testing.T, s RevocationStore) {
	t.Helper()

	f := seedAccess(t, s)
	revEnter(t, s)

	ctx := t.Context()
	before := revAuditTotal(t, s)

	invalidEntry := revEntry(accesstoken.Ref(revPersonalToken), revAt(10))
	invalidEntry.Subject = ""

	otherScope := revEntry(accesstoken.Ref(revPersonalToken), revAt(10))
	otherScope.Namespace = "acme"

	cases := []struct {
		run  func() error
		want error
		name string
	}{
		{
			name: "an end reason outside the known set",
			run: func() error {
				return s.EndSession(ctx, revSessionOne, session.EndReason("granted"),
					revEntry(session.FullRef(revPerson, revSessionOne), revAt(10)))
			},
			want: session.ErrInvalid,
		},
		{
			name: "an audit entry without a subject",
			run:  func() error { return s.RevokeToken(ctx, revPersonalToken, invalidEntry) },
			want: audit.ErrInvalidEntry,
		},
		{
			name: "an audit entry of another scope",
			run:  func() error { return s.RevokeToken(ctx, revPersonalToken, otherScope) },
			want: audit.ErrInvalidEntry,
		},
		{
			name: "a session that does not exist",
			run: func() error {
				return s.EndSession(
					ctx,
					"ses-none",
					session.ReasonSignOut,
					revEntry(session.FullRef(revPerson, "ses-none"), revAt(10)),
				)
			},
			want: session.ErrNotFound,
		},
		{
			name: "a token that does not exist",
			run:  func() error { return s.RevokeToken(ctx, "tok-none", revEntry(accesstoken.Ref("tok-none"), revAt(10))) },
			want: accesstoken.ErrNotFound,
		},
		{
			name: "a temporary administrator that does not exist",
			run: func() error {
				return s.ExpireTempAdmin(ctx, "prn-none", revEntry(tempadmin.Ref("prn-none"), revAt(10)))
			},
			want: tempadmin.ErrNotFound,
		},
		{
			name: "a principal that does not exist",
			run: func() error {
				return s.DisablePrincipal(ctx, "prn-none", revEntry(principal.Ref("prn-none"), revAt(10)))
			},
			want: principal.ErrNotFound,
		},
		{
			name: "a grant that does not exist",
			run: func() error {
				return s.RevokeGrant(ctx, "grt-none", revNoFence(), revEntry("server", revAt(10)), nil)
			},
			want: access.ErrNotFound,
		},
		{
			name: "a grant of the namespace through the fence of the installation",
			run: func() error {
				return s.RevokeGrant(ctx, revGrantSpace, revNoFence(), revEntry("server", revAt(10)), nil)
			},
			want: access.ErrNotFound,
		},
		{
			name: "a grant of the installation through the fence of a namespace",
			run: func() error {
				e := revEntryIn(revNamespace, "namespace:acme", revAt(10))

				return s.RevokeGrant(ctx, revGrantServer, f.fence, e, nil)
			},
			want: access.ErrNotFound,
		},
		{
			name: "a grant through a stale fence",
			run: func() error {
				stale := namespace.Fence{Namespace: revNamespace, FailoverVersion: f.fence.FailoverVersion + 1}
				e := revEntryIn(revNamespace, "namespace:acme", revAt(10))

				return s.RevokeGrant(ctx, revGrantSpace, stale, e, nil)
			},
			want: namespace.ErrFenced,
		},
		{
			name: "a join token that does not exist",
			run: func() error {
				e := []audit.Entry{revEntryIn(revNamespace, "namespace:acme", revAt(10))}

				return s.RunnerPools().RevokeJoinToken(ctx, "rjt-none", f.fence, e)
			},
			want: runner.ErrNotFound,
		},
		{
			name: "a join token without an audit entry",
			run:  func() error { return s.RunnerPools().RevokeJoinToken(ctx, f.joinToken.ID, f.fence, nil) },
			want: audit.ErrInvalidEntry,
		},
		{
			name: "a join token through a stale fence",
			run: func() error {
				stale := namespace.Fence{Namespace: revNamespace, FailoverVersion: f.fence.FailoverVersion + 1}
				e := []audit.Entry{revEntryIn(revNamespace, "namespace:acme", revAt(10))}

				return s.RunnerPools().RevokeJoinToken(ctx, f.joinToken.ID, stale, e)
			},
			want: namespace.ErrFenced,
		},
		{
			name: "a runner that does not exist",
			run: func() error {
				e := []audit.Entry{revEntryIn(revNamespace, "namespace:acme", revAt(10))}

				return s.RunnerPorts().Members.Retire(ctx, "rnr-none", revAt(10), f.fence, e)
			},
			want: runner.ErrNotFound,
		},
		{
			name: "a runner without an audit entry",
			run:  func() error { return s.RunnerPorts().Members.Retire(ctx, f.runnerID, revAt(10), f.fence, nil) },
			want: audit.ErrInvalidEntry,
		},
		{
			name: "a runner through a stale fence",
			run: func() error {
				stale := namespace.Fence{Namespace: revNamespace, FailoverVersion: f.fence.FailoverVersion + 1}
				e := []audit.Entry{revEntryIn(revNamespace, "namespace:acme", revAt(10))}

				return s.RunnerPorts().Members.Retire(ctx, f.runnerID, revAt(10), stale, e)
			},
			want: namespace.ErrFenced,
		},
		{
			name: "a provider that does not exist",
			run: func() error {
				return s.DisableProviderConfig(ctx, "none", 1, revAt(10), revEntry("sign-in-provider:none", revAt(10)))
			},
			want: session.ErrProviderNotFound,
		},
		{
			name: "a provider at another version",
			run: func() error {
				return s.DisableProviderConfig(
					ctx,
					revProvider,
					7,
					revAt(10),
					revEntry("sign-in-provider:x", revAt(10)),
				)
			},
			want: session.ErrProviderChanged,
		},
	}

	for _, tc := range cases {
		if err := tc.run(); !errors.Is(err, tc.want) {
			t.Fatalf("%s in maintenance mode: err = %v, want %v", tc.name, err, tc.want)
		}
	}

	revSessionActive(t, s, revSessionOne)
	revTokenActive(t, s, revPersonalToken)
	revGrantLive(t, s, revGrantServer)
	revGrantLive(t, s, revGrantSpace)
	revJoinTokenLive(t, s, f)
	revProviderUntouched(t, s)

	if got := revAuditTotal(t, s); got != before {
		t.Fatalf("audit entries after the refused writes = %d, want %d", got, before)
	}

	// A second end of one session is refused and appends no entry.
	end := revEntry(session.FullRef(revPerson, revSessionOne), revAt(11))
	if err := s.EndSession(ctx, revSessionOne, session.ReasonSignOut, end); err != nil {
		t.Fatalf("end the session: %v", err)
	}

	again := revAuditTotal(t, s)
	if err := s.EndSession(ctx, revSessionOne, session.ReasonSignOut, end); !errors.Is(err, session.ErrEnded) {
		t.Fatalf("end the session twice: err = %v, want ErrEnded", err)
	}

	if got := revAuditTotal(t, s); got != again {
		t.Fatalf("audit entries after the second end = %d, want %d", got, again)
	}

	// A second disable of one principal is refused and appends no entry.
	disable := revEntry(principal.Ref(revPerson), revAt(12))
	if err := s.DisablePrincipal(ctx, revPerson, disable); err != nil {
		t.Fatalf("disable the principal: %v", err)
	}

	again = revAuditTotal(t, s)
	if err := s.DisablePrincipal(ctx, revPerson, disable); !errors.Is(err, principal.ErrDisabled) {
		t.Fatalf("disable the principal twice: err = %v, want ErrDisabled", err)
	}

	if got := revAuditTotal(t, s); got != again {
		t.Fatalf("audit entries after the second disable = %d, want %d", got, again)
	}
}

// revStageAndApply stages the export of s on s and applies it: a restore of
// the state that the export holds.
func revStageAndApply(t *testing.T, s RevocationStore, export revExport) {
	t.Helper()

	if _, err := s.StagePortable(t.Context(), export.sink.read, export.sum, export.files, StageAs(t, s)); err != nil {
		t.Fatalf("stage the export: %v", err)
	}

	if err := s.ApplyStaging(t.Context()); err != nil {
		t.Fatalf("apply the staged export: %v", err)
	}
}

// revExport holds one export of a store.
type revExport struct {
	sink  *portableSink
	sum   backup.Summary
	files []backup.PortableFile
}

// revTakeExport exports s.
func revTakeExport(t *testing.T, s RevocationStore) revExport {
	t.Helper()

	sink, sum, files := exportPortable(t, s)

	return revExport{sink: sink, sum: sum, files: files}
}

// revJournaled is one revocation of a stay, with the entry that a restore
// writes for it.
type revJournaled struct {
	run    func(t *testing.T, s RevocationStore, f revFixture)
	kind   string
	object string
}

// revStayRevocations lists one revocation of each kind, as an operator makes
// them in a stay in maintenance mode.
func revStayRevocations() []revJournaled {
	return []revJournaled{
		{
			kind: maintenance.RevokedSession, object: session.FullRef(revPerson, revSessionOne),
			run: revStayEndSession,
		},
		{kind: maintenance.RevokedToken, object: accesstoken.Ref(revServiceToken), run: revStayRevokeToken},
		{kind: maintenance.RevokedTempAdmin, object: tempadmin.Ref(revTempAdmin), run: revStayExpire},
		{kind: maintenance.RevokedPrincipal, object: principal.Ref(revService), run: revStayDisablePrincipal},
		{kind: maintenance.RevokedGrant, object: identity.ServerObject().String(), run: revStayRevokeServerGrant},
		{
			kind: maintenance.RevokedGrant, object: identity.NamespaceObject(revNamespace).String(),
			run: revStayRevokeSpaceGrant,
		},
		{
			kind: maintenance.RevokedJoinToken, object: "namespace:" + revNamespace.String(),
			run: revStayRevokeJoinToken,
		},
		{kind: maintenance.RevokedProvider, object: "sign-in-provider:" + revProvider, run: revStayDisableProvider},
		{kind: maintenance.RevokedRunner, object: "namespace:" + revNamespace.String(), run: revStayRetireRunner},
		{
			kind: maintenance.RevokedProvider, object: "sign-in-provider:" + revSpareProvider,
			run: revStayDeleteProvider,
		},
	}
}

func revStayEndSession(t *testing.T, s RevocationStore, _ revFixture) {
	t.Helper()

	e := revEntry(session.FullRef(revPerson, revSessionOne), revAt(30))
	if err := s.EndSession(t.Context(), revSessionOne, session.ReasonEnded, e); err != nil {
		t.Fatalf("end the session in maintenance mode: %v", err)
	}
}

func revStayRevokeToken(t *testing.T, s RevocationStore, _ revFixture) {
	t.Helper()

	e := revEntry(accesstoken.Ref(revServiceToken), revAt(31))
	if err := s.RevokeToken(t.Context(), revServiceToken, e); err != nil {
		t.Fatalf("revoke the token in maintenance mode: %v", err)
	}
}

func revStayExpire(t *testing.T, s RevocationStore, _ revFixture) {
	t.Helper()

	e := revEntry(tempadmin.Ref(revTempAdmin), revAt(32))
	if err := s.ExpireTempAdmin(t.Context(), revTempAdmin, e); err != nil {
		t.Fatalf("expire the temporary administrator in maintenance mode: %v", err)
	}
}

func revStayDisablePrincipal(t *testing.T, s RevocationStore, _ revFixture) {
	t.Helper()

	e := revEntry(principal.Ref(revService), revAt(33))
	if err := s.DisablePrincipal(t.Context(), revService, e); err != nil {
		t.Fatalf("disable the principal in maintenance mode: %v", err)
	}
}

func revStayRevokeServerGrant(t *testing.T, s RevocationStore, _ revFixture) {
	t.Helper()

	e := revEntry(identity.ServerObject().String(), revAt(34))
	if err := s.RevokeGrant(t.Context(), revGrantServer, revNoFence(), e, nil); err != nil {
		t.Fatalf("revoke the grant of the installation in maintenance mode: %v", err)
	}
}

func revStayRevokeSpaceGrant(t *testing.T, s RevocationStore, f revFixture) {
	t.Helper()

	e := revEntryIn(revNamespace, identity.NamespaceObject(revNamespace).String(), revAt(35))
	if err := s.RevokeGrant(t.Context(), revGrantSpace, f.fence, e, nil); err != nil {
		t.Fatalf("revoke the grant of the namespace in maintenance mode: %v", err)
	}
}

func revStayRevokeJoinToken(t *testing.T, s RevocationStore, f revFixture) {
	t.Helper()

	e := []audit.Entry{revEntryIn(revNamespace, "namespace:"+revNamespace.String(), revAt(36))}
	if err := s.RunnerPools().RevokeJoinToken(t.Context(), f.joinToken.ID, f.fence, e); err != nil {
		t.Fatalf("revoke the join token in maintenance mode: %v", err)
	}
}

func revStayRetireRunner(t *testing.T, s RevocationStore, f revFixture) {
	t.Helper()

	e := []audit.Entry{revEntryIn(revNamespace, "namespace:"+revNamespace.String(), revAt(38))}
	if err := s.RunnerPorts().Members.Retire(t.Context(), f.runnerID, revAt(38), f.fence, e); err != nil {
		t.Fatalf("retire the runner in maintenance mode: %v", err)
	}
}

func revStayDisableProvider(t *testing.T, s RevocationStore, _ revFixture) {
	t.Helper()

	e := revEntry("sign-in-provider:"+revProvider, revAt(37))
	if err := s.DisableProviderConfig(t.Context(), revProvider, 1, revAt(37), e); err != nil {
		t.Fatalf("disable the provider in maintenance mode: %v", err)
	}
}

func revStayDeleteProvider(t *testing.T, s RevocationStore, _ revFixture) {
	t.Helper()

	e := revEntry("sign-in-provider:"+revSpareProvider, revAt(38))
	if err := s.DeleteProviderConfig(t.Context(), revSpareProvider, e); err != nil {
		t.Fatalf("delete the provider in maintenance mode: %v", err)
	}

	revSpareProviderGone(t, s)
}

// revCheckRestored fails the test unless every revocation of the stay shows
// in the restored rows, and the rows that nobody revoked stay as the backup
// holds them.
func revCheckRestored(t *testing.T, s RevocationStore, f revFixture) {
	t.Helper()

	revSessionEnded(t, s, revSessionOne, session.ReasonEnded)
	revSessionActive(t, s, revSessionTwo)
	revTokenRevoked(t, s, revServiceToken)
	revTokenActive(t, s, revPersonalToken)
	revTokenActive(t, s, revKeepToken)
	revPrincipalDisabled(t, s, revTempAdmin)
	revTokenRevoked(t, s, revTempToken)
	revPrincipalDisabled(t, s, revService)
	revPrincipalEnabled(t, s, revPerson)
	revGrantRevoked(t, s, revGrantServer)
	revGrantRevoked(t, s, revGrantSpace)
	revJoinTokenRevoked(t, s, f)
	revRunnerRetired(t, s, f)
	revProviderDisabled(t, s, 2)
	revSessionEnded(t, s, revSessionIdp, session.ReasonEnded)
	revSpareProviderDisabled(t, s)
}

// testRevocationSurvivesRestore proves the restore interaction. A backup
// holds the access data from before the incident. An operator then enters
// maintenance mode and makes every kind of revocation. The restore swaps in
// the older access data, and the swap applies the revocations of the stay
// again: the restored rows end, are revoked or are disabled, the rows that
// nobody revoked stay as the backup holds them, and the swap writes one audit
// entry for each revocation that it applied, after the log of the backup.
func testRevocationSurvivesRestore(t *testing.T, s RevocationStore) {
	t.Helper()

	f := seedAccess(t, s)

	export := revTakeExport(t, s)

	revEnter(t, s)

	stay := revStayRevocations()
	for _, r := range stay {
		r.run(t, s, f)
	}

	revStageAndApply(t, s, export)

	revCheckRestored(t, s, f)

	if got, err := s.GetState(t.Context()); err != nil || got != maintenance.StateMaintenance {
		t.Fatalf("installation state after the restore = %q, %v, want maintenance without error", got, err)
	}

	revCheckReplayEntries(t, s, stay)

	// A second restore in the same stay applies the journal again. The
	// staged rows are the rows of the export, so the replay must end in the
	// same state and write no second set of entries.
	total := revAuditTotal(t, s)

	revStageAndApply(t, s, export)

	revCheckRestored(t, s, f)

	if got := revAuditTotal(t, s); got != total {
		t.Fatalf("audit entries after the second restore = %d, want %d", got, total)
	}

	if again := revReplayEntries(t, s); len(again) != len(stay) {
		t.Fatalf("replay audit entries after the second restore = %d, want %d", len(again), len(stay))
	}
}

// revClaimKind is the durable kind of the operation that the claim cases
// hold for the runner.
const revClaimKind durable.Kind = "revocation-suite"

// testRunnerRetireEndsClaim proves the second statement of the replay of a
// runner retire. The claims of the runners are in a control table, which a
// restore keeps, so the swap does not bring a claim back. The statement is
// still a layer of its own: a claim that the retired runner holds when the
// swap runs, for example one that committed just after the retire, ends with
// the replay. The case claims an operation for the runner after the retire,
// restores the backup, and expects the claim ended. A second restore changes
// nothing.
func testRunnerRetireEndsClaim(t *testing.T, s RevocationStore) {
	t.Helper()

	f := seedAccess(t, s)
	ops := s.DurableOperationsPort()

	start := durable.StartRequest{Key: "revocation-suite/claim", Kind: revClaimKind, Version: 0, Input: nil}
	if _, _, err := ops.Start(t.Context(), start); err != nil {
		t.Fatalf("start the operation: %v", err)
	}

	export := revTakeExport(t, s)

	revEnter(t, s)
	revStayRetireRunner(t, s, f)

	held, err := ops.Claim(t.Context(), durable.ClaimRequest{
		Owner: f.runnerID, Kinds: []durable.Kind{revClaimKind}, MaxVersions: nil, Lease: time.Hour,
		TakeCanceled: false,
	})
	if err != nil {
		t.Fatalf("claim the operation for the runner: %v", err)
	}

	if got := revOperation(t, s, held.ID).Claim.Owner; got != f.runnerID {
		t.Fatalf("claim owner before the restore = %q, want %q", got, f.runnerID)
	}

	revStageAndApply(t, s, export)

	revRunnerRetired(t, s, f)
	revClaimEnded(t, s, held.ID, f.runnerID)

	total := revAuditTotal(t, s)
	after := revOperation(t, s, held.ID)

	revStageAndApply(t, s, export)

	revRunnerRetired(t, s, f)
	revClaimEnded(t, s, held.ID, f.runnerID)

	if got := revAuditTotal(t, s); got != total {
		t.Fatalf("audit entries after the second restore = %d, want %d", got, total)
	}

	if again := revOperation(t, s, held.ID); again.Claim != after.Claim || again.State != after.State {
		t.Fatalf("operation after the second restore = %+v, want %+v", again, after)
	}
}

// revOperation reads the durable operation id.
func revOperation(t *testing.T, s RevocationStore, id durable.ID) durable.Operation {
	t.Helper()

	op, err := s.DurableOperationsPort().Get(t.Context(), id)
	if err != nil {
		t.Fatalf("get the operation %s: %v", id, err)
	}

	return op
}

// revClaimEnded fails the test when the operation id is still claimed by
// owner.
func revClaimEnded(t *testing.T, s RevocationStore, id durable.ID, owner string) {
	t.Helper()

	if op := revOperation(t, s, id); op.Claim.Owner == owner {
		t.Fatalf("operation %s is still claimed by the retired runner %s: %+v", id, owner, op.Claim)
	}
}

// revCheckReplayEntries fails the test unless the log holds one replay entry
// for each revocation of the stay, in order.
func revCheckReplayEntries(t *testing.T, s RevocationStore, stay []revJournaled) {
	t.Helper()

	entries := revReplayEntries(t, s)
	if len(entries) != len(stay) {
		t.Fatalf("replay audit entries = %d, want %d (one for each revocation)", len(entries), len(stay))
	}

	for i, r := range stay {
		e := entries[i]
		if e.Subject != "alice" || e.Object != r.object || e.Outcome != audit.OutcomeSuccess ||
			e.Namespace != audit.ServerScope || e.ValuePath != "/maintenance/revocations/"+r.kind {
			t.Fatalf("replay entry %d = %+v, want the subject alice, the object %q and the kind %q",
				i, e, r.object, r.kind)
		}
	}
}

// replayOutcomes counts the replay entries by outcome.
type replayOutcomes struct {
	applied int
	denied  int
}

// revCountReplayOutcomes returns the count of the replay entries that applied
// a revocation and the count of those that refused a row. It fails the test
// for an entry with another outcome, and for an entry that names the actor of
// a hostile row.
func revCountReplayOutcomes(t *testing.T, entries []audit.Entry) replayOutcomes {
	t.Helper()

	var out replayOutcomes

	for i := range entries {
		switch entries[i].Outcome {
		case audit.OutcomeSuccess:
			out.applied++
		case audit.OutcomeDenied:
			out.denied++
		case audit.OutcomeFailure:
			t.Fatalf("replay entry %d has the outcome failure", i)
		default:
			t.Fatalf("replay entry %d has the unknown outcome %q", i, entries[i].Outcome)
		}

		if entries[i].Subject == "mallory" {
			t.Fatalf("replay entry %d = %+v, names the actor of a hostile row", i, entries[i])
		}
	}

	return out
}

// testReplayRefusesHostileRows proves that the replay checks each journal row
// as if every layer before it had failed. The CHECK constraint of the journal
// and the typed writes of the store keep these rows out; the case writes them
// with the raw insert. A row of an unknown kind, and a row whose target is
// not an ID, change nothing and get an audit entry with the outcome denied.
// The valid revocation of the same stay is applied, and the restore does not
// fail.
func testReplayRefusesHostileRows(t *testing.T, s RevocationStore) {
	t.Helper()

	f := seedAccess(t, s)
	export := revTakeExport(t, s)

	revEnter(t, s)

	valid := revEntry(session.FullRef(revPerson, revSessionOne), revAt(30))
	if err := s.EndSession(t.Context(), revSessionOne, session.ReasonEnded, valid); err != nil {
		t.Fatalf("end the session in maintenance mode: %v", err)
	}

	// Each row names a target that the replay would revoke, so that a row
	// which got through would show in the state below.
	hostile := []struct {
		kind, target, actor, object string
	}{
		{maintenance.RevokedToken, revKeepToken + "%", "mallory", "token:x"},
		{maintenance.RevokedToken, "' OR '1'='1", "mallory", "token:x"},
		{maintenance.RevokedToken, "", "mallory", "token:x"},
		{maintenance.RevokedSession, revSessionTwo + "\n", "mallory", "token:x"},
		{maintenance.RevokedPrincipal, "../" + revPerson, "mallory", "token:x"},
		{maintenance.RevokedToken, revKeepToken, "mallory\nforged", "token:x"},
		{maintenance.RevokedToken, revKeepToken, "mallory", "token:x\nforged"},
		{maintenance.RevokedToken, revKeepToken, "mallory" + strings.Repeat("a", 256), "token:x"},
		{maintenance.RevokedToken, revKeepToken, "mallory", "token:" + strings.Repeat("a", 256)},
		{maintenance.RevokedToken, revKeepToken, "", "token:x"},
		{maintenance.RevokedToken, revKeepToken, "mallory", ""},
	}

	if s.KeepsUnknownKinds() {
		hostile = append(hostile, struct{ kind, target, actor, object string }{
			"wipe", revKeepToken, "mallory", "token:x",
		})
	}

	for _, h := range hostile {
		row := RawRevocation{
			Kind: h.kind, Target: h.target, Actor: h.actor, Object: h.object, AtMs: revAt(31).UnixMilli(),
		}

		if err := s.InsertRawRevocation(t.Context(), row); err != nil {
			t.Fatalf("insert the hostile row %q %q: %v", h.kind, h.target, err)
		}
	}

	// A row without a time after the epoch would end its target at the time
	// 0, which reads as not ended. The replay refuses it as it refuses the
	// rows above, and its denied entry takes a fixed time.
	for _, atMs := range []int64{time.Time{}.UnixMilli(), 0, -1} {
		noTime := RawRevocation{
			Kind: maintenance.RevokedToken, Target: revKeepToken, Actor: "mallory", Object: "token:x",
			AtMs: atMs,
		}

		if err := s.InsertRawRevocation(t.Context(), noTime); err != nil {
			t.Fatalf("insert the row with the time %d: %v", atMs, err)
		}
	}

	denials := len(hostile) + 3

	revStageAndApply(t, s, export)

	revSessionEnded(t, s, revSessionOne, session.ReasonEnded)
	revSessionActive(t, s, revSessionTwo)
	revTokenActive(t, s, revKeepToken)
	revTokenActive(t, s, revPersonalToken)
	revPrincipalEnabled(t, s, revPerson)
	revGrantLive(t, s, revGrantServer)
	revJoinTokenLive(t, s, f)

	got := revCountReplayOutcomes(t, revReplayEntries(t, s))

	if got.applied != 1 || got.denied != denials {
		t.Fatalf("replay entries: %d applied and %d denied, want 1 and %d", got.applied, got.denied, denials)
	}
}

// testRefusedProviderDeleteJournalsNothing proves that the journal row of a
// delete of a provider is part of the transaction of the delete: a delete
// that fails writes no audit entry and no journal row. After a restore of
// the older data, the replay finds no row, so it writes no replay entry and
// leaves the provider enabled.
func testRefusedProviderDeleteJournalsNothing(t *testing.T, s RevocationStore) {
	t.Helper()

	seedAccess(t, s)

	export := revTakeExport(t, s)

	revEnter(t, s)

	cases := []struct {
		name string
		id   string
		want error
	}{
		{name: "a provider with a person", id: revProvider, want: session.ErrProviderInUse},
		{name: "a provider that does not exist", id: "missing-idp", want: session.ErrProviderNotFound},
	}

	for _, tc := range cases {
		before := revAuditTotal(t, s)

		e := revEntry("sign-in-provider:"+tc.id, revAt(50))
		e.Action = "server:manage-sign-in"

		if err := s.DeleteProviderConfig(t.Context(), tc.id, e); !errors.Is(err, tc.want) {
			t.Fatalf("%s: delete err = %v, want %v", tc.name, err, tc.want)
		}

		if got := revAuditTotal(t, s); got != before {
			t.Fatalf("%s: audit entries after the refused delete = %d, want %d", tc.name, got, before)
		}
	}

	revStageAndApply(t, s, export)

	if entries := revReplayEntries(t, s); len(entries) != 0 {
		t.Fatalf("replay audit entries = %d, want none: a refused delete journals nothing", len(entries))
	}

	revProviderUntouched(t, s)
}

// testRevocationStayIsBounded proves what the swap applies again: the
// revocations of the stay that the restore runs in. A revocation made in
// normal operation, and a revocation of an earlier stay, belong to the
// access data that the restore replaces, so the restored rows come back as
// the backup holds them.
func testRevocationStayIsBounded(t *testing.T, s RevocationStore) {
	t.Helper()

	seedAccess(t, s)

	export := revTakeExport(t, s)

	// In normal operation: no journal row.
	normal := revEntry(accesstoken.Ref(revPersonalToken), revAt(40))
	if err := s.RevokeToken(t.Context(), revPersonalToken, normal); err != nil {
		t.Fatalf("revoke the token in normal operation: %v", err)
	}

	// In an earlier stay: the next enter starts a new stay.
	revEnter(t, s)

	earlier := revEntry(session.FullRef(revPerson, revSessionOne), revAt(41))
	if err := s.EndSession(t.Context(), revSessionOne, session.ReasonEnded, earlier); err != nil {
		t.Fatalf("end the session in the first stay: %v", err)
	}

	revLeave(t, s)
	revEnter(t, s)

	revStageAndApply(t, s, export)

	revTokenActive(t, s, revPersonalToken)
	revSessionActive(t, s, revSessionOne)

	if entries := revReplayEntries(t, s); len(entries) != 0 {
		t.Fatalf("replay audit entries = %d, want none for revocations outside the stay", len(entries))
	}
}

// testTempAdminSweepRuns proves the expiry through the service that runs it
// at the fixed time: the sweep of the temporary administrators ends the
// record in maintenance mode, with the same effects and one audit entry. The
// sweep logs a failed expiry and goes on, so a refusal would show only in its
// result.
func testTempAdminSweepRuns(t *testing.T, s RevocationStore) {
	t.Helper()

	seedAccess(t, s)
	revEnter(t, s)

	expiry := revAt(2).Add(tempadmin.Lifetime)
	svc, err := tempadmin.NewService(tempadmin.Config{
		Store: s, Reviews: nil, Now: func() time.Time { return expiry.Add(time.Minute) }, Logger: nil,
	})
	if err != nil {
		t.Fatalf("temporary administrator service: %v", err)
	}

	before := revAuditTotal(t, s)

	done, err := svc.Sweep(t.Context(), revEntry(tempadmin.Ref(revTempAdmin), revAt(10)))
	if err != nil || len(done) != 1 || done[0] != revTempAdmin {
		t.Fatalf("sweep in maintenance mode = %v, %v, want the expiry of %q", done, err, revTempAdmin)
	}

	revPrincipalDisabled(t, s, revTempAdmin)
	revTokenRevoked(t, s, revTempToken)

	if got := revAuditTotal(t, s); got != before+1 {
		t.Fatalf("audit entries after the sweep = %d, want %d", got, before+1)
	}
}
