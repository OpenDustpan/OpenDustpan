package storetest

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/calendar"
	"opendustpan.org/opendustpan/internal/ident"
	"opendustpan.org/opendustpan/pkg/access"
	"opendustpan.org/opendustpan/pkg/accesstoken"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/backup"
	"opendustpan.org/opendustpan/pkg/backup/blobguard"
	"opendustpan.org/opendustpan/pkg/breakglass"
	"opendustpan.org/opendustpan/pkg/durable"
	"opendustpan.org/opendustpan/pkg/forge"
	"opendustpan.org/opendustpan/pkg/gate"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/inventory"
	"opendustpan.org/opendustpan/pkg/maintenance"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/notify"
	"opendustpan.org/opendustpan/pkg/notify/notifytest"
	"opendustpan.org/opendustpan/pkg/principal"
	"opendustpan.org/opendustpan/pkg/savedview"
	"opendustpan.org/opendustpan/pkg/secret"
	"opendustpan.org/opendustpan/pkg/session"
	"opendustpan.org/opendustpan/pkg/stack"
	"opendustpan.org/opendustpan/pkg/workspace"
)

// BackupStore is every port the backup suite exercises: the domain stores it
// fills and compares through, and the backup ports under test. RunBackup
// exercises them the same way Run covers the namespace and stack ports.
// Every store adapter runs the suite once it implements the ports (issues
// #143 and #144); until then a fake in backup_test.go runs it and proves
// each case can fail.
type BackupStore interface { //nolint:interfacebloat // A whole-store round trip fills every table, so the union holds every port.
	namespace.Store
	stack.Store
	stack.UpdateStore
	stack.CheckpointStore
	stack.EventStore
	stack.JournalStore
	stack.BuiltCheckpointStore
	secret.KeyStore
	inventory.Reader
	inventory.DeletedReader
	workspace.Store
	workspace.StateStore
	backup.KeyPeeker
	backup.Registry
	blobguard.Watch
	// GetRestore and ListRestores read the restores table, as
	// backup.RestoreOperations does. The cases that write restore rows
	// assert the store to the restore operation ports.
	GetRestore(ctx context.Context, id backup.RestoreID) (backup.RestoreRecord, error)
	ListRestores(ctx context.Context) ([]backup.RestoreRecord, error)
	backup.Inspector
	backup.Coverage
	maintenance.Store
	// DiscardStaging removes the staging area, if any. It leaves the live data
	// unchanged.
	DiscardStaging(ctx context.Context) error
	// DurableOperationsPort returns the store of durable operations that
	// shares the database of the registry. The backup cases start, claim,
	// cancel and fail operations through it, and read the steps that
	// FinishBackup records.
	DurableOperationsPort() durable.Store
	audit.Store
}

// BackupFactory returns a fresh, empty store for one subtest.
type BackupFactory func(t *testing.T) BackupStore

// Reopener is an optional BackupStore capability for the close-and-reopen
// case: Close releases the store and Reopen returns a new handle to the same
// backing storage, as reopening the database file or reconnecting does.
type Reopener interface {
	BackupStore
	// Close releases the store.
	Close() error
	// Reopen returns a new handle to the same backing storage.
	Reopen() BackupStore
}

// StagingWitness is an optional BackupStore capability for the
// stage-replaces case: it exposes the identity of the current staging area,
// so the suite can prove a new stage discards the previous one. IDs are
// opaque: a file path for SQLite, a schema name for PostgreSQL, a generation
// for the fake. An empty ID means no staging area exists.
type StagingWitness interface {
	BackupStore
	// CurrentStagingID returns the ID of the current staging area, or empty.
	CurrentStagingID() string
	// StagingAlive reports whether the staging area with id still exists.
	StagingAlive(id string) bool
}

// RunBackup executes the conformance suite of the backup ports against stores
// produced by newStore.
func RunBackup(t *testing.T, newStore BackupFactory) {
	t.Helper()

	cases := []struct {
		test func(t *testing.T, newStore BackupFactory)
		name string
	}{
		{name: "the table list names every table of the schema", test: testBackupTablesCoverSchema},
		{name: "column lists cover every column of the schema", test: testBackupColumnsCoverSchema},
		{name: "backups table creates, finishes, gets and lists", test: testBackupRegistry},
		{
			name: "a row that is not ended stores NULL, a row that is ended stores its time",
			test: testBackupEndedShape,
		},
		{name: "one backup runs at a time", test: testBackupOneRunning},
		{name: "starting a backup stores the row and the operation together", test: testBackupStart},
		{name: "a refused start leaves no operation", test: testBackupStartRefused},
		{name: "a claimed start runs the operation under the claim of the starter", test: testBackupStartClaimed},
		{name: "a refused claimed start leaves no row and no operation", test: testBackupStartClaimedRefused},
		{name: "a worker takes a claimed start after its claim expires", test: testBackupStartClaimedTakeover},
		{name: "a start with an invalid request stores no row", test: testBackupStartInvalid},
		{name: "a start with a foreign key or kind stores no row", test: testBackupStartForeign},
		{name: "finishing a backup records the step in the same call", test: testBackupFinishRecordsStep},
		{name: "a stale claim cannot finish a backup", test: testBackupFinishStaleClaim},
		{name: "a canceled operation cannot finish a backup", test: testBackupFinishCanceled},
		{name: "a fence that is not a live claim cannot finish a backup", test: testBackupFinishForeignFence},
		{name: "a refused finish records no step", test: testBackupFinishRollsBack},
		{name: "ended operations fail their running rows", test: testBackupFailEndedFailed},
		{name: "canceled operations fail their running rows", test: testBackupFailEndedCanceled},
		{name: "live operations keep their running rows", test: testBackupFailEndedLive},
		{name: "an expired claim keeps its running row", test: testBackupFailEndedExpiredClaim},
		{name: "a backup row survives close and reopen", test: testBackupRowSurvivesReopen},
		{name: "the running work read returns none for an empty store", test: testRunningNone},
		{name: "the running work read returns the running backup", test: testRunningBackup},
		{name: "the running work read returns none when every backup ended", test: testRunningEnded},
		{name: "a takeover leaves the backup row running", test: testRunningTakeover},
		{name: "a refused start gives no running work", test: testRunningRefusedStart},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tc.test(t, newStore)
		})
	}
}

// backupSeed holds the rows a fill wrote, so the comparison knows what to read.
type backupSeed struct {
	acme       namespace.Namespace
	other      namespace.Namespace
	prod       stack.Stack
	dev        stack.Stack
	otherProd  stack.Stack
	finished   stack.Update
	waiting    stack.Update
	checkpoint stack.Checkpoint
	events     []stack.Event
	scopes     []secret.Scope
	net        workspace.Workspace
	plain      workspace.Workspace
	current    workspace.StateVersion
	pending    workspace.StateVersion
	backedUp   backup.Record
	audit      []audit.Entry
	imports    *seededImports
	profiles   *seededProfiles
	issuer     *seededIssuer
	runners    *seededRunners
}

// seedBackupStore fills s through the real ports: namespaces, stacks with
// tags, a finished and a waiting update, one checkpoint, events, secret keys
// under two wrappers, workspaces with a finalized and a pending state
// version, one deleted stack, and one finished backup row. Inventory rows
// follow from the tool writes. The finished update carries a creator, the waiting update is a
// restore with a creator and a restore source, the checkpoint manages one
// resource, and the pending state version is a restore of the finalized one,
// so the round trip proves the columns that later migrations added survive
// an export and an import. A store that keeps imports gets three imports
// (see seedBackupImports).
func seedBackupStore(t *testing.T, s BackupStore) backupSeed {
	t.Helper()

	ctx := t.Context()
	f := newFixture(t, s)
	prod := mustGetStack(t, s, f.ref)

	other := namespace.Namespace{
		ID:                  namespace.NewID(),
		CreatedAt:           now(),
		Name:                "other",
		PrimaryInstallation: "us-east",
		FailoverVersion:     1,
		Overrides:           namespace.Overrides{Forced: false, Unlocked: false},
	}

	if err := s.CreateNamespace(ctx, other); err != nil {
		t.Fatalf("create namespace %q: %v", other.Name, err)
	}

	// The switches are on in both namespaces, and on in one unit of each
	// kind, so the round trip carries every switch column with a value that
	// is not the default (ADR 44, decision 6).
	if err := s.SetOverrides(ctx, "acme", namespace.Overrides{Forced: true, Unlocked: false}, f.fence); err != nil {
		t.Fatalf("set acme switches: %v", err)
	}

	err := s.SetOverrides(
		ctx,
		other.Name,
		namespace.Overrides{Forced: false, Unlocked: true},
		other.Fence(),
	)
	if err != nil {
		t.Fatalf("set other switches: %v", err)
	}

	acme := mustGetNamespace(t, s, "acme")
	other = mustGetNamespace(t, s, "other")
	dev := mustCreateStack(t, s, acme, "dev")
	otherProd := mustCreateStack(t, s, other, "prod")

	// A deleted stack keeps its row and moves its inventory entry to the
	// deleted units (ADR 47), so the round trip carries both.
	gone := mustCreateStack(t, s, acme, "gone")
	err = s.ForceDeleteStack(
		ctx,
		gone.Ref,
		acme.Fence(),
		inventory.Deletion{Entry: deleteEntry(acme.Name), PurgeAfter: purgeAt()},
	)
	if err != nil {
		t.Fatalf("delete stack %s: %v", gone.Ref, err)
	}

	running := f.run(t, f.ref, "u1", stack.Plan{
		Kind:         stack.KindUpdate,
		DryRun:       false,
		CreatedBy:    "alice@example.com",
		RestoredFrom: 0, GitHead: "",
	})
	checkpoint := f.checkpoint(t, running, 0, resourcedDeployment)

	if _, err := s.PutCheckpoint(ctx, checkpoint, f.fence); err != nil {
		t.Fatalf("put checkpoint: %v", err)
	}

	batch := newEvents(running, 1, 0, 1)
	if err := s.AppendEvents(ctx, batch, f.fence); err != nil {
		t.Fatalf("append events: %v", err)
	}

	finished := f.end(t, running, stack.StatusSucceeded)
	waiting := f.create(t, f.ref, "u2", stack.Plan{
		Kind:         stack.KindRestore,
		DryRun:       false,
		CreatedBy:    "bob@example.com",
		RestoredFrom: finished.Version, GitHead: "",
	})

	scopes := []secret.Scope{
		seedBackupKey(t, s, acme, prod, "kek-1"),
		seedBackupKey(t, s, acme, dev, "kek-2"),
	}

	net := setUnitSwitchesOnly(t, s, acme, createTestWorkspace(t, s, acme, "network"),
		namespace.Overrides{Forced: true, Unlocked: true})
	locked := lockTestWorkspace(t, s, acme, net, "alice")
	declared := newPendingVersion(t, locked, locked.Lock.ID, stateInput("l1", "v1", 1))
	createPendingVersion(t, s, acme, declared)

	current := finalizedVersion(declared, now().Add(2*time.Hour), "v1")
	if _, err := s.RecordUploadAttempt(ctx, current.ID, current.Content, acme.Fence()); err != nil {
		t.Fatalf("record upload attempt: %v", err)
	}

	if err := s.FinalizeStateVersion(ctx, current, acme.Fence()); err != nil {
		t.Fatalf("finalize state version: %v", err)
	}

	pending := newPendingVersion(t, locked, locked.Lock.ID, stateInput("l1", "v2", 2))
	pending.RestoredFrom = current.ID
	createPendingVersion(t, s, acme, pending)

	plain := setUnitSwitchesOnly(t, s, other, createTestWorkspace(t, s, other, "plain"),
		namespace.Overrides{Forced: false, Unlocked: true})
	backedUp := seedBackupRecord(t, s)
	entries := seedBackupAudit(t, s, acme)
	seedBackupPrincipals(t, s)
	seedBackupTokens(t, s)
	seedBackupAccess(t, s)
	seedBackupPurge(t, s, acme)
	seedBackupSignIn(t, s)
	imports := seedBackupImports(t, s, acme)
	seedBlameRows(t, s, blameSeed{ns: acme, w: net, first: current.ID.String(), second: pending.ID.String()})
	profiles := seedBackupProfiles(t, s, acme)
	issuer := seedBackupIssuer(t, s, acme)
	runners := seedBackupRunners(t, s, acme)
	seedBackupGate(t, s, acme)
	seedBackupSavedViews(t, s, acme)
	seedBackupDrift(t, s, acme)
	seedBackupGitHost(t, s, acme)
	seedBackupSinks(t, s, acme)
	seedBackupTriggers(t, s, acme)
	seedBackupItemKeys(t, s, acme)
	seedBackupBreakGlass(t, s, acme)

	return backupSeed{
		acme:       acme,
		other:      other,
		prod:       prod,
		dev:        dev,
		otherProd:  otherProd,
		finished:   finished,
		waiting:    waiting,
		checkpoint: checkpoint,
		events:     batch.Events,
		scopes:     scopes,
		net:        net,
		plain:      plain,
		current:    current,
		pending:    pending,
		backedUp:   backedUp,
		audit:      entries,
		imports:    imports,
		profiles:   profiles,
		issuer:     issuer,
		runners:    runners,
	}
}

// seedBackupSavedViews writes two saved views of one person in the namespace
// acme, one with a label and a reference and one with the empty query, when s
// keeps saved views. A store with no saved view port skips it: the fake of this
// package's own tests is one.
func seedBackupSavedViews(t *testing.T, s BackupStore, acme namespace.Namespace) {
	t.Helper()

	keeper, ok := s.(interface{ SavedViews() savedview.Store })
	if !ok {
		return
	}

	entry := audit.Entry{
		At: now(), Namespace: acme.Name, Subject: "alice", IDPSubject: "", TokenID: "", Action: "view:create",
		Object: acme.Name.String(), Version: "", ValuePath: "", ValueCount: nil, Changes: nil, SourceIP: "192.0.2.1",
		UserAgent: "", Channel: audit.ChannelAPI, Kind: audit.KindChange, Outcome: audit.OutcomeSuccess, RequestID: "",
		ObjectID: "", PrevHash: "", Hash: "", Sequence: 0, FenceVersion: 0, PrincipalKind: "", JobID: "", Reason: "",
	}

	for i, query := range []savedview.Query{
		{Labels: []savedview.Label{{Key: "team", Value: "payments"}}, References: []string{"pulumi:acme/web/prod"}},
		{Labels: nil, References: nil},
	} {
		v := savedview.View{
			CreatedAt: now(), ID: savedview.NewID(), Namespace: acme.Name, Name: fmt.Sprintf("view %d", i),
			Owner: "prn-seedseedseedseedseed", Query: query,
		}

		if err := keeper.SavedViews().Create(t.Context(), v, acme.Fence(), entry); err != nil {
			t.Fatalf("create saved view %d: %v", i, err)
		}
	}
}

// BreakGlassPorts holds the break-glass store of one store.
type BreakGlassPorts struct {
	// Store is the store of break-glass reviews, or nil when the store keeps
	// none.
	Store breakglass.Store
}

// BreakGlassSeeder is an optional BackupStore capability: a store that keeps
// break-glass reviews gives its store, so the round trip fills and compares
// the break-glass table. A store with no break-glass port skips it: the fake
// of this package's own tests is one.
type BreakGlassSeeder interface {
	BreakGlassPorts() BreakGlassPorts
}

// seedBackupBreakGlass writes two break-glass reviews, one pending in the
// namespace acme and one accepted in audit.ServerScope, when s keeps a
// break-glass store. A store with no break-glass port skips it: the fake of
// this package's own tests is one.
func seedBackupBreakGlass(t *testing.T, s BackupStore, acme namespace.Namespace) {
	t.Helper()

	seeder, ok := s.(BreakGlassSeeder)
	if !ok {
		return
	}

	store := seeder.BreakGlassPorts().Store
	if store == nil {
		return
	}

	at := now().Add(-3 * time.Hour)
	entry := audit.Entry{
		At:            now(),
		Namespace:     acme.Name,
		Subject:       "server",
		IDPSubject:    "",
		TokenID:       "",
		Action:        "namespace:review-break-glass",
		Object:        acme.Name.String(),
		Version:       "",
		ValuePath:     "",
		ValueCount:    nil,
		Changes:       nil,
		SourceIP:      "192.0.2.1",
		UserAgent:     "test",
		Channel:       audit.ChannelAPI,
		Kind:          audit.KindChange,
		Outcome:       audit.OutcomeSuccess,
		RequestID:     "req-break-glass",
		ObjectID:      "",
		PrevHash:      "",
		Hash:          "",
		Sequence:      0,
		FenceVersion:  0,
		Reason:        "",
		PrincipalKind: "",
		JobID:         "",
	}

	// The pending review carries a notice step that is due, so the round trip
	// carries every notice column with a value that is not the default.
	pending := breakglass.Review{
		ID: ident.NewID(breakglass.IDPrefix),
		Use: breakglass.Use{
			Kind: breakglass.KindFreezeBreak, Ref: "seed-break-glass-pending", Scope: acme.Name,
			Object: "opentofu:acme/network", Subject: "alice", People: []string{"alice", "carol"},
			Reason: "Roll back the failed apply", At: at, Until: time.Time{},
		},
		State:        breakglass.StatePending,
		CreatedAt:    at,
		DueAt:        at.Add(31 * time.Hour),
		Calendar:     calendar.DefaultID,
		CalendarFrom: calendar.Scope{Namespace: acme.Name, Group: ""},
		Step:         0,
		NextNoticeAt: at.Add(time.Hour),
		Verdict: breakglass.Verdict{
			Reviewer: "", Note: "", Auth: "", AuthAt: time.Time{}, ReviewedAt: time.Time{}, ActAs: nil,
		},
		Version: 0,
	}

	if _, err := store.Open(t.Context(), pending, acme.Fence(), entry); err != nil {
		t.Fatalf("open pending break-glass review: %v", err)
	}

	// The accepted review carries a verdict and no notice step, so the round
	// trip carries every verdict column with a value that is not the default.
	inSession := breakglass.Review{
		ID: ident.NewID(breakglass.IDPrefix),
		Use: breakglass.Use{
			Kind: breakglass.KindAccountSession, Ref: "seed-break-glass-reviewed", Scope: audit.ServerScope,
			Object: "", Subject: "bob", People: []string{"bob"},
			Reason: "The console session of the break-glass account", At: at, Until: at.Add(time.Hour),
		},
		State:        breakglass.StatePending,
		CreatedAt:    at,
		DueAt:        time.Time{},
		Calendar:     calendar.DefaultID,
		CalendarFrom: calendar.Scope{Namespace: "", Group: ""},
		Step:         0,
		NextNoticeAt: time.Time{},
		Verdict: breakglass.Verdict{
			Reviewer: "", Note: "", Auth: "", AuthAt: time.Time{}, ReviewedAt: time.Time{}, ActAs: nil,
		},
		Version: 0,
	}

	serverEntry := entry
	serverEntry.Namespace = audit.ServerScope
	serverEntry.Action = "server:review-break-glass"
	serverEntry.Object = audit.ServerScope.String()

	opened, err := store.Open(t.Context(), inSession, zeroFence(), serverEntry)
	if err != nil {
		t.Fatalf("open the session break-glass review: %v", err)
	}

	accepted := opened
	accepted.State = breakglass.StateAccepted
	accepted.Verdict = breakglass.Verdict{
		Reviewer: "dana", Note: "The sign-in was necessary", Auth: breakglass.AuthMFA,
		AuthAt: at.Add(55 * time.Minute), ReviewedAt: at.Add(time.Hour), ActAs: []string{"erin"},
	}

	if err := store.Record(t.Context(), accepted, zeroFence(), serverEntry); err != nil {
		t.Fatalf("record the break-glass review: %v", err)
	}
}

// zeroFence is the fence of a write in audit.ServerScope.
func zeroFence() namespace.Fence { return namespace.Fence{Namespace: "", FailoverVersion: 0} }

// checkBreakGlassRowCounts proves that the seed wrote the two break-glass
// reviews that the round trip carries. A store with no break-glass port skips
// it: the fake of this package's own tests is one.
func checkBreakGlassRowCounts(t *testing.T, s BackupStore, sum backup.Summary) {
	t.Helper()

	seeder, ok := s.(BreakGlassSeeder)
	if !ok || seeder.BreakGlassPorts().Store == nil {
		return
	}

	const table = "break_glass_reviews"

	if got := sum.RowCounts[table]; got != 2 {
		t.Errorf("exported rows of %q = %d, want 2", table, got)
	}
}

// seedBackupItemKeys writes two item key records in the namespace acme, one live
// and one shredded, when s keeps item keys. A store with no item key index
// skips it: the fake of this package's own tests is one.
func seedBackupItemKeys(t *testing.T, s BackupStore, acme namespace.Namespace) {
	t.Helper()

	keys, ok := s.(secret.ItemKeyIndex)
	if !ok {
		return
	}

	scope, err := secret.NewScope(acme.Name, "unit-seed")
	if err != nil {
		t.Fatalf("item key scope: %v", err)
	}

	for i, kind := range []secret.ItemKind{secret.ItemKindPlan, secret.ItemKindJobLog} {
		ref, refErr := secret.NewItemRef(scope, kind, fmt.Sprintf("item-%d", i))
		if refErr != nil {
			t.Fatalf("item ref: %v", refErr)
		}

		k := secret.ItemKey{
			CreatedAt:  now(),
			ShreddedAt: time.Time{},
			Ref:        ref,
			ID:         secret.NewItemKeyID(),
			WrapperID:  "kek-1",
			Blob: secret.ItemKeyBlobInfo{
				Version: 1,
				Size:    60,
				MD5:     [16]byte{1, 2, 3},
				Attempt: "abcdefghij0123456789",
			},
		}
		if _, err = keys.CreateItemKey(t.Context(), k, acme.Fence()); err != nil {
			t.Fatalf("create item key %d: %v", i, err)
		}

		if i == 1 {
			if _, err = keys.ShredItemKey(t.Context(), ref, now(), acme.Fence()); err != nil {
				t.Fatalf("shred item key %d: %v", i, err)
			}
		}
	}
}

// seedBackupGitHost writes the registration of the installation-level GitHub
// App, the assignment of one installation to the namespace acme and one
// connection of acme, when s keeps Git host connections. A store with no Git
// host port skips it: the fake of this package's own tests is one.
func seedBackupGitHost(t *testing.T, s BackupStore, acme namespace.Namespace) {
	t.Helper()

	keeper, ok := s.(interface{ GitHost() forge.Store })
	if !ok {
		return
	}

	store := keeper.GitHost()

	entry := func(scope namespace.Name, action string) audit.Entry {
		return audit.Entry{
			At: now(), Namespace: scope, Subject: "alice", IDPSubject: "", TokenID: "", Action: action,
			Object: scope.String(), Version: "", ValuePath: "", ValueCount: nil, Changes: nil, SourceIP: "192.0.2.1",
			UserAgent: "", Channel: audit.ChannelAPI, Kind: audit.KindChange, Outcome: audit.OutcomeSuccess,
			RequestID: "", ObjectID: "", PrevHash: "", Hash: "", Sequence: 0, FenceVersion: 0, PrincipalKind: "",
			JobID: "", Reason: "",
		}
	}

	const apiURL = "https://api.github.com"

	reg := forge.Registration{
		CreatedAt: now(), UpdatedAt: now(), KeyCreatedAt: now(), EventSecretCreatedAt: now(), Kind: forge.KindGitHub,
		AppID: "12345", BaseURL: apiURL, HookID: forge.NewHookID(), KeySetID: forge.NewSetID(),
		EventSecretSetID: forge.NewSetID(), AddressDigest: forge.AddressDigest(forge.KindGitHub, apiURL, "12345"),
		SealedKey: []byte("sealed-key"), SealedEventSecret: []byte("sealed-event-secret"), Version: 1,
	}

	_, err := store.PutRegistration(t.Context(), reg, 0, entry(audit.ServerScope, "git-host-app:put"))
	if err != nil {
		t.Fatalf("put Git host registration: %v", err)
	}

	assignment := forge.Assignment{
		CreatedAt: now(), UpdatedAt: now(), InstallationID: "4242", Namespace: acme.Name, Version: 1,
	}

	_, err = store.PutAssignment(t.Context(), assignment, 0, entry(audit.ServerScope, "git-host-installation:assign"))
	if err != nil {
		t.Fatalf("assign Git host installation: %v", err)
	}

	conn := forge.ConnectionRecord{
		CreatedAt: now(), UpdatedAt: now(), Namespace: acme.Name, ID: forge.NewConnectionID(), Kind: forge.KindGitHub,
		Auth: forge.AuthApp, Scope: forge.AppScopeInstallation, InstallationID: "4242", Version: 1, Push: true,
		Default: false,
	}

	_, err = store.CreateConnection(t.Context(), conn, acme.Fence(), entry(acme.Name, "git-host-connection:create"))
	if err != nil {
		t.Fatalf("create Git host connection: %v", err)
	}
}

// seedWebhookHost is the host of the webhook sink that seedBackupSinks writes.
const seedWebhookHost = "hooks.example.com"

// seedWebhookToken returns the token in the URL of the seeded webhook sink. It
// is joined at run time, so that no credential-shaped text sits in the source.
// An export must never hold it, nor the URL that holds it.
func seedWebhookToken() string { return strings.Join([]string{"seed", "hook", "token", "7c1e"}, "-") }

// seedWebhookURL returns the URL of the seeded webhook sink.
func seedWebhookURL() string {
	return "https://" + seedWebhookHost + "/ops/" + seedWebhookToken() + "?k=1"
}

// seedBackupSinks writes a chat sink and a webhook sink in the namespace acme,
// and replaces the secret of the chat sink once, so the store holds a ref that
// no sink names any more. A store with no sink port skips it: the fake of this
// package's own tests is one.
func seedBackupSinks(t *testing.T, s BackupStore, acme namespace.Namespace) {
	t.Helper()

	keeper, ok := s.(interface{ NotificationSinks() notify.SinkStore })
	if !ok {
		return
	}

	entry := audit.Entry{
		At: now(), Namespace: acme.Name, Subject: "alice", IDPSubject: "", TokenID: "", Action: "sink:create",
		Object: acme.Name.String(), Version: "", ValuePath: "", ValueCount: nil, Changes: nil, SourceIP: "192.0.2.1",
		UserAgent: "", Channel: audit.ChannelAPI, Kind: audit.KindChange, Outcome: audit.OutcomeSuccess, RequestID: "",
		ObjectID: "", PrevHash: "", Hash: "", Sequence: 0, FenceVersion: 0, PrincipalKind: "", JobID: "", Reason: "",
	}

	chat := notify.Sink{
		ID: "snk-seedchat", Scope: acme.Name, Kind: notify.SinkChat, Name: "Chat", Platform: "slack",
		Target: "hooks.slack.com", SecretRefs: []string{"itm-seedchat1"}, CreatedBy: "alice", Version: 0,
	}
	hook := notify.Sink{
		ID: "snk-seedhook", Scope: acme.Name, Kind: notify.SinkWebhook, Name: "Hook", Platform: "",
		Target: seedWebhookHost, SecretRefs: []string{"itm-seedhook0", "itm-seedhook1", "itm-seedhook2"},
		CreatedBy: "alice", Version: 0,
	}

	put := func(sink notify.Sink, values ...string) {
		t.Helper()

		secrets := make([]notify.SinkSecret, 0, len(values))
		for i, v := range values {
			secrets = append(secrets, notify.SinkSecret{Ref: sink.SecretRefs[i], Value: []byte(v)})
		}

		sealed, err := notifytest.Sealer().Seal(t.Context(), sink, secrets)
		if err != nil {
			t.Fatalf("seal sink %s: %v", sink.ID, err)
		}

		if err = keeper.NotificationSinks().PutSink(t.Context(), sink, sealed, acme.Fence(), entry); err != nil {
			t.Fatalf("put sink %s: %v", sink.ID, err)
		}
	}

	put(chat, "https://hooks.slack.com/services/T0/B0/seed-one")
	put(hook, seedWebhookURL(), "seed-key-one", "seed-key-two")

	chat.Version, chat.SecretRefs = 1, []string{"itm-seedchat2"}
	put(chat, "https://hooks.slack.com/services/T0/B0/seed-two")
}

// DriftSeeder is the capability of a backup store that can write a drift result
// of a unit. The store of a SQL adapter needs direct SQL for the job that the
// result names, so the test package of the adapter implements it.
type DriftSeeder interface {
	// SeedDriftResult records one drift result of a unit in ns, with one changed
	// resource.
	SeedDriftResult(t *testing.T, ns namespace.Namespace)
}

// seedBackupDrift writes one drift result in the namespace acme, when s can. A
// store with no drift seeder skips it: the fake of this package's own tests is
// one.
func seedBackupDrift(t *testing.T, s BackupStore, acme namespace.Namespace) {
	t.Helper()

	if seeder, ok := s.(DriftSeeder); ok {
		seeder.SeedDriftResult(t, acme)
	}
}

// seedBackupGate writes one gate that reads a path set, so the store holds a
// reference row, one decision record of the installation and one decision
// record of the namespace acme, when s keeps the policy model. A store with no
// gate port skips it: the fake of this package's own tests is one.
func seedBackupGate(t *testing.T, s BackupStore, acme namespace.Namespace) {
	t.Helper()

	gs, ok := s.(gate.Store)
	if !ok {
		return
	}

	ctx := t.Context()
	scope := gate.NamespaceScope(acme.Name)
	e := gateEntry(acme.Name, "namespace:manage-gates")

	set := gate.PathSet{Scope: scope, ID: "network", Version: 0, Globs: []string{"modules/net/**"}}
	if _, err := gs.PathSets().Put(ctx, set, 0, acme.Fence(), e); err != nil {
		t.Fatalf("put path set: %v", err)
	}

	g := gate.Gate{
		Scope: scope, ID: "cost-cap", Version: 0, DecisionPoint: gate.PointChangePlanned, Condition: "", Unless: "",
		Outcome: gate.OutcomeDeny, Message: "no", Mode: gate.ModeEnforce, OnError: gate.OnErrorFail, Final: false,
		CheckedWith: gate.CheckedWith{Engine: "", Schema: gate.SchemaVersion{Subject: "", Major: 0, Minor: 0}},
		State:       "", Reads: []gate.ID{"network"},
	}
	if _, err := gs.Gates().Put(ctx, g, 0, acme.Fence(), e); err != nil {
		t.Fatalf("put gate: %v", err)
	}

	rule := gate.ApprovalRule{
		Scope:     scope,
		ID:        "net-review",
		Version:   0,
		Count:     1,
		FreshAuth: gate.NoFreshAuth(),
		Approvers: gate.Approvers{
			Groups:     []string{"netops"},
			GroupIDs:   []string{"group-id-1"},
			Users:      nil,
			UnitOwners: false,
		},
		AuthorMayApprove: false,
		Timeout:          0,
	}
	if _, err := gs.ApprovalRules().Put(ctx, rule, 0, acme.Fence(), e); err != nil {
		t.Fatalf("put approval rule: %v", err)
	}

	for _, ns := range []namespace.Name{audit.ServerScope, acme.Name} {
		fence := namespace.Fence{Namespace: "", FailoverVersion: 0}
		if ns == acme.Name {
			fence = acme.Fence()
		}

		// A refusal record needs the window key that NoteRefusal sets.
		if _, err := gs.Records().NoteRefusal(ctx, seedGateRecord(ns), gate.AccessRefusalWindow, fence); err != nil {
			t.Fatalf("note gate refusal of %q: %v", ns, err)
		}
	}
}

// seedGateRecord returns a refusal record of an access limit in ns.
func seedGateRecord(ns namespace.Name) gate.Record {
	return gate.Record{
		ID: gate.NewRecordID(), Point: "", Change: gate.ChangeRef{Kind: gate.ChangeAccess, ID: ""}, Namespace: ns,
		At: now(), Subject: "agent-1", Schema: gate.SchemaVersion{Subject: "", Major: 0, Minor: 0},
		PathSets: nil, Units: nil, Requirements: nil, Warnings: nil, AutoApply: false, Controls: nil, BreakGlass: "",
		Result: gate.ResultDeny, Action: "state.read", Count: 1, LastAt: now(),
	}
}

// requireSeededGate checks that s holds what seedBackupGate wrote: the gate
// and the path set that it reads, and the two records. It also checks that
// the row counts of the summary hold the reference row. A store with no gate
// port skips it.
func requireSeededGate(t *testing.T, s BackupStore, sum backup.Summary) {
	t.Helper()

	gs, ok := s.(gate.Store)
	if !ok {
		return
	}

	for _, table := range []string{"gate_objects", "gate_references"} {
		if sum.RowCounts[table] < 1 {
			t.Fatalf("summary rows of %s = %d, want at least 1", table, sum.RowCounts[table])
		}
	}

	if sum.RowCounts["gate_records"] != 2 {
		t.Fatalf("summary rows of gate_records = %d, want 2", sum.RowCounts["gate_records"])
	}

	ctx := t.Context()
	scope := gate.NamespaceScope("acme")

	if _, err := gs.Gates().Get(ctx, scope, "cost-cap"); err != nil {
		t.Fatalf("get the restored gate: %v", err)
	}

	if _, err := gs.PathSets().Get(ctx, scope, "network"); err != nil {
		t.Fatalf("get the restored path set: %v", err)
	}

	requireSeededApprovalRule(t, gs, scope)

	change := gate.ChangeRef{Kind: gate.ChangeAccess, ID: ""}

	for _, ns := range []namespace.Name{audit.ServerScope, "acme"} {
		records, err := gs.Records().ListByChange(ctx, ns, change)
		if err != nil {
			t.Fatalf("list the restored records of %q: %v", ns, err)
		}

		if len(records) != 1 || records[0].Action != "state.read" {
			t.Fatalf("restored records of %q = %+v, want one record with the action state.read", ns, records)
		}

		requireRefusalFolded(t, s, gs, ns, records[0].ID)
	}
}

// requireSeededApprovalRule checks that the restored approval rule still holds
// the name and the ID of its approver group.
func requireSeededApprovalRule(t *testing.T, gs gate.Store, scope gate.Scope) {
	t.Helper()

	rule, err := gs.ApprovalRules().Get(t.Context(), scope, "net-review")
	if err != nil {
		t.Fatalf("get the restored approval rule: %v", err)
	}

	if !slices.Equal(rule.Approvers.Groups, []string{"netops"}) ||
		!slices.Equal(rule.Approvers.GroupIDs, []string{"group-id-1"}) {
		t.Fatalf("restored approvers = %+v, want the group netops with the ID group-id-1", rule.Approvers)
	}
}

// requireRefusalFolded checks that the restored refusal key of ns still folds a
// second refusal into the record id.
func requireRefusalFolded(t *testing.T, s BackupStore, gs gate.Store, ns namespace.Name, id gate.RecordID) {
	t.Helper()

	ctx := t.Context()
	change := gate.ChangeRef{Kind: gate.ChangeAccess, ID: ""}

	again, err := gs.Records().NoteRefusal(ctx, seedGateRecord(ns), gate.AccessRefusalWindow, fenceOf(t, s, ns))
	if err != nil {
		t.Fatalf("note a second refusal in %q: %v", ns, err)
	}

	if again != id {
		t.Fatalf("second refusal in %q returned the record %s, want %s", ns, again, id)
	}

	folded, err := gs.Records().ListByChange(ctx, ns, change)
	if err != nil {
		t.Fatalf("list the folded records of %q: %v", ns, err)
	}

	if len(folded) != 1 || folded[0].Count != 2 {
		t.Fatalf("records of %q after a second refusal = %+v, want one record with count 2", ns, folded)
	}
}

// seedBackupPrincipals writes a person, a service, a deleted principal, a
// group, a current membership and an ended one when s keeps principals, so
// the round trip compares the principal tables row by row, nullable end
// times included (ADR 52). A store with no principal port skips it: the
// fake of this package's own tests is one.
func seedBackupPrincipals(t *testing.T, s BackupStore) {
	t.Helper()

	ps, ok := s.(principal.Store)
	if !ok {
		return
	}

	ctx := t.Context()

	principals := []principal.Principal{
		{
			CreatedAt: now(), UpdatedAt: now().Add(time.Minute), DisabledAt: time.Time{}, ID: "prn-alice",
			Subject: "alice@example.com", DisplayName: "Alice", Kind: principal.KindPerson,
			Source: principal.SourceLocal, Namespace: "", GroupCount: 0, BreakGlass: false,
		},
		{
			CreatedAt: now(), UpdatedAt: now(), DisabledAt: time.Time{}, ID: "prn-deploy",
			Subject: "ci-deploy", DisplayName: "", Kind: principal.KindService,
			Source: principal.SourceLocal, Namespace: "", GroupCount: 0, BreakGlass: false,
		},
		{
			CreatedAt: now(), UpdatedAt: now(), DisabledAt: time.Time{}, ID: "prn-gone",
			Subject: "gone", DisplayName: "", Kind: principal.KindWorkload,
			Source: principal.SourceSCIM, Namespace: "", GroupCount: 0, BreakGlass: false,
		},
		{
			CreatedAt: now(), UpdatedAt: now(), DisabledAt: time.Time{}, ID: "prn-suspended",
			Subject: "suspended@example.com", DisplayName: "Suspended", Kind: principal.KindPerson,
			Source: principal.SourceLocal, Namespace: "", GroupCount: 0, BreakGlass: false,
		},
	}
	for i := range principals {
		e := ServerChange(now(), "principal:"+principals[i].ID)
		if err := ps.CreatePrincipal(ctx, principals[i], e); err != nil {
			t.Fatalf("create principal %q: %v", principals[i].Subject, err)
		}
	}

	// The group carries the switch that closes it to the identity provider,
	// so that the round trip compares that column too.
	g := principal.Group{
		CreatedAt:                now(),
		UpdatedAt:                now(),
		ID:                       "grp-ops",
		Name:                     "Ops",
		Description:              "Runs the platform",
		Source:                   principal.SourceLocal,
		MemberCount:              0,
		ClosedToIdentityProvider: true,
	}
	if err := ps.CreateGroup(ctx, g, ServerChange(now(), "group:grp-ops")); err != nil {
		t.Fatalf("create group: %v", err)
	}

	for _, id := range []string{"prn-alice", "prn-deploy"} {
		m := principal.Membership{
			AddedAt: now().Add(time.Hour), SuspendedAt: time.Time{}, GroupID: g.ID, PrincipalID: id,
			Source: principal.MembershipSourceLocal, SourceID: "", State: principal.MembershipStateActive,
			Pinned: false,
		}
		if err := ps.AddMember(ctx, m, ServerChange(m.AddedAt, "group:grp-ops#member:"+id), nil); err != nil {
			t.Fatalf("add member %q: %v", id, err)
		}
	}

	suspended := principal.Membership{
		AddedAt: now().Add(time.Hour), SuspendedAt: now().Add(90 * time.Minute), GroupID: g.ID,
		PrincipalID: "prn-suspended", Source: principal.MembershipSourceSCIM, SourceID: "scim-token-1",
		State: principal.MembershipStateSuspended, Pinned: true,
	}
	addErr := ps.AddMember(ctx, suspended, ServerChange(suspended.AddedAt, "group:grp-ops#member:prn-suspended"), nil)
	if addErr != nil {
		t.Fatalf("add suspended member: %v", addErr)
	}

	later := now().Add(2 * time.Hour)
	removal := ServerChange(later, "group:grp-ops#member:prn-alice")
	if err := ps.RemoveMember(ctx, g.ID, "prn-alice", removal); err != nil {
		t.Fatalf("remove member: %v", err)
	}

	if err := ps.DeletePrincipal(ctx, "prn-gone", ServerChange(later, "principal:prn-gone")); err != nil {
		t.Fatalf("delete principal: %v", err)
	}
}

// seedBackupTokens writes a used token, a revoked token and a rotation when
// s keeps access tokens, so the round trip compares the token table row by
// row, nullable times and replaced_by included (ADR 53). It needs the
// principals of seedBackupPrincipals.
func seedBackupTokens(t *testing.T, s BackupStore) {
	t.Helper()

	ts, ok := s.(accesstoken.Store)
	if !ok {
		return
	}

	ctx := t.Context()

	tokens := []accesstoken.Token{
		backupToken("tok-used", "prn-deploy", accesstoken.KindService),
		backupToken("tok-revoked", "prn-alice", accesstoken.KindPersonal),
		backupToken("tok-old", "prn-deploy", accesstoken.KindService),
	}
	for i, d := range []accesstoken.Digest{{1}, {2}, {3}} {
		if err := ts.CreateToken(ctx, tokens[i], d, ServerChange(now(), "token:"+tokens[i].ID)); err != nil {
			t.Fatalf("create token %q: %v", tokens[i].ID, err)
		}
	}

	if err := ts.TouchToken(ctx, "tok-used", now().Add(time.Hour), time.Minute); err != nil {
		t.Fatalf("touch token: %v", err)
	}

	if err := ts.RevokeToken(ctx, "tok-revoked", ServerChange(now().Add(time.Hour), "token:tok-revoked")); err != nil {
		t.Fatalf("revoke token: %v", err)
	}

	r := accesstoken.Replacement{
		OldEndsAt: now().Add(2 * time.Hour), OldID: "tok-old",
		Next: backupToken("tok-new", "prn-deploy", accesstoken.KindService), Digest: accesstoken.Digest{9},
	}
	rotation := ServerChange(now().Add(time.Hour), "token:tok-old")

	if err := ts.RotateToken(ctx, r, rotation); err != nil {
		t.Fatalf("rotate token: %v", err)
	}

	seedBackupSweep(t, ts)
}

// seedBackupSignIn writes a provider binding, a sign-in request with a
// max_age and a token freshness window when s keeps the console sign-in,
// so the round trip compares the new columns and the provider table row by
// row.
func seedBackupSignIn(t *testing.T, s BackupStore) {
	t.Helper()

	ctx := t.Context()

	if ps, ok := s.(session.ProviderStore); ok {
		if _, err := ps.BindProvider(ctx, "corp", "https://idp.example", now()); err != nil {
			t.Fatalf("bind provider: %v", err)
		}
	}

	if ss, ok := s.(session.Store); ok {
		r := session.Request{
			CreatedAt: now(), ExpiresAt: now().Add(session.RequestLifetime), Provider: "corp",
			Nonce: "n-0S6_WzA2Mj-rG7k3QpL", Verifier: "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk", ReturnPath: "/",
			MaxAge: 0, HasMaxAge: true,
		}
		if err := ss.CreateRequest(ctx, r, session.Digest{7}); err != nil {
			t.Fatalf("create sign-in request: %v", err)
		}
	}

	if pol, ok := s.(session.PolicyStore); ok {
		_, err := pol.UpdateSessionPolicy(ctx, func(old session.Policy) (session.Policy, audit.Entry, error) {
			old.TokenFreshness, old.TokenFreshnessDefault, old.UpdatedAt = 30*time.Minute, false, now()

			return old, ServerChange(now(), session.PolicyRef), nil
		})
		if err != nil {
			t.Fatalf("set the token freshness window: %v", err)
		}
	}

	seedBackupProviders(t, s)
}

// seedBackupProviders writes an installation key and a sign-in provider
// that the API manages, so the round trip carries both tables.
func seedBackupProviders(t *testing.T, s BackupStore) {
	t.Helper()

	ctx := t.Context()

	if ks, ok := s.(secret.InstallationKeyStore); ok {
		scope, err := secret.InstallationScope("sign-in-corp")
		if err != nil {
			t.Fatalf("installation scope: %v", err)
		}

		k := secret.WrappedKey{CreatedAt: now(), Scope: scope, WrapperID: "kek-test", Ciphertext: []byte("wrapped key")}
		if _, err := ks.CreateInstallationKey(ctx, k); err != nil {
			t.Fatalf("create installation key: %v", err)
		}
	}

	if pc, ok := s.(session.ProviderConfigStore); ok {
		p := session.ProviderConfig{
			CreatedAt: now(), UpdatedAt: now(), ID: "corp", Mode: session.ModeOIDC, Issuer: "https://idp.example",
			DisplayName: "Corp", ClientID: "dustpan", SealedSecret: []byte("sealed secret"),
			Scopes: []string{"profile"}, OAuth: session.OAuthSettings{
				AuthorizationURL: "", TokenURL: "", UserinfoURL: "", SubjectClaim: "", NameClaim: "", UsernameClaim: "",
				EmailClaim: "",
			}, Version: 1, AutoCreate: true, Enabled: false,
		}
		if err := pc.CreateProviderConfig(ctx, p, ServerChange(now(), "sign-in-provider:corp")); err != nil {
			t.Fatalf("create sign-in provider: %v", err)
		}

		plain := session.ProviderConfig{
			CreatedAt: now(), UpdatedAt: now(), ID: "hub", Mode: session.ModeOAuth, Issuer: "", DisplayName: "Hub",
			ClientID: "dustpan", SealedSecret: []byte("sealed secret of hub"), Scopes: []string{"read:user"},
			OAuth: session.OAuthSettings{ //nolint:gosec // Test fixture: the URLs of a provider, no credential.
				AuthorizationURL: "https://hub.example/login/oauth/authorize",
				TokenURL:         "https://hub.example/login/oauth/access_token",
				UserinfoURL:      "https://api.hub.example/user", SubjectClaim: "id", NameClaim: "name",
				UsernameClaim: "login", EmailClaim: "email",
			}, Version: 1, AutoCreate: false, Enabled: true,
		}
		if err := pc.CreateProviderConfig(ctx, plain, ServerChange(now(), "sign-in-provider:hub")); err != nil {
			t.Fatalf("create plain OAuth sign-in provider: %v", err)
		}
	}
}

// seedBackupSweep changes the token policy and runs one sweep, so the round
// trip carries the policy row and the disable times of a token and of a
// principal (ADR 53).
func seedBackupSweep(t *testing.T, ts accesstoken.Store) {
	t.Helper()

	ctx := t.Context()

	policy := accesstoken.Policy{
		UpdatedAt: now(), MaxLifetime: 30 * 24 * time.Hour, Inactivity: time.Hour,
		DisableTokens: true, DisableAccounts: true, DisableBreakGlass: false,
		ExchangeLifetime: accesstoken.DefaultExchangeLifetime,
	}

	_, err := ts.UpdatePolicy(ctx, func(accesstoken.Policy) (accesstoken.Policy, audit.Entry, error) {
		return policy, ServerChange(now(), accesstoken.PolicyRef), nil
	})
	if err != nil {
		t.Fatalf("set policy: %v", err)
	}

	// The sweep runs at the time of the database clock, after now(): every
	// token that has not ended and prn-alice have no use in the last hour.
	// tok-old ends before the sweep.
	sweep := accesstoken.Sweep{Inactivity: time.Hour, Tokens: true, Accounts: true}
	res, err := ts.SweepInactive(ctx, sweep, ServerChange)
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}

	if len(res.Tokens) == 0 || len(res.Principals) == 0 {
		t.Fatalf("sweep = %+v, want a disabled token and a disabled principal", res)
	}
}

// backupToken returns a token of the seed that expires 100 years after now,
// so it has not ended when the sweep runs at the time of the database clock.
func backupToken(id, principalID string, kind accesstoken.Kind) accesstoken.Token {
	return accesstoken.Token{
		CreatedAt:   now(),
		ExpiresAt:   now().AddDate(100, 0, 0),
		LastUsedAt:  time.Time{},
		RevokedAt:   time.Time{},
		DisabledAt:  time.Time{},
		ID:          id,
		PrincipalID: principalID,
		Name:        "seed " + id,
		Kind:        kind,
		ReplacedBy:  "",
		CreatedBy:   "admin",
	}
}

// ServerChange returns a valid audit entry of the kind change in the scope
// @server, for the record object at the time at: the entry that a write to
// an installation store takes.
func ServerChange(at time.Time, object string) audit.Entry {
	return audit.Entry{
		At:            at,
		Namespace:     audit.ServerScope,
		Subject:       "admin",
		IDPSubject:    "",
		TokenID:       "",
		Action:        "server:manage-principals",
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

// GrantWrite returns the fence and the audit entry that a grant write on
// obj takes in s, for the record object at the time at: the zero fence and
// an entry in @server for the installation, the fence of the namespace of
// obj and an entry in that namespace otherwise.
func GrantWrite(
	tb testing.TB, s namespace.Store, obj identity.Object, at time.Time, object string,
) (namespace.Fence, audit.Entry) {
	tb.Helper()

	e := ServerChange(at, object)
	if obj.Namespace() == "" {
		return namespace.Fence{Namespace: "", FailoverVersion: 0}, e
	}

	ns, err := s.GetNamespace(tb.Context(), obj.Namespace())
	if err != nil {
		tb.Fatalf("namespace of grant on %q: %v", obj.String(), err)
	}

	e.Namespace = ns.Name

	return ns.Fence(), e
}

// seedBackupAccess writes a custom role, an ended role, two current grants
// (one with a condition) and a revoked one when s keeps roles and grants
// (ADR 52), so the round trip carries the nullable end times. A store with
// no access port skips it.
func seedBackupAccess(t *testing.T, s BackupStore) {
	t.Helper()

	as, ok := s.(access.Store)
	if !ok {
		return
	}

	ctx := t.Context()

	role := access.Role{
		CreatedAt: now(), UpdatedAt: now().Add(time.Minute), ID: "rol-deployer", Name: "Deployer",
		Description: "Runs updates", Actions: []identity.Action{identity.ActionUnitRead, identity.ActionUnitWrite},
		BuiltIn: false,
	}
	if err := as.CreateRole(ctx, role, ServerChange(now(), "role:rol-deployer")); err != nil {
		t.Fatalf("create role: %v", err)
	}

	gone := role
	gone.ID, gone.Name = "rol-gone", "Gone"

	if err := as.CreateRole(ctx, gone, ServerChange(now(), "role:rol-gone")); err != nil {
		t.Fatalf("create role: %v", err)
	}

	if err := as.DeleteRole(ctx, gone.ID, ServerChange(now().Add(time.Hour), "role:rol-gone")); err != nil {
		t.Fatalf("delete role: %v", err)
	}

	condition := "reserved"

	grants := []access.Grant{
		{
			CreatedAt: now(),
			Condition: nil,
			Object:    identity.NamespaceObject("acme"),
			ID:        "grt-ops",
			Role:      access.RoleViewer,
			CreatedBy: "admin",
			Subject:   access.Subject{Kind: access.SubjectGroup, ID: "grp-ops"},
		},
		{
			CreatedAt: now().Add(time.Hour), Condition: &condition, Object: identity.ServerObject(), ID: "grt-deploy",
			Role: role.ID, CreatedBy: "admin", Subject: access.Subject{Kind: access.SubjectPrincipal, ID: "prn-deploy"},
		},
		{
			CreatedAt: now(),
			Condition: nil,
			Object:    identity.NamespaceObject("acme"),
			ID:        "grt-revoked",
			Role:      access.RoleWriter,
			CreatedBy: "admin",
			Subject:   access.Subject{Kind: access.SubjectPrincipal, ID: "prn-alice"},
		},
	}
	for i := range grants {
		fence, e := GrantWrite(t, s, grants[i].Object, now(), "grant:"+grants[i].ID)
		if err := as.CreateGrant(ctx, grants[i], fence, e, nil); err != nil {
			t.Fatalf("create grant %q: %v", grants[i].ID, err)
		}
	}

	fence, revoked := GrantWrite(t, s, identity.NamespaceObject("acme"), now().Add(2*time.Hour), "grant:grt-revoked")

	err := as.RevokeGrant(ctx, "grt-revoked", fence, revoked, nil)
	if err != nil {
		t.Fatalf("revoke grant: %v", err)
	}
}

// backupChanges answers changes whose text JSON escapes: <, & and > (which
// encoding/json writes as \u003c, \u0026 and \u003e), U+2028, quotes and a
// backslash, so the round trip proves the changes column keeps the exact
// text that the hash covers.
func backupChanges() []audit.Change {
	old := `say "hi" <&> \ end`

	return []audit.Change{
		{Key: "region", Old: &old, New: "line\u2028sep <tag> & \"q\"", Layer: "file", TakesEffect: "next start"},
		{Key: "database", Old: nil, New: "set", Layer: "env", TakesEffect: ""},
	}
}

// seedBackupAudit appends a reveal entry, a decrypt entry with a value
// count and a change entry with the changes of backupChanges to acme, so the
// round trip compares the audit table row by row, carries the nullable value
// count and the nullable changes both ways, and proves the chain survives
// it. A store that implements audit.Feed also gets one sink cursor row.
func seedBackupAudit(t *testing.T, s BackupStore, acme namespace.Namespace) []audit.Entry {
	t.Helper()

	out := make([]audit.Entry, 0, 3)

	for i := 1; i <= 3; i++ {
		kind, count, changes := audit.KindReveal, (*int64)(nil), []audit.Change(nil)
		if i == 2 {
			n := int64(3)
			kind, count = audit.KindToolOperation, &n
		}

		if i == 3 {
			kind, changes = audit.KindChange, backupChanges()
		}

		stored, err := s.Append(t.Context(), acme.Fence(), audit.Entry{
			At:            now(),
			Namespace:     acme.Name,
			Subject:       "admin",
			IDPSubject:    "",
			TokenID:       "",
			Action:        "unit:decrypt",
			Object:        "pulumi:" + acme.Name.String() + "/web/prod",
			Version:       "",
			ValuePath:     "",
			ValueCount:    count,
			Changes:       changes,
			SourceIP:      "192.0.2.1",
			UserAgent:     "test",
			Channel:       audit.ChannelAPI,
			Kind:          kind,
			Outcome:       audit.OutcomeSuccess,
			RequestID:     fmt.Sprintf("req-%d", i),
			ObjectID:      "",
			PrevHash:      "",
			Hash:          "",
			Sequence:      0,
			FenceVersion:  0,
			Reason:        "",
			PrincipalKind: "",
			JobID:         "",
		})
		if err != nil {
			t.Fatalf("append audit entry %d: %v", i, err)
		}

		out = append(out, stored)
	}

	// A store that ships entries to log sinks also keeps a cursor row, so
	// the round trip carries the cursor table too (ADR 60 decision 13).
	// The cursor write carries the live claim of the sink.
	if feed, ok := s.(audit.Feed); ok {
		leaser, ok := s.(audit.SinkLeaser)
		if !ok {
			t.Fatal("store keeps audit sink cursors but no sink leases")
		}

		claim, err := leaser.AcquireSinkLease(t.Context(), "siem", "backup-seed", time.Hour)
		if err != nil {
			t.Fatalf("acquire audit sink lease: %v", err)
		}

		last := out[len(out)-1].Sequence

		advErr := feed.AdvanceSinkCursor(t.Context(), acme.Fence(), claim, "siem", last)
		if advErr != nil {
			t.Fatalf("advance audit sink cursor: %v", advErr)
		}
	}

	return out
}

// mustCreateStack creates one stack called name in ns, failing on error.
func mustCreateStack(t *testing.T, s BackupStore, ns namespace.Namespace, name string) stack.Stack {
	t.Helper()

	st := newStack(ns.Name, "web", name)
	if err := s.CreateStack(t.Context(), st, ns.Fence()); err != nil {
		t.Fatalf("create stack %s: %v", st.Ref, err)
	}

	return st
}

// seedBackupKey stores one wrapped data key and returns its scope.
func seedBackupKey(
	t *testing.T, s BackupStore, acme namespace.Namespace, st stack.Stack, wrapper string,
) secret.Scope {
	t.Helper()

	scope, err := secret.NewScope(acme.Name, st.ID.String())
	if err != nil {
		t.Fatalf("new scope of %s: %v", st.Ref, err)
	}

	key := secret.WrappedKey{
		CreatedAt:  now(),
		Scope:      scope,
		WrapperID:  wrapper,
		Ciphertext: []byte("wrapped-" + st.Ref.Stack.String()),
	}

	stored, err := s.CreateKey(t.Context(), key, acme.Fence())
	if err != nil {
		t.Fatalf("create key of %s: %v", st.Ref, err)
	}

	_ = stored

	return scope
}

// seedBackupPurge makes, deletes and purges the stack web/purged of acme, so
// the seed holds one blob purge record (ADR 47) and the round trip carries
// the blob_purges table.
func seedBackupPurge(t *testing.T, s BackupStore, acme namespace.Namespace) {
	t.Helper()

	ctx := t.Context()
	st := newStack(acme.Name, "web", "purged")

	if err := s.CreateStack(ctx, st, acme.Fence()); err != nil {
		t.Fatalf("create stack to purge: %v", err)
	}

	d := inventory.Deletion{Entry: deleteEntry(acme.Name), PurgeAfter: purgeAt()}
	d.Entry.Object = "pulumi:" + acme.Name.String() + "/web/purged"

	if err := s.DeleteStack(ctx, st.Ref, acme.Fence(), stack.EmptyGuard(), d); err != nil {
		t.Fatalf("delete stack to purge: %v", err)
	}

	if err := s.PurgeStack(ctx, st.ID, acme.Fence(), time.Time{}, lifecycleEntry(acme.Name, "unit:purge")); err != nil {
		t.Fatalf("purge stack: %v", err)
	}
}

// seedBackupRecord stores one finished backup row, so the round trip compares
// the backups table row by row too, not only through its row count.
func seedBackupRecord(t *testing.T, s BackupStore) backup.Record {
	t.Helper()

	ctx := t.Context()
	seeded := newBackupRecord(backup.ID(ident.NewID("bkp-")), now())

	if err := s.CreateBackup(ctx, seeded); err != nil {
		t.Fatalf("create backup: %v", err)
	}

	return mustFinishBackup(t, s, backupFinish{
		ID: seeded.ID, State: backup.StateSucceeded, Error: "", EndedAt: now().Add(time.Hour),
	})
}

// assertBackupEmpty fails when s holds any namespace, secret key, backup
// row or restore row. Namespaces own every other row through foreign keys,
// so an empty namespace and backup list means an empty store; the wrapper-ID
// check names the secret_keys table directly, matching the fake's
// full-table empty check.
func assertBackupEmpty(t *testing.T, s BackupStore) {
	t.Helper()

	namespaces, err := s.ListNamespaces(t.Context())
	if err != nil {
		t.Fatalf("list namespaces: %v", err)
	}

	if len(namespaces) != 0 {
		t.Fatalf("namespaces = %+v, want none", namespaces)
	}

	wrappers, err := s.ListWrapperIDs(t.Context())
	if err != nil {
		t.Fatalf("list wrapper IDs: %v", err)
	}

	if len(wrappers) != 0 {
		t.Fatalf("wrapper IDs = %+v, want none", wrappers)
	}

	rows, err := s.ListBackups(t.Context())
	if err != nil {
		t.Fatalf("list backups: %v", err)
	}

	if len(rows) != 0 {
		t.Fatalf("backups = %+v, want none", rows)
	}

	restores, err := s.ListRestores(t.Context())
	if err != nil {
		t.Fatalf("list restores: %v", err)
	}

	if len(restores) != 0 {
		t.Fatalf("restores = %+v, want none", restores)
	}
}

// mustGetNamespace reads one namespace, failing on error.
func mustGetNamespace(t *testing.T, s BackupStore, name namespace.Name) namespace.Namespace {
	t.Helper()

	got, err := s.GetNamespace(t.Context(), name)
	if err != nil {
		t.Fatalf("get namespace %q: %v", name, err)
	}

	return got
}

// mustListNamespaces lists every namespace, failing on error.
func mustListNamespaces(t *testing.T, s BackupStore) []namespace.Namespace {
	t.Helper()

	got, err := s.ListNamespaces(t.Context())
	if err != nil {
		t.Fatalf("list namespaces: %v", err)
	}

	return got
}

// mustGetStack reads one stack, failing on error.
func mustGetStack(t *testing.T, s BackupStore, ref stack.Ref) stack.Stack {
	t.Helper()

	got, err := s.GetStack(t.Context(), ref)
	if err != nil {
		t.Fatalf("get stack %s: %v", ref, err)
	}

	return got
}

// mustGetKey reads one wrapped data key, failing on error.
func mustGetKey(t *testing.T, s BackupStore, scope secret.Scope) secret.WrappedKey {
	t.Helper()

	got, err := s.GetKey(t.Context(), scope)
	if err != nil {
		t.Fatalf("get key of %s: %v", scope, err)
	}

	return got
}

// mustGetBackup reads one backup row, failing on error.
func mustGetBackup(t *testing.T, s BackupStore, id backup.ID) backup.Record {
	t.Helper()

	got, err := s.GetBackup(t.Context(), id)
	if err != nil {
		t.Fatalf("get backup %s: %v", id, err)
	}

	return got
}

// mustListBackups lists every backup row, failing on error.
func mustListBackups(t *testing.T, s BackupStore) []backup.Record {
	t.Helper()

	got, err := s.ListBackups(t.Context())
	if err != nil {
		t.Fatalf("list backups: %v", err)
	}

	return got
}

// mustListRestores lists every restore row, failing on error.
func mustListRestores(t *testing.T, s BackupStore) []backup.RestoreRecord {
	t.Helper()

	got, err := s.ListRestores(t.Context())
	if err != nil {
		t.Fatalf("list restores: %v", err)
	}

	return got
}

// mustCreateBackup stores a running backup row, failing on error.
func mustCreateBackup(t *testing.T, s BackupStore, r backup.Record) backup.Record {
	t.Helper()

	if err := s.CreateBackup(t.Context(), r); err != nil {
		t.Fatalf("create backup %s: %v", r.ID, err)
	}

	return r
}

// backupFinish holds how to finish a running backup row: its ID, the end
// state with the error text, and the end time.
type backupFinish struct {
	ID      backup.ID
	State   backup.State
	Error   string
	EndedAt time.Time
}

// mustFinishBackup finishes a running backup row that CreateBackup stored,
// failing on error. CreateBackup starts no operation, so the helper starts a
// bare operation under the start key of the row, claims it and finishes the
// row with the fence of that claim.
func mustFinishBackup(t *testing.T, s BackupStore, finish backupFinish) backup.Record {
	t.Helper()

	return mustFinishClaimed(t, s, bareBackupClaim(t, s, finish.ID), finish)
}

// mustFinishClaimed finishes a running backup row with the fence of claim for
// the step StepFinish, failing on error.
func mustFinishClaimed(t *testing.T, s BackupStore, claim durable.Claim, finish backupFinish) backup.Record {
	t.Helper()

	got, err := s.FinishBackup(
		t.Context(), claim.Step(backup.StepFinish), finish.ID, finish.State, finish.Error, finish.EndedAt,
	)
	if err != nil {
		t.Fatalf("finish backup %s: %v", finish.ID, err)
	}

	return got
}

// backupStartRequest returns the start request of the operation that runs
// the backup id.
func backupStartRequest(id backup.ID) durable.StartRequest {
	return durable.StartRequest{
		Key:     backup.OperationKey(id),
		Kind:    backup.OperationKind,
		Version: backup.OperationVersion,
		Input:   []byte(id.String()),
	}
}

// backupLongLease keeps a claim live for the whole case.
const backupLongLease = time.Hour

// backupWaitLimit is the longest time a case waits for a short lease to
// expire on the clock of the store.
const backupWaitLimit = 10 * time.Second

// backupPollEvery is the interval of the wait for an expired lease.
const backupPollEvery = 25 * time.Millisecond

// claimBackupOperation claims the operation of backup id for owner with lease.
// It fails the test when the claim names another operation or no operation
// can run.
func claimBackupOperation(
	t *testing.T, s BackupStore, id backup.ID, owner string, lease time.Duration,
) durable.Claim {
	t.Helper()

	op, err := s.DurableOperationsPort().Claim(t.Context(), durable.ClaimRequest{
		Owner: owner, Kinds: []durable.Kind{backup.OperationKind}, MaxVersions: nil, Lease: lease,
		TakeCanceled: false,
	})
	if err != nil {
		t.Fatalf("claim the operation of backup %s: %v", id, err)
	}

	if op.Key != backup.OperationKey(id) {
		t.Fatalf("claimed operation key = %q, want %q", op.Key, backup.OperationKey(id))
	}

	return op.Claim
}

// claimBackupWhenExpired claims the operation of backup id for owner as soon
// as the claim before it expired on the clock of the store. It polls and
// fails the test after backupWaitLimit.
func claimBackupWhenExpired(t *testing.T, s BackupStore, id backup.ID, owner string) durable.Claim {
	t.Helper()

	ctx, cancel := context.WithTimeout(t.Context(), backupWaitLimit)
	defer cancel()

	for {
		op, err := s.DurableOperationsPort().Claim(ctx, durable.ClaimRequest{
			Owner: owner, Kinds: []durable.Kind{backup.OperationKind}, MaxVersions: nil, Lease: backupLongLease,
			TakeCanceled: false,
		})
		if err == nil {
			if op.Key != backup.OperationKey(id) {
				t.Fatalf("claimed operation key = %q, want %q", op.Key, backup.OperationKey(id))
			}

			return op.Claim
		}

		if !errors.Is(err, durable.ErrNoWork) {
			t.Fatalf("claim the operation of backup %s: %v", id, err)
		}

		select {
		case <-ctx.Done():
			t.Fatalf("the claim of backup %s did not expire within %v", id, backupWaitLimit)
		case <-time.After(backupPollEvery):
		}
	}
}

// bareBackupClaim starts a bare operation under the start key of the backup
// id and claims it for a long lease. It serves a row that CreateBackup
// stored.
func bareBackupClaim(t *testing.T, s BackupStore, id backup.ID) durable.Claim {
	t.Helper()

	if _, _, err := s.DurableOperationsPort().Start(t.Context(), backupStartRequest(id)); err != nil {
		t.Fatalf("start the operation of backup %s: %v", id, err)
	}

	return claimBackupOperation(t, s, id, "bare", backupLongLease)
}

// mustStartBackup starts a backup with its operation in one call, failing on
// error.
func mustStartBackup(t *testing.T, s BackupStore, r backup.Record) backup.Record {
	t.Helper()

	if err := s.StartBackup(t.Context(), r, backupStartRequest(r.ID)); err != nil {
		t.Fatalf("start backup %s: %v", r.ID, err)
	}

	return r
}

// startBackupWithClaim starts a backup with its operation and claims the
// operation for a long lease.
func startBackupWithClaim(t *testing.T, s BackupStore, r backup.Record) durable.Claim {
	t.Helper()

	mustStartBackup(t, s, r)

	return claimBackupOperation(t, s, r.ID, "test", backupLongLease)
}

// mustOperation returns the operation id, failing on error.
func mustOperation(t *testing.T, s BackupStore, id durable.ID) durable.Operation {
	t.Helper()

	op, err := s.DurableOperationsPort().Get(t.Context(), id)
	if err != nil {
		t.Fatalf("get operation %s: %v", id, err)
	}

	return op
}

// backupOperations returns every operation of the backup kind.
func backupOperations(t *testing.T, s BackupStore) []durable.Operation {
	t.Helper()

	ops, err := s.DurableOperationsPort().List(t.Context(), durable.Filter{
		Kind: backup.OperationKind, States: nil, Limit: 0,
	})
	if err != nil {
		t.Fatalf("list backup operations: %v", err)
	}

	return ops
}

// requireNoBackupOperation fails when an operation of the backup kind holds
// the start key of backup id.
func requireNoBackupOperation(t *testing.T, s BackupStore, id backup.ID) {
	t.Helper()

	ops := backupOperations(t, s)
	for i := range ops {
		if ops[i].Key == backup.OperationKey(id) {
			t.Fatalf("operation %s exists for backup %s, want none", ops[i].ID, id)
		}
	}
}

// requireStillRunning fails unless the row id is running, without an end
// time and without an error, and the operation did not record StepFinish.
func requireStillRunning(t *testing.T, s BackupStore, id backup.ID, claim durable.Claim) {
	t.Helper()

	got := mustGetBackup(t, s, id)
	if got.State != backup.StateRunning || !got.EndedAt.IsZero() || got.Error != "" {
		t.Fatalf("backup after a refused finish = %+v, want running without end time or error", got)
	}

	if _, done := mustOperation(t, s, claim.Operation).Step(backup.StepFinish); done {
		t.Fatalf("operation %s recorded %s after a refused finish, want none", claim.Operation, backup.StepFinish)
	}
}

// mustFailEnded runs FailEndedBackups and returns its count, failing on error.
func mustFailEnded(t *testing.T, s BackupStore, endedAt time.Time) int {
	t.Helper()

	n, err := s.FailEndedBackups(t.Context(), endedAt)
	if err != nil {
		t.Fatalf("fail ended backups: %v", err)
	}

	return n
}

// failBackupOperation ends the operation of claim failed with message.
func failBackupOperation(t *testing.T, s BackupStore, claim durable.Claim, message string) {
	t.Helper()

	err := s.DurableOperationsPort().Fail(t.Context(), claim, durable.Failure{
		Message: message, Final: true, RetryAfter: 0,
	})
	if err != nil {
		t.Fatalf("fail operation %s: %v", claim.Operation, err)
	}
}

// sameBlobRef compares two blob references.
func sameBlobRef(a, b backup.BlobRef) bool {
	return a.Key == b.Key && a.Size == b.Size && a.MD5 == b.MD5
}

// testBackupTablesCoverSchema checks that the data tables of the migrated
// schema are exactly the tables of backup.Tables and backup.ControlTables.
// It reads the schema tables through the Inspector, never through the lists
// themselves, so a migration that adds a table fails here until its author
// adds the table to one of the lists.
func testBackupTablesCoverSchema(t *testing.T, newStore BackupFactory) {
	t.Helper()

	s := newStore(t)

	schema, err := s.SchemaTables(t.Context())
	if err != nil {
		t.Fatalf("schema tables: %v", err)
	}

	want := append(backup.TablesCopy(), backup.ControlTables...)
	want = append(want, backup.EmptiedTables...)
	slices.Sort(want)

	got := slices.Clone(schema)
	slices.Sort(got)

	if !slices.Equal(got, want) {
		t.Fatalf("schema tables = %q, want backup.Tables, backup.ControlTables and backup.EmptiedTables %q", got, want)
	}
}

// testBackupColumnsCoverSchema checks that the column list of each table of
// backup.Tables matches the live columns of the migrated schema (ADR 42,
// decision 5). A column that a migration adds fails here until its list names it.
func testBackupColumnsCoverSchema(t *testing.T, newStore BackupFactory) {
	t.Helper()

	s := newStore(t)

	if err := s.CheckCoverage(t.Context()); err != nil {
		t.Fatalf("column lists and schema differ: %v", err)
	}
}

// newBackupRecord returns a running backup record with the given start time.
func newBackupRecord(id backup.ID, startedAt time.Time) backup.Record {
	return backup.Record{
		StartedAt: startedAt,
		EndedAt:   time.Time{},
		ID:        id,
		Target:    "dir:/var/backups",
		Error:     "",
		State:     backup.StateRunning,
	}
}

// testBackupEndedShape proves the stored shape of ended_at_ms: a running row
// stores NULL and a finished row stores its end time. It reads the column
// with the raw reader of the store, not through the domain record.
func testBackupEndedShape(t *testing.T, newStore BackupFactory) {
	t.Helper()

	s := newStore(t)

	witness, ok := s.(NullEndedWitness)
	if !ok {
		t.Skip("store has no raw end-time reader")
	}

	running := mustCreateBackup(t, s, newBackupRecord(backup.ID(ident.NewID("bkp-")), now()))
	requireNotEndedStored(t, witness, backup.TableBackups, running.ID.String())

	endedAt := now().Add(time.Hour)
	mustFinishBackup(t, s, backupFinish{
		ID: running.ID, State: backup.StateSucceeded, Error: "", EndedAt: endedAt,
	})
	requireEndedStored(t, witness, backup.TableBackups, running.ID.String(), endedAt)
}

// testBackupRegistry covers the backups table: create a running row, finish
// it succeeded or failed with the error text, get it, and list newest first.
func testBackupRegistry(t *testing.T, newStore BackupFactory) {
	t.Helper()

	s := newStore(t)
	ctx := t.Context()

	if empty := mustListBackups(t, s); len(empty) != 0 {
		t.Fatalf("backups = %+v, want none", empty)
	}

	first := mustCreateBackup(t, s, newBackupRecord(backup.ID(ident.NewID("bkp-")), now()))

	if err := s.CreateBackup(ctx, first); !errors.Is(err, backup.ErrAlreadyExists) {
		t.Fatalf("create duplicate backup: err = %v, want ErrAlreadyExists", err)
	}

	if got := mustGetBackup(t, s, first.ID); got != first {
		t.Fatalf("backup = %+v, want %+v", got, first)
	}

	if _, err := s.GetBackup(ctx, backup.ID(ident.NewID("bkp-"))); !errors.Is(err, backup.ErrNotFound) {
		t.Fatalf("get missing backup: err = %v, want ErrNotFound", err)
	}

	endedAt := now().Add(time.Hour)
	claim := bareBackupClaim(t, s, first.ID)
	finished := mustFinishClaimed(t, s, claim, backupFinish{
		ID:      first.ID,
		State:   backup.StateSucceeded,
		Error:   "",
		EndedAt: endedAt,
	})

	if finished.State != backup.StateSucceeded || !finished.EndedAt.Equal(endedAt) || finished.Error != "" {
		t.Fatalf("finished backup = %+v, want succeeded at %v without error", finished, endedAt)
	}

	checkBackupFinishErrors(t, s, claim, first.ID, endedAt)

	secondDone := failSecondBackup(t, s)
	third := mustCreateBackup(t, s, newBackupRecord(backup.ID(ident.NewID("bkp-")), now().Add(4*time.Hour)))

	checkBackupList(t, s, first.ID, secondDone, third.ID)
}

// checkBackupFinishErrors checks that finishing a finished row reports
// ErrNotRunning and finishing a missing row reports ErrNotFound, both with the
// live claim of the operation, and that neither records the step.
func checkBackupFinishErrors(t *testing.T, s BackupStore, claim durable.Claim, id backup.ID, endedAt time.Time) {
	t.Helper()

	ctx := t.Context()
	probe := claim.Step("probe")

	if _, err := s.FinishBackup(ctx, probe, id, backup.StateFailed, "again", endedAt); !errors.Is(
		err, backup.ErrNotRunning,
	) {
		t.Fatalf("finish a finished backup: err = %v, want ErrNotRunning", err)
	}

	missing := backup.ID(ident.NewID("bkp-"))
	missingProbe := bareBackupClaim(t, s, missing).Step("probe")

	_, err := s.FinishBackup(ctx, missingProbe, missing, backup.StateFailed, "no", endedAt)
	if !errors.Is(err, backup.ErrNotFound) {
		t.Fatalf("finish a missing backup: err = %v, want ErrNotFound", err)
	}

	if _, done := mustOperation(t, s, claim.Operation).Step(probe.Step); done {
		t.Fatalf("operation recorded step %q after a refused finish, want none", probe.Step)
	}
}

// checkBackupList checks that ListBackups returns every row newest first with
// its state, error text and end time.
func checkBackupList(t *testing.T, s BackupStore, firstID backup.ID, secondDone backup.Record, thirdID backup.ID) {
	t.Helper()

	list := mustListBackups(t, s)

	if len(list) != 3 || list[0].ID != thirdID || list[1].ID != secondDone.ID || list[2].ID != firstID {
		t.Fatalf("backups newest first = %v, want [%s %s %s]", backupIDs(list), thirdID, secondDone.ID, firstID)
	}

	if list[1] != secondDone {
		t.Fatalf("listed failed backup = %+v, want %+v", list[1], secondDone)
	}
}

// failSecondBackup creates a second backup row and finishes it failed with
// the error text.
func failSecondBackup(t *testing.T, s BackupStore) backup.Record {
	t.Helper()

	second := mustCreateBackup(t, s, newBackupRecord(backup.ID(ident.NewID("bkp-")), now().Add(2*time.Hour)))
	failedAt := now().Add(3 * time.Hour)

	secondDone := mustFinishBackup(t, s, backupFinish{
		ID:      second.ID,
		State:   backup.StateFailed,
		Error:   "copy blobs: connection reset",
		EndedAt: failedAt,
	})

	if secondDone.State != backup.StateFailed || secondDone.Error != "copy blobs: connection reset" ||
		!secondDone.EndedAt.Equal(failedAt) {
		t.Fatalf("failed backup = %+v, want the error text and end time", secondDone)
	}

	return secondDone
}

// backupIDs returns the IDs of rows for failure messages.
func backupIDs(rows []backup.Record) []backup.ID {
	ids := make([]backup.ID, 0, len(rows))
	for i := range rows {
		ids = append(ids, rows[i].ID)
	}

	return ids
}

// testBackupOneRunning refuses a second running backup with
// ErrAlreadyRunning, keeps ErrAlreadyExists for a taken ID, and allows the
// next backup once the running one finished.
func testBackupOneRunning(t *testing.T, newStore BackupFactory) {
	t.Helper()

	s := newStore(t)
	first := mustCreateBackup(t, s, newBackupRecord(backup.ID(ident.NewID("bkp-")), now()))

	second := newBackupRecord(backup.ID(ident.NewID("bkp-")), now().Add(time.Minute))
	if err := s.CreateBackup(t.Context(), second); !errors.Is(err, backup.ErrAlreadyRunning) {
		t.Fatalf("second running backup: err = %v, want ErrAlreadyRunning", err)
	}

	if _, err := s.GetBackup(t.Context(), second.ID); !errors.Is(err, backup.ErrNotFound) {
		t.Fatalf("refused backup row: err = %v, want ErrNotFound", err)
	}

	if err := s.CreateBackup(t.Context(), first); !errors.Is(err, backup.ErrAlreadyExists) {
		t.Fatalf("taken backup ID: err = %v, want ErrAlreadyExists", err)
	}

	mustFinishBackup(t, s, backupFinish{ID: first.ID, State: backup.StateSucceeded, Error: "", EndedAt: now()})
	mustCreateBackup(t, s, second)
}

// testBackupStart starts a backup: the running row and the operation of its
// start key exist after the call, and the operation waits to be claimed.
func testBackupStart(t *testing.T, newStore BackupFactory) {
	t.Helper()

	s := newStore(t)
	r := mustStartBackup(t, s, newBackupRecord(backup.ID(ident.NewID("bkp-")), now()))

	if got := mustGetBackup(t, s, r.ID); got != r {
		t.Fatalf("backup after start = %+v, want %+v", got, r)
	}

	ops := backupOperations(t, s)
	if len(ops) != 1 {
		t.Fatalf("operations after start = %d, want 1", len(ops))
	}

	want := backupStartRequest(r.ID)

	if ops[0].Key != want.Key || ops[0].Kind != want.Kind || ops[0].Version != want.Version ||
		!bytes.Equal(ops[0].Input, want.Input) || ops[0].State != durable.StatePending {
		t.Fatalf("operation after start = %+v, want a pending operation for %+v", ops[0], want)
	}
}

// testBackupStartRefused refuses a start while a backup runs, and a start with
// a taken ID: the row and the operation of the refused start never exist. The
// refused requests carry their own start keys, so a store that starts the
// operation before it checks the row leaves a visible operation.
func testBackupStartRefused(t *testing.T, newStore BackupFactory) {
	t.Helper()

	s := newStore(t)
	ctx := t.Context()
	first := mustStartBackup(t, s, newBackupRecord(backup.ID(ident.NewID("bkp-")), now()))

	second := newBackupRecord(backup.ID(ident.NewID("bkp-")), now().Add(time.Minute))
	if err := s.StartBackup(ctx, second, backupStartRequest(second.ID)); !errors.Is(err, backup.ErrAlreadyRunning) {
		t.Fatalf("start while a backup runs: err = %v, want ErrAlreadyRunning", err)
	}

	if _, err := s.GetBackup(ctx, second.ID); !errors.Is(err, backup.ErrNotFound) {
		t.Fatalf("refused backup row: err = %v, want ErrNotFound", err)
	}

	requireNoBackupOperation(t, s, second.ID)

	if err := s.StartBackup(ctx, first, backupStartRequest(first.ID)); !errors.Is(err, backup.ErrAlreadyExists) {
		t.Fatalf("start with a taken ID: err = %v, want ErrAlreadyExists", err)
	}

	if ops := backupOperations(t, s); len(ops) != 1 || ops[0].Key != backup.OperationKey(first.ID) {
		t.Fatalf("operations after refused starts = %+v, want only the operation of %s", ops, first.ID)
	}

	if got := mustGetBackup(t, s, first.ID); got != first {
		t.Fatalf("backup after refused starts = %+v, want %+v", got, first)
	}
}

// testBackupStartForeign refuses a start whose request carries the key of
// another backup or another kind, and stores no row and no operation.
func testBackupStartForeign(t *testing.T, newStore BackupFactory) {
	t.Helper()

	s := newStore(t)
	r := newBackupRecord(backup.ID(ident.NewID("bkp-")), now())

	foreignKey := backupStartRequest(r.ID)
	foreignKey.Key = backup.OperationKey(backup.ID(ident.NewID("bkp-")))

	foreignKind := backupStartRequest(r.ID)
	foreignKind.Kind = durable.Kind("other")

	for name, req := range map[string]durable.StartRequest{"key": foreignKey, "kind": foreignKind} {
		if err := s.StartBackup(t.Context(), r, req); !errors.Is(err, durable.ErrKeyConflict) {
			t.Fatalf("start with a foreign %s: err = %v, want durable.ErrKeyConflict", name, err)
		}
	}

	if _, err := s.GetBackup(t.Context(), r.ID); !errors.Is(err, backup.ErrNotFound) {
		t.Fatalf("row of a foreign start: err = %v, want ErrNotFound", err)
	}

	if ops := backupOperations(t, s); len(ops) != 0 {
		t.Fatalf("operations after foreign starts = %+v, want none", ops)
	}
}

// testBackupStartInvalid refuses a start whose request fails validation with
// durable.ErrInvalid and stores no row.
func testBackupStartInvalid(t *testing.T, newStore BackupFactory) {
	t.Helper()

	s := newStore(t)
	r := newBackupRecord(backup.ID(ident.NewID("bkp-")), now())

	invalid := backupStartRequest(r.ID)
	invalid.Key = ""

	if err := s.StartBackup(t.Context(), r, invalid); !errors.Is(err, durable.ErrInvalid) {
		t.Fatalf("start with an empty key: err = %v, want durable.ErrInvalid", err)
	}

	if _, err := s.GetBackup(t.Context(), r.ID); !errors.Is(err, backup.ErrNotFound) {
		t.Fatalf("row of an invalid start: err = %v, want ErrNotFound", err)
	}

	if ops := backupOperations(t, s); len(ops) != 0 {
		t.Fatalf("operations after an invalid start = %+v, want none", ops)
	}
}

// testBackupFinishRecordsStep finishes a backup with the live fence: the row
// ends and the operation records the step in the same call. A second finish
// with the same fence reports durable.ErrStepDone, because the fence check
// comes before the row check, and changes nothing.
func testBackupFinishRecordsStep(t *testing.T, newStore BackupFactory) {
	t.Helper()

	s := newStore(t)
	r := newBackupRecord(backup.ID(ident.NewID("bkp-")), now())
	claim := startBackupWithClaim(t, s, r)
	endedAt := now().Add(time.Hour)

	finished := mustFinishClaimed(t, s, claim, backupFinish{
		ID: r.ID, State: backup.StateSucceeded, Error: "", EndedAt: endedAt,
	})

	if finished.State != backup.StateSucceeded || !finished.EndedAt.Equal(endedAt) {
		t.Fatalf("finished backup = %+v, want succeeded at %v", finished, endedAt)
	}

	if got := mustGetBackup(t, s, r.ID); got != finished {
		t.Fatalf("stored backup = %+v, want %+v", got, finished)
	}

	if _, done := mustOperation(t, s, claim.Operation).Step(backup.StepFinish); !done {
		t.Fatalf("operation did not record %s with the finish", backup.StepFinish)
	}

	_, err := s.FinishBackup(
		t.Context(), claim.Step(backup.StepFinish), r.ID, backup.StateFailed, "again", endedAt.Add(time.Hour),
	)
	if !errors.Is(err, durable.ErrStepDone) {
		t.Fatalf("finish with a recorded step: err = %v, want durable.ErrStepDone", err)
	}

	if got := mustGetBackup(t, s, r.ID); got != finished {
		t.Fatalf("backup after the second finish = %+v, want %+v", got, finished)
	}
}

// testBackupFinishStaleClaim lets a second holder take the operation over
// after the first claim expired. The first holder, and a fence with a
// neighbor token, get durable.ErrClaimLost and change nothing. The new holder
// still finishes the row.
func testBackupFinishStaleClaim(t *testing.T, newStore BackupFactory) {
	t.Helper()

	s := newStore(t)
	r := mustStartBackup(t, s, newBackupRecord(backup.ID(ident.NewID("bkp-")), now()))
	stale := claimBackupOperation(t, s, r.ID, "first", 100*time.Millisecond)
	live := claimBackupWhenExpired(t, s, r.ID, "second")

	if live.Token <= stale.Token {
		t.Fatalf("token of the new claim = %d, want more than %d", live.Token, stale.Token)
	}

	endedAt := now().Add(time.Hour)

	fences := map[string]durable.StepFence{
		"stale token":  stale.Step(backup.StepFinish),
		"token before": {Operation: live.Operation, Token: live.Token - 1, Step: backup.StepFinish},
		"token after":  {Operation: live.Operation, Token: live.Token + 1, Step: backup.StepFinish},
	}

	for name, fence := range fences {
		_, err := s.FinishBackup(t.Context(), fence, r.ID, backup.StateSucceeded, "", endedAt)
		if !errors.Is(err, durable.ErrClaimLost) {
			t.Fatalf("finish with %s: err = %v, want durable.ErrClaimLost", name, err)
		}

		requireStillRunning(t, s, r.ID, live)
	}

	mustFinishClaimed(t, s, live, backupFinish{ID: r.ID, State: backup.StateSucceeded, Error: "", EndedAt: endedAt})
}

// testBackupFinishCanceled cancels the operation of a running backup: the
// holder gets durable.ErrCanceled and the row stays running.
func testBackupFinishCanceled(t *testing.T, newStore BackupFactory) {
	t.Helper()

	s := newStore(t)
	r := newBackupRecord(backup.ID(ident.NewID("bkp-")), now())
	claim := startBackupWithClaim(t, s, r)

	if err := s.DurableOperationsPort().Cancel(t.Context(), claim.Operation); err != nil {
		t.Fatalf("cancel operation %s: %v", claim.Operation, err)
	}

	_, err := s.FinishBackup(
		t.Context(), claim.Step(backup.StepFinish), r.ID, backup.StateSucceeded, "", now().Add(time.Hour),
	)
	if !errors.Is(err, durable.ErrCanceled) {
		t.Fatalf("finish after a cancel: err = %v, want durable.ErrCanceled", err)
	}

	requireStillRunning(t, s, r.ID, claim)
}

// testBackupFinishForeignFence finishes with a fence that is not the live
// claim of any operation: an operation ID that does not exist, and the claim
// of an operation that ended. Both get durable.ErrClaimLost and change
// nothing.
func testBackupFinishForeignFence(t *testing.T, newStore BackupFactory) {
	t.Helper()

	s := newStore(t)
	r := newBackupRecord(backup.ID(ident.NewID("bkp-")), now())
	claim := startBackupWithClaim(t, s, r)

	other := durable.StartRequest{
		Key: "other:" + r.ID.String(), Kind: "backup-other", Version: 0, Input: nil,
	}
	if _, _, err := s.DurableOperationsPort().Start(t.Context(), other); err != nil {
		t.Fatalf("start the other operation: %v", err)
	}

	otherOp, err := s.DurableOperationsPort().Claim(t.Context(), durable.ClaimRequest{
		Owner: "other", Kinds: []durable.Kind{other.Kind}, MaxVersions: nil, Lease: backupLongLease,
		TakeCanceled: false,
	})
	if err != nil {
		t.Fatalf("claim the other operation: %v", err)
	}

	if completeErr := s.DurableOperationsPort().Complete(t.Context(), otherOp.Claim); completeErr != nil {
		t.Fatalf("complete the other operation: %v", completeErr)
	}

	fences := map[string]durable.StepFence{
		"unknown operation": {Operation: "no-such-operation", Token: claim.Token, Step: backup.StepFinish},
		"ended operation":   otherOp.Claim.Step(backup.StepFinish),
	}

	for name, fence := range fences {
		_, finishErr := s.FinishBackup(t.Context(), fence, r.ID, backup.StateSucceeded, "", now().Add(time.Hour))
		if !errors.Is(finishErr, durable.ErrClaimLost) {
			t.Fatalf("finish with a fence of an %s: err = %v, want durable.ErrClaimLost", name, finishErr)
		}

		requireStillRunning(t, s, r.ID, claim)
	}

	// A live claim on the operation of another backup must not finish this
	// row: the operation of the fence has to be the operation of the row.
	otherBackup := backup.ID(ident.NewID("bkp-"))
	live := bareBackupClaim(t, s, otherBackup)
	liveFence := live.Step(backup.StepFinish)

	_, err = s.FinishBackup(t.Context(), liveFence, r.ID, backup.StateSucceeded, "", now().Add(time.Hour))
	if !errors.Is(err, durable.ErrClaimLost) {
		t.Fatalf("finish with a live fence of another backup: err = %v, want durable.ErrClaimLost", err)
	}

	requireStillRunning(t, s, r.ID, claim)

	if _, done := mustOperation(t, s, live.Operation).Step(liveFence.Step); done {
		t.Fatalf("operation of another backup recorded step %q after a refused finish, want none", liveFence.Step)
	}
}

// testBackupFinishRollsBack refuses a finish of a missing row and a finish
// with a state that is not an end state, both with a live fence: the step
// stays unrecorded, so the same fence finishes the row afterwards.
func testBackupFinishRollsBack(t *testing.T, newStore BackupFactory) {
	t.Helper()

	s := newStore(t)
	r := newBackupRecord(backup.ID(ident.NewID("bkp-")), now())
	claim := startBackupWithClaim(t, s, r)
	fence := claim.Step(backup.StepFinish)
	endedAt := now().Add(time.Hour)

	missing := backup.ID(ident.NewID("bkp-"))
	missingFence := bareBackupClaim(t, s, missing).Step(backup.StepFinish)

	if _, err := s.FinishBackup(t.Context(), missingFence, missing, backup.StateFailed, "no", endedAt); !errors.Is(
		err, backup.ErrNotFound,
	) {
		t.Fatalf("finish a missing backup: err = %v, want ErrNotFound", err)
	}

	requireStillRunning(t, s, r.ID, claim)

	if _, err := s.FinishBackup(t.Context(), fence, r.ID, backup.StateRunning, "", endedAt); !errors.Is(
		err, backup.ErrNotRunning,
	) {
		t.Fatalf("finish with the state running: err = %v, want ErrNotRunning", err)
	}

	requireStillRunning(t, s, r.ID, claim)

	mustFinishClaimed(t, s, claim, backupFinish{ID: r.ID, State: backup.StateSucceeded, Error: "", EndedAt: endedAt})
}

// testBackupFailEndedFailed fails the running row of an operation that ended
// failed, with the last error of the operation and the given end time. A
// finished row stays as it is, and a second call finds nothing to change.
func testBackupFailEndedFailed(t *testing.T, newStore BackupFactory) {
	t.Helper()

	s := newStore(t)

	if n := mustFailEnded(t, s, now()); n != 0 {
		t.Fatalf("fail ended backups of an empty table = %d, want 0", n)
	}

	// One backup runs at a time: the finished row runs and ends first.
	done := newBackupRecord(backup.ID(ident.NewID("bkp-")), now().Add(time.Hour))
	doneClaim := startBackupWithClaim(t, s, done)
	finished := mustFinishClaimed(t, s, doneClaim, backupFinish{
		ID: done.ID, State: backup.StateSucceeded, Error: "", EndedAt: now().Add(2 * time.Hour),
	})

	running := newBackupRecord(backup.ID(ident.NewID("bkp-")), now())
	claim := startBackupWithClaim(t, s, running)
	failBackupOperation(t, s, claim, "write blobs: connection reset")

	endedAt := now().Add(3 * time.Hour)
	if n := mustFailEnded(t, s, endedAt); n != 1 {
		t.Fatalf("fail ended backups = %d, want 1", n)
	}

	got := mustGetBackup(t, s, running.ID)
	if got.State != backup.StateFailed || got.Error != "write blobs: connection reset" ||
		!got.EndedAt.Equal(endedAt) || !got.StartedAt.Equal(running.StartedAt) || got.Target != running.Target {
		t.Fatalf("backup of a failed operation = %+v, want failed with the operation error and end time", got)
	}

	if untouched := mustGetBackup(t, s, done.ID); untouched != finished {
		t.Fatalf("finished backup = %+v, want %+v", untouched, finished)
	}

	if n := mustFailEnded(t, s, endedAt.Add(time.Hour)); n != 0 {
		t.Fatalf("second fail ended backups = %d, want 0", n)
	}
}

// testBackupFailEndedCanceled fails the running row of a canceled operation,
// pending or running at the cancel, and of a failed operation without an
// error text, with the fixed text.
func testBackupFailEndedCanceled(t *testing.T, newStore BackupFactory) {
	t.Helper()

	s := newStore(t)
	ops := s.DurableOperationsPort()

	// A pending operation ends canceled at once.
	pending := mustStartBackup(t, s, newBackupRecord(backup.ID(ident.NewID("bkp-")), now()))

	list := backupOperations(t, s)
	if len(list) != 1 {
		t.Fatalf("operations = %d, want 1", len(list))
	}

	if err := ops.Cancel(t.Context(), list[0].ID); err != nil {
		t.Fatalf("cancel pending operation: %v", err)
	}

	// A running operation records the request and ends canceled at the release.
	running := newBackupRecord(backup.ID(ident.NewID("bkp-")), now().Add(time.Hour))

	endedAt := now().Add(5 * time.Hour)
	requireFailedWithText(t, s, pending.ID, endedAt, backup.EndedWithoutFinishText)

	claim := startBackupWithClaim(t, s, running)

	if err := ops.Cancel(t.Context(), claim.Operation); err != nil {
		t.Fatalf("cancel running operation: %v", err)
	}

	if err := ops.Release(t.Context(), claim); err != nil {
		t.Fatalf("release canceled operation: %v", err)
	}

	requireFailedWithText(t, s, running.ID, endedAt, backup.EndedWithoutFinishText)

	// A failed operation with an empty error gets the same text.
	silent := newBackupRecord(backup.ID(ident.NewID("bkp-")), now().Add(2*time.Hour))
	failBackupOperation(t, s, startBackupWithClaim(t, s, silent), "")
	requireFailedWithText(t, s, silent.ID, endedAt, backup.EndedWithoutFinishText)
}

// requireFailedWithText fails the running rows with FailEndedBackups and
// checks that the row id changed alone, to failed with text and endedAt.
func requireFailedWithText(t *testing.T, s BackupStore, id backup.ID, endedAt time.Time, text string) {
	t.Helper()

	if n := mustFailEnded(t, s, endedAt); n != 1 {
		t.Fatalf("fail ended backups = %d, want 1", n)
	}

	got := mustGetBackup(t, s, id)
	if got.State != backup.StateFailed || got.Error != text || !got.EndedAt.Equal(endedAt) {
		t.Fatalf("backup %s = %+v, want failed with %q at %v", id, got, text, endedAt)
	}
}

// testBackupFailEndedLive keeps the running row of an operation that waits or
// runs: pending, running, and pending again after a retry. Then the holder
// still finishes the row.
func testBackupFailEndedLive(t *testing.T, newStore BackupFactory) {
	t.Helper()

	s := newStore(t)
	r := mustStartBackup(t, s, newBackupRecord(backup.ID(ident.NewID("bkp-")), now()))

	requireRunningAfterFailEnded(t, s, r, "pending operation")

	claim := claimBackupOperation(t, s, r.ID, "test", backupLongLease)
	requireRunningAfterFailEnded(t, s, r, "running operation")

	err := s.DurableOperationsPort().Fail(t.Context(), claim, durable.Failure{
		Message: "try again", Final: false, RetryAfter: time.Hour,
	})
	if err != nil {
		t.Fatalf("fail the attempt: %v", err)
	}

	requireRunningAfterFailEnded(t, s, r, "operation that waits for a retry")
}

// testBackupFailEndedExpiredClaim lets the claim of a running operation
// expire and does not claim the operation again. The operation stays running
// for the next worker, so FailEndedBackups changes no row.
func testBackupFailEndedExpiredClaim(t *testing.T, newStore BackupFactory) {
	t.Helper()

	s := newStore(t)
	r := mustStartBackup(t, s, newBackupRecord(backup.ID(ident.NewID("bkp-")), now()))

	const lease = 200 * time.Millisecond

	claimBackupOperation(t, s, r.ID, "test", lease)

	select {
	case <-time.After(3 * lease):
	case <-t.Context().Done():
		t.Fatal("test ended while the claim expired")
	}

	requireRunningAfterFailEnded(t, s, r, "operation with an expired claim")
}

// requireRunningAfterFailEnded checks that FailEndedBackups changes nothing
// and leaves the row r running.
func requireRunningAfterFailEnded(t *testing.T, s BackupStore, r backup.Record, what string) {
	t.Helper()

	if n := mustFailEnded(t, s, now().Add(time.Hour)); n != 0 {
		t.Fatalf("fail ended backups with a %s = %d, want 0", what, n)
	}

	if got := mustGetBackup(t, s, r.ID); got != r {
		t.Fatalf("backup with a %s = %+v, want %+v", what, got, r)
	}
}

// testBackupRowSurvivesReopen closes the store and reopens the same backing
// storage: every backup row stays, and the operation of the running row stays
// with it. Stores without the Reopener capability skip this case; the SQL
// adapters implement it.
func testBackupRowSurvivesReopen(t *testing.T, newStore BackupFactory) {
	t.Helper()

	s := newStore(t)

	reopenable, ok := s.(Reopener)
	if !ok {
		t.Skip("store does not support close and reopen")
	}

	// One backup runs at a time: the finished row runs and ends first.
	done := mustCreateBackup(t, s, newBackupRecord(backup.ID(ident.NewID("bkp-")), now().Add(time.Hour)))
	finished := mustFinishBackup(t, s, backupFinish{
		ID:      done.ID,
		State:   backup.StateSucceeded,
		Error:   "",
		EndedAt: now().Add(2 * time.Hour),
	})
	running := newBackupRecord(backup.ID(ident.NewID("bkp-")), now())
	claim := startBackupWithClaim(t, s, running)

	if err := reopenable.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}

	reopened := reopenable.Reopen()
	checkReopenedBackups(t, reopened, running, finished)

	if op := mustOperation(t, reopened, claim.Operation); op.Key != backup.OperationKey(running.ID) ||
		op.State != durable.StateRunning {
		t.Fatalf("operation after reopen = %+v, want the running operation of %s", op, running.ID)
	}

	// The operation ends while no process runs: the next start fails the row.
	failBackupOperation(t, reopened, claim, "server stopped during backup")

	if n := mustFailEnded(t, reopened, now().Add(3*time.Hour)); n != 1 {
		t.Fatalf("fail ended backups after reopen = %d, want 1", n)
	}

	if after := mustGetBackup(t, reopened, running.ID); after.State != backup.StateFailed {
		t.Fatalf("backup after reopen = %+v, want failed", after)
	}
}

// checkReopenedBackups checks that both backup rows survived close and reopen.
func checkReopenedBackups(
	t *testing.T, reopened BackupStore, running, finished backup.Record,
) {
	t.Helper()

	if gotRunning := mustGetBackup(t, reopened, running.ID); gotRunning != running {
		t.Fatalf("running backup after reopen = %+v, want %+v", gotRunning, running)
	}

	if gotFinished := mustGetBackup(t, reopened, finished.ID); gotFinished != finished {
		t.Fatalf("finished backup after reopen = %+v, want %+v", gotFinished, finished)
	}
}

// fenceOf returns the fence of the namespace ns in s, or the zero fence for the
// installation scope.
func fenceOf(t *testing.T, s BackupStore, ns namespace.Name) namespace.Fence {
	t.Helper()

	if ns == audit.ServerScope {
		return namespace.Fence{Namespace: "", FailoverVersion: 0}
	}

	n, err := s.GetNamespace(t.Context(), ns)
	if err != nil {
		t.Fatalf("get namespace %q: %v", ns, err)
	}

	return n.Fence()
}
