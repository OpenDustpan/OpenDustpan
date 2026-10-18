package lifecycletest_test

import (
	"context"
	"fmt"
	"maps"
	"sync"
	"testing"

	"opendustpan.org/opendustpan/providerkit/lifecycle"
	"opendustpan.org/opendustpan/providerkit/lifecycle/lifecycletest"
	"opendustpan.org/opendustpan/providerkit/schema"
)

// TestSuiteAgainstMemoryProvider runs the suite against an in-memory
// provider of notes, so the suite itself has a test.
func TestSuiteAgainstMemoryProvider(t *testing.T) {
	t.Parallel()

	lifecycletest.Run(t, lifecycletest.Fixture{
		Provider: newNotes(),
		Config:   schema.Values{"owner": schema.StringValue("ops")},
		Resources: []lifecycletest.ResourceCase{{
			Type:   "note",
			Create: schema.Values{"folder": schema.StringValue("a"), "text": schema.StringValue("one")},
			Update: schema.Values{"folder": schema.StringValue("a"), "text": schema.StringValue("two")},
		}},
		DataSources: []lifecycletest.DataSourceCase{{
			Type:   "owner",
			Config: schema.Values{},
		}},
	})
}

func attr(name string, mode schema.Mode) schema.Attribute {
	return schema.Attribute{
		Name: name, Type: schema.String(), Mode: mode,
		Sensitive: false, WriteOnly: false, Replace: false, Description: "",
	}
}

// notes is a provider with one resource type, "note", and one data source
// type, "owner". It keeps notes in a map.
type notes struct {
	mu    sync.Mutex
	next  int
	notes map[string]schema.Values
}

func newNotes() *notes {
	return &notes{mu: sync.Mutex{}, next: 0, notes: map[string]schema.Values{}}
}

func (*notes) Info() lifecycle.Info { return lifecycle.Info{Name: "notes", Version: "0.0.0"} }

func (*notes) ConfigSchema() schema.Schema {
	return schema.Schema{Version: 0, Attributes: []schema.Attribute{attr("owner", schema.ModeRequired)}}
}

func (*notes) ResourceSchemas() map[string]schema.Schema {
	folder := attr("folder", schema.ModeRequired)
	folder.Replace = true

	return map[string]schema.Schema{"note": {Version: 0, Attributes: []schema.Attribute{
		attr("id", schema.ModeComputed), folder, attr("text", schema.ModeRequired),
	}}}
}

func (*notes) DataSourceSchemas() map[string]schema.Schema {
	return map[string]schema.Schema{"owner": {Version: 0, Attributes: []schema.Attribute{
		attr("name", schema.ModeComputed),
	}}}
}

func (n *notes) Configure(_ context.Context, config schema.Values) (lifecycle.Session, error) {
	if err := lifecycle.CheckConfig(n.ConfigSchema(), config); err != nil {
		return lifecycle.Session{Resources: nil, DataSources: nil, Close: nil}, err
	}

	return lifecycle.Session{
		Resources:   map[string]lifecycle.Resource{"note": n},
		DataSources: map[string]lifecycle.DataSource{"owner": ownerSource{owner: config["owner"]}},
		Close:       nil,
	}, nil
}

func (n *notes) Create(_ context.Context, planned schema.Values) (schema.Values, error) {
	n.mu.Lock()
	defer n.mu.Unlock()

	n.next++
	got := maps.Clone(planned)
	got["id"] = schema.StringValue(fmt.Sprintf("n%d", n.next))
	n.notes[fmt.Sprintf("n%d", n.next)] = got

	return maps.Clone(got), nil
}

func (n *notes) Read(_ context.Context, prior schema.Values) (schema.Values, error) {
	n.mu.Lock()
	defer n.mu.Unlock()

	id, _ := prior["id"].AsString()

	got, ok := n.notes[id]
	if !ok {
		return nil, fmt.Errorf("note %q: %w", id, lifecycle.ErrNotFound)
	}

	return maps.Clone(got), nil
}

func (n *notes) Update(_ context.Context, prior, planned schema.Values) (schema.Values, error) {
	n.mu.Lock()
	defer n.mu.Unlock()

	id, _ := prior["id"].AsString()
	if _, ok := n.notes[id]; !ok {
		return nil, fmt.Errorf("note %q: %w", id, lifecycle.ErrNotFound)
	}

	n.notes[id] = maps.Clone(planned)

	return maps.Clone(planned), nil
}

func (n *notes) Delete(_ context.Context, prior schema.Values) error {
	n.mu.Lock()
	defer n.mu.Unlock()

	id, _ := prior["id"].AsString()
	delete(n.notes, id)

	return nil
}

type ownerSource struct {
	owner schema.Value
}

func (o ownerSource) Read(context.Context, schema.Values) (schema.Values, error) {
	return schema.Values{"name": o.owner}, nil
}
