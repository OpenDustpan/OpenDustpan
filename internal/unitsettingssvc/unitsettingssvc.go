// Package unitsettingssvc is the service of the settings of a unit. It adds
// to the Store port of package unitsettings what the port does not know: the
// fence of the namespace, the time, the checks that the Git host connection
// and the runner pool of the settings exist and can do their work, and the
// effective view of the settings.
//
// The service checks no grant of the caller. The route checks the actions,
// and Needs tells it which actions a write needs beyond the action on the
// unit.
package unitsettingssvc

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/forge"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/runner"
	"opendustpan.org/opendustpan/pkg/unitsettings"
)

// ErrMissingDependency reports a Config without a required dependency.
var ErrMissingDependency = errors.New("missing dependency")

// The refusals of a write, beyond the errors of package unitsettings.
var (
	// ErrConnectionUnknown reports a Git host connection that the namespace
	// does not have.
	ErrConnectionUnknown = errors.New("git host connection not found")
	// ErrConnectionLacksCapability reports a connection that cannot do what the
	// settings use it for.
	ErrConnectionLacksCapability = errors.New("git host connection lacks a capability")
	// ErrPoolUnknown reports a runner pool that the namespace does not have, or
	// that does not run jobs.
	ErrPoolUnknown = errors.New("runner pool for jobs not found")
)

// Fences gives the fence of a namespace for the writes of the service.
type Fences interface {
	// Fence returns the current fence of ns. It returns namespace.ErrNotFound
	// for a namespace that does not exist.
	Fence(ctx context.Context, ns namespace.Name) (namespace.Fence, error)
}

// Connections reads the Git host connections of the namespaces.
type Connections interface {
	// GetConnection returns the connection id of ns, or
	// forge.ErrConnectionNotFound.
	GetConnection(ctx context.Context, ns namespace.Name, id string) (forge.ConnectionRecord, error)
	// ListConnections returns the connections of ns.
	ListConnections(ctx context.Context, ns namespace.Name) ([]forge.ConnectionRecord, error)
}

// Pools reads the runner pools.
type Pools interface {
	// Pool returns the pool id, or runner.ErrNotFound.
	Pool(ctx context.Context, id string) (runner.Pool, error)
	// Pools returns the pools of ns.
	Pools(ctx context.Context, ns namespace.Name) ([]runner.Pool, error)
}

// Config holds the dependencies of a Service. Every field except Now is
// required.
type Config struct {
	Store       unitsettings.Store
	Fences      Fences
	Connections Connections
	Pools       Pools
	// Now gives the time of a write. Nil means time.Now.
	Now func() time.Time
}

// Service reads and writes the settings of units.
type Service struct {
	store       unitsettings.Store
	fences      Fences
	connections Connections
	pools       Pools
	now         func() time.Time
}

// New returns a Service.
func New(cfg Config) (*Service, error) {
	switch {
	case cfg.Store == nil:
		return nil, fmt.Errorf("%w: unit settings store", ErrMissingDependency)
	case cfg.Fences == nil:
		return nil, fmt.Errorf("%w: fences", ErrMissingDependency)
	case cfg.Connections == nil:
		return nil, fmt.Errorf("%w: git host connections", ErrMissingDependency)
	case cfg.Pools == nil:
		return nil, fmt.Errorf("%w: runner pools", ErrMissingDependency)
	}

	now := cfg.Now
	if now == nil {
		now = time.Now
	}

	return &Service{
		store: cfg.Store, fences: cfg.Fences, connections: cfg.Connections, pools: cfg.Pools, now: now,
	}, nil
}

// View is the answer of a read: the record of the unit (the zero record at
// version 0 when the unit has none), the defaults of its namespace and the
// effective value of each field.
type View struct {
	Settings  unitsettings.Settings
	Defaults  unitsettings.NamespaceDefaults
	Effective unitsettings.Effective
	// DefaultConnection is the default Git host connection of the namespace,
	// or empty.
	DefaultConnection string
	// Context is what Resolve read from the namespace and the tool of the
	// unit. Updated uses it to make the view of a new record with no read.
	Context unitsettings.Context
}

// Updated returns the view of the record stored, with defaults as the unit
// defaults of the view and the namespace facts of v. It reads nothing, so a
// caller can build the stored answer of a request in the transaction of the
// write.
func (v *View) Updated(stored *unitsettings.Settings, defaults unitsettings.NamespaceDefaults) View {
	return View{
		Settings: *stored, Defaults: defaults, Effective: unitsettings.Resolve(stored, defaults, v.Context),
		DefaultConnection: v.DefaultConnection, Context: v.Context,
	}
}

// Read returns the view of unit, the unit reference with its stable unit ID.
// A unit with no record gives a view at version 0 whose fields all come from
// the namespace and the built-in values.
func (s *Service) Read(ctx context.Context, unit identity.Object) (View, error) {
	rec, err := s.store.Get(ctx, unit)

	switch {
	case errors.Is(err, unitsettings.ErrNotFound):
		rec = emptyRecord(unit)
	case err != nil:
		return View{}, fmt.Errorf("read unit settings: %w", err)
	default:
	}

	return s.view(ctx, &rec)
}

// emptyRecord is the record of a unit that has none: no field is set, and the
// version is 0.
func emptyRecord(unit identity.Object) unitsettings.Settings {
	var rec unitsettings.Settings

	rec.Unit = unit

	return rec
}

func defaultConnection(conns []forge.ConnectionRecord) string {
	for i := range conns {
		if conns[i].Default {
			return conns[i].ID
		}
	}

	return ""
}

func hasJobPool(pools []runner.Pool) bool {
	for i := range pools {
		if pools[i].Purpose == runner.PurposeJobs {
			return true
		}
	}

	return false
}

// DefaultFiles returns the default write-back files of the tool of unit: the
// config file that the tool reads for the unit. A tool that the service does
// not know has none. For a Pulumi stack the name is project/stack, and the
// file is Pulumi.<stack>.yaml.
func DefaultFiles(unit identity.Object) []string {
	name := unit.UnitName()

	switch unit.Tool() {
	case "pulumi":
		stack := name[strings.LastIndex(name, "/")+1:]

		return []string{"Pulumi." + stack + ".yaml"}
	case "opentofu":
		return []string{name + ".auto.tfvars.json"}
	default:
		return nil
	}
}

// Needs are the actions that a write needs beyond the action on the unit.
type Needs struct {
	// Connection is the effective Git host connection that the write newly
	// names, or empty when the write names none. The action
	// identity.ActionNamespaceUseGitHostConnection applies.
	Connection string
	// Steps is true when the write changes the custom steps. The action
	// identity.ActionUnitDefineSteps applies.
	Steps bool
}

// NeedsOf returns the actions that a write of next over the view cur needs.
// A write needs the use action for a connection when it names a connection
// that the record did not name, or when the effective connection after the
// write is not the one before it. It needs the steps action when the steps
// differ.
func NeedsOf(cur *View, next *unitsettings.Settings) Needs {
	var n Needs

	after := next.CodeLocation.Connection
	named := after != "" && after != cur.Settings.CodeLocation.Connection

	if after == "" {
		after = cur.DefaultConnection
	}

	if named || (after != "" && after != cur.Effective.Connection.Value) {
		n.Connection = after
	}

	n.Steps = !sameSteps(cur.Settings.Steps, next.Steps)

	return n
}

// NeedsKeyed returns the actions that a write with an Idempotency-Key needs.
// A replay answers from the stored answer and does not run the write, so the
// server cannot compare the body with the record that the first request
// changed. A keyed write therefore needs the use action for each connection
// that the body names, and the steps action when the body sets steps.
func NeedsKeyed(next *unitsettings.Settings) Needs {
	return Needs{Connection: next.CodeLocation.Connection, Steps: len(next.Steps) > 0}
}

func sameSteps(a, b []job.Step) bool {
	if len(a) == 0 && len(b) == 0 {
		return true
	}

	return reflect.DeepEqual(a, b)
}

// Put stores in as the settings of in.Unit, over the record at version
// expected, and returns the view. e is the audit entry of the caller: the
// service stamps the time, the namespace, the object and the fence version on
// it, and the store adds the action and the changes.
//
// It refuses, before it writes, a connection that the namespace does not have
// (ErrConnectionUnknown), a connection that cannot do what the settings use
// it for (ErrConnectionLacksCapability) and a pool that is not a jobs pool of
// the namespace (ErrPoolUnknown). The store refuses the rest.
func (s *Service) Put(
	ctx context.Context, in *unitsettings.Settings, expected int64, e audit.Entry,
) (View, error) {
	if err := in.Validate(); err != nil {
		return View{}, fmt.Errorf("unit settings: %w", err)
	}

	if err := checkDirectory(in.CodeLocation.Directory); err != nil {
		return View{}, err
	}

	cur, err := s.Read(ctx, in.Unit)
	if err != nil {
		return View{}, err
	}

	if refErr := s.checkReferences(ctx, in, &cur); refErr != nil {
		return View{}, refErr
	}

	fence, err := s.fences.Fence(ctx, in.Unit.Namespace())
	if err != nil {
		return View{}, fmt.Errorf("fence: %w", err)
	}

	e.Action = unitsettings.AuditActionChanged

	out, err := s.store.Put(ctx, in, expected, fence, s.stamp(e, in.Unit, fence))
	if err != nil {
		return View{}, fmt.Errorf("write unit settings: %w", err)
	}

	// The view uses the defaults that the store read in the transaction of
	// the write, not the defaults of cur, which came from an earlier read.
	// The view reads nothing else: the answer of a keyed request holds the
	// same bytes that the store kept in the transaction of the write.
	return cur.Updated(&out.Settings, out.Defaults), nil
}

// checkDirectory returns an error that wraps unitsettings.ErrInvalid for a
// directory that holds CI configuration, such as .github, or that leaves the
// repository.
//
// Defense in depth: Validate checks the directory as a path of a code
// location. This check applies the rule of a write-back path to the directory
// too, so that a directory never leads a write-back into CI configuration, and
// it does not rely on the files that the settings list.
func checkDirectory(dir string) error {
	if dir == "" {
		return nil
	}

	probe := job.FileEdit{Path: "probe", Keys: map[string][]byte{"probe": []byte("1")}}

	if err := job.ValidateEdits(dir, []job.FileEdit{probe}, func(string) bool { return true }); err != nil {
		return fmt.Errorf("%w: the directory is not a path that a write-back can reach", unitsettings.ErrInvalid)
	}

	return nil
}

// Reset removes the settings of unit, over the record at version expected.
func (s *Service) Reset(ctx context.Context, unit identity.Object, expected int64, e audit.Entry) error {
	fence, err := s.fences.Fence(ctx, unit.Namespace())
	if err != nil {
		return fmt.Errorf("fence: %w", err)
	}

	e.Action = unitsettings.AuditActionReset

	if err := s.store.Delete(ctx, unit, expected, fence, s.stamp(e, unit, fence)); err != nil {
		return fmt.Errorf("reset unit settings: %w", err)
	}

	return nil
}

// Defaults returns the unit defaults of ns.
func (s *Service) Defaults(ctx context.Context, ns namespace.Name) (unitsettings.NamespaceDefaults, error) {
	d, err := s.store.Defaults(ctx, ns)
	if err != nil {
		return unitsettings.NamespaceDefaults{}, fmt.Errorf("read unit defaults: %w", err)
	}

	return d, nil
}

// SetDefaults stores the pool as the unit defaults of ns, over the defaults at
// version expected. It refuses a pool that is not a jobs pool of ns.
func (s *Service) SetDefaults(
	ctx context.Context, ns namespace.Name, pool string, expected int64, e audit.Entry,
) (unitsettings.NamespaceDefaults, error) {
	d := unitsettings.NamespaceDefaults{Namespace: ns, Pool: pool, Version: 0, UpdatedAt: time.Time{}}
	if err := d.Validate(); err != nil {
		return unitsettings.NamespaceDefaults{}, fmt.Errorf("unit defaults: %w", err)
	}

	if err := s.checkPool(ctx, ns, pool); err != nil {
		return unitsettings.NamespaceDefaults{}, err
	}

	fence, err := s.fences.Fence(ctx, ns)
	if err != nil {
		return unitsettings.NamespaceDefaults{}, fmt.Errorf("fence: %w", err)
	}

	e.Action = unitsettings.AuditActionDefaultsChanged
	e.Namespace = ns
	e.At = s.now().UTC()
	e.Object = ns.String()
	e.FenceVersion = fence.FailoverVersion

	stored, err := s.store.SetDefaults(ctx, d, expected, fence, e)
	if err != nil {
		return unitsettings.NamespaceDefaults{}, fmt.Errorf("write unit defaults: %w", err)
	}

	return stored, nil
}

// view completes a record into a View.
func (s *Service) view(ctx context.Context, rec *unitsettings.Settings) (View, error) {
	ns := rec.Unit.Namespace()

	defaults, err := s.store.Defaults(ctx, ns)
	if err != nil {
		return View{}, fmt.Errorf("read unit defaults: %w", err)
	}

	conns, err := s.connections.ListConnections(ctx, ns)
	if err != nil {
		return View{}, fmt.Errorf("list git host connections: %w", err)
	}

	pools, err := s.pools.Pools(ctx, ns)
	if err != nil {
		return View{}, fmt.Errorf("list runner pools: %w", err)
	}

	c := unitsettings.Context{
		DefaultConnection: defaultConnection(conns),
		HasJobPool:        hasJobPool(pools),
		DefaultFiles:      DefaultFiles(rec.Unit),
	}

	return View{
		Settings: *rec, Defaults: defaults, Effective: unitsettings.Resolve(rec, defaults, c),
		DefaultConnection: c.DefaultConnection, Context: c,
	}, nil
}

// stamp sets the fields of e that the service owns.
func (s *Service) stamp(e audit.Entry, unit identity.Object, fence namespace.Fence) audit.Entry {
	e.Namespace = unit.Namespace()
	e.At = s.now().UTC()
	e.Object = unit.String()
	e.ObjectID = unit.UnitID()
	e.FenceVersion = fence.FailoverVersion

	return e
}

// checkReferences checks the pool and the connection of in. cur is the view
// before the write.
func (s *Service) checkReferences(ctx context.Context, in *unitsettings.Settings, cur *View) error {
	ns := in.Unit.Namespace()

	if err := s.checkPool(ctx, ns, in.Pool); err != nil {
		return err
	}

	conn := in.CodeLocation.Connection

	if conn == "" {
		// The default connection of the namespace serves the settings that
		// name none, so its capabilities count.
		conn = cur.DefaultConnection
		if conn == "" {
			return nil
		}
	}

	rec, err := s.connections.GetConnection(ctx, ns, conn)

	switch {
	case errors.Is(err, forge.ErrConnectionNotFound):
		return ErrConnectionUnknown
	case err != nil:
		return fmt.Errorf("get git host connection: %w", err)
	}

	if missing := Capabilities(&rec).Missing(Required(in)); len(missing) > 0 {
		return fmt.Errorf("%w: %s", ErrConnectionLacksCapability, joinCapabilities(missing))
	}

	return nil
}

func (s *Service) checkPool(ctx context.Context, ns namespace.Name, pool string) error {
	if pool == "" {
		return nil
	}

	p, err := s.pools.Pool(ctx, pool)

	switch {
	case errors.Is(err, runner.ErrNotFound):
		return ErrPoolUnknown
	case err != nil:
		return fmt.Errorf("get runner pool: %w", err)
	}

	// Defense in depth: the store looks a pool up by its ID alone. A pool of
	// another namespace, or a pool for sweeps, must not answer for this one.
	if runner.CheckJobPool(&p, ns) != nil {
		return ErrPoolUnknown
	}

	return nil
}

// Capabilities returns what the connection can do, from its record. The
// adapter of a forge reports its capabilities only after it opens the
// connection, which a settings write does not do. So the service reads the
// push flag: a connection with no push access has neither forge.CapCommit nor
// forge.CapReview, as forge.Connection documents.
func Capabilities(rec *forge.ConnectionRecord) forge.Capabilities {
	all := forge.Capabilities{
		forge.CapFiles, forge.CapCompare, forge.CapGitAccess, forge.CapRepoToken, forge.CapEvents,
	}

	if rec.Push {
		all = append(all, forge.CapCommit, forge.CapReview)
	}

	return all
}

// Required returns the capabilities that the settings use: those of the
// write-back executor, with the review request when the delivery is one, and
// the events of pull request automation.
func Required(s *unitsettings.Settings) forge.Capabilities {
	var need forge.Capabilities

	if s.Executor == job.ExecutorWriteBack || s.WriteBack.Delivery != "" || len(s.WriteBack.Files) > 0 {
		consumer := forge.ConsumerWriteBackReview
		if s.WriteBack.Delivery == job.DeliveryDirectPush {
			consumer = forge.ConsumerWriteBack
		}

		need = append(need, forge.Needs(consumer)...)
	}

	if s.ReviewRequests.Enabled {
		need = append(need, forge.CapEvents)
	}

	return need
}

func joinCapabilities(c forge.Capabilities) string {
	names := make([]string, 0, len(c))
	for _, x := range c {
		names = append(names, string(x))
	}

	return strings.Join(names, ", ")
}

// Suggestion is a code location that the git details of a recent update of a
// unit name. A person accepts it by saving the settings. It sets no field.
type Suggestion struct {
	// Repository is the repository that the update named.
	Repository string
	// Commit is the commit that the update ran on.
	Commit string
	// Source says where the suggestion comes from, for example
	// pulumi-update.
	Source string
	// ObservedAt is the time of the update.
	ObservedAt time.Time
}

// Suggester gives the suggestions for the code location of a unit, newest
// first. A tool that keeps no git details of its runs gives none.
type Suggester interface {
	// Suggest returns the suggestions for unit, the unit reference with its
	// stable unit ID. It reads and writes nothing else.
	Suggest(ctx context.Context, unit identity.Object) ([]Suggestion, error)
}
