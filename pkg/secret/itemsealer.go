package secret

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"time"

	"opendustpan.org/opendustpan/pkg/namespace"
)

// NamespaceReader reads a namespace for its ID, which names its blob scope.
// namespace.Store and every SQL store adapter implement it.
type NamespaceReader interface {
	GetNamespace(ctx context.Context, name namespace.Name) (namespace.Namespace, error)
}

// ItemSealerConfig holds the dependencies of an ItemKeySealer.
type ItemSealerConfig struct {
	Index ItemKeyIndex
	Blobs *ItemKeyBlobs
	// Wrapper holds the key-encryption key. It may be nil when no key is
	// configured; NewItemKey, SealContent and OpenContent then fail with
	// ErrKeyNotConfigured.
	Wrapper    Wrapper
	Namespaces NamespaceReader
	Now        func() time.Time
}

// ItemKeySealer is the ItemSealer over the key records of an ItemKeyIndex and
// the wrapped key blobs of ItemKeyBlobs. Each item has its own random key.
// The Wrapper wraps it with the associated data of the item, and the content
// seals as a stream under a key that the stream derives from the item key.
type ItemKeySealer struct {
	index      ItemKeyIndex
	blobs      *ItemKeyBlobs
	wrapper    Wrapper
	namespaces NamespaceReader
	now        func() time.Time
}

var _ ItemSealer = (*ItemKeySealer)(nil)

// NewItemKeySealer returns an ItemKeySealer. Index, Blobs and Namespaces are
// required.
func NewItemKeySealer(cfg ItemSealerConfig) (*ItemKeySealer, error) {
	switch {
	case cfg.Index == nil:
		return nil, fmt.Errorf("%w: item key index", ErrMissingDependency)
	case cfg.Blobs == nil:
		return nil, fmt.Errorf("%w: item key blobs", ErrMissingDependency)
	case cfg.Namespaces == nil:
		return nil, fmt.Errorf("%w: namespace reader", ErrMissingDependency)
	}

	now := cfg.Now
	if now == nil {
		now = time.Now
	}

	return &ItemKeySealer{
		index: cfg.Index, blobs: cfg.Blobs, wrapper: cfg.Wrapper, namespaces: cfg.Namespaces, now: now,
	}, nil
}

// checkRef validates ref again. A caller builds a reference with NewItemRef,
// but a struct literal skips that.
//
// Defense in depth: the index and the blob view check the parts of a
// reference too. This check stays so that a reference that was not built with
// NewItemRef never reaches the associated data.
func checkRef(ref ItemRef) error {
	if _, err := NewItemRef(ref.Scope, ref.Kind, ref.Item); err != nil {
		return err
	}

	return nil
}

// NewItemKey implements ItemSealer. It writes the wrapped key blob first and
// the record after it. A second call for the same item returns the stored
// record, so two writers of the first save agree on one key. When another
// writer stored its record first, the blob of this call is an orphan, and
// NewItemKey deletes it.
func (s *ItemKeySealer) NewItemKey(ctx context.Context, ref ItemRef, fence namespace.Fence) (ItemKey, error) {
	if err := checkRef(ref); err != nil {
		return ItemKey{}, err
	}

	if s.wrapper == nil {
		return ItemKey{}, fmt.Errorf("new item key of %s: %w", ref, ErrKeyNotConfigured)
	}

	// Defense in depth: the index compares the namespace of the record with
	// the fence too. This check stays so that no wrapped key is written to the
	// blob store under a fence of another namespace.
	if ref.Scope.Namespace != fence.Namespace {
		return ItemKey{}, fmt.Errorf("%w: item names %q, fence names %q", namespace.ErrFenced,
			ref.Scope.Namespace, fence.Namespace)
	}

	existing, err := s.index.GetItemKey(ctx, ref)

	switch {
	case err == nil || errors.Is(err, ErrShredded):
		return existing, err
	case !errors.Is(err, ErrKeyNotFound):
		return ItemKey{}, fmt.Errorf("new item key of %s: %w", ref, err)
	default:
	}

	return s.createItemKey(ctx, ref, fence)
}

// SealContent implements ItemSealer.
func (s *ItemKeySealer) SealContent(
	ctx context.Context, ref ItemRef, binding []byte, r io.Reader,
) (io.Reader, error) {
	if err := checkBinding(binding); err != nil {
		return nil, err
	}

	key, err := s.openItemKey(ctx, ref)
	if err != nil {
		return nil, err
	}

	defer clear(key)

	return sealStreamWith(key, ref, binding, r)
}

// OpenContent implements ItemSealer. It reads the record before any blob, so
// a shredded key answers ErrShredded at once.
func (s *ItemKeySealer) OpenContent(
	ctx context.Context, ref ItemRef, binding []byte, r io.Reader,
) (io.Reader, error) {
	if err := checkBinding(binding); err != nil {
		return nil, err
	}

	key, err := s.openItemKey(ctx, ref)
	if err != nil {
		return nil, err
	}

	return openStreamWith(key, ref, binding, r, true), nil
}

// openItemKey returns the raw item key of ref. The caller clears it. It reads
// the record first and returns ErrShredded for a shredded record and
// ErrKeyNotFound for no record or no blob. A key that does not unwrap for ref
// gives ErrDecrypt.
func (s *ItemKeySealer) openItemKey(ctx context.Context, ref ItemRef) ([]byte, error) {
	if err := checkRef(ref); err != nil {
		return nil, err
	}

	rec, err := s.index.GetItemKey(ctx, ref)
	if err != nil {
		return nil, fmt.Errorf("item key of %s: %w", ref, err)
	}

	// Defense in depth: the index looked the record up by the reference. This
	// check stays so that a record of another item never opens the content of
	// this one.
	if rec.Ref != ref {
		return nil, fmt.Errorf("%w: the record is not the record of %s", ErrDecrypt, ref)
	}

	if s.wrapper == nil {
		return nil, fmt.Errorf("item key of %s: %w", ref, ErrKeyNotConfigured)
	}

	if rec.WrapperID != s.wrapper.ID() {
		return nil, fmt.Errorf("%w: key of %s is wrapped by %q, server holds %q",
			ErrDecrypt, ref, rec.WrapperID, s.wrapper.ID())
	}

	ns, err := s.namespaces.GetNamespace(ctx, ref.Scope.Namespace)
	if err != nil {
		return nil, fmt.Errorf("item key of %s: %w", ref, err)
	}

	keys, err := s.blobs.Versions(ctx, ns.ID, rec.ID)
	if err != nil {
		return nil, fmt.Errorf("item key blobs of %s: %w", ref, err)
	}

	if len(keys) == 0 {
		return nil, fmt.Errorf("item key blob of %s: %w", ref, ErrKeyNotFound)
	}

	wrapped, err := s.blobs.Get(ctx, ns.ID, keys[len(keys)-1])
	if err != nil {
		return nil, fmt.Errorf("item key blob of %s: %w", ref, err)
	}

	key, err := s.wrapper.Unwrap(ctx, wrapped, ref.aad())
	if err != nil {
		return nil, fmt.Errorf("unwrap item key of %s: %w", ref, err)
	}

	return key, nil
}

// createItemKey makes a new item key for ref and stores its blob and its
// record.
func (s *ItemKeySealer) createItemKey(ctx context.Context, ref ItemRef, fence namespace.Fence) (ItemKey, error) {
	ns, err := s.namespaces.GetNamespace(ctx, ref.Scope.Namespace)
	if err != nil {
		return ItemKey{}, fmt.Errorf("new item key of %s: %w", ref, err)
	}

	key := make([]byte, dataKeyBytes)
	defer clear(key)

	if _, err = rand.Read(key); err != nil {
		return ItemKey{}, fmt.Errorf("generate item key: %w", err)
	}

	wrapped, err := s.wrapper.Wrap(ctx, key, ref.aad())
	if err != nil {
		return ItemKey{}, fmt.Errorf("wrap item key of %s: %w", ref, err)
	}

	id := NewItemKeyID()

	blobKey, err := s.blobs.Key(ns.ID, id, 1)
	if err != nil {
		return ItemKey{}, fmt.Errorf("item key blob of %s: %w", ref, err)
	}

	if err = s.blobs.Put(ctx, ns.ID, blobKey, wrapped); err != nil {
		return ItemKey{}, fmt.Errorf("store item key blob of %s: %w", ref, err)
	}

	stored, err := s.index.CreateItemKey(ctx, ItemKey{
		CreatedAt: s.now().UTC(), ShreddedAt: time.Time{}, Ref: ref, ID: id, WrapperID: s.wrapper.ID(),
	}, fence)
	if err != nil || stored.ID != id {
		// The record was not stored, or another writer's record won. The
		// blob of this call is an orphan. The orphan cleanup also deletes it
		// after a grace period, so a failed delete is not an error.
		_ = s.blobs.Delete(context.WithoutCancel(ctx), ns.ID, blobKey) //nolint:errcheck // See above.
	}

	if err != nil {
		return stored, fmt.Errorf("store item key record of %s: %w", ref, err)
	}

	return stored, nil
}
