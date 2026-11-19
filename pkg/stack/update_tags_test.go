package stack_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/collection"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/stack"
)

// tagRecorder is a store that records each tag update and never fails.
type tagRecorder struct {
	stack.Store

	mu      sync.Mutex
	updates []stack.TagUpdate
}

func (r *tagRecorder) UpdateStackTags(_ context.Context, _ stack.Ref, u stack.TagUpdate, _ namespace.Fence) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.updates = append(r.updates, u)

	return nil
}

func (r *tagRecorder) GetStack(context.Context, stack.Ref) (stack.Stack, error) {
	var st stack.Stack

	return st, nil
}

func (r *tagRecorder) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()

	return len(r.updates)
}

// tagService returns a stack service over a tagRecorder with a fixed clock.
func tagService(t *testing.T, at time.Time) (*stack.Service, *tagRecorder, stack.Ref) {
	t.Helper()

	inner := wrapCrashSQL(t, nil)

	ns := namespace.Namespace{
		ID: namespace.NewID(), CreatedAt: time.Unix(1, 0), Name: "acme", PrimaryInstallation: "eu-west",
		FailoverVersion: 1, Overrides: namespace.Overrides{Forced: false, Unlocked: false},
	}
	if err := inner.CreateNamespace(t.Context(), ns); err != nil {
		t.Fatalf("create namespace: %v", err)
	}

	namespaces, err := namespace.NewService(namespace.Config{Store: inner, Now: nil, Installation: "eu-west"})
	if err != nil {
		t.Fatalf("namespace service: %v", err)
	}

	rec := &tagRecorder{Store: inner, mu: sync.Mutex{}, updates: nil}

	svc, err := stack.NewService(stack.Config{
		Store: rec, Namespaces: namespaces, Now: func() time.Time { return at }, State: nil,
		Retention: 0, ManualLocks: nil,
	})
	if err != nil {
		t.Fatalf("stack service: %v", err)
	}

	ref, err := stack.ParseRef("acme", "web", "prod")
	if err != nil {
		t.Fatalf("parse ref: %v", err)
	}

	return svc, rec, ref
}

// TestUpdateTagsStampsTheEntry proves that UpdateTags passes the entry to the
// store with the time of the update, whatever time the caller set.
func TestUpdateTagsStampsTheEntry(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	svc, rec, ref := tagService(t, at)

	var e audit.Entry

	e.At = time.Unix(42, 0)
	e.Subject = "tester"

	if _, err := svc.UpdateTags(t.Context(), ref, map[string]string{"team": "web"}, e); err != nil {
		t.Fatalf("UpdateTags: %v", err)
	}

	if rec.count() != 1 {
		t.Fatalf("store updates = %d, want 1", rec.count())
	}

	got := rec.updates[0]
	if !got.Entry.At.Equal(at) || !got.UpdatedAt.Equal(at) {
		t.Fatalf("entry time = %v, update time = %v, want both %v", got.Entry.At, got.UpdatedAt, at)
	}

	if got.Entry.Subject != "tester" {
		t.Fatalf("entry subject = %q, want the subject of the caller", got.Entry.Subject)
	}
}

// TestUpdateTagsRefusesHostileTags proves that UpdateTags and CreatePlaced
// return ErrInvalidName, and call no store, for too many tags and for a tag
// name or value that is not allowed.
func TestUpdateTagsRefusesHostileTags(t *testing.T) {
	t.Parallel()

	tooMany := make(map[string]string, 65)
	for i := range 65 {
		tooMany[strings.Repeat("k", 1)+string(rune('a'+i%26))+strings.Repeat("0", i/26)] = "v"
	}

	cases := []struct {
		name string
		tags map[string]string
	}{
		{"65 tags", tooMany},
		{"CR LF in the name", map[string]string{"evil\r\nInjected: yes": "v"}},
		{"zero-width space", map[string]string{"a" + string(rune(0x200b)) + "b": "v"}},
		{"invalid UTF-8", map[string]string{"bad\xffutf": "v"}},
		{"empty name", map[string]string{"": "v"}},
		{"41-byte name", map[string]string{strings.Repeat("k", 41): "v"}},
		{"space in the name", map[string]string{"a b": "v"}},
		{"value over 256 bytes", map[string]string{"k": strings.Repeat("v", 257)}},
		{"value with invalid UTF-8", map[string]string{"k": "\xff"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			svc, rec, ref := tagService(t, time.Unix(100, 0))

			_, err := svc.UpdateTags(t.Context(), ref, tc.tags, zeroEntry())
			if !errors.Is(err, stack.ErrInvalidName) {
				t.Fatalf("UpdateTags err = %v, want ErrInvalidName", err)
			}

			if strings.ContainsAny(err.Error(), "\r\n"+string(rune(0x200b))) || strings.Contains(err.Error(), "\xff") {
				t.Fatalf("error text %q holds the hostile name", err)
			}

			if rec.count() != 0 {
				t.Fatalf("store updates = %d, want 0", rec.count())
			}
		})
	}
}

// TestCreatePlacedRefusesHostileTags proves that CreatePlaced checks the tags
// itself: with a complete placement and a store that accepts any placement, a
// hostile tag returns ErrInvalidName and the stack does not exist.
func TestCreatePlacedRefusesHostileTags(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		tags map[string]string
	}{
		{"name with CR LF", map[string]string{"evil\r\nInjected: yes": "v"}},
		{"empty name", map[string]string{"": "v"}},
		{"41-byte name", map[string]string{strings.Repeat("k", 41): "v"}},
		{"value over 256 bytes", map[string]string{"k": strings.Repeat("v", 257)}},
		{"value with invalid UTF-8", map[string]string{"k": "\xff"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			svc, ref := permissiveService(t)

			placement := stack.Placement{
				Collection:   collection.Ref{Namespace: "acme", Name: "payments"},
				CollectionID: "col-1",
				Entry:        zeroEntry(),
			}

			_, err := svc.CreatePlaced(t.Context(), ref, tc.tags, placement)
			if !errors.Is(err, stack.ErrInvalidName) {
				t.Fatalf("CreatePlaced err = %v, want ErrInvalidName", err)
			}

			if _, getErr := svc.Get(t.Context(), ref); !errors.Is(getErr, stack.ErrNotFound) {
				t.Fatalf("Get after the refused create = %v, want stack.ErrNotFound", getErr)
			}
		})
	}
}

// TestUpdateTagsAcceptsPulumiTags proves the names that the Pulumi CLI sends
// pass the check, and the limit values pass too.
func TestUpdateTagsAcceptsPulumiTags(t *testing.T) {
	t.Parallel()

	svc, rec, ref := tagService(t, time.Unix(100, 0))

	tags := map[string]string{
		"pulumi:project":        "web",
		"pulumi:runtime":        "nodejs",
		"pulumi:description":    "a\nb\tc",
		"pulumi:long":           strings.Repeat("d", 256),
		"vcs:root":              "a\nb\tc",
		"github:owner":          "x",
		strings.Repeat("n", 40): "",
		"a_b-c.d":               "v",
	}

	if _, err := svc.UpdateTags(t.Context(), ref, tags, zeroEntry()); err != nil {
		t.Fatalf("UpdateTags: %v", err)
	}

	if rec.count() != 1 {
		t.Fatalf("store updates = %d, want 1", rec.count())
	}
}

// zeroEntry answers an entry with no field set.
func zeroEntry() audit.Entry {
	var e audit.Entry

	return e
}
