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
//	<unit key>/<sequence>-<attempt>.json    the facts of one attempt to store the version
//
// Each attempt writes its body and its facts file to its own keys, and no key
// is written twice, so a store that replaces an object instead of refusing a
// second write loses nothing. The facts file names the body key, the size and
// the digest. The commit row of the import store is the commit of a version:
// the row names the facts file of the one attempt that won, and the database
// decides which attempt that is. A body or a facts file that no row names is
// never read. The attempt that loses deletes its two files. The cancel and the
// release of the import delete each object below the prefix.
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
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"opendustpan.org/opendustpan/internal/unitimport"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/blob"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// MaxMetadataBytes is the limit of the metadata document of one version.
const MaxMetadataBytes = 64 << 10

// maxFactsBytes is the most that the source reads of a facts or unit file.
const maxFactsBytes = MaxMetadataBytes + 4<<10

// attemptFacts is the name of a facts file of one attempt.
var attemptFacts = regexp.MustCompile(`^\d{10}-[0-9a-z]{1,64}\.json$`)

// sequenceDigits is the width of a sequence in a key and in a version ID, so
// that the order of the keys is the order of the versions.
const sequenceDigits = 10

// sha256Hex is the length of a SHA-256 digest in hex.
const sha256Hex = 64

// Commits is the commit store of one import: the store, the fence of the
// namespace of the import, and the ID of the import. Entry gives the audit
// entry that the store appends in the transaction of a commit row. The caller
// sets the principal, the channel and the request in it; the source does not
// know them.
type Commits struct {
	Store    unitimport.UploadStore
	Fence    namespace.Fence
	ImportID string
	Entry    func(c unitimport.UploadCommit) audit.Entry
}

// Source is the upload source of one import.
type Source struct {
	blobs    blob.Store
	commits  Commits
	now      func() time.Time
	prefix   string
	maxBytes int64
}

var _ unitimport.Source = (*Source)(nil)

// New returns the upload source for the blob keys below prefix. The prefix
// names one import, for example "upload/<namespace ID>/<import ID>". commits
// is the commit store of that import. maxBytes is the limit of one body. now
// gives the time of a version that a client sent with no time and the time of
// each commit.
func New(
	blobs blob.Store, commits Commits, prefix string, maxBytes int64, now func() time.Time,
) (*Source, error) {
	key, err := blob.ParseKey(prefix)
	if err != nil {
		return nil, fmt.Errorf("upload prefix: %w", err)
	}

	// The cancel and the release delete each object below the prefix, and
	// every audit checkpoint copy is evidence that no sweep deletes.
	if audit.IsCheckpointKey(key) {
		return nil, fmt.Errorf(
			"%w: upload prefix %s is under %s",
			unitimport.ErrInvalid,
			prefix,
			audit.CheckpointPrefix,
		)
	}

	if commits.Store == nil || commits.ImportID == "" || commits.Entry == nil {
		return nil, fmt.Errorf(
			"%w: the upload source needs a commit store, an import ID and an audit entry", unitimport.ErrInvalid,
		)
	}

	if maxBytes < 1 {
		return nil, fmt.Errorf("%w: the body limit is %d bytes", unitimport.ErrInvalid, maxBytes)
	}

	return &Source{blobs: blobs, commits: commits, now: now, prefix: prefix, maxBytes: maxBytes}, nil
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

	out := []unitimport.SourceUnit{}

	err := blob.Walk(ctx, s.blobs, s.prefix, func(o blob.Object) error {
		if !strings.HasSuffix(o.Key.String(), "/unit.json") {
			return nil
		}

		var f facts
		if readErr := s.readJSON(ctx, o.Key, &f); readErr != nil {
			return readErr
		}

		if len(sel.Names) == 0 || slices.Contains(sel.Names, f.Name) {
			out = append(out, unitimport.SourceUnit{Tool: f.Tool, Name: f.Name})
		}

		return nil
	})
	if err != nil {
		return nil, s.walkError(err)
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

	idx, err := s.index(ctx, u)
	if err != nil {
		return nil, err
	}

	out := []unitimport.SourceVersion{}

	for _, seq := range idx.seqs {
		if seq <= afterSeq {
			continue
		}

		if len(out) == limit {
			break
		}

		f, loadErr := s.loadFacts(ctx, u, seq, idx.names[seq])
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

	idx, err := s.index(ctx, u)
	if err != nil {
		return nil, err
	}

	name, ok := idx.names[seq]
	if !ok {
		return nil, unitimport.ErrSourceNotFound
	}

	f, err := s.loadFacts(ctx, u, seq, name)
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

	idx, err := s.index(ctx, u)
	if err != nil {
		return "", err
	}

	if len(idx.seqs) == 0 {
		return "", unitimport.ErrSourceNotFound
	}

	return unitimport.VersionID(pad(idx.seqs[len(idx.seqs)-1])), nil
}

// Put stores the version seq of the unit u. The first version of a unit has
// the sequence 1, and each later version has the next sequence, so a client
// sends the versions oldest first. Put wraps unitimport.ErrSourceInvalid for
// a body above the limit, a bad sequence, a bad metadata document, or a
// sequence that another upload committed first; the last also wraps
// unitimport.ErrUploadExists. A stored version never changes.
func (s *Source) Put(
	ctx context.Context, u unitimport.SourceUnit, seq int, v Version, body io.Reader,
) (unitimport.SourceVersion, error) {
	at, err := s.prepare(ctx, u, seq, v)
	if err != nil {
		return unitimport.SourceVersion{}, err
	}

	f := at.facts

	bk, err := s.bodyKey(u, f)
	if err != nil {
		return unitimport.SourceVersion{}, err
	}

	size, sum, err := s.putBody(ctx, bk, body)
	if err != nil {
		return unitimport.SourceVersion{}, err
	}

	f.Size, f.SHA256 = size, sum

	at.facts = f

	return s.commit(ctx, u, at, bk)
}

// Delete deletes each object below the prefix of the import: the unit files,
// the facts files and the bodies, also the bodies that no facts file names.
func (s *Source) Delete(ctx context.Context) error {
	// The walk deletes each object as its page arrives. A key sorts after the
	// cursor of the walk, so a delete does not move the walk.
	err := blob.Walk(ctx, s.blobs, s.prefix, func(o blob.Object) error {
		// Defense in depth: New refuses a prefix under the audit checkpoint
		// prefix, and the listing answers only keys below the prefix. This check
		// stays so that a wrong prefix or a listing that answers too much
		// never deletes an audit checkpoint copy: every copy is evidence.
		if audit.IsCheckpointKey(o.Key) {
			return fmt.Errorf("%w: refuse to delete %s under %s", unitimport.ErrInvalid, o.Key, audit.CheckpointPrefix)
		}

		if delErr := blob.DeleteRetried(ctx, s.blobs, o.Key); delErr != nil {
			return fmt.Errorf("%w: delete upload: %w", unitimport.ErrSourceUnreachable, delErr)
		}

		return nil
	})
	if err != nil {
		return s.walkError(err)
	}

	return nil
}

// walkError turns the error of a walk of the uploads into the error of a
// source. An error that already is a source error stays as it is, and a
// store error is a source that cannot be reached.
func (s *Source) walkError(err error) error {
	if errors.Is(err, unitimport.ErrSourceUnreachable) || errors.Is(err, unitimport.ErrInvalid) {
		return err
	}

	return fmt.Errorf("%w: list uploads: %w", unitimport.ErrSourceUnreachable, err)
}

// attempt is one attempt to store a version: the facts with the longest size
// and digest, the encoded unit file and the identifier of the attempt. The
// longest facts are the largest facts, so a file that is too large is refused
// before the body is stored.
type attempt struct {
	facts    facts
	unitFile []byte
	id       string
	started  time.Time
}

// prepare checks a Put before it writes and returns the new attempt.
func (s *Source) prepare(
	ctx context.Context, u unitimport.SourceUnit, seq int, v Version,
) (attempt, error) {
	if err := ctxErr(ctx); err != nil {
		return attempt{}, err
	}

	if err := checkPut(u, seq, v); err != nil {
		return attempt{}, err
	}

	if err := s.checkNext(ctx, u, seq); err != nil {
		return attempt{}, err
	}

	at := v.At
	if at.IsZero() {
		at = s.now()
	}

	started := s.now().UTC()
	id := newAttempt()

	f := facts{
		Tool: u.Tool, Name: u.Name, Sequence: seq, Body: bodyName(seq, id), AtMs: at.UnixMilli(),
		Actor: unitimport.SanitizeSourceActor(v.Actor), Metadata: v.Metadata,
		Size: s.maxBytes, SHA256: strings.Repeat("0", sha256Hex),
	}

	if _, err := encode(f); err != nil {
		return attempt{}, err
	}

	unitFile, err := encode(facts{
		Tool: u.Tool, Name: u.Name, Sequence: 0, Body: "", AtMs: 0, Actor: "", Metadata: nil, Size: 0, SHA256: "",
	})
	if err != nil {
		return attempt{}, err
	}

	return attempt{facts: f, unitFile: unitFile, id: id, started: started}, nil
}

// commit makes the unit file, writes the facts file of the attempt, and
// inserts the commit row, which commits the version. The row decides which
// attempt wins a sequence. The body at bk and the facts file of this attempt
// are deleted when the row says that another attempt won, or when the attempt
// fails before the row. After an error of the commit that does not say whether
// the row exists, both files stay.
func (s *Source) commit(
	ctx context.Context, u unitimport.SourceUnit, at attempt, bk blob.Key,
) (unitimport.SourceVersion, error) {
	f := at.facts

	if err := s.ensureUnit(ctx, u, at.unitFile); err != nil {
		return unitimport.SourceVersion{}, s.discard(ctx, err, bk)
	}

	encoded, err := encode(f)
	if err != nil {
		return unitimport.SourceVersion{}, s.discard(ctx, err, bk)
	}

	name := factsName(f.Sequence, at.id)

	fk, err := s.factsKey(u, name)
	if err != nil {
		return unitimport.SourceVersion{}, s.discard(ctx, err, bk)
	}

	if err = s.blobs.Put(ctx, fk, bytes.NewReader(encoded)); err != nil {
		return unitimport.SourceVersion{}, s.discard(ctx, fmt.Errorf(
			"%w: write upload file: %w", unitimport.ErrSourceUnreachable, err,
		), bk, fk)
	}

	row := unitimport.UploadCommit{
		ImportID: s.commits.ImportID, UnitDir: s.unitDirName(u), Sequence: f.Sequence, Facts: name,
		CommittedAt: s.now().UTC(), AttemptStartedAt: at.started,
	}

	switch err = s.commits.Store.CommitUploadVersion(ctx, s.commits.Fence, row, s.commits.Entry(row)); {
	case err == nil:
		return f.version(), nil
	case errors.Is(err, unitimport.ErrUploadExists):
		return unitimport.SourceVersion{}, s.discard(ctx, fmt.Errorf(
			"%w: sequence %d is stored: %w", unitimport.ErrSourceInvalid, f.Sequence, err,
		), bk, fk)
	case errors.Is(err, unitimport.ErrAttemptExpired):
		return unitimport.SourceVersion{}, s.discard(ctx, fmt.Errorf(
			"%w: the attempt for sequence %d is outside its time window (at most %s old): %w",
			unitimport.ErrSourceInvalid, f.Sequence, unitimport.UploadAttemptDeadline, err,
		), bk, fk)
	case errors.Is(err, unitimport.ErrInvalid), errors.Is(err, unitimport.ErrNotFound),
		errors.Is(err, unitimport.ErrPurged), errors.Is(err, unitimport.ErrTransition),
		errors.Is(err, namespace.ErrFenced), errors.Is(err, namespace.ErrNotFound):
		// The store refused the row, so no row names the two files.
		return unitimport.SourceVersion{}, s.discard(ctx, fmt.Errorf("commit upload version: %w", err), bk, fk)
	default:
		return s.settle(ctx, u, f, name, err)
	}
}

// settle resolves a commit that failed with an error that does not say
// whether the row exists, for example a lost response. It reads the row of
// the sequence. When the row names the facts file of this attempt, the commit
// succeeded. Otherwise the two files stay, because the commit may still land,
// and the cancel or the release of the import deletes them.
func (s *Source) settle(
	ctx context.Context, u unitimport.SourceUnit, f facts, name string, cause error,
) (unitimport.SourceVersion, error) {
	rows, err := s.commits.Store.UploadCommits(ctx, s.commits.Fence.Namespace, s.commits.ImportID, s.unitDirName(u))
	if err == nil {
		for _, r := range rows {
			if r.Sequence == f.Sequence && r.Facts == name {
				return f.version(), nil
			}
		}
	}

	return unitimport.SourceVersion{}, fmt.Errorf(
		"%w: commit upload version: %w", unitimport.ErrSourceUnreachable, cause,
	)
}

// discard deletes the objects at keys, which only the failed attempt wrote,
// and it returns cause with any error of the deletes.
func (s *Source) discard(ctx context.Context, cause error, keys ...blob.Key) error {
	errs := make([]error, 0, 1+len(keys))
	errs = append(errs, cause)

	for _, k := range keys {
		errs = append(errs, s.blobs.Delete(context.WithoutCancel(ctx), k))
	}

	return errors.Join(errs...)
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
	idx, err := s.index(ctx, u)
	if err != nil {
		return err
	}

	if next := len(idx.seqs) + 1; seq != next {
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
		return 0, "", s.discard(ctx, fmt.Errorf("%w: store upload: %w", unitimport.ErrSourceUnreachable, err), k)
	}

	if counter.n > s.maxBytes {
		return 0, "", s.discard(ctx, invalid("the body is larger than %d bytes", s.maxBytes), k)
	}

	return counter.n, hex.EncodeToString(h.Sum(nil)), nil
}

// newAttempt returns the identifier of a new attempt to store a version: a
// random text that is unique to the attempt.
func newAttempt() string {
	return strings.ToLower(rand.Text())
}

// bodyName returns the file name of the body of the attempt to store seq.
func bodyName(seq int, attempt string) string {
	return pad(seq) + "." + attempt + ".bin"
}

// factsName returns the file name of the facts of the attempt to store seq.
func factsName(seq int, attempt string) string {
	return pad(seq) + "-" + attempt + ".json"
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

// factsKey returns the key of the facts file name in the directory of u. The
// name is one of the names that the source or a commit row gives; the check
// here is a defense-in-depth layer next to the check of the commit row, so a
// name from a restored row cannot point outside the unit directory.
func (s *Source) factsKey(u unitimport.SourceUnit, name string) (blob.Key, error) {
	if !attemptFacts.MatchString(name) {
		return "", invalid("an upload file name is not a facts file name")
	}

	k, err := blob.ParseKey(s.unitDir(u) + "/" + name)
	if err != nil {
		return "", fmt.Errorf("upload key: %w", err)
	}

	return k, nil
}

// unitDirName returns the last element of the directory of u: the hex of a
// digest of the tool and the name, which the commit rows hold.
func (s *Source) unitDirName(u unitimport.SourceUnit) string {
	dir := s.unitDir(u)

	return dir[strings.LastIndex(dir, "/")+1:]
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

// versionIndex is the set of the committed versions of one unit: the
// sequences in order, and the name of the facts file of each.
type versionIndex struct {
	seqs  []int
	names map[int]string
}

// index returns the committed versions of u: the commit rows. A facts file of
// an attempt that no row names is not a version.
func (s *Source) index(ctx context.Context, u unitimport.SourceUnit) (versionIndex, error) {
	rows, err := s.commits.Store.UploadCommits(ctx, s.commits.Fence.Namespace, s.commits.ImportID, s.unitDirName(u))
	if err != nil {
		return versionIndex{}, fmt.Errorf("%w: read upload commits: %w", unitimport.ErrSourceUnreachable, err)
	}

	idx := versionIndex{seqs: nil, names: map[int]string{}}

	for _, r := range rows {
		// Defense in depth: a row can come from a restored backup, so the
		// reader checks it again.
		if validErr := r.Validate(); validErr != nil {
			return versionIndex{}, invalid("an upload commit row is not valid: %v", validErr)
		}

		idx.names[r.Sequence] = r.Facts
	}

	for n := range idx.names {
		idx.seqs = append(idx.seqs, n)
	}

	slices.Sort(idx.seqs)

	return idx, nil
}

// loadFacts reads the facts file name of the version seq of u. The sequence in
// the file must be seq.
func (s *Source) loadFacts(ctx context.Context, u unitimport.SourceUnit, seq int, name string) (facts, error) {
	k, err := s.factsKey(u, name)
	if err != nil {
		return facts{}, err
	}

	var f facts
	if readErr := s.readJSON(ctx, k, &f); readErr != nil {
		return facts{}, readErr
	}

	if f.Sequence != seq {
		return facts{}, invalid("an upload file holds the sequence %d, not %d", f.Sequence, seq)
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
