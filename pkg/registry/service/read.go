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

// Release returns the release of version of the provider name in ns. It returns
// registry.ErrNotFound for a namespace, a provider or a version that does not
// exist.
func (s *Service) Release(ctx context.Context, ns namespace.Name, name, version string) (registry.Release, error) {
	addr := registry.Address{Namespace: ns, Kind: registry.KindProvider, Name: name, System: ""}
	if err := addr.Validate(); err != nil {
		return registry.Release{}, fmt.Errorf("check the address: %w", err)
	}

	if _, err := s.namespaces.GetNamespace(ctx, ns); err != nil {
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

	return File{}, false
}

// Open opens a file of a published release. The file must be a file of the
// release: a content key is never opened by its hash alone, so a hash of a
// staged file, of another release or of another namespace opens nothing. The
// caller closes the reader. It returns registry.ErrNotFound for a file that the
// release does not hold, and for a blob that is missing.
func (s *Service) Open(
	ctx context.Context, ns namespace.Name, name, version, sum string,
) (File, io.ReadCloser, error) {
	rel, err := s.Release(ctx, ns, name, version)
	if err != nil {
		return File{}, nil, err
	}

	file, ok := FileOf(rel, sum)
	if !ok {
		return File{}, nil, registry.ErrNotFound
	}

	rec, err := s.namespaces.GetNamespace(ctx, ns)
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
