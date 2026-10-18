// Package opsmem holds the namespaces, fences and audit log in memory that
// the reference adapters of the operations ports share in their tests. One
// mutex stands for the transaction of each write. A write checks its fence
// and runs its change under the mutex, and appends its audit entries only
// when the change succeeds, so a failed write changes nothing.
package opsmem

import (
	"context"
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

// Write runs change as one transaction. When fence names a namespace, it
// first checks the fence and returns namespace.ErrFenced for a stale one.
// When change returns nil, Write appends entries, each to the scope of
// its namespace. A zero fence skips the check, for a write outside a
// namespace.
func (d *Domain) Write(fence namespace.Fence, entries []audit.Entry, change func() error) error {
	return d.WriteEntries(fence, func() ([]audit.Entry, error) {
		return entries, change()
	})
}

// WriteEntries is Write for a change that decides its audit entries
// itself, for example a create that appends an entry only when the row
// is new.
func (d *Domain) WriteEntries(fence namespace.Fence, change func() ([]audit.Entry, error)) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if fence.Namespace != "" {
		v, ok := d.fences[fence.Namespace]
		if !ok {
			return fmt.Errorf("write: %w", namespace.ErrNotFound)
		}

		if v != fence.FailoverVersion {
			return fmt.Errorf("write: %w", namespace.ErrFenced)
		}
	}

	entries, err := change()
	if err != nil {
		return err
	}

	for i := range entries {
		d.audit[entries[i].Namespace] = append(d.audit[entries[i].Namespace], entries[i])
	}

	return nil
}

// Read runs read under the mutex of the domain.
func (d *Domain) Read(read func()) {
	d.mu.Lock()
	defer d.mu.Unlock()

	read()
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
	}
}
