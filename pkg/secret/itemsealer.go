package secret

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"opendustpan.org/opendustpan/pkg/blob"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// itemKeyBlobVersion is the version of the blob that NewItemKey writes. The
// key-encryption key wraps it directly.
const itemKeyBlobVersion = 1

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
// The Wrapper wraps it with the associated data of the item and of the ID of
// its record, and the content seals as a stream under a key that the stream
// derives from the item key. The associated data of each chunk holds the item
// and the ID of the record too.
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
// the record after it, through tx, so the record commits with the item in the
// transaction of the caller. A second call for the same item returns the
// stored record, so two writers of the first save agree on one key. When
// another writer stored its record first, or the record is not stored, the
// blob of this call is an orphan, and NewItemKey deletes it. When the caller
// rolls back after NewItemKey returns, the blob is an orphan that the orphan
// cleanup deletes after a grace period.
func (s *ItemKeySealer) NewItemKey(
	ctx context.Context, tx ItemKeyTx, ref ItemRef, fence namespace.Fence,
) (ItemKey, error) {
	if err := checkRef(ref); err != nil {
		return ItemKey{}, err
	}

	if tx == nil {
		return ItemKey{}, fmt.Errorf("%w: item key transaction", ErrMissingDependency)
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

	existing, err := tx.GetItemKey(ctx, ref)

	switch {
	case err == nil || errors.Is(err, ErrShredded):
		return existing, err
	case !errors.Is(err, ErrKeyNotFound):
		return ItemKey{}, fmt.Errorf("new item key of %s: %w", ref, err)
	default:
	}

	return s.createItemKey(ctx, tx, ref)
}

// SealContent implements ItemSealer.
func (s *ItemKeySealer) SealContent(
	ctx context.Context, ref ItemRef, binding []byte, r io.Reader,
) (io.Reader, error) {
	if err := checkBinding(binding); err != nil {
		return nil, err
	}

	key, id, err := s.openItemKey(ctx, ref)
	if err != nil {
		return nil, err
	}

	defer clear(key)

	return sealStreamWith(key, itemContent{ref: ref, id: id}, binding, r)
}

// OpenContent implements ItemSealer. It reads the record before any blob, so
// a shredded key answers ErrShredded at once. The reader holds the item key
// until it reads the stream header or until Close, so the caller closes it.
func (s *ItemKeySealer) OpenContent(
	ctx context.Context, ref ItemRef, binding []byte, r io.Reader,
) (io.ReadCloser, error) {
	if err := checkBinding(binding); err != nil {
		return nil, err
	}

	key, id, err := s.openItemKey(ctx, ref)
	if err != nil {
		return nil, err
	}

	return openStreamWith(key, itemContent{ref: ref, id: id}, binding, r, true), nil
}

// SealContents implements ItemSealer. It checks every binding before it reads
// the key.
func (s *ItemKeySealer) SealContents(ctx context.Context, ref ItemRef, parts []ContentPart) ([][]byte, error) {
	for i := range parts {
		if err := checkBinding(parts[i].Binding); err != nil {
			return nil, fmt.Errorf("part %d: %w", i, err)
		}
	}

	key, id, err := s.openItemKey(ctx, ref)
	if err != nil {
		return nil, err
	}

	defer clear(key)

	content := itemContent{ref: ref, id: id}
	out := make([][]byte, 0, len(parts))

	for i := range parts {
		r, err := sealStreamWith(key, content, parts[i].Binding, bytes.NewReader(parts[i].Data))
		if err != nil {
			return nil, fmt.Errorf("seal part %d: %w", i, err)
		}

		sealed, err := io.ReadAll(r)
		if err != nil {
			return nil, fmt.Errorf("seal part %d: %w", i, err)
		}

		out = append(out, sealed)
	}

	return out, nil
}

// OpenContents implements ItemSealer. It reads the record before any blob, so
// a shredded key answers ErrShredded at once. It reads each part to its end,
// and clears the key after the last part.
func (s *ItemKeySealer) OpenContents(ctx context.Context, ref ItemRef, parts []ContentPart) ([][]byte, error) {
	for i := range parts {
		if err := checkBinding(parts[i].Binding); err != nil {
			return nil, fmt.Errorf("part %d: %w", i, err)
		}
	}

	key, id, err := s.openItemKey(ctx, ref)
	if err != nil {
		return nil, err
	}

	defer clear(key)

	content := itemContent{ref: ref, id: id}
	out := make([][]byte, 0, len(parts))

	for i := range parts {
		plain, err := openPart(key, content, parts[i])
		if err != nil {
			return nil, fmt.Errorf("open part %d: %w", i, err)
		}

		out = append(out, plain)
	}

	return out, nil
}

// openPart opens one part of a batch and reads it to the end. The reader
// clears the key that it gets, so the part gets its own copy, and Close
// clears that copy also when the read fails before the header.
func openPart(key []byte, content itemContent, part ContentPart) ([]byte, error) {
	rd := openStreamWith(bytes.Clone(key), content, part.Binding, bytes.NewReader(part.Data), true)
	plain, err := io.ReadAll(rd)

	return plain, errors.Join(err, rd.Close())
}

// openItemKey returns the raw item key of ref and the ID of its record. The
// caller clears the key. It reads the record first and returns ErrShredded for
// a shredded record and ErrKeyNotFound for no record or no blob. It reads only
// the blob key that the record names, with its version and its attempt part,
// and it compares the size and the MD5 of the blob with the record before it
// unwraps the key. A blob that does not match gives ErrItemKeyBlobMismatch. A
// key that does not unwrap for ref and the record ID gives ErrDecrypt.
func (s *ItemKeySealer) openItemKey(ctx context.Context, ref ItemRef) ([]byte, ItemKeyID, error) {
	if err := checkRef(ref); err != nil {
		return nil, "", err
	}

	rec, err := s.index.GetItemKey(ctx, ref)
	if err != nil {
		return nil, "", fmt.Errorf("item key of %s: %w", ref, err)
	}

	// Defense in depth: the index looked the record up by the reference. This
	// check stays so that a record of another item never opens the content of
	// this one.
	if rec.Ref != ref {
		return nil, "", fmt.Errorf("%w: the record is not the record of %s", ErrDecrypt, ref)
	}

	// Defense in depth: the store adapter and the blob view check the record
	// ID too, and the associated data puts a length before each part. This
	// check stays so that a record ID that is not valid never reaches the
	// associated data.
	if _, idErr := ParseItemKeyID(string(rec.ID)); idErr != nil {
		return nil, "", fmt.Errorf("%w: the record of %s has an ID that is not valid", ErrDecrypt, ref)
	}

	if s.wrapper == nil {
		return nil, "", fmt.Errorf("item key of %s: %w", ref, ErrKeyNotConfigured)
	}

	// Defense in depth: unwrapKey checks the wrapper ID too. This check stays
	// so that a key of another wrapper reads no namespace and no blob.
	if rec.WrapperID != s.wrapper.ID() {
		return nil, "", fmt.Errorf("%w: key of %s is wrapped by %q, server holds %q",
			ErrDecrypt, ref, rec.WrapperID, s.wrapper.ID())
	}

	ns, err := s.namespaces.GetNamespace(ctx, ref.Scope.Namespace)
	if err != nil {
		return nil, "", fmt.Errorf("item key of %s: %w", ref, err)
	}

	// The record names the blob version and the attempt part. A blob of
	// another version or another attempt, for example a stray or planted
	// blob, is never read.
	blobKey, err := s.blobs.Key(ns.ID, rec.ID, rec.Blob.Version, rec.Blob.Attempt)
	if err != nil {
		return nil, "", fmt.Errorf("item key blob of %s: %w", ref, err)
	}

	// Get compares the size and the MD5 of the blob with the record before it
	// returns the bytes, so a replaced blob never reaches the unwrap.
	wrapped, err := s.blobs.Get(ctx, ns.ID, blobKey, rec.Blob)
	if err != nil {
		return nil, "", fmt.Errorf("item key blob of %s: %w", ref, err)
	}

	// The associated data holds the record ID, so a wrapped key that was
	// put under the blob key of another record does not unwrap.
	key, err := unwrapKey(
		ctx,
		s.wrapper,
		sealedKey{wrapperID: rec.WrapperID, ciphertext: wrapped},
		itemKeyAAD(ref, rec.ID),
		"item key of "+ref.String(),
	)
	if err != nil {
		return nil, "", err
	}

	return key, rec.ID, nil
}

// createItemKey makes a new item key for ref and stores its blob and, through
// tx, its record.
func (s *ItemKeySealer) createItemKey(ctx context.Context, tx ItemKeyTx, ref ItemRef) (ItemKey, error) {
	ns, err := tx.GetNamespace(ctx, ref.Scope.Namespace)
	if err != nil {
		return ItemKey{}, fmt.Errorf("new item key of %s: %w", ref, err)
	}

	// The ID comes first: the associated data of the wrapped key holds it.
	id := NewItemKeyID()

	wrapped, err := wrapNewKey(ctx, s.wrapper, itemKeyAAD(ref, id), "item key of "+ref.String())
	if err != nil {
		return ItemKey{}, err
	}

	// Each write has its own attempt part, so this write never replaces a
	// blob of another write, also on a store that does not refuse a second
	// write to a key. The record names the attempt part.
	blobKey, attempt, err := s.blobs.NewKey(ns.ID, id, itemKeyBlobVersion)
	if err != nil {
		return ItemKey{}, fmt.Errorf("item key blob of %s: %w", ref, err)
	}

	if err = s.blobs.Put(ctx, ns.ID, blobKey, wrapped); err != nil {
		return ItemKey{}, fmt.Errorf("store item key blob of %s: %w", ref, err)
	}

	digest := blob.DigestBytes(wrapped)

	stored, err := tx.CreateItemKey(ctx, ItemKey{
		CreatedAt:  s.now().UTC(),
		ShreddedAt: time.Time{},
		Ref:        ref,
		ID:         id,
		WrapperID:  s.wrapper.ID(),
		Blob: ItemKeyBlobInfo{
			Version: itemKeyBlobVersion,
			Attempt: attempt,
			Size:    digest.Size,
			MD5:     digest.MD5,
		},
	})
	if err != nil || stored.ID != id {
		// The record was not stored, or another writer's record won. The
		// blob of this call is an orphan. Its key is unique to this call, so
		// the delete never removes a blob that a record names. The orphan
		// cleanup also deletes it after a grace period, so a failed delete is
		// not an error.
		_ = s.blobs.Delete(context.WithoutCancel(ctx), ns.ID, blobKey) //nolint:errcheck // See above.
	}

	if err != nil {
		return stored, fmt.Errorf("store item key record of %s: %w", ref, err)
	}

	return stored, nil
}
