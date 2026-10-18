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
//				Type:   "schedule",
//				Create: schema.Values{"name": schema.StringValue("primary")},
//				Update: schema.Values{"name": schema.StringValue("secondary")},
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
	"slices"
	"testing"

	"opendustpan.org/opendustpan/providerkit/lifecycle"
	"opendustpan.org/opendustpan/providerkit/schema"
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

// ResourceCase is the configuration of one object of a resource type.
type ResourceCase struct {
	// Type is the resource type name.
	Type string
	// Create is the configuration of the new object.
	Create schema.Values
	// Update is a configuration that changes the object in place. Nil skips
	// the update case, for a type whose attributes all force a replace.
	Update schema.Values
}

// DataSourceCase is the configuration of one read of a data source type.
type DataSourceCase struct {
	// Type is the data source type name.
	Type string
	// Config is the configuration of the read. It must select something.
	Config schema.Values
}

// Run executes the conformance suite. The cases of one resource type run in
// order on one object. Different types run in parallel.
func Run(t *testing.T, f Fixture) {
	t.Helper()

	t.Run("schemas are valid and every type has a case", func(t *testing.T) {
		t.Parallel()
		testSchemas(t, f)
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
		if err := dataSources[name].Validate(); err != nil {
			t.Errorf("data source %q: %v", name, err)
		}

		if !slices.ContainsFunc(f.DataSources, func(dc DataSourceCase) bool { return dc.Type == name }) {
			t.Errorf("data source %q has no case in the fixture", name)
		}
	}
}

// session configures the provider and closes the session at the end of the
// test.
func session(t *testing.T, f Fixture) lifecycle.Session {
	t.Helper()

	s, err := f.Provider.Configure(t.Context(), f.Config)
	if err != nil {
		t.Fatalf("Configure() error = %v", err)
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

// driver drives one object of one resource type through its life cycle.
type driver struct {
	ctx context.Context //nolint:containedctx // The driver lives for one subtest and carries its context.
	t   *testing.T
	sch schema.Schema
	r   lifecycle.Resource
}

func testResource(t *testing.T, f Fixture, rc ResourceCase) {
	t.Helper()

	r, ok := session(t, f).Resources[rc.Type]
	if !ok {
		t.Fatalf("session has no resource type %q", rc.Type)
	}

	d := driver{ctx: t.Context(), t: t, sch: f.Provider.ResourceSchemas()[rc.Type], r: r}

	created := d.create(rc.Create)
	d.readEquals(created, created, "read after create")
	d.readEquals(schema.Values{schema.IDAttribute: created[schema.IDAttribute]}, created, "import by id")

	current := created
	if rc.Update != nil {
		current = d.update(created, rc.Update)
		d.readEquals(current, current, "read after update")
	}

	if err := r.Delete(d.ctx, current); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}

	if _, err := r.Read(d.ctx, current); !errors.Is(err, lifecycle.ErrNotFound) {
		t.Fatalf("Read() after Delete error = %v, want ErrNotFound", err)
	}

	if err := r.Delete(d.ctx, current); err != nil {
		t.Fatalf("second Delete() error = %v, want nil for an object that is gone", err)
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

	checkApplied(d.t, d.sch, change.Planned, got)

	if id, ok := got[schema.IDAttribute].AsString(); !ok || id == "" {
		d.t.Fatal("Create() returned no id")
	}

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

	checkApplied(d.t, d.sch, change.Planned, got)

	if !got[schema.IDAttribute].Equal(prior[schema.IDAttribute]) {
		d.t.Fatal("Update() changed the id, want the same object")
	}

	return got
}

// checkApplied checks the values that an apply returned: every attribute is
// there and known, write-only ones are null, and every known planned value
// is kept.
func checkApplied(t *testing.T, sch schema.Schema, planned, got schema.Values) {
	t.Helper()

	for _, a := range sch.Attributes {
		v, ok := got[a.Name]

		switch {
		case !ok:
			t.Errorf("attribute %q is missing", a.Name)
		case !v.WhollyKnown():
			t.Errorf("attribute %q is unknown after the apply", a.Name)
		case a.WriteOnly && !v.IsNull():
			t.Errorf("write-only attribute %q is in the returned values", a.Name)
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

	d, ok := session(t, f).DataSources[dc.Type]
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
}
