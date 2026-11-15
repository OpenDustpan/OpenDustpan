package sqlite_test

import (
	"crypto/md5" //nolint:gosec // md5.Size only sizes the zero BlobMD5 field; the benchmark computes no hash.
	"crypto/sha256"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/ident"
	"opendustpan.org/opendustpan/internal/token"
	"opendustpan.org/opendustpan/pkg/blob"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/stack"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
	"opendustpan.org/opendustpan/pkg/store/storetest"
)

// benchResourceBytes is the approximate size of one benchmark resource, so a
// deployment of 1,000 resources is about 3 MB, like a large Pulumi stack.
const benchResourceBytes = 3 << 10

// benchDeployment builds a deployment with managed entries of about
// benchResourceBytes each. Every entry carries the realistic shape the issue
// names (urn, type, custom, id, inputs, outputs, parent, dependencies,
// provider); providers and components are mixed in, and one entry holds
// braces, quotes and backslashes in its inputs, so the count scans realistic
// text. Only the automatic stack resource is uncounted.
func benchDeployment(b *testing.B, managed int) []byte {
	b.Helper()

	probe := benchResource(0, "")
	pad := strings.Repeat("x", (benchResourceBytes-len(probe))/2)

	var sb strings.Builder

	sb.WriteString(`{"manifest":{"version":3},"resources":[`)
	sb.WriteString(`{"urn":"urn:pulumi:dev::web::pulumi:pulumi:Stack::web-dev","type":"pulumi:pulumi:Stack"}`)

	for i := range managed {
		sb.WriteByte(',')
		sb.WriteString(benchResource(i, pad))
	}

	sb.WriteString(`]}`)

	deployment := []byte(sb.String())

	if got := stack.CountManagedResources(deployment); got != int64(managed) {
		b.Fatalf("bench deployment of %d resources counts as %d", managed, got)
	}

	if avg := len(deployment) / managed; avg < 2<<10 || avg > 4<<10 {
		b.Fatalf("bench resource averages %d bytes, want about %d", avg, benchResourceBytes)
	}

	return deployment
}

// benchResource renders entry i with pad as the filler of its inputs and
// outputs. The pad slots keep every entry near benchResourceBytes.
func benchResource(i int, pad string) string {
	n := strconv.Itoa(i)

	switch {
	case i%50 == 0:
		return `{"urn":"urn:pulumi:dev::web::pulumi:providers:aws::default-` + n +
			`","type":"pulumi:providers:aws","custom":true,"id":"default-` + n +
			`","inputs":{"version":"6.0.0","pad":"` + pad +
			`"},"outputs":{"version":"6.0.0","pad":"` + pad + `"}}`
	case i%25 == 0:
		return `{"urn":"urn:pulumi:dev::web::pkg:index:Component::comp-` + n +
			`","type":"pkg:index:Component","custom":false,"parent":"urn:pulumi:dev::web::pulumi:pulumi:Stack::web-dev",` +
			`"inputs":{"name":"comp-` + n + `","pad":"` + pad +
			`"},"outputs":{"name":"comp-` + n + `","pad":"` + pad + `"}}`
	default:
		note := ""
		if i == 1 {
			note = `,"note":` + strconv.Quote(`brace { and quote " and backslash \`)
		}

		return `{"urn":"urn:pulumi:dev::web::aws:s3/bucket:Bucket::bucket-` + n +
			`","type":"aws:s3/bucket:Bucket","custom":true,"id":"bucket-` + n +
			`","parent":"urn:pulumi:dev::web::pkg:index:Component::comp-` + n +
			`","provider":"urn:pulumi:dev::web::pulumi:providers:aws::default-` + n +
			`","dependencies":["urn:pulumi:dev::web::aws:iam/role:Role::role-` + n + `"]` +
			`,"inputs":{"name":"bucket-` + n + `","tags":{"team":"platform"` + note + `},"pad":"` + pad +
			`"},"outputs":{"arn":"arn:aws:s3:::bucket-` + n + `","pad":"` + pad + `"}}`
	}
}

// benchSaveFixture is a store holding namespace "acme" and stack
// "acme/web/prod" for the save benchmarks.
type benchSaveFixture struct {
	store *sqlite.Store
	ref   stack.Ref
	fence namespace.Fence

	// nsID and stackID are the IDs that the checkpoint blob keys hold.
	nsID    namespace.ID
	stackID stack.ID
}

// openBenchSaveStore returns a fresh store in a temporary directory with one
// namespace and stack.
func openBenchSaveStore(b *testing.B) *benchSaveFixture {
	b.Helper()

	ctx := b.Context()

	s, err := sqlite.Open(ctx, filepath.Join(b.TempDir(), "bench.db"))
	if err != nil {
		b.Fatalf("open store: %v", err)
	}

	b.Cleanup(func() {
		if closeErr := s.Close(); closeErr != nil {
			b.Errorf("close store: %v", closeErr)
		}
	})

	now := time.Date(2026, time.October, 12, 12, 0, 0, 0, time.UTC)

	ns := namespace.Namespace{
		ID:                  namespace.NewID(),
		CreatedAt:           now,
		Name:                "acme",
		PrimaryInstallation: "eu-west",
		FailoverVersion:     1,
		Overrides:           namespace.Overrides{Forced: false, Unlocked: false},
	}
	if err = s.CreateNamespace(ctx, ns); err != nil {
		b.Fatalf("create namespace: %v", err)
	}

	ref, err := stack.ParseRef("acme", "web", "prod")
	if err != nil {
		b.Fatalf("parse ref: %v", err)
	}

	stackID := stack.ID(ident.NewID("stk-"))

	st := stack.Stack{
		NamespaceID: "",
		CreatedAt:   now,
		UpdatedAt:   now,
		Tags:        map[string]string{},
		Ref:         ref,
		ID:          stackID,
		Version:     0,
	}

	if err = s.CreateStack(ctx, st, ns.Fence()); err != nil {
		b.Fatalf("create stack: %v", err)
	}

	return &benchSaveFixture{store: s, ref: ref, fence: ns.Fence(), nsID: ns.ID, stackID: stackID}
}

// startBenchUpdate creates and starts update id of the fixture stack and
// returns it running under its lease.
func (f *benchSaveFixture) startBenchUpdate(b *testing.B, id string) stack.Update {
	b.Helper()

	ctx := b.Context()

	u := stack.Update{
		TokenID:         "",
		JournalVersion:  0,
		CreatedAt:       time.Now().UTC(),
		StartedAt:       time.Time{},
		EndedAt:         time.Time{},
		Lease:           stack.Lease{ExpiresAt: time.Time{}, Digest: token.Digest{}},
		ID:              stack.UpdateID(id),
		Kind:            stack.KindUpdate,
		Status:          stack.StatusNotStarted,
		Ref:             f.ref,
		DryRun:          false,
		Version:         0,
		Revision:        1,
		CreatedBy:       "",
		RestoredFrom:    0,
		CheckpointBytes: 0, JobID: "", GitHead: "",
	}

	if err := f.store.CreateUpdate(ctx, u, storetest.UnrecordedKey(), f.fence); err != nil {
		b.Fatalf("create update %s: %v", id, err)
	}

	tok, err := token.New()
	if err != nil {
		b.Fatalf("new lease token: %v", err)
	}

	u.Status = stack.StatusRunning
	u.StartedAt = time.Now().UTC()
	u.Lease = stack.Lease{ExpiresAt: time.Now().UTC().Add(stack.DefaultLease), Digest: tok.Digest()}
	u.Revision++

	started, err := f.store.StartUpdate(ctx, u, f.fence)
	if err != nil {
		b.Fatalf("start update %s: %v", id, err)
	}

	return started
}

// endBenchUpdate ends running with success, releasing the stack for the
// next benchmark update.
func (f *benchSaveFixture) endBenchUpdate(b *testing.B, u stack.Update) {
	b.Helper()

	u.Status = stack.StatusSucceeded
	u.EndedAt = time.Now().UTC()
	u.Lease = stack.Lease{ExpiresAt: time.Time{}, Digest: token.Digest{}}
	u.Revision++

	if err := f.store.SaveUpdate(b.Context(), u, f.fence); err != nil {
		b.Fatalf("end update %s: %v", u.ID, err)
	}
}

// benchCheckpoint returns the checkpoint save of u with sequence seq holding
// deployment. The blob key has the form that the save path builds,
// namespaces/<namespace ID>/pulumi/<stack ID>/<update ID>/<sequence>-<attempt ID>, which
// the store checks before it writes the row. The content lives in the blob
// store, so the SQL row keeps metadata only, while the removed count scanned
// the full deployment bytes.
func (f *benchSaveFixture) benchCheckpoint(
	u stack.Update,
	seq int64,
	deployment []byte,
	digest [sha256.Size]byte,
) stack.Checkpoint {
	dir := "namespaces/" + f.nsID.String() + "/pulumi/" + f.stackID.String() + "/" + u.ID.String() + "/"
	key := blob.Key(dir + strconv.FormatInt(seq, 10) + "-" + ident.NewID(""))

	return stack.Checkpoint{
		NamespaceID:   "",
		StackID:       "",
		SavedAt:       time.Now().UTC(),
		Features:      []string{"deltaCheckpointUploadsV2", "verbatim"},
		Deployment:    deployment,
		Update:        u.ID,
		Ref:           u.Ref,
		Lease:         u.Lease.Digest,
		SchemaVersion: 3,
		Sequence:      seq,
		Digest:        digest,
		Invalid:       false,
		BlobKey:       key,
		BlobSize:      0,
		BlobMD5:       [md5.Size]byte{},

		AttemptStartedAt: time.Now().UTC(), Origin: identity.StateOrigin{Job: "", Commit: ""},
	}
}

// benchFullSave benchmarks one full checkpoint save: an unsequenced
// PutCheckpoint of the same update, as the CLI's full-checkpoint route
// issues on every resource operation.
func benchFullSave(b *testing.B, managed int) {
	b.Helper()

	ctx := b.Context()
	f := openBenchSaveStore(b)
	deployment := benchDeployment(b, managed)
	digest := sha256.Sum256(deployment)
	running := f.startBenchUpdate(b, "bench-full")

	b.SetBytes(int64(len(deployment)))
	b.ResetTimer()

	for b.Loop() {
		first := f.benchCheckpoint(running, 0, deployment, digest)
		if _, err := f.store.PutCheckpoint(ctx, first, f.fence); err != nil {
			b.Fatalf("save checkpoint: %v", err)
		}
	}
}

// benchDeltaSave benchmarks one sequenced checkpoint save on a fresh update.
// Every delta commit funnels through the same PutCheckpoint the full save
// uses, so this is the per-save cost of delta mode: the first sequenced save
// of an update, with the update setup outside the timer.
func benchDeltaSave(b *testing.B, managed int) {
	b.Helper()

	ctx := b.Context()
	f := openBenchSaveStore(b)
	deployment := benchDeployment(b, managed)
	digest := sha256.Sum256(deployment)

	b.SetBytes(int64(len(deployment)))
	b.ResetTimer()

	serial := 0

	for b.Loop() {
		b.StopTimer()
		running := f.startBenchUpdate(b, "bench-delta-"+strconv.Itoa(serial))
		serial++
		b.StartTimer()

		_, saveErr := f.store.PutCheckpoint(ctx, f.benchCheckpoint(running, 1, deployment, digest), f.fence)
		if saveErr != nil {
			b.Fatalf("save checkpoint: %v", saveErr)
		}

		b.StopTimer()
		f.endBenchUpdate(b, running)
		b.StartTimer()
	}
}

// benchCount benchmarks the removed per-save cost alone:
// CountManagedResources on the same deployments the saves store.
func benchCount(b *testing.B, managed int) {
	b.Helper()

	deployment := benchDeployment(b, managed)

	b.SetBytes(int64(len(deployment)))
	b.ResetTimer()

	for b.Loop() {
		if got := stack.CountManagedResources(deployment); got != int64(managed) {
			b.Fatalf("count = %d, want %d", got, managed)
		}
	}
}

// BenchmarkCheckpointSaveFull1000 saves one ~3 MB checkpoint (1,000
// resources) per iteration through the real store save path.
func BenchmarkCheckpointSaveFull1000(b *testing.B) {
	benchFullSave(b, 1000)
}

// BenchmarkCheckpointSaveFull3000 saves one ~9 MB checkpoint (3,000
// resources) per iteration through the real store save path.
func BenchmarkCheckpointSaveFull3000(b *testing.B) {
	benchFullSave(b, 3000)
}

// BenchmarkCheckpointSaveDelta1000 saves one sequenced ~3 MB checkpoint
// (1,000 resources) per iteration: the per-save cost of delta mode.
func BenchmarkCheckpointSaveDelta1000(b *testing.B) {
	benchDeltaSave(b, 1000)
}

// BenchmarkCheckpointSaveDelta3000 saves one sequenced ~9 MB checkpoint
// (3,000 resources) per iteration: the per-save cost of delta mode.
func BenchmarkCheckpointSaveDelta3000(b *testing.B) {
	benchDeltaSave(b, 3000)
}

// BenchmarkCountManagedResources1000 counts one ~3 MB deployment (1,000
// resources) per iteration: the work the save path no longer does.
func BenchmarkCountManagedResources1000(b *testing.B) {
	benchCount(b, 1000)
}

// BenchmarkCountManagedResources3000 counts one ~9 MB deployment (3,000
// resources) per iteration: the work the save path no longer does.
func BenchmarkCountManagedResources3000(b *testing.B) {
	benchCount(b, 3000)
}
