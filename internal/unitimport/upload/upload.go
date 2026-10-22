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
//	<unit key>/unit.json                    the tool and the name of the unit
//	<unit key>/<sequence>.<attempt>.bin     the bytes of one attempt to store the version
//	<unit key>/<sequence>.json              the facts of the version
//
// Each attempt writes its body to its own key, and no key is written twice.
// The facts file names the body key, the size and the digest. The create-only
// write of the facts file is the commit of a version: the first attempt that
// writes it wins, and the body of each other attempt is deleted or stays as
// an unreferenced object. A body that no facts file names is never read.
// The cancel and the release of the import delete each object below the prefix.
package upload

import (
	"bytes"
	"context"
	"crypto/rand"
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

	"opendustpan.org/opendustpan/internal/unitimport"
	"opendustpan.org/opendustpan/pkg/blob"
)

// MaxMetadataBytes is the limit of the metadata document of one version.
const MaxMetadataBytes = 64 << 10

// maxFactsBytes is the most that the source reads of a facts or unit file.
const maxFactsBytes = MaxMetadataBytes + 4<<10

// sequenceDigits is the width of a sequence in a key and in a version ID, so
// that the order of the keys is the order of the versions.
const sequenceDigits = 10

// sha256Hex is the length of a SHA-256 digest in hex.
const sha256Hex = 64

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
	Body     string          `json:"body,omitempty"`
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

	f, err := s.loadFacts(ctx, u, seq)
	if err != nil {
		return nil, err
	}

	k, err := s.bodyKey(u, f)
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
// a body above the limit, a bad sequence, a bad metadata document, or a
// sequence that another upload stored first. A stored version never changes.
func (s *Source) Put(
	ctx context.Context, u unitimport.SourceUnit, seq int, v Version, body io.Reader,
) (unitimport.SourceVersion, error) {
	f, unitFile, err := s.prepare(ctx, u, seq, v)
	if err != nil {
		return unitimport.SourceVersion{}, err
	}

	bk, err := s.bodyKey(u, f)
	if err != nil {
		return unitimport.SourceVersion{}, err
	}

	size, sum, err := s.putBody(ctx, bk, body)
	if err != nil {
		return unitimport.SourceVersion{}, err
	}

	f.Size, f.SHA256 = size, sum

	return s.commit(ctx, u, f, bk, unitFile)
}

// Delete deletes each object below the prefix of the import: the unit files,
// the facts files and the bodies, also the bodies that no facts file names.
func (s *Source) Delete(ctx context.Context) error {
	objs, err := s.blobs.List(ctx, s.prefix)
	if err != nil {
		return fmt.Errorf("%w: list uploads: %w", unitimport.ErrSourceUnreachable, err)
	}

	for _, o := range objs {
		if err = blob.DeleteRetried(ctx, s.blobs, o.Key); err != nil {
			return fmt.Errorf("%w: delete upload: %w", unitimport.ErrSourceUnreachable, err)
		}
	}

	return nil
}

// prepare checks a Put before it writes. It returns the facts with the
// longest size and digest, and the encoded unit file. The longest facts are
// the largest facts, so a file that is too large is refused before the body
// is stored.
func (s *Source) prepare(
	ctx context.Context, u unitimport.SourceUnit, seq int, v Version,
) (facts, []byte, error) {
	if err := ctxErr(ctx); err != nil {
		return facts{}, nil, err
	}

	if err := checkPut(u, seq, v); err != nil {
		return facts{}, nil, err
	}

	if err := s.checkNext(ctx, u, seq); err != nil {
		return facts{}, nil, err
	}

	at := v.At
	if at.IsZero() {
		at = s.now()
	}

	f := facts{
		Tool: u.Tool, Name: u.Name, Sequence: seq, Body: bodyName(seq), AtMs: at.UnixMilli(),
		Actor: unitimport.SanitizeSourceActor(v.Actor), Metadata: v.Metadata,
		Size: s.maxBytes, SHA256: strings.Repeat("0", sha256Hex),
	}

	if _, err := encode(f); err != nil {
		return facts{}, nil, err
	}

	unitFile, err := encode(facts{
		Tool: u.Tool, Name: u.Name, Sequence: 0, Body: "", AtMs: 0, Actor: "", Metadata: nil, Size: 0, SHA256: "",
	})
	if err != nil {
		return facts{}, nil, err
	}

	return f, unitFile, nil
}

// commit makes the unit file, and then it writes the facts file f, which
// commits the version. The body at bk is deleted when the commit fails for
// a reason that leaves the facts file unwritten.
func (s *Source) commit(
	ctx context.Context, u unitimport.SourceUnit, f facts, bk blob.Key, unitFile []byte,
) (unitimport.SourceVersion, error) {
	if err := s.ensureUnit(ctx, u, unitFile); err != nil {
		return unitimport.SourceVersion{}, s.discard(ctx, bk, err)
	}

	encoded, err := encode(f)
	if err != nil {
		return unitimport.SourceVersion{}, s.discard(ctx, bk, err)
	}

	k, err := s.key(u, f.Sequence, "json")
	if err != nil {
		return unitimport.SourceVersion{}, s.discard(ctx, bk, err)
	}

	switch err = s.blobs.Put(ctx, k, bytes.NewReader(encoded)); {
	case errors.Is(err, blob.ErrExists):
		return s.settle(ctx, u, f, bk)
	case err != nil:
		// The write may have stored the facts file, and then the body is in
		// use. The body stays.
		return unitimport.SourceVersion{}, fmt.Errorf(
			"%w: write upload file: %w", unitimport.ErrSourceUnreachable, err,
		)
	default:
		return f.version(), nil
	}
}

// settle resolves a create-only write of the facts file that the store
// refused as existing. The store may have retried its own write after it lost
// the response, and then the stored facts file is the one of this attempt.
func (s *Source) settle(
	ctx context.Context, u unitimport.SourceUnit, f facts, bk blob.Key,
) (unitimport.SourceVersion, error) {
	stored, err := s.loadFacts(ctx, u, f.Sequence)
	if err != nil {
		// The facts file may be this attempt's, and then the body is in use.
		// The body stays.
		return unitimport.SourceVersion{}, fmt.Errorf("%w: read upload file: %w", unitimport.ErrSourceUnreachable, err)
	}

	if stored.Body == f.Body {
		return f.version(), nil
	}

	return unitimport.SourceVersion{}, s.discard(ctx, bk, invalid("sequence %d is stored", f.Sequence))
}

// discard deletes the body at k, which only the failed attempt wrote, and it
// returns cause with any error of the delete.
func (s *Source) discard(ctx context.Context, k blob.Key, cause error) error {
	return errors.Join(cause, s.blobs.Delete(context.WithoutCancel(ctx), k))
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
	if err := u.Validate(); err != nil {
		return fmt.Errorf("%w: %w", unitimport.ErrSourceInvalid, err)
	}

	if seq < 1 {
		return invalid("an upload needs a sequence of 1 or more")
	}

	if len(v.Metadata) > MaxMetadataBytes || (len(v.Metadata) > 0 && !json.Valid(v.Metadata)) {
		return invalid("the metadata is not one JSON document of at most %d bytes", MaxMetadataBytes)
	}

	return nil
}

// checkNext checks that seq is the next sequence of u. The check lists the
// unit directory, so it costs O(n) for each Put. That is acceptable at the
// version limit of one unit.
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
// unitFile is the encoded content of the file.
func (s *Source) ensureUnit(ctx context.Context, u unitimport.SourceUnit, unitFile []byte) error {
	k, err := s.unitKey(u)
	if err != nil {
		return err
	}

	err = s.blobs.Put(ctx, k, bytes.NewReader(unitFile))
	if err != nil && !errors.Is(err, blob.ErrExists) {
		return fmt.Errorf("%w: write upload file: %w", unitimport.ErrSourceUnreachable, err)
	}

	return nil
}

// putBody streams the body to the blob store at k. It removes a partial
// object when the body is too large or the store fails.
func (s *Source) putBody(ctx context.Context, k blob.Key, body io.Reader) (int64, string, error) {
	h := sha256.New()
	counter := &countingReader{r: io.LimitReader(body, s.maxBytes+1), n: 0}

	if err := s.blobs.Put(ctx, k, io.TeeReader(counter, h)); err != nil {
		return 0, "", s.discard(ctx, k, fmt.Errorf("%w: store upload: %w", unitimport.ErrSourceUnreachable, err))
	}

	if counter.n > s.maxBytes {
		return 0, "", s.discard(ctx, k, invalid("the body is larger than %d bytes", s.maxBytes))
	}

	return counter.n, hex.EncodeToString(h.Sum(nil)), nil
}

// bodyName returns the file name of a new attempt to store the body of seq.
func bodyName(seq int) string {
	return pad(seq) + "." + strings.ToLower(rand.Text()) + ".bin"
}

// bodyKey returns the key of the body that f names.
func (s *Source) bodyKey(u unitimport.SourceUnit, f facts) (blob.Key, error) {
	if f.Body == "" || strings.Contains(f.Body, "/") {
		return "", invalid("an upload file names no body")
	}

	k, err := blob.ParseKey(s.unitDir(u) + "/" + f.Body)
	if err != nil {
		return "", fmt.Errorf("upload key: %w", err)
	}

	return k, nil
}

// encode returns the JSON of a facts file. It refuses a file above
// maxFactsBytes, the most that a reader accepts. The encoder keeps the
// characters <, > and & as they are, because a metadata document of the
// largest size would grow past the limit if each were six bytes.
func encode(f facts) ([]byte, error) {
	var buf bytes.Buffer

	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)

	if err := enc.Encode(f); err != nil {
		return nil, fmt.Errorf("encode upload file: %w", err)
	}

	if buf.Len() > maxFactsBytes {
		return nil, invalid("an upload file would be larger than %d bytes", maxFactsBytes)
	}

	return buf.Bytes(), nil
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
