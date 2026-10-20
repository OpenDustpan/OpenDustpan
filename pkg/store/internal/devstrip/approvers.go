package devstrip

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"opendustpan.org/opendustpan/pkg/backup"
	"opendustpan.org/opendustpan/pkg/gate"
	"opendustpan.org/opendustpan/pkg/principal"
)

// approverPage is the number of objects that one read of gate_objects returns.
// The strip reads a page, closes the cursor, and then writes, so a write never
// runs while a cursor of the same transaction is open.
const approverPage = 100

// The kinds of the gate objects that name approvers by subject.
const (
	kindApprovalRule    = "approval-rule"
	kindApprovalDefault = "approval-default"
	kindOwnershipRule   = "ownership-rule"
)

// errApproverBody reports a body of a gate object that names approvers and
// that the strip cannot read. The strip cannot prove that such a body holds no
// dev subject, so it fails closed.
var errApproverBody = errors.New("the body of a gate object that names approvers cannot be read")

// object is one stored gate object that names approvers.
type object struct {
	namespace, scope, kind, id, body string
}

// stripped is the result of the strip on one body.
type stripped struct {
	body     string
	removed  int
	replaced bool
}

// stripBody removes the dev subjects from approvers.users of one body. When
// the removal leaves no approver at all (no group, no user, no unit owners),
// it writes Users with the value gate.NoApprover, so the list is never empty
// and the approval never widens. It reads each member of the body as raw JSON
// and changes only approvers.users, so it keeps every other member as it is.
// A body that it cannot read returns errApproverBody.
func stripBody(body string) (stripped, error) {
	doc, approvers, users, err := readApprovers(body)
	if err != nil {
		return stripped{}, err
	}

	if approvers == nil {
		return stripped{body: body, removed: 0, replaced: false}, nil
	}

	kept := make([]string, 0, len(users))
	for _, user := range users {
		if !principal.IsDevSubject(user) {
			kept = append(kept, user)
		}
	}

	removed := len(users) - len(kept)
	if removed == 0 {
		return stripped{body: body, removed: 0, replaced: false}, nil
	}

	replaced, err := namesNobody(approvers, kept)
	if err != nil {
		return stripped{}, err
	}

	if replaced {
		kept = []string{gate.NoApprover}
	}

	usersJSON, err := json.Marshal(kept)
	if err != nil {
		return stripped{}, fmt.Errorf("%w", errApproverBody)
	}

	approvers["users"] = usersJSON

	approversJSON, err := json.Marshal(approvers)
	if err != nil {
		return stripped{}, fmt.Errorf("%w", errApproverBody)
	}

	doc["approvers"] = approversJSON

	out, err := json.Marshal(doc)
	if err != nil {
		return stripped{}, fmt.Errorf("%w", errApproverBody)
	}

	return stripped{body: string(out), removed: removed, replaced: replaced}, nil
}

// readApprovers decodes the body, its approvers member and the users of it.
// The approvers member is nil when the body has none.
func readApprovers(body string) (map[string]json.RawMessage, map[string]json.RawMessage, []string, error) {
	var doc map[string]json.RawMessage
	if err := json.Unmarshal([]byte(body), &doc); err != nil {
		return nil, nil, nil, fmt.Errorf("%w", errApproverBody)
	}

	raw, ok := doc["approvers"]
	if !ok || string(raw) == "null" {
		return doc, nil, nil, nil
	}

	var approvers map[string]json.RawMessage
	if err := json.Unmarshal(raw, &approvers); err != nil {
		return nil, nil, nil, fmt.Errorf("%w", errApproverBody)
	}

	var users []string
	if usersRaw, has := approvers["users"]; has {
		if err := json.Unmarshal(usersRaw, &users); err != nil {
			return nil, nil, nil, fmt.Errorf("%w", errApproverBody)
		}
	}

	return doc, approvers, users, nil
}

// namesNobody reports whether the approvers would name nobody if their users
// were kept: no group, no user and no unit owners.
func namesNobody(approvers map[string]json.RawMessage, kept []string) (bool, error) {
	if len(kept) > 0 {
		return false, nil
	}

	var groups []string
	if raw, ok := approvers["groups"]; ok {
		if err := json.Unmarshal(raw, &groups); err != nil {
			return false, fmt.Errorf("%w", errApproverBody)
		}
	}

	var unitOwners bool
	if raw, ok := approvers["unitOwners"]; ok {
		if err := json.Unmarshal(raw, &unitOwners); err != nil {
			return false, fmt.Errorf("%w", errApproverBody)
		}
	}

	return len(groups) == 0 && !unitOwners, nil
}

// eachObject reads the gate objects that name approvers, one page at a time in
// the order of the primary key, and calls fn for each one. fn may write to the
// transaction: no cursor is open while it runs.
func eachObject(ctx context.Context, q Querier, fn func(object) error) error {
	after := object{namespace: "", scope: "", kind: "", id: "", body: ""}

	for {
		page, err := readPage(ctx, q, after)
		if err != nil {
			return err
		}

		for _, obj := range page {
			if err := fn(obj); err != nil {
				return err
			}
		}

		if len(page) < approverPage {
			return nil
		}

		after = page[len(page)-1]
	}
}

// readPage reads the page of objects after the key of after.
func readPage(ctx context.Context, q Querier, after object) ([]object, error) {
	rows, err := q.Query(ctx,
		"SELECT namespace_id, scope_key, kind, id, body FROM gate_objects WHERE kind IN ('"+
			kindApprovalRule+"', '"+kindApprovalDefault+"', '"+kindOwnershipRule+"')"+
			" AND (namespace_id, scope_key, kind, id) > (?, ?, ?, ?)"+
			" ORDER BY namespace_id, scope_key, kind, id LIMIT "+fmt.Sprint(approverPage),
		after.namespace, after.scope, after.kind, after.id,
	)
	if err != nil {
		return nil, fmt.Errorf("read the approvers of the staged gate objects: %w", backup.ErrExportRead)
	}

	var page []object

	for rows.Next() {
		var obj object
		if err := rows.Scan(&obj.namespace, &obj.scope, &obj.kind, &obj.id, &obj.body); err != nil {
			_ = rows.Close() //nolint:errcheck // The scan error is the cause.

			return nil, fmt.Errorf("read the approvers of the staged gate objects: %w", backup.ErrExportRead)
		}

		page = append(page, obj)
	}

	err = rows.Err()
	if closeErr := rows.Close(); closeErr != nil && err == nil {
		err = closeErr
	}

	if err != nil {
		return nil, fmt.Errorf("read the approvers of the staged gate objects: %w", backup.ErrExportRead)
	}

	return page, nil
}

// countApprovers returns the number of dev subjects in the approvers of the
// staged gate objects. It reads only.
func countApprovers(ctx context.Context, q Querier) (int64, error) {
	var total int64

	err := eachObject(ctx, q, func(obj object) error {
		_, _, users, err := readApprovers(obj.body)
		if err != nil {
			return err
		}

		for _, user := range users {
			if principal.IsDevSubject(user) {
				total++
			}
		}

		return nil
	})
	if err != nil {
		return 0, err
	}

	return total, nil
}

// removeApprovers removes the dev subjects from the approvers of the staged
// gate objects and adds them to the report.
func removeApprovers(ctx context.Context, q Querier, _ Dialect, r *reporter) error {
	return eachObject(ctx, q, func(obj object) error {
		result, err := stripBody(obj.body)
		if err != nil {
			return err
		}

		if result.removed == 0 {
			return nil
		}

		_, err = q.Exec(ctx,
			"UPDATE gate_objects SET body = ? WHERE namespace_id = ? AND scope_key = ? AND kind = ? AND id = ?",
			result.body, obj.namespace, obj.scope, obj.kind, obj.id,
		)
		if err != nil {
			return fmt.Errorf("write the approvers of a staged gate object: %w", backup.ErrExportRead)
		}

		r.add(backup.RemovedApprovers, int64(result.removed))

		if len(r.report.Approvers) < backup.MaxReportedApprovers {
			scope, _ := backup.CleanSubject(obj.scope)
			id, _ := backup.CleanSubject(obj.id)

			r.report.Approvers = append(r.report.Approvers, backup.DevApproverRemoval{
				Kind: obj.kind, Scope: scope, ID: id, Replaced: result.replaced,
			})
		}

		return nil
	})
}
