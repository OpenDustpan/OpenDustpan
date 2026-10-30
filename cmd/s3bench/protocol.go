package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"opendustpan.org/opendustpan/pkg/blob"
)

// The protocol run compares the old path (a create-only PUT with
// If-None-Match) and the new path (a key for each attempt, and the fenced
// row decides) for each blob family. Each run does a fixed count of
// operations at a fixed client concurrency. The runs alternate: old, new,
// old, new. Every family is a model: it sends the real requests of each
// path in their order, against the PostgreSQL model tables.

// Modes of a protocol run.
const (
	modeOld = "old"
	modeNew = "new"
)

// Object sizes of the protocol families, in bytes.
const (
	// checkpointBytes is the deployment of 256 KiB of the load harness,
	// in its versioned envelope.
	checkpointBytes = 262409
	// journalBatchBytes is a journal batch of 20 entries of the load
	// harness.
	journalBatchBytes = 8856
	// logChunkBytes is an assumption: the load harness records no job log.
	// The runner protocol allows 256 MiB in 20,000 chunks, about 13 KiB
	// for each chunk.
	logChunkBytes = 16 << 10
	// auditCheckpointBytes is an assumption: a signed checkpoint is a small
	// JSON document.
	auditCheckpointBytes = 1 << 10
)

// errGetBody reports a GET that did not answer the whole object with its
// hash.
var errGetBody = errors.New("GET answered a body of another size or hash")

// family is one blob family of the protocol: its object size and the two
// paths.
type family struct {
	Name  string `json:"name"`
	Bytes int    `json:"bytes"`
	Read  bool   `json:"read"`
	// OpBytes is the count of bytes that one operation moves, when it is not
	// Bytes, the size of the object of the family.
	OpBytes int `json:"opBytes"`
	// Candidate marks a model of a design that no code has. The record
	// keeps it apart and gives it no verdict.
	Candidate bool   `json:"candidate"`
	OldPath   string `json:"oldPath"`
	NewPath   string `json:"newPath"`
	// seed writes the objects that a read family reads, before its runs.
	seed func(ctx context.Context, r *protoRun) error
	// pause is the wait after each run, outside the timing. See restorePresent.
	pause time.Duration
	old   func(ctx context.Context, r *protoRun, i int) error
	new   func(ctx context.Context, r *protoRun, i int) error
}

// protoRun is what the operations of one run share.
type protoRun struct {
	target *s3Target
	db     *pgModel
	// prefix is unique for each run. readPrefix is the prefix of the seeded
	// objects of a read family, the same for every run of one family.
	prefix     string
	readPrefix string
	body       payload
	attempt    string
	keys       []string
	mu         sync.Mutex
}

// fixedKey answers the key of operation i of the run, as the old path makes
// it, and remembers it for the cleanup.
func (r *protoRun) fixedKey(i int) string {
	return r.remember(r.prefix + "/w/" + strconv.Itoa(i))
}

// attemptKey answers the key of operation i of the run with the attempt
// part, as the new path makes it, and remembers it for the cleanup.
func (r *protoRun) attemptKey(i int) string {
	return r.remember(r.prefix + "/w/" + strconv.Itoa(i) + "-" + r.attempt)
}

// remember adds k to the keys that the cleanup deletes, and answers it.
func (r *protoRun) remember(k string) string {
	r.mu.Lock()
	r.keys = append(r.keys, k)
	r.mu.Unlock()

	return k
}

// putConditional sends one PUT with If-None-Match: * under key.
func (r *protoRun) putConditional(ctx context.Context, key string) error {
	return r.target.putOnce(ctx, modeRawConditional, key, r.body)
}

// putPlain sends one PUT without the header under key.
func (r *protoRun) putPlain(ctx context.Context, key string) error {
	return r.target.putOnce(ctx, modeRawPlain, key, r.body)
}

// get reads the whole object under key. A missing object answers
// blob.ErrNotFound.
func (r *protoRun) get(ctx context.Context, key string) error {
	parsed, err := blob.ParseKey(key)
	if err != nil {
		return fmt.Errorf("parse key: %w", err)
	}

	rc, err := r.target.adapter.Get(ctx, parsed)
	if err != nil {
		return fmt.Errorf("get: %w", err)
	}

	h := sha256.New()

	n, readErr := io.Copy(h, rc)
	if err := errors.Join(readErr, rc.Close()); err != nil {
		return fmt.Errorf("read body: %w", err)
	}

	if int(n) != len(r.body.body) || hex.EncodeToString(h.Sum(nil)) != r.body.hash {
		return fmt.Errorf("%w: %d of %d bytes", errGetBody, n, len(r.body.body))
	}

	return nil
}

// writeThenCommit is the hot path of a family whose fenced row names the
// blob: a PUT, then the fenced transaction. The old path sends the
// conditional header under a fixed key; the new path sends a plain PUT
// under a key for each attempt. The row decides in the same transaction,
// so the new path adds no round trip.
func writeThenCommit(name string, size int) family {
	return family{
		Name: name, Bytes: size, Read: false,
		OpBytes:   0,
		Candidate: false,
		OldPath:   seqConditionalTx,
		NewPath:   "PUT under an attempt key + fenced tx",
		pause:     0,
		seed:      nil,
		old: func(ctx context.Context, r *protoRun, i int) error {
			key := r.fixedKey(i)
			if err := r.putConditional(ctx, key); err != nil {
				return err
			}

			return r.db.fencedWrite(ctx, r.prefix, int64(i), key)
		},
		new: func(ctx context.Context, r *protoRun, i int) error {
			key := r.attemptKey(i)
			if err := r.putPlain(ctx, key); err != nil {
				return err
			}

			return r.db.fencedWrite(ctx, r.prefix, int64(i), key)
		},
	}
}

// putOnly is a family with no row of its own in the write: the old path is
// a conditional PUT, the new path a plain PUT under an attempt key.
func putOnly(name string, size int) family {
	return family{
		Name: name, Bytes: size, Read: false,
		OpBytes:   0,
		Candidate: false,
		OldPath:   seqConditional,
		NewPath:   "PUT under an attempt key",
		pause:     0,
		seed:      nil,
		old: func(ctx context.Context, r *protoRun, i int) error {
			return r.putConditional(ctx, r.fixedKey(i))
		},
		new: func(ctx context.Context, r *protoRun, i int) error {
			return r.putPlain(ctx, r.attemptKey(i))
		},
	}
}

// unitImport models the unit import upload. The new path first records the
// attempt in a fenced transaction, so that a failed write deletes only the
// key of its own attempt.
func unitImport() family {
	f := writeThenCommit("unit import upload", checkpointBytes)
	f.NewPath = "fenced tx (attempt) + PUT under an attempt key + fenced tx"
	f.new = func(ctx context.Context, r *protoRun, i int) error {
		key := r.attemptKey(i)
		if err := r.db.fencedWrite(ctx, r.prefix+"/attempt", int64(i), key); err != nil {
			return err
		}

		if err := r.putPlain(ctx, key); err != nil {
			return err
		}

		return r.db.fencedWrite(ctx, r.prefix, int64(i), key)
	}

	return f
}

// restoreWrite models the write of one blob by a restore into a live store
// that lacks it. The old path relies on the conditional PUT. The new path
// reads the live key first, checks the fence again, then writes.
func restoreWrite() family {
	return family{
		Name: "restore write (key absent)", Bytes: checkpointBytes, Read: false,
		OpBytes:   0,
		Candidate: false,
		OldPath:   seqConditional,
		NewPath:   "GET live key (absent) + fence check + PUT",
		pause:     0,
		seed:      nil,
		old: func(ctx context.Context, r *protoRun, i int) error {
			return r.putConditional(ctx, r.fixedKey(i))
		},
		new: func(ctx context.Context, r *protoRun, i int) error {
			key := r.fixedKey(i)

			err := r.get(ctx, key)
			if err == nil || !errors.Is(err, blob.ErrNotFound) {
				return fmt.Errorf("restore read of an absent key: %w", errors.Join(err, errGetBody))
			}

			if err := r.db.checkFence(ctx); err != nil {
				return err
			}

			return r.putPlain(ctx, key)
		},
	}
}

// errKeyPresentStatus reports a conditional PUT on a present key that
// answered neither a refusal nor a success.
var errKeyPresentStatus = errors.New("conditional PUT on a present key answered an unexpected status")

// restorePresent models the write of one blob by a restore into a live store
// that holds it already. The old path sends the conditional PUT and takes
// the refusal (412 or 409) as "present". A store that ignores the header
// answers 200 and writes the same bytes again. The new path reads the live
// key, compares its size and hash with the manifest, and skips the write.
func restorePresent() family {
	// MinIO refuses the conditional PUT on a present key before it reads the
	// body. The body is above 256 KiB, so the Go server closes the
	// connection, and each refused write opens a new one. 10,000 writes leave
	// 10,000 sockets in TIME_WAIT on the host for 60 seconds. Three runs in a
	// row use up the 28,232 ports of the host range, and the next connections
	// fail. A pause of 65 seconds after each run lets the sockets close.
	const restorePresentPause = 65 * time.Second

	return family{
		Name: "restore write (key present)", Bytes: checkpointBytes, Read: true,
		OpBytes:   0,
		Candidate: false,
		OldPath:   seqConditional + " (refused: present)",
		NewPath:   "GET live key + compare size and hash (skip)",
		pause:     restorePresentPause,
		seed: func(ctx context.Context, r *protoRun) error {
			return parallel(protoSeedCount, func(i int) error {
				return r.putPlain(ctx, r.readPrefix+"/"+strconv.Itoa(i))
			})
		},
		old: func(ctx context.Context, r *protoRun, i int) error {
			status, err := r.target.rawPut(
				ctx,
				r.readPrefix+"/"+strconv.Itoa(i%protoSeedCount),
				r.body,
				conditionalHeader(),
			)
			if err != nil {
				return err
			}

			switch status {
			case http.StatusPreconditionFailed, http.StatusConflict, http.StatusOK:
				return nil
			default:
				return fmt.Errorf("%w: %d", errKeyPresentStatus, status)
			}
		},
		new: func(ctx context.Context, r *protoRun, i int) error {
			return r.get(ctx, r.readPrefix+"/"+strconv.Itoa(i%protoSeedCount))
		},
	}
}

// auditCheck models the read of one audit checkpoint by the check. The old
// path reads the one copy at its fixed key. The new path lists the prefix
// of the position, then reads each copy (one copy here).
func auditCheck() family {
	return family{
		Name: "audit checkpoint check (read)", Bytes: auditCheckpointBytes, Read: true,
		OpBytes:   0,
		Candidate: false,
		OldPath:   "GET the copy",
		NewPath:   "LIST the position prefix + GET each copy (1)",
		pause:     0,
		seed: func(ctx context.Context, r *protoRun) error {
			return parallel(protoSeedCount, func(i int) error {
				base := r.readPrefix + "/" + strconv.Itoa(i)

				return errors.Join(r.putPlain(ctx, base+"/old"), r.putPlain(ctx, base+"/new/"+r.attempt))
			})
		},
		old: func(ctx context.Context, r *protoRun, i int) error {
			return r.get(ctx, r.readPrefix+"/"+strconv.Itoa(i%protoSeedCount)+"/old")
		},
		new: func(ctx context.Context, r *protoRun, i int) error {
			listed, err := r.target.adapter.List(ctx, r.readPrefix+"/"+strconv.Itoa(i%protoSeedCount)+"/new")
			if err != nil {
				return fmt.Errorf("list: %w", err)
			}

			for _, obj := range listed {
				if err := r.get(ctx, obj.Key.String()); err != nil {
					return err
				}
			}

			return nil
		},
	}
}

// protoSeedCount is the count of positions that the audit check reads. The
// operations cycle over them.
const protoSeedCount = 1000

// protocolFamilies answers the families of the protocol run.
func protocolFamilies() []family {
	return []family{
		writeThenCommit("Pulumi checkpoint", checkpointBytes),
		writeThenCommit("Pulumi journal batch", journalBatchBytes),
		writeThenCommit("job log chunk", logChunkBytes),
		unitImport(),
		putOnly("backup target copy", checkpointBytes),
		restoreWrite(),
		restorePresent(),
		putOnly("audit checkpoint write", auditCheckpointBytes),
		auditCheck(),
		unitImportUpload(),
		restoreCopyAbsent(),
		restoreCopyPresent(),
		auditWrite(),
		auditVerify(),
		auditWriteFlat(),
		auditWriteOneList(),
		auditVerifyFlat(),
	}
}

// protoResult is one run of one family in one mode.
type protoResult struct {
	Profile     string  `json:"profile"`
	Store       string  `json:"store"`
	Family      string  `json:"family"`
	Mode        string  `json:"mode"`
	Run         int     `json:"run"`
	Ops         int     `json:"ops"`
	Runs        int     `json:"runs"`
	Concurrency int     `json:"concurrency"`
	Errors      int     `json:"errors"`
	FirstError  string  `json:"firstError,omitempty"`
	P50Ms       float64 `json:"p50Ms"`
	P99Ms       float64 `json:"p99Ms"`
	WallMs      float64 `json:"wallMs"`
	OpsPerSec   float64 `json:"opsPerSec"`
	BytesPerSec float64 `json:"bytesPerSec"`
	Time        string  `json:"time"`
}

// id answers the identity of the result, so a resumed run skips it. The
// plan is part of it, so a result of one plan never stands for a result of
// another plan.
func (p protoResult) id() string {
	return p.Profile + "|" + p.Store + "|" + p.Family + "|" + p.Mode + "|" + strconv.Itoa(p.Run) +
		"|ops=" + strconv.Itoa(p.Ops) + "|runs=" + strconv.Itoa(p.Runs) + "|conc=" + strconv.Itoa(p.Concurrency)
}

// series runs ops operations of op with conc workers and answers the
// latency of each operation, the errors and the wall time.
func series(ctx context.Context, ops, conc int, op func(ctx context.Context, i int) error) (tally, time.Duration) {
	var (
		next    atomic.Int64
		mu      sync.Mutex
		wg      sync.WaitGroup
		results = tally{samples: make([]float64, 0, ops), errors: 0, first: ""}
	)

	start := time.Now()

	for range conc {
		wg.Go(func() {
			for {
				i := int(next.Add(1)) - 1
				if i >= ops || ctx.Err() != nil {
					return
				}

				opStart := time.Now()
				err := op(ctx, i)
				took := time.Since(opStart)

				mu.Lock()
				results.add(took, err)
				mu.Unlock()
			}
		})
	}

	wg.Wait()

	return results, time.Since(start)
}

// protocolPlan holds the dimensions of the protocol run.
type protocolPlan struct {
	Ops         int `json:"ops"`
	Runs        int `json:"runs"`
	Concurrency int `json:"concurrency"`
}

// protocolRunner runs the families of one profile on one store.
type protocolRunner struct {
	plan    protocolPlan
	profile string
	target  *s3Target
	db      *pgModel
	runID   string
	logger  *slog.Logger
}

// runFamily runs every run of f in both modes, old first, and calls save
// after each result. done names the results that a resumed run skips.
func (p protocolRunner) runFamily(
	ctx context.Context, f family, done map[string]bool, save func(protoResult) error,
) error {
	seed, err := p.prepare(ctx, f)
	if err != nil {
		return err
	}

	for run := 1; run <= p.plan.Runs; run++ {
		for _, mode := range []string{modeOld, modeNew} {
			res, runErr := p.oneRun(ctx, runSpec{family: f, seed: seed, mode: mode, run: run}, done)
			if runErr != nil {
				return runErr
			}

			if err := keep(ctx, res, f.pause, save); err != nil {
				return err
			}
		}
	}

	if f.seed != nil {
		return p.cleanSeed(ctx, seed.readPrefix)
	}

	return nil
}

// prepare answers the body, the attempt part and the read prefix that the
// runs of f share, and writes the objects of a read family.
func (p protocolRunner) prepare(ctx context.Context, f family) (*protoRun, error) {
	readPrefix := "bench/" + p.runID + "/protocol/" + p.profile + "/read-" + slugOf(f.Name)

	attempt, err := newKey("a", 0)
	if err != nil {
		return nil, err
	}

	body, err := newPayload(f.Bytes)
	if err != nil {
		return nil, err
	}

	seed := &protoRun{
		target: p.target, db: p.db, prefix: readPrefix, readPrefix: readPrefix,
		body: body, attempt: attemptPart(attempt), keys: nil, mu: sync.Mutex{},
	}

	if f.seed != nil {
		if err := f.seed(ctx, seed); err != nil {
			return nil, fmt.Errorf("seed %s: %w", f.Name, err)
		}
	}

	return seed, nil
}

// runSpec names one run: the family, what its runs share, the mode and the
// run number.
type runSpec struct {
	family family
	seed   *protoRun
	mode   string
	run    int
}

// oneRun runs one mode of one run, unless done holds it: then it answers
// nil. It deletes the keys and the rows of the run after the timing.
func (p protocolRunner) oneRun(ctx context.Context, spec runSpec, done map[string]bool) (*protoResult, error) {
	f, seed, mode, run := spec.family, spec.seed, spec.mode, spec.run
	res := protoResult{
		Profile: p.profile, Store: p.target.name, Family: f.Name, Mode: mode, Run: run,
		Ops: p.plan.Ops, Runs: p.plan.Runs, Concurrency: p.plan.Concurrency, Errors: 0, FirstError: "",
		P50Ms: 0, P99Ms: 0, WallMs: 0, OpsPerSec: 0, BytesPerSec: 0, Time: time.Now().UTC().Format(time.RFC3339),
	}

	if done[res.id()] {
		var skipped *protoResult

		return skipped, nil
	}

	r := &protoRun{
		target: p.target,
		db:     p.db,
		mu:     sync.Mutex{},
		keys:   make([]string, 0, p.plan.Ops),
		prefix: "bench/" + p.runID + "/protocol/" + p.profile + "/" + slugOf(
			f.Name,
		) + "/" + mode + "/" + strconv.Itoa(
			run,
		),
		readPrefix: seed.readPrefix,
		body:       seed.body,
		attempt:    seed.attempt,
	}

	op := f.old
	if mode == modeNew {
		op = f.new
	}

	p.logger.InfoContext(ctx, "protocol run", slog.String("profile", p.profile), slog.String("store", p.target.name),
		slog.String("family", f.Name), slog.String("mode", mode), slog.Int("run", run))

	t, wall := series(
		ctx,
		p.plan.Ops,
		p.plan.Concurrency,
		func(ctx context.Context, i int) error { return op(ctx, r, i) },
	)

	s := t.stats()
	res.Errors, res.FirstError, res.P50Ms, res.P99Ms = s.Errors, s.FirstError, s.P50Ms, s.P99Ms
	res.WallMs = float64(wall) / float64(time.Millisecond)
	res.OpsPerSec = float64(s.Samples) / wall.Seconds()
	opBytes := f.Bytes
	if f.OpBytes > 0 {
		opBytes = f.OpBytes
	}

	res.BytesPerSec = res.OpsPerSec * float64(opBytes)

	// A stopped run keeps no result, and its keys stay for the end of the
	// whole run. One line names the stop, not one line for each key.
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("run %d of %s: %w", run, f.Name, err)
	}

	if err := errors.Join(p.target.deleteAll(ctx, r.keys), p.db.truncate(ctx)); err != nil {
		return nil, fmt.Errorf("clean run %d of %s: %w", run, f.Name, err)
	}

	return &res, nil
}

// cleanSeed deletes the seeded objects of a read family.
func (p protocolRunner) cleanSeed(ctx context.Context, prefix string) error {
	listed, err := p.target.adapter.List(ctx, prefix)
	if err != nil {
		return fmt.Errorf("list the seed: %w", err)
	}

	keys := make([]string, 0, len(listed))
	for _, obj := range listed {
		keys = append(keys, obj.Key.String())
	}

	return p.target.deleteAll(ctx, keys)
}

// slugOf answers name in the key alphabet: lower case letters and digits,
// with a dash for every other run of characters.
func slugOf(name string) string {
	out := make([]byte, 0, len(name))
	dash := false

	for _, c := range []byte(name) {
		switch {
		case c >= 'a' && c <= 'z', c >= '0' && c <= '9':
			out = append(out, c)
			dash = false
		case c >= 'A' && c <= 'Z':
			out = append(out, c+('a'-'A'))
			dash = false
		case !dash && len(out) > 0:
			out = append(out, '-')
			dash = true
		default:
			// A second separator in a row, or one at the start: nothing.
		}
	}

	if len(out) > 0 && out[len(out)-1] == '-' {
		out = out[:len(out)-1]
	}

	return string(out)
}

// attemptPart answers the random part of a key from newKey, for use as an
// attempt identifier.
func attemptPart(key string) string {
	for i := len(key) - 1; i >= 0; i-- {
		if key[i] == '-' {
			return key[i+1:]
		}
	}

	return key
}

// keep saves res, unless it is nil because the run was done before, and
// then waits pause.
func keep(ctx context.Context, res *protoResult, pause time.Duration, save func(protoResult) error) error {
	if res == nil {
		return nil
	}

	if err := save(*res); err != nil {
		return err
	}

	return waitFor(ctx, pause)
}

// waitFor waits d, or until ctx ends.
func waitFor(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}

	timer := time.NewTimer(d)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return fmt.Errorf("wait between runs: %w", ctx.Err())
	case <-timer.C:
		return nil
	}
}
