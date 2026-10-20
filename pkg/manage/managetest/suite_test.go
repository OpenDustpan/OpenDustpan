package managetest_test

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"sync"
	"testing"

	"opendustpan.org/opendustpan/pkg/manage"
	"opendustpan.org/opendustpan/pkg/manage/managetest"
)

// memClient is an in-memory manage.Client. It shows that the suite can pass,
// and it is the reference for the rules of the port.
type memClient struct {
	mu      sync.Mutex
	objects map[manage.Ref]manage.Object
	counter int
}

func newMemClient() *memClient {
	return &memClient{mu: sync.Mutex{}, objects: map[manage.Ref]manage.Object{}, counter: 0}
}

func (c *memClient) ServerVersion(context.Context) (string, error) { return "0.12.0", nil }

func (c *memClient) Get(_ context.Context, ref manage.Ref) (manage.Object, error) {
	if err := ref.Validate(); err != nil {
		return manage.Object{}, err
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	obj, ok := c.objects[ref]
	if !ok {
		return manage.Object{}, manage.ErrNotFound
	}

	return obj, nil
}

func (c *memClient) List(
	_ context.Context, kind manage.Kind, namespace string, page manage.Page,
) (manage.ObjectPage, error) {
	if err := (manage.Ref{Kind: kind, Namespace: namespace, ID: "x"}).Validate(); err != nil {
		return manage.ObjectPage{}, err
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	all := make([]manage.Object, 0, len(c.objects))

	for ref, obj := range c.objects {
		if ref.Kind == kind && ref.Namespace == namespace {
			all = append(all, obj)
		}
	}

	slices.SortFunc(all, func(a, b manage.Object) int {
		switch {
		case a.Ref.ID < b.Ref.ID:
			return -1
		case a.Ref.ID > b.Ref.ID:
			return 1
		}

		return 0
	})

	start := 0
	if page.Cursor != "" {
		var err error

		start, err = strconv.Atoi(page.Cursor)
		if err != nil {
			return manage.ObjectPage{}, fmt.Errorf("cursor %q: %w", page.Cursor, manage.ErrInvalidSpec)
		}
	}

	limit := page.Limit
	if limit <= 0 {
		limit = 50
	}

	start = min(max(start, 0), len(all))
	end := min(start+limit, len(all))
	next := ""

	if end < len(all) {
		next = strconv.Itoa(end)
	}

	return manage.ObjectPage{Objects: all[start:end], Next: next}, nil
}

func (c *memClient) Create(_ context.Context, ref manage.Ref, spec json.RawMessage) (manage.Object, error) {
	if err := ref.Validate(); err != nil {
		return manage.Object{}, err
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if _, ok := c.objects[ref]; ok {
		return manage.Object{}, manage.ErrAlreadyExists
	}

	c.counter++
	obj := manage.Object{Ref: ref, Version: strconv.Itoa(c.counter), Spec: spec}
	c.objects[ref] = obj

	return obj, nil
}

func (c *memClient) Update(_ context.Context, obj manage.Object) (manage.Object, error) {
	if err := obj.Ref.Validate(); err != nil {
		return manage.Object{}, err
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	current, ok := c.objects[obj.Ref]
	if !ok {
		return manage.Object{}, manage.ErrNotFound
	}

	if current.Version != obj.Version {
		return manage.Object{}, manage.ErrConflict
	}

	c.counter++
	updated := manage.Object{Ref: obj.Ref, Version: strconv.Itoa(c.counter), Spec: obj.Spec}
	c.objects[obj.Ref] = updated

	return updated, nil
}

func (c *memClient) Delete(_ context.Context, ref manage.Ref, version string) error {
	if err := ref.Validate(); err != nil {
		return err
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	current, ok := c.objects[ref]
	if !ok {
		return manage.ErrNotFound
	}

	if current.Version != version {
		return manage.ErrConflict
	}

	delete(c.objects, ref)

	return nil
}

func TestConformance(t *testing.T) {
	t.Parallel()

	managetest.Run(t, func(*testing.T) manage.Client { return newMemClient() })
}
