// Package opsmem holds the namespaces, fences and audit log in memory that
// the reference adapters of the operations ports share in their tests. One
// mutex stands for the transaction of each write. A write checks its fence
// and runs its change under the mutex, and appends its audit entries only
// when the change succeeds, so a failed write changes nothing.
package opsmem

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// Domain is the shared part of a reference adapter in memory.
type Domain struct {
	mu     sync.Mutex
	fences map[namespace.Name]int64
	audit  map[namespace.Name][]audit.Entry
}

// New returns an empty domain.
func New() *Domain {
	return &Domain{
		mu:     sync.Mutex{},
		fences: map[namespace.Name]int64{},
		audit:  map[namespace.Name][]audit.Entry{},
	}
}

// CreateNamespace creates the namespace name and returns its fence.
func (d *Domain) CreateNamespace(_ context.Context, name namespace.Name) (namespace.Fence, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	d.fences[name] = 1

	return namespace.Fence{Namespace: name, FailoverVersion: 1}, nil
}

// BumpFence increments the failover version of the namespace of fence, as
// a failover does, and returns the new fence.
func (d *Domain) BumpFence(_ context.Context, fence namespace.Fence) (namespace.Fence, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	v, ok := d.fences[fence.Namespace]
	if !ok {
		return namespace.Fence{}, fmt.Errorf("bump fence: %w", namespace.ErrNotFound)
	}

	d.fences[fence.Namespace] = v + 1

	return namespace.Fence{Namespace: fence.Namespace, FailoverVersion: v + 1}, nil
}

// AuditEntries returns the audit entries of scope, in the order of their
// append.
func (d *Domain) AuditEntries(_ context.Context, scope namespace.Name) ([]audit.Entry, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	return slices.Clone(d.audit[scope]), nil
}

// ErrScope is the error of a write whose fence does not belong to the
// namespace of its row: a zero fence for a row in a namespace, a fence for
// a row in the server scope, or the fence of another namespace. An adapter
// translates it into the sentinel of its port, for example ErrInvalid or
// ErrNotFound.
var ErrScope = errors.New("the fence does not belong to the namespace of the row")

// Scope returns a scope function for a row in the namespace ns that is
// known before the write, for example the row of a create.
func Scope(ns namespace.Name) func() (namespace.Name, error) {
	return func() (namespace.Name, error) { return ns, nil }
}

// Write runs change as one transaction. It first calls scope, under the
// mutex, to get the namespace of the row; an error of scope, for example
// an unknown row, stops the write. Then it checks fence against that
// namespace: a row in audit.ServerScope needs a zero fence, every other
// row needs the fence of its own namespace (ErrScope), at the current
// failover version (namespace.ErrFenced). When change returns nil, Write
// appends entries to the scope of the row. An entry whose Namespace is not
// the scope of the row stops the write with ErrScope and changes nothing,
// so a write cannot append to the audit chain of another namespace.
func (d *Domain) Write(
	fence namespace.Fence,
	scope func() (namespace.Name, error),
	entries []audit.Entry,
	change func() error,
) error {
	return d.WriteEntries(fence, scope, func() ([]audit.Entry, func() error, error) {
		return entries, change, nil
	})
}

// WriteEntries is Write for a change that decides its audit entries
// itself, for example a create that appends an entry only when the row
// is new. plan returns the entries and the change; it must not change a
// row itself. WriteEntries checks the entries before it runs the change,
// so a refused entry changes nothing.
func (d *Domain) WriteEntries(
	fence namespace.Fence,
	scope func() (namespace.Name, error),
	plan func() ([]audit.Entry, func() error, error),
) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	ns, err := scope()
	if err != nil {
		return err
	}

	err = d.check(fence, ns)
	if err != nil {
		return err
	}

	entries, change, err := plan()
	if err != nil {
		return err
	}

	for i := range entries {
		if entries[i].Namespace != ns {
			return fmt.Errorf("write: audit entry %d: %w", i, ErrScope)
		}
	}

	if change != nil {
		err = change()
		if err != nil {
			return err
		}
	}

	d.audit[ns] = append(d.audit[ns], entries...)

	return nil
}

// Read runs read under the mutex of the domain.
func (d *Domain) Read(read func()) {
	d.mu.Lock()
	defer d.mu.Unlock()

	read()
}

func (d *Domain) check(fence namespace.Fence, ns namespace.Name) error {
	if ns == audit.ServerScope {
		if fence.Namespace != "" || fence.FailoverVersion != 0 {
			return fmt.Errorf("write: %w", ErrScope)
		}

		return nil
	}

	if fence.Namespace == "" || fence.Namespace != ns {
		return fmt.Errorf("write: %w", ErrScope)
	}

	v, ok := d.fences[fence.Namespace]
	if !ok {
		return fmt.Errorf("write: %w", namespace.ErrNotFound)
	}

	if v != fence.FailoverVersion {
		return fmt.Errorf("write: %w", namespace.ErrFenced)
	}

	return nil
}

// Entry returns a valid audit entry of the kind change, for a suite that
// passes one to a write.
func Entry(scope namespace.Name, subject, action, object string, at time.Time) audit.Entry {
	return audit.Entry{
		At:           at,
		Namespace:    scope,
		Subject:      subject,
		IDPSubject:   "",
		TokenID:      "",
		Action:       action,
		Object:       object,
		Version:      "",
		ValuePath:    "",
		ValueCount:   nil,
		Changes:      nil,
		SourceIP:     "192.0.2.1",
		UserAgent:    "suite",
		Channel:      audit.ChannelAPI,
		Kind:         audit.KindChange,
		Outcome:      audit.OutcomeSuccess,
		RequestID:    "req-suite",
		ObjectID:     "",
		PrevHash:     "",
		Hash:         "",
		Sequence:     0,
		FenceVersion: 0,
		Reason:       "",
	}
}
