package job

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strconv"
	"time"

	"opendustpan.org/opendustpan/pkg/blob"
	"opendustpan.org/opendustpan/pkg/history"
	"opendustpan.org/opendustpan/pkg/secret"
)

// ErrHashMismatch reports content whose SHA-256 or size is not the one that the
// caller stated: an upload with another hash than its header, or a commit that
// names a size or a hash other than those of the part that the server holds.
var ErrHashMismatch = errors.New("the content does not have the stated hash")

// PlanCommitPart is one part that a commit names: the kind, and the size and the
// SHA-256 of its plaintext, as the caller says it uploaded it.
type PlanCommitPart struct {
	Kind   OutputKind
	Size   int64
	SHA256 string
}

// PlanRecorder stores the plan of a plan phase in three calls, the way a
// multipart upload does, so that any instance of the server can serve any call
// and none holds plaintext between two calls. The parts are sealed with the
// item key of the plan when they arrive. A part is not readable until the
// commit stores the plan with it. Every call checks the claim and its
// namespace fence in the transaction of its write, as Recorder does.
type PlanRecorder interface {
	// BeginPlan records the plan of the claim, with its item key, before any
	// part exists, and returns it. The ID of the plan is PlanIDOf(c), so a
	// second call with the same claim returns the same plan. It returns
	// ErrInvalid for a phase that is not a plan phase, ErrConflict for a phase
	// that has a plan, and ErrPlanStale (it fails the phase) when the state
	// version moved.
	BeginPlan(ctx context.Context, c PhaseClaim) (PlanRef, error)
	// PutPlanPart seals the content of body as the part kind of the plan of the
	// claim (BeginPlan runs first), and stores it. plainSHA256 is the SHA-256
	// of the plaintext in lowercase hex, which the caller stated: a body with
	// another hash is deleted and returns ErrHashMismatch. A body above
	// MaxPlanPart, a kind that is not a plan output, a hash that is not a
	// SHA-256 in hex, and a nil body return ErrInvalid. A repeat of a stored
	// part with the same content changes nothing, and with other content returns
	// ErrConflict or replaces the part, but a commit checks the hash of what it
	// reads in both cases. A phase that has a plan returns ErrConflict.
	PutPlanPart(ctx context.Context, c PhaseClaim, kind OutputKind, body io.Reader, plainSHA256 string) error
	// CommitPlan stores the plan of the claim with the named parts. It reads
	// back each part and checks its size and its SHA-256 against the named ones
	// (ErrHashMismatch), and returns ErrInvalid for a part that was not
	// uploaded, for no part or more than two, for a kind twice, and for a hash
	// or a size that is not valid. A second call with the same parts returns the
	// stored plan and changes nothing, and a call with other parts returns
	// ErrConflict. A part that is not named stays an orphan blob, which the
	// artifact sweep deletes.
	CommitPlan(ctx context.Context, c PhaseClaim, parts []PlanCommitPart) (PlanRef, error)
}

var _ PlanRecorder = (*DurableRecorder)(nil)

// planIDDigits is the number of hex digits of the digest in the ID of a plan.
const planIDDigits = 32

// PlanIDOf returns the ID of the plan that the claim c stores: a digest of the
// job, the phase and the token of the claim. It depends on nothing that the
// caller sends, so two instances of the server name the same plan for one
// claim, and a claim of one job never names the plan of another job.
func PlanIDOf(c PhaseClaim) string {
	sum := sha256.Sum256([]byte("dustpan plan\n" + string(c.Job) + "\n" + strconv.Itoa(c.Phase) + "\n" +
		strconv.FormatInt(c.Claim.Token, 10)))

	return PlanIDPrefix + hex.EncodeToString(sum[:])[:planIDDigits]
}

// BeginPlan implements PlanRecorder.
func (r *DurableRecorder) BeginPlan(ctx context.Context, c PhaseClaim) (PlanRef, error) {
	_, p, err := r.reservePlan(ctx, c)

	return p, err
}

// reservePlan reserves the plan of c, and returns the job as it read it.
func (r *DurableRecorder) reservePlan(ctx context.Context, c PhaseClaim) (Job, PlanRef, error) {
	// A repeat reserve, or a reserve that loses a race against the first one,
	// returns the plan of the first. One more try reads that row.
	var lastErr error

	for range 2 {
		j, err := r.jobOf(ctx, c)
		if err != nil {
			return Job{}, PlanRef{}, err
		}

		if c.Phase < 0 || c.Phase >= len(j.Phases) {
			return Job{}, PlanRef{}, fmt.Errorf("%w: job %s has no phase %d", ErrInvalid, j.ID, c.Phase)
		}

		// A cheap check before the write: the store checks the same in its
		// transaction.
		if checkErr := CheckPutPlan(&j, c.Phase, j.Phases[c.Phase].StateVersion); checkErr != nil {
			return Job{}, PlanRef{}, checkErr
		}

		p, err := r.reserveFor(ctx, c, &j)
		if err == nil {
			return j, p, nil
		}

		if !errors.Is(err, ErrConflict) {
			return Job{}, PlanRef{}, err
		}

		lastErr = err
	}

	return Job{}, PlanRef{}, lastErr
}

// reserveFor reserves the plan of c for the job j.
func (r *DurableRecorder) reserveFor(ctx context.Context, c PhaseClaim, j *Job) (PlanRef, error) {
	planID := PlanIDOf(c)
	art := ArtifactRecord{Job: j.ID, Unit: j.Spec.Unit, Step: "", Plan: nil, Log: nil, ChunkKeys: nil}

	ref, err := itemRef(&art, secret.ItemKindPlan, planID)
	if err != nil {
		return PlanRef{}, err
	}

	now := r.now().UTC()

	p, err := r.store.ReservePlan(ctx, c, &NewPlan{
		Plan: PlanRef{
			ID: planID, ItemKey: "", StateVersion: "", Parts: nil, CreatedAt: now,
			ExpiresAt: history.ExpiresAt(now, r.retention.Plan), RedactedAt: time.Time{},
		},
		Ref:    ref,
		NewKey: r.sealer.NewItemKey,
	})
	if err != nil {
		return PlanRef{}, fmt.Errorf("reserve plan %s: %w", planID, err)
	}

	return p, nil
}

// checkPartInput returns ErrInvalid for a kind that is not a plan output and a
// hash that is not a SHA-256 in lowercase hex.
func checkPartInput(kind OutputKind, sum string) error {
	switch {
	case kind != OutputPlanDisplay && kind != OutputPlanApply:
		return fmt.Errorf("%w: %q is not a plan output", ErrInvalid, kind)
	case !ValidHash(sum):
		return fmt.Errorf("%w: the hash of plan part %q is not a SHA-256 in hex", ErrInvalid, kind)
	default:
		return nil
	}
}

// PutPlanPart implements PlanRecorder.
func (r *DurableRecorder) PutPlanPart(
	ctx context.Context, c PhaseClaim, kind OutputKind, body io.Reader, sum string,
) error {
	if err := checkPartInput(kind, sum); err != nil {
		return err
	}

	if body == nil {
		return fmt.Errorf("%w: plan part %q has no content", ErrInvalid, kind)
	}

	j, plan, err := r.reservePlan(ctx, c)
	if err != nil {
		return err
	}

	at, err := r.planAtOf(ctx, &j, &plan)
	if err != nil {
		return err
	}

	key, err := PlanPartKey(j.ID, plan.ID, kind)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrInvalid, err)
	}

	counted := &countingReader{src: body, sum: sha256.New(), size: 0, limit: MaxPlanPart}

	if _, err = r.putPart(ctx, at, kind, key, counted); err != nil {
		if errors.Is(err, blob.ErrExists) {
			return r.repeatedPart(ctx, at, kind, sum)
		}

		// The write may have stored a part of the blob. No row names it.
		r.deleteParts(ctx, at, namedByKind(kind))

		return fmt.Errorf("store plan part %q: %w", kind, err)
	}

	if got := hex.EncodeToString(counted.sum.Sum(nil)); got != sum {
		r.deleteParts(ctx, at, namedByKind(kind))

		return fmt.Errorf("%w: plan part %q", ErrHashMismatch, kind)
	}

	return nil
}

// namedByKind returns the one part that deleteParts needs to find the blob of
// the part kind: it reads only the kind.
func namedByKind(kind OutputKind) []PlanPart {
	return []PlanPart{{Kind: kind, Size: 0, SHA256: "", Secret: false, StoredSize: 0, StoredMD5: ""}}
}

// repeatedPart answers an upload of a part that the blob store already holds:
// nil when it holds the same content, ErrConflict when it holds other content.
func (r *DurableRecorder) repeatedPart(ctx context.Context, at *planAt, kind OutputKind, sum string) error {
	m, err := r.measurePart(ctx, at, kind)
	if err != nil {
		return err
	}

	if m.sha256 != sum {
		return fmt.Errorf("%w: plan part %q was uploaded with other content", ErrConflict, kind)
	}

	return nil
}

// planAtOf returns the place of the plan p of the job j.
func (r *DurableRecorder) planAtOf(ctx context.Context, j *Job, p *PlanRef) (*planAt, error) {
	art := ArtifactRecord{Job: j.ID, Unit: j.Spec.Unit, Step: "", Plan: nil, Log: nil, ChunkKeys: nil}

	ref, err := itemRef(&art, secret.ItemKindPlan, p.ID)
	if err != nil {
		return nil, err
	}

	nsID, err := namespaceIDOf(ctx, r.namespaces, j.Spec.Unit.Namespace())
	if err != nil {
		return nil, err
	}

	return &planAt{ns: nsID, job: j.ID, plan: p.ID, item: ref}, nil
}

// measuredPart is what the recorder reads from a stored part: the size and the
// SHA-256 of its plaintext, and the digest of the sealed bytes.
type measuredPart struct {
	size   int64
	sha256 string
	stored blob.Digest
}

// measurePart opens the stored part kind, opens its seal, and reads it to its
// end. It reads at most MaxPlanPart bytes of plaintext. A part that was not
// uploaded returns ErrInvalid.
func (r *DurableRecorder) measurePart(ctx context.Context, at *planAt, kind OutputKind) (measuredPart, error) {
	key, err := PlanPartKey(at.job, at.plan, kind)
	if err != nil {
		return measuredPart{}, fmt.Errorf("%w: %w", ErrInvalid, err)
	}

	rc, err := r.blobs.OpenPlanPart(ctx, at.part(kind))
	if errors.Is(err, blob.ErrNotFound) {
		return measuredPart{}, fmt.Errorf("%w: plan part %q was not uploaded", ErrInvalid, kind)
	}

	if err != nil {
		return measuredPart{}, fmt.Errorf("open plan part %q: %w", kind, err)
	}

	measured := blob.NewDigestReader(rc)

	plain, err := r.sealer.OpenContent(ctx, at.item, []byte(key.String()), measured)
	if err != nil {
		return measuredPart{}, closeWith(rc, fmt.Errorf("open plan part %q: %w", kind, err))
	}

	sum := sha256.New()

	size, err := io.Copy(sum, io.LimitReader(plain, MaxPlanPart+1))
	if err != nil {
		return measuredPart{}, closeWith(rc, fmt.Errorf("read plan part %q: %w", kind, err))
	}

	if size > MaxPlanPart {
		tooLong := fmt.Errorf("%w: plan part %q is above %d bytes", ErrInvalid, kind, MaxPlanPart)

		return measuredPart{}, closeWith(rc, tooLong)
	}

	// The seal can end before the blob does. The digest counts every byte of the
	// blob, so read the rest.
	if _, err = io.Copy(io.Discard, measured); err != nil {
		return measuredPart{}, closeWith(rc, fmt.Errorf("read plan part %q: %w", kind, err))
	}

	if err = rc.Close(); err != nil {
		return measuredPart{}, fmt.Errorf("close plan part %q: %w", kind, err)
	}

	return measuredPart{size: size, sha256: hex.EncodeToString(sum.Sum(nil)), stored: measured.Digest()}, nil
}

// CommitPlan implements PlanRecorder.
func (r *DurableRecorder) CommitPlan(ctx context.Context, c PhaseClaim, named []PlanCommitPart) (PlanRef, error) {
	want := make([]PlanPart, 0, len(named))

	for _, n := range named {
		want = append(want, PlanPart{
			Kind: n.Kind, Size: n.Size, SHA256: n.SHA256, Secret: n.Kind == OutputPlanApply, StoredSize: 0,
			StoredMD5: "",
		})
	}

	if err := CheckPlanParts(want); err != nil {
		return PlanRef{}, err
	}

	j, err := r.jobOf(ctx, c)
	if err != nil {
		return PlanRef{}, err
	}

	if c.Phase < 0 || c.Phase >= len(j.Phases) {
		return PlanRef{}, fmt.Errorf("%w: job %s has no phase %d", ErrInvalid, j.ID, c.Phase)
	}

	if cur := j.Phases[c.Phase].Plan; cur != nil {
		return r.recommit(ctx, c, &j, want)
	}

	out, err := r.commit(ctx, c, want)
	if errors.Is(err, ErrConflict) {
		// A commit of the same claim can have won between the read and the
		// write. Read the job again, and answer as a repeat.
		again, readErr := r.jobOf(ctx, c)
		if readErr == nil && again.Phases[c.Phase].Plan != nil {
			return r.recommit(ctx, c, &again, want)
		}
	}

	return out, err
}

// commit reads back the parts, checks them against want, and stores the plan.
func (r *DurableRecorder) commit(ctx context.Context, c PhaseClaim, want []PlanPart) (PlanRef, error) {
	j, plan, err := r.reservePlan(ctx, c)
	if err != nil {
		return PlanRef{}, err
	}

	at, err := r.planAtOf(ctx, &j, &plan)
	if err != nil {
		return PlanRef{}, err
	}

	stored := make([]PlanPart, 0, len(want))

	for i := range want {
		w := &want[i]

		m, measureErr := r.measurePart(ctx, at, w.Kind)
		if measureErr != nil {
			return PlanRef{}, measureErr
		}

		if m.size != w.Size || m.sha256 != w.SHA256 {
			return PlanRef{}, fmt.Errorf(
				"%w: plan part %q is not the part that the commit names",
				ErrHashMismatch,
				w.Kind,
			)
		}

		stored = append(stored, PlanPart{
			Kind: w.Kind, Size: m.size, SHA256: m.sha256, Secret: w.Kind == OutputPlanApply,
			StoredSize: m.stored.Size, StoredMD5: hex.EncodeToString(m.stored.MD5[:]),
		})
	}

	plan.Parts = stored

	out, err := r.store.PutPlan(ctx, c, plan)
	if err != nil {
		return PlanRef{}, fmt.Errorf("put plan %s: %w", plan.ID, err)
	}

	return out, nil
}

// recommit answers a commit for a phase that has a plan: the plan when the
// claim stored it with the named parts, ErrConflict for any other plan. It
// checks that the claim is live first, so a holder that lost its claim learns
// it.
func (r *DurableRecorder) recommit(ctx context.Context, c PhaseClaim, j *Job, want []PlanPart) (PlanRef, error) {
	cur := j.Phases[c.Phase].Plan

	// The reserve checks the claim before it checks the phase. A live claim
	// finds the plan of the phase and returns ErrConflict. Any other error
	// is the answer.
	if _, err := r.reserveFor(ctx, c, j); err != nil && !errors.Is(err, ErrConflict) {
		return PlanRef{}, err
	}

	if cur.ID != PlanIDOf(c) || len(cur.Parts) != len(want) {
		return PlanRef{}, fmt.Errorf("%w: phase %d of job %s has plan %s", ErrConflict, c.Phase, j.ID, cur.ID)
	}

	for i := range want {
		have := cur.Part(want[i].Kind)
		if have == nil || have.Size != want[i].Size || have.SHA256 != want[i].SHA256 {
			return PlanRef{}, fmt.Errorf("%w: plan %s has other parts than the commit names", ErrConflict, cur.ID)
		}
	}

	return *cur, nil
}
