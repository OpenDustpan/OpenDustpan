package backup

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"opendustpan.org/opendustpan/pkg/blob"
)

const (
	// livePointerName is the object backups/<id>/live.json. It names the
	// attempt prefix that holds the finished backup.
	livePointerName = "live.json"
	// maxLivePointerBytes bounds the pointer that a reader parses from a
	// target.
	maxLivePointerBytes = 1 << 10
)

// livePointer is the content of backups/<id>/live.json.
type livePointer struct {
	Attempt string `json:"attempt"`
}

// livePointerKey returns the key of the live pointer of backup id.
func livePointerKey(id ID) (blob.Key, error) {
	if _, err := ParseID(id.String()); err != nil {
		return "", err
	}

	key, err := blob.ParseKey("backups/" + id.String() + "/" + livePointerName)
	if err != nil {
		return "", fmt.Errorf("name the live pointer of backup %s: %w", id, err)
	}

	return key, nil
}

// livePointerBytes returns the one fixed byte form of a pointer that names
// attempt. Every writer of the same attempt writes these bytes, so two
// holders of the row attempt give the same pointer on a store that refuses
// the second put and on a store that replaces the object.
func livePointerBytes(attempt string) ([]byte, error) {
	data, err := json.Marshal(livePointer{Attempt: attempt})
	if err != nil {
		return nil, fmt.Errorf("encode a live pointer of attempt %q: %w", attempt, err)
	}

	return data, nil
}

// writeLivePointer writes the pointer of the attempt that the backups row
// records. The caller is the holder of the live claim and passed Check on
// the prefix of that attempt; store checks the claim again right before the
// put, so a holder whose claim moved writes no pointer.
//
// The pointer is a copy of the row attempt, not a write that must win: the
// writer puts it only when no pointer exists. When the pointer already names
// the row attempt, the step is done and there is no put. When the put returns
// ErrExists, the writer reads the pointer again and applies the same rules:
// ErrExists alone never decides the result. A pointer that names another
// attempt, or one that does not parse, is never replaced on any store; the
// error wraps ErrCorrupt and names both attempts.
func writeLivePointer(ctx context.Context, store blob.Store, row attemptPrefix) error {
	current, err := readLivePointerLimit(ctx, store, row.id, maxLivePointerBytes)
	if err == nil {
		return requireLivePointer(current, row)
	}

	if !errors.Is(err, blob.ErrNotFound) {
		// The pointer exists but does not parse: fail closed, never replace.
		return err
	}

	key, err := livePointerKey(row.id)
	if err != nil {
		return err
	}

	data, err := livePointerBytes(row.attempt)
	if err != nil {
		return err
	}

	// The claim check runs inside this put, right before the target call.
	if putErr := store.Put(ctx, key, bytes.NewReader(data)); putErr != nil {
		if !errors.Is(putErr, blob.ErrExists) {
			return fmt.Errorf("write the live pointer of backup %s: %w", row.id, putErr)
		}

		// Another holder wrote the pointer while this one read none. Read it
		// and apply the same rules; ErrExists alone never decides.
		again, againErr := readLivePointer(ctx, store, row.id)
		if againErr != nil {
			return againErr
		}

		return requireLivePointer(again, row)
	}

	return nil
}

// requireLivePointer returns nil when found names the row attempt, else an
// error that wraps ErrCorrupt and names both attempts. The writer never
// replaces a pointer that names another attempt, on any store.
func requireLivePointer(found, row attemptPrefix) error {
	if found.attempt == row.attempt {
		return nil
	}

	return fmt.Errorf(
		"backup %s holds a live pointer that names attempt %q, the backups row records %q: %w",
		row.id, found.attempt, row.attempt, ErrCorrupt,
	)
}

// readLivePointer reads backups/<id>/live.json with the bound
// maxLivePointerBytes and returns the attempt prefix that it names. It fails
// closed with ErrCorrupt when the pointer is missing, larger than the bound,
// not valid JSON, or names an attempt that the allowlist refuses: a reader
// never lists the parent prefix to find the attempt itself.
func readLivePointer(ctx context.Context, target blob.Store, id ID) (attemptPrefix, error) {
	p, err := readLivePointerLimit(ctx, target, id, maxLivePointerBytes)
	if errors.Is(err, blob.ErrNotFound) {
		return attemptPrefix{}, fmt.Errorf("backup %s has no live pointer: %w", id, ErrCorrupt)
	}

	return p, err
}

// readLivePointerLimit is readLivePointer with the size bound as a parameter,
// so a test can reach the bound with a small limit. Unlike readLivePointer
// it reports a missing pointer with an error that wraps blob.ErrNotFound, so
// the writer can tell a missing pointer from a corrupt one.
func readLivePointerLimit(ctx context.Context, target blob.Store, id ID, limit int64) (attemptPrefix, error) {
	key, err := livePointerKey(id)
	if err != nil {
		return attemptPrefix{}, err
	}

	reader, err := target.Get(ctx, key)
	if err != nil {
		if errors.Is(err, blob.ErrNotFound) {
			return attemptPrefix{}, fmt.Errorf("backup %s has no live pointer at %q: %w", id, key, blob.ErrNotFound)
		}

		return attemptPrefix{}, fmt.Errorf("read the live pointer of backup %s: %w", id, err)
	}

	data, readErr := io.ReadAll(io.LimitReader(reader, limit+1))
	closeErr := reader.Close()

	if readErr != nil {
		return attemptPrefix{}, fmt.Errorf("read the live pointer of backup %s: %w", id, readErr)
	}

	if closeErr != nil {
		return attemptPrefix{}, fmt.Errorf("close the live pointer of backup %s: %w", id, closeErr)
	}

	if int64(len(data)) > limit {
		return attemptPrefix{}, fmt.Errorf(
			"live pointer of backup %s is larger than %d bytes: %w", id, limit, ErrCorrupt,
		)
	}

	var pointer livePointer
	if decodeErr := json.Unmarshal(data, &pointer); decodeErr != nil {
		return attemptPrefix{}, fmt.Errorf("backup %s has a corrupt live pointer: %w", id, ErrCorrupt)
	}

	p, err := newAttemptPrefix(id, pointer.Attempt)
	if err != nil {
		return attemptPrefix{}, fmt.Errorf(
			"backup %s has a live pointer with a bad attempt: %w: %w", id, err, ErrCorrupt,
		)
	}

	return p, nil
}
