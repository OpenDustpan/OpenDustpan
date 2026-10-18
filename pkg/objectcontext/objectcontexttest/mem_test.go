package objectcontexttest_test

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/graph"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/objectcontext"
	"opendustpan.org/opendustpan/pkg/objectcontext/objectcontexttest"
)

// memStore is the reference adapter of objectcontext.Store. It runs the
// suite, so each case of the suite runs before any SQL adapter exists.
type memStore struct {
	fences  map[namespace.Name]int64
	api     map[string]objectcontext.Entry
	output  map[string]objectcontext.Entry
	seq     map[string]int64
	cols    map[string]bool
	units   map[string]bool
	audited []audit.Entry
	mu      sync.Mutex
}

func newMem() *memStore {
	return &memStore{
		fences:  map[namespace.Name]int64{},
		api:     map[string]objectcontext.Entry{},
		output:  map[string]objectcontext.Entry{},
		seq:     map[string]int64{},
		cols:    map[string]bool{},
		units:   map[string]bool{},
		audited: nil,
		mu:      sync.Mutex{},
	}
}

func (m *memStore) Entries(_ context.Context, objects []objectcontext.Ref) ([]objectcontext.Entry, error) {
	if len(objects) == 0 || len(objects) > objectcontext.MaxBatch {
		return nil, fmt.Errorf("%w: batch of %d", objectcontext.ErrInvalidObject, len(objects))
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	var out []objectcontext.Entry

	for _, o := range objects {
		if err := o.Validate(); err != nil {
			return nil, err
		}

		for _, src := range []map[string]objectcontext.Entry{m.api, m.output} {
			if e, ok := src[o.Key()]; ok {
				out = append(out, e)
			}
		}
	}

	return out, nil
}

//nolint:revive // The signature of the objectcontext.Store port.
func (m *memStore) SetAPI(
	_ context.Context, object objectcontext.Ref, p objectcontext.Patch, by string, fence namespace.Fence, e audit.Entry,
) (objectcontext.Entry, error) {
	if err := object.Validate(); err != nil {
		return objectcontext.Entry{}, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.checkFence(fence); err != nil {
		return objectcontext.Entry{}, err
	}

	if object.Kind == objectcontext.KindCollection && !m.cols[object.Key()] {
		return objectcontext.Entry{}, fmt.Errorf("%w: no collection %q", objectcontext.ErrInvalidObject, object.Name)
	}

	if err := m.checkUnit(object); err != nil {
		return objectcontext.Entry{}, err
	}

	var output *objectcontext.Entry
	if o, ok := m.output[object.Key()]; ok {
		output = &o
	}

	if err := objectcontext.CheckPatch(output, p); err != nil {
		return objectcontext.Entry{}, err
	}

	before := m.api[object.Key()].Fields

	fields, err := p.Apply(before)
	if err != nil {
		return objectcontext.Entry{}, err
	}

	e.Changes = objectcontext.ChangesOf(before, fields, p.Fields())

	entry := objectcontext.Entry{
		Object: object, Source: objectcontext.SourceAPI, Fields: fields, UpdatedAt: e.At, UpdatedBy: by,
		Readme:  "",
		Item:    "",
		Warning: "",
	}
	m.api[object.Key()] = entry
	m.audited = append(m.audited, e)

	return entry, nil
}

func (m *memStore) DeleteAPI(_ context.Context, object objectcontext.Ref, fence namespace.Fence, e audit.Entry) error {
	if err := object.Validate(); err != nil {
		return err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.checkFence(fence); err != nil {
		return err
	}

	if err := m.checkUnit(object); err != nil {
		return err
	}

	prev, ok := m.api[object.Key()]
	if !ok {
		return objectcontext.ErrNotFound
	}

	var cleared objectcontext.Fields

	e.Changes = objectcontext.ChangesOf(prev.Fields, cleared, prev.Fields.Set())

	delete(m.api, object.Key())
	m.audited = append(m.audited, e)

	return nil
}

func (m *memStore) ReplaceOutput(
	_ context.Context, unit objectcontext.Ref, item objectcontext.OutputItem, entries []objectcontext.Entry,
	fence namespace.Fence,
) error {
	if err := unit.Validate(); err != nil || unit.Kind != objectcontext.KindUnit {
		return fmt.Errorf("%w: %s", objectcontext.ErrInvalidObject, unit.Key())
	}

	for i := range entries {
		e := &entries[i]
		if err := e.Fields.Validate(); err != nil {
			return err
		}

		if e.Object.Unit != unit.Unit || e.Source != objectcontext.SourceOutput {
			return fmt.Errorf("%w: entry %s", objectcontext.ErrInvalidObject, e.Object.Key())
		}
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.checkFence(fence); err != nil {
		return err
	}

	if err := m.checkUnit(unit); err != nil {
		return err
	}

	if m.seq[unit.Key()] > item.Seq {
		return nil
	}

	dropUnitEntries(m.output, unit)
	m.seq[unit.Key()] = item.Seq

	for i := range entries {
		m.output[entries[i].Object.Key()] = entries[i]
	}

	return nil
}

func (m *memStore) DropUnit(_ context.Context, unit objectcontext.Ref, fence namespace.Fence) error {
	if err := unit.Validate(); err != nil {
		return err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.checkFence(fence); err != nil {
		return err
	}

	dropUnitEntries(m.api, unit)
	dropUnitEntries(m.output, unit)
	delete(m.seq, unit.Key())

	return nil
}

func (m *memStore) ByOwner(
	_ context.Context, ns namespace.Name, owner, after string, limit int,
) ([]objectcontext.Entry, string, error) {
	if _, err := objectcontext.ParseOwner(owner); err != nil {
		return nil, "", err
	}

	if limit < 1 || limit > objectcontext.MaxBatch {
		return nil, "", fmt.Errorf("%w: limit %d", objectcontext.ErrInvalidContext, limit)
	}

	if after != "" && !strings.HasSuffix(after, "|api") && !strings.HasSuffix(after, "|output") {
		return nil, "", fmt.Errorf("%w: cursor", objectcontext.ErrInvalidContext)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	var out []objectcontext.Entry

	for _, src := range []map[string]objectcontext.Entry{m.api, m.output} {
		for k := range src {
			if e := src[k]; e.Object.Namespace == ns && slices.Contains(e.Fields.Owners, owner) {
				out = append(out, e)
			}
		}
	}

	slices.SortFunc(out, func(a, b objectcontext.Entry) int {
		return strings.Compare(cursorOf(a), cursorOf(b))
	})

	out = slices.DeleteFunc(out, func(e objectcontext.Entry) bool { return cursorOf(e) <= after })

	next := ""

	if len(out) > limit {
		out = out[:limit]
		next = cursorOf(out[limit-1])
	}

	return out, next, nil
}

// cursorOf returns the page cursor after e: its key and its source.
func cursorOf(e objectcontext.Entry) string {
	return e.Object.Key() + "|" + string(e.Source)
}

func (m *memStore) checkFence(f namespace.Fence) error {
	if v, ok := m.fences[f.Namespace]; !ok || v != f.FailoverVersion {
		return namespace.ErrFenced
	}

	return nil
}

// checkUnit returns ErrInvalidObject when object names a unit, or a resource of
// one, that the store does not hold.
func (m *memStore) checkUnit(object objectcontext.Ref) error {
	if object.Kind != objectcontext.KindUnit && object.Kind != objectcontext.KindResource {
		return nil
	}

	if !m.units[objectcontext.UnitRef(object.Unit).Key()] {
		return fmt.Errorf("%w: %s does not exist", objectcontext.ErrInvalidObject, object.Key())
	}

	return nil
}

// dropUnitEntries deletes the entries of unit and of its resources from src.
func dropUnitEntries(src map[string]objectcontext.Entry, unit objectcontext.Ref) {
	for k := range src {
		o := src[k].Object
		if (o.Kind == objectcontext.KindUnit || o.Kind == objectcontext.KindResource) && o.Unit == unit.Unit {
			delete(src, k)
		}
	}
}

func TestMemStore(t *testing.T) {
	t.Parallel()

	objectcontexttest.Run(t, func(_ *testing.T) objectcontexttest.Harness {
		m := newMem()

		return objectcontexttest.Harness{
			Store: m,
			Namespace: func(_ *testing.T, name string) namespace.Fence {
				m.mu.Lock()
				defer m.mu.Unlock()

				m.fences[namespace.Name(name)] = 1

				return namespace.Fence{Namespace: namespace.Name(name), FailoverVersion: 1}
			},
			Collection: func(_ *testing.T, ns, name string) {
				m.mu.Lock()
				defer m.mu.Unlock()

				m.cols[objectcontext.CollectionRef(namespace.Name(ns), name).Key()] = true
			},
			Unit: func(_ *testing.T, u graph.Unit) {
				m.mu.Lock()
				defer m.mu.Unlock()

				m.units[objectcontext.UnitRef(u).Key()] = true
			},
			Purge: func(_ *testing.T, u graph.Unit) {
				m.mu.Lock()
				defer m.mu.Unlock()

				delete(m.units, objectcontext.UnitRef(u).Key())
			},
			Audited: func(_ *testing.T, ns string) []audit.Entry {
				m.mu.Lock()
				defer m.mu.Unlock()

				return slices.DeleteFunc(
					slices.Clone(m.audited),
					func(e audit.Entry) bool { return e.Namespace.String() != ns },
				)
			},
		}
	})
}
