// Package lifecycletest is the conformance suite for a provider. A provider
// author runs it against a test instance of the remote system, or against a
// fake of it:
//
//	func TestConformance(t *testing.T) {
//		t.Parallel()
//		lifecycletest.Run(t, lifecycletest.Fixture{
//			Provider: newProvider(),
//			Config:   schema.Values{"base_url": schema.StringValue(srv.URL)},
//			Resources: []lifecycletest.ResourceCase{{
//				Type:     "schedule",
//				Create:   schema.Values{"name": schema.StringValue("primary"), "team": schema.StringValue("a")},
//				Update:   schema.Values{"name": schema.StringValue("secondary"), "team": schema.StringValue("a")},
//				Replace:  schema.Values{"name": schema.StringValue("secondary"), "team": schema.StringValue("b")},
//				Received: fake.received,
//			}},
//		})
//	}
//
// The HTTP engine and the CLI engine run the same suite with declared
// providers, so a declared provider and a written one meet one contract.
package lifecycletest

import (
	"context"
	"errors"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"opendustpan.org/providerkit/lifecycle"
	"opendustpan.org/providerkit/schema"
)

// Fixture is the provider under test and the values that the suite uses.
type Fixture struct {
	// Provider is the provider under test.
	Provider lifecycle.Provider
	// Config is the provider configuration. It can hold credentials for a
	// test instance. The suite never logs it.
	Config schema.Values
	// Resources are the resource types that the suite drives. Each type of
	// the provider needs a case.
	Resources []ResourceCase
	// DataSources are the data source types that the suite reads. Each type
	// of the provider needs a case.
	DataSources []DataSourceCase
}

// ResourceCase is the configuration of the objects of one resource type.
type ResourceCase struct {
	// Type is the resource type name.
	Type string
	// Create is the configuration of the new object.
	Create schema.Values
	// Update is a configuration that changes the object in place. Nil skips
	// the update case, for a type whose attributes all force a replace.
	Update schema.Values
	// Replace is a configuration that forces a new object. Nil skips the
	// replace case, for a type with no attribute that forces a replace.
	Replace schema.Values
	// Second is the configuration of a second object, which the suite drives
	// at the same time as the first. Nil uses Create. A remote system that
	// refuses two objects of one configuration needs a value of its own.
	Second schema.Values
	// Received returns the values that the fake of the remote system got
	// for the object with the id, in its last create or update. The suite
	// then checks that each write-only value of the configuration reached
	// it. Nil skips this check, for a test instance that cannot tell.
	Received func(id string) schema.Values
}

// DataSourceCase is the configuration of reads of one data source type.
type DataSourceCase struct {
	// Type is the data source type name.
	Type string
	// Config is the configuration of the read. It must select something.
	Config schema.Values
	// NotFound is a configuration that selects nothing. The read must give
	// lifecycle.ErrNotFound. Nil skips this case.
	NotFound schema.Values
}

// Run executes the conformance suite. The cases of one object run in order.
// Two objects of each resource type, and different types, run in parallel.
func Run(t *testing.T, f Fixture) {
	t.Helper()

	t.Run("schemas are valid and every type has a case", func(t *testing.T) {
		t.Parallel()
		testSchemas(t, f)
	})

	t.Run("each secret of the configuration has a file form", func(t *testing.T) {
		t.Parallel()
		testFileForms(t, f)
	})

	for _, rc := range f.Resources {
		t.Run("resource "+rc.Type, func(t *testing.T) {
			t.Parallel()
			testResource(t, f, rc)
		})
	}

	for _, dc := range f.DataSources {
		t.Run("data source "+dc.Type, func(t *testing.T) {
			t.Parallel()
			testDataSource(t, f, dc)
		})
	}
}

func testSchemas(t *testing.T, f Fixture) {
	t.Helper()

	if err := f.Provider.ConfigSchema().Validate(); err != nil {
		t.Errorf("provider configuration schema: %v", err)
	}

	resources := f.Provider.ResourceSchemas()
	for _, name := range slices.Sorted(maps.Keys(resources)) {
		if err := resources[name].ValidateResource(); err != nil {
			t.Errorf("resource %q: %v", name, err)
		}

		if !slices.ContainsFunc(f.Resources, func(rc ResourceCase) bool { return rc.Type == name }) {
			t.Errorf("resource %q has no case in the fixture", name)
		}
	}

	dataSources := f.Provider.DataSourceSchemas()
	for _, name := range slices.Sorted(maps.Keys(dataSources)) {
		if err := dataSources[name].ValidateDataSource(); err != nil {
			t.Errorf("data source %q: %v", name, err)
		}

		if !slices.ContainsFunc(f.DataSources, func(dc DataSourceCase) bool { return dc.Type == name }) {
			t.Errorf("data source %q has no case in the fixture", name)
		}
	}
}

// testFileForms checks the schema that lifecycle.WithFileForms gives, and
// configures the provider with the file form of each secret in the fixture.
func testFileForms(t *testing.T, f Fixture) {
	t.Helper()

	cs := f.Provider.ConfigSchema()
	viaFiles := maps.Clone(f.Config)
	both := maps.Clone(f.Config)
	dir := t.TempDir()

	for _, a := range cs.Attributes {
		if !a.Sensitive || !hasFileForm(t, cs, a) {
			continue
		}

		value, ok := f.Config.Get(a).AsString()
		if !ok {
			continue
		}

		name := a.Name + lifecycle.FileSuffix

		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(value), 0o600); err != nil {
			t.Fatalf("write the file form of %q: %v", a.Name, err)
		}

		delete(viaFiles, a.Name)
		viaFiles[name] = schema.StringValue(path)
		both[name] = schema.StringValue(path)

		configure(t, f.Provider, viaFiles, "the file form of "+a.Name)
	}

	if !maps.EqualFunc(both, f.Config, schema.Value.Equal) {
		refuseBoth(t, f.Provider, both)
	}
}

// hasFileForm checks that the sensitive attribute a is optional and has an
// optional string file form.
func hasFileForm(t *testing.T, cs schema.Schema, a schema.Attribute) bool {
	t.Helper()

	name := a.Name + lifecycle.FileSuffix

	ff, ok := cs.Lookup(name)
	if !ok || ff.Mode != schema.ModeOptional || ff.Sensitive || !ff.Type.Equal(schema.String()) {
		t.Errorf("sensitive attribute %q has no optional string %q", a.Name, name)

		return false
	}

	if a.Mode == schema.ModeRequired {
		t.Errorf("sensitive attribute %q is required, want optional next to %q", a.Name, name)
	}

	return true
}

// refuseBoth checks that the provider refuses a configuration that sets a
// secret and its file form.
func refuseBoth(t *testing.T, p lifecycle.Provider, both schema.Values) {
	t.Helper()

	s, err := p.Configure(t.Context(), both)
	if errors.Is(err, lifecycle.ErrInvalidConfig) {
		return
	}

	t.Errorf("Configure() with a secret and its file form error = %v, want ErrInvalidConfig", err)

	if err == nil && s.Close != nil {
		if err := s.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	}
}

// configure configures the provider and closes the session at the end of the
// test.
func configure(t *testing.T, p lifecycle.Provider, config schema.Values, what string) lifecycle.Session {
	t.Helper()

	s, err := p.Configure(t.Context(), config)
	if err != nil {
		t.Fatalf("Configure() with %s error = %v", what, err)
	}

	t.Cleanup(func() {
		if s.Close == nil {
			return
		}

		if err := s.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})

	return s
}

// driver drives one object of one resource type through its life cycle, as
// a protocol adapter does: it plans with lifecycle.Plan, and it keeps only
// what lifecycle.ForTool gives as state.
type driver struct {
	ctx context.Context //nolint:containedctx // The driver lives for one subtest and carries its context.
	t   *testing.T
	sch schema.Schema
	r   lifecycle.Resource
	rc  ResourceCase
}

// testResource drives two objects of the type at the same time through one
// session. Both tools apply objects in parallel.
func testResource(t *testing.T, f Fixture, rc ResourceCase) {
	t.Helper()

	r, ok := configure(t, f.Provider, f.Config, "the fixture configuration").Resources[rc.Type]
	if !ok {
		t.Fatalf("session has no resource type %q", rc.Type)
	}

	sch := f.Provider.ResourceSchemas()[rc.Type]

	t.Run("first object", func(t *testing.T) {
		t.Parallel()
		driver{ctx: t.Context(), t: t, sch: sch, r: r, rc: rc}.lifecycle()
	})

	t.Run("second object at the same time", func(t *testing.T) {
		t.Parallel()

		second := rc.Second
		if second == nil {
			second = rc.Create
		}

		d := driver{ctx: t.Context(), t: t, sch: sch, r: r, rc: rc}
		created := d.create(second)
		d.readEquals(created, created, "read after create")
		d.delete(created)
	})
}

func (d driver) lifecycle() {
	d.t.Helper()

	created := d.create(d.rc.Create)
	d.readEquals(created, created, "read after create")
	d.noOp(created, d.rc.Create)
	d.readEquals(idOnly(created), created, "import by id")

	current := created
	if d.rc.Update != nil {
		current = d.update(current, d.rc.Update)
		d.readEquals(current, current, "read after update")
		d.noOp(current, d.rc.Update)
	}

	if d.rc.Replace != nil {
		current = d.replace(current, d.rc.Replace)
		d.readEquals(current, current, "read after replace")
	}

	d.delete(current)
}

func idOnly(vs schema.Values) schema.Values {
	return schema.Values{schema.IDAttribute: vs[schema.IDAttribute]}
}

// noOp checks that the plan of the configuration that made the object
// changes nothing.
func (d driver) noOp(state, config schema.Values) {
	d.t.Helper()

	change, err := lifecycle.Plan(d.sch, state, config)
	if err != nil {
		d.t.Fatalf("Plan() after apply error = %v", err)
	}

	if change.Action != lifecycle.ActionNoOp {
		d.t.Fatalf("Plan() after apply = %v of %v, want no-op: the provider does not keep what it applied",
			change.Action, change.Changed)
	}
}

func (d driver) create(config schema.Values) schema.Values {
	d.t.Helper()

	change, err := lifecycle.Plan(d.sch, nil, config)
	if err != nil {
		d.t.Fatalf("Plan() for create error = %v", err)
	}

	got, err := d.r.Create(d.ctx, change.Planned)
	if err != nil {
		d.t.Fatalf("Create() error = %v", err)
	}

	got = lifecycle.ForTool(d.sch, got)
	checkApplied(d.t, d.sch, lifecycle.ForTool(d.sch, change.Planned), got)

	id, ok := got[schema.IDAttribute].AsString()
	if !ok || id == "" {
		d.t.Fatal("Create() returned no id")
	}

	d.checkReceived(id, config)

	return got
}

func (d driver) update(prior, config schema.Values) schema.Values {
	d.t.Helper()

	change, err := lifecycle.Plan(d.sch, prior, config)
	if err != nil {
		d.t.Fatalf("Plan() for update error = %v", err)
	}

	if change.Action != lifecycle.ActionUpdate {
		d.t.Fatalf(
			"Plan() action = %v, want update: the fixture's Update must change the object in place",
			change.Action,
		)
	}

	got, err := d.r.Update(d.ctx, prior, change.Planned)
	if err != nil {
		d.t.Fatalf("Update() error = %v", err)
	}

	got = lifecycle.ForTool(d.sch, got)
	checkApplied(d.t, d.sch, lifecycle.ForTool(d.sch, change.Planned), got)

	if !got[schema.IDAttribute].Equal(prior[schema.IDAttribute]) {
		d.t.Fatal("Update() changed the id, want the same object")
	}

	if id, ok := got[schema.IDAttribute].AsString(); ok {
		d.checkReceived(id, config)
	}

	return got
}

// replace deletes the object and creates the new one, in the default order
// of OpenTofu.
func (d driver) replace(prior, config schema.Values) schema.Values {
	d.t.Helper()

	change, err := lifecycle.Plan(d.sch, prior, config)
	if err != nil {
		d.t.Fatalf("Plan() for replace error = %v", err)
	}

	if change.Action != lifecycle.ActionReplace {
		d.t.Fatalf("Plan() action = %v, want replace: the fixture's Replace must force a new object", change.Action)
	}

	if err := d.r.Delete(d.ctx, prior); err != nil {
		d.t.Fatalf("Delete() for replace error = %v", err)
	}

	return d.create(config)
}

// delete removes the object and checks that it is gone: a read and an
// import give ErrNotFound, and a second delete succeeds.
func (d driver) delete(current schema.Values) {
	d.t.Helper()

	if err := d.r.Delete(d.ctx, current); err != nil {
		d.t.Fatalf("Delete() error = %v", err)
	}

	if _, err := d.r.Read(d.ctx, current); !errors.Is(err, lifecycle.ErrNotFound) {
		d.t.Fatalf("Read() after Delete error = %v, want ErrNotFound", err)
	}

	if _, err := d.r.Read(d.ctx, idOnly(current)); !errors.Is(err, lifecycle.ErrNotFound) {
		d.t.Fatalf("import of a missing id error = %v, want ErrNotFound", err)
	}

	if err := d.r.Delete(d.ctx, current); err != nil {
		d.t.Fatalf("second Delete() error = %v, want nil for an object that is gone", err)
	}
}

// checkReceived checks that each write-only value of the configuration
// reached the remote system.
func (d driver) checkReceived(id string, config schema.Values) {
	d.t.Helper()

	if d.rc.Received == nil {
		return
	}

	received := d.rc.Received(id)

	for _, a := range d.sch.Attributes {
		if a.WriteOnly && !config.Get(a).IsNull() && !received.Get(a).Equal(config.Get(a)) {
			d.t.Errorf("the remote system did not get the write-only attribute %q", a.Name)
		}
	}
}

// checkApplied checks the values that an apply returned, after ForTool:
// every attribute is there and known, and every known planned value is kept.
func checkApplied(t *testing.T, sch schema.Schema, planned, got schema.Values) {
	t.Helper()

	for _, a := range sch.Attributes {
		v, ok := got[a.Name]

		switch {
		case !ok:
			t.Errorf("attribute %q is missing", a.Name)
		case !v.WhollyKnown():
			t.Errorf("attribute %q is unknown after the apply", a.Name)
		case planned.Get(a).WhollyKnown() && !v.Equal(planned.Get(a)):
			t.Errorf("attribute %q changed from its planned value", a.Name)
		default:
		}
	}
}

// readEquals reads the object from prior and checks that Read finds no drift
// against want. An import passes only the id as prior.
func (d driver) readEquals(prior, want schema.Values, step string) {
	d.t.Helper()

	got, err := d.r.Read(d.ctx, prior)
	if err != nil {
		d.t.Fatalf("%s: Read() error = %v", step, err)
	}

	got = lifecycle.ForTool(d.sch, got)

	for _, a := range d.sch.Attributes {
		if !got.Get(a).Equal(want.Get(a)) {
			d.t.Errorf("%s: attribute %q drifted", step, a.Name)
		}
	}
}

func testDataSource(t *testing.T, f Fixture, dc DataSourceCase) {
	t.Helper()

	sch := f.Provider.DataSourceSchemas()[dc.Type]
	if err := lifecycle.CheckConfig(sch, dc.Config); err != nil {
		t.Fatalf("fixture configuration: %v", err)
	}

	d, ok := configure(t, f.Provider, f.Config, "the fixture configuration").DataSources[dc.Type]
	if !ok {
		t.Fatalf("session has no data source type %q", dc.Type)
	}

	first, err := d.Read(t.Context(), dc.Config)
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}

	second, err := d.Read(t.Context(), dc.Config)
	if err != nil {
		t.Fatalf("second Read() error = %v", err)
	}

	for _, a := range sch.Attributes {
		if !first.Get(a).WhollyKnown() {
			t.Errorf("attribute %q is unknown after a read", a.Name)
		}

		if !first.Get(a).Equal(second.Get(a)) {
			t.Errorf("attribute %q differs between two reads", a.Name)
		}
	}

	if dc.NotFound == nil {
		return
	}

	if err := lifecycle.CheckConfig(sch, dc.NotFound); err != nil {
		t.Fatalf("fixture NotFound configuration: %v", err)
	}

	if _, err := d.Read(t.Context(), dc.NotFound); !errors.Is(err, lifecycle.ErrNotFound) {
		t.Fatalf("Read() of a configuration that selects nothing error = %v, want ErrNotFound", err)
	}
}
