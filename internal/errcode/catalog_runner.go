package errcode

// The codes of the runner management API. A code is never removed and never
// changes its meaning.
const (
	// RunnerImmutable means that the request changes a field of the runner
	// pool that cannot change after its creation.
	RunnerImmutable = "runner.immutable"
	// RunnerInvalid means that the runner request has an empty or bad field.
	RunnerInvalid = "runner.invalid"
	// RunnerLimit means that the request is over the limit of runner pools,
	// runners or join tokens.
	RunnerLimit = "runner.limit"
	// RunnerNotFound means that the runner pool or the runner does not exist,
	// or that the caller cannot read it.
	RunnerNotFound = "runner.not-found"
	// RunnerPoolExists means that the namespace has a runner pool with the
	// name.
	RunnerPoolExists = "runner.pool-exists"
	// RunnerPoolInUse means that a job that did not end, or a sweep target,
	// names the runner pool.
	RunnerPoolInUse = "runner.pool-in-use"
	// RunnerRetired means that an administrator retired the runner.
	RunnerRetired = "runner.retired"
	// RunnerTokenUsed means that a request already used the join token or the
	// handoff token.
	RunnerTokenUsed = "runner.token-used"
)

// runnerSpecs are the codes of the runners and the runner pools.
func runnerSpecs() []Spec {
	return []Spec{
		{
			Code:    RunnerImmutable,
			Meaning: "the request changes a field of the runner pool that cannot change after its creation",
			Args:    nil,
		},
		{Code: RunnerInvalid, Meaning: "the runner request has an empty or bad field", Args: nil},
		{
			Code:    RunnerLimit,
			Meaning: "the request is over the limit of runner pools, runners or join tokens",
			Args:    nil,
		},
		{
			Code:    RunnerNotFound,
			Meaning: "the runner pool or the runner does not exist, or the caller cannot read it",
			Args:    nil,
		},
		{Code: RunnerPoolExists, Meaning: "the namespace has a runner pool with the name", Args: nil},
		{
			Code:    RunnerPoolInUse,
			Meaning: "a job that did not end, or a sweep target, names the runner pool",
			Args:    nil,
		},
		{Code: RunnerRetired, Meaning: "an administrator retired the runner", Args: nil},
		{
			Code:    RunnerTokenUsed,
			Meaning: "a request already used the join token or the handoff token",
			Args:    nil,
		},
	}
}
