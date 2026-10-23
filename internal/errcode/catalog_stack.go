package errcode

// The codes of the stacks of the Pulumi Cloud-compatible API. A code is never
// removed and never changes its meaning.
const (
	// StackRevealPathTooLong is a reveal request whose value path is longer
	// than the limit.
	StackRevealPathTooLong = "stack.reveal-path-too-long"
)

// stackSpecs are the codes of the stacks of the Pulumi Cloud-compatible API.
func stackSpecs() []Spec {
	return []Spec{
		{
			Code:    StackRevealPathTooLong,
			Meaning: "the value path of the reveal request is longer than the limit, in bytes",
			Args:    []Arg{{Name: "limit", Kind: KindQuantity}},
		},
	}
}
