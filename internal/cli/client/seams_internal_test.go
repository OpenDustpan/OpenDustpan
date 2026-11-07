package client

import (
	"context"
	"io"
)

// Test seams of the collection and placement commands. The commands read the
// admin token through a lookup function. These functions let the black-box
// tests pass their own lookup, so the tests run in parallel and do not change
// the process environment.

// CollectionCreateWithLookup runs collection create with lookup for the token.
func CollectionCreateWithLookup(
	ctx context.Context, args []string, lookup func(string) (string, bool), stdout, stderr io.Writer,
) error {
	return runCollectionCreate(ctx, args, lookup, stdout, stderr)
}

// CollectionListWithLookup runs collection list with lookup for the token.
func CollectionListWithLookup(
	ctx context.Context, args []string, lookup func(string) (string, bool), stdout, stderr io.Writer,
) error {
	return runCollectionList(ctx, args, lookup, stdout, stderr)
}

// PlacementAddWithLookup runs placement add with lookup for the token.
func PlacementAddWithLookup(
	ctx context.Context, args []string, lookup func(string) (string, bool), stdout, stderr io.Writer,
) error {
	return runPlacementAdd(ctx, args, lookup, stdout, stderr)
}

// PlacementListWithLookup runs placement list with lookup for the token.
func PlacementListWithLookup(
	ctx context.Context, args []string, lookup func(string) (string, bool), stdout, stderr io.Writer,
) error {
	return runPlacementList(ctx, args, lookup, stdout, stderr)
}

// PlacementDeleteWithLookup runs placement delete with lookup for the token.
func PlacementDeleteWithLookup(
	ctx context.Context, args []string, lookup func(string) (string, bool), stdout, stderr io.Writer,
) error {
	return runPlacementDelete(ctx, args, lookup, stdout, stderr)
}
