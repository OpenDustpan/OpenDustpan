package stack

// ReadMasked is the masked read of a pinned version of a stack: the caller
// holds unit:read on the unit, and the answer carries the structure of the
// deployment with each secret replaced by a marker, keyed by the JSON
// Pointer value path. The read answers in pages of 200 markers behind an
// opaque cursor, loads at most one 5 MB version, and answers no plain
// secret value and no bytes in an error.

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"time"

	"opendustpan.org/opendustpan/pkg/identity"
)

// maskedPageMarkers is the most markers of one page of a masked read.
const maskedPageMarkers = 200

// maskedMaxVersionBytes is the size above which a version is refused before
// its content is held. The size comes from the version record, not from the
// content.
const maskedMaxVersionBytes = 5 << 20

// ReadMaskedArgs are the arguments of one masked read. Key is the HMAC key
// of the cursors, bound by the server; Sink counts the refusals of the
// read, and nil disables it. Cursor is the cursor of a previous page, or
// empty for the first page.
type ReadMaskedArgs struct {
	Ref        Ref
	Object     identity.Object
	Selector   StateSelector
	Principal  identity.Principal
	Authorizer identity.Authorizer
	Key        []byte
	Sink       RefusalSink
	Cursor     string
}

// MaskedMarker is the marker of one secret value of a version. Path is the
// JSON Pointer value path of the value, in the canonical RFC 6901 form this
// package writes. Secret is always true.
type MaskedMarker struct {
	Secret bool   `json:"secret"`
	Path   string `json:"path"`
}

// MaskedPage is one page of a masked read. Version is the resolved version
// of the state, as text. Markers and ValuePaths hold the markers of this
// page, ValuePaths as the matching paths, in the same order. NextCursor is
// the cursor of the next page, or empty on the last page.
type MaskedPage struct {
	Version    string         `json:"version"`
	Markers    []MaskedMarker `json:"markers"`
	ValuePaths []string       `json:"valuePaths"`
	NextCursor string         `json:"nextCursor"`
}

// ReadMasked answers one page of the masked read of the version args names.
// The authorization is the object check of point of use; the boundary check
// of the version, the value path and the cursor runs before any content is
// held; the version record's size refuses an oversized version before the
// content is loaded. Refusals carry no bytes and report one reason to the
// sink of the caller.
func (s *Updates) ReadMasked(ctx context.Context, args ReadMaskedArgs) (MaskedPage, error) {
	page, err := s.maskedPage(ctx, args)
	if err != nil {
		if r, ok := errors.AsType[*maskedRefusalError](err); ok {
			if args.Sink != nil {
				args.Sink(args.Principal, args.Object, r.reason)
			}

			return MaskedPage{}, r.err
		}

		return MaskedPage{}, err
	}

	return page, nil
}

// maskedRefusalError is a refusal of a masked read: the reason that reaches
// the sink of the caller and the error the read answers.
type maskedRefusalError struct {
	reason string
	err    error
}

// Error implements error.
func (r *maskedRefusalError) Error() string { return r.err.Error() }

// Unwrap returns the error the read answers.
func (r *maskedRefusalError) Unwrap() error { return r.err }

// maskedRefused returns the refusal of a masked read with reason and err.
func maskedRefused(reason string, err error) error {
	return &maskedRefusalError{reason: reason, err: err}
}

// maskedPage is ReadMasked without the report to the sink: each refusal
// comes back as a maskedRefusalError.
func (s *Updates) maskedPage(ctx context.Context, args ReadMaskedArgs) (MaskedPage, error) {
	if ctx.Err() != nil {
		return MaskedPage{}, maskedRefused(RefusalDeadline, ErrReadDeadline)
	}

	if err := args.Authorizer.Authorize(ctx, args.Principal, identity.ActionUnitRead, args.Object); err != nil {
		return MaskedPage{}, maskedRefused(RefusalDenied, fmt.Errorf("masked read of the unit: %w", err))
	}

	update, err := s.maskedUpdate(ctx, args.Ref, args.Selector)
	if err != nil {
		return MaskedPage{}, err
	}

	bind := cursorPayload{
		Unit:    args.Object.String(),
		Version: strconv.FormatInt(update.Version, 10),
		Subject: args.Principal.Subject,
		Offset:  0,
		Expiry:  0,
	}

	offset, err := verifyCursor(args.Key, args.Cursor, bind, s.now())
	if err != nil {
		return MaskedPage{}, maskedRefused(RefusalBadPath, err)
	}

	paths, err := s.maskedPaths(ctx, args.Ref, update.Version)
	if err != nil {
		return MaskedPage{}, err
	}

	return maskedPageOf(args.Key, bind, paths, offset, s.now())
}

// maskedUpdate resolves the selector of a read to the update of its
// version, without holding the content. The size of the version record
// refuses an oversized version before the content is loaded.
func (s *Updates) maskedUpdate(ctx context.Context, ref Ref, sel StateSelector) (Update, error) {
	cp, err := s.maskedCheckpoint(ctx, ref, sel)
	if err != nil {
		if errors.Is(err, ErrBadPath) {
			return Update{}, maskedRefused(RefusalBadPath, err)
		}

		return Update{}, err
	}

	update, err := s.Get(ctx, ref, cp.Update)
	if err != nil {
		if ctx.Err() != nil {
			return Update{}, maskedRefused(RefusalDeadline, ErrReadDeadline)
		}

		return Update{}, fmt.Errorf("masked read of %s: %w", ref, err)
	}

	// Defense in depth: the size is checked from the version record before
	// the content is loaded, so an oversized version never reaches the
	// blob store. The length check after the load in maskedPaths stays, so
	// a record that understates the content still stops at the same refusal.
	if update.CheckpointBytes > maskedMaxVersionBytes {
		return Update{}, maskedRefused(RefusalTooLarge, ErrTooLarge)
	}

	return update, nil
}

// maskedPaths loads the content of version and returns the value paths of
// its secrets.
func (s *Updates) maskedPaths(ctx context.Context, ref Ref, version int64) ([]string, error) {
	state, err := s.ExportVersion(ctx, ref, version)
	if err != nil {
		if ctx.Err() != nil {
			return nil, maskedRefused(RefusalDeadline, ErrReadDeadline)
		}

		return nil, fmt.Errorf("masked read of %s version %d: %w", ref, version, err)
	}

	// Defense in depth: maskedUpdate checks the size of the version record
	// before the load; this check stays so that a record that understates
	// the content cannot skip the limit.
	if int64(len(state.Deployment)) > maskedMaxVersionBytes {
		return nil, maskedRefused(RefusalTooLarge, ErrTooLarge)
	}

	paths, err := maskedValuePaths(state.Deployment)
	if err != nil {
		return nil, fmt.Errorf("masked read of %s version %d: %w", ref, version, err)
	}

	return paths, nil
}

// maskedPageOf answers the page of paths that starts at offset, with the
// cursor of the next page bound to bind, or no cursor on the last page.
func maskedPageOf(key []byte, bind cursorPayload, paths []string, offset int, now time.Time) (MaskedPage, error) {
	offset = min(offset, len(paths))
	end := min(offset+maskedPageMarkers, len(paths))
	next := ""

	if end < len(paths) {
		bind.Offset = end

		var err error

		next, err = mintCursor(key, bind, now)
		if err != nil {
			return MaskedPage{}, err
		}
	}

	markers := make([]MaskedMarker, 0, end-offset)

	for _, path := range paths[offset:end] {
		markers = append(markers, MaskedMarker{Secret: true, Path: path})
	}

	return MaskedPage{
		Version:    bind.Version,
		Markers:    markers,
		ValuePaths: paths[offset:end],
		NextCursor: next,
	}, nil
}

// maskedCheckpoint resolves the selector of a read to the checkpoint of the
// version, without holding its content. A pinned version passes the
// allowlist first; the errors of the checkpoints propagate unchanged, so
// ErrNoState of a version with no checkpoint reaches the caller.
func (s *Updates) maskedCheckpoint(ctx context.Context, ref Ref, sel StateSelector) (Checkpoint, error) {
	if sel.Latest {
		return s.checkpoints.LatestCheckpoint(ctx, ref)
	}

	if err := validateVersion(sel.Version); err != nil {
		return Checkpoint{}, err
	}

	return s.checkpoints.CheckpointByVersion(ctx, ref, sel.Version)
}

// maskedValuePaths decodes deployment and returns the sorted, unique JSON
// Pointer value paths of its secrets. The walk mirrors maskValue of the
// masked read: a secret is not descended into. The deployment must decode;
// an error carries no bytes of it.
func maskedValuePaths(deployment []byte) ([]string, error) {
	doc, err := decodeDeployment(deployment)
	if err != nil {
		return nil, err
	}

	var paths []string

	collectMarkers(doc, "", &paths)

	slices.Sort(paths)

	return slices.Compact(paths), nil
}

// collectMarkers appends the base path of each secret value of v to masked.
func collectMarkers(v any, base string, masked *[]string) {
	if isSecret(v) {
		*masked = append(*masked, base)

		return
	}

	switch typed := v.(type) {
	case map[string]any:
		for key, elem := range typed {
			collectMarkers(elem, base+"/"+EscapePointerToken(key), masked)
		}

	case []any:
		for i, elem := range typed {
			collectMarkers(elem, base+"/"+strconv.Itoa(i), masked)
		}

	default:
	}
}
