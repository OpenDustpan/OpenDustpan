package service

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"io"

	"opendustpan.org/opendustpan/internal/registry/modulecheck"
	"opendustpan.org/opendustpan/pkg/blob"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/registry"
)

// DefaultMaxModuleArchiveBytes is the largest archive of a module. The server
// reads the whole archive to check it, so the limit is lower than the limit of
// a provider archive.
const DefaultMaxModuleArchiveBytes int64 = 64 << 20

// ModulePublishRequest is a request to publish one module release.
type ModulePublishRequest struct {
	Namespace namespace.Name
	// Name and System are the module name and its target system, for example
	// "vpc" and "aws".
	Name    string
	System  string
	Version string
	// Upload is the token that Stage gave for the archive.
	Upload string
}

// moduleArchiveName returns the file name of a stored module archive. The
// server makes the name: it follows from the address, the version and the
// format that the check found, so the client names nothing that the server
// stores.
func moduleArchiveName(addr registry.Address, version string, format modulecheck.Format) string {
	return addr.Name + "-" + addr.System + "-" + version + "." + format.Extension()
}

// PublishModule publishes one module release from the staged archive of req. A
// module has no signature: the authorization of the caller is the check of the
// publisher, and the audit entry names the caller and the hash of the archive.
// A publish of a version that exists with the same archive returns that
// release and changes nothing. A version that exists with other content is
// refused before any file is copied to a content key. A second publish of the
// same version that runs at the same time can pass this check; the store then
// refuses its record with registry.ErrVersionExists, and the copied content key
// stays unreferenced. It returns registry.ErrInvalid or
// ErrBadRequest for a request that fails its checks, ErrNotStaged,
// ErrTooLarge, modulecheck.ErrRefused, registry.ErrVersionExists,
// namespace.ErrNotFound or namespace.ErrFenced. The staged file is removed
// whatever the result.
func (s *Service) PublishModule(ctx context.Context, c *Caller, req ModulePublishRequest) (registry.Release, error) {
	if err := c.check(); err != nil {
		return registry.Release{}, err
	}

	addr, err := checkModuleRequest(req)
	if err != nil {
		return registry.Release{}, err
	}

	rec, err := s.namespaces.GetNamespace(ctx, req.Namespace)
	if err != nil {
		return registry.Release{}, fmt.Errorf("find namespace: %w", err)
	}

	view, err := s.scope(rec)
	if err != nil {
		return registry.Release{}, err
	}

	key, err := registry.StagingKey(rec.ID, req.Upload)
	if err != nil {
		return registry.Release{}, fmt.Errorf("%w: the upload token is not valid", ErrBadRequest)
	}

	// The staged file goes away whatever happens. A caller who retries uploads
	// it again, or replays the answer through the Idempotency-Key.
	defer s.dropStaged(ctx, view, key)

	f := measured{
		Role: RoleArchive, Name: addr.Name, Token: req.Upload,
		key: key, sum: "", size: 0, md5: "", bytes: nil,
	}

	if measureErr := s.measureModule(ctx, view, &f); measureErr != nil {
		return registry.Release{}, s.refusal(ctx, c, rec, req.Version, measureErr)
	}

	format, err := modulecheck.Check(f.bytes)
	if err != nil {
		return registry.Release{}, s.refusal(ctx, c, rec, req.Version, fmt.Errorf("check the module archive: %w", err))
	}

	f.Name = moduleArchiveName(addr, req.Version, format)

	rel := s.stagedModuleRelease(c, addr, req.Version, f)

	if err = rel.Validate(); err != nil {
		return registry.Release{}, s.refusal(
			ctx, c, rec, req.Version, fmt.Errorf("check the release: %w: %w", errReleaseRefused, err),
		)
	}

	p := modulePublishing(view, req, addr, rec)

	// The check runs before move, so a refused publish writes no content key.
	if refuseErr := s.refuseOtherContent(ctx, p, rel); refuseErr != nil {
		return registry.Release{}, s.refusal(ctx, c, rec, req.Version, refuseErr)
	}

	f.bytes = nil

	if moveErr := s.move(ctx, view, rec.ID, []measured{f}); moveErr != nil {
		return registry.Release{}, s.refusal(ctx, c, rec, req.Version, moveErr)
	}

	rel, err = s.record(ctx, c, p, rel)
	if err != nil {
		return registry.Release{}, s.refusal(ctx, c, rec, req.Version, err)
	}

	return rel, nil
}

// modulePublishing returns what the publish of a module needs after its checks:
// the namespace, the scoped view of the blob store and the address. A module
// has no signature and no other file.
func modulePublishing(
	view *blob.Scoped, req ModulePublishRequest, addr registry.Address, rec namespace.Namespace,
) publishing {
	return publishing{
		view: view,
		req: PublishRequest{
			Namespace: req.Namespace,
			Name:      req.Name,
			Version:   req.Version,
			Protocols: nil,
			Files:     nil,
		},
		addr:  addr,
		files: nil,
		rec:   rec,
	}
}

// checkModuleRequest checks the address and the version of req before any read
// of the store or of the blob store. Every field comes from outside the process.
func checkModuleRequest(req ModulePublishRequest) (registry.Address, error) {
	addr := registry.Address{Namespace: req.Namespace, Kind: registry.KindModule, Name: req.Name, System: req.System}
	if err := addr.Validate(); err != nil {
		return addr, fmt.Errorf("check the address: %w", err)
	}

	if err := registry.ValidateVersion(req.Version); err != nil {
		return addr, fmt.Errorf("check the version: %w", err)
	}

	return addr, nil
}

// stagedModuleRelease returns the release of the staged archive f, before the
// checks of the release.
func (s *Service) stagedModuleRelease(c *Caller, addr registry.Address, version string, f measured) registry.Release {
	return registry.Release{
		PublishedAt: s.now().UTC(), Address: addr, Version: version, SumsFile: "", SumsSHA256: "",
		SignatureFile: "", SignatureSHA256: "", KeyFingerprint: "", PublishedBy: c.Principal.Subject, Protocols: nil,
		Archives: []registry.Archive{{Platform: "", FileName: f.Name, SHA256: f.sum, Size: f.size}},
		Files:    storedFiles([]measured{f}),
	}
}

// measureModule reads the staged archive of a module into memory, up to the
// limit of a module archive, and computes its SHA-256, size and MD5.
func (s *Service) measureModule(ctx context.Context, view *blob.Scoped, f *measured) error {
	r, err := view.Get(ctx, f.key)
	if errors.Is(err, blob.ErrNotFound) {
		return fmt.Errorf("%w: the archive", ErrNotStaged)
	}

	if err != nil {
		return fmt.Errorf("open the staged archive: %w", err)
	}

	digest := blob.NewDigestReader(r)
	m := newMeasure(digest, min(s.maxFile, s.maxModule))

	f.bytes, err = io.ReadAll(m)
	closeErr := r.Close()

	if errors.Is(err, ErrTooLarge) {
		return fmt.Errorf("%w: a module archive is at most %d bytes", ErrTooLarge, min(s.maxFile, s.maxModule))
	}

	if err != nil || closeErr != nil {
		return fmt.Errorf("read the staged archive: %w", errors.Join(err, closeErr))
	}

	f.sum, f.size = m.sum(), m.n
	sum := digest.Digest().MD5
	f.md5 = hex.EncodeToString(sum[:])

	return nil
}

// ModuleReleases returns the releases of the module at addr, from the lowest
// version to the highest. It returns registry.ErrNotFound for a namespace that
// does not exist and for a module with no release, and registry.ErrInvalid for
// an address that is not a valid module address.
func (s *Service) ModuleReleases(ctx context.Context, addr registry.Address) ([]registry.Release, error) {
	if addr.Kind != registry.KindModule {
		return nil, fmt.Errorf("%w: the address is not a module", registry.ErrInvalid)
	}

	if err := addr.Validate(); err != nil {
		return nil, fmt.Errorf("check the address: %w", err)
	}

	if _, err := s.namespaces.GetNamespace(ctx, addr.Namespace); err != nil {
		return nil, notFound(err)
	}

	out, err := s.store.List(ctx, addr)
	if err != nil {
		return nil, fmt.Errorf("list releases: %w", err)
	}

	if len(out) == 0 {
		return nil, registry.ErrNotFound
	}

	return out, nil
}

// ModuleRelease returns the release of version of the module name/system in ns,
// or registry.ErrNotFound.
func (s *Service) ModuleRelease(
	ctx context.Context, ns namespace.Name, name, system, version string,
) (registry.Release, error) {
	return s.releaseAt(
		ctx,
		registry.Address{Namespace: ns, Kind: registry.KindModule, Name: name, System: system},
		version,
	)
}

// OpenModuleFile opens the archive of the published release of version at addr
// by its SHA-256, and returns its size. As Open does, it opens a file only
// through a published release. It returns registry.ErrNotFound for a file that
// the release does not hold and for an address that is not a module.
func (s *Service) OpenModuleFile(
	ctx context.Context, addr registry.Address, version, sum string,
) (int64, io.ReadCloser, error) {
	// Defense in depth: the HTTP adapter builds a module address. This check
	// stays so that a new caller cannot open a file of a provider through the
	// module route.
	if addr.Kind != registry.KindModule {
		return 0, nil, registry.ErrNotFound
	}

	file, r, err := s.openAt(ctx, addr, version, sum)
	if err != nil {
		return 0, nil, err
	}

	return file.Size, r, nil
}

// Modules returns one page of the addresses of the modules in ns that have at
// least one release, ordered by name and system, and the cursor of the next
// page. The cursor after is the cursor of the page before, and it is empty
// for the first page. The next cursor is empty when this page is the last
// one. It returns an empty list for a namespace with no module, and
// registry.ErrNotFound for a namespace that does not exist.
func (s *Service) Modules(
	ctx context.Context, ns namespace.Name, after string, limit int,
) ([]registry.Address, string, error) {
	// Defense in depth: the HTTP handler gets ns from a checked route and the
	// namespace lookup below also refuses an unknown name; this check stays so
	// that a new caller cannot reach the store with a name that is not valid.
	if _, err := namespace.ParseName(string(ns)); err != nil {
		return nil, "", fmt.Errorf("%w: %w", registry.ErrNotFound, err)
	}

	if _, err := s.namespaces.GetNamespace(ctx, ns); err != nil {
		return nil, "", notFound(err)
	}

	addrs, next, err := s.store.Addresses(ctx, ns, registry.KindModule, after, limit)
	if err != nil {
		return nil, "", fmt.Errorf("list modules: %w", err)
	}

	return addrs, next, nil
}
