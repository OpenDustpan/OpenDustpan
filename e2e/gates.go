//go:build e2e

package e2e

import (
	"context"
	"errors"
	"fmt"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/gate"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// gateEntry returns the audit entry of a gate write of the scenarios.
func gateEntry(ns namespace.Name) audit.Entry {
	return audit.Entry{
		At: time.Now(), Namespace: ns, Subject: Subject, IDPSubject: "", TokenID: "",
		Action: "namespace:manage-gates", Object: ns.String(), Version: "", ValuePath: "", ValueCount: nil,
		Changes: nil, SourceIP: "", UserAgent: "", Channel: audit.ChannelAPI, Kind: audit.KindChange,
		Outcome: audit.OutcomeSuccess, RequestID: "", ObjectID: "", PrevHash: "", Hash: "", Sequence: 0,
		FenceVersion: 0, Reason: "", PrincipalKind: "", JobID: "",
	}
}

// errBinaryGates reports a gate write against the binary server, which holds
// its database open while it runs.
var errBinaryGates = errors.New("gate writes need the in-process server")

// withGateStore runs fn on the gate store of the in-process server with the
// fence of ns. The gate configuration has no route yet, so the scenarios write
// it through the store. The binary server holds its database open, so it
// gives an error.
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

// PutGate saves g in the namespace ns as a new gate.
func (e *Env) PutGate(ctx context.Context, ns namespace.Name, g gate.Gate) error {
	g.CheckedWith = gate.CheckedWith{Engine: "e2e", Schema: gate.SchemaVersion{
		Subject: string(g.DecisionPoint), Major: 1, Minor: 0,
	}}

	return e.withGateStore(ctx, ns, func(st gate.Store, fence namespace.Fence) error {
		_, err := st.Gates().Put(ctx, g, 0, fence, gateEntry(ns))

		return err
	})
}

// DeleteGate removes the gate id of scope.
func (e *Env) DeleteGate(ctx context.Context, ns namespace.Name, scope gate.Scope, id gate.ID) error {
	return e.withGateStore(ctx, ns, func(st gate.Store, fence namespace.Fence) error {
		g, err := st.Gates().Get(ctx, scope, id)
		if err != nil {
			return err
		}

		return st.Gates().Delete(ctx, g.Ref(), fence, gateEntry(ns))
	})
}

// PutAccessLimit saves l in the namespace ns as a new access limit.
func (e *Env) PutAccessLimit(ctx context.Context, ns namespace.Name, l gate.AccessLimit) error {
	return e.withGateStore(ctx, ns, func(st gate.Store, fence namespace.Fence) error {
		_, err := st.AccessLimits().Put(ctx, l, 0, fence, gateEntry(ns))

		return err
	})
}

// DeleteAccessLimit removes the access limit id of scope.
func (e *Env) DeleteAccessLimit(ctx context.Context, ns namespace.Name, scope gate.Scope, id gate.ID) error {
	return e.withGateStore(ctx, ns, func(st gate.Store, fence namespace.Fence) error {
		l, err := st.AccessLimits().Get(ctx, scope, id)
		if err != nil {
			return err
		}

		return st.AccessLimits().Delete(ctx, l.Ref(), fence, gateEntry(ns))
	})
}

// PutApprovalRule saves r in the namespace ns as a new approval rule.
func (e *Env) PutApprovalRule(ctx context.Context, ns namespace.Name, r gate.ApprovalRule) error {
	return e.withGateStore(ctx, ns, func(st gate.Store, fence namespace.Fence) error {
		_, err := st.ApprovalRules().Put(ctx, r, 0, fence, gateEntry(ns))

		return err
	})
}

// DeleteApprovalRule removes the approval rule id of scope.
func (e *Env) DeleteApprovalRule(ctx context.Context, ns namespace.Name, scope gate.Scope, id gate.ID) error {
	return e.withGateStore(ctx, ns, func(st gate.Store, fence namespace.Fence) error {
		r, err := st.ApprovalRules().Get(ctx, scope, id)
		if err != nil {
			return err
		}

		return st.ApprovalRules().Delete(ctx, r.Ref(), fence, gateEntry(ns))
	})
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
