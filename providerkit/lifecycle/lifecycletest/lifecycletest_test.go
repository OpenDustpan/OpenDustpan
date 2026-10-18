package lifecycletest_test

import (
	"context"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"

	"opendustpan.org/providerkit/lifecycle"
	"opendustpan.org/providerkit/lifecycle/lifecycletest"
	"opendustpan.org/providerkit/schema"
)

// TestSuiteAgainstMemoryProvider runs the suite against an in-memory
// provider of notes, so the suite itself has a test.
func TestSuiteAgainstMemoryProvider(t *testing.T) {
	t.Parallel()

	runSuite(t, newNotes(t, ""))
}

// faultEnv names the fault of the provider that TestFaultyProvider runs the
// suite against, in a child process of TestSuiteFindsFaults.
const faultEnv = "LIFECYCLETEST_FAULT"

// TestSuiteFindsFaults runs the suite against a provider with one fault, in
// a child process, and checks that the suite fails with the message of the
// step that finds the fault.
func TestSuiteFindsFaults(t *testing.T) {
	t.Parallel()

	tests := []struct {
		fault string
		want  string
	}{
		{fault: "text", want: "want no-op: the provider does not keep what it applied"},
		{fault: "key", want: `the remote system did not get the write-only attribute "key"`},
	}

	for _, tt := range tests {
		t.Run(tt.fault, func(t *testing.T) {
			t.Parallel()

			//nolint:gosec // The test binary itself, with fixed arguments.
			cmd := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestFaultyProvider$", "-test.v")
			cmd.Env = append(os.Environ(), faultEnv+"="+tt.fault)

			out, err := cmd.CombinedOutput()
			if err == nil {
				t.Fatalf("the suite passed a provider with the fault %q, want a failure", tt.fault)
			}

			if !strings.Contains(string(out), tt.want) {
				t.Fatalf("the suite output does not have %q:\n%s", tt.want, out)
			}
		})
	}
}

// TestFaultyProvider runs the suite against a provider with the fault that
// faultEnv names. It runs only as a child process of TestSuiteFindsFaults.
func TestFaultyProvider(t *testing.T) {
	t.Parallel()

	fault := os.Getenv(faultEnv)
	if fault == "" {
		t.Skip("runs only in a child process of TestSuiteFindsFaults")
	}

	runSuite(t, newNotes(t, fault))
}

func runSuite(t *testing.T, n *notes) {
	t.Helper()

	lifecycletest.Run(t, lifecycletest.Fixture{
		Provider: n,
		Config:   schema.Values{"owner": str("ops"), "token": str("t0ken")},
		Resources: []lifecycletest.ResourceCase{{
			Type:     "note",
			Create:   schema.Values{"folder": str("a"), "text": str("one"), "key": str("k1")},
			Update:   schema.Values{"folder": str("a"), "text": str("two"), "key": str("k2")},
			Replace:  schema.Values{"folder": str("b"), "text": str("two"), "key": str("k3")},
			Second:   nil,
			Received: n.received,
		}},
		DataSources: []lifecycletest.DataSourceCase{{
			Type:     "owner",
			Config:   schema.Values{},
			NotFound: schema.Values{"name": str("nobody")},
		}},
	})
}

func str(s string) schema.Value { return schema.StringValue(s) }

func attr(name string, mode schema.Mode) schema.Attribute {
	return schema.Attribute{
		Name: name, Type: schema.String(), Mode: mode,
		Sensitive: false, WriteOnly: false, Replace: false, Stable: false, Description: "",
	}
}

// notes is a provider with one resource type, "note", and one data source
// type, "owner". It keeps notes in a map. The write-only key of a note goes
// to the map of received values, and never back into the values of a note.
// A fault makes it break the contract: "text" keeps another text than the
// planned one, and "key" does not send the write-only key.
type notes struct {
	fault  string
	config schema.Schema
	mu     sync.Mutex
	next   int
	notes  map[string]schema.Values
	got    map[string]schema.Values
}

func newNotes(t *testing.T, fault string) *notes {
	t.Helper()

	config, err := lifecycle.WithFileForms(baseConfig())
	if err != nil {
		t.Fatalf("WithFileForms() error = %v", err)
	}

	return &notes{
		fault:  fault,
		config: config,
		mu:     sync.Mutex{},
		next:   0,
		notes:  map[string]schema.Values{},
		got:    map[string]schema.Values{},
	}
}

func (*notes) Info() lifecycle.Info { return lifecycle.Info{Name: "notes", Version: "0.0.0"} }

// baseConfig is the configuration that the author declares. The provider
// serves it with the file forms.
func baseConfig() schema.Schema {
	token := attr("token", schema.ModeRequired)
	token.Sensitive = true

	return schema.Schema{Version: 0, Attributes: []schema.Attribute{attr("owner", schema.ModeRequired), token}}
}

func (n *notes) ConfigSchema() schema.Schema { return n.config }

func (*notes) ResourceSchemas() map[string]schema.Schema {
	folder := attr("folder", schema.ModeRequired)
	folder.Replace = true

	key := attr("key", schema.ModeOptional)
	key.Sensitive = true
	key.WriteOnly = true

	created := attr("created", schema.ModeComputed)
	created.Stable = true

	return map[string]schema.Schema{"note": {Version: 0, Attributes: []schema.Attribute{
		attr("id", schema.ModeComputed), folder, attr("text", schema.ModeRequired), key,
		created, attr("revision", schema.ModeComputed),
	}}}
}

func (*notes) DataSourceSchemas() map[string]schema.Schema {
	return map[string]schema.Schema{"owner": {Version: 0, Attributes: []schema.Attribute{
		attr("name", schema.ModeOptionalComputed),
	}}}
}

func (n *notes) Configure(_ context.Context, config schema.Values) (lifecycle.Session, error) {
	none := lifecycle.Session{Resources: nil, DataSources: nil, Close: nil}
	if err := lifecycle.CheckConfig(n.ConfigSchema(), config); err != nil {
		return none, fmt.Errorf("configure notes: %w", err)
	}

	if err := lifecycle.CheckFileForms(baseConfig(), config, func(string) bool { return false }); err != nil {
		return none, fmt.Errorf("configure notes: %w", err)
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
	id := fmt.Sprintf("n%d", n.next)
	planned = maps.Clone(planned)
	planned["created"] = str(fmt.Sprintf("t%d", n.next))

	if n.fault == "text" {
		planned["text"] = str(mustString(planned["text"]) + " (edited)")
	}

	return n.store(id, planned, 1), nil
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

	old, ok := n.notes[id]
	if !ok {
		return nil, fmt.Errorf("note %q: %w", id, lifecycle.ErrNotFound)
	}

	var r int
	if _, err := fmt.Sscanf(mustString(old["revision"]), "r%d", &r); err != nil {
		return nil, fmt.Errorf("note %q revision: %w", id, err)
	}

	planned = maps.Clone(planned)
	planned["created"] = old["created"]

	return n.store(id, planned, r+1), nil
}

func mustString(v schema.Value) string {
	s, _ := v.AsString()

	return s
}

func (n *notes) Delete(_ context.Context, prior schema.Values) error {
	n.mu.Lock()
	defer n.mu.Unlock()

	id, _ := prior["id"].AsString()
	delete(n.notes, id)
	delete(n.got, id)

	return nil
}

// received returns the values of the last create or update of the note.
func (n *notes) received(id string) schema.Values {
	n.mu.Lock()
	defer n.mu.Unlock()

	return maps.Clone(n.got[id])
}

// store keeps planned as the note id, with revision r. The write-only key
// goes only to the received values.
func (n *notes) store(id string, planned schema.Values, r int) schema.Values {
	n.got[id] = maps.Clone(planned)
	if n.fault == "key" {
		n.got[id]["key"] = schema.NullOf(schema.String())
	}

	note := maps.Clone(planned)
	note["id"] = str(id)
	note["key"] = schema.NullOf(schema.String())
	note["revision"] = str(fmt.Sprintf("r%d", r))
	n.notes[id] = note

	return maps.Clone(note)
}

type ownerSource struct {
	owner schema.Value
}

func (o ownerSource) Read(_ context.Context, config schema.Values) (schema.Values, error) {
	if name, ok := config["name"]; ok && !name.IsNull() && !name.Equal(o.owner) {
		return nil, fmt.Errorf("owner %v: %w", name, lifecycle.ErrNotFound)
	}

	return schema.Values{"name": o.owner}, nil
}
