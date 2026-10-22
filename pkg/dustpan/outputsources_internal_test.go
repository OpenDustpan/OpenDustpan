package dustpan

import (
	"crypto/md5" //nolint:gosec // The protocol under test uses MD5 for integrity.
	"encoding/hex"
	"errors"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/internal/outputsharesvc"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/outputshare"
	"opendustpan.org/opendustpan/pkg/stack"
	"opendustpan.org/opendustpan/pkg/workspace"
)

// secretOutput is the deployment form of a secret output that a stack saves.
//
//nolint:gosec // The signature key is the well-known Pulumi marker of a secret, not a credential.
const secretOutput = `{"4dabf18193072939515e22adb298388d":"1b47061264138c4ac30d75fd1eb44270","ciphertext":"c2VjcmV0"}`

// stackDeployment returns a deployment whose stack resource holds the outputs.
func stackDeployment(outputs string) []byte {
	return []byte(`{"manifest":{"time":"2026-01-01T00:00:00Z","magic":"","version":""},` +
		`"secrets_providers":{"type":"service"},"resources":[` +
		`{"urn":"urn:pulumi:prod::web::pulumi:pulumi:Stack::web-prod","type":"pulumi:pulumi:Stack","outputs":` +
		outputs + `},` +
		`{"urn":"urn:pulumi:prod::web::aws:s3/bucket:Bucket::b","type":"aws:s3/bucket:Bucket","custom":true,` +
		`"outputs":{"private":` + secretOutput + `}}]}`)
}

func unitOf(t *testing.T, tool, name, id string) identity.Object {
	t.Helper()

	unit, err := identity.UnitObject(tool, "acme", name)
	if err != nil {
		t.Fatalf("unit object: %v", err)
	}

	return unit.WithUnitID(id)
}

// pushedWorkspace creates the workspace prod in acme with a state that holds a
// plain and a sensitive output, and returns the workspace and the state
// version.
func pushedWorkspace(t *testing.T, f sweepFixture) (workspace.Workspace, workspace.StateVersion) {
	t.Helper()

	name, err := workspace.ParseName("prod")
	if err != nil {
		t.Fatalf("name: %v", err)
	}

	ws, err := f.svc.workspaces.Create(t.Context(), workspace.Ref{Namespace: "acme", Name: name})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	if _, lockErr := f.svc.workspaces.Lock(t.Context(), ws.ID, "saver", "apply"); lockErr != nil {
		t.Fatalf("lock: %v", lockErr)
	}

	content := `{"serial":1,"lineage":"l","outputs":{` +
		`"token":{"sensitive":true,"value":"s3cr3t","type":"string"},` +
		`"greeting":{"sensitive":false,"value":"hi","type":"string"}}}`
	sum := md5.Sum([]byte(content)) //nolint:gosec // The protocol under test uses MD5 for integrity.

	pending, err := f.svc.workspaces.BeginState(t.Context(), ws.ID, "saver", workspace.NewState{
		Lineage: "l", MD5: hex.EncodeToString(sum[:]), Outputs: nil, Serial: 1, Force: false,
	}, workspace.Authority{Allow: namespace.Overrides{Forced: false, Unlocked: false}, Record: nil})
	if err != nil {
		t.Fatalf("declare: %v", err)
	}

	sv, err := f.svc.workspaces.Upload(t.Context(), pending.Version.ID, pending.Upload, strings.NewReader(content))
	if err != nil {
		t.Fatalf("upload: %v", err)
	}

	return ws, sv
}

// TestWorkspaceOutputsReadTheCurrentState proves the source of OpenTofu: the
// names in order, the plain value, and a sensitive output with no value.
func TestWorkspaceOutputsReadTheCurrentState(t *testing.T) {
	t.Parallel()

	f := newSweepFixture(t)
	ws, sv := pushedWorkspace(t, f)
	source := workspaceOutputs{workspaces: f.svc.workspaces}

	got, err := source.Outputs(t.Context(), unitOf(t, "opentofu", "prod", ws.ID.String()), sv.ID.String())
	if err != nil {
		t.Fatalf("Outputs: %v", err)
	}

	if len(got) != 2 || got[0].Name != "greeting" || string(got[0].Value) != `"hi"` || got[0].Secret ||
		got[1].Name != "token" || !got[1].Secret || got[1].Value != nil {
		t.Errorf("outputs = %+v, want greeting in plain and token as a secret with no value", got)
	}
}

// TestWorkspaceOutputsRefuseAStateThatMoved proves the refusal of a state that
// is not the latest, and of a workspace ID that is not the one of the unit.
func TestWorkspaceOutputsRefuseAStateThatMoved(t *testing.T) {
	t.Parallel()

	f := newSweepFixture(t)
	ws, sv := pushedWorkspace(t, f)
	source := workspaceOutputs{workspaces: f.svc.workspaces}
	unit := unitOf(t, "opentofu", "prod", ws.ID.String())

	if _, err := source.Outputs(t.Context(), unit, "sv-older"); !errors.Is(err, outputsharesvc.ErrStateMoved) {
		t.Errorf("Outputs of another state = %v, want ErrStateMoved", err)
	}

	other := unitOf(t, "opentofu", "prod", "ws-other")
	if _, err := source.Outputs(t.Context(), other, sv.ID.String()); err == nil {
		t.Error("Outputs of an unknown workspace ID = nil, want an error")
	}
}

// savedStack creates the stack web/prod in acme with a checkpoint whose
// outputs hold a plain output, a secret and a secret inside an object, and
// returns the stack and the update.
func savedStack(t *testing.T, f sweepFixture) (stack.Stack, stack.Update) {
	t.Helper()

	ref := stack.Ref{Namespace: "acme", Project: "web", Stack: "prod"}

	st, err := f.svc.stacks.Create(t.Context(), ref, nil)
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	update, err := f.svc.updates.Import(t.Context(), ref, "saver", stack.CheckpointInput{
		Features: nil, SchemaVersion: 3, Sequence: 0, Invalid: false,
		Deployment: stackDeployment(
			`{"url":"https://x","token":` + secretOutput + `,"nested":{"k":` + secretOutput + `}}`,
		),
	})
	if err != nil {
		t.Fatalf("import: %v", err)
	}

	return st, update
}

// TestStackOutputsReadTheLatestCheckpoint proves the source of Pulumi: the root
// outputs only, a secret as a mark with no value and no ciphertext, and a
// secret inside an object as a marker.
func TestStackOutputsReadTheLatestCheckpoint(t *testing.T) {
	t.Parallel()

	f := newSweepFixture(t)
	st, update := savedStack(t, f)
	source := stackOutputs{stacks: f.svc.stacks, updates: f.svc.updates}

	got, err := source.Outputs(
		t.Context(), unitOf(t, "pulumi", "web/prod", st.ID.String()), stack.StateID(st.ID, update.Version),
	)
	if err != nil {
		t.Fatalf("Outputs: %v", err)
	}

	if len(got) != 3 || got[0].Name != "nested" || got[1].Name != "token" || got[2].Name != "url" {
		t.Fatalf("outputs = %+v, want nested, token and url in order", got)
	}

	if got[1].Value != nil || !got[1].Secret {
		t.Errorf("token = %+v, want a secret with no value", got[1])
	}

	if string(got[2].Value) != `"https://x"` || got[2].Secret {
		t.Errorf("url = %+v, want the plain value", got[2])
	}

	for _, output := range got {
		if strings.Contains(string(output.Value), "c2VjcmV0") || strings.Contains(string(output.Value), "ciphertext") {
			t.Errorf("output %q holds ciphertext: %s", output.Name, output.Value)
		}
	}
}

// TestStackOutputsRefuse proves the refusal of a state that is not the latest,
// of a stack ID that is not the one of the stack, and of a name with no project.
func TestStackOutputsRefuse(t *testing.T) {
	t.Parallel()

	f := newSweepFixture(t)
	st, update := savedStack(t, f)
	source := stackOutputs{stacks: f.svc.stacks, updates: f.svc.updates}
	stateID := stack.StateID(st.ID, update.Version)

	tests := []struct {
		name    string
		unit    identity.Object
		stateID string
		want    error
	}{
		{
			"another version",
			unitOf(t, "pulumi", "web/prod", st.ID.String()),
			stack.StateID(st.ID, update.Version+1),
			outputsharesvc.ErrStateMoved,
		},
		{"another stable ID", unitOf(t, "pulumi", "web/prod", "stk-other"), stateID, outputshare.ErrUnknownProducer},
		{"a name with no project", unitOf(t, "pulumi", "noproject", st.ID.String()), stateID, outputshare.ErrInvalid},
	}

	for _, tt := range tests {
		if _, err := source.Outputs(t.Context(), tt.unit, tt.stateID); !errors.Is(err, tt.want) {
			t.Errorf("Outputs with %s = %v, want %v", tt.name, err, tt.want)
		}
	}
}

// TestStackRootOutputsHandleOddDeployments proves the edge cases of the
// parse: no stack resource, no outputs, and a plain output that has the shape
// of a plain object.
func TestStackRootOutputsHandleOddDeployments(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		deployment string
		want       int
	}{
		{"no resources", `{"manifest":{},"resources":[]}`, 0},
		{"no outputs", `{"manifest":{},"resources":[{"urn":"u","type":"pulumi:pulumi:Stack"}]}`, 0},
		{
			"a plain object output",
			`{"manifest":{},"resources":[{"urn":"u","type":"pulumi:pulumi:Stack","outputs":{"a":{"secret":true}}}]}`,
			1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := stackRootOutputs([]byte(tt.deployment))
			if err != nil || len(got) != tt.want {
				t.Fatalf("stackRootOutputs = %+v, %v; want %d outputs", got, err, tt.want)
			}

			for _, output := range got {
				if output.Secret || output.Value == nil {
					t.Errorf("output %+v: an object with a secret member and no path is a plain output", output)
				}
			}
		})
	}

	if _, err := stackRootOutputs([]byte(`{`)); err == nil {
		t.Error("stackRootOutputs of broken JSON = nil, want an error")
	}
}
