package backup_test

import (
	"bytes"
	"io"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/store/sqlite"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/backup"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/profile"
)

// TestEmptiedTables proves that the table of open sessions is in
// EmptiedTables: a backup leaves it out and a restore leaves it empty.
func TestEmptiedTables(t *testing.T) {
	t.Parallel()

	if !slices.Contains(backup.EmptiedTables, backup.TableProfileOpenSessions) {
		t.Errorf("EmptiedTables lacks %q: a backup would hold open sessions", backup.TableProfileOpenSessions)
	}

	if slices.Contains(backup.Tables, backup.TableProfileOpenSessions) {
		t.Errorf("Tables holds %q: a backup would hold open sessions", backup.TableProfileOpenSessions)
	}

	if slices.Contains(backup.ControlTables, backup.TableProfileOpenSessions) {
		t.Errorf("ControlTables holds %q: a restore would keep open sessions", backup.TableProfileOpenSessions)
	}
}

// TestBackupHoldsNoProfileSession proves that a backup of a database with a
// live open session holds no session: the export has no table file for the
// session table and no exported bytes name the session. A restore built from
// the export then holds an empty session table.
func TestBackupHoldsNoProfileSession(t *testing.T) {
	t.Parallel()

	store, _, sess := openStoreWithSession(t)

	exported, files := exportPortable(t, store)

	checkNoSessionTable(t, exported, files, sess)
}

// openStoreWithSession opens a store with one namespace, one profile and one
// live open session, and answers all three.
func openStoreWithSession(t *testing.T) (*sqlite.Store, namespace.Namespace, profile.Session) {
	t.Helper()

	ctx := t.Context()

	store, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "dustpan.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	t.Cleanup(func() {
		if closeErr := store.Close(); closeErr != nil {
			t.Errorf("close store: %v", closeErr)
		}
	})

	ns := namespace.Namespace{
		ID: namespace.NewID(), CreatedAt: time.Now().UTC(), Name: "acme", PrimaryInstallation: "eu-west",
		FailoverVersion: 1, Overrides: namespace.Overrides{Forced: false, Unlocked: false},
	}

	if err := store.CreateNamespace(ctx, ns); err != nil {
		t.Fatalf("create namespace: %v", err)
	}

	fence := ns.Fence()

	rootID := profile.NewID()
	now := time.Now().UTC()

	p := profile.Profile{
		ID: rootID, Namespace: ns.Name, Name: "web", Latest: 1, CreatedAt: now, CreatedBy: "tester",
		UpdatedAt: now,
	}
	r := profile.Revision{
		Profile: rootID, Number: 1, Document: []byte("schemaVersion: 1\nvalues:\n  region: eu-west-1\n"),
		SHA256: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef", Author: "tester",
		CreatedAt: now, Reason: "",
	}

	if _, err := store.Profiles().Create(ctx, p, r, fence, openEntries(ns.Name, now, "tester")); err != nil {
		t.Fatalf("create profile: %v", err)
	}

	sess := profile.Session{
		ID: "pos-0123456789abcdefghij", Namespace: ns.Name, Root: rootID,
		Owner: profile.SessionOwner{Kind: profile.OwnerPrincipal, ID: "tester"}, Consumer: "api",
		Paths: []string{"region"}, Sealed: []byte("sealed-answer"), CreatedAt: now,
		ExpiresAt: now.Add(2 * time.Hour),
	}

	if err := store.Sessions().Put(ctx, sess, fence, openEntries(ns.Name, now, "tester")); err != nil {
		t.Fatalf("put session: %v", err)
	}

	// The session reads before the backup, so the export below has a live
	// session to leave out.
	if _, err := store.Sessions().Get(ctx, ns.Name, sess.ID, sess.Owner, now.Add(time.Minute)); err != nil {
		t.Fatalf("get session: %v", err)
	}

	return store, ns, sess
}

// exportPortable exports the store and answers the table files with the bytes
// of each file by path.
func exportPortable(t *testing.T, store *sqlite.Store) ([]backup.PortableFile, map[string][]byte) {
	t.Helper()

	ctx := t.Context()

	files := map[string][]byte{}

	sink := func(path string) (io.WriteCloser, error) {
		buf := &bytes.Buffer{}
		files[path] = nil

		return writeCloser{buf: buf, done: func() { files[path] = buf.Bytes() }}, nil
	}

	_, exported, err := store.ExportPortable(ctx, sink)
	if err != nil {
		t.Fatalf("export: %v", err)
	}

	return exported, files
}

// checkNoSessionTable checks that no export file holds the session table or
// the session ID, and that the profiles still back up.
func checkNoSessionTable(
	t *testing.T, exported []backup.PortableFile, files map[string][]byte, sess profile.Session,
) {
	t.Helper()

	for _, f := range exported {
		if f.Table == backup.TableProfileOpenSessions {
			t.Errorf("export holds a table file for %q", backup.TableProfileOpenSessions)
		}
	}

	var profileFiles int

	for _, f := range exported {
		if f.Table == "profiles" || f.Table == "profile_revisions" {
			profileFiles++
		}
	}

	if profileFiles != 2 {
		t.Errorf("export holds %d profile table files, want 2: the profiles back up but the sessions do not",
			profileFiles)
	}

	for path, body := range files {
		if bytes.Contains(body, []byte(sess.ID)) {
			t.Errorf("export file %q holds the session ID", path)
		}
	}
}

// openEntries is one audit entry of a profile write of the backup test.
func openEntries(ns namespace.Name, at time.Time, subject string) []audit.Entry {
	return []audit.Entry{{
		At: at, Namespace: ns, Subject: subject, IDPSubject: "", TokenID: "", Action: "profile:write",
		Object: "namespace:" + ns.String(), Version: "", ValuePath: "", ValueCount: nil, Changes: nil, SourceIP: "",
		UserAgent: "", Channel: audit.ChannelAPI, Kind: audit.KindChange, Outcome: audit.OutcomeSuccess,
		RequestID: "", ObjectID: "", PrevHash: "", Hash: "", Sequence: 0, FenceVersion: 0, PrincipalKind: "",
		JobID: "", Reason: "", Origin: nil,
	}}
}

// writeCloser collects the bytes of one export file.
type writeCloser struct {
	buf  *bytes.Buffer
	done func()
}

func (w writeCloser) Write(p []byte) (int, error) {
	//nolint:wrapcheck // Transparent writer: callers must see the original error.
	return w.buf.Write(p)
}

func (w writeCloser) Close() error {
	w.done()

	return nil
}
