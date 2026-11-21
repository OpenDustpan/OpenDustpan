package outputsharetest

import (
	"context"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/ident"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/collection"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/inventory"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/outputshare"
	"opendustpan.org/opendustpan/pkg/stack"
	"opendustpan.org/opendustpan/pkg/workspace"
)

// The tools that the unit hooks make units of.
const (
	// ToolOpenTofu makes a workspace. Its unit name is the workspace name.
	ToolOpenTofu = "opentofu"
	// ToolPulumi makes a stack. Its unit name is "project/stack".
	ToolPulumi = "pulumi"
)

// WorkspaceUnits is the part of a SQL store that makes, deletes and purges
// workspaces.
type WorkspaceUnits interface {
	CreateWorkspace(ctx context.Context, w workspace.Workspace, fence namespace.Fence) error
	DeleteWorkspace(ctx context.Context, ref workspace.Ref, fence namespace.Fence, d inventory.Deletion) error
	PurgeWorkspace(ctx context.Context, id workspace.ID, fence namespace.Fence, dueBy time.Time, e audit.Entry) error
}

// StackUnits is the part of a SQL store that makes and renames stacks.
type StackUnits interface {
	CreateStack(ctx context.Context, st stack.Stack, fence namespace.Fence) error
	RenameStack(ctx context.Context, oldRef stack.Ref, newName stack.Name, fence namespace.Fence, e audit.Entry) error
}

// CollectionUnits is the part of a SQL store that makes collections and puts
// units in them.
type CollectionUnits interface {
	CreateCollection(ctx context.Context, c collection.Collection, fence namespace.Fence, e audit.Entry) error
	DeleteCollection(ctx context.Context, ref collection.Ref, fence namespace.Fence, e audit.Entry) error
	GetCollection(ctx context.Context, ref collection.Ref) (collection.Collection, error)
	SetUnitCollection(ctx context.Context, m collection.Move, fence namespace.Fence, e audit.Entry) error
}

// UnitStore is the part of a SQL store that the unit hooks use to make, move,
// rename, delete and purge units and collections.
type UnitStore interface {
	WorkspaceUnits
	StackUnits
	CollectionUnits
}

// WithUnits returns h with the hooks that make and change units and
// collections through s: Unit, RemoveUnit, PurgeUnit, RenameUnit, MoveUnit and
// RemakeCollection. h.Namespace must be set. The store s must be the store of
// h.Store, so a rule sees the units.
//
// Unit makes the unit with the plain create of the tool store. When the
// namespace has the setting namespace, that create also writes the rule of the
// new unit.
func WithUnits(h Harness, s UnitStore) Harness {
	u := unitHooks{s: s, namespace: h.Namespace}

	h.Unit = u.unit
	h.RemoveUnit = u.remove
	h.PurgeUnit = u.purge
	h.RenameUnit = u.rename
	h.MoveUnit = u.move
	h.RemakeCollection = u.remakeCollection

	return h
}

// unitHooks implements the unit hooks over a UnitStore.
type unitHooks struct {
	s         UnitStore
	namespace func(t *testing.T, name string) namespace.Fence
}

// hookEntry returns a valid audit entry of a change of an object in ns.
func hookEntry(ns namespace.Name, object string) audit.Entry {
	e := settingEntry(namespace.Fence{Namespace: ns, FailoverVersion: 0})
	e.Action = "unit:manage"
	e.Object = object

	return e
}

func (u unitHooks) unit(
	t *testing.T, tool string, ns namespace.Name, name, collectionName string,
) outputshare.Placement {
	t.Helper()

	fence := u.namespace(t, ns.String())

	var id string

	switch tool {
	case ToolOpenTofu:
		id = u.makeWorkspace(t, ns, name, fence)
	case ToolPulumi:
		id = u.makeStack(t, ns, name, fence)
	default:
		t.Fatalf("unit hook: unknown tool %q", tool)
	}

	obj, err := identity.UnitObject(tool, ns, name)
	if err != nil {
		t.Fatalf("unit object %s:%s/%s: %v", tool, ns, name, err)
	}

	p := outputshare.Placement{Unit: obj.WithUnitID(id), Collection: identity.Object{}}

	if collectionName != "" {
		p.Collection = u.place(t, p.Unit, "", collectionName)
	}

	return p
}

// makeWorkspace makes the workspace and returns its stable ID.
func (u unitHooks) makeWorkspace(t *testing.T, ns namespace.Name, name string, fence namespace.Fence) string {
	t.Helper()

	at := time.UnixMilli(settingAtMs).UTC()

	wn, err := workspace.ParseName(name)
	if err != nil {
		t.Fatalf("workspace name %q: %v", name, err)
	}

	w := workspace.Workspace{
		NamespaceID:  "",
		InCollection: false,
		CreatedAt:    at,
		UpdatedAt:    at,
		Lock:         workspace.Lock{LockedAt: time.Time{}, ID: "", Holder: "", Reason: ""},
		LastRelease:  workspace.NoRelease(),
		ID:           workspace.ID(ident.NewID("ws-")),
		Ref:          workspace.Ref{Namespace: ns, Name: wn},
		Revision:     1,
		Overrides:    namespace.Overrides{Forced: false, Unlocked: false},
		Origin:       identity.StateOrigin{Job: "", Commit: ""},
	}

	if err = u.s.CreateWorkspace(t.Context(), w, fence); err != nil {
		t.Fatalf("make workspace %s/%s: %v", ns, name, err)
	}

	return w.ID.String()
}

// makeStack makes the stack "project/stack" and returns its stable ID.
func (u unitHooks) makeStack(t *testing.T, ns namespace.Name, name string, fence namespace.Fence) string {
	t.Helper()

	at := time.UnixMilli(settingAtMs).UTC()
	project, stackName, _ := strings.Cut(name, "/")

	ref, err := stack.ParseRef(ns.String(), project, stackName)
	if err != nil {
		t.Fatalf("stack name %q: %v", name, err)
	}

	st := stack.Stack{
		CreatedAt:   at,
		UpdatedAt:   at,
		Tags:        nil,
		Ref:         ref,
		ID:          stack.ID(ident.NewID("stk-")),
		NamespaceID: "",
		Version:     0,
	}

	if err = u.s.CreateStack(t.Context(), st, fence); err != nil {
		t.Fatalf("make stack %s/%s: %v", ns, name, err)
	}

	return st.ID.String()
}

// place makes the collection to when it does not exist, moves the unit from
// the collection from into it and returns the collection with its stable ID.
func (u unitHooks) place(t *testing.T, unit identity.Object, from, to string) identity.Object {
	t.Helper()

	ns := unit.Namespace()
	fence := u.namespace(t, ns.String())
	name, err := collection.ParseName(to)
	if err != nil {
		t.Fatalf("collection name %q: %v", to, err)
	}

	ref := collection.Ref{Namespace: ns, Name: name}

	c, err := u.s.GetCollection(t.Context(), ref)
	if err != nil {
		c = collection.Collection{
			CreatedAt: time.UnixMilli(settingAtMs).UTC(),
			ID:        ident.NewID("col-"),
			Ref:       ref,
		}

		if err = u.s.CreateCollection(t.Context(), c, fence, hookEntry(ns, ref.String())); err != nil {
			t.Fatalf("make collection %s: %v", ref, err)
		}
	}

	move := collection.Move{
		Unit:     collection.UnitRef{Namespace: ns, Tool: unit.Tool(), Name: unit.UnitName()},
		From:     collection.Name(from),
		To:       ref,
		ToID:     c.ID,
		EndsLock: false,
	}

	if err = u.s.SetUnitCollection(t.Context(), move, fence, hookEntry(ns, unit.String())); err != nil {
		t.Fatalf("move %s into %s: %v", unit, ref, err)
	}

	return identity.CollectionObject(ns, c.Ref.Name.String()).WithCollectionID(c.ID)
}

func (u unitHooks) move(t *testing.T, p outputshare.Placement, to string) outputshare.Placement {
	t.Helper()

	from := ""
	if p.Collection.IsCollection() {
		from = p.Collection.CollectionName()
	}

	p.Collection = u.place(t, p.Unit, from, to)

	return p
}

func (u unitHooks) remove(t *testing.T, p outputshare.Placement) {
	t.Helper()

	ns := p.Unit.Namespace()
	fence := u.namespace(t, ns.String())
	d := inventory.Deletion{
		PurgeAfter: time.UnixMilli(settingAtMs).UTC().Add(time.Hour),
		Entry:      hookEntry(ns, p.Unit.String()),
	}

	var err error

	if p.Unit.Tool() == ToolOpenTofu {
		err = u.s.DeleteWorkspace(
			t.Context(),
			workspace.Ref{Namespace: ns, Name: workspace.Name(p.Unit.UnitName())},
			fence,
			d,
		)
	} else {
		t.Fatalf("remove unit: the hook removes a workspace only, got %q", p.Unit.Tool())
	}

	if err != nil {
		t.Fatalf("delete %s: %v", p.Unit, err)
	}
}

func (u unitHooks) purge(t *testing.T, p outputshare.Placement) {
	t.Helper()

	u.remove(t, p)

	ns := p.Unit.Namespace()
	fence := u.namespace(t, ns.String())

	if p.Unit.Tool() != ToolOpenTofu {
		t.Fatalf("purge unit: the hook purges a workspace only, got %q", p.Unit.Tool())
	}

	err := u.s.PurgeWorkspace(
		t.Context(),
		workspace.ID(p.Unit.UnitID()),
		fence,
		time.Time{},
		hookEntry(ns, p.Unit.String()),
	)
	if err != nil {
		t.Fatalf("purge %s: %v", p.Unit, err)
	}
}

func (u unitHooks) rename(t *testing.T, p outputshare.Placement, newName string) outputshare.Placement {
	t.Helper()

	ns := p.Unit.Namespace()
	fence := u.namespace(t, ns.String())

	if p.Unit.Tool() != ToolPulumi {
		t.Fatalf("rename unit: the hook renames a stack only, got %q", p.Unit.Tool())
	}

	project, stackName, _ := strings.Cut(p.Unit.UnitName(), "/")

	oldRef, err := stack.ParseRef(ns.String(), project, stackName)
	if err != nil {
		t.Fatalf("stack ref %q: %v", p.Unit.UnitName(), err)
	}

	name, err := stack.ParseName(newName)
	if err != nil {
		t.Fatalf("stack name %q: %v", newName, err)
	}

	if err = u.s.RenameStack(t.Context(), oldRef, name, fence, hookEntry(ns, p.Unit.String())); err != nil {
		t.Fatalf("rename %s: %v", p.Unit, err)
	}

	obj, err := identity.UnitObject(ToolPulumi, ns, oldRef.Project.String()+"/"+newName)
	if err != nil {
		t.Fatalf("unit object: %v", err)
	}

	p.Unit = obj.WithUnitID(p.Unit.UnitID())

	return p
}

func (u unitHooks) remakeCollection(t *testing.T, ns namespace.Name, name string) identity.Object {
	t.Helper()

	fence := u.namespace(t, ns.String())

	cn, err := collection.ParseName(name)
	if err != nil {
		t.Fatalf("collection name %q: %v", name, err)
	}

	ref := collection.Ref{Namespace: ns, Name: cn}

	if err = u.s.DeleteCollection(t.Context(), ref, fence, hookEntry(ns, ref.String())); err != nil {
		t.Fatalf("delete collection %s: %v", ref, err)
	}

	c := collection.Collection{CreatedAt: time.UnixMilli(settingAtMs).UTC(), ID: ident.NewID("col-"), Ref: ref}

	if err = u.s.CreateCollection(t.Context(), c, fence, hookEntry(ns, ref.String())); err != nil {
		t.Fatalf("make collection %s again: %v", ref, err)
	}

	return identity.CollectionObject(ns, name).WithCollectionID(c.ID)
}
