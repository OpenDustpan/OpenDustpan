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
	"strings"
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

	// The stable ID of the unit names the stack that the caller authorizes.
	// Each row that the read resolves by name must carry this ID, so the
	// read never serves the content of a stack that the grant does not name.
	// An object with no stable ID names no stack: the read fails closed.
	stackID := ID(args.Object.UnitID())
	if stackID == "" {
		return MaskedPage{}, maskedRefused(
			RefusalDenied, fmt.Errorf("masked read of %s: %w: the unit has no stable ID", args.Ref, ErrNotFound),
		)
	}

	if err := args.Authorizer.Authorize(ctx, args.Principal, identity.ActionUnitRead, args.Object); err != nil {
		return MaskedPage{}, maskedRefused(RefusalDenied, fmt.Errorf("masked read of the unit: %w", err))
	}

	cp, update, err := s.maskedUpdate(ctx, args.Ref, args.Selector, stackID)
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

	paths, err := s.maskedPaths(ctx, args.Ref, cp)
	if err != nil {
		return MaskedPage{}, err
	}

	return maskedPageOf(args.Key, bind, paths, offset, s.now())
}

// maskedUpdate resolves the selector of a read to the checkpoint row and the
// update of its version, without holding the content. The checkpoint row
// must belong to the stack that the caller authorized; a row of another
// stack is refused before any content is read. The size of the version
// record refuses an oversized version before the content is loaded.
//
// The masked read serves only a stored checkpoint row. It never replays the
// journal of a running update, so no journal batch is read and the size of
// the version record is the size of the content that the load reads.
func (s *Updates) maskedUpdate(
	ctx context.Context, ref Ref, sel StateSelector, stackID ID,
) (Checkpoint, Update, error) {
	cp, err := s.maskedCheckpoint(ctx, ref, sel)
	if err != nil {
		if errors.Is(err, ErrBadPath) {
			return Checkpoint{}, Update{}, maskedRefused(RefusalBadPath, err)
		}

		return Checkpoint{}, Update{}, err
	}

	// The name of the stack may now map to another stack than the one that
	// the caller authorized: the stack was deleted and made again, or
	// renamed, between the lookup of the handler and this read. Defense in
	// depth: the handler authorizes on the stable ID, and the load below
	// reads in the scope of the row, not of the name. This comparison stays
	// so that neither layer has to hold the other.
	if cp.StackID != stackID {
		return Checkpoint{}, Update{}, maskedRefused(
			RefusalDenied, fmt.Errorf("masked read of %s: %w: the name maps to another stack", ref, ErrNotFound),
		)
	}

	update, err := s.Get(ctx, ref, cp.Update)
	if err != nil {
		if ctx.Err() != nil {
			return Checkpoint{}, Update{}, maskedRefused(RefusalDeadline, ErrReadDeadline)
		}

		return Checkpoint{}, Update{}, fmt.Errorf("masked read of %s: %w", ref, err)
	}

	// Defense in depth: the size is checked from the version record before
	// the content is loaded, so an oversized version never reaches the
	// blob store. The length check after the load in maskedPaths stays, so
	// a record that understates the content still stops at the same refusal.
	if update.CheckpointBytes > maskedMaxVersionBytes {
		return Checkpoint{}, Update{}, maskedRefused(RefusalTooLarge, ErrTooLarge)
	}

	return cp, update, nil
}

// maskedPaths loads the content of the checkpoint row cp and returns the
// value paths of its secrets. The content comes from the blob scope of the
// stack and the namespace that the row names, never from a second lookup by
// name.
func (s *Updates) maskedPaths(ctx context.Context, ref Ref, cp Checkpoint) ([]string, error) {
	bl, err := s.blobs.scope(cp.NamespaceID, cp.StackID)
	if err != nil {
		return nil, fmt.Errorf("masked read of %s: %w", ref, err)
	}

	resolved, err := s.resolveCheckpoint(ctx, bl, cp)
	if err != nil {
		if ctx.Err() != nil {
			return nil, maskedRefused(RefusalDeadline, ErrReadDeadline)
		}

		return nil, fmt.Errorf("masked read of %s: %w", ref, err)
	}

	deployment := innerDeployment(resolved).Deployment

	// Defense in depth: maskedUpdate checks the size of the version record
	// before the load; this check stays so that a record that understates
	// the content cannot skip the limit.
	if int64(len(deployment)) > maskedMaxVersionBytes {
		return nil, maskedRefused(RefusalTooLarge, ErrTooLarge)
	}

	return maskedValuePaths(ctx, deployment)
}

// maskedPageOf answers the page of paths that starts at offset, with the
// cursor of the next page bound to bind, or no cursor on the last page.
func maskedPageOf(key []byte, bind cursorPayload, paths []string, offset int, now time.Time) (MaskedPage, error) {
	// Defense in depth: only a signed cursor holds an offset, and verifyCursor
	// refuses a negative one after the HMAC check. This check stays so that a
	// caller that skips both cannot slice the paths with a negative start.
	if offset < 0 {
		return MaskedPage{}, maskedRefused(RefusalBadPath, ErrBadPath)
	}

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
// masked read: a secret is not descended into. Each step is bounded and
// fails closed:
//
//  1. the decode refuses a nesting depth above MaxStateDepth with
//     ErrStateBound;
//  2. the decoded document must have an object at the top and, when it
//     holds resources, an array there;
//  3. the work scan of the read charges every value to the one budget of the
//     read, and refuses with ErrStateBound before the walk builds any path;
//  4. only then the walk builds one path for each marker, never one for a
//     container.
//
// The deadline of ctx is checked after the decode, after the scan and after
// the walk. An error carries no bytes of the deployment.
func maskedValuePaths(ctx context.Context, deployment []byte) ([]string, error) {
	doc, err := decodeDeployment(deployment)
	if err != nil {
		if errors.Is(err, ErrStateBound) {
			return nil, maskedRefused(RefusalTooLarge, ErrStateBound)
		}

		return nil, maskedRefused(RefusalUnreadable, ErrUnreadableState)
	}

	if ctx.Err() != nil {
		return nil, maskedRefused(RefusalDeadline, ErrReadDeadline)
	}

	if !readableShape(doc) {
		return nil, maskedRefused(RefusalUnreadable, ErrUnreadableState)
	}

	// The scan charges the value paths and the markers of all values before the
	// walk builds the first path.
	budget := new(maskBudget)
	if boundErr := checkMaskWork(budget, doc, 0); boundErr != nil {
		return nil, maskedRefused(RefusalTooLarge, boundErr)
	}

	if ctx.Err() != nil {
		return nil, maskedRefused(RefusalDeadline, ErrReadDeadline)
	}

	walker := markerWalker{keys: nil, paths: nil}
	walker.walk(doc)

	slices.Sort(walker.paths)

	if ctx.Err() != nil {
		return nil, maskedRefused(RefusalDeadline, ErrReadDeadline)
	}

	return slices.Compact(walker.paths), nil
}

// readableShape reports whether doc has the shape of a Pulumi deployment: an
// object at the top, whose resources member, when present, is an array.
func readableShape(doc any) bool {
	top, ok := doc.(map[string]any)
	if !ok {
		return false
	}

	raw, present := top["resources"]
	if !present {
		return true
	}

	_, isArray := raw.([]any)

	return isArray
}

// markerWalker walks a document and keeps a stack of the escaped keys on the
// way down. It builds one path string for each secret and none for another
// node.
type markerWalker struct {
	keys  []string
	paths []string
}

// walk appends the path of each secret value below v to w.paths.
func (w *markerWalker) walk(v any) {
	if isSecret(v) {
		path := ""
		if len(w.keys) > 0 {
			path = "/" + strings.Join(w.keys, "/")
		}

		w.paths = append(w.paths, path)

		return
	}

	switch typed := v.(type) {
	case map[string]any:
		for key, elem := range typed {
			w.keys = append(w.keys, EscapePointerToken(key))
			w.walk(elem)
			w.keys = w.keys[:len(w.keys)-1]
		}

	case []any:
		for i, elem := range typed {
			w.keys = append(w.keys, strconv.Itoa(i))
			w.walk(elem)
			w.keys = w.keys[:len(w.keys)-1]
		}

	default:
	}
}
