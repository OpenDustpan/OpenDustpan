package conformance

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
)

// ErrNotApplicable means that a case needs an operation that the client does
// not cover. The runner reports the case as not applicable.
var ErrNotApplicable = errors.New("conformance: the case needs an operation that the client does not cover")

// caseFunc is the body of a case.
type caseFunc = func(context.Context, Env) error

// caseBodies maps "group/name" to the body of a case.
func caseBodies() map[string]caseFunc {
	bodies := make(map[string]caseFunc)

	for _, table := range []map[string]caseFunc{operationCases(), errorCases(), stateCases()} {
		maps.Copy(bodies, table)
	}

	return bodies
}

// withBodies sets the body of each case that has one.
func withBodies(cases []Case) []Case {
	bodies := caseBodies()

	for i := range cases {
		cases[i].Run = bodies[string(cases[i].Group)+"/"+cases[i].Name]
	}

	return cases
}

// need returns ErrNotApplicable unless the client covers the operations.
func need(env Env, ops ...string) error {
	for _, op := range ops {
		if !slices.Contains(env.Operations, op) {
			return fmt.Errorf("%w: %s", ErrNotApplicable, op)
		}
	}

	return nil
}

// onlyExchange returns the one exchange that the proxy recorded.
func onlyExchange(env Env) (Exchange, error) {
	ex := env.Proxy.Exchanges()
	if len(ex) != 1 {
		var none Exchange

		return none, failf("want 1 request through the proxy, got %d", len(ex))
	}

	return ex[0], nil
}

// callOnce runs one call, and returns the one exchange that it made.
func callOnce(ctx context.Context, env Env, p CallParams) (Exchange, error) {
	var none Exchange

	if _, err := env.Adapter.Call(ctx, p); err != nil {
		return none, fmt.Errorf("%s: %w", p.Operation, err)
	}

	return onlyExchange(env)
}
