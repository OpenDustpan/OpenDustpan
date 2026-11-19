package workspace

// ReadMasked is the masked read of a pinned state version of a workspace: the
// caller holds unit:read on the unit, and the answer carries the structure of
// the state with each secret replaced by a marker, keyed by the JSON Pointer
// value path. The read answers in pages of 200 markers behind an opaque
// cursor, loads at most one 5 MB version, and answers no plain secret value
// and no bytes in an error. The boundary check of the version, the value path
// and the cursor is in this file; each refusal carries no value of the caller
// and reports to the sink of the caller.

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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

// maskedMaxVersionIDBytes is the longest version ID the boundary check accepts.
const maskedMaxVersionIDBytes = 64

// maskedChunkBytes is the size of one chunk that the server reads of a state
// version. The context is checked between chunks.
const maskedChunkBytes = 256 << 10

// maskedMaxCursorBytes is the most bytes of one cursor of a masked read. A
// cursor above the cap is refused before any state is read.
const maskedMaxCursorBytes = 512

// maskedCursorTTL is how long a cursor of a masked read stays valid, from the
// time the server made it.
const maskedCursorTTL = 10 * time.Minute

// maskedHMACBytes is the size of the signature of a cursor.
const maskedHMACBytes = sha256.Size

// maskedMaxDepth is the deepest nesting of a JSON document that a masked read
// accepts. A document that nests deeper is refused before it is decoded.
const maskedMaxDepth = 100

// maskedMaxMarkers is the most markers that one version may hold for a masked
// read. A version with more markers is refused before the server builds the
// path of any marker.
const maskedMaxMarkers = 10000

// maskedMaxPathBytes is the most bytes that the value paths of one version may
// need in all. It is the bound on the memory that the paths can use: a path
// holds its whole prefix, so a long key above many values would multiply.
const maskedMaxPathBytes = 32 << 20

var (
	// ErrBadPath is the refusal of a version, a value path or a cursor of a
	// masked read that fails the boundary check. It carries no value of the
	// caller.
	ErrBadPath = errors.New("state: bad path")

	// ErrReadDeadline is the refusal when the read exceeds the deadline of
	// its caller. The answer carries no bytes of the version.
	ErrReadDeadline = errors.New("state: read deadline exceeded")

	// ErrUnreadableState is the refusal when the server cannot read the shape
	// of a version: a state that the client encrypted, a document that is not
	// an object, a resources value that is not an array, or a resource with no
	// type or name. The answer carries no content, so a reader cannot mistake
	// it for a state with no secrets.
	ErrUnreadableState = errors.New("state: unreadable shape")
)

const (
	// RefusalDenied is the reason a read reports when the caller holds no
	// grant on the unit.
	RefusalDenied = "denied"
	// RefusalBadPath is the reason a read reports when the boundary check
	// refuses the version, the value path or the cursor.
	RefusalBadPath = "bad-path"
	// RefusalTooLarge is the reason a read reports when the version record
	// names a content above the size limit of the read.
	RefusalTooLarge = "too-large"
	// RefusalDeadline is the reason a read reports when the context of its
	// caller is past its deadline.
	RefusalDeadline = "deadline"
)

// RefusalSink counts one refusal of a masked read. The object is the unit
// the read names, never a value of the version; reason is one of the
// Refusal* constants of this package. A nil sink disables the count.
type RefusalSink func(p identity.Principal, obj identity.Object, reason string)

// VersionSelector names the version of a masked read. Latest selects the
// current version; otherwise ID names the version of the workspace.
type VersionSelector struct {
	Latest bool
	ID     StateVersionID
}

// ReadMaskedArgs are the arguments of one masked read. Key is the HMAC key of
// the cursors, bound by the server; Sink counts the refusals of the read, and
// nil disables it. Cursor is the cursor of a previous page, or empty for the
// first page.
type ReadMaskedArgs struct {
	Ref        Ref
	Object     identity.Object
	Selector   VersionSelector
	Principal  identity.Principal
	Authorizer identity.Authorizer
	Key        []byte
	Sink       RefusalSink
	Cursor     string
}

// MaskedMarker is the marker of one secret value of a version. Path is the
// JSON Pointer value path of the value, in the canonical RFC 6901 form. Secret
// is always true.
type MaskedMarker struct {
	Secret bool   `json:"secret"`
	Path   string `json:"path"`
}

// MaskedPage is one page of a masked read. Version is the ID of the resolved
// version. Markers and ValuePaths hold the markers of this page, ValuePaths as
// the matching paths, in the same order. NextCursor is the cursor of the next
// page, or empty on the last page.
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
func (s *Service) ReadMasked(ctx context.Context, args ReadMaskedArgs) (MaskedPage, error) {
	refuse := func(reason string, err error) (MaskedPage, error) {
		if args.Sink != nil {
			args.Sink(args.Principal, args.Object, reason)
		}

		return MaskedPage{}, err
	}

	if err := ctx.Err(); err != nil {
		return refuse(RefusalDeadline, ErrReadDeadline)
	}

	if err := args.Authorizer.Authorize(ctx, args.Principal, identity.ActionUnitRead, args.Object); err != nil {
		return refuse(RefusalDenied, fmt.Errorf("masked read of the unit: %w", err))
	}

	page, reason, err := s.maskedRead(ctx, args)
	if reason != "" {
		return refuse(reason, err)
	}

	return page, err
}

// maskedRead runs the steps of a masked read after the authorization. It
// returns the reason of a refusal, or an empty reason for a plain error.
func (s *Service) maskedRead(ctx context.Context, args ReadMaskedArgs) (MaskedPage, string, error) {
	w, err := s.maskedWorkspace(ctx, args)
	if err != nil {
		return MaskedPage{}, "", err
	}

	sv, err := s.maskedVersion(ctx, w, args.Selector)
	if err != nil {
		reason, refusal := maskedFailure(ctx, err)

		return MaskedPage{}, reason, refusal
	}

	// Defense in depth: the size is checked from the version record before the
	// content is loaded, so an oversized version never reaches the blob store.
	// The length check after the load stays, so a record that understates the
	// content still stops at the same refusal.
	if sv.Size > maskedMaxVersionBytes {
		return MaskedPage{}, RefusalTooLarge, ErrTooLarge
	}

	bind := maskedBinding{Unit: args.Object.String(), Version: string(sv.ID), Subject: args.Principal.Subject}

	offset, err := verifyMaskedCursor(args.Key, args.Cursor, bind, s.clock())
	if err != nil {
		return MaskedPage{}, RefusalBadPath, err
	}

	paths, err := s.maskedPaths(ctx, sv)
	if err != nil {
		reason, refusal := maskedFailure(ctx, err)
		if reason == "" {
			return MaskedPage{}, "", fmt.Errorf("masked read of %s version %s: %w", args.Ref, sv.ID, err)
		}

		return MaskedPage{}, reason, refusal
	}

	page, err := maskedPageOf(paths, offset, args.Key, bind, s.clock())

	return page, "", err
}

// maskedWorkspace returns the workspace that the caller was authorized on. It
// reads by the stable ID of the authorized object, never by name: a name can
// move to another workspace between the check and the read, and the ID
// cannot. A workspace that was deleted and made again has a new ID, so the
// read refuses it as not found.
func (s *Service) maskedWorkspace(ctx context.Context, args ReadMaskedArgs) (Workspace, error) {
	id := ID(args.Object.UnitID())
	if id == "" {
		// Defense in depth: the lookup by ID below answers ErrNotFound for an
		// empty ID too, and the handler always sets the ID; this check stays so
		// that a store that answers an empty ID cannot be reached. Fail closed: with no ID the read cannot tell which workspace the
		// grant covers.
		return Workspace{}, fmt.Errorf("masked read of %s: %w: no unit ID", args.Ref, ErrNotFound)
	}

	w, err := s.GetByID(ctx, id)
	if err != nil {
		return Workspace{}, fmt.Errorf("masked read of %s: %w", args.Ref, err)
	}

	// Defense in depth: the store answers by ID, so the namespace of the
	// object is compared with the namespace of the row. A mismatch is a unit
	// that the grant does not name.
	if w.Ref.Namespace != args.Object.Namespace() {
		return Workspace{}, fmt.Errorf("masked read of %s: %w", args.Ref, ErrNotFound)
	}

	return w, nil
}

// maskedFailure maps an error of a step of the masked read to the refusal it
// reports. The boundary check, the size limit and the deadline are refusals;
// any other error has no reason and is returned as it is.
func maskedFailure(ctx context.Context, err error) (string, error) {
	switch {
	case errors.Is(err, ErrTooLarge):
		return RefusalTooLarge, err
	case errors.Is(err, ErrBadPath):
		return RefusalBadPath, err
	case errors.Is(err, ErrReadDeadline):
		return RefusalDeadline, ErrReadDeadline
	case ctx.Err() != nil:
		return RefusalDeadline, ErrReadDeadline
	default:
		return "", err
	}
}

// maskedBinding is what a cursor of a masked read binds: the unit, the
// resolved version and the caller.
type maskedBinding struct {
	Unit    string
	Version string
	Subject string
}

// maskedVersion resolves the selector of a read to the version of the
// workspace w, without holding its content. A pinned version passes the
// boundary check first, and must belong to w: a version of another workspace
// is refused as not found.
func (s *Service) maskedVersion(ctx context.Context, w Workspace, sel VersionSelector) (StateVersion, error) {
	if sel.Latest {
		sv, err := s.states.CurrentStateVersion(ctx, w.ID)
		if err != nil {
			return StateVersion{}, fmt.Errorf("current state of %s: %w", w.ID, err)
		}

		// Defense in depth: the store follows the pointer of the workspace; the
		// owner is checked here too, as for a pinned version.
		if sv.Workspace != w.ID {
			return StateVersion{}, fmt.Errorf("%w: current state of %s", ErrStateVersionNotFound, w.ID)
		}

		return sv, nil
	}

	if err := validMaskedVersionID(sel.ID); err != nil {
		return StateVersion{}, err
	}

	// The store takes the namespace and the workspace with the ID of the
	// version, so it cannot answer the version of another workspace.
	sv, err := s.states.GetWorkspaceStateVersion(ctx, w.Ref.Namespace, w.ID, sel.ID)
	if err != nil {
		return StateVersion{}, fmt.Errorf("read state %s: %w", sel.ID, err)
	}

	// Defense in depth: the store lookup takes the workspace too; the owner and
	// the status are checked here as well, so a store that ignores the
	// workspace still cannot answer the version of another workspace or a
	// version that is not finalized. The status check does not rely on the check
	// of Open, which refuses a version that is not finalized later.
	if sv.Workspace != w.ID || sv.Status != StatusFinalized {
		return StateVersion{}, fmt.Errorf("%w: state version %s", ErrStateVersionNotFound, sel.ID)
	}

	return sv, nil
}

// maskedPaths loads the content of sv and returns the sorted value paths of its
// secrets. The content is loaded only after the size of the record passed. The
// deadline is checked after the parse and after the walk, so a read never
// answers after its deadline.
func (s *Service) maskedPaths(ctx context.Context, sv StateVersion) ([]string, error) {
	content, err := s.readMaskedContent(ctx, sv)
	if err != nil {
		return nil, err
	}

	return maskedStatePaths(ctx, content, sv.SensitiveMarks)
}

// maskedPageOf returns the page of paths that starts at offset. A page after
// the first one carries a cursor that binds the same unit, version and caller.
func maskedPageOf(
	paths []string, offset int, key []byte, bind maskedBinding, now time.Time,
) (MaskedPage, error) {
	// Defense in depth: verifyMaskedCursor refuses a negative offset and the
	// signature covers it; this check stays so that a new caller cannot slice
	// the paths with a negative offset.
	if offset < 0 {
		return MaskedPage{}, fmt.Errorf("%w: offset", ErrBadPath)
	}

	if offset > len(paths) {
		offset = len(paths)
	}

	end := offset + maskedPageMarkers
	next := ""

	if end < len(paths) {
		var err error

		next, err = mintMaskedCursor(key, bind, end, now)
		if err != nil {
			return MaskedPage{}, err
		}
	} else {
		end = len(paths)
	}

	page := paths[offset:end]
	markers := make([]MaskedMarker, 0, len(page))

	for _, path := range page {
		markers = append(markers, MaskedMarker{Secret: true, Path: path})
	}

	return MaskedPage{
		Version:    bind.Version,
		Markers:    markers,
		ValuePaths: page,
		NextCursor: next,
	}, nil
}

// validMaskedVersionID is the allowlist of a version ID of a masked read: the
// "sv-" prefix, lowercase letters, digits and hyphens, at most 64 bytes.
func validMaskedVersionID(id StateVersionID) error {
	s := string(id)

	if s == "" || len(s) > maskedMaxVersionIDBytes || !strings.HasPrefix(s, "sv-") {
		return fmt.Errorf("%w: version", ErrBadPath)
	}

	for i := range len(s) {
		c := s[i]

		if (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '-' {
			return fmt.Errorf("%w: version", ErrBadPath)
		}
	}

	return nil
}

// readMaskedContent loads the content of sv in chunks of maskedChunkBytes, and
// refuses a content over the size limit with ErrTooLarge. The reader stops one
// byte past the limit, so the refusal does not depend on the record. The
// context is checked before each chunk, so a deadline stops a slow read.
func (s *Service) readMaskedContent(ctx context.Context, sv StateVersion) ([]byte, error) {
	rc, err := s.Open(ctx, sv)
	if err != nil {
		return nil, err
	}

	content, readErr := readMaskedChunks(ctx, io.LimitReader(rc, maskedMaxVersionBytes+1))
	closeErr := rc.Close()

	if readErr != nil {
		return nil, fmt.Errorf("read state content: %w", readErr)
	}

	if closeErr != nil {
		return nil, fmt.Errorf("close state content: %w", closeErr)
	}

	if len(content) > maskedMaxVersionBytes {
		return nil, fmt.Errorf("%w: content of version %s", ErrTooLarge, sv.ID)
	}

	return content, nil
}

// readMaskedChunks reads r to its end in chunks of maskedChunkBytes. Before each
// chunk it checks ctx, and it stops at once on a canceled or expired context.
func readMaskedChunks(ctx context.Context, r io.Reader) ([]byte, error) {
	var content []byte

	chunk := make([]byte, maskedChunkBytes)

	for {
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("read state content: %w", err)
		}

		n, err := io.ReadFull(r, chunk)
		content = append(content, chunk[:n]...)

		if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
			return content, nil
		}

		if err != nil {
			return nil, fmt.Errorf("read chunk: %w", err)
		}
	}
}

// maskedStatePaths returns the sorted, unique JSON Pointer value paths of the
// secrets of state: the masked resource values of MaskedPaths, and each
// sensitive output. An error carries no bytes of the state. The work is
// bounded by the shape of the state: a nesting depth above maskedMaxDepth or
// more than maskedMaxMarkers markers is refused with ErrTooLarge before the
// server builds the path of any marker. The deadline is checked after the
// parse and after the walk.
func maskedStatePaths(ctx context.Context, state []byte, marks SensitiveMarks) ([]string, error) {
	paths, err := maskedResourcePaths(ctx, state, marks)
	if err != nil {
		return nil, err
	}

	outputs, err := maskedOutputPaths(state)
	if err != nil {
		return nil, err
	}

	paths = append(paths, outputs...)

	slices.Sort(paths)

	paths = slices.Compact(paths)

	// Defense in depth: the walk and maskedResourcePaths bound the markers of the
	// resources; this check bounds the markers with the sensitive outputs added,
	// so it does not rely on those checks.
	if len(paths) > maskedMaxMarkers {
		return nil, fmt.Errorf("%w: more than %d markers", ErrTooLarge, maskedMaxMarkers)
	}

	if ctx.Err() != nil {
		return nil, ErrReadDeadline
	}

	return paths, nil
}

// maskedResourcePaths returns the masked set of state: the value path of each
// marker of every resource instance, masked with marks. With no kept marks
// for the version, every attribute value is a marker. It refuses a state that
// it cannot read with ErrUnreadableState, and a state over the bounds of the
// work with ErrTooLarge, before it builds a path.
func maskedResourcePaths(ctx context.Context, state []byte, marks SensitiveMarks) ([]string, error) {
	if err := maskedCheckDepth(state); err != nil {
		return nil, err
	}

	encrypted, err := IsClientEncrypted(state)
	if err != nil || encrypted {
		return nil, fmt.Errorf("%w: the state is encrypted by the client or not an object", ErrUnreadableState)
	}

	doc, err := decodeState(state)
	if err != nil {
		return nil, err
	}

	if ctx.Err() != nil {
		return nil, ErrReadDeadline
	}

	entries, skipped := tofuStateResources(doc)
	if skipped {
		return nil, fmt.Errorf("%w: resources has an entry that the server cannot read", ErrUnreadableState)
	}

	if err := maskedCheckWork(entries, marks); err != nil {
		return nil, err
	}

	masked := []string{}

	for _, entry := range entries {
		for i, instance := range tofuInstances(entry.obj) {
			_, paths := maskTofuInstance(
				instance, entry.index, i, tofuInstanceAddress(entry.obj, instance), marks,
			)
			masked = append(masked, paths...)
		}
	}

	// The copies of a masked secret add markers that the pointers did not
	// count, so the count is checked after the masking too.
	if len(masked) > maskedMaxMarkers {
		return nil, fmt.Errorf("%w: more than %d markers", ErrTooLarge, maskedMaxMarkers)
	}

	return masked, nil
}

// maskedCheckDepth refuses a document that nests deeper than maskedMaxDepth.
// It scans the bytes and allocates nothing, so a deep document never reaches
// the decoder.
func maskedCheckDepth(state []byte) error {
	depth := 0
	inString := false

	for i := 0; i < len(state); i++ {
		c := state[i]

		if inString {
			switch c {
			case '\\':
				i++
			case '"':
				inString = false
			}

			continue
		}

		switch c {
		case '"':
			inString = true
		case '{', '[':
			depth++

			if depth > maskedMaxDepth {
				return fmt.Errorf("%w: nesting deeper than %d", ErrTooLarge, maskedMaxDepth)
			}
		case '}', ']':
			depth--
		}
	}

	return nil
}

// maskedWork is the count of the work of one masked read, made before any path
// is built: the leaf values, the kept marks and the bytes that the paths of
// all values need.
type maskedWork struct {
	markers   int
	pathBytes int
}

// maskedCheckWork refuses a state whose markers or paths are over the bounds.
// It builds no path: it carries the length of the path of each value as a
// number. With no kept marks every leaf is a marker; with marks the markers
// come from the pointers that the instance names.
func maskedCheckWork(entries []tofuResourceEntry, marks SensitiveMarks) error {
	var work maskedWork

	for _, entry := range entries {
		for i, instance := range tofuInstances(entry.obj) {
			base := len("/resources/") + len(strconv.Itoa(entry.index)) +
				len("/instances/") + len(strconv.Itoa(i)) + len("/attributes")

			if len(marks) > 0 {
				address := tofuInstanceAddress(entry.obj, instance)
				work.markers += len(tofuSensitivePointers(instance, address, tofuInstanceBase(address), marks))
			}

			attributes := instance["attributes"]
			if attributes == nil {
				// The masking replaces missing attributes with an empty object.
				attributes = map[string]any{}
			}

			if err := work.walk(attributes, base, len(marks) == 0); err != nil {
				return err
			}
		}
	}

	return nil
}

// walk adds the values under v to the work. pathLen is the length of the path
// of v. A leaf counts as a marker when leaves is true.
func (w *maskedWork) walk(v any, pathLen int, leaves bool) error {
	w.pathBytes += pathLen

	if w.pathBytes > maskedMaxPathBytes {
		return fmt.Errorf("%w: the value paths need more than %d bytes", ErrTooLarge, maskedMaxPathBytes)
	}

	switch typed := v.(type) {
	case map[string]any:
		for key, elem := range typed {
			escaped := len(key) + strings.Count(key, "~") + strings.Count(key, "/")
			if err := w.walk(elem, pathLen+1+escaped, leaves); err != nil {
				return err
			}
		}
	case []any:
		for i, elem := range typed {
			if err := w.walk(elem, pathLen+1+len(strconv.Itoa(i)), leaves); err != nil {
				return err
			}
		}
	default:
		if leaves {
			w.markers++
		}
	}

	if w.markers > maskedMaxMarkers {
		return fmt.Errorf("%w: more than %d markers", ErrTooLarge, maskedMaxMarkers)
	}

	return nil
}

// maskedOutputPaths returns the value path of each sensitive output of state.
func maskedOutputPaths(state []byte) ([]string, error) {
	var doc struct {
		Outputs map[string]struct {
			Sensitive bool `json:"sensitive"`
		} `json:"outputs"`
	}

	if err := json.Unmarshal(state, &doc); err != nil {
		return nil, fmt.Errorf("decode outputs: %w", err)
	}

	var paths []string

	for name, out := range doc.Outputs {
		if out.Sensitive {
			paths = append(paths, "/outputs/"+escapePointerToken(name)+"/value")
		}
	}

	return paths, nil
}

// escapePointerToken escapes one reference token of a JSON Pointer (RFC 6901).
func escapePointerToken(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, "~", "~0"), "/", "~1")
}

// maskedCursorPayload is the payload of a cursor of a masked read. The payload
// binds the unit, the resolved version, the caller and the offset of the page,
// and expires ten minutes after the server made it.
type maskedCursorPayload struct {
	Unit    string `json:"u"`
	Version string `json:"v"`
	Subject string `json:"s"`
	Offset  int    `json:"o"`
	Expiry  int64  `json:"e"`
}

// signMaskedCursor signs payload with the key of the server.
func signMaskedCursor(key, payload []byte) []byte {
	mac := hmac.New(sha256.New, key)
	// A hash never fails.
	_, _ = mac.Write(payload)

	return mac.Sum(nil)
}

// mintMaskedCursor makes the cursor of the page that starts at offset of the
// markers of the version of the unit, bound to the caller in bind.
func mintMaskedCursor(key []byte, bind maskedBinding, offset int, now time.Time) (string, error) {
	payload, err := json.Marshal(maskedCursorPayload{
		Unit:    bind.Unit,
		Version: bind.Version,
		Subject: bind.Subject,
		Offset:  offset,
		Expiry:  now.Add(maskedCursorTTL).Unix(),
	})
	if err != nil {
		return "", fmt.Errorf("masked read: cursor payload: %w", err)
	}

	return base64.RawURLEncoding.EncodeToString(payload) + "." +
		base64.RawURLEncoding.EncodeToString(signMaskedCursor(key, payload)), nil
}

// splitMaskedCursor splits a cursor into its payload and its signature. The
// cursor has the length cap, one dot, and two parts that decode as base64url;
// the signature has the size of a SHA-256 sum.
func splitMaskedCursor(raw string) (maskedCursorParts, error) {
	if len(raw) > maskedMaxCursorBytes {
		return maskedCursorParts{}, ErrBadPath
	}

	left, right, ok := strings.Cut(raw, ".")
	if !ok || strings.Contains(right, ".") {
		return maskedCursorParts{}, ErrBadPath
	}

	payload, err := base64.RawURLEncoding.DecodeString(left)
	if err != nil || len(payload) == 0 {
		return maskedCursorParts{}, ErrBadPath
	}

	mac, err := base64.RawURLEncoding.DecodeString(right)
	if err != nil || len(mac) != maskedHMACBytes {
		return maskedCursorParts{}, ErrBadPath
	}

	return maskedCursorParts{payload: payload, mac: mac}, nil
}

// maskedCursorParts are the two decoded parts of a cursor: the signed payload
// and its signature.
type maskedCursorParts struct {
	payload []byte
	mac     []byte
}

// verifyMaskedCursor checks the cursor of a masked read and returns the offset
// of its page. The empty cursor is the first page. Any failure answers
// ErrBadPath, with no echo of the cursor.
func verifyMaskedCursor(key []byte, raw string, bind maskedBinding, now time.Time) (int, error) {
	if raw == "" {
		return 0, nil
	}

	parts, err := splitMaskedCursor(raw)
	if err != nil {
		return 0, err
	}

	if !hmac.Equal(signMaskedCursor(key, parts.payload), parts.mac) {
		return 0, ErrBadPath
	}

	var parsed maskedCursorPayload

	if err := json.Unmarshal(parts.payload, &parsed); err != nil {
		return 0, ErrBadPath
	}

	if parsed.Unit != bind.Unit || parsed.Version != bind.Version || parsed.Subject != bind.Subject {
		return 0, ErrBadPath
	}

	if parsed.Offset < 0 || parsed.Expiry < now.Unix() {
		return 0, ErrBadPath
	}

	return parsed.Offset, nil
}
