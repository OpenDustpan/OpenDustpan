package pulumi

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"opendustpan.org/opendustpan/pkg/stack"
)

// journalEntries stores one journal batch of a running update (ADR 43, decision
// 6). The lease token authorizes the write. The server answers 2xx only after
// the batch is committed (decision 6.2). The body is the batch as the CLI sent
// it, gzipped or not. A batch over the journal batch bound answers 413, and the
// CLI splits it. A batch with one entry may hold up to the checkpoint bound,
// because the CLI cannot split one entry.
func (h *Handler) journalEntries(w http.ResponseWriter, r *http.Request, call LeaseCall) {
	ctx := r.Context()

	limit := int64(h.updates.MaxJournalBatchBytes())
	readLimit := int64(h.updates.MaxCheckpointBytes())

	releaseHold, ok := h.hold(w, r, readLimit)
	if !ok {
		return
	}

	defer releaseHold()

	body, ok := h.readJournalBody(w, r, readLimit)
	if !ok {
		return
	}

	// Defense in depth: the journal writer of the update service refuses a
	// large batch with more than one entry too; this check stays so that the
	// adapter answers 413 before the service decodes the body, and so that a
	// service without that check cannot take one.
	if int64(len(body)) > limit && !h.singleEntry(w, r, body, limit) {
		return
	}

	if err := h.updates.AppendJournal(ctx, call.Ref, call.ID, call.Token, body); err != nil {
		h.writeError(ctx, w, err)

		return
	}

	h.writeJSON(ctx, w, http.StatusOK, struct{}{})
}

// singleEntry reports whether body holds one journal entry. A batch over the
// batch bound still passes when it holds one entry, so it answers 413 only
// for larger batches. limit is the batch bound for the 413 message.
func (h *Handler) singleEntry(w http.ResponseWriter, r *http.Request, body []byte, limit int64) bool {
	var batch struct {
		Entries []json.RawMessage `json:"entries"`
	}

	if err := json.Unmarshal(body, &batch); err != nil || len(batch.Entries) != 1 {
		h.writeError(r.Context(), w, stack.BatchOverLimit(len(body), int(limit)))

		return false
	}

	return true
}

// readJournalBody reads one journal batch as the decompressed JSON the CLI
// sent. It bounds the decompressed size by limit, so a gzip bomb cannot exhaust
// memory, and maps an oversized body to 413.
func (h *Handler) readJournalBody(w http.ResponseWriter, r *http.Request, limit int64) ([]byte, bool) {
	counted, release, ok := h.largeReader(w, r, limit)
	if !ok {
		return nil, false
	}

	defer release()

	body, err := io.ReadAll(counted)
	if counted.n > limit {
		h.writeTooLarge(w, r, limit)

		return nil, false
	}

	if err != nil {
		if tooLarge, ok := errors.AsType[*http.MaxBytesError](err); ok && tooLarge != nil {
			h.writeTooLarge(w, r, limit)
		} else {
			h.writeInvalidBody(r.Context(), w)
		}

		return nil, false
	}

	if !json.Valid(body) {
		h.writeInvalidBody(r.Context(), w)

		return nil, false
	}

	return body, true
}
