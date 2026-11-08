package service

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"slices"
	"time"

	"opendustpan.org/opendustpan/internal/registry/releasecheck"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/blob"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/registry"
)

// Bounds of the work of the use cases.
const (
	// cleanupTimeout bounds the removal of a staged file after the request ended.
	cleanupTimeout = 30 * time.Second
	// maxFilesOfRequest is the most files of one publish: every archive, the
	// checksums file and the signature file.
	maxFilesOfRequest = registry.MaxArchives + 2
)

// Staged is a file that the server stored under a staging key. The SHA-256 and
// the size are the values that the server computed while it wrote the file.
type Staged struct {
	Token  string
	SHA256 string
	Size   int64
}

// Stage stores body under a new staging key of ns and returns its token. The
// file is not readable through the registry: only a publish moves it to its
// content key, after every check. A staged file that no publish moved is
// removed after StagingGrace, when a later stage in the namespace sweeps the
// staging directory.
func (s *Service) Stage(ctx context.Context, c *Caller, ns namespace.Name, body io.Reader) (Staged, error) {
	if err := c.check(); err != nil {
		return Staged{}, err
	}

	rec, err := s.namespaces.GetNamespace(ctx, ns)
	if err != nil {
		return Staged{}, fmt.Errorf("find namespace: %w", err)
	}

	view, err := s.scope(rec)
	if err != nil {
		return Staged{}, err
	}

	token, err := newToken()
	if err != nil {
		return Staged{}, err
	}

	key, err := registry.StagingKey(rec.ID, token)
	if err != nil {
		return Staged{}, fmt.Errorf("make the staging key: %w", err)
	}

	s.sweepStaging(ctx, view, rec.ID)

	m := newMeasure(body, s.maxFile)

	if putErr := view.Put(ctx, key, m); putErr != nil {
		s.dropStaged(ctx, view, key)

		if errors.Is(putErr, ErrTooLarge) {
			return Staged{}, ErrTooLarge
		}

		return Staged{}, fmt.Errorf("store the staged file: %w", putErr)
	}

	// Defense in depth: the reader stops the write at the limit. This check stays
	// so that a store that ignores the error of the reader cannot keep a file above
	// the limit, and an empty file never becomes a release.
	if m.n > s.maxFile || m.n == 0 {
		s.dropStaged(ctx, view, key)

		if m.n == 0 {
			return Staged{}, fmt.Errorf("%w: the file is empty", ErrBadRequest)
		}

		return Staged{}, ErrTooLarge
	}

	return Staged{Token: token, SHA256: m.sum(), Size: m.n}, nil
}

// dropStaged removes a staged file after a failed or refused write. It uses a
// context that outlives the request, so a canceled request still cleans up.
func (s *Service) dropStaged(ctx context.Context, view *blob.Scoped, key blob.Key) {
	cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), cleanupTimeout)
	defer cancel()

	// Delete of a missing key is not an error. A failed delete leaves the file to
	// the sweep after the grace period.
	_ = view.Delete(cleanup, key) //nolint:errcheck // Best effort: the sweep removes what stays.
}

// sweepStaging removes the staged files of the namespace that are older than
// StagingGrace. It is best effort and bounded.
func (s *Service) sweepStaging(ctx context.Context, view *blob.Scoped, id namespace.ID) {
	dir, err := registry.StagingKey(id, "aaaaaaaaaaaaaaaaaaaa")
	if err != nil {
		return
	}

	prefix := dir.String()[:len(dir.String())-len("aaaaaaaaaaaaaaaaaaaa")]

	cutoff := s.now().Add(-StagingGrace)
	seen := 0

	// The walk stops after maxStagedPerSweep files, so a sweep stays bounded.
	err = blob.Walk(ctx, view, prefix, func(o blob.Object) error {
		if seen >= maxStagedPerSweep {
			return errSweepBound
		}

		seen++

		if o.ModifiedAt.Before(cutoff) {
			s.dropStaged(ctx, view, o.Key)
		}

		return nil
	})
	if err != nil && !errors.Is(err, errSweepBound) {
		// A failed listing leaves the rest to the next sweep.
		return
	}
}

// FileRole says what a staged file is in a release.
type FileRole string

// The roles of the files of a provider release.
const (
	RoleSums      FileRole = "sums"
	RoleSignature FileRole = "signature"
	RoleArchive   FileRole = "archive"
)

// StagedFile names one staged file of a publish: its role, the file name that
// the release keeps, and the token that Stage gave.
type StagedFile struct {
	Role  FileRole
	Name  string
	Token string
}

// PublishRequest is a request to publish one provider release.
type PublishRequest struct {
	Namespace namespace.Name
	// Name is the provider type, for example "payments".
	Name    string
	Version string
	// Protocols are the plugin protocol versions that the publisher states.
	Protocols []string
	Files     []StagedFile
}

// measured is a staged file after the server measured it again.
type measured struct {
	StagedFile

	key   blob.Key
	sum   string
	size  int64
	md5   string
	bytes []byte
}

// Publish publishes one provider release from the staged files of req. It
// returns the release as stored. A publish of a version that exists with the
// same content returns that release and changes nothing. A version that exists
// with other content is refused before any file is copied to a content key. A
// second publish of the same version that runs at the same time can pass this
// check; the store then refuses its record with registry.ErrVersionExists, and
// the copied content key stays unreferenced. It returns registry.ErrInvalid or
// ErrBadRequest for a request that fails its checks,
// ErrNotStaged, ErrTooLarge, ErrNoKeys, registry.ErrBadSignature,
// registry.ErrUntrustedKey (also for a key removed during the publish),
// releasecheck.ErrRefused, registry.ErrVersionExists,
// namespace.ErrNotFound or namespace.ErrFenced. The staged files are removed
// whatever the result.
func (s *Service) Publish(ctx context.Context, c *Caller, req PublishRequest) (registry.Release, error) {
	if err := c.check(); err != nil {
		return registry.Release{}, err
	}

	addr, err := checkRequest(req)
	if err != nil {
		return registry.Release{}, err
	}

	p, err := s.prepare(ctx, addr, req)
	if err != nil {
		return registry.Release{}, err
	}

	// The staged files go away whatever happens. A caller who retries uploads
	// them again, or replays the answer through the Idempotency-Key.
	defer func() {
		for i := range p.files {
			s.dropStaged(ctx, p.view, p.files[i].key)
		}
	}()

	rel, err := s.verify(ctx, c, p)
	if err != nil {
		return registry.Release{}, s.refusal(ctx, c, p.rec, req.Version, err)
	}

	if refuseErr := s.refuseOtherContent(ctx, p, rel); refuseErr != nil {
		return registry.Release{}, s.refusal(ctx, c, p.rec, req.Version, refuseErr)
	}

	if moveErr := s.move(ctx, p.view, p.rec.ID, p.files); moveErr != nil {
		return registry.Release{}, s.refusal(ctx, c, p.rec, req.Version, moveErr)
	}

	rel, err = s.record(ctx, c, p, rel)
	if err != nil {
		return registry.Release{}, s.refusal(ctx, c, p.rec, req.Version, err)
	}

	return rel, nil
}

// publishing holds what a publish needs after the request is checked: the
// namespace, the scoped view of the blob store and the staged files.
type publishing struct {
	view  *blob.Scoped
	req   PublishRequest
	addr  registry.Address
	files []measured
	rec   namespace.Namespace
}

// prepare finds the namespace, scopes the blob store and makes the staging key
// of each file of req.
func (s *Service) prepare(ctx context.Context, addr registry.Address, req PublishRequest) (publishing, error) {
	var none publishing

	rec, err := s.namespaces.GetNamespace(ctx, req.Namespace)
	if err != nil {
		return none, fmt.Errorf("find namespace: %w", err)
	}

	view, err := s.scope(rec)
	if err != nil {
		return none, err
	}

	files, err := stagedKeys(rec.ID, req.Files)
	if err != nil {
		return none, err
	}

	return publishing{view: view, req: req, addr: addr, files: files, rec: rec}, nil
}

// verify measures the staged files, runs the checks and returns the release to
// store. It writes nothing to a content key.
func (s *Service) verify(ctx context.Context, c *Caller, p publishing) (registry.Release, error) {
	if err := s.measureAll(ctx, p.view, p.files); err != nil {
		return registry.Release{}, err
	}

	rel, err := s.check(ctx, p.rec, p.addr, p.req, p.files)
	if err != nil {
		return registry.Release{}, err
	}

	rel.PublishedBy = c.Principal.Subject
	rel.PublishedAt = s.now().UTC()

	if err = rel.Validate(); err != nil {
		return registry.Release{}, fmt.Errorf("check the release: %w: %w", errReleaseRefused, err)
	}

	return rel, nil
}

// refuseOtherContent returns registry.ErrVersionExists when the version of
// rel is published with other content. It runs before move, so a
// refused publish writes no content key. A repeat with the same content
// passes, and the store answers it.
func (s *Service) refuseOtherContent(ctx context.Context, p publishing, rel registry.Release) error {
	existing, err := s.store.Get(ctx, p.addr, rel.Version)
	if errors.Is(err, registry.ErrNotFound) {
		return nil
	}

	if err != nil {
		return fmt.Errorf("read the published release: %w", err)
	}

	if !existing.SameContent(rel) {
		return registry.ErrVersionExists
	}

	return nil
}

// record stores rel with its fence and audit entry, and returns the release as
// the store holds it. A repeat of a published version changes nothing, so the
// answer is the first release, with the first publisher and time.
func (s *Service) record(ctx context.Context, c *Caller, p publishing, rel registry.Release) (registry.Release, error) {
	// Defense in depth: refuseOtherContent refuses a known version with other
	// content before move. The store checks it again in the transaction of
	// the write, so a publish that races with another one is still refused.
	err := s.store.Publish(ctx, rel, p.rec.Fence(), s.releaseEntry(c, rel))
	if errors.Is(err, registry.ErrKeyNotFound) {
		// A key removed between the check and the commit. The store checked it in
		// the transaction of the write. The answer is the one of a signature of a
		// key that the namespace does not trust.
		return registry.Release{}, fmt.Errorf(
			"%w: the signing key was removed during the publish", registry.ErrUntrustedKey,
		)
	}

	if err != nil {
		return registry.Release{}, fmt.Errorf("publish release: %w", err)
	}

	stored, err := s.store.Get(ctx, p.addr, rel.Version)
	if err != nil {
		return registry.Release{}, fmt.Errorf("read the published release: %w", err)
	}

	return stored, nil
}

// checkRequest checks the request before any read of the store or of the blob
// store. Every field comes from outside the process.
func checkRequest(req PublishRequest) (registry.Address, error) {
	addr := registry.Address{Namespace: req.Namespace, Kind: registry.KindProvider, Name: req.Name, System: ""}
	if err := addr.Validate(); err != nil {
		return addr, fmt.Errorf("check the address: %w", err)
	}

	if err := registry.ValidateVersion(req.Version); err != nil {
		return addr, fmt.Errorf("check the version: %w", err)
	}

	if err := checkFileList(req.Files); err != nil {
		return addr, err
	}

	return addr, nil
}

// checkFileList checks the files of a request: their number, their roles, and
// that no token and no name repeats.
func checkFileList(files []StagedFile) error {
	if len(files) > maxFilesOfRequest {
		return fmt.Errorf("%w: more than %d files", ErrBadRequest, maxFilesOfRequest)
	}

	roles := map[FileRole]int{}
	tokens := map[string]bool{}
	names := map[string]bool{}

	for _, f := range files {
		roles[f.Role]++

		if tokens[f.Token] || names[f.Name] {
			return fmt.Errorf("%w: the token or the name of file %s is used two times", ErrBadRequest, f.Name)
		}

		tokens[f.Token], names[f.Name] = true, true

		if f.Role != RoleSums && f.Role != RoleSignature && f.Role != RoleArchive {
			return fmt.Errorf(
				"%w: file %s has the role %q: use sums, signature or archive",
				ErrBadRequest,
				f.Name,
				f.Role,
			)
		}
	}

	if roles[RoleSums] != 1 || roles[RoleSignature] != 1 || roles[RoleArchive] == 0 {
		return fmt.Errorf("%w: send one sums file, one signature file and at least one archive", ErrBadRequest)
	}

	return nil
}

// stagedKeys makes the staging key of each file. A token that is not a token of
// the registry fails here, so no other key is ever read.
func stagedKeys(id namespace.ID, files []StagedFile) ([]measured, error) {
	out := make([]measured, 0, len(files))

	for _, f := range files {
		key, err := registry.StagingKey(id, f.Token)
		if err != nil {
			return nil, fmt.Errorf("%w: the token of file %s is not valid", ErrBadRequest, f.Name)
		}

		out = append(out, measured{StagedFile: f, key: key, sum: "", size: 0, md5: "", bytes: nil})
	}

	return out, nil
}

// measureAll reads each staged file again and computes its SHA-256 and size.
// The server trusts no hash that a client sends. The checksums file and the
// signature file are also kept in memory: both are small.
func (s *Service) measureAll(ctx context.Context, view *blob.Scoped, files []measured) error {
	var total int64

	for i := range files {
		if err := s.measureOne(ctx, view, &files[i]); err != nil {
			return err
		}

		total += files[i].size

		if total > s.maxRelease {
			return fmt.Errorf("%w: the release is larger than %d bytes", ErrTooLarge, s.maxRelease)
		}
	}

	return nil
}

// limitOf returns the largest size of a file with the given role.
func (s *Service) limitOf(role FileRole) int64 {
	switch role {
	case RoleSums:
		return releasecheck.MaxSumsBytes
	case RoleSignature:
		return MaxSignatureBytes
	case RoleArchive:
		return s.maxFile
	default:
		return 0
	}
}

// measureOne reads one staged file, computes its SHA-256 and size, and keeps
// the bytes of a checksums file and of a signature file.
func (s *Service) measureOne(ctx context.Context, view *blob.Scoped, f *measured) error {
	r, err := view.Get(ctx, f.key)
	if errors.Is(err, blob.ErrNotFound) {
		return fmt.Errorf("%w: %s", ErrNotStaged, f.Name)
	}

	if err != nil {
		return fmt.Errorf("open the staged file %s: %w", f.Name, err)
	}

	digest := blob.NewDigestReader(r)
	m := newMeasure(digest, s.limitOf(f.Role))

	if f.Role == RoleArchive {
		_, err = io.Copy(io.Discard, m)
	} else {
		f.bytes, err = io.ReadAll(m)
	}

	closeErr := r.Close()

	if errors.Is(err, ErrTooLarge) {
		return fmt.Errorf("%w: %s", ErrTooLarge, f.Name)
	}

	if err != nil || closeErr != nil {
		return fmt.Errorf("read the staged file %s: %w", f.Name, errors.Join(err, closeErr))
	}

	f.sum, f.size = m.sum(), m.n
	sum := digest.Digest().MD5
	f.md5 = hex.EncodeToString(sum[:])

	return nil
}

// byRole is the measured files of a request, sorted by role. A nil file is a
// file that the request lacks.
type byRole struct {
	set  releasecheck.Set
	sums *measured
	sig  *measured
}

// sortByRole builds the file set of the release check from the measured files.
func sortByRole(files []measured) byRole {
	var out byRole

	for i := range files {
		f := &files[i]
		file := releasecheck.File{Name: f.Name, SHA256: f.sum, Size: f.size}

		switch f.Role {
		case RoleSums:
			out.set.Sums, out.sums = file, f
		case RoleSignature:
			out.set.Signature, out.sig = file, f
		case RoleArchive:
			out.set.Archives = append(out.set.Archives, file)
		default:
		}
	}

	return out
}

// check runs the checks in the fixed order: the signature over the raw bytes of
// the checksums file first, then the parse, the names and the hashes. It
// returns the release without the publisher and the time.
func (s *Service) check(
	ctx context.Context, rec namespace.Namespace, addr registry.Address, req PublishRequest, files []measured,
) (registry.Release, error) {
	sorted := sortByRole(files)
	set, sums, sig := sorted.set, sorted.sums, sorted.sig

	// Defense in depth: checkFileList refuses a request without one checksums
	// file and one signature. This check stays so that a change of the request
	// check cannot send a missing file to the verifier. It does not rely on
	// the request check.
	if sums == nil || sig == nil {
		return registry.Release{}, fmt.Errorf("%w: the release needs a checksums file and a signature", ErrBadRequest)
	}

	records, err := s.keys.Keys(ctx, rec.Name)
	if err != nil {
		return registry.Release{}, fmt.Errorf("read the trusted keys: %w", err)
	}

	if len(records) == 0 {
		return registry.Release{}, ErrNoKeys
	}

	trusted := make([]registry.TrustedKey, 0, len(records))
	for _, k := range records {
		trusted = append(trusted, k.Key)
	}

	// The signature is checked before the checksums file is parsed, so no parser
	// reads bytes that nobody signed.
	fingerprint, err := s.verifier.Verify(ctx, sums.bytes, sig.bytes, trusted)
	if err != nil {
		return registry.Release{}, fmt.Errorf("%w: %s", err, sig.Name)
	}

	parsed, err := releasecheck.ParseSums(sums.Name, sums.bytes)
	if err != nil {
		return registry.Release{}, fmt.Errorf("parse the checksums file: %w: %w", releasecheck.ErrRefused, err)
	}

	rel, err := releasecheck.Check(addr, req.Version, set, parsed)
	if err != nil {
		return registry.Release{}, fmt.Errorf("check the archive set: %w", err)
	}

	rel.KeyFingerprint = fingerprint
	rel.Protocols = slices.Clone(req.Protocols)
	rel.Files = storedFiles(files)

	return rel, nil
}

// move copies each staged file to its content key. The reader that feeds the
// copy checks the SHA-256 and the size again, and fails at the end when they
// differ, so a content key never holds bytes that were not checked.
func (s *Service) move(ctx context.Context, view *blob.Scoped, id namespace.ID, files []measured) error {
	for i := range files {
		f := &files[i]

		dst, err := registry.BlobKey(id, f.sum)
		if err != nil {
			return fmt.Errorf("make the content key: %w", err)
		}

		src, err := view.Get(ctx, f.key)
		if err != nil {
			return fmt.Errorf("open the staged file %s: %w", f.Name, err)
		}

		m := newMeasure(src, f.size)
		err = view.Put(ctx, dst, &verifying{m: m, want: f.sum, wantSize: f.size})
		closeErr := src.Close()

		// A content key that exists holds bytes with this hash: the key is the
		// hash. The copy writes the same bytes, or leaves the key alone.
		if err != nil && !errors.Is(err, blob.ErrExists) {
			return fmt.Errorf("store the file %s: %w", f.Name, err)
		}

		if closeErr != nil {
			return fmt.Errorf("close the staged file %s: %w", f.Name, closeErr)
		}
	}

	return nil
}

// verifying passes the bytes of m and fails at the end of them when the hash or
// the size is not the one the checks used.
type verifying struct {
	m        *measure
	want     string
	wantSize int64
}

func (v *verifying) Read(p []byte) (int, error) {
	n, err := v.m.Read(p)
	if errors.Is(err, io.EOF) && (v.m.sum() != v.want || v.m.n != v.wantSize) {
		return n, ErrIntegrity
	}

	return n, err
}

// releaseEntry returns the audit entry of a publish. The object is the
// namespace, and the changes name the address, the version, the key and the
// hashes.
func (s *Service) releaseEntry(c *Caller, rel registry.Release) audit.Entry {
	e := s.entry(c, rel.Address.Namespace, identity.ActionRegistryPublish, rel.Address.Namespace.String())

	if rel.Address.Kind == registry.KindModule {
		e.Changes = []audit.Change{
			{
				Key: "registry.release", Old: nil, New: rel.Address.Label() + "@" + rel.Version,
				Layer: "", TakesEffect: "",
			},
			{Key: "registry.archiveSha256", Old: nil, New: rel.Archives[0].SHA256, Layer: "", TakesEffect: ""},
		}

		return e
	}

	e.Changes = []audit.Change{
		{
			Key:         "registry.release",
			Old:         nil,
			New:         "provider/" + rel.Address.Name + "@" + rel.Version,
			Layer:       "",
			TakesEffect: "",
		},
		{Key: "registry.keyFingerprint", Old: nil, New: rel.KeyFingerprint, Layer: "", TakesEffect: ""},
		{Key: "registry.sumsSha256", Old: nil, New: rel.SumsSHA256, Layer: "", TakesEffect: ""},
		{Key: "registry.signatureSha256", Old: nil, New: rel.SignatureSHA256, Layer: "", TakesEffect: ""},
	}

	return e
}

// storedFiles returns the digests of the files of a release: the SHA-256, the
// size and the MD5 that the measure computed while it read each staged file.
// A file is stored as it came, so these are the digests of the stored bytes.
// A file with the same bytes as an earlier one appears once.
func storedFiles(files []measured) []registry.StoredFile {
	out := make([]registry.StoredFile, 0, len(files))

	for i := range files {
		f := &files[i]
		if slices.ContainsFunc(out, func(o registry.StoredFile) bool { return o.SHA256 == f.sum }) {
			continue
		}

		out = append(out, registry.StoredFile{SHA256: f.sum, Size: f.size, MD5: f.md5})
	}

	return out
}
