package backup

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"slices"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/blob"
	"opendustpan.org/opendustpan/pkg/durable"
	"opendustpan.org/opendustpan/pkg/installation"
)

const preBackupID ID = "bkp-pre"

// Errors of the stubs of these tests.
var (
	errAuditDown     = errors.New("the audit store is down")
	errThrottled     = errors.New("throttled")
	errRowUnreadable = errors.New("row unreadable")
)

// damagedBytes returns the damaged live bytes of the tests.
func damagedBytes() []byte { return []byte("tampered") }

// md5Hex returns the lowercase hex MD5 of data.
func md5Hex(data []byte) string {
	h := blob.NewMD5()
	h.Write(data)

	return hex.EncodeToString(h.Sum(nil))
}

// damagedEntry returns the damaged entry of the live key of the tests for
// data.
func damagedEntry(data []byte) DamagedBlob {
	return DamagedBlob{
		Key: liveKey, State: DamageMismatch, Reason: ReasonChecksum, MD5: md5Hex(data), Size: int64(len(data)),
	}
}

// putPreManifest writes the manifest of the backup before the restore, with
// damaged, to target. The manifest holds no table file, which the reader
// accepts.
func putPreManifest(t *testing.T, target *mapStore, damaged []DamagedBlob) {
	t.Helper()

	files := make([]PortableFile, 0, len(Tables))
	counts := make(map[string]int64, len(Tables))
	empty := sha256.Sum256(nil)

	for _, table := range Tables {
		files = append(files, PortableFile{
			Table:   table,
			Path:    PortablePath(table),
			SHA256:  hex.EncodeToString(empty[:]),
			Columns: nil,
			Rows:    0,
			Size:    0,
		})
		counts[table] = 0
	}

	size, sha, err := sqlAggregate(files)
	if err != nil {
		t.Fatalf("aggregate the SQL part: %v", err)
	}

	data, err := json.Marshal(Manifest{
		CreatedAt: time.Unix(1700000000, 0).UTC(), ID: preBackupID, Dialect: DialectSQLite,
		PrimaryInstallations: nil, WrapperIDs: nil, Blobs: nil, Version: "test", SQLSHA256: hex.EncodeToString(sha[:]),
		Format: FormatVersion, SchemaVersion: BaselineSchemaVersion, SQLSize: size, RowCounts: counts,
		SQLFormat: PortableFormat, SQLFiles: files, InstallationKind: installation.KindProduction, Unverified: nil,
		Damaged: damaged,
	})
	if err != nil {
		t.Fatalf("encode the manifest: %v", err)
	}

	target.objects["backups/"+preBackupID.String()+"/manifest.json"] = data
}

// preInput returns the input of a restore with the backup before it.
func preInput() restoreInput {
	in := keyInput()
	in.PreBackupID = preBackupID

	return in
}

func (u liveBlobSetup) copyKeyWith(t *testing.T, in restoreInput) error {
	t.Helper()

	return u.svc.copyCheckedKey(t.Context(), in, durable.StepFence{Operation: "op", Token: 1, Step: "copy"}, wantRef())
}

func hasEventPrefix(events []string, prefix string) bool {
	return slices.ContainsFunc(events, func(e string) bool { return strings.HasPrefix(e, prefix) })
}

// TestCopyCheckedKeyReplacesARecordedDamagedLiveBlob proves case (b): the
// live key holds the damaged bytes that the backup before the restore
// recorded, so the restore stores the audit event and writes the checked
// bytes of the backup, on a store that enforces conditional writes and on
// one that ignores them.
func TestCopyCheckedKeyReplacesARecordedDamagedLiveBlob(t *testing.T) {
	t.Parallel()

	for _, conditional := range []bool{true, false} {
		u := newLiveBlobSetup(conditional)
		u.blobs.objects[liveKey] = damagedBytes()
		putPreManifest(t, u.target, []DamagedBlob{damagedEntry(damagedBytes())})

		in := preInput()
		if err := u.copyKeyWith(t, in); err != nil {
			t.Fatalf("conditional %v: copy = %v, want nil", conditional, err)
		}

		if got, _ := u.blobs.liveBytes(); !bytes.Equal(got, wantBytes()) {
			t.Fatalf("conditional %v: live key = %q, want the bytes of the backup", conditional, got)
		}

		if !hasEventPrefix(u.live.events, "restore "+in.ID.String()+" replaced damaged live blob") {
			t.Fatalf("conditional %v: events = %q, want the replacement", conditional, u.live.events)
		}
	}
}

// TestCopyCheckedKeyRefusesDamageWithoutEvidence sends hostile evidence
// straight to the copy, as if every other layer had failed. A live key that
// differs from the backup is replaced only with a damaged entry of the key
// that names the bytes it holds now. Every other case refuses as case (c):
// ErrCorrupt, the audit event of the refusal, and no write.
func TestCopyCheckedKeyRefusesDamageWithoutEvidence(t *testing.T) {
	t.Parallel()

	other := []byte("other bytes")

	for _, tc := range []struct {
		name    string
		in      func() restoreInput
		damaged []DamagedBlob
		noPre   bool
	}{
		{name: "no backup before the restore", in: keyInput, damaged: nil, noPre: true},
		{name: "a check-only restore", in: func() restoreInput {
			in := preInput()
			in.CheckOnly = true

			return in
		}, damaged: []DamagedBlob{damagedEntry(damagedBytes())}, noPre: false},
		{name: "no manifest of the backup before the restore", in: preInput, damaged: nil, noPre: true},
		{name: "no entry of the key", in: preInput, damaged: nil, noPre: false},
		{name: "an entry of another key", in: preInput, damaged: []DamagedBlob{{
			Key: liveKey + "x", State: DamageMismatch, Reason: ReasonChecksum, MD5: md5Hex(damagedBytes()), Size: 8,
		}}, noPre: false},
		{name: "a missing entry for a key that holds bytes", in: preInput, damaged: []DamagedBlob{{
			Key: liveKey, State: DamageMissing, Reason: ReasonAbsent, MD5: "", Size: 0,
		}}, noPre: false},
		{name: "an entry of other bytes", in: preInput, damaged: []DamagedBlob{damagedEntry(other)}, noPre: false},
		{name: "an entry of another size", in: preInput, damaged: []DamagedBlob{{
			Key: liveKey, State: DamageMismatch, Reason: ReasonSize, MD5: md5Hex(damagedBytes()), Size: 9,
		}}, noPre: false},
		{name: "an entry that is not valid", in: preInput, damaged: []DamagedBlob{{
			Key: liveKey, State: DamageMismatch, Reason: ReasonChecksum,
			MD5: strings.ToUpper(md5Hex(damagedBytes())), Size: 8,
		}}, noPre: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			u := newLiveBlobSetup(true)
			u.blobs.objects[liveKey] = damagedBytes()

			if !tc.noPre {
				putPreManifest(t, u.target, tc.damaged)
			}

			in := tc.in()

			err := u.copyKeyWith(t, in)
			if !errors.Is(err, ErrCorrupt) {
				t.Fatalf("copy = %v, want ErrCorrupt", err)
			}

			if got, _ := u.blobs.liveBytes(); !bytes.Equal(got, damagedBytes()) || u.blobs.liveWrites() != 0 {
				t.Fatalf(
					"live key = %q after %d writes, want the damaged bytes and no write",
					got,
					u.blobs.liveWrites(),
				)
			}

			if !hasEventPrefix(u.live.events, "restore "+in.ID.String()+" refused to overwrite live blob") ||
				hasEventPrefix(u.live.events, "restore "+in.ID.String()+" replaced") {
				t.Fatalf("events = %q, want only the refusal", u.live.events)
			}
		})
	}
}

// TestCopyCheckedKeyKeepsDamageWhenTheEventFails proves that the restore
// deletes no damaged live blob when it cannot store the audit event of the
// replacement: it fails closed.
func TestCopyCheckedKeyKeepsDamageWhenTheEventFails(t *testing.T) {
	t.Parallel()

	u := newLiveBlobSetup(true)
	u.blobs.objects[liveKey] = damagedBytes()
	u.live.eventErr = errAuditDown
	putPreManifest(t, u.target, []DamagedBlob{damagedEntry(damagedBytes())})

	if err := u.copyKeyWith(t, preInput()); err == nil {
		t.Fatal("copy = nil, want the error of the event")
	}

	if got, _ := u.blobs.liveBytes(); !bytes.Equal(got, damagedBytes()) || u.blobs.liveWrites() != 0 {
		t.Fatalf("live key = %q, want the damaged bytes and no write", got)
	}
}

// TestRemoveDamagedRefusesAProtectedKey sends a key that a restore does not
// write straight to the delete of a damaged blob. It deletes nothing and
// stores no event.
func TestRemoveDamagedRefusesAProtectedKey(t *testing.T) {
	t.Parallel()

	u := newLiveBlobSetup(true)
	key := blob.Key("audit-checkpoints/epochs/0001/6163/abc")
	u.blobs.objects[key.String()] = []byte("checkpoint")

	ref := ManifestBlob{Key: key.String(), Size: 1, MD5: md5Hex([]byte("x")), Sealed: false}

	err := u.svc.removeDamaged(t.Context(), preInput(), noFence(), ref, key)
	if !errors.Is(err, ErrProtectedKey) {
		t.Fatalf("removeDamaged = %v, want ErrProtectedKey", err)
	}

	if _, ok := u.blobs.objects[key.String()]; !ok || len(u.live.events) != 0 {
		t.Fatalf("protected key kept %v, events %q; want it kept and no event", ok, u.live.events)
	}
}

// TestValidDamagedRefusesHostileEntries sends hostile damaged entries to the
// check of the manifest reader.
func TestValidDamagedRefusesHostileEntries(t *testing.T) {
	t.Parallel()

	good := damagedEntry(damagedBytes())

	for _, tc := range []struct {
		name    string
		entries []DamagedBlob
	}{
		{name: "a protected key", entries: []DamagedBlob{{
			Key: "audit-checkpoints/x", State: DamageMismatch, Reason: ReasonChecksum, MD5: good.MD5, Size: 1,
		}}},
		{name: "a key with a dot segment", entries: []DamagedBlob{{
			Key: "namespaces/../x", State: DamageMissing, Reason: ReasonAbsent, MD5: "", Size: 0,
		}}},
		{name: "an unknown state", entries: []DamagedBlob{{
			Key: liveKey, State: "overwrite", Reason: ReasonChecksum, MD5: good.MD5, Size: 1,
		}}},
		{name: "an unknown reason", entries: []DamagedBlob{{
			Key: liveKey, State: DamageMismatch, Reason: "because", MD5: good.MD5, Size: 1,
		}}},
		{name: "a missing entry with an MD5", entries: []DamagedBlob{{
			Key: liveKey, State: DamageMissing, Reason: ReasonAbsent, MD5: good.MD5, Size: 0,
		}}},
		{name: "a missing entry with a damage reason", entries: []DamagedBlob{{
			Key: liveKey, State: DamageMissing, Reason: ReasonSize, MD5: "", Size: 0,
		}}},
		{name: "a damaged entry with no MD5", entries: []DamagedBlob{{
			Key: liveKey, State: DamageMismatch, Reason: ReasonSize, MD5: "", Size: 1,
		}}},
		{name: "a damaged entry with a short MD5", entries: []DamagedBlob{{
			Key: liveKey, State: DamageMismatch, Reason: ReasonSize, MD5: "abcd", Size: 1,
		}}},
		{name: "a negative size", entries: []DamagedBlob{{
			Key: liveKey, State: DamageMismatch, Reason: ReasonSize, MD5: good.MD5, Size: -1,
		}}},
		{name: "a key twice", entries: []DamagedBlob{good, good}},
	} {
		if err := validDamaged(tc.entries); !errors.Is(err, errDamagedEntry) {
			t.Errorf("%s: validDamaged = %v, want errDamagedEntry", tc.name, err)
		}
	}

	if err := validDamaged([]DamagedBlob{good}); err != nil {
		t.Fatalf("validDamaged of a good entry = %v", err)
	}
}

// TestReadManifestRefusesAHostileDamagedEntry proves that the manifest reader
// refuses a damaged entry that is not valid, so neither backup check nor a
// restore reads it.
func TestReadManifestRefusesAHostileDamagedEntry(t *testing.T) {
	t.Parallel()

	target := newMapStore(false, nil)
	putPreManifest(t, target, []DamagedBlob{{
		Key: "store-checks/chk-1/probe", State: DamageMismatch, Reason: ReasonChecksum,
		MD5: md5Hex(damagedBytes()), Size: 8,
	}})

	if _, err := readManifest(t.Context(), target, preBackupID); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("readManifest = %v, want ErrCorrupt", err)
	}
}

// TestCheckRefusesABackupWithDamagedEntries proves that backup check reports
// a backup that holds no copy of a damaged live blob as not complete.
func TestCheckRefusesABackupWithDamagedEntries(t *testing.T) {
	t.Parallel()

	target := newMapStore(false, nil)
	putPreManifest(t, target, []DamagedBlob{damagedEntry(damagedBytes())})

	err := Check(t.Context(), target, preBackupID)
	if !errors.Is(err, ErrCorrupt) || !strings.Contains(err.Error(), "missing or damaged") {
		t.Fatalf("Check = %v, want ErrCorrupt that names the damaged blobs", err)
	}
}

// fakeOpener is a StateOpener that returns err, or the bytes of the live
// store when err is nil.
type fakeOpener struct {
	err    error
	source *mapStore
}

func (o fakeOpener) OpenStateBlob(ctx context.Context, key blob.Key) (io.ReadCloser, error) {
	if o.err != nil {
		return nil, o.err
	}

	return o.source.Get(ctx, key)
}

// errStore is a blob store whose Get fails with a read error that is not a
// missing object.
type errStore struct {
	*mapStore
}

func (errStore) Get(context.Context, blob.Key) (io.ReadCloser, error) {
	return nil, errThrottled
}

func backupService(source, target blob.Store, opener StateOpener) *Service {
	return &Service{
		exporter: nil, kinds: nil, registry: nil, source: source, opener: opener, target: target, targetName: "",
		now: nil, version: "", maxSQL: 0, runner: nil, lease: 0, logger: slog.New(slog.DiscardHandler),
	}
}

func blobRef(key string, data []byte, sealed bool) BlobRef {
	h := blob.NewMD5()
	h.Write(data)

	return BlobRef{MD5: [16]byte(h.Sum(nil)), Key: key, Size: int64(len(data)), Sealed: sealed}
}

// TestBackupCopyBlobsRecordsMissingAndDamagedBlobs proves that the backup
// before a restore records a missing and a damaged live blob in key order
// and copies the whole one, and that a backup that does not record damage
// stops at the first of them.
func TestBackupCopyBlobsRecordsMissingAndDamagedBlobs(t *testing.T) {
	t.Parallel()

	source := newMapStore(true, nil)
	target := newMapStore(true, nil)

	whole, damaged, missing := liveKey+"1", liveKey+"2", liveKey+"3"
	source.objects[whole] = wantBytes()
	source.objects[damaged] = damagedBytes()

	refs := []BlobRef{
		blobRef(whole, wantBytes(), false), blobRef(damaged, wantBytes(), false),
		blobRef(missing, wantBytes(), false),
	}

	s := backupService(source, target, nil)

	stored, entries, err := s.copyBlobs(t.Context(), refs, recordDamage)
	if err != nil {
		t.Fatalf("copyBlobs = %v", err)
	}

	if len(stored) != 1 || stored[0].Key != whole {
		t.Fatalf("stored = %+v, want only %s", stored, whole)
	}

	want := []DamagedBlob{
		{Key: damaged, State: DamageMismatch, Reason: ReasonChecksum, MD5: md5Hex(damagedBytes()), Size: 8},
		{Key: missing, State: DamageMissing, Reason: ReasonAbsent, MD5: "", Size: 0},
	}
	want[0].Reason = ReasonSize // "tampered" is shorter than the bytes of the row.

	if !slices.Equal(entries, want) {
		t.Fatalf("damaged = %+v, want %+v", entries, want)
	}

	if _, ok := target.objects["blobs/"+damaged]; ok {
		t.Fatal("the target holds a copy of the damaged live bytes")
	}

	strict := backupService(source, newMapStore(true, nil), nil)

	if _, _, err := strict.copyBlobs(t.Context(), refs, stopAtDamage); err == nil {
		t.Fatal("copyBlobs of a backup that does not record damage = nil, want the error of the damaged blob")
	}
}

// TestBackupCopyBlobsStopsAtAReadError proves that a read error that is not a
// missing object stops the backup before a restore: the code cannot tell it
// from damage.
func TestBackupCopyBlobsStopsAtAReadError(t *testing.T) {
	t.Parallel()

	s := backupService(errStore{mapStore: newMapStore(true, nil)}, newMapStore(true, nil), nil)

	_, _, err := s.copyBlobs(t.Context(), []BlobRef{blobRef(liveKey, wantBytes(), false)}, recordDamage)
	if err == nil || !strings.Contains(err.Error(), "throttled") {
		t.Fatalf("copyBlobs = %v, want the read error", err)
	}
}

// TestBackupCopyBlobsClassifiesSealedErrors proves the classification of the
// errors of a sealed blob: sealed bytes that do not open are damaged with the
// size and the MD5 of the stored bytes, a missing object is missing, and any
// other error, for example a row that cannot be read, stops the backup.
func TestBackupCopyBlobsClassifiesSealedErrors(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name    string
		err     error
		stored  bool
		want    DamageState
		stopped bool
	}{
		{name: "sealed bytes that do not open", err: ErrLiveBlobDamaged, stored: true, want: DamageMismatch, stopped: false},
		{name: "a missing object", err: blob.ErrNotFound, stored: false, want: DamageMissing, stopped: false},
		{name: "a row that cannot be read", err: errRowUnreadable, stored: true, want: "", stopped: true},
	} {
		source := newMapStore(true, nil)
		if tc.stored {
			source.objects[liveKey] = damagedBytes()
		}

		s := backupService(source, newMapStore(true, nil), fakeOpener{err: tc.err, source: source})

		_, entries, err := s.copyBlobs(t.Context(), []BlobRef{blobRef(liveKey, wantBytes(), true)}, recordDamage)
		if tc.stopped {
			if err == nil {
				t.Errorf("%s: copyBlobs = nil, want the error", tc.name)
			}

			continue
		}

		if err != nil || len(entries) != 1 || entries[0].State != tc.want {
			t.Errorf("%s: copyBlobs = %+v, %v; want one entry %q", tc.name, entries, err, tc.want)

			continue
		}

		if tc.want == DamageMismatch &&
			(entries[0].Reason != ReasonSealed || entries[0].MD5 != md5Hex(damagedBytes())) {
			t.Errorf("%s: entry = %+v, want the reason sealed and the MD5 of the stored bytes", tc.name, entries[0])
		}
	}
}
