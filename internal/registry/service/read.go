package service

import (
	"context"
	"errors"
	"fmt"
	"io"

	"opendustpan.org/opendustpan/pkg/blob"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/registry"
)

// File is one file of a published provider release, found by its SHA-256.
type File struct {
	Name   string
	SHA256 string
	Size   int64
}

// Releases returns the releases of the provider name in ns, from the lowest
// version to the highest. It returns registry.ErrNotFound for a namespace that
// does not exist and for a provider with no release, so a caller cannot tell
// the two apart.
func (s *Service) Releases(ctx context.Context, ns namespace.Name, name string) ([]registry.Release, error) {
	addr := registry.Address{Namespace: ns, Kind: registry.KindProvider, Name: name, System: ""}
	if err := addr.Validate(); err != nil {
		return nil, fmt.Errorf("check the address: %w", err)
	}

	if _, err := s.namespaces.GetNamespace(ctx, ns); err != nil {
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

// ServableReleases returns the releases of the provider name in ns whose
// signing key the namespace still trusts, from the lowest version to the
// highest. A release of a removed key is left out, because the registry serves
// no download of it and a client that picks it fails. It returns
// registry.ErrNotFound as Releases does, also when the namespace trusts the
// key of no release.
func (s *Service) ServableReleases(ctx context.Context, ns namespace.Name, name string) ([]registry.Release, error) {
	all, err := s.Releases(ctx, ns, name)
	if err != nil {
		return nil, err
	}

	keys, err := s.keys.Keys(ctx, ns)
	if err != nil {
		return nil, fmt.Errorf("read the trusted keys: %w", err)
	}

	trusted := make(map[string]bool, len(keys))
	for _, k := range keys {
		trusted[k.Key.Fingerprint] = true
	}

	out := make([]registry.Release, 0, len(all))

	for i := range all {
		if trusted[all[i].KeyFingerprint] {
			out = append(out, all[i])
		}
	}

	if len(out) == 0 {
		return nil, registry.ErrNotFound
	}

	return out, nil
}

// Release returns the release of version of the provider name in ns. It returns
// registry.ErrNotFound for a namespace, a provider or a version that does not
// exist.
func (s *Service) Release(ctx context.Context, ns namespace.Name, name, version string) (registry.Release, error) {
	return s.releaseAt(
		ctx,
		registry.Address{Namespace: ns, Kind: registry.KindProvider, Name: name, System: ""},
		version,
	)
}

// releaseAt returns the release of version at addr, with the errors of Release.
func (s *Service) releaseAt(ctx context.Context, addr registry.Address, version string) (registry.Release, error) {
	if err := addr.Validate(); err != nil {
		return registry.Release{}, fmt.Errorf("check the address: %w", err)
	}

	if _, err := s.namespaces.GetNamespace(ctx, addr.Namespace); err != nil {
		return registry.Release{}, notFound(err)
	}

	rel, err := s.store.Get(ctx, addr, version)
	if err != nil {
		return registry.Release{}, fmt.Errorf("get release: %w", err)
	}

	return rel, nil
}

// SigningKey returns the trusted key that signed rel. It returns
// registry.ErrKeyNotFound when the namespace no longer trusts that key, and the
// registry then serves no download of the release (fail closed).
func (s *Service) SigningKey(ctx context.Context, rel registry.Release) (registry.TrustedKey, error) {
	keys, err := s.keys.Keys(ctx, rel.Address.Namespace)
	if err != nil {
		return registry.TrustedKey{}, fmt.Errorf("read the trusted keys: %w", err)
	}

	for _, k := range keys {
		if k.Key.Fingerprint == rel.KeyFingerprint {
			return k.Key, nil
		}
	}

	return registry.TrustedKey{}, registry.ErrKeyNotFound
}

// FileOf finds the file of rel with the SHA-256 sum: the checksums file, the
// signature file or an archive. It returns false when rel has no such file.
func FileOf(rel registry.Release, sum string) (File, bool) {
	var none File

	switch {
	case rel.SumsSHA256 == sum && rel.SumsFile != "":
		return File{Name: rel.SumsFile, SHA256: sum, Size: 0}, true
	case rel.SignatureSHA256 == sum && rel.SignatureFile != "":
		return File{Name: rel.SignatureFile, SHA256: sum, Size: 0}, true
	}

	for _, a := range rel.Archives {
		if a.SHA256 == sum {
			return File{Name: a.FileName, SHA256: sum, Size: a.Size}, true
		}
	}

	return none, false
}

// Open opens a file of a published release. The file must be a file of the
// release: a content key is never opened by its hash alone, so a hash of a
// staged file, of another release or of another namespace opens nothing. The
// caller closes the reader. It returns registry.ErrNotFound for a file that the
// release does not hold, and for a blob that is missing.
func (s *Service) Open(
	ctx context.Context, ns namespace.Name, name, version, sum string,
) (File, io.ReadCloser, error) {
	return s.openAt(
		ctx,
		registry.Address{Namespace: ns, Kind: registry.KindProvider, Name: name, System: ""},
		version,
		sum,
	)
}

// openAt opens a file of the release of version at addr, with the rules of Open.
func (s *Service) openAt(
	ctx context.Context, addr registry.Address, version, sum string,
) (File, io.ReadCloser, error) {
	rel, err := s.releaseAt(ctx, addr, version)
	if err != nil {
		return File{}, nil, err
	}

	file, ok := FileOf(rel, sum)
	if !ok {
		return File{}, nil, registry.ErrNotFound
	}

	rec, err := s.namespaces.GetNamespace(ctx, addr.Namespace)
	if err != nil {
		return File{}, nil, notFound(err)
	}

	view, err := s.scope(rec)
	if err != nil {
		return File{}, nil, err
	}

	key, err := registry.BlobKey(rec.ID, sum)
	if err != nil {
		return File{}, nil, fmt.Errorf("make the content key: %w", err)
	}

	r, err := view.Get(ctx, key)
	if errors.Is(err, blob.ErrNotFound) {
		return File{}, nil, registry.ErrNotFound
	}

	if err != nil {
		return File{}, nil, fmt.Errorf("open the file: %w", err)
	}

	return file, r, nil
}

// notFound maps a missing namespace to registry.ErrNotFound, and keeps every
// other error.
func notFound(err error) error {
	if errors.Is(err, namespace.ErrNotFound) {
		return registry.ErrNotFound
	}

	return fmt.Errorf("find namespace: %w", err)
}

// OpenFile opens a file of a published release by its SHA-256, as Open does,
// and returns its size. The size is zero for a checksums file and a signature
// file, which the release does not measure.
func (s *Service) OpenFile(
	ctx context.Context, ns namespace.Name, name, version, sum string,
) (int64, io.ReadCloser, error) {
	file, r, err := s.Open(ctx, ns, name, version, sum)
	if err != nil {
		return 0, nil, err
	}

	return file.Size, r, nil
}

// Providers returns one page of the names of the providers in ns that have at
// least one release, ordered by name, and the cursor of the next page. The
// cursor after is the cursor of the page before, and it is empty for the
// first page. The next cursor is empty when this page is the last one. It
// returns an empty list for a namespace with no provider, and
// registry.ErrNotFound for a namespace that does not exist.
func (s *Service) Providers(
	ctx context.Context, ns namespace.Name, after string, limit int,
) ([]string, string, error) {
	// Defense in depth: the HTTP handler gets ns from a checked route and the
	// namespace lookup below also refuses an unknown name; this check stays so
	// that a new caller cannot reach the store with a name that is not valid.
	if _, err := namespace.ParseName(string(ns)); err != nil {
		return nil, "", fmt.Errorf("%w: %w", registry.ErrNotFound, err)
	}

	if _, err := s.namespaces.GetNamespace(ctx, ns); err != nil {
		return nil, "", notFound(err)
	}

	addrs, next, err := s.store.Addresses(ctx, ns, registry.KindProvider, after, limit)
	if err != nil {
		return nil, "", fmt.Errorf("list providers: %w", err)
	}

	out := make([]string, 0, len(addrs))
	for i := range addrs {
		out = append(out, addrs[i].Name)
	}

	return out, next, nil
}
