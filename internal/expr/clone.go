package expr

import (
	"maps"
	"slices"

	"opendustpan.org/opendustpan/internal/selfservice"
)

// cloneEnv returns a deep copy of env. A run that ends at the time limit
// leaves its goroutine alive until the next counted call, and that goroutine
// still reads the environment. So the goroutine gets its own copy, and a
// caller that reuses or changes env after the timeout cannot race with it.
//
// checkEnvSize has accepted env, so each value is a JSON value: nil, a
// boolean, a number, a string, a list or an object. Strings are immutable
// and are shared.
func cloneEnv(env *selfservice.Env) selfservice.Env {
	out := *env
	out.Value = cloneValue(env.Value)
	out.Input = cloneValue(env.Input)
	out.Inputs = cloneObject(env.Inputs)
	out.Usage = maps.Clone(env.Usage)
	out.Request.Requester.Groups = slices.Clone(env.Request.Requester.Groups)

	return out
}

func cloneValue(v any) any {
	switch x := v.(type) {
	case []any:
		out := make([]any, len(x))
		for i, item := range x {
			out[i] = cloneValue(item)
		}

		return out
	case map[string]any:
		return cloneObject(x)
	default:
		// Scalars: nil, bool, float64, json.Number and string.
		return v
	}
}

func cloneObject(m map[string]any) map[string]any {
	if m == nil {
		return nil
	}

	out := make(map[string]any, len(m))
	for k, item := range m {
		out[k] = cloneValue(item)
	}

	return out
}
