package stacktest

import (
	"context"

	"opendustpan.org/opendustpan/pkg/stack"
)

// Membership is a stack.Membership with a fixed answer.
type Membership struct {
	// Err is the error that CollectionOf returns.
	Err error
	// Collection is the collection ID that CollectionOf returns when Found is true.
	Collection string
	// Found is the second answer of CollectionOf.
	Found bool
}

var _ stack.Membership = Membership{Err: nil, Collection: "", Found: false}

// PlacedAll returns a Membership that puts every stack in a collection. Use it
// where a test has no collections and the check is not under test.
func PlacedAll() Membership {
	return Membership{Err: nil, Collection: "c-test", Found: true}
}

// CollectionOf implements stack.Membership.
func (m Membership) CollectionOf(context.Context, stack.Ref) (string, bool, error) {
	return m.Collection, m.Found, m.Err
}
