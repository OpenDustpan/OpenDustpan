// Package managetest is the conformance suite for manage.Client
// implementations. Every adapter runs it, and a fake in a test of a provider
// should too:
//
//	func TestConformance(t *testing.T) {
//		t.Parallel()
//		managetest.Run(t, func(t *testing.T) manage.Client { return newMyClient(t) })
//	}
//
// The suite is a skeleton. It checks the life cycle of an object of each kind
// and the errors of the port. Cases that need a real server, such as the fresh
// authentication of a write and the pin of the server version, belong to the
// suite of the adapter that talks to a server.
package managetest

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"opendustpan.org/opendustpan/pkg/manage"
)

// Factory returns a new, empty client for one test. Two clients from the
// factory do not share objects.
type Factory func(t *testing.T) manage.Client

// Run runs the suite against clients from the factory.
func Run(t *testing.T, newClient Factory) {
	t.Helper()

	cases := []struct {
		name string
		run  func(t *testing.T, client manage.Client, kind manage.Kind)
	}{
		{"create then get", createThenGet},
		{"get of a missing object", missingObject},
		{"create twice", createTwice},
		{"update with the current version", updateCurrent},
		{"update with a stale version", updateStale},
		{"delete with a stale version", deleteStale},
		{"delete then get", deleteThenGet},
		{"list", list},
		{"invalid reference", invalidRef},
	}

	for _, kind := range manage.Kinds() {
		for _, test := range cases {
			t.Run(string(kind)+"/"+test.name, func(t *testing.T) {
				t.Parallel()

				test.run(t, newClient(t), kind)
			})
		}
	}
}

func ref(kind manage.Kind, id string) manage.Ref {
	return manage.Ref{Kind: kind, Namespace: "team-a", ID: id}
}

func spec(note string) json.RawMessage {
	return json.RawMessage(`{"note":"` + note + `"}`)
}

// sameSpec compares two JSON documents by value.
func sameSpec(t *testing.T, got, want json.RawMessage) {
	t.Helper()

	var gotValue, wantValue any

	if err := json.Unmarshal(got, &gotValue); err != nil {
		t.Fatalf("spec %s is not JSON: %v", got, err)
	}

	if err := json.Unmarshal(want, &wantValue); err != nil {
		t.Fatalf("spec %s is not JSON: %v", want, err)
	}

	if !reflect.DeepEqual(gotValue, wantValue) {
		t.Errorf("spec = %s, want %s", got, want)
	}
}

func mustCreate(t *testing.T, client manage.Client, r manage.Ref, s json.RawMessage) manage.Object {
	t.Helper()

	obj, err := client.Create(t.Context(), r, s)
	if err != nil {
		t.Fatalf("Create(%+v): %v", r, err)
	}

	return obj
}

func createThenGet(t *testing.T, client manage.Client, kind manage.Kind) {
	t.Helper()

	r := ref(kind, "one")
	created := mustCreate(t, client, r, spec("a"))

	if created.Ref != r || created.Version == "" {
		t.Errorf("Create returned %+v, want the reference and a version", created)
	}

	got, err := client.Get(t.Context(), r)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if got.Version != created.Version {
		t.Errorf("Get version = %q, want %q", got.Version, created.Version)
	}

	sameSpec(t, got.Spec, spec("a"))
}

func missingObject(t *testing.T, client manage.Client, kind manage.Kind) {
	t.Helper()

	if _, err := client.Get(t.Context(), ref(kind, "missing")); !errors.Is(err, manage.ErrNotFound) {
		t.Errorf("Get error = %v, want ErrNotFound", err)
	}
}

func createTwice(t *testing.T, client manage.Client, kind manage.Kind) {
	t.Helper()

	r := ref(kind, "twice")
	first := mustCreate(t, client, r, spec("a"))

	if _, err := client.Create(t.Context(), r, spec("b")); !errors.Is(err, manage.ErrAlreadyExists) {
		t.Errorf("second Create error = %v, want ErrAlreadyExists", err)
	}

	got, err := client.Get(t.Context(), r)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if got.Version != first.Version {
		t.Errorf("a refused Create changed the version to %q", got.Version)
	}

	sameSpec(t, got.Spec, spec("a"))
}

func updateCurrent(t *testing.T, client manage.Client, kind manage.Kind) {
	t.Helper()

	created := mustCreate(t, client, ref(kind, "upd"), spec("a"))

	change := manage.Object{Ref: created.Ref, Version: created.Version, Spec: spec("b")}

	updated, err := client.Update(t.Context(), change)
	if err != nil {
		t.Fatalf("Update: %v", err)
	}

	if updated.Version == created.Version {
		t.Errorf("Update kept the version %q", updated.Version)
	}

	sameSpec(t, updated.Spec, spec("b"))
}

func updateStale(t *testing.T, client manage.Client, kind manage.Kind) {
	t.Helper()

	created := mustCreate(t, client, ref(kind, "stale"), spec("a"))

	first := manage.Object{Ref: created.Ref, Version: created.Version, Spec: spec("b")}
	if _, err := client.Update(t.Context(), first); err != nil {
		t.Fatalf("first Update: %v", err)
	}

	second := manage.Object{Ref: created.Ref, Version: created.Version, Spec: spec("c")}

	_, err := client.Update(t.Context(), second)
	if !errors.Is(err, manage.ErrConflict) {
		t.Fatalf("stale Update error = %v, want ErrConflict", err)
	}

	got, err := client.Get(t.Context(), created.Ref)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	sameSpec(t, got.Spec, spec("b"))

	_, err = client.Update(t.Context(), manage.Object{Ref: ref(kind, "absent"), Version: "1", Spec: spec("x")})
	if !errors.Is(err, manage.ErrNotFound) {
		t.Errorf("Update of a missing object error = %v, want ErrNotFound", err)
	}
}

func deleteStale(t *testing.T, client manage.Client, kind manage.Kind) {
	t.Helper()

	created := mustCreate(t, client, ref(kind, "delstale"), spec("a"))

	change := manage.Object{Ref: created.Ref, Version: created.Version, Spec: spec("b")}
	if _, err := client.Update(t.Context(), change); err != nil {
		t.Fatalf("Update: %v", err)
	}

	if err := client.Delete(t.Context(), created.Ref, created.Version); !errors.Is(err, manage.ErrConflict) {
		t.Fatalf("stale Delete error = %v, want ErrConflict", err)
	}

	if _, err := client.Get(t.Context(), created.Ref); err != nil {
		t.Errorf("a refused Delete removed the object: %v", err)
	}
}

func deleteThenGet(t *testing.T, client manage.Client, kind manage.Kind) {
	t.Helper()

	created := mustCreate(t, client, ref(kind, "del"), spec("a"))

	if err := client.Delete(t.Context(), created.Ref, created.Version); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	if _, err := client.Get(t.Context(), created.Ref); !errors.Is(err, manage.ErrNotFound) {
		t.Errorf("Get after Delete error = %v, want ErrNotFound", err)
	}

	if err := client.Delete(t.Context(), created.Ref, created.Version); !errors.Is(err, manage.ErrNotFound) {
		t.Errorf("second Delete error = %v, want ErrNotFound", err)
	}
}

const (
	pageSize = 2
	listed   = 3
)

func list(t *testing.T, client manage.Client, kind manage.Kind) {
	t.Helper()

	for _, id := range []string{"l1", "l2", "l3"} {
		mustCreate(t, client, ref(kind, id), spec(id))
	}

	seen := map[string]bool{}
	page := manage.Page{Cursor: "", Limit: pageSize}

	for range 10 {
		got, err := client.List(t.Context(), kind, "team-a", page)
		if err != nil {
			t.Fatalf("List: %v", err)
		}

		for _, obj := range got.Objects {
			if seen[obj.Ref.ID] {
				t.Errorf("List returned %q twice", obj.Ref.ID)
			}

			seen[obj.Ref.ID] = true
		}

		if got.Next == "" {
			break
		}

		page.Cursor = got.Next
	}

	if len(seen) != listed {
		t.Errorf("List returned %d objects, want %d", len(seen), listed)
	}
}

// invalidRef sends hostile references as if every other layer had failed. The
// client must refuse them with ErrInvalidRef.
func invalidRef(t *testing.T, client manage.Client, kind manage.Kind) {
	t.Helper()

	ctx := context.Background()
	bad := manage.Ref{Kind: kind, Namespace: "team-a", ID: "../other"}

	if _, err := client.Get(ctx, bad); !errors.Is(err, manage.ErrInvalidRef) {
		t.Errorf("Get error = %v, want ErrInvalidRef", err)
	}

	if _, err := client.Create(ctx, bad, spec("a")); !errors.Is(err, manage.ErrInvalidRef) {
		t.Errorf("Create error = %v, want ErrInvalidRef", err)
	}

	change := manage.Object{Ref: bad, Version: "1", Spec: spec("a")}
	if _, err := client.Update(ctx, change); !errors.Is(err, manage.ErrInvalidRef) {
		t.Errorf("Update error = %v, want ErrInvalidRef", err)
	}

	if err := client.Delete(ctx, bad, "1"); !errors.Is(err, manage.ErrInvalidRef) {
		t.Errorf("Delete error = %v, want ErrInvalidRef", err)
	}

	_, err := client.List(ctx, kind, "team-a/../b", manage.Page{Cursor: "", Limit: 0})
	if !errors.Is(err, manage.ErrInvalidRef) {
		t.Errorf("List error = %v, want ErrInvalidRef", err)
	}
}
