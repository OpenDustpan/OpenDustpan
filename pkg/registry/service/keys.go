package service

import (
	"context"
	"fmt"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/registry"
)

// AddKey registers the public key in armored as a trusted signing key of ns.
// The server reads the key with the inspector and takes the fingerprint from
// the key, never from the caller. It stores the canonical form of the key. It
// returns the record. A key that the namespace holds already returns the
// record as stored. It returns registry.ErrBadKey, registry.ErrKeyExists,
// registry.ErrKeyLimit, namespace.ErrNotFound or namespace.ErrFenced.
func (s *Service) AddKey(
	ctx context.Context,
	c *Caller,
	ns namespace.Name,
	armored []byte,
) (registry.KeyRecord, error) {
	if err := c.check(); err != nil {
		return registry.KeyRecord{}, err
	}

	rec, err := s.namespaces.GetNamespace(ctx, ns)
	if err != nil {
		return registry.KeyRecord{}, fmt.Errorf("find namespace: %w", err)
	}

	key, err := s.inspector.Inspect(ctx, armored)
	if err != nil {
		return registry.KeyRecord{}, fmt.Errorf("inspect the key: %w", err)
	}

	k := registry.KeyRecord{AddedAt: s.now().UTC(), Namespace: rec.Name, Key: key, AddedBy: c.Principal.Subject}

	e := s.entry(c, rec.Name, identity.ActionRegistryManageKeys, rec.Name.String())
	e.Changes = []audit.Change{{Key: "registry.key.add", Old: nil, New: key.Fingerprint, Layer: "", TakesEffect: ""}}

	if err = s.keys.AddKey(ctx, k, rec.Fence(), e); err != nil {
		return registry.KeyRecord{}, fmt.Errorf("add key: %w", err)
	}

	stored, err := s.heldKey(ctx, rec.Name, key.Fingerprint)
	if err != nil {
		return registry.KeyRecord{}, err
	}

	return stored, nil
}

// RemoveKey removes the trusted signing key with fingerprint from ns. A release
// that the key signed stays in the store, and the registry stops serving its
// download, because the download needs the key that signed the release. It
// returns registry.ErrKeyNotFound, registry.ErrInvalid, namespace.ErrNotFound
// or namespace.ErrFenced.
func (s *Service) RemoveKey(ctx context.Context, c *Caller, ns namespace.Name, fingerprint string) error {
	if err := c.check(); err != nil {
		return err
	}

	if !registry.ValidFingerprint(fingerprint) {
		return fmt.Errorf("%w: key fingerprint", registry.ErrInvalid)
	}

	rec, err := s.namespaces.GetNamespace(ctx, ns)
	if err != nil {
		return fmt.Errorf("find namespace: %w", err)
	}

	e := s.entry(c, rec.Name, identity.ActionRegistryManageKeys, rec.Name.String())
	e.Changes = []audit.Change{{Key: "registry.key.remove", Old: &fingerprint, New: "", Layer: "", TakesEffect: ""}}

	if err = s.keys.RemoveKey(ctx, rec.Name, fingerprint, rec.Fence(), e); err != nil {
		return fmt.Errorf("remove key: %w", err)
	}

	return nil
}

// Keys lists the trusted signing keys of ns, ordered by fingerprint.
func (s *Service) Keys(ctx context.Context, ns namespace.Name) ([]registry.KeyRecord, error) {
	if _, err := s.namespaces.GetNamespace(ctx, ns); err != nil {
		return nil, fmt.Errorf("find namespace: %w", err)
	}

	keys, err := s.keys.Keys(ctx, ns)
	if err != nil {
		return nil, fmt.Errorf("list keys: %w", err)
	}

	return keys, nil
}

// heldKey returns the key of ns with fingerprint.
func (s *Service) heldKey(ctx context.Context, ns namespace.Name, fingerprint string) (registry.KeyRecord, error) {
	keys, err := s.keys.Keys(ctx, ns)
	if err != nil {
		return registry.KeyRecord{}, fmt.Errorf("read the key: %w", err)
	}

	for _, k := range keys {
		if k.Key.Fingerprint == fingerprint {
			return k, nil
		}
	}

	return registry.KeyRecord{}, registry.ErrKeyNotFound
}
