package tofuunit

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/collection"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/workspace"
)

// projectAny is the project part of the tool-specific name of an OpenTofu
// workspace. A workspace has no project, so a placement rule matches it with
// the project "*".
const projectAny = "*"

// ErrPlacementOff reports a server with no Placer: it keeps no placement
// rules, so it cannot put a new workspace in a collection and makes no
// workspace.
var ErrPlacementOff = errors.New(
	"this server keeps no placement rules, so it cannot place a new workspace: use a store with collections and placement rules",
)

// PlacementRules finds the placement rule of a new workspace.
type PlacementRules interface {
	// Place returns the first placement rule of ns that matches the new unit.
	// It returns an error that matches collection.ErrNoPlacement when no rule
	// matches. It writes nothing.
	Place(ctx context.Context, ns namespace.Name, tool, project, name string) (collection.Rule, error)
}

// PlacementCollections reads the collection of a placement rule.
type PlacementCollections interface {
	// CollectionID returns the stable ID of the collection obj, and false when
	// the collection does not exist.
	CollectionID(ctx context.Context, obj identity.Object) (string, bool, error)
	// CollectionOf returns the name of the collection that holds the unit, and
	// false when the unit is in no collection or does not exist.
	CollectionOf(ctx context.Context, unit identity.Object) (string, bool, error)
}

// Guard authorizes an action of the caller of a request and records a refusal.
// refusal.Recorder implements it.
type Guard interface {
	Authorize(
		ctx context.Context, r *http.Request, p identity.Principal, action identity.Action, obj identity.Object,
	) error
}

// WorkspaceReader reads a workspace. It lets a duplicate create answer
// workspace.ErrAlreadyExists whatever the placement rules say.
type WorkspaceReader interface {
	Get(ctx context.Context, ref workspace.Ref) (workspace.Workspace, error)
}

// Placer chooses the collection of a new workspace. The workspace store then
// makes the workspace and its membership in one transaction. The server gives
// the Placer the placement rules and the collections of the installation.
// Without a Placer the server makes no workspace.
type Placer struct {
	Rules       PlacementRules
	Collections PlacementCollections
}

// WritableNamespaces is the namespace use case that Place needs.
// namespace.Service implements it.
type WritableNamespaces interface {
	// WritableHere returns the namespace if this installation may write to it,
	// namespace.ErrNotHome when another installation is its primary, or
	// namespace.ErrNotFound.
	WritableHere(ctx context.Context, name namespace.Name) (namespace.Namespace, error)
}

// PlaceRequest is what Place needs to place one new workspace.
type PlaceRequest struct {
	Request    *http.Request
	Principal  identity.Principal
	Ref        workspace.Ref
	Guard      Guard
	Namespaces WritableNamespaces
	Workspaces WorkspaceReader
}

// Place finds the collection of the new workspace and authorizes the caller on
// it. It returns an error that matches collection.ErrNoPlacement when no rule
// matches, identity.ErrDenied when the caller lacks collection:create-unit on
// the collection of the rule, and collection.ErrNotFound when the collection
// does not exist. It writes nothing. A nil Placer returns ErrPlacementOff.
//
// Defense in depth: the route already authorizes unit:manage on the new unit.
// The check of collection:create-unit stays here, in front of the write, so
// that no other check can stand in for it.
func (p *Placer) Place(ctx context.Context, req PlaceRequest) (workspace.Placement, error) {
	var none workspace.Placement

	if p == nil || p.Rules == nil || p.Collections == nil {
		// A namespace that does not exist gets its own error first.
		if _, err := req.Namespaces.WritableHere(ctx, req.Ref.Namespace); err != nil {
			return none, err
		}

		return none, ErrPlacementOff
	}

	rule, err := p.rule(ctx, req)
	if err != nil {
		return none, err
	}

	target := identity.CollectionObject(req.Ref.Namespace, rule.Collection.String())

	err = req.Guard.Authorize(ctx, req.Request, req.Principal, identity.ActionCollectionCreateUnit, target)
	if err != nil {
		return none, fmt.Errorf("authorize %s: %w", identity.ActionCollectionCreateUnit, err)
	}

	id, found, err := p.Collections.CollectionID(ctx, target)

	switch {
	case err != nil:
		return none, err
	case !found:
		return none, fmt.Errorf("placement rule %s names collection %q: %w",
			rule.ID, rule.Collection, collection.ErrNotFound)
	default:
	}

	return p.placement(req, rule, id)
}

// Require refuses a write to the existing workspace obj when the workspace is
// in no collection. It returns an error that matches collection.ErrNoCollection
// and names the command that moves the workspace into a collection. It writes
// nothing. It fails closed: a nil Placer, or a Placer with no collections,
// cannot check the workspace, so it returns ErrPlacementOff, as Place does.
//
// Defense in depth: the authorizer already finds no collection grant for a
// unit in no collection, but a grant on the namespace still allows the write.
// This check stays so that no grant can write to a unit that no collection
// holds.
func (p *Placer) Require(ctx context.Context, obj identity.Object) error {
	if p == nil || p.Collections == nil {
		return fmt.Errorf("write to %s: %w", obj, ErrPlacementOff)
	}

	_, found, err := p.Collections.CollectionOf(ctx, obj)
	if err != nil {
		return fmt.Errorf("collection of %s: %w", obj, err)
	}

	if !found {
		return fmt.Errorf("write to %s: %w", obj, collection.ErrNoCollection)
	}

	return nil
}

// rule returns the first placement rule that matches the new workspace. When no
// rule matches, a namespace that does not exist and a workspace that exists
// get their own errors first.
func (p *Placer) rule(ctx context.Context, req PlaceRequest) (collection.Rule, error) {
	rule, err := p.Rules.Place(ctx, req.Ref.Namespace, Tool, projectAny, req.Ref.Name.String())
	if !errors.Is(err, collection.ErrNoPlacement) {
		return rule, err
	}

	// A namespace that does not exist has no rules, and a namespace of another
	// installation holds none here. Their errors come first.
	if _, nsErr := req.Namespaces.WritableHere(ctx, req.Ref.Namespace); nsErr != nil {
		return rule, nsErr
	}

	// A workspace that exists is a conflict, whatever the rules say.
	if _, getErr := req.Workspaces.Get(ctx, req.Ref); getErr == nil {
		return rule, workspace.ErrAlreadyExists
	}

	return rule, err
}

// placement returns the placement that puts the new workspace in the collection
// of rule, with the audit entry that names the rule and the caller.
func (p *Placer) placement(req PlaceRequest, rule collection.Rule, id string) (workspace.Placement, error) {
	obj, err := Object(req.Ref)
	if err != nil {
		return workspace.Placement{}, err
	}

	entry := Entry(req.Request, req.Principal, obj, Detail{
		At:        time.Time{},
		Action:    identity.ActionCollectionCreateUnit,
		Version:   "from=none;to=" + rule.Collection.String() + ";rule=" + rule.ID,
		ValuePath: "",
		Kind:      audit.KindChange,
		Outcome:   audit.OutcomeSuccess,
		Reason:    "",
	})

	return workspace.Placement{
		Collection:   collection.Ref{Namespace: req.Ref.Namespace, Name: rule.Collection},
		CollectionID: id,
		Entry:        entry,
	}, nil
}
