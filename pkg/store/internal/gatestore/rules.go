package gatestore

import (
	"context"
	"fmt"
	"strings"

	"opendustpan.org/opendustpan/pkg/collection"
	"opendustpan.org/opendustpan/pkg/gate"
)

// widerScopes returns the scopes before scope in its chain, widest first. A
// unit has the collection that holds it now, when it has one.
func widerScopes(ctx context.Context, tx Tx, scope gate.Scope) ([]gate.Scope, error) {
	switch scope.Level() { //nolint:exhaustive // The installation has no wider scope, and a blueprint version or a profile holds none of its own.
	case gate.LevelNamespace:
		return []gate.Scope{gate.InstallationScope()}, nil
	case gate.LevelCollection:
		return []gate.Scope{gate.InstallationScope(), gate.NamespaceScope(scope.Namespace())}, nil
	case gate.LevelUnit:
		out := []gate.Scope{gate.InstallationScope(), gate.NamespaceScope(scope.Namespace())}

		key, ok, err := tx.UnitCollection(ctx, scope)
		if err != nil {
			return nil, err
		}

		if ok {
			out = append(out, gate.CollectionScope(scope.Namespace(), collection.Name(key)))
		}

		return out, nil
	default:
		return nil, nil
	}
}

// resolves reports whether scope or a wider scope holds the object ref.
func resolves(ctx context.Context, tx Tx, scope gate.Scope, ref Ref) (bool, error) {
	wider, err := widerScopes(ctx, tx, scope)
	if err != nil {
		return false, err
	}

	for _, s := range append([]gate.Scope{scope}, wider...) {
		if _, ok, gerr := tx.Get(ctx, s, ref.Kind, ref.ID); gerr != nil || ok {
			return ok, gerr
		}
	}

	return false, nil
}

// requireResolves returns gate.ErrInvalid when scope and its wider scopes
// hold no object ref.
func requireResolves(ctx context.Context, tx Tx, scope gate.Scope, ref Ref) error {
	ok, err := resolves(ctx, tx, scope, ref)
	if err != nil {
		return err
	}

	if !ok {
		return fmt.Errorf("%w: %s %q does not resolve at %s or a wider scope", gate.ErrInvalid, ref.Kind, ref.ID, scope)
	}

	return nil
}

// requireReads returns gate.ErrInvalid when a path set in reads does not
// resolve at scope.
func requireReads(ctx context.Context, tx Tx, scope gate.Scope, reads []gate.ID) error {
	for _, id := range reads {
		if err := requireResolves(ctx, tx, scope, Ref{Kind: RefPathSet, ID: string(id)}); err != nil {
			return err
		}
	}

	return nil
}

// scopeNames returns the scope keys of found, joined.
func scopeNames(found []Located) string {
	names := make([]string, len(found))
	for i, f := range found {
		names[i] = f.ScopeKey
	}

	return strings.Join(names, ", ")
}

// othersThan returns found without the row of scope.
func othersThan(found []Located, scope gate.Scope) []Located {
	var out []Located

	for _, f := range found {
		if f.ScopeKey != scope.Key() {
			out = append(out, f)
		}
	}

	return out
}

// widerHolders returns the rows of the object ref in the wider scopes of
// scope.
func widerHolders(ctx context.Context, tx Tx, scope gate.Scope, ref Ref) ([]Located, error) {
	wider, err := widerScopes(ctx, tx, scope)
	if err != nil {
		return nil, err
	}

	var out []Located

	for _, w := range wider {
		row, ok, gerr := tx.Get(ctx, w, ref.Kind, ref.ID)
		if gerr != nil {
			return nil, gerr
		}

		if ok {
			out = append(out, Located{ScopeKey: w.Key(), Row: row})
		}
	}

	return out, nil
}

// narrowerHolders returns the rows of the object ref in the narrower scopes of
// scope.
func narrowerHolders(ctx context.Context, tx Tx, scope gate.Scope, ref Ref) ([]Located, error) {
	all, err := tx.Narrower(ctx, scope, ref.Kind, ref.ID)
	if err != nil {
		return nil, err
	}

	return othersThan(all, scope), nil
}

// checkPut runs the checks across scopes of a Put. previous is the stored
// gate that obj replaces, or nil.
func checkPut(ctx context.Context, tx Tx, obj any, previous *gate.Gate, reason string) error {
	switch v := obj.(type) {
	case gate.Gate:
		return checkPutGate(ctx, tx, v, previous, reason)
	case gate.ApprovalRule:
		return checkPutShadowing(ctx, tx, shadow{
			scope: v.Scope, ref: Ref{Kind: RefApprovalRule, ID: string(v.ID)}, widerErr: gate.ErrFinal, reason: reason,
		})
	case gate.OwnershipRule:
		return requireReads(ctx, tx, v.Scope, v.Reads)
	case gate.PathSet:
		return checkPutShadowing(ctx, tx, shadow{
			scope: v.Scope, ref: Ref{Kind: RefPathSet, ID: string(v.ID)}, widerErr: gate.ErrInvalid, reason: reason,
		})
	default:
		return nil
	}
}

// checkDelete runs the checks across scopes of a Delete. current is the
// stored object.
func checkDelete(ctx context.Context, tx Tx, current any, reason string) error {
	switch v := current.(type) {
	case gate.Gate:
		if v.Final && reason == "" {
			return fmt.Errorf("%w: %s is final: give a reason to delete it", gate.ErrFinal, v.PolicyID())
		}

		return nil
	case gate.ApprovalRule:
		return checkDeleteReferenced(ctx, tx, v.Scope, Ref{Kind: RefApprovalRule, ID: string(v.ID)}, reason)
	case gate.PathSet:
		return checkDeleteReferenced(ctx, tx, v.Scope, Ref{Kind: RefPathSet, ID: string(v.ID)}, reason)
	default:
		return nil
	}
}

func checkPutGate(ctx context.Context, tx Tx, g gate.Gate, previous *gate.Gate, reason string) error {
	if rule, ok := g.Outcome.Rule(); ok {
		if err := requireResolves(ctx, tx, g.Scope, Ref{Kind: RefApprovalRule, ID: string(rule)}); err != nil {
			return err
		}
	}

	if err := requireReads(ctx, tx, g.Scope, g.Reads); err != nil {
		return err
	}

	if err := checkWiderFinal(ctx, tx, g); err != nil {
		return err
	}

	if g.Final {
		if err := checkNarrowerGates(ctx, tx, g); err != nil {
			return err
		}
	}

	if previous != nil && previous.Final && reason == "" && weaker(*previous, g) {
		return fmt.Errorf("%w: %s is final: give a reason to make it weaker", gate.ErrFinal, g.PolicyID())
	}

	return nil
}

// checkWiderFinal returns gate.ErrFinal when a wider scope of g holds a final
// gate with the ID of g.
func checkWiderFinal(ctx context.Context, tx Tx, g gate.Gate) error {
	wider, err := widerHolders(ctx, tx, g.Scope, Ref{Kind: KindGate, ID: string(g.ID)})
	if err != nil {
		return err
	}

	for _, w := range wider {
		ws, perr := ParseScope(w.ScopeKey)
		if perr != nil {
			return perr
		}

		other, derr := decodeGate(ws, w.Row)
		if derr != nil {
			return derr
		}

		if other.Final {
			return fmt.Errorf("%w: the final gate %s of a wider scope has this ID", gate.ErrFinal, other.PolicyID())
		}
	}

	return nil
}

// checkNarrowerGates returns gate.ErrFinal when a narrower scope of g holds a
// gate with the ID of g.
func checkNarrowerGates(ctx context.Context, tx Tx, g gate.Gate) error {
	narrower, err := narrowerHolders(ctx, tx, g.Scope, Ref{Kind: KindGate, ID: string(g.ID)})
	if err != nil {
		return err
	}

	if len(narrower) > 0 {
		return fmt.Errorf("%w: %s cannot be final: a gate with its ID is in %s", gate.ErrFinal, g.PolicyID(),
			scopeNames(narrower))
	}

	return nil
}

// weaker reports whether next is a weaker gate than cur: a switch to
// evaluate, a clear of Final, or a change of the decision point, the
// condition, Unless, the outcome or OnError.
func weaker(cur, next gate.Gate) bool {
	switch {
	case next.Mode == gate.ModeEvaluate && cur.Mode != gate.ModeEvaluate:
		return true
	case cur.Final && !next.Final:
		return true
	default:
		return cur.DecisionPoint != next.DecisionPoint || cur.Condition != next.Condition ||
			cur.Unless != next.Unless || cur.Outcome != next.Outcome || cur.OnError != next.OnError
	}
}

// shadow is a Put of an approval rule or a path set, for the check that no
// scope of the same chain holds its ID.
type shadow struct {
	scope gate.Scope
	ref   Ref
	// widerErr is the error for a wider scope that holds the ID.
	widerErr error
	reason   string
}

// checkPutShadowing runs the checks of a Put of an approval rule or a path
// set: no wider scope holds the ID (widerErr says what that is), no narrower
// scope holds it, and a final gate that names the object needs a reason.
func checkPutShadowing(ctx context.Context, tx Tx, p shadow) error {
	wider, err := widerHolders(ctx, tx, p.scope, p.ref)
	if err != nil {
		return err
	}

	if len(wider) > 0 {
		return fmt.Errorf("%w: the %s %q is set in %s, a wider scope", p.widerErr, p.ref.Kind, p.ref.ID,
			scopeNames(wider))
	}

	narrower, err := narrowerHolders(ctx, tx, p.scope, p.ref)
	if err != nil {
		return err
	}

	if len(narrower) > 0 {
		return fmt.Errorf("%w: the %s %q is set in %s, a narrower scope", gate.ErrInvalid, p.ref.Kind, p.ref.ID,
			scopeNames(narrower))
	}

	return requireNoFinalReader(ctx, tx, p.scope, p.ref, p.reason)
}

// requireNoFinalReader returns gate.ErrFinal when a final gate in scope or a
// narrower scope names ref, and reason is empty: a change of what a final
// gate names counts as a change of that gate.
func requireNoFinalReader(ctx context.Context, tx Tx, scope gate.Scope, ref Ref, reason string) error {
	if reason != "" {
		return nil
	}

	readers, err := tx.Referrers(ctx, scope, ref)
	if err != nil {
		return err
	}

	for _, r := range readers {
		g, ok, derr := finalGate(r)
		if derr != nil {
			return derr
		}

		if ok {
			return fmt.Errorf("%w: the final gate %s names the %s %q: give a reason to change it", gate.ErrFinal,
				g.PolicyID(), ref.Kind, ref.ID)
		}
	}

	return nil
}

// finalGate decodes r when it is a final gate.
func finalGate(r Located) (gate.Gate, bool, error) {
	if r.Row.Kind != KindGate {
		return zeroGate(), false, nil
	}

	scope, err := ParseScope(r.ScopeKey)
	if err != nil {
		return zeroGate(), false, err
	}

	g, err := decodeGate(scope, r.Row)
	if err != nil {
		return zeroGate(), false, err
	}

	return g, g.Final, nil
}

// zeroGate returns the empty gate.
func zeroGate() gate.Gate {
	return gate.Gate{
		Scope: gate.Scope{}, ID: "", Version: 0, DecisionPoint: "", Condition: "", Unless: "", Outcome: "", Message: "",
		Mode: "", OnError: "", Final: false, CheckedWith: gate.CheckedWith{Engine: "", Schema: gate.SchemaVersion{
			Subject: "", Major: 0, Minor: 0,
		}},
		State: "", Reads: nil,
	}
}

// checkDeleteReferenced runs the checks of the removal of an approval rule or
// a path set: a final gate that names it needs a reason, and no stored gate,
// approval default or ownership rule may name it.
func checkDeleteReferenced(ctx context.Context, tx Tx, scope gate.Scope, ref Ref, reason string) error {
	if err := requireNoFinalReader(ctx, tx, scope, ref, reason); err != nil {
		return err
	}

	readers, err := tx.Referrers(ctx, scope, ref)
	if err != nil {
		return err
	}

	if len(readers) == 0 {
		return nil
	}

	names := make([]string, len(readers))
	for i, r := range readers {
		names[i] = readerName(r)
	}

	return fmt.Errorf("%w: the %s %q is used by %s", gate.ErrInvalid, ref.Kind, ref.ID, strings.Join(names, ", "))
}

// readerName returns the policy ID of a gate, and the scope and kind of any
// other reader.
func readerName(r Located) string {
	switch {
	case r.Row.Kind == KindGate:
		return r.ScopeKey + "/" + r.Row.ID
	case r.Row.ID == "":
		return r.Row.Kind + " of " + r.ScopeKey
	default:
		return r.Row.Kind + " " + r.ScopeKey + "/" + r.Row.ID
	}
}

// ParseScope parses a stored scope key.
func ParseScope(key string) (gate.Scope, error) {
	scope, err := gate.ParseScopeKey(key)
	if err != nil {
		return gate.Scope{}, fmt.Errorf("stored scope key: %w", err)
	}

	return scope, nil
}

// CheckJoin returns gate.ErrFinal when the unit holds a gate with the ID of a
// final gate of the collection. A unit that joins a collection falls under its
// final gates, so it must not shadow one. Put enforces the same rule when a
// scope gets a final gate.
func CheckJoin(ctx context.Context, tx Tx, unit, coll gate.Scope) error {
	held, err := tx.List(ctx, unit, KindGate)
	if err != nil {
		return err
	}

	if len(held) == 0 {
		return nil
	}

	finals, err := tx.List(ctx, coll, KindGate)
	if err != nil {
		return err
	}

	for _, row := range finals {
		g, derr := decodeGate(coll, row)
		if derr != nil {
			return derr
		}

		if !g.Final {
			continue
		}

		for _, h := range held {
			if h.ID == row.ID {
				return fmt.Errorf("%w: the unit holds a gate with the ID of the final gate %s of the collection",
					gate.ErrFinal, g.PolicyID())
			}
		}
	}

	return nil
}
