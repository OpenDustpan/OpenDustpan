//go:build e2e

package e2e

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/gate"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// errGateRoute reports a gate configuration request that the server refused.
var errGateRoute = errors.New("gate configuration request refused")

// errGateScope reports a scope that no gate route serves.
var errGateScope = errors.New("no gate route serves this scope")

// errBinaryGates reports a read of the store against the binary server, which
// holds its database open while it runs.
var errBinaryGates = errors.New("this read needs the in-process server")

// gateWire is a gate on the wire of the gate routes.
type gateWire struct {
	DecisionPoint string `json:"decisionPoint"`
	Condition     string `json:"condition"`
	Unless        string `json:"unless"`
	Outcome       string `json:"outcome"`
	Message       string `json:"message"`
	Mode          string `json:"mode"`
	OnError       string `json:"onError"`
	Final         bool   `json:"final"`
}

// accessLimitWire is an access limit on the wire of the gate routes.
type accessLimitWire struct {
	Policy string `json:"policy"`
}

// approversWire is the approvers of an approval rule on the wire.
type approversWire struct {
	Groups     []string `json:"groups"`
	Users      []string `json:"users"`
	UnitOwners bool     `json:"unitOwners"`
}

// approvalRuleWire is an approval rule on the wire of the gate routes.
type approvalRuleWire struct {
	Count            int           `json:"count"`
	Approvers        approversWire `json:"approvers"`
	AuthorMayApprove bool          `json:"authorMayApprove"`
}

// gatePath returns the path of the object id of kind (gates, access-limits)
// at scope, or of the collection of the kind when id is empty. The server
// serves a gate and an access limit at a namespace, a collection and a unit.
func gatePath(scope gate.Scope, kind string, id gate.ID) (string, error) {
	base := "/api/dustpan/v1/namespaces/" + url.PathEscape(scope.Namespace().String())

	level := scope.Level()
	unit, isUnit := scope.Unit()

	switch {
	case level == gate.LevelNamespace:
	case level == gate.LevelCollection:
		base += "/collections/" + url.PathEscape(scope.Collection())
	case level == gate.LevelUnit && isUnit:
		base += "/units/by-name/" + url.PathEscape(unit.Tool()) + "/" + url.PathEscape(unit.UnitName())
	default:
		return "", fmt.Errorf("%w: %s", errGateScope, scope)
	}

	path := base + "/" + kind
	if id != "" {
		path += "/" + url.PathEscape(string(id))
	}

	return path, nil
}

// gateWrite sends one change of the gate configuration as the site
// administrator, with a console session that has a fresh sign-in: an access
// token never gives the fresh authentication that these routes need. The env
// must come from NewEnvWithSignIn or one of its variants.
func (e *Env) gateWrite(ctx context.Context, tb testing.TB, method, path string, doc any) error {
	tb.Helper()

	body := ""

	if doc != nil {
		raw, err := json.Marshal(doc)
		if err != nil {
			return fmt.Errorf("encode the request body: %w", err)
		}

		body = string(raw)
	}

	// A set answers 200 with the stored object, and a delete answers 204.
	want := http.StatusOK
	if method == http.MethodDelete {
		want = http.StatusNoContent
	}

	status, answer := e.FreshAdminAPIStatus(ctx, tb, SessionRequest{Method: method, Path: path, Body: body})
	if status != want {
		return fmt.Errorf("%w: %s %s answered %d, want %d: %s", errGateRoute, method, path, status, want, answer)
	}

	return nil
}

// PutGate saves g in its scope through the gate route. A gate with no mode
// starts in evaluate mode, as the route says.
func (e *Env) PutGate(ctx context.Context, tb testing.TB, g gate.Gate) error {
	tb.Helper()

	path, err := gatePath(g.Scope, "gates", g.ID)
	if err != nil {
		return err
	}

	doc := gateWire{
		DecisionPoint: string(g.DecisionPoint), Condition: g.Condition, Unless: g.Unless, Outcome: string(g.Outcome),
		Message: g.Message, Mode: string(g.Mode), OnError: string(g.OnError), Final: g.Final,
	}

	return e.gateWrite(ctx, tb, http.MethodPut, path, doc)
}

// DeleteGate removes the gate id of scope through the gate route.
func (e *Env) DeleteGate(ctx context.Context, tb testing.TB, scope gate.Scope, id gate.ID) error {
	tb.Helper()

	path, err := gatePath(scope, "gates", id)
	if err != nil {
		return err
	}

	return e.gateWrite(ctx, tb, http.MethodDelete, path, nil)
}

// PutAccessLimit saves l in its scope through the access limit route.
func (e *Env) PutAccessLimit(ctx context.Context, tb testing.TB, l gate.AccessLimit) error {
	tb.Helper()

	path, err := gatePath(l.Scope, "access-limits", l.ID)
	if err != nil {
		return err
	}

	return e.gateWrite(ctx, tb, http.MethodPut, path, accessLimitWire{Policy: l.Policy})
}

// DeleteAccessLimit removes the access limit id of scope through the access
// limit route.
func (e *Env) DeleteAccessLimit(ctx context.Context, tb testing.TB, scope gate.Scope, id gate.ID) error {
	tb.Helper()

	path, err := gatePath(scope, "access-limits", id)
	if err != nil {
		return err
	}

	return e.gateWrite(ctx, tb, http.MethodDelete, path, nil)
}

// PutApprovalRule saves r in its namespace through the approval rule route.
func (e *Env) PutApprovalRule(ctx context.Context, tb testing.TB, r gate.ApprovalRule) error {
	tb.Helper()

	path, err := gatePath(r.Scope, "approval-rules", r.ID)
	if err != nil {
		return err
	}

	if r.Scope.Level() != gate.LevelNamespace {
		return fmt.Errorf("%w: %s", errGateScope, r.Scope)
	}

	doc := approvalRuleWire{
		Count:            r.Count,
		AuthorMayApprove: r.AuthorMayApprove,
		Approvers: approversWire{
			Groups:     r.Approvers.Groups,
			Users:      r.Approvers.Users,
			UnitOwners: r.Approvers.UnitOwners,
		},
	}

	return e.gateWrite(ctx, tb, http.MethodPut, path, doc)
}

// DeleteApprovalRule removes the approval rule id of scope through the
// approval rule route.
func (e *Env) DeleteApprovalRule(ctx context.Context, tb testing.TB, scope gate.Scope, id gate.ID) error {
	tb.Helper()

	path, err := gatePath(scope, "approval-rules", id)
	if err != nil {
		return err
	}

	if scope.Level() != gate.LevelNamespace {
		return fmt.Errorf("%w: %s", errGateScope, scope)
	}

	return e.gateWrite(ctx, tb, http.MethodDelete, path, nil)
}

// withGateStore runs fn on the gate store of the in-process server with the
// fence of ns. Only a read uses it: the decision records have no route. The
// binary server holds its database open, so it gives an error.
func (e *Env) withGateStore(ctx context.Context, ns namespace.Name, fn func(gate.Store, namespace.Fence) error) error {
	if e.binPath != "" || e.inprocess == nil {
		return errBinaryGates
	}

	gs, ok := e.inprocess.(gate.Store)
	if !ok {
		return fmt.Errorf("%w: the store %T keeps no gates", errBinaryGates, e.inprocess)
	}

	n, err := e.inprocess.GetNamespace(ctx, ns)
	if err != nil {
		return fmt.Errorf("namespace %s: %w", ns, err)
	}

	return fn(gs, n.Fence())
}

// AuditEntries returns the audit entries of the namespace ns, newest first.
// It reads the store of the in-process server.
func (e *Env) AuditEntries(ctx context.Context, ns namespace.Name) ([]audit.Entry, error) {
	if e.binPath != "" || e.inprocess == nil {
		return nil, errBinaryGates
	}

	st, ok := any(e.inprocess).(audit.Store)
	if !ok {
		return nil, fmt.Errorf("%w: the store %T keeps no audit log", errBinaryGates, e.inprocess)
	}

	listing, err := st.List(ctx, ns, audit.Page{Number: 1, Size: audit.MaxPageSize})
	if err != nil {
		return nil, fmt.Errorf("audit entries of %s: %w", ns, err)
	}

	return listing.Entries, nil
}

// StateWriteRecords returns the decision records of state.write in ns, newest
// first.
func (e *Env) StateWriteRecords(ctx context.Context, ns namespace.Name) ([]gate.Record, error) {
	var records []gate.Record

	err := e.withGateStore(ctx, ns, func(st gate.Store, _ namespace.Fence) error {
		var err error

		records, err = st.Records().Query(ctx, ns, gate.RecordQuery{
			Point: gate.PointStateWrite, From: time.Unix(0, 0), To: time.Now().Add(time.Hour),
			Limit: gate.MaxSimulateRecords,
		})

		return err
	})

	return records, err
}
