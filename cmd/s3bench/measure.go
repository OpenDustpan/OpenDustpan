package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"sync"
	"time"

	"opendustpan.org/opendustpan/pkg/blob"
	"opendustpan.org/opendustpan/pkg/blob/s3"
)

// Put modes of the blob measurement.
const (
	// modeAdapter is the blob/s3 adapter Put: it stages the body in a
	// temporary file, hashes it, and sends If-None-Match: *. This is the
	// code that runs today.
	modeAdapter = "adapter-conditional"
	// modeRawConditional is one signed PUT with If-None-Match: * from memory.
	modeRawConditional = "raw-conditional"
	// modeRawPlain is one signed PUT without the header under a unique key,
	// as the database-arbitrated design sends it.
	modeRawPlain = "raw-plain-unique-key"
)

// Names of the conditional sequences, in the tables of both runs.
const (
	seqConditional   = "PUT If-None-Match"
	seqConditionalTx = seqConditional + " + fenced tx"
)

// putModes lists the modes in the order the run measures them.
func putModes() []string {
	return []string{modeAdapter, modeRawConditional, modeRawPlain}
}

// warmupPuts are the untimed writes before each timed series, so the
// series starts on open connections.
const warmupPuts = 2

// keyRandBytes is the random part of each bench key.
const keyRandBytes = 6

// errPutStatus reports a raw PUT that did not answer 200.
var errPutStatus = errors.New("PUT did not answer 200")

// newKey answers a unique key under prefix.
func newKey(prefix string, n int) (string, error) {
	raw := make([]byte, keyRandBytes)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("random key: %w", err)
	}

	return prefix + "/" + strconv.Itoa(n) + "-" + hex.EncodeToString(raw), nil
}

// payload is one body with its hash.
type payload struct {
	body []byte
	hash string
}

// newPayload answers size random bytes with their hash.
func newPayload(size int) (payload, error) {
	body := make([]byte, size)
	if _, err := rand.Read(body); err != nil {
		return payload{}, fmt.Errorf("random body: %w", err)
	}

	return payload{body: body, hash: s3.PayloadHash(body)}, nil
}

// putOnce writes p under key in mode.
func (t *s3Target) putOnce(ctx context.Context, mode, key string, p payload) error {
	if mode == modeAdapter {
		parsed, err := blob.ParseKey(key)
		if err != nil {
			return fmt.Errorf("parse key: %w", err)
		}

		return t.adapter.Put(ctx, parsed, bytes.NewReader(p.body))
	}

	hdr := http.Header{}
	if mode == modeRawConditional {
		hdr = conditionalHeader()
	}

	status, err := t.rawPut(ctx, key, p, hdr)
	if err != nil {
		return err
	}

	if status != http.StatusOK {
		return fmt.Errorf("%w: status %d", errPutStatus, status)
	}

	return nil
}

// putResult is the timing of one size and one mode.
type putResult struct {
	SizeBytes int    `json:"sizeBytes"`
	Mode      string `json:"mode"`
	Stats     stats  `json:"stats"`
}

// measurePuts times reps writes of each size in each mode, each under a new
// key. Within one size the modes take turns, one write each, so a drift of
// the host or the store hits every mode alike. The keys of each size are
// deleted after its series.
func (t *s3Target) measurePuts(ctx context.Context, prefix string, sizes []sizeReps) ([]putResult, error) {
	var out []putResult

	for _, sr := range sizes {
		p, err := newPayload(sr.Bytes)
		if err != nil {
			return nil, err
		}

		res, err := t.putRound(ctx, prefix+"/put-"+strconv.Itoa(sr.Bytes), p, sr.Reps)
		if err != nil {
			return nil, err
		}

		for _, mode := range putModes() {
			out = append(out, putResult{SizeBytes: sr.Bytes, Mode: mode, Stats: res[mode].stats()})
		}
	}

	return out, nil
}

// putRound writes p warmupPuts times untimed, then reps times timed, in
// each mode in turn, each under a new key of prefix, and deletes the keys.
func (t *s3Target) putRound(ctx context.Context, prefix string, p payload, reps int) (map[string]*tally, error) {
	modes := putModes()
	keys := make([]string, 0, (warmupPuts+reps)*len(modes))
	series := map[string]*tally{}

	for _, mode := range modes {
		series[mode] = &tally{samples: nil, errors: 0, first: ""}
	}

	for i := range warmupPuts + reps {
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("put series: %w", err)
		}

		for _, mode := range modes {
			key, err := newKey(prefix+"-"+mode, i)
			if err != nil {
				return nil, err
			}

			keys = append(keys, key)

			if i >= warmupPuts {
				series[mode].time(func() error { return t.putOnce(ctx, mode, key, p) })

				continue
			}

			if err := t.putOnce(ctx, mode, key, p); err != nil {
				return nil, fmt.Errorf("%s warm-up put: %w", t.name, err)
			}
		}
	}

	return series, t.deleteAll(ctx, keys)
}

// maxParallel is the count of parallel requests of the untimed seeding and
// cleanup steps.
const maxParallel = 32

// deleteAll deletes keys with maxParallel requests at once. It is not timed.
func (t *s3Target) deleteAll(ctx context.Context, keys []string) error {
	return parallel(len(keys), func(i int) error {
		parsed, err := blob.ParseKey(keys[i])
		if err != nil {
			return fmt.Errorf("parse key: %w", err)
		}

		if err := t.adapter.Delete(ctx, parsed); err != nil {
			return fmt.Errorf("delete %s on %s: %w", keys[i], t.name, err)
		}

		return nil
	})
}

// parallel runs fn for 0..n-1 with maxParallel calls at once and answers
// the errors joined.
func parallel(n int, fn func(i int) error) error {
	var (
		wg   sync.WaitGroup
		mu   sync.Mutex
		errs []error
	)

	slots := make(chan struct{}, maxParallel)

	for i := range n {
		slots <- struct{}{}

		wg.Go(func() {
			defer func() { <-slots }()

			if err := fn(i); err != nil {
				mu.Lock()
				errs = append(errs, err)
				mu.Unlock()
			}
		})
	}

	wg.Wait()

	return errors.Join(errs...)
}

// sizeReps is one body size with its timed write count.
type sizeReps struct {
	Bytes int `json:"bytes"`
	Reps  int `json:"reps"`
}

// sequenceResult is one modeled write sequence in both designs.
type sequenceResult struct {
	Flow string `json:"flow"`
	// Conditional is the sequence that relies on If-None-Match, as the code
	// does today. Arbitrated is the sequence of the database-arbitrated
	// design: a plain PUT under a unique key and the extra database or
	// list step that the design adds. Both are models: they send the real
	// requests of each design, not the code of the server.
	Conditional    stats  `json:"conditional"`
	Arbitrated     stats  `json:"arbitrated"`
	ConditionalSeq string `json:"conditionalSeq"`
	ArbitratedSeq  string `json:"arbitratedSeq"`
}

// sequence is one modeled flow: its steps in both designs.
type sequence struct {
	flow           string
	conditionalSeq string
	arbitratedSeq  string
	conditional    func(ctx context.Context, n int) error
	arbitrated     func(ctx context.Context, n int) error
}

// modelRun holds what the modeled sequences share.
type modelRun struct {
	target *s3Target
	db     *pgModel
	prefix string
	body   payload
	keys   []string
	mu     sync.Mutex
}

// put writes the body under a new key of the run in mode and remembers the
// key for the cleanup.
func (m *modelRun) put(ctx context.Context, mode string, n int) (string, error) {
	key, err := newKey(m.prefix, n)
	if err != nil {
		return "", err
	}

	m.mu.Lock()
	m.keys = append(m.keys, key)
	m.mu.Unlock()

	return key, m.target.putOnce(ctx, mode, key, m.body)
}

// listPrefix lists the directory of the run, as the backup, the restore and
// the audit checkpoint writer list their directory before a write.
func (m *modelRun) listPrefix(ctx context.Context) error {
	if _, err := m.target.adapter.List(ctx, m.prefix+"/list"); err != nil {
		return fmt.Errorf("list: %w", err)
	}

	return nil
}

// sequences answers the three modeled flows.
func (m *modelRun) sequences() []sequence {
	return []sequence{m.hotPath(), m.unitImport(), m.listThenPut()}
}

// unit answers the unit name of the rows of flow.
func (m *modelRun) unit(flow string) string {
	return m.prefix + "/" + flow
}

// hotPath models the checkpoint, journal batch and job log chunk writes:
// the database-arbitrated design adds at most one row read.
func (m *modelRun) hotPath() sequence {
	return sequence{
		flow:           "hot path (checkpoint, journal batch, job log chunk)",
		conditionalSeq: seqConditionalTx,
		arbitratedSeq:  "PUT unique key + fenced tx + 1 row read",
		conditional: func(ctx context.Context, n int) error {
			key, err := m.put(ctx, modeRawConditional, n)
			if err != nil {
				return err
			}

			return m.db.fencedWrite(ctx, m.unit("hot-c"), int64(n), key)
		},
		arbitrated: func(ctx context.Context, n int) error {
			key, err := m.put(ctx, modeRawPlain, n)
			if err != nil {
				return err
			}

			if writeErr := m.db.fencedWrite(ctx, m.unit("hot-a"), int64(n), key); writeErr != nil {
				return writeErr
			}

			return m.db.readRow(ctx, m.unit("hot-a"), int64(n))
		},
	}
}

// unitImport models the unit import upload: the database-arbitrated design
// adds one fenced transaction that claims the upload.
func (m *modelRun) unitImport() sequence {
	return sequence{
		flow:           "unit import upload",
		conditionalSeq: seqConditionalTx,
		arbitratedSeq:  "fenced tx (claim) + PUT unique key + fenced tx",
		conditional: func(ctx context.Context, n int) error {
			key, err := m.put(ctx, modeRawConditional, n)
			if err != nil {
				return err
			}

			return m.db.fencedWrite(ctx, m.unit("imp-c"), int64(n), key)
		},
		arbitrated: func(ctx context.Context, n int) error {
			claim, err := newKey(m.prefix+"/claim", n)
			if err != nil {
				return err
			}

			if claimErr := m.db.fencedWrite(ctx, m.unit("imp-claim"), int64(n), claim); claimErr != nil {
				return claimErr
			}

			key, err := m.put(ctx, modeRawPlain, n)
			if err != nil {
				return err
			}

			return m.db.fencedWrite(ctx, m.unit("imp-a"), int64(n), key)
		},
	}
}

// listThenPut models the backup target, the restore and the audit
// checkpoint writes: the database-arbitrated design adds one LIST.
func (m *modelRun) listThenPut() sequence {
	return sequence{
		flow:           "backup target, restore, audit checkpoint",
		conditionalSeq: seqConditional,
		arbitratedSeq:  "LIST prefix + PUT unique key",
		conditional: func(ctx context.Context, n int) error {
			_, err := m.put(ctx, modeRawConditional, n)

			return err
		},
		arbitrated: func(ctx context.Context, n int) error {
			if err := m.listPrefix(ctx); err != nil {
				return err
			}

			_, err := m.put(ctx, modeRawPlain, n)

			return err
		},
	}
}

// designs is the count of designs of each round: each round uses one
// sequence number for each design.
const designs = 2

// listedObjects is the count of objects in the listed directory of the
// modeled list step: a backup directory holds a manifest and some parts.
const listedObjects = 10

// sequenceRun is one series of the modeled flows on one store.
type sequenceRun struct {
	target *s3Target
	db     *pgModel
	prefix string
	reps   int
}

// measure times reps runs of each modeled flow in both designs with a body
// of bodyBytes. The two designs alternate, so a drift in the store hits both
// alike.
func (r sequenceRun) measure(ctx context.Context, bodyBytes int) ([]sequenceResult, error) {
	body, err := newPayload(bodyBytes)
	if err != nil {
		return nil, err
	}

	m := &modelRun{target: r.target, db: r.db, prefix: r.prefix, body: body, keys: nil, mu: sync.Mutex{}}

	for i := range listedObjects {
		if _, putErr := m.putUnder(ctx, r.prefix+"/list", i); putErr != nil {
			return nil, putErr
		}
	}

	out := make([]sequenceResult, 0, len(m.sequences()))

	for _, s := range m.sequences() {
		res, seriesErr := r.series(ctx, s)
		if seriesErr != nil {
			return nil, seriesErr
		}

		out = append(out, res)
	}

	return out, r.target.deleteAll(ctx, m.keys)
}

// series times one modeled flow in both designs.
func (r sequenceRun) series(ctx context.Context, s sequence) (sequenceResult, error) {
	var cond, arb tally

	for i := range warmupPuts + r.reps {
		if err := ctx.Err(); err != nil {
			return sequenceResult{}, fmt.Errorf("%s: %w", s.flow, err)
		}

		n := i * designs
		if i < warmupPuts {
			if err := errors.Join(s.conditional(ctx, n), s.arbitrated(ctx, n+1)); err != nil {
				return sequenceResult{}, fmt.Errorf("%s warm-up: %w", s.flow, err)
			}

			continue
		}

		cond.time(func() error { return s.conditional(ctx, n) })
		arb.time(func() error { return s.arbitrated(ctx, n+1) })
	}

	return sequenceResult{
		Flow: s.flow, Conditional: cond.stats(), Arbitrated: arb.stats(),
		ConditionalSeq: s.conditionalSeq, ArbitratedSeq: s.arbitratedSeq,
	}, nil
}

// putUnder writes the body under a new key of dir and remembers the key.
func (m *modelRun) putUnder(ctx context.Context, dir string, n int) (string, error) {
	key, err := newKey(dir, n)
	if err != nil {
		return "", err
	}

	m.mu.Lock()
	m.keys = append(m.keys, key)
	m.mu.Unlock()

	return key, m.target.putOnce(ctx, modeRawPlain, key, m.body)
}

// sweepResult is the timing of one orphan sweep.
type sweepResult struct {
	Objects   int     `json:"objects"`
	Deleted   int     `json:"deleted"`
	ListMs    float64 `json:"listMs"`
	KeysMs    float64 `json:"keysMs"`
	DeletesMs float64 `json:"deletesMs"`
	TotalMs   float64 `json:"totalMs"`
	Error     string  `json:"error,omitempty"`
}

// sweepBodyBytes is the size of each orphan object.
const sweepBodyBytes = 1024

// measureSweep writes objects orphans under one unit directory, untimed,
// then times the sweep as the stack and workspace services run it: one
// LIST of the directory, one read of the referenced keys, and one DELETE for
// each listed key that no row names, one after the other.
func measureSweep(ctx context.Context, t *s3Target, db *pgModel, prefix string, objects int) (sweepResult, error) {
	body, err := newPayload(sweepBodyBytes)
	if err != nil {
		return sweepResult{}, err
	}

	dir := prefix + "/sweep"

	err = parallel(objects, func(i int) error {
		key, keyErr := newKey(dir, i)
		if keyErr != nil {
			return keyErr
		}

		return t.putOnce(ctx, modeRawPlain, key, body)
	})
	if err != nil {
		return sweepResult{}, fmt.Errorf("seed the sweep: %w", err)
	}

	return runSweep(ctx, t, db, dir, objects), nil
}

// runSweep runs the timed part of the sweep over dir.
func runSweep(ctx context.Context, t *s3Target, db *pgModel, dir string, objects int) sweepResult {
	out := sweepResult{Objects: objects, Deleted: 0, ListMs: 0, KeysMs: 0, DeletesMs: 0, TotalMs: 0, Error: ""}
	start := time.Now()

	listed, err := t.adapter.List(ctx, dir)
	out.ListMs = msSince(start)

	if err != nil {
		out.Error = err.Error()

		return out
	}

	keysStart := time.Now()
	keep, err := db.referencedKeys(ctx, dir)
	out.KeysMs = msSince(keysStart)

	if err != nil {
		out.Error = err.Error()

		return out
	}

	deletesStart := time.Now()

	for _, obj := range listed {
		if _, ok := keep[obj.Key.String()]; ok {
			continue
		}

		if err := t.adapter.Delete(ctx, obj.Key); err != nil {
			out.Error = err.Error()

			break
		}

		out.Deleted++
	}

	out.DeletesMs = msSince(deletesStart)
	out.TotalMs = msSince(start)

	return out
}

// msSince answers the milliseconds since start.
func msSince(start time.Time) float64 {
	return float64(time.Since(start)) / float64(time.Millisecond)
}
