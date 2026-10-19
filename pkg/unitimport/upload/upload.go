// Package upload is the upload source of unitimport: the versions of the
// units of one import are the bodies that a client sends, one version at a
// time, oldest first. The source streams each body to the blob store, and it
// keeps the claims of the client (time, actor and metadata) beside it. Every
// version is marked as reported by the client.
//
// The source needs no credential and ignores the one that it gets.
//
// Key layout, below the prefix of one import:
//
//	<unit key>/unit.json        the tool and the name of the unit
//	<unit key>/<sequence>.bin   the bytes of the version
//	<unit key>/<sequence>.json  the facts of the version
//
// The facts file is the commit of a version: a body with no facts file is a
// crashed upload, and a later upload of the same sequence replaces it.
package upload

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"
	"time"

	"opendustpan.org/opendustpan/pkg/blob"
	"opendustpan.org/opendustpan/pkg/unitimport"
)

// MaxMetadataBytes is the limit of the metadata document of one version.
const MaxMetadataBytes = 64 << 10

// maxFactsBytes is the most that the source reads of a facts or unit file.
const maxFactsBytes = MaxMetadataBytes + 4<<10

// sequenceDigits is the width of a sequence in a key and in a version ID, so
// that the order of the keys is the order of the versions.
const sequenceDigits = 10

// Source is the upload source of one import.
type Source struct {
	blobs    blob.Store
	now      func() time.Time
	prefix   string
	maxBytes int64
}

var _ unitimport.Source = (*Source)(nil)

// New returns the upload source for the blob keys below prefix. The prefix
// names one import, for example "upload/<namespace ID>/<import ID>". maxBytes
// is the limit of one body. now gives the time of a version that a client sent
// with no time.
func New(blobs blob.Store, prefix string, maxBytes int64, now func() time.Time) (*Source, error) {
	if _, err := blob.ParseKey(prefix); err != nil {
		return nil, fmt.Errorf("upload prefix: %w", err)
	}

	if maxBytes < 1 {
		return nil, fmt.Errorf("%w: the body limit is %d bytes", unitimport.ErrInvalid, maxBytes)
	}

	return &Source{blobs: blobs, now: now, prefix: prefix, maxBytes: maxBytes}, nil
}

// Version is what a client reports with one body. Each value is a claim of
// the client.
type Version struct {
	// At is the time in the source. The zero time means the time of the
	// upload.
	At time.Time
	// Actor is the user or the run in the source, as text.
	Actor string
	// Metadata is the tool part of the version as one JSON document, or empty.
	Metadata []byte
}

// facts is the JSON of a facts file. A unit file holds the tool and the name
// only.
type facts struct {
	Tool     string          `json:"tool"`
	Name     string          `json:"name"`
	Sequence int             `json:"sequence"`
	AtMs     int64           `json:"atMs"`
	Actor    string          `json:"actor"`
	Metadata json.RawMessage `json:"metadata,omitempty"`
	Size     int64           `json:"size"`
	SHA256   string          `json:"sha256"`
}

// Kind implements unitimport.Source.
func (*Source) Kind() unitimport.SourceKind { return unitimport.SourceUpload }

// Units implements unitimport.Source.
func (s *Source) Units(
	ctx context.Context, _ unitimport.Credential, sel unitimport.Selection,
) ([]unitimport.SourceUnit, error) {
	if err := ctxErr(ctx); err != nil {
		return nil, err
	}

	objs, err := s.blobs.List(ctx, s.prefix)
	if err != nil {
		return nil, fmt.Errorf("%w: list uploads: %w", unitimport.ErrSourceUnreachable, err)
	}

	out := []unitimport.SourceUnit{}

	for _, o := range objs {
		if !strings.HasSuffix(o.Key.String(), "/unit.json") {
			continue
		}

		var f facts
		if readErr := s.readJSON(ctx, o.Key, &f); readErr != nil {
			return nil, readErr
		}

		if len(sel.Names) == 0 || slices.Contains(sel.Names, f.Name) {
			out = append(out, unitimport.SourceUnit{Tool: f.Tool, Name: f.Name})
		}
	}

	return out, nil
}

// Versions implements unitimport.Source.
func (s *Source) Versions(
	ctx context.Context, _ unitimport.Credential, u unitimport.SourceUnit, after unitimport.VersionID, limit int,
) ([]unitimport.SourceVersion, error) {
	if err := ctxErr(ctx); err != nil {
		return nil, err
	}

	if limit < 1 {
		return nil, invalid("the page limit is %d", limit)
	}

	afterSeq, err := parseCursor(after)
	if err != nil {
		return nil, err
	}

	if unitErr := s.requireUnit(ctx, u); unitErr != nil {
		return nil, unitErr
	}

	seqs, err := s.factsSequences(ctx, u)
	if err != nil {
		return nil, err
	}

	out := []unitimport.SourceVersion{}

	for _, seq := range seqs {
		if seq <= afterSeq {
			continue
		}

		if len(out) == limit {
			break
		}

		f, loadErr := s.loadFacts(ctx, u, seq)
		if loadErr != nil {
			return nil, loadErr
		}

		out = append(out, f.version())
	}

	return out, nil
}

// Open implements unitimport.Source.
func (s *Source) Open(
	ctx context.Context, _ unitimport.Credential, u unitimport.SourceUnit, id unitimport.VersionID,
) (io.ReadCloser, error) {
	if err := ctxErr(ctx); err != nil {
		return nil, err
	}

	seq, err := strconv.Atoi(string(id))
	if err != nil || seq < 1 {
		return nil, unitimport.ErrSourceNotFound
	}

	if _, err = s.loadFacts(ctx, u, seq); err != nil {
		return nil, err
	}

	k, err := s.key(u, seq, "bin")
	if err != nil {
		return nil, err
	}

	r, err := s.blobs.Get(ctx, k)
	if errors.Is(err, blob.ErrNotFound) {
		return nil, unitimport.ErrSourceNotFound
	}

	if err != nil {
		return nil, fmt.Errorf("%w: open upload: %w", unitimport.ErrSourceUnreachable, err)
	}

	return r, nil
}

// Head implements unitimport.Source.
func (s *Source) Head(
	ctx context.Context, _ unitimport.Credential, u unitimport.SourceUnit,
) (unitimport.VersionID, error) {
	if err := ctxErr(ctx); err != nil {
		return "", err
	}

	if err := s.requireUnit(ctx, u); err != nil {
		return "", err
	}

	seqs, err := s.factsSequences(ctx, u)
	if err != nil {
		return "", err
	}

	if len(seqs) == 0 {
		return "", unitimport.ErrSourceNotFound
	}

	return unitimport.VersionID(pad(seqs[len(seqs)-1])), nil
}

// Put stores the version seq of the unit u. The first version of a unit has
// the sequence 1, and each later version has the next sequence, so a client
// sends the versions oldest first. Put wraps unitimport.ErrSourceInvalid for
// a body above the limit, a bad sequence or a bad metadata document. A client
// sends the versions of one unit one at a time.
func (s *Source) Put(
	ctx context.Context, u unitimport.SourceUnit, seq int, v Version, body io.Reader,
) (unitimport.SourceVersion, error) {
	if err := ctxErr(ctx); err != nil {
		return unitimport.SourceVersion{}, err
	}

	if err := checkPut(u, seq, v); err != nil {
		return unitimport.SourceVersion{}, err
	}

	if err := s.checkNext(ctx, u, seq); err != nil {
		return unitimport.SourceVersion{}, err
	}

	size, sum, err := s.putBody(ctx, u, seq, body)
	if err != nil {
		return unitimport.SourceVersion{}, err
	}

	if unitErr := s.ensureUnit(ctx, u); unitErr != nil {
		return unitimport.SourceVersion{}, unitErr
	}

	at := v.At
	if at.IsZero() {
		at = s.now()
	}

	f := facts{
		Tool: u.Tool, Name: u.Name, Sequence: seq, AtMs: at.UnixMilli(),
		Actor: unitimport.SanitizeSourceActor(v.Actor), Metadata: v.Metadata, Size: size, SHA256: sum,
	}

	k, err := s.key(u, seq, "json")
	if err != nil {
		return unitimport.SourceVersion{}, err
	}

	if writeErr := s.writeJSON(ctx, k, f); writeErr != nil {
		return unitimport.SourceVersion{}, writeErr
	}

	return f.version(), nil
}

func (f facts) version() unitimport.SourceVersion {
	return unitimport.SourceVersion{
		ID:             unitimport.VersionID(pad(f.Sequence)),
		At:             time.UnixMilli(f.AtMs).UTC(),
		Size:           f.Size,
		SHA256:         f.SHA256,
		Actor:          f.Actor,
		Metadata:       f.Metadata,
		ClientReported: true,
	}
}

func invalid(format string, args ...any) error {
	return fmt.Errorf("%w: %s", unitimport.ErrSourceInvalid, fmt.Sprintf(format, args...))
}

func ctxErr(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("upload source: %w", err)
	}

	return nil
}

func pad(seq int) string {
	n := strconv.Itoa(seq)

	return strings.Repeat("0", max(0, sequenceDigits-len(n))) + n
}

// parseCursor returns the sequence of a version ID, 0 for the empty ID.
func parseCursor(after unitimport.VersionID) (int, error) {
	if after == "" {
		return 0, nil
	}

	n, err := strconv.Atoi(string(after))
	if err != nil || n < 0 {
		return 0, invalid("the version cursor is not an upload version")
	}

	return n, nil
}

// checkPut checks the arguments of Put that need no read.
func checkPut(u unitimport.SourceUnit, seq int, v Version) error {
	if u.Tool == "" || u.Name == "" || seq < 1 {
		return invalid("an upload needs a tool, a unit name and a sequence of 1 or more")
	}

	if len(v.Metadata) > MaxMetadataBytes || (len(v.Metadata) > 0 && !json.Valid(v.Metadata)) {
		return invalid("the metadata is not one JSON document of at most %d bytes", MaxMetadataBytes)
	}

	return nil
}

// checkNext checks that seq is the next sequence of u.
func (s *Source) checkNext(ctx context.Context, u unitimport.SourceUnit, seq int) error {
	seqs, err := s.factsSequences(ctx, u)
	if err != nil {
		return err
	}

	if next := len(seqs) + 1; seq != next {
		return invalid("the next version of this unit has the sequence %d, not %d", next, seq)
	}

	return nil
}

// ensureUnit makes the unit file of u when the first version of u is stored.
func (s *Source) ensureUnit(ctx context.Context, u unitimport.SourceUnit) error {
	k, err := s.unitKey(u)
	if err != nil {
		return err
	}

	err = s.writeJSON(ctx, k, facts{
		Tool: u.Tool, Name: u.Name, Sequence: 0, AtMs: 0, Actor: "", Metadata: nil, Size: 0, SHA256: "",
	})
	if errors.Is(err, blob.ErrExists) {
		return nil
	}

	return err
}

// putBody streams the body to the blob store. It removes a partial object
// when the body is too large or the store fails, and it replaces the body of
// a crashed upload of the same sequence.
func (s *Source) putBody(
	ctx context.Context, u unitimport.SourceUnit, seq int, body io.Reader,
) (int64, string, error) {
	k, err := s.key(u, seq, "bin")
	if err != nil {
		return 0, "", err
	}

	if err = s.blobs.Delete(ctx, k); err != nil {
		return 0, "", fmt.Errorf("%w: remove crashed upload: %w", unitimport.ErrSourceUnreachable, err)
	}

	h := sha256.New()
	counter := &countingReader{r: io.LimitReader(body, s.maxBytes+1), n: 0}

	if err = s.blobs.Put(ctx, k, io.TeeReader(counter, h)); err != nil {
		cleanup := s.blobs.Delete(context.WithoutCancel(ctx), k)

		return 0, "", errors.Join(fmt.Errorf("%w: store upload: %w", unitimport.ErrSourceUnreachable, err), cleanup)
	}

	if counter.n > s.maxBytes {
		cleanup := s.blobs.Delete(context.WithoutCancel(ctx), k)

		return 0, "", errors.Join(invalid("the body is larger than %d bytes", s.maxBytes), cleanup)
	}

	return counter.n, hex.EncodeToString(h.Sum(nil)), nil
}

// unitDir returns the key directory of u. The directory name is a digest of
// the tool and the name, because a name holds characters that a key does not.
func (s *Source) unitDir(u unitimport.SourceUnit) string {
	sum := sha256.Sum256([]byte(u.Tool + "\x00" + u.Name))

	return s.prefix + "/" + hex.EncodeToString(sum[:16])
}

func (s *Source) key(u unitimport.SourceUnit, seq int, ext string) (blob.Key, error) {
	k, err := blob.ParseKey(s.unitDir(u) + "/" + pad(seq) + "." + ext)
	if err != nil {
		return "", fmt.Errorf("upload key: %w", err)
	}

	return k, nil
}

func (s *Source) unitKey(u unitimport.SourceUnit) (blob.Key, error) {
	k, err := blob.ParseKey(s.unitDir(u) + "/unit.json")
	if err != nil {
		return "", fmt.Errorf("upload key: %w", err)
	}

	return k, nil
}

// readJSON reads the JSON file at k into v. It returns
// unitimport.ErrSourceNotFound for a missing file.
func (s *Source) readJSON(ctx context.Context, k blob.Key, v any) error {
	r, err := s.blobs.Get(ctx, k)
	if errors.Is(err, blob.ErrNotFound) {
		return unitimport.ErrSourceNotFound
	}

	if err != nil {
		return fmt.Errorf("%w: read upload file: %w", unitimport.ErrSourceUnreachable, err)
	}

	b, readErr := io.ReadAll(io.LimitReader(r, maxFactsBytes+1))
	if closeErr := r.Close(); readErr == nil {
		readErr = closeErr
	}

	if readErr != nil {
		return fmt.Errorf("%w: read upload file: %w", unitimport.ErrSourceUnreachable, readErr)
	}

	if len(b) > maxFactsBytes {
		return invalid("an upload file is larger than %d bytes", maxFactsBytes)
	}

	if json.Unmarshal(b, v) != nil {
		return invalid("an upload file is not valid JSON")
	}

	return nil
}

func (s *Source) writeJSON(ctx context.Context, k blob.Key, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("encode upload file: %w", err)
	}

	if err = s.blobs.Put(ctx, k, strings.NewReader(string(b))); err != nil {
		return fmt.Errorf("%w: write upload file: %w", unitimport.ErrSourceUnreachable, err)
	}

	return nil
}

// factsSequences returns the sequences of u that have a facts file, in order.
func (s *Source) factsSequences(ctx context.Context, u unitimport.SourceUnit) ([]int, error) {
	objs, err := s.blobs.List(ctx, s.unitDir(u))
	if err != nil {
		return nil, fmt.Errorf("%w: list uploads: %w", unitimport.ErrSourceUnreachable, err)
	}

	var seqs []int

	for _, o := range objs {
		name := o.Key.String()[strings.LastIndex(o.Key.String(), "/")+1:]

		base, ok := strings.CutSuffix(name, ".json")
		if !ok || base == "unit" {
			continue
		}

		if n, convErr := strconv.Atoi(base); convErr == nil {
			seqs = append(seqs, n)
		}
	}

	slices.Sort(seqs)

	return seqs, nil
}

func (s *Source) loadFacts(ctx context.Context, u unitimport.SourceUnit, seq int) (facts, error) {
	k, err := s.key(u, seq, "json")
	if err != nil {
		return facts{}, err
	}

	var f facts
	if readErr := s.readJSON(ctx, k, &f); readErr != nil {
		return facts{}, readErr
	}

	return f, nil
}

// requireUnit checks that u has been uploaded to.
func (s *Source) requireUnit(ctx context.Context, u unitimport.SourceUnit) error {
	k, err := s.unitKey(u)
	if err != nil {
		return err
	}

	var f facts

	return s.readJSON(ctx, k, &f)
}

type countingReader struct {
	r io.Reader
	n int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)

	return n, err //nolint:wrapcheck // The error of the body passes through unchanged.
}
