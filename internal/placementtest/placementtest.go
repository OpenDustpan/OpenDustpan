// Package placementtest makes the collection that a test unit needs. A stack
// or a workspace is always in a collection, so a test that makes a unit makes a
// collection and a placement first.
package placementtest

import (
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/ident"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/collection"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/stack"
	"opendustpan.org/opendustpan/pkg/workspace"
)

// collectionName is the name of the collection that a test placement makes.
const collectionName = "placed"

// testSubject is the subject of the audit entries of a test placement.
const testSubject = "test:placement"

// Entry returns a valid audit entry of a placement in ns.
func Entry(ns namespace.Name) audit.Entry {
	return audit.Entry{
		At:            time.Unix(1, 0).UTC(),
		Namespace:     ns,
		Subject:       testSubject,
		IDPSubject:    "",
		TokenID:       "",
		Action:        "collection:create-unit",
		Object:        ns.String(),
		Version:       "",
		ValuePath:     "",
		ValueCount:    nil,
		SourceIP:      "",
		UserAgent:     "",
		Channel:       audit.ChannelAPI,
		Kind:          audit.KindChange,
		Outcome:       audit.OutcomeSuccess,
		RequestID:     "",
		PrevHash:      "",
		Hash:          "",
		Sequence:      0,
		Changes:       nil,
		ObjectID:      "",
		FenceVersion:  0,
		PrincipalKind: "",
		JobID:         "",
		Reason:        "",
	}
}

// Collection makes the collection "placed" in the namespace of fence through
// store when it does not exist yet, and returns it.
func Collection(tb testing.TB, store collection.Store, fence namespace.Fence) collection.Collection {
	tb.Helper()

	name, err := collection.ParseName(collectionName)
	if err != nil {
		tb.Fatalf("collection name: %v", err)
	}

	ref := collection.Ref{Namespace: fence.Namespace, Name: name}

	got, err := store.GetCollection(tb.Context(), ref)
	if err == nil {
		return got
	}

	c := collection.Collection{CreatedAt: time.Unix(1, 0).UTC(), ID: ident.NewID("col-"), Ref: ref}
	if err := store.CreateCollection(tb.Context(), c, fence, Entry(fence.Namespace)); err != nil {
		tb.Fatalf("create collection %s: %v", ref, err)
	}

	return c
}

// Stack returns a placement of a new stack in the collection "placed".
func Stack(tb testing.TB, store collection.Store, fence namespace.Fence) stack.Placement {
	tb.Helper()

	c := Collection(tb, store, fence)

	return stack.Placement{Collection: c.Ref, CollectionID: c.ID, Entry: Entry(fence.Namespace)}
}

// Workspace returns a placement of a new workspace in the collection "placed".
func Workspace(tb testing.TB, store collection.Store, fence namespace.Fence) workspace.Placement {
	tb.Helper()

	c := Collection(tb, store, fence)

	return workspace.Placement{Collection: c.Ref, CollectionID: c.ID, Entry: Entry(fence.Namespace)}
}
