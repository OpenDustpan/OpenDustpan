package objectcontext

import (
	"context"
	"errors"
	"fmt"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// ErrMissingDependency reports a Config without a required port.
var ErrMissingDependency = errors.New("missing dependency")

// WriterConfig holds the dependencies of a Writer. A nil Now uses time.Now.
type WriterConfig struct {
	Store      Store
	Namespaces namespace.Store
	Now        func() time.Time
}

// Writer implements the writes of API context. It reads the fence of the
// namespace before each write, so a write carries the failover version that
// the namespace has now.
type Writer struct {
	store      Store
	namespaces namespace.Store
	now        func() time.Time
}

// NewWriter returns a Writer. It returns ErrMissingDependency when a port
// is missing.
func NewWriter(cfg WriterConfig) (*Writer, error) {
	if cfg.Store == nil || cfg.Namespaces == nil {
		return nil, fmt.Errorf("%w: context store and namespace store", ErrMissingDependency)
	}

	now := cfg.Now
	if now == nil {
		now = time.Now
	}

	return &Writer{store: cfg.Store, namespaces: cfg.Namespaces, now: now}, nil
}

// Set applies p to the API context of object and appends e, which names the
// caller. by is the subject of the principal that makes the change. The
// audit time is the injected clock. It returns the new API entry.
func (s *Writer) Set(ctx context.Context, object Ref, p Patch, by string, e audit.Entry) (Entry, error) {
	fence, err := s.fence(ctx, object.Namespace)
	if err != nil {
		return Entry{}, err
	}

	e.At = s.now().UTC()

	entry, err := s.store.SetAPI(ctx, object, p, by, fence, e)
	if err != nil {
		return Entry{}, fmt.Errorf("set context of %s: %w", object.Key(), err)
	}

	return entry, nil
}

// Clear deletes the API context of object and appends e. It returns
// ErrNotFound when the object has no API context.
func (s *Writer) Clear(ctx context.Context, object Ref, e audit.Entry) error {
	fence, err := s.fence(ctx, object.Namespace)
	if err != nil {
		return err
	}

	e.At = s.now().UTC()

	if err := s.store.DeleteAPI(ctx, object, fence, e); err != nil {
		return fmt.Errorf("clear context of %s: %w", object.Key(), err)
	}

	return nil
}

func (s *Writer) fence(ctx context.Context, ns namespace.Name) (namespace.Fence, error) {
	n, err := s.namespaces.GetNamespace(ctx, ns)
	if err != nil {
		return namespace.Fence{}, fmt.Errorf("read namespace %s: %w", ns, err)
	}

	return n.Fence(), nil
}
