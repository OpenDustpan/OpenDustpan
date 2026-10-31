package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"opendustpan.org/opendustpan/pkg/blob"
	"opendustpan.org/opendustpan/pkg/blob/s3"
)

// The families in this file follow the code of the family that exists, on
// main or in an open change. Each family names the file it follows; the
// benchmark record names the commit. The parts of the code that are the same
// on both paths and that make no request stay out of the model.

// Sizes of the small objects of the real-code families. They are
// assumptions: the load harness records none of them.
const (
	// unitFileBytes is the unit file of an upload import: the tool and the
	// name of the unit.
	unitFileBytes = 256
	// factsFileBytes is the facts file of one attempt: the sequence, the
	// body key, the size, the hash and the metadata.
	factsFileBytes = 512
	// versionsPerUnit is the count of versions of one unit in the unit
	// import model: each unit writes its unit file once, then refuses or
	// replaces it for each later version.
	versionsPerUnit = 100
	// scopeCheckpoints is the count of checkpoints in the directory of one
	// audit scope in the audit models.
	scopeCheckpoints = 100
	// planned is the count of copies that one check reads: the newest, one
	// newer than the last match, and the sample of older ones.
	planned = 10
)

// positionWidth pads a sequence or an operation number, so the keys of one
// directory sort in their order.
const positionWidth = 6

// errPageSize reports a LIST that does not hold every seeded copy.
var errPageSize = errors.New("the LIST holds another count of copies")

// padded answers i with leading zeros to positionWidth digits.
func padded(i int) string {
	s := strconv.Itoa(i)
	for len(s) < positionWidth {
		s = "0" + s
	}

	return s
}

// smallPayload answers n bytes of the letter c with their hash.
func smallPayload(n int, c byte) payload {
	body := bytes.Repeat([]byte{c}, n)

	return payload{body: body, hash: s3.PayloadHash(body)}
}

// putSmall writes p under key, with the conditional header when cond is
// set. A store refusal of the conditional write counts as done: the caller
// of the real code takes blob.ErrExists as "present".
func (r *protoRun) putSmall(ctx context.Context, key string, p payload, hdr http.Header) error {
	status, err := r.target.rawPut(ctx, key, p, hdr)
	if err != nil {
		return err
	}

	switch status {
	case http.StatusOK, http.StatusPreconditionFailed, http.StatusConflict:
		return nil
	default:
		return fmt.Errorf("%w: status %d", errPutStatus, status)
	}
}

// del deletes key; a missing key is no error.
func (r *protoRun) del(ctx context.Context, key string) error {
	parsed, err := blob.ParseKey(key)
	if err != nil {
		return fmt.Errorf("parse key: %w", err)
	}

	if err := r.target.adapter.Delete(ctx, parsed); err != nil {
		return fmt.Errorf("delete: %w", err)
	}

	return nil
}

// list lists the objects under prefix and answers their count.
func (r *protoRun) list(ctx context.Context, prefix string) (int, error) {
	listed, err := r.target.adapter.List(ctx, prefix)
	if err != nil {
		return 0, fmt.Errorf("list: %w", err)
	}

	return len(listed), nil
}

// headerOf answers the header of a path: the conditional header on the old
// path, none on the new path.
func headerOf(mode string) http.Header {
	if mode == modeOld {
		return conditionalHeader()
	}

	return http.Header{}
}

// unitImportUpload follows Source.Put in internal/unitimport/upload/upload.go
// and CommitUploadVersion in pkg/store/postgres/unitimport_upload.go: the PUT
// of the body, the PUT of the unit file (a refusal counts as present), the
// PUT of the facts file, and one fenced, audited commit transaction. The old
// path is the same with the conditional header and one fixed key for each
// version.
func unitImportUpload() family {
	unitFile, facts := smallPayload(unitFileBytes, 'u'), smallPayload(factsFileBytes, 'f')

	op := func(ctx context.Context, r *protoRun, i int, mode string) error {
		body, factsKey := r.attemptKey(i), r.remember(r.prefix+"/f/"+padded(i)+"-"+r.attempt)
		if mode == modeOld {
			body, factsKey = r.fixedKey(i), r.remember(r.prefix+"/f/"+padded(i))
		}

		if err := r.target.putOnce(ctx, modeOf(mode), body, r.body); err != nil {
			return err
		}

		// The code writes the unit file for the first version of a unit
		// only. The old path, before that change, wrote it for each version
		// and the store refused all but the first write.
		unitKey := r.prefix + "/u/" + padded(i/versionsPerUnit)
		if i%versionsPerUnit == 0 {
			r.remember(unitKey)
		}

		if mode == modeOld || i%versionsPerUnit == 0 {
			if err := r.putSmall(ctx, unitKey, unitFile, headerOf(mode)); err != nil {
				return err
			}
		}

		if err := r.putSmall(ctx, factsKey, facts, headerOf(mode)); err != nil {
			return err
		}

		return r.db.commitAudited(ctx, r.prefix, int64(i), factsKey)
	}

	return family{
		Name: "unit import upload (upload code)", Bytes: checkpointBytes, Read: false,
		OpBytes: checkpointBytes + unitFileBytes + factsFileBytes, Candidate: false,
		OldPath: "PUT body + PUT unit file + PUT facts, all with If-None-Match and fixed keys + audited fenced tx",
		NewPath: "PUT body + PUT unit file (first version of a unit only) + PUT facts, attempt keys + audited fenced tx",
		pause:   0,
		seed:    nil,
		old:     func(ctx context.Context, r *protoRun, i int) error { return op(ctx, r, i, modeOld) },
		new:     func(ctx context.Context, r *protoRun, i int) error { return op(ctx, r, i, modeNew) },
	}
}

// modeOf answers the put mode of a path.
func modeOf(mode string) string {
	if mode == modeOld {
		return modeRawConditional
	}

	return modeRawPlain
}

// restoreCopyAbsent follows RestoreService.copyBlob in
// pkg/backup/restore_steps.go for a live key that does not exist: the GET of
// the live key, the delete of the temporary key, the GET of the backup copy
// and its PUT to the temporary key, the GET of the temporary key and its PUT
// to the live key, and the delete of the temporary key. The new path checks
// the fence before the PUT to the live key and sends no conditional header.
// The real code streams each GET into the next PUT; the model reads the
// whole object, then writes it.
func restoreCopyAbsent() family {
	op := func(ctx context.Context, r *protoRun, i int, mode string) error {
		live, tmp := r.fixedKey(i), r.remember(r.prefix+"/tmp/"+padded(i))
		backup := r.readPrefix + "/" + strconv.Itoa(i%protoSeedCount)

		steps := []func() error{
			func() error { return expectAbsent(r.get(ctx, live)) },
			func() error { return r.del(ctx, tmp) },
			func() error { return r.get(ctx, backup) },
			func() error { return r.target.putOnce(ctx, modeOf(mode), tmp, r.body) },
			func() error { return r.get(ctx, tmp) },
		}

		if mode == modeNew {
			steps = append(steps, func() error { return r.db.checkFence(ctx) })
		}

		steps = append(steps,
			func() error { return r.target.putOnce(ctx, modeOf(mode), live, r.body) },
			func() error { return r.del(ctx, tmp) },
		)

		return runSteps(steps)
	}

	return family{
		Name:      "restore copy, key absent (copyBlob)",
		Bytes:     checkpointBytes,
		Read:      false,
		OpBytes:   0,
		Candidate: false,
		OldPath:   "GET live (absent) + DELETE tmp + GET backup + PUT tmp + GET tmp + PUT live, with If-None-Match + DELETE tmp",
		NewPath:   "the same, without the header, with a fence check before the PUT of the live key",
		pause:     0,
		seed:      seedObjects(protoSeedCount),
		old:       func(ctx context.Context, r *protoRun, i int) error { return op(ctx, r, i, modeOld) },
		new:       func(ctx context.Context, r *protoRun, i int) error { return op(ctx, r, i, modeNew) },
	}
}

// restoreCopyPresent follows RestoreService.copyBlob for a live key that
// holds the bytes of the manifest: the GET of the live key and the check of
// its size and hash, and no write. The two paths send the same requests.
func restoreCopyPresent() family {
	op := func(ctx context.Context, r *protoRun, i int) error {
		return r.get(ctx, r.readPrefix+"/"+strconv.Itoa(i%protoSeedCount))
	}

	return family{
		Name: "restore copy, key present (copyBlob)", Bytes: checkpointBytes, Read: true,
		OpBytes:   0,
		Candidate: false,
		OldPath:   "GET live + compare size and hash",
		NewPath:   "GET live + compare size and hash",
		pause:     0,
		seed:      seedObjects(protoSeedCount),
		old:       op,
		new:       op,
	}
}

// expectAbsent answers nil for the answer of a GET of a missing key, and an
// error for any other answer.
func expectAbsent(err error) error {
	if err != nil && errors.Is(err, blob.ErrNotFound) {
		return nil
	}

	return fmt.Errorf("restore read of an absent key: %w", errors.Join(err, errGetBody))
}

// runSteps runs steps in order and stops at the first error.
func runSteps(steps []func() error) error {
	for _, step := range steps {
		if err := step(); err != nil {
			return err
		}
	}

	return nil
}

// seedObjects answers a seed that writes n objects of the run body under the
// read prefix.
func seedObjects(n int) func(ctx context.Context, r *protoRun) error {
	return func(ctx context.Context, r *protoRun) error {
		return parallel(n, func(i int) error {
			return r.putPlain(ctx, r.readPrefix+"/"+strconv.Itoa(i))
		})
	}
}

// seedScope writes the epoch object and the checkpoint copies of one scope in
// each layout: one key for each sequence (the old path), and one key for each
// copy beside the others, as <sequence>-<attempt> (the new path, which is
// the layout of the code).
func seedScope(ctx context.Context, r *protoRun) error {
	if err := r.putPlain(ctx, r.readPrefix+"/epoch/"+padded(1)); err != nil {
		return err
	}

	return parallel(scopeCheckpoints, func(i int) error {
		seq := padded(i + 1)

		return errors.Join(
			r.putPlain(ctx, r.readPrefix+"/old/"+seq),
			r.putPlain(ctx, r.copyKey(seq)),
		)
	})
}

// copyKey answers the key of the one copy of seq: <sequence>-<attempt> in
// one directory, as CheckpointKey makes it.
func (r *protoRun) copyKey(seq string) string {
	return r.readPrefix + "/new/" + seq + "-" + r.attempt
}

// scopeDir answers the scope directory of a path: one key for each sequence
// on the old path, the copies in one directory on the new path.
func (r *protoRun) scopeDir(mode string) string {
	if mode == modeOld {
		return r.readPrefix + "/old"
	}

	return r.readPrefix + "/new"
}

// writeVariant is one model of the audit checkpoint write: whether the new
// path lists the scope a second time before the PUT, and whether the model is
// a candidate rather than code.
type writeVariant struct {
	name       string
	newPath    string
	listsTwice bool
	candidate  bool
}

// auditWrite follows Checkpoints.Write and storeNew in
// pkg/audit/checkpoint.go: the read of the epoch (a LIST), the read of the
// check marks, the LIST of the scope directory, the read of the last entry,
// the read of the epoch again, and the PUT of the checkpoint. The new path
// lists the scope directory a second time in storeNew, and writes under a key
// with an attempt part, <sequence>-<attempt> in the directory of the scope. The model writes the checkpoint outside the seeded
// directory, so the directory keeps its size during a run.
func auditWrite() family {
	return auditWriteModel(writeVariant{
		name:       "audit checkpoint write (Write)",
		newPath:    "the same with a second LIST of the scope, and the PUT under <sequence>-<attempt>",
		listsTwice: true, candidate: false,
	})
}

// auditWriteOneList is a candidate, not code: the write of auditWrite
// without the second LIST of the scope in storeNew.
func auditWriteOneList() family {
	return auditWriteModel(writeVariant{
		name:       "audit checkpoint write, one LIST (candidate)",
		newPath:    "as the code, without the second LIST of the scope before the PUT",
		listsTwice: false, candidate: true,
	})
}

// auditWriteModel answers the write model of v.
func auditWriteModel(v writeVariant) family {
	op := func(ctx context.Context, r *protoRun, i int, mode string) error {
		key := r.fixedKey(i)
		if mode == modeNew {
			key = r.attemptKey(i)
		}

		dir := r.scopeDir(mode)
		listDir := func(d string) func() error {
			return func() error { _, err := r.list(ctx, d); return err }
		}

		steps := []func() error{
			listDir(r.readPrefix + "/epoch"),
			func() error { return r.db.readAuditChain(ctx) },
			listDir(dir),
			func() error { return r.db.readAuditChain(ctx) },
		}

		if mode == modeNew && v.listsTwice {
			steps = append(steps, listDir(dir))
		}

		steps = append(steps,
			listDir(r.readPrefix+"/epoch"),
			func() error { return r.target.putOnce(ctx, modeOf(mode), key, r.body) },
		)

		return runSteps(steps)
	}

	return family{
		Name: v.name, Bytes: auditCheckpointBytes, Read: false,
		OpBytes: 0, Candidate: v.candidate,
		OldPath: "LIST epoch + read marks + LIST scope + read last entry + LIST epoch + PUT If-None-Match",
		NewPath: v.newPath,
		pause:   0,
		seed:    seedScope,
		old:     func(ctx context.Context, r *protoRun, i int) error { return op(ctx, r, i, modeOld) },
		new:     func(ctx context.Context, r *protoRun, i int) error { return op(ctx, r, i, modeNew) },
	}
}

// auditVerify follows Checkpoints.Verify in pkg/audit/checkpoint.go: one LIST
// of the scope directory for each check, the GET of each planned copy (the
// newest, one newer than the last match, and the sample of older ones), and
// the read of the last entry. The code before the copies did the same with
// one key for each sequence, so the two paths send the same count of
// requests.
func auditVerify() family {
	return auditVerifyModel("audit checkpoint check (Verify)",
		"LIST scope + GET the copies of 10 planned checkpoints (1 each) + read last entry")
}

// auditVerifyModel answers the check model.
func auditVerifyModel(name, newPath string) family {
	op := func(ctx context.Context, r *protoRun, i int, mode string) error {
		n, err := r.list(ctx, r.scopeDir(mode))
		if err != nil {
			return err
		}

		if n != scopeCheckpoints {
			return fmt.Errorf("%w: %d copies, want %d", errPageSize, n, scopeCheckpoints)
		}

		for j := range planned {
			seq := padded((i+j*7)%scopeCheckpoints + 1)

			key := r.readPrefix + "/old/" + seq
			if mode == modeNew {
				key = r.copyKey(seq)
			}

			if err := r.get(ctx, key); err != nil {
				return err
			}
		}

		return r.db.readAuditChain(ctx)
	}

	return family{
		Name: name, Bytes: auditCheckpointBytes, Read: true,
		OpBytes: planned * auditCheckpointBytes, Candidate: false,
		OldPath: "LIST scope + GET 10 planned checkpoints + read last entry",
		NewPath: newPath,
		pause:   0,
		seed:    seedScope,
		old:     func(ctx context.Context, r *protoRun, i int) error { return op(ctx, r, i, modeOld) },
		new:     func(ctx context.Context, r *protoRun, i int) error { return op(ctx, r, i, modeNew) },
	}
}
