package errcode

// The codes of the stacks of the Pulumi Cloud-compatible API. A code is never
// removed and never changes its meaning.
const (
	// StackAlreadyExists is a stack that exists already.
	StackAlreadyExists = "stack.already-exists"
	// StackHasResources is a delete without force of a stack that still has
	// resources.
	StackHasResources = "stack.has-resources"
	// StackInvalidName is a project name, a stack name or a set of tags that
	// is not valid.
	StackInvalidName = "stack.invalid-name"
	// StackNoState is a stack that has no state, also at the time of a
	// request for the state at a time.
	StackNoState = "stack.no-state"
	// StackNoStateAtTime is a time before the first state of a stack that has
	// a state.
	StackNoStateAtTime = "stack.no-state-at-time"
	// StackNotFound is a stack that does not exist.
	StackNotFound = "stack.not-found"
	// StackOtherSecretsProvider is a stack that uses another secrets provider
	// than the request needs.
	StackOtherSecretsProvider = "stack.other-secrets-provider"
	// StackResourceNotFound is a resource that the state of the stack does not
	// hold.
	StackResourceNotFound = "stack.resource-not-found"
	// StackRevealPathTooLong is a reveal request whose value path is longer
	// than the limit.
	StackRevealPathTooLong = "stack.reveal-path-too-long"
)

// stackSpecs are the codes of the stacks of the Pulumi Cloud-compatible API.
func stackSpecs() []Spec {
	return []Spec{
		{Code: StackAlreadyExists, Meaning: "a stack with the same name exists in the project", Args: nil},
		{
			Code:    StackHasResources,
			Meaning: "the stack still has resources, and the delete is not forced",
			Args:    nil,
		},
		{
			Code:    StackInvalidName,
			Meaning: "the project name, the stack name or the tags of the stack are not valid",
			Args:    nil,
		},
		{Code: StackNoState, Meaning: "the stack has no state", Args: nil},
		{
			Code:    StackNoStateAtTime,
			Meaning: "the stack has no state at the time of the request; first is the time of its first state",
			Args:    []Arg{{Name: "first", Kind: KindTime}},
		},
		{Code: StackNotFound, Meaning: "the stack does not exist", Args: nil},
		{
			Code:    StackOtherSecretsProvider,
			Meaning: "the stack uses another secrets provider than the request needs",
			Args:    nil,
		},
		{Code: StackResourceNotFound, Meaning: "the state of the stack has no such resource", Args: nil},
		{
			Code:    StackRevealPathTooLong,
			Meaning: "the value path of the reveal request is longer than the limit, in bytes",
			Args:    []Arg{{Name: "limit", Kind: KindQuantity}},
		},
	}
}
