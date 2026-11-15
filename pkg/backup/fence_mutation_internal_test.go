package backup

import (
	"errors"
	"log/slog"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/blob"
	"opendustpan.org/opendustpan/pkg/durable"
	"opendustpan.org/opendustpan/pkg/durable/durabletest"
	"opendustpan.org/opendustpan/pkg/installation"
)

// TestNewAttemptPrefixAllowlist proves that the attempt constructor refuses
// hostile input: a dot-dot or a slash never becomes a prefix, and only 1 to
// 20 ASCII digits count as an attempt.
func TestNewAttemptPrefixAllowlist(t *testing.T) {
	t.Parallel()

	const id = ID("bkp-allowlist")

	for _, attempt := range []string{"1", "2", "999", "12345678901234567890"} {
		prefix, err := newAttemptPrefix(id, attempt)
		if err != nil {
			t.Fatalf("newAttemptPrefix(%q): %v, want nil", attempt, err)
		}

		if prefix.prefix() != "backups/"+id.String()+"/"+attempt+"/" {
			t.Fatalf("prefix = %q, want the attempt directory", prefix.prefix())
		}
	}

	for _, attempt := range []string{
		"", ".", "..", "/", "1/2", "../1", "1/../2", "x", "-1", "+1", " 1", "1 ", "0x1", "１",
		"123456789012345678901",
	} {
		if _, err := newAttemptPrefix(id, attempt); err == nil {
			t.Fatalf("newAttemptPrefix(%q): err = nil, want a refusal", attempt)
		}
	}

	for _, bad := range []string{"", "nope", "bkp-"} {
		if _, err := newAttemptPrefix(ID(bad), "1"); err == nil {
			t.Fatalf("newAttemptPrefix(%q): err = nil, want a refusal", bad)
		}
	}
}

// claimBackupForTest starts the backup operation of id on ops and claims it,
// returning the fence of the live claim.
func claimBackupForTest(t *testing.T, ops durable.Store, id ID) durable.StepFence {
	t.Helper()

	_, _, err := ops.Start(t.Context(), durable.StartRequest{
		Key: OperationKey(id), Kind: OperationKind, Version: OperationVersion, Input: nil,
	})
	if err != nil {
		t.Fatalf("start backup operation %s: %v", id, err)
	}

	op, err := ops.Claim(t.Context(), durable.ClaimRequest{
		Owner:        "test",
		Kinds:        []durable.Kind{OperationKind},
		MaxVersions:  nil,
		Lease:        time.Minute,
		TakeCanceled: false,
	})
	if err != nil {
		t.Fatalf("claim backup operation %s: %v", id, err)
	}

	return op.Claim.Step(StepWrite)
}

// TestCheckClaimRefusesStaleFence proves that the pre-call fence check
// refuses a fence that no longer carries the live claim: a moved token, or a
// backup with no live claim at all.
func TestCheckClaimRefusesStaleFence(t *testing.T) {
	t.Parallel()

	const id = ID("bkp-fence-check")

	s := &Service{
		exporter: nil, kinds: nil, registry: nil, source: nil, opener: nil, target: nil, targetName: "",
		now: nil, version: "", maxSQL: 0, maxUnverified: 0, runner: nil, operations: durabletest.NewMem().Store(),
		lease: 0, logger: slog.New(slog.DiscardHandler),
	}
	fence := claimBackupForTest(t, s.operations, id)

	if err := s.checkClaim(t.Context(), fence, id); err != nil {
		t.Fatalf("checkClaim with the live fence: %v, want nil", err)
	}

	stale := durable.StepFence{Operation: fence.Operation, Token: fence.Token + 1, Step: StepWrite}

	if err := s.checkClaim(t.Context(), stale, id); !errors.Is(err, durable.ErrClaimLost) {
		t.Fatalf("checkClaim with a moved token: err = %v, want ErrClaimLost", err)
	}

	if err := s.checkClaim(t.Context(), fence, "bkp-fence-other"); !errors.Is(err, durable.ErrClaimLost) {
		t.Fatalf("checkClaim with no live claim: err = %v, want ErrClaimLost", err)
	}
}

// staleFenceService returns a backup service with a live claim on id and the
// fence of a stale claim on the same operation, with an empty target.
func staleFenceService(t *testing.T, id ID) (*Service, durable.StepFence, *mapStore) {
	t.Helper()

	target := newMapStore(true, nil)
	s := &Service{
		exporter: nil, kinds: nil, registry: nil, source: newMapStore(true, nil), opener: nil, target: target,
		targetName: "", now: nil, version: "test-1.0", maxSQL: 0, maxUnverified: 0, runner: nil,
		operations: durabletest.NewMem().Store(), lease: 0, logger: slog.New(slog.DiscardHandler),
	}
	fence := claimBackupForTest(t, s.operations, id)
	stale := durable.StepFence{Operation: fence.Operation, Token: fence.Token + 1, Step: StepWrite}

	return s, stale, target
}

// TestStaleFenceStopsBlobCopy is a mutation test of the pre-call fence check
// of the blob copy: a stale fence copies nothing, not even under its own
// prefix. Removing the checkClaim lets the copy land and fails this test.
func TestStaleFenceStopsBlobCopy(t *testing.T) {
	t.Parallel()

	const id = ID("bkp-fence-copy")

	s, stale, target := staleFenceService(t, id)

	data := []byte(`{"state":1}`)
	source, ok := s.source.(*mapStore)
	if !ok {
		t.Fatal("backup source is not a map store")
	}

	key, err := blob.ParseKey("namespaces/ns-aaaaaaaaaaaaaaaaaaaa/tofu/ws1/sv1/a1")
	if err != nil {
		t.Fatalf("parse key: %v", err)
	}

	source.objects[key.String()] = data

	hasher := blob.NewMD5()
	hasher.Write(data)

	var sum [16]byte

	copy(sum[:], hasher.Sum(nil))

	ref := BlobRef{Key: key.String(), Size: int64(len(data)), MD5: sum, Sealed: false}

	if _, err := s.copyBlob(t.Context(), copyTarget{fence: stale, id: id}, ref); err == nil ||
		!errors.Is(err, durable.ErrClaimLost) {
		t.Fatalf("copyBlob with a stale fence: err = %v, want ErrClaimLost", err)
	}

	if len(target.objects) != 0 {
		t.Fatalf("target holds %d objects after a stale copy, want none", len(target.objects))
	}
}

// TestStaleFenceStopsManifestWrite is a mutation test of the pre-call fence
// check of the manifest write: a stale fence writes no manifest, not even
// under its own prefix. Removing the checkClaim lets the write land and fails
// this test.
func TestStaleFenceStopsManifestWrite(t *testing.T) {
	t.Parallel()

	const id = ID("bkp-fence-manifest")

	s, stale, target := staleFenceService(t, id)

	prefix, err := newAttemptPrefix(id, "999")
	if err != nil {
		t.Fatalf("attempt prefix: %v", err)
	}

	in := manifestInput{
		startedAt: time.Time{},
		kind:      installation.KindProduction,
		id:        id,
		attempt:   "999",
		sum: Summary{
			Dialect:              DialectSQLite,
			PrimaryInstallations: nil,
			WrapperIDs:           nil,
			RowCounts:            nil,
			Blobs:                nil,
			Unverified:           nil,
			SchemaVersion:        BaselineSchemaVersion,
		},
		files:   nil,
		ordered: nil,
		damaged: nil,
		size:    0,
		sha:     [32]byte{},
		prefix:  prefix,
	}

	if err := s.writeManifest(t.Context(), stale, in); err == nil ||
		!errors.Is(err, durable.ErrClaimLost) {
		t.Fatalf("writeManifest with a stale fence: err = %v, want ErrClaimLost", err)
	}

	if len(target.objects) != 0 {
		t.Fatalf("target holds %d objects after a stale manifest write, want none", len(target.objects))
	}
}
