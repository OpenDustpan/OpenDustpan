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
	"strings"
	"time"
	"unicode/utf8"

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

var (
	// ErrBadPath is the refusal of a version, a value path or a cursor of a
	// masked read that fails the boundary check. It carries no value of the
	// caller.
	ErrBadPath = errors.New("state: bad path")

	// ErrReadDeadline is the refusal when the read exceeds the deadline of
	// its caller. The answer carries no bytes of the version.
	ErrReadDeadline = errors.New("state: read deadline exceeded")
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
	w, err := s.Get(ctx, args.Ref)
	if err != nil {
		return MaskedPage{}, "", fmt.Errorf("masked read of %s: %w", args.Ref, err)
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

// maskedFailure maps an error of a step of the masked read to the refusal it
// reports. The boundary check, the size limit and the deadline are refusals;
// any other error has no reason and is returned as it is.
func maskedFailure(ctx context.Context, err error) (string, error) {
	switch {
	case errors.Is(err, ErrTooLarge):
		return RefusalTooLarge, err
	case errors.Is(err, ErrBadPath):
		return RefusalBadPath, err
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

		return sv, nil
	}

	if err := validMaskedVersionID(sel.ID); err != nil {
		return StateVersion{}, err
	}

	sv, err := s.states.GetStateVersion(ctx, sel.ID)
	if err != nil {
		return StateVersion{}, fmt.Errorf("read state %s: %w", sel.ID, err)
	}

	// Defense in depth: the store answers a version of any workspace by its ID,
	// so the owner and the status are checked here too.
	if sv.Workspace != w.ID || sv.Status != StatusFinalized {
		return StateVersion{}, fmt.Errorf("%w: state version %s", ErrStateVersionNotFound, sel.ID)
	}

	return sv, nil
}

// maskedPaths loads the content of sv and returns the sorted value paths of its
// secrets. The content is loaded only after the size of the record passed.
func (s *Service) maskedPaths(ctx context.Context, sv StateVersion) ([]string, error) {
	content, err := s.readMaskedContent(ctx, sv)
	if err != nil {
		return nil, err
	}

	return maskedStatePaths(content, sv.SensitiveMarks)
}

// maskedPageOf returns the page of paths that starts at offset. A page after
// the first one carries a cursor that binds the same unit, version and caller.
func maskedPageOf(
	paths []string, offset int, key []byte, bind maskedBinding, now time.Time,
) (MaskedPage, error) {
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
// sensitive output. An error carries no bytes of the state.
func maskedStatePaths(state []byte, marks SensitiveMarks) ([]string, error) {
	paths, err := maskedResourcePaths(state, marks)
	if err != nil {
		return nil, err
	}

	outputs, err := maskedOutputPaths(state)
	if err != nil {
		return nil, err
	}

	paths = append(paths, outputs...)

	slices.Sort(paths)

	return slices.Compact(paths), nil
}

// maskedResourcePaths returns the masked set of state: the value path of each
// marker of every resource instance, masked with marks. With no kept marks
// for the version, every attribute value is a marker.
func maskedResourcePaths(state []byte, marks SensitiveMarks) ([]string, error) {
	doc, err := decodeState(state)
	if err != nil {
		return nil, err
	}

	entries, _ := tofuStateResources(doc)

	masked := []string{}

	for _, entry := range entries {
		for i, instance := range tofuInstances(entry.obj) {
			_, paths := maskTofuInstance(
				instance, entry.index, i, tofuInstanceAddress(entry.obj, instance), marks,
			)
			masked = append(masked, paths...)
		}
	}

	return masked, nil
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

// ParseValuePath parses a JSON Pointer (RFC 6901) strictly and returns its
// tokens. The empty string is the pointer of the whole document. The error
// carries no echo of the input, so a refusal cannot print a value.
func ParseValuePath(s string) ([]string, error) {
	if !utf8.ValidString(s) {
		return nil, fmt.Errorf("%w: value path", ErrBadPath)
	}

	for _, r := range s {
		if r < 0x20 || r == 0x7f {
			return nil, fmt.Errorf("%w: value path", ErrBadPath)
		}
	}

	if s == "" {
		return []string{}, nil
	}

	if s[0] != '/' {
		return nil, fmt.Errorf("%w: value path", ErrBadPath)
	}

	parts := strings.Split(s[1:], "/")
	tokens := make([]string, 0, len(parts))

	for _, part := range parts {
		if !validPointerEscapes(part) {
			return nil, fmt.Errorf("%w: value path", ErrBadPath)
		}

		tokens = append(tokens, strings.ReplaceAll(strings.ReplaceAll(part, "~1", "/"), "~0", "~"))
	}

	return tokens, nil
}

// validPointerEscapes reports whether every "~" of a reference token is
// followed by "0" or "1", as RFC 6901 requires.
func validPointerEscapes(token string) bool {
	for i := 0; i < len(token); i++ {
		if token[i] != '~' {
			continue
		}

		if i+1 >= len(token) || (token[i+1] != '0' && token[i+1] != '1') {
			return false
		}

		i++
	}

	return true
}
