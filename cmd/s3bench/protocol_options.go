package main

import (
	"context"
	"errors"
	"fmt"
	"strconv"
)

// errPageSize reports a LIST page that does not hold every seeded copy.
var errPageSize = errors.New("the LIST page holds another count of copies")

// The families in this file model the options that reduce the cost of the
// new path where the first protocol run found it above the measure. The
// first models stay, so the record shows both.

// listPage is the count of objects of one S3 LIST page, and the size of
// one batch of the batched models.
const listPage = 1000

// positionWidth pads a position or an operation number, so the keys of one
// page sort in their order.
const positionWidth = 6

// padded answers i with leading zeros to positionWidth digits.
func padded(i int) string {
	s := strconv.Itoa(i)
	for len(s) < positionWidth {
		s = "0" + s
	}

	return s
}

// unitImportCreateTx models the unit import upload with the attempt
// recorded in the transaction that creates the import. Both paths run the
// create transaction, the PUT and the commit transaction. The new path
// inserts one more row in the create transaction: one more round trip in a
// transaction that exists already.
func unitImportCreateTx() family {
	return family{
		Name: "unit import upload (attempt in create tx)", Bytes: checkpointBytes, Read: false,
		OldPath: "create tx + " + seqConditionalTx,
		NewPath: "create tx with the attempt row + PUT under an attempt key + fenced tx",
		pause:   0,
		seed:    nil,
		old: func(ctx context.Context, r *protoRun, i int) error {
			key := r.fixedKey(i)
			if err := r.db.fencedWrite(ctx, r.prefix+"/create", int64(i), key); err != nil {
				return err
			}

			if err := r.putConditional(ctx, key); err != nil {
				return err
			}

			return r.db.fencedWrite(ctx, r.prefix, int64(i), key)
		},
		new: func(ctx context.Context, r *protoRun, i int) error {
			key := r.attemptKey(i)
			if err := r.db.fencedWriteTwo(ctx, r.prefix+"/create", r.prefix+"/attempt", int64(i), key); err != nil {
				return err
			}

			if err := r.putPlain(ctx, key); err != nil {
				return err
			}

			return r.db.fencedWrite(ctx, r.prefix, int64(i), key)
		},
	}
}

// restoreAbsentBatched models the restore write into a live store that
// lacks the key, with one LIST of the live prefix and one fence check for
// each batch of listPage blobs. The operation that starts a batch pays for
// the LIST and the fence check; the others send only the PUT.
func restoreAbsentBatched() family {
	return family{
		Name: "restore write (key absent, batch LIST)", Bytes: checkpointBytes, Read: false,
		OldPath: seqConditional,
		NewPath: "LIST of the batch prefix + fence check, once for each 1,000 blobs + PUT",
		pause:   0,
		seed:    nil,
		old: func(ctx context.Context, r *protoRun, i int) error {
			return r.putConditional(ctx, r.fixedKey(i))
		},
		new: func(ctx context.Context, r *protoRun, i int) error {
			batch := r.prefix + "/b/" + padded(i/listPage)

			if i%listPage == 0 {
				if _, err := r.target.adapter.List(ctx, batch); err != nil {
					return fmt.Errorf("list the batch: %w", err)
				}

				if err := r.db.checkFence(ctx); err != nil {
					return err
				}
			}

			return r.putPlain(ctx, r.remember(batch+"/"+padded(i)))
		},
	}
}

// auditCheckPaged models the read of audit checkpoints by a check that
// lists the copies of listPage positions with one LIST page, then reads
// each copy. The operation that starts a page pays for the LIST.
func auditCheckPaged() family {
	return family{
		Name: "audit checkpoint check (read, paged LIST)", Bytes: auditCheckpointBytes, Read: true,
		OldPath: "GET the copy",
		NewPath: "LIST of 1,000 positions, once for each 1,000 + GET each copy (1)",
		pause:   0,
		seed: func(ctx context.Context, r *protoRun) error {
			return parallel(listPage, func(i int) error {
				pos := padded(i)

				return errors.Join(
					r.putPlain(ctx, r.readPrefix+"/old/"+pos),
					r.putPlain(ctx, r.readPrefix+"/new/"+pos+"/"+r.attempt),
				)
			})
		},
		old: func(ctx context.Context, r *protoRun, i int) error {
			return r.get(ctx, r.readPrefix+"/old/"+padded(i%listPage))
		},
		new: func(ctx context.Context, r *protoRun, i int) error {
			if i%listPage == 0 {
				listed, err := r.target.adapter.List(ctx, r.readPrefix+"/new")
				if err != nil {
					return fmt.Errorf("list the page: %w", err)
				}

				if len(listed) != listPage {
					return fmt.Errorf("%w: %d copies, want %d", errPageSize, len(listed), listPage)
				}
			}

			return r.get(ctx, r.readPrefix+"/new/"+padded(i%listPage)+"/"+r.attempt)
		},
	}
}
