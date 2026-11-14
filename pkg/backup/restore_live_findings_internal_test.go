package backup

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"slices"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/blob"
	"opendustpan.org/opendustpan/pkg/installation"
)

const preBackupID ID = "bkp-pre"

// Errors of the stubs of these tests.
var (
	errThrottled     = errors.New("throttled")
	errRowUnreadable = errors.New("row unreadable")
	errKeyFault      = errors.New("cannot open sealed state: decrypt failed")
)

// damagedBytes returns the damaged live bytes of the tests.
func damagedBytes() []byte { return []byte("tampered") }

// md5Hex returns the lowercase hex MD5 of data.
func md5Hex(data []byte) string {
	h := blob.NewMD5()
	h.Write(data)

	return hex.EncodeToString(h.Sum(nil))
}

func blobRef(key string, data []byte, sealed bool) BlobRef {
	h := blob.NewMD5()
	h.Write(data)

	return BlobRef{MD5: [16]byte(h.Sum(nil)), Key: key, Size: int64(len(data)), Sealed: sealed}
}

// entryOf returns the entry of key in state, with the reference of a live row
// of wantBytes.
func entryOf(key string, state DamageState) DamagedBlob {
	reason := ReasonAbsent
	if state == DamageMismatch {
		reason = ReasonChecksum
	}

	return damagedEntry(blobRef(key, wantBytes(), false), state, reason)
}

// testManifest returns a manifest with no table file, which the reader
// accepts, with blobs and damaged.
func testManifest(t *testing.T, blobs []ManifestBlob, damaged []DamagedBlob) Manifest {
	t.Helper()

	files := make([]PortableFile, 0, len(Tables))
	counts := make(map[string]int64, len(Tables))
	empty := sha256.Sum256(nil)

	for _, table := range Tables {
		files = append(files, PortableFile{
			Table: table, Path: PortablePath(table), SHA256: hex.EncodeToString(empty[:]), Columns: nil, Rows: 0,
			Size: 0,
		})
		counts[table] = 0
	}

	size, sha, err := sqlAggregate(files)
	if err != nil {
		t.Fatalf("aggregate the SQL part: %v", err)
	}

	return Manifest{
		CreatedAt: time.Unix(1700000000, 0).UTC(), ID: preBackupID, Dialect: DialectSQLite,
		PrimaryInstallations: nil, WrapperIDs: nil, Blobs: blobs, Version: "test",
		SQLSHA256: hex.EncodeToString(sha[:]),
		Format:    FormatVersion, SchemaVersion: BaselineSchemaVersion, SQLSize: size, RowCounts: counts,
		SQLFormat: PortableFormat, SQLFiles: files, InstallationKind: installation.KindProduction, Unverified: nil,
		Damaged: damaged,
	}
}

// putManifest writes manifest to target as the manifest of preBackupID.
func putManifest(t *testing.T, target *mapStore, manifest Manifest) {
	t.Helper()

	data, err := json.Marshal(manifest)
	if err != nil {
		t.Fatalf("encode the manifest: %v", err)
	}

	target.objects["backups/"+preBackupID.String()+"/manifest.json"] = data

	for _, file := range manifest.SQLFiles {
		target.objects["backups/"+preBackupID.String()+"/"+file.Path] = []byte{}
	}
}

// TestValidDamagedRefusesHostileEntries sends hostile damaged entries to the
// check of the manifest reader.
func TestValidDamagedRefusesHostileEntries(t *testing.T) {
	t.Parallel()

	good := entryOf(liveKey, DamageMismatch)
	sum := good.MD5

	for _, tc := range []struct {
		name    string
		entries []DamagedBlob
	}{
		{name: "a protected key", entries: []DamagedBlob{{
			Key: "audit-checkpoints/x", State: DamageMismatch, Reason: ReasonChecksum, MD5: sum, Size: 1, Sealed: false,
		}}},
		{name: "a key with a dot segment", entries: []DamagedBlob{{
			Key: "namespaces/../x", State: DamageMissing, Reason: ReasonAbsent, MD5: sum, Size: 0, Sealed: false,
		}}},
		{name: "a key with a line break", entries: []DamagedBlob{{
			Key: liveKey + "\r\nx", State: DamageMissing, Reason: ReasonAbsent, MD5: sum, Size: 0, Sealed: false,
		}}},
		{name: "an unknown state", entries: []DamagedBlob{{
			Key: liveKey, State: "overwrite", Reason: ReasonChecksum, MD5: sum, Size: 1, Sealed: false,
		}}},
		{name: "an unknown reason", entries: []DamagedBlob{{
			Key: liveKey, State: DamageMismatch, Reason: "because", MD5: sum, Size: 1, Sealed: false,
		}}},
		{name: "a missing entry with a damage reason", entries: []DamagedBlob{{
			Key: liveKey, State: DamageMissing, Reason: ReasonSize, MD5: sum, Size: 0, Sealed: false,
		}}},
		{name: "a missing entry with no MD5", entries: []DamagedBlob{{
			Key: liveKey, State: DamageMissing, Reason: ReasonAbsent, MD5: "", Size: 0, Sealed: false,
		}}},
		{name: "a damaged entry with a short MD5", entries: []DamagedBlob{{
			Key: liveKey, State: DamageMismatch, Reason: ReasonSize, MD5: "abcd", Size: 1, Sealed: false,
		}}},
		{name: "an MD5 in upper case", entries: []DamagedBlob{{
			Key: liveKey, State: DamageMismatch, Reason: ReasonSize, MD5: strings.ToUpper(sum), Size: 1, Sealed: false,
		}}},
		{name: "a negative size", entries: []DamagedBlob{{
			Key: liveKey, State: DamageMismatch, Reason: ReasonSize, MD5: sum, Size: -1, Sealed: false,
		}}},
		{name: "a key twice", entries: []DamagedBlob{good, good}},
	} {
		if err := validDamaged(tc.entries); !errors.Is(err, errDamagedEntry) {
			t.Errorf("%s: validDamaged = %v, want errDamagedEntry", tc.name, err)
		}
	}

	if err := validDamaged([]DamagedBlob{good, entryOf(liveKey+"2", DamageMissing)}); err != nil {
		t.Fatalf("validDamaged of good entries = %v", err)
	}
}

// TestReadManifestRefusesAHostileDamagedList proves that the manifest reader
// refuses a damaged entry that is not valid, and a damaged key that is also
// among the blobs, so neither backup check nor a restore reads them.
func TestReadManifestRefusesAHostileDamagedList(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name    string
		blobs   []ManifestBlob
		damaged []DamagedBlob
	}{
		{name: "a protected key", blobs: nil, damaged: []DamagedBlob{{
			Key: "store-checks/chk-1/probe", State: DamageMismatch, Reason: ReasonChecksum,
			MD5: md5Hex(damagedBytes()), Size: 8, Sealed: false,
		}}},
		{
			name: "a damaged key that the backup also holds", blobs: []ManifestBlob{wantRef()},
			damaged: []DamagedBlob{entryOf(liveKey, DamageMissing)},
		},
	} {
		target := newMapStore(false, nil)
		putManifest(t, target, testManifest(t, tc.blobs, tc.damaged))

		if _, err := readManifest(t.Context(), target, preBackupID); !errors.Is(err, ErrCorrupt) {
			t.Errorf("%s: readManifest = %v, want ErrCorrupt", tc.name, err)
		}
	}
}

// TestCheckAcceptsABackupWithADamagedList proves that backup check does not
// refuse the backup before a restore because it found live blobs missing or
// damaged: that backup must stay restorable, so that it can undo the restore.
func TestCheckAcceptsABackupWithADamagedList(t *testing.T) {
	t.Parallel()

	target := newMapStore(false, nil)
	putManifest(t, target, testManifest(t, nil, []DamagedBlob{
		entryOf(liveKey, DamageMismatch), entryOf(liveKey+"2", DamageMissing),
	}))

	if err := Check(t.Context(), target, preBackupID); err != nil {
		t.Fatalf("Check = %v, want nil", err)
	}
}

// TestRestoreWantBlobsNamesTheDamagedKeys proves that the staged data of a
// backup with a damaged list must name those keys with the reference of the
// live row, and that a hostile entry stops the restore. The second case
// sends the entry to this layer directly, as if the manifest reader had
// failed.
func TestRestoreWantBlobsNamesTheDamagedKeys(t *testing.T) {
	t.Parallel()

	damaged := DamagedBlob{
		Key: liveKey + "2", State: DamageMismatch, Reason: ReasonSealed, MD5: md5Hex(wantBytes()), Size: 23,
		Sealed: true,
	}
	manifest := testManifest(t, []ManifestBlob{wantRef()}, []DamagedBlob{damaged})

	got, err := restoreWantBlobs(manifest, preBackupID)
	if err != nil {
		t.Fatalf("restoreWantBlobs = %v", err)
	}

	want := []BlobRef{blobRef(liveKey, wantBytes(), false), blobRef(liveKey+"2", wantBytes(), true)}
	if !slices.Equal(got, want) {
		t.Fatalf("restoreWantBlobs = %+v, want %+v", got, want)
	}

	for _, hostile := range []DamagedBlob{
		{Key: "audit-checkpoints/x", State: DamageMissing, Reason: ReasonAbsent, MD5: damaged.MD5, Size: 0, Sealed: false},
		{Key: liveKey + "2", State: DamageMissing, Reason: ReasonAbsent, MD5: "zz", Size: 0, Sealed: false},
	} {
		manifest.Damaged = []DamagedBlob{hostile}

		if _, err := restoreWantBlobs(manifest, preBackupID); !errors.Is(err, ErrCorrupt) {
			t.Errorf("restoreWantBlobs of %+v = %v, want ErrCorrupt", hostile, err)
		}
	}
}

// TestDamagedOnceRefusesAKeyInTwoLists sends a key that is both damaged and
// held, or both damaged and unverified, to the check directly.
func TestDamagedOnceRefusesAKeyInTwoLists(t *testing.T) {
	t.Parallel()

	held := testManifest(t, []ManifestBlob{wantRef()}, []DamagedBlob{entryOf(liveKey, DamageMissing)})
	unverified := testManifest(t, nil, []DamagedBlob{entryOf(liveKey, DamageMissing)})
	unverified.Unverified = []string{liveKey}

	for name, manifest := range map[string]Manifest{"held": held, "unverified": unverified} {
		if err := damagedOnce(manifest); !errors.Is(err, errDamagedEntry) {
			t.Errorf("%s: damagedOnce = %v, want errDamagedEntry", name, err)
		}
	}

	distinct := testManifest(t, []ManifestBlob{wantRef()}, []DamagedBlob{entryOf(liveKey+"2", DamageMissing)})
	if err := damagedOnce(distinct); err != nil {
		t.Fatalf("damagedOnce of distinct keys = %v", err)
	}
}

// TestMissingBlobLimitExceeded checks the bound on missing live blobs: more
// than 10, or more than 20 percent of the blobs that the live data names when
// more than 1. A count that cannot be true is above the bound.
func TestMissingBlobLimitExceeded(t *testing.T) {
	t.Parallel()

	limit := EffectiveMissingBlobLimit()
	if limit != (MissingBlobLimit{Count: 10, Percent: 20, PercentAbove: 1}) {
		t.Fatalf("EffectiveMissingBlobLimit = %+v, want 10 blobs, or 20 percent when more than 1", limit)
	}

	for _, tc := range []struct {
		name           string
		missing, named int
		want           bool
	}{
		{name: "an empty installation", missing: 0, named: 0, want: false},
		{name: "no missing blob", missing: 0, named: 50, want: false},
		{name: "1 of 5", missing: 1, named: 5, want: false},
		{name: "1 of 4", missing: 1, named: 4, want: false},
		{name: "1 of 2", missing: 1, named: 2, want: false},
		{name: "1 of 1", missing: 1, named: 1, want: false},
		{name: "2 of 4", missing: 2, named: 4, want: true},
		{name: "2 of 10", missing: 2, named: 10, want: false},
		{name: "3 of 10", missing: 3, named: 10, want: true},
		{name: "10 of 1000", missing: 10, named: 1000, want: false},
		{name: "10 of 50", missing: 10, named: 50, want: false},
		{name: "11 of 1000", missing: 11, named: 1000, want: true},
		{name: "every blob of 7", missing: 7, named: 7, want: true},
		{name: "every blob of 2", missing: 2, named: 2, want: true},
		{name: "a negative count", missing: -1, named: 5, want: true},
		{name: "a negative total", missing: 0, named: -1, want: true},
		{name: "more missing than named", missing: 3, named: 2, want: true},
	} {
		if got := limit.Exceeded(tc.missing, tc.named); got != tc.want {
			t.Errorf("%s: Exceeded(%d, %d) = %t, want %t", tc.name, tc.missing, tc.named, got, tc.want)
		}
	}
}

// heldBlobs returns n manifest entries of distinct keys.
func heldBlobs(n int) []ManifestBlob {
	out := make([]ManifestBlob, 0, n)

	for i := range n {
		ref := wantRef()
		ref.Key = liveKey + "/held" + strings.Repeat("x", i+1)
		out = append(out, ref)
	}

	return out
}

// missingEntries returns n missing entries of distinct keys, in key order.
func missingEntries(n int) []DamagedBlob {
	out := make([]DamagedBlob, 0, n)

	for i := range n {
		out = append(out, entryOf(liveKey+"/missing"+strings.Repeat("x", i+1), DamageMissing))
	}

	return out
}

// TestCheckLiveFindings proves the stop of a restore at the live blobs that
// the backup before it found: any damaged blob stops it, and missing blobs
// stop it above the bound. The text names the counts, the keys and the next
// step.
func TestCheckLiveFindings(t *testing.T) {
	t.Parallel()

	limit := EffectiveMissingBlobLimit()

	for _, tc := range []struct {
		name     string
		manifest Manifest
		want     error
		text     []string
	}{
		{name: "every live blob whole", manifest: testManifest(t, heldBlobs(3), nil), want: nil, text: nil},
		{name: "an empty installation", manifest: testManifest(t, nil, nil), want: nil, text: nil},
		{
			name: "1 missing of 6", manifest: testManifest(t, heldBlobs(5), missingEntries(1)), want: nil, text: nil,
		},
		{
			name:     "one damaged blob among many whole blobs",
			manifest: testManifest(t, heldBlobs(40), []DamagedBlob{entryOf(liveKey, DamageMismatch)}),
			want:     ErrLiveBlobsDamaged,
			text:     []string{"found 1 live blobs whose bytes do not match", `"` + liveKey + `"`, "opendustpan restore"},
		},
		{
			name: "every live blob missing", manifest: testManifest(t, nil, missingEntries(3)), want: ErrLiveBlobsMissing,
			text: []string{
				"found 3 of 3 live blobs missing", "10 blobs, or 20 percent when more than 1",
				"store check --target blobs",
			},
		},
		{
			name:     "11 missing of 1000",
			manifest: testManifest(t, heldBlobs(989), missingEntries(11)),
			want:     ErrLiveBlobsMissing,
			text:     []string{"found 11 of 1000 live blobs missing"},
		},
	} {
		mustLiveFindings(t, tc.name, checkLiveFindings(tc.manifest, limit), tc.want, tc.text)
	}
}

// mustLiveFindings checks err of the case name against want and the parts
// of its text.
func mustLiveFindings(t *testing.T, name string, err, want error, text []string) {
	t.Helper()

	if want == nil {
		if err != nil {
			t.Errorf("%s: checkLiveFindings = %v, want nil", name, err)
		}

		return
	}

	if err == nil || !errors.Is(err, want) || !errors.Is(err, ErrCorrupt) {
		t.Errorf("%s: checkLiveFindings = %v, want %v and ErrCorrupt", name, err, want)

		return
	}

	for _, part := range text {
		if !strings.Contains(err.Error(), part) {
			t.Errorf("%s: text %q misses %q", name, err.Error(), part)
		}
	}

	refused, ok := errors.AsType[*LiveBlobsError](err)
	if !ok || refused == nil || len(refused.Keys) > MaxReportedBlobKeys {
		t.Errorf("%s: error %T, want a *LiveBlobsError with at most %d keys", name, err, MaxReportedBlobKeys)
	}
}

// TestLiveBlobsErrorQuotesHostileKeys sends keys with line breaks and quotes
// to the text of the refusal directly, as if every check of the manifest had
// failed. The stored text holds no raw line break.
func TestLiveBlobsErrorQuotesHostileKeys(t *testing.T) {
	t.Parallel()

	for _, sentinel := range []error{ErrLiveBlobsDamaged, ErrLiveBlobsMissing} {
		refused := &LiveBlobsError{
			Sentinel: sentinel, Findings: BlobFindings{Named: 2, Missing: 2, Damaged: 2, Keys: nil},
			Limit: EffectiveMissingBlobLimit(), Keys: []string{"a\r\nINFO forged", `b"c`},
		}

		text := sanitizeRestoreError(refused)
		if strings.ContainsAny(text, "\r\n") || !strings.Contains(text, `"a\r\nINFO forged"`) {
			t.Errorf("text of %v = %q, want the keys quoted", sentinel, text)
		}
	}
}

// TestFindingsOfBoundsTheKeys proves that the findings count every entry and
// name at most MaxReportedBlobKeys keys.
func TestFindingsOfBoundsTheKeys(t *testing.T) {
	t.Parallel()

	entries := missingEntries(MaxReportedBlobKeys + 5)
	found := findingsOf(testManifest(t, heldBlobs(2), entries))

	if found.Named != MaxReportedBlobKeys+7 || found.Missing != MaxReportedBlobKeys+5 || found.Damaged != 0 ||
		len(found.Keys) != MaxReportedBlobKeys || found.Keys[0] != entries[0].Key {
		t.Fatalf("findingsOf = %+v", found)
	}
}

// keysOf returns the keys of blobs, in order.
func keysOf(blobs []ManifestBlob) []string {
	out := make([]string, 0, len(blobs))

	for _, ref := range blobs {
		out = append(out, ref.Key)
	}

	return out
}

// TestFindingsOfCountsEachKeyOnce proves that Named counts each key once,
// also a key that more than one list names, and that the missing-blob limit
// result stays the same for a manifest whose keys no two lists share.
func TestFindingsOfCountsEachKeyOnce(t *testing.T) {
	t.Parallel()

	limit := EffectiveMissingBlobLimit()

	for _, tc := range []struct {
		name       string
		blobs      []ManifestBlob
		unverified []string
		damaged    []DamagedBlob
		named      int
		missing    int
		mismatched int
		exceeded   bool
	}{
		{
			name:  "a key in the blobs and in the unverified keys",
			blobs: []ManifestBlob{wantRef()}, unverified: []string{liveKey}, damaged: nil,
			named: 1, missing: 0, mismatched: 0, exceeded: false,
		},
		{
			name:  "a distinct key in each list",
			blobs: []ManifestBlob{wantRef()}, unverified: []string{liveKey + "/unverified"},
			damaged: []DamagedBlob{entryOf(liveKey+"/damaged", DamageMismatch)},
			named:   3, missing: 0, mismatched: 1, exceeded: false,
		},
		{
			name:  "missing and damaged entries over unshared keys",
			blobs: heldBlobs(6), unverified: nil,
			damaged: append(missingEntries(3), entryOf(liveKey+"/damaged", DamageMismatch)),
			named:   10, missing: 3, mismatched: 1, exceeded: true,
		},
		{
			name:  "shared keys in the blobs and the unverified keys still exceed the limit",
			blobs: heldBlobs(7), unverified: keysOf(heldBlobs(7)),
			damaged: missingEntries(3),
			named:   10, missing: 3, mismatched: 0, exceeded: true,
		},
	} {
		manifest := testManifest(t, tc.blobs, tc.damaged)
		manifest.Unverified = tc.unverified

		found := findingsOf(manifest)

		if found.Named != tc.named || found.Missing != tc.missing || found.Damaged != tc.mismatched ||
			len(found.Keys) != tc.missing+tc.mismatched {
			t.Errorf("%s: findingsOf = %+v, want %d named, %d missing, %d damaged", tc.name, found, tc.named,
				tc.missing, tc.mismatched)
		}

		if got := limit.Exceeded(found.Missing, found.Named); got != tc.exceeded {
			t.Errorf("%s: Exceeded(%d, %d) = %t, want %t", tc.name, found.Missing, found.Named, got, tc.exceeded)
		}
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
		now: nil, version: "", maxSQL: 0, maxUnverified: 0, runner: nil, operations: nil, lease: 0,
		logger: slog.New(slog.DiscardHandler),
	}
}

// TestBackupCopyBlobsRecordsMissingAndDamagedBlobs proves that the backup
// before a restore records a missing and a damaged live blob in key order,
// with the reference of the live row, and copies the whole one, and that a
// backup that does not record damage stops at the first of them.
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

	// "tampered" is shorter than the bytes of the row.
	want := []DamagedBlob{
		damagedEntry(refs[1], DamageMismatch, ReasonSize), damagedEntry(refs[2], DamageMissing, ReasonAbsent),
	}

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
// errors of a sealed blob: plain bytes that do not match the row are damaged,
// sealed bytes that do not open are damaged with the reason unopened, a
// missing object is missing, and any other error stops the backup: a row that
// cannot be read, and an error that the opener does not mark.
func TestBackupCopyBlobsClassifiesSealedErrors(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name   string
		err    error
		stored bool
		// want is the reason of the entry, empty when the backup stops.
		want DamageReason
	}{
		{name: "plain bytes that do not match the row", err: ErrLiveBlobDamaged, stored: true, want: ReasonSealed},
		{
			name: "sealed bytes that do not open", err: fmt.Errorf("%w: %w", ErrLiveBlobUnopened, errKeyFault),
			stored: true, want: ReasonUnopened,
		},
		{name: "a missing object", err: blob.ErrNotFound, stored: false, want: ReasonAbsent},
		{name: "a row that cannot be read", err: errRowUnreadable, stored: true, want: ""},
		{name: "a key fault that the opener does not mark", err: errKeyFault, stored: true, want: ""},
		{name: "a corrupt error that is not damage", err: ErrCorrupt, stored: true, want: ""},
	} {
		source := newMapStore(true, nil)
		if tc.stored {
			source.objects[liveKey] = damagedBytes()
		}

		s := backupService(source, newMapStore(true, nil), fakeOpener{err: tc.err, source: source})
		ref := blobRef(liveKey, wantBytes(), true)

		_, entries, err := s.copyBlobs(t.Context(), []BlobRef{ref}, recordDamage)
		mustSealedEntry(t, tc.name, entries, err, tc.want)
	}
}

// mustSealedEntry checks the result of the copy of one sealed blob: no entry
// and an error when want is empty, else one entry of reason want with the
// reference of the sealed row.
func mustSealedEntry(t *testing.T, name string, entries []DamagedBlob, err error, want DamageReason) {
	t.Helper()

	if want == "" {
		if err == nil {
			t.Errorf("%s: copyBlobs = nil, want the error", name)
		}

		return
	}

	if err != nil || len(entries) != 1 || entries[0].Reason != want || entries[0].MD5 != md5Hex(wantBytes()) ||
		!entries[0].Sealed {
		t.Errorf("%s: copyBlobs = %+v, %v; want one entry %q with the reference of the sealed row", name, entries,
			err, want)
	}
}
