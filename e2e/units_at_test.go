//go:build e2e

package e2e_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"opendustpan.org/opendustpan/e2e"
)

// unitAtDoc is one unit of the inventory at a time: the name at the time,
// the name now, and the mark of a soft-deleted unit.
type unitAtDoc struct {
	Tool        string `json:"tool"`
	Reference   string `json:"reference"`
	Name        string `json:"name"`
	UnitID      string `json:"unitID"`
	CurrentName string `json:"currentName"`
	Deleted     bool   `json:"deleted"`
	DeletedAt   string `json:"deletedAt"`
}

// unitsAtDoc is the answer of GET /api/dustpan/v1/namespaces/{namespace}/units with the at value.
type unitsAtDoc struct {
	Units   []unitAtDoc `json:"units"`
	At      int64       `json:"at"`
	Purged  int         `json:"purged"`
	Omitted int         `json:"omitted"`
}

// unitsAt reads the inventory of the namespace of u at the time at, through
// the inventory API, as the administrator of the e2e server.
func unitsAt(ctx context.Context, env *e2e.Env, org string, at time.Time) (unitsAtDoc, string, error) {
	q := url.Values{}
	q.Set("at", strconv.FormatInt(at.UnixMilli(), 10))

	path := "/api/dustpan/v1/namespaces/" + url.PathEscape(org) + "/units?" + q.Encode()

	status, body, err := env.APIStatus(ctx, http.MethodGet, path, "")
	if err != nil {
		return unitsAtDoc{}, "", fmt.Errorf("get %s: %w", path, err)
	}

	if status != http.StatusOK {
		return unitsAtDoc{}, "", fmt.Errorf("%w: get %s: status %d (%s)", errUnexpected, path, status, body)
	}

	var doc unitsAtDoc
	if err := json.Unmarshal([]byte(body), &doc); err != nil {
		return unitsAtDoc{}, "", fmt.Errorf("decode %s: %w", path, err)
	}

	return doc, body, nil
}

// unitAtNames are the names a check expects for one unit of the view.
type unitAtNames struct {
	tool    string
	atTime  string
	current string
}

// checkUnitAtDeleted checks that the view of the namespace of u at the time
// at lists u under the name it had then, with the name it has now, and with
// the mark of a soft-deleted unit and the time of the delete.
func checkUnitAtDeleted(ctx context.Context, env *e2e.Env, u unitTarget, at time.Time, want unitAtNames) error {
	doc, _, err := unitsAt(ctx, env, u.org, at)
	if err != nil {
		return err
	}

	for i := range doc.Units {
		got := doc.Units[i]
		if got.UnitID != u.id {
			continue
		}

		if _, parseErr := time.Parse(time.RFC3339Nano, got.DeletedAt); parseErr != nil || !got.Deleted ||
			got.Name != want.atTime || got.CurrentName != want.current ||
			!strings.HasSuffix(got.Reference, ":"+u.org+"/"+want.atTime) {
			return fmt.Errorf("%w: unit %s at %d = %+v, want the name %q, the current name %q and the mark deleted",
				errUnexpected, u.id, at.UnixMilli(), got, want.atTime, want.current)
		}

		return nil
	}

	return fmt.Errorf("%w: the view of %s at %d lists no unit %s: %+v", errUnexpected, u.org, at.UnixMilli(), u.id, doc)
}

// checkUnitAtPurged checks that the view of the namespace of u at the time at
// counts a purged unit and does not name u: not by its ID and not by the name
// that it had.
func checkUnitAtPurged(ctx context.Context, env *e2e.Env, u unitTarget, at time.Time) error {
	doc, body, err := unitsAt(ctx, env, u.org, at)
	if err != nil {
		return err
	}

	if doc.Purged < 1 {
		return fmt.Errorf("%w: the view of %s at %d counts %d purged units, want 1 or more",
			errUnexpected, u.org, at.UnixMilli(), doc.Purged)
	}

	for i := range doc.Units {
		if doc.Units[i].UnitID == u.id {
			return fmt.Errorf(
				"%w: the view of %s at %d names the purged unit %s",
				errUnexpected,
				u.org,
				at.UnixMilli(),
				u.id,
			)
		}
	}

	if strings.Contains(body, u.id) {
		return fmt.Errorf(
			"%w: the view of %s at %d holds the ID of the purged unit %s",
			errUnexpected,
			u.org,
			at.UnixMilli(),
			u.id,
		)
	}

	return nil
}

// noteTime returns the step action that stores the current time in at, and
// waits two milliseconds, so that every later write has a later time.
func noteTime(at *time.Time) func(e2e.Result) error {
	return func(e2e.Result) error {
		recordTime(at)

		return nil
	}
}

// recordTime stores the current time in at, and waits two milliseconds, so
// that every later write has a later time.
func recordTime(at *time.Time) {
	*at = time.Now()

	<-time.After(2 * time.Millisecond)
}

// checkUnitAtRenamed checks that the view of the namespace org at the time at
// lists the unit with the reference at the old name, with the name that the
// unit has now, and with no mark of a delete.
func checkUnitAtRenamed(
	ctx context.Context, env *e2e.Env, org string, at time.Time, want unitAtNames,
) func(e2e.Result) error {
	return func(e2e.Result) error {
		doc, _, err := unitsAt(ctx, env, org, at)
		if err != nil {
			return err
		}

		reference := want.tool + ":" + org + "/" + want.atTime
		for i := range doc.Units {
			got := doc.Units[i]
			if got.Reference != reference {
				continue
			}

			if got.Name != want.atTime || got.CurrentName != want.current || got.Deleted || got.DeletedAt != "" {
				return fmt.Errorf("%w: unit %s at %d = %+v, want the current name %q and no delete mark",
					errUnexpected, reference, at.UnixMilli(), got, want.current)
			}

			return nil
		}

		return fmt.Errorf("%w: the view of %s at %d lists no unit %s: %+v",
			errUnexpected, org, at.UnixMilli(), reference, doc)
	}
}
