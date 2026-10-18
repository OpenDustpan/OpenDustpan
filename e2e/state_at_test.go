//go:build e2e

package e2e_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/e2e"
)

// stateAtResponse is the answer of the resource list at a time.
type stateAtResponse struct {
	Resources []maskedStateItem `json:"resources"`
	Version   int               `json:"version"`
	EndedAt   int64             `json:"endedAt"`
	Running   int               `json:"running"`
}

// stateAtPath returns the route of the resource list at the time at.
func stateAtPath(base string, at time.Time) string {
	return fmt.Sprintf("%s/state-at/%d/resources", base, at.UnixMilli())
}

// firstStateTime matches the time of the first state in the text of the 404.
var firstStateTime = regexp.MustCompile(`the first state of \S+ is at \S+ \((\d+) ms\)`)

// stateAtTimes are the two times of the masked scenario: before the first
// up, and after the first up ended and before the second up started.
type stateAtTimes struct {
	before  time.Time
	between time.Time
}

// checkMaskedStateAt proves the resource list at a time after two ups: a time
// between them answers version 1 with the URNs and masking of the version 1
// list, and a time before the first up answers 404 that names the time of the
// first update.
func checkMaskedStateAt(
	ctx context.Context, env *e2e.Env, org, secret string, times stateAtTimes,
) func(e2e.Result) error {
	return func(e2e.Result) error {
		base := fmt.Sprintf("/api/stacks/%s/%s/masked", org, e2e.Project)

		body, err := maskedStateGet(ctx, env, stateAtPath(base, times.between))
		if err != nil {
			return err
		}

		if strings.Contains(body, secret) {
			return fmt.Errorf("%w: the list at a time holds the secret plain text", errUnexpected)
		}

		got, err := decodeStateAt(body)
		if err != nil {
			return err
		}

		if got.Version != 1 || got.Running != 0 || got.EndedAt > times.between.UnixMilli() {
			return fmt.Errorf(
				"%w: list between the ups = %+v, want version 1 and no running update",
				errUnexpected,
				got,
			)
		}

		pinned, err := maskedStateGet(ctx, env, base+"/state/1/resources")
		if err != nil {
			return err
		}

		if err := checkSameURNs(got, pinned); err != nil {
			return err
		}

		return checkStateAtBeforeFirst(ctx, env, base, times.before)
	}
}

// decodeStateAt decodes the answer of the resource list at a time.
func decodeStateAt(body string) (stateAtResponse, error) {
	var got stateAtResponse
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		return got, fmt.Errorf("%w: decode the list at a time: %w", errUnexpected, err)
	}

	return got, nil
}

// checkSameURNs proves the list at a time holds the URNs of the list of the
// version that it names.
func checkSameURNs(got stateAtResponse, pinned string) error {
	want, err := maskedListURNs(pinned)
	if err != nil {
		return err
	}

	urns := make([]string, 0, len(got.Resources))
	for i := range got.Resources {
		urns = append(urns, got.Resources[i].URN)
	}

	slices.Sort(urns)
	slices.Sort(want)

	if !slices.Equal(urns, want) {
		return fmt.Errorf("%w: list between the ups holds %q, version 1 holds %q", errUnexpected, urns, want)
	}

	return nil
}

// checkStateAtBeforeFirst proves that a time before the first up answers 404
// with the time of the first update, which is not before that time.
func checkStateAtBeforeFirst(ctx context.Context, env *e2e.Env, base string, before time.Time) error {
	path := stateAtPath(base, before)

	status, body, err := env.APIStatus(ctx, http.MethodGet, path, "")
	if err != nil {
		return fmt.Errorf("%w: GET %s: %w", errUnexpected, path, err)
	}

	if status != http.StatusNotFound {
		return fmt.Errorf("%w: GET %s answers %d, want 404 (%s)", errUnexpected, path, status, body)
	}

	match := firstStateTime.FindStringSubmatch(body)
	if match == nil {
		return fmt.Errorf("%w: the 404 does not name the time of the first state (%s)", errUnexpected, body)
	}

	first, err := strconv.ParseInt(match[1], 10, 64)
	if err != nil || first < before.UnixMilli() {
		return fmt.Errorf(
			"%w: the 404 names the first state at %q, want a time after %d",
			errUnexpected,
			match[1],
			before.UnixMilli(),
		)
	}

	return nil
}

// checkStateAtRunning proves that a time while an update runs answers the
// state before it and names the update. It holds an update through the API,
// as a running up would, then ends it.
func checkStateAtRunning(ctx context.Context, t *testing.T, env *e2e.Env, org string) {
	t.Helper()

	base := fmt.Sprintf("/api/stacks/%s/%s/masked", org, e2e.Project)
	held := env.StartHeldUpdate(ctx, t, org, e2e.Project, "masked")

	body, err := maskedStateGet(ctx, env, stateAtPath(base, time.Now()))
	if err == nil {
		var got stateAtResponse

		got, err = decodeStateAt(body)
		if err == nil && (got.Version != 2 || got.Running != 3) {
			err = fmt.Errorf(
				"%w: list while an update runs = %+v, want version 2 and running update 3",
				errUnexpected,
				got,
			)
		}
	}

	if err != nil {
		t.Error(err)
	}

	env.EndHeldUpdate(ctx, t, held, "cancelled") //nolint:misspell // The CLI's wire value; it must not change.
}
