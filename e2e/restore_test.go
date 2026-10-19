//go:build e2e

package e2e_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"opendustpan.org/opendustpan/e2e"
)

// deletedUnitDoc is one entry of GET /api/dustpan/v1/namespaces/{namespace}/deleted-units.
type deletedUnitDoc struct {
	Tool       string `json:"tool"`
	Reference  string `json:"reference"`
	Namespace  string `json:"namespace"`
	Name       string `json:"name"`
	UnitID     string `json:"unitID"`
	DeletedAt  string `json:"deletedAt"`
	PurgeAfter string `json:"purgeAfter"`
}

// deletedUnitsDoc is the answer of GET /api/dustpan/v1/namespaces/{namespace}/deleted-units.
type deletedUnitsDoc struct {
	NextPageToken string           `json:"nextPageToken"`
	Units         []deletedUnitDoc `json:"units"`
	Total         int              `json:"total"`
}

// restoredUnitDoc is the answer of a restore: the live unit.
type restoredUnitDoc struct {
	Tool      string `json:"tool"`
	Reference string `json:"reference"`
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
	UnitID    string `json:"unitID"`
}

// unitTarget names one deleted unit for the inventory API: its namespace,
// its tool code and its stable ID.
type unitTarget struct {
	org  string
	tool string
	id   string
}

// listDeletedUnits reads the deleted units of the namespace of u whose tool
// is the tool of u and whose name starts with prefix, through the inventory
// API.
func listDeletedUnits(ctx context.Context, env *e2e.Env, u unitTarget, prefix string) (deletedUnitsDoc, error) {
	q := url.Values{}
	q.Set("tool", u.tool)
	q.Set("namePrefix", prefix)

	path := "/api/dustpan/v1/namespaces/" + url.PathEscape(u.org) + "/deleted-units?" + q.Encode()

	status, body, err := env.APIStatus(ctx, http.MethodGet, path, "")
	if err != nil {
		return deletedUnitsDoc{}, fmt.Errorf("get %s: %w", path, err)
	}

	if status != http.StatusOK {
		return deletedUnitsDoc{}, fmt.Errorf("%w: get %s: status %d (%s)", errUnexpected, path, status, body)
	}

	var doc deletedUnitsDoc
	if err := json.Unmarshal([]byte(body), &doc); err != nil {
		return deletedUnitsDoc{}, fmt.Errorf("decode %s: %w", path, err)
	}

	return doc, nil
}

// checkDeletedListed checks that the deleted units hold u under name, with a
// retention end after its delete time.
func checkDeletedListed(ctx context.Context, env *e2e.Env, u unitTarget, name string) error {
	doc, err := listDeletedUnits(ctx, env, u, name)
	if err != nil {
		return err
	}

	for i := range doc.Units {
		got := doc.Units[i]
		if got.UnitID != u.id {
			continue
		}

		if got.Name != name || !retentionEndsAfterDelete(got) {
			return fmt.Errorf(
				"%w: deleted unit %s = %+v, want name %s and a retention end",
				errUnexpected,
				u.id,
				got,
				name,
			)
		}

		return nil
	}

	return fmt.Errorf("%w: deleted units of %s hold no unit %s: %+v", errUnexpected, u.org, u.id, doc)
}

// retentionEndsAfterDelete reports whether both times of u are RFC 3339
// times and the retention ends after the delete.
func retentionEndsAfterDelete(u deletedUnitDoc) bool {
	deleted, err := time.Parse(time.RFC3339Nano, u.DeletedAt)
	if err != nil {
		return false
	}

	purge, err := time.Parse(time.RFC3339Nano, u.PurgeAfter)
	if err != nil {
		return false
	}

	return purge.After(deleted)
}

// checkDeletedAbsent checks that the deleted units whose name starts with
// name do not hold u.
func checkDeletedAbsent(ctx context.Context, env *e2e.Env, u unitTarget, name string) error {
	doc, err := listDeletedUnits(ctx, env, u, name)
	if err != nil {
		return err
	}

	for i := range doc.Units {
		if doc.Units[i].UnitID == u.id {
			return fmt.Errorf("%w: deleted units of %s still hold %s", errUnexpected, u.org, u.id)
		}
	}

	return nil
}

// sweptUnitDoc is one unit that a sweep purged.
type sweptUnitDoc struct {
	Tool      string `json:"tool"`
	Namespace string `json:"namespace"`
	UnitID    string `json:"unitID"`
	Reference string `json:"reference"`
}

// sweepDoc is the answer of POST /api/server/deleted-units/sweep.
type sweepDoc struct {
	Purged             []sweptUnitDoc `json:"purged"`
	Failed             int            `json:"failed"`
	FinishedBlobPurges int            `json:"finishedBlobPurges"`
	PendingBlobPurges  int            `json:"pendingBlobPurges"`
}

// sweepPath is the route that runs one retention sweep now.
const sweepPath = "/api/server/deleted-units/sweep"

// sweepDeleted runs one retention sweep through the installation API.
func sweepDeleted(ctx context.Context, env *e2e.Env) (sweepDoc, error) {
	status, body, err := env.ServerAPIStatus(ctx, http.MethodPost, sweepPath, "")
	if err != nil {
		return sweepDoc{}, fmt.Errorf("post %s: %w", sweepPath, err)
	}

	if status != http.StatusOK {
		return sweepDoc{}, fmt.Errorf("%w: post %s: status %d (%s)", errUnexpected, sweepPath, status, body)
	}

	var doc sweepDoc
	if err := json.Unmarshal([]byte(body), &doc); err != nil {
		return sweepDoc{}, fmt.Errorf("decode %s: %w", sweepPath, err)
	}

	return doc, nil
}

// checkSweepKeeps runs one sweep and checks that it purged no unit and
// finished no blob purge record: the retention of every deleted unit and the
// grace period of every record are still running. Then the deleted units
// must still hold u under name.
func checkSweepKeeps(ctx context.Context, env *e2e.Env, u unitTarget, name string) error {
	doc, err := sweepDeleted(ctx, env)
	if err != nil {
		return err
	}

	if len(doc.Purged) != 0 || doc.Failed != 0 || doc.FinishedBlobPurges != 0 {
		return fmt.Errorf("%w: sweep = %+v, want no purge, no failure and no finished record", errUnexpected, doc)
	}

	return checkDeletedListed(ctx, env, u, name)
}

// checkSweepKeepsRecord runs one sweep after the purge of a unit and checks
// that it keeps the blob purge record of the unit: the grace period of the
// record is still running, so at least one record stays pending.
func checkSweepKeepsRecord(ctx context.Context, env *e2e.Env) error {
	doc, err := sweepDeleted(ctx, env)
	if err != nil {
		return err
	}

	if doc.FinishedBlobPurges != 0 || doc.PendingBlobPurges < 1 || doc.Failed != 0 {
		return fmt.Errorf("%w: sweep after a purge = %+v, want one pending record or more and none finished",
			errUnexpected, doc)
	}

	return nil
}

// unitPath is the path of the deleted unit u in the inventory API, with
// suffix after its ID.
func unitPath(u unitTarget, suffix string) string {
	return "/api/inventory/deleted-units/" + u.tool + "/" + u.id + suffix + "?organization=" + url.QueryEscape(u.org)
}

// restoreUnit restores u, under newName when it is not empty, and returns
// the status and the body.
func restoreUnit(ctx context.Context, env *e2e.Env, u unitTarget, newName string) (int, string, error) {
	path := unitPath(u, "/restore")

	body := "{}"
	if newName != "" {
		raw, err := json.Marshal(map[string]string{"name": newName})
		if err != nil {
			return 0, "", fmt.Errorf("encode restore body: %w", err)
		}

		body = string(raw)
	}

	status, answer, err := env.APIStatus(ctx, http.MethodPost, path, body)
	if err != nil {
		return 0, "", fmt.Errorf("post %s: %w", path, err)
	}

	return status, answer, nil
}

// restoreCase is one restore call and its expected answer: the new name
// that the request sends (empty for none), the name that a 200 answer must
// show, and the status.
type restoreCase struct {
	newName  string
	wantName string
	want     int
}

// checkRestore restores u as rc asks and checks the status. On 200 it also
// checks that the answer names the same unit under rc.wantName.
func checkRestore(ctx context.Context, env *e2e.Env, u unitTarget, rc restoreCase) error {
	status, body, err := restoreUnit(ctx, env, u, rc.newName)
	if err != nil {
		return err
	}

	if status != rc.want {
		return fmt.Errorf("%w: restore %s %s as %q: status %d, want %d (%s)",
			errUnexpected, u.tool, u.id, rc.newName, status, rc.want, body)
	}

	if rc.want != http.StatusOK {
		return nil
	}

	var doc restoredUnitDoc
	if err := json.Unmarshal([]byte(body), &doc); err != nil {
		return fmt.Errorf("decode restore answer: %w", err)
	}

	if doc.UnitID != u.id || doc.Name != rc.wantName {
		return fmt.Errorf("%w: restore answer = %+v, want unit %s named %s", errUnexpected, doc, u.id, rc.wantName)
	}

	return nil
}

// checkPurge purges u and checks the status.
func checkPurge(ctx context.Context, env *e2e.Env, u unitTarget, want int) error {
	path := unitPath(u, "")

	status, body, err := env.APIStatus(ctx, http.MethodDelete, path, "")
	if err != nil {
		return fmt.Errorf("delete %s: %w", path, err)
	}

	if status != want {
		return fmt.Errorf("%w: purge %s %s: status %d, want %d (%s)", errUnexpected, u.tool, u.id, status, want, body)
	}

	return nil
}

// restoredStack is the new name of the restored Pulumi stack.
const restoredStack = "scratch-restored"

// pulumiRestoreSteps run after the soft delete steps, while a new stack holds
// the name of the deleted stack removedID. The deleted list holds the stack.
// A restore under its old name answers 409, a restore under a new name brings
// back the same stack ID, and a purge removes it for good: a restore after
// the purge answers 404.
func pulumiRestoreSteps(ctx context.Context, env *e2e.Env, org, removedID string, existedAt *time.Time) []e2e.StepSpec {
	oldName := e2e.Project + "/" + softDeleted
	newName := e2e.Project + "/" + restoredStack
	qualified := fmt.Sprintf("%s/%s/%s", org, e2e.Project, restoredStack)
	u := unitTarget{org: org, tool: "pulumi", id: removedID}

	return []e2e.StepSpec{
		step("deleted list holds the removed stack", func(e2e.Result) error {
			return checkDeletedListed(ctx, env, u, oldName)
		}, "stack", "ls", "--json", "--all"),
		step("retention sweep keeps the removed stack", func(e2e.Result) error {
			return checkSweepKeeps(ctx, env, u, oldName)
		}, "stack", "ls", "--json", "--all"),
		step("restore under a taken name answers 409", func(e2e.Result) error {
			return checkRestore(ctx, env, u, restoreCase{newName: "", wantName: "", want: http.StatusConflict})
		}, "stack", "ls", "--json", "--all"),
		step("restore under a new name", func(e2e.Result) error {
			return checkRestore(
				ctx,
				env,
				u,
				restoreCase{newName: restoredStack, wantName: newName, want: http.StatusOK},
			)
		}, "stack", "ls", "--json", "--all"),
		step("restored stack keeps its stable ID", func(e2e.Result) error {
			id, status, err := env.StackID(ctx, org, e2e.Project, restoredStack)
			if err != nil {
				return fmt.Errorf("get stack %s: %w", restoredStack, err)
			}

			if status != http.StatusOK || id != removedID {
				return fmt.Errorf(
					"%w: restored stack = %s (status %d), want ID %s",
					errUnexpected,
					id,
					status,
					removedID,
				)
			}

			return checkDeletedAbsent(ctx, env, u, oldName)
		}, "stack", "select", qualified),
		step("stack rm restored", nil, "stack", "rm", "--yes", "--stack", qualified),
		step("the inventory at a time names the deleted stack by both names", func(e2e.Result) error {
			return checkUnitAtDeleted(ctx, env, u, *existedAt, unitAtNames{tool: "", atTime: oldName, current: newName})
		}, "stack", "ls", "--json", "--all"),
		step("purge the removed stack", func(e2e.Result) error {
			if err := checkPurge(ctx, env, u, http.StatusNoContent); err != nil {
				return err
			}

			if err := checkDeletedAbsent(ctx, env, u, newName); err != nil {
				return err
			}

			return checkRestore(ctx, env, u, restoreCase{newName: "", wantName: "", want: http.StatusNotFound})
		}, "stack", "ls", "--json", "--all"),
		step("the inventory at a time counts the purged stack and never names it", func(e2e.Result) error {
			return checkUnitAtPurged(ctx, env, u, *existedAt)
		}, "stack", "ls", "--json", "--all"),
		step("retention sweep keeps the blob purge record of the stack", func(e2e.Result) error {
			return checkSweepKeepsRecord(ctx, env)
		}, "stack", "ls", "--json", "--all"),
	}
}

// restoredWorkspace is the new name of the restored OpenTofu workspace,
// without the remote-backend prefix.
const restoredWorkspace = "restored"

// tofuRestoreSteps run after the workspace soft delete steps, while a new
// workspace holds the name of the deleted workspace. They restore the deleted
// workspace under a new name, which the CLI lists, then delete and purge it.
func tofuRestoreSteps(ctx context.Context, env *e2e.Env, removedID *string, existedAt *time.Time) []e2e.StepSpec {
	newName := e2e.RemotePrefix + restoredWorkspace
	// target reads the ID when a step runs: an earlier step sets it.
	target := func() unitTarget { return unitTarget{org: e2e.Namespace, tool: "opentofu", id: *removedID} }

	return []e2e.StepSpec{
		step("deleted list holds the deleted workspace", func(e2e.Result) error {
			return checkDeletedListed(ctx, env, target(), remoteProdName)
		}, "workspace", "list", "-no-color"),
		step("retention sweep keeps the deleted workspace", func(e2e.Result) error {
			return checkSweepKeeps(ctx, env, target(), remoteProdName)
		}, "workspace", "list", "-no-color"),
		step("restore workspace under a taken name answers 409", func(e2e.Result) error {
			return checkRestore(ctx, env, target(), restoreCase{newName: "", wantName: "", want: http.StatusConflict})
		}, "workspace", "list", "-no-color"),
		step("restore workspace under a new name", func(e2e.Result) error {
			return checkRestore(
				ctx,
				env,
				target(),
				restoreCase{newName: newName, wantName: newName, want: http.StatusOK},
			)
		}, "workspace", "list", "-no-color"),
		step("workspace list shows the restored workspace", func(r e2e.Result) error {
			id, err := remoteWorkspaceID(ctx, env, newName)
			if err != nil {
				return err
			}

			if id != *removedID {
				return fmt.Errorf("%w: restored workspace ID = %s, want %s", errUnexpected, id, *removedID)
			}

			return contains(restoredWorkspace)(r)
		}, "workspace", "list", "-no-color"),
		step("workspace delete restored", nil, "workspace", "delete", restoredWorkspace, "-no-color"),
		step("the inventory at a time names the deleted workspace by both names", func(e2e.Result) error {
			return checkUnitAtDeleted(
				ctx,
				env,
				target(),
				*existedAt,
				unitAtNames{tool: "", atTime: remoteProdName, current: newName},
			)
		}, "workspace", "list", "-no-color"),
		step("purge the deleted workspace", func(e2e.Result) error {
			if err := checkPurge(ctx, env, target(), http.StatusNoContent); err != nil {
				return err
			}

			if err := checkDeletedAbsent(ctx, env, target(), newName); err != nil {
				return err
			}

			return checkRestore(ctx, env, target(), restoreCase{newName: "", wantName: "", want: http.StatusNotFound})
		}, "workspace", "list", "-no-color"),
		step("the inventory at a time counts the purged workspace and never names it", func(e2e.Result) error {
			return checkUnitAtPurged(ctx, env, target(), *existedAt)
		}, "workspace", "list", "-no-color"),
		step("retention sweep keeps the blob purge record of the workspace", func(e2e.Result) error {
			return checkSweepKeepsRecord(ctx, env)
		}, "workspace", "list", "-no-color"),
	}
}
