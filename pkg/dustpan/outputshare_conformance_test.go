package dustpan_test

import (
	"context"
	"crypto/md5" //nolint:gosec // The protocol under test uses MD5 for integrity.
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/outputsharesvc"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/blob/fs"
	"opendustpan.org/opendustpan/pkg/dustpan"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/outputshare"
	"opendustpan.org/opendustpan/pkg/outputshare/outputsharetest"
	"opendustpan.org/opendustpan/pkg/secret"
	"opendustpan.org/opendustpan/pkg/stack"
	"opendustpan.org/opendustpan/pkg/store/postgres"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
	"opendustpan.org/opendustpan/pkg/workspace"
)

// grantAuthorizer allows exactly the grants that the suite gave. The cases of
// the roles are in the tests of package access: the checker only asks this
// port.
type grantAuthorizer struct {
	mu     sync.Mutex
	grants map[string]bool
}

func grantKey(subject string, action identity.Action, object identity.Object) string {
	return subject + "|" + string(action) + "|" + object.String()
}

// Authorize implements outputsharesvc.Authorizer.
func (g *grantAuthorizer) Authorize(
	_ context.Context, p identity.Principal, action identity.Action, obj identity.Object,
) error {
	g.mu.Lock()
	defer g.mu.Unlock()

	if g.grants[grantKey(p.Subject, action, obj)] {
		return nil
	}

	return fmt.Errorf("%w: %s on %s", identity.ErrDenied, action, obj)
}

func (g *grantAuthorizer) grant(subject string, action identity.Action, object identity.Object) {
	g.mu.Lock()
	defer g.mu.Unlock()

	g.grants[grantKey(subject, action, object)] = true
}

// outputShareStore is what the harness needs of a store besides the options.
type outputShareStore interface {
	namespace.Store
	audit.Store
	stack.UpdateStore
	stack.JournalStore
	stack.CheckpointStore
	workspace.StateStore
	// The update store is also the index of the item keys of the updates.
	secret.ItemKeyIndex
	// OutputSharingSettings returns the port of the setting.
	OutputSharingSettings() outputshare.Settings
}

// outputShareOptions opens the store of the dialect and returns the options of
// a server over it, and the store.
//
//nolint:ireturn // The two SQL stores share no concrete type, so the helper returns the port.
func outputShareOptions(t *testing.T, dialect string) ([]dustpan.Option, outputShareStore) {
	t.Helper()

	wrapper, err := secret.NewBuiltinWrapper([]byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatalf("key wrapper: %v", err)
	}

	blobs, err := fs.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open blob store: %v", err)
	}

	common := []dustpan.Option{
		dustpan.WithInstallation("test"), dustpan.WithSecretWrapper(wrapper), dustpan.WithBlobStore(blobs),
		dustpan.WithClock(time.Now), dustpan.WithLogger(slog.New(slog.NewTextHandler(t.Output(), nil))),
	}

	switch dialect {
	case "sqlite":
		st, err := sqlite.Open(t.Context(), filepath.Join(t.TempDir(), "dustpan.db"))
		if err != nil {
			t.Fatalf("open store: %v", err)
		}

		t.Cleanup(func() {
			if closeErr := st.Close(); closeErr != nil {
				t.Errorf("close store: %v", closeErr)
			}
		})

		return append(common,
			dustpan.WithNamespaceStore(st), dustpan.WithStackStore(st), dustpan.WithUpdateStore(st),
			dustpan.WithCheckpointStore(st), dustpan.WithEventStore(st), dustpan.WithSecretKeyStore(st),
			dustpan.WithWorkspaceStore(st), dustpan.WithStateStore(st), dustpan.WithInventoryReader(st),
		), st
	case "postgres":
		base := os.Getenv(postgresURLEnv)
		if base == "" {
			t.Skipf("%s is not set", postgresURLEnv)
		}

		st, err := postgres.Open(t.Context(), isolatedPostgresURL(t, base))
		if err != nil {
			t.Fatalf("open store: %v", err)
		}

		t.Cleanup(st.Close)

		return append(common,
			dustpan.WithNamespaceStore(st), dustpan.WithStackStore(st), dustpan.WithUpdateStore(st),
			dustpan.WithCheckpointStore(st), dustpan.WithEventStore(st), dustpan.WithSecretKeyStore(st),
			dustpan.WithWorkspaceStore(st), dustpan.WithStateStore(st), dustpan.WithInventoryReader(st),
		), st
	default:
		t.Fatalf("unknown dialect %q", dialect)

		return nil, nil
	}
}

// outputShareRig is the services of the tools over one store, with the ports
// of output sharing on them.
type outputShareRig struct {
	t       *testing.T
	svc     dustpan.ToolServices
	store   outputShareStore
	authz   *grantAuthorizer
	checker *outputsharesvc.Checker
	states  outputsharesvc.ToolStates
	outputs toolOutputs
	race    *readRace
	mu      sync.Mutex
	serials map[string]int64
}

// newOutputShareRig opens a store of the dialect and builds the ports.
func newOutputShareRig(t *testing.T, dialect string) *outputShareRig {
	t.Helper()

	opts, st := outputShareOptions(t, dialect)

	race := &readRace{mu: sync.Mutex{}, armed: false, left: 0, fired: false, hook: nil}
	opts = append(opts,
		dustpan.WithUpdateStore(raceUpdates{UpdateStore: st, JournalStore: st, ItemKeyIndex: st, race: race}),
		dustpan.WithCheckpointStore(raceCheckpoints{CheckpointStore: st, race: race}),
		dustpan.WithStateStore(raceStates{StateStore: st, race: race}),
	)

	svc, err := dustpan.NewToolServices(opts...)
	if err != nil {
		t.Fatalf("services: %v", err)
	}

	authz := &grantAuthorizer{mu: sync.Mutex{}, grants: map[string]bool{}}
	states := outputsharesvc.ToolStates{
		"opentofu": workspace.NewOutputStates(svc.Workspaces),
		"pulumi":   stack.NewOutputStates(svc.Stacks, svc.Updates),
	}

	checker, err := outputsharesvc.NewChecker(outputsharesvc.CheckerConfig{
		Authorizer: authz, States: states, Namespaces: svc.Namespaces, Audit: st,
		Logger: slog.New(slog.NewTextHandler(t.Output(), nil)), Now: nil,
	})
	if err != nil {
		t.Fatalf("checker: %v", err)
	}

	outputs := toolOutputs{
		"opentofu": workspace.NewOutputStates(svc.Workspaces),
		"pulumi":   stack.NewOutputStates(svc.Stacks, svc.Updates),
	}

	return &outputShareRig{
		t: t, svc: svc, store: st, authz: authz, checker: checker, states: states, outputs: outputs, race: race,
		mu: sync.Mutex{}, serials: map[string]int64{},
	}
}

// harness returns the harness of the suite over the rig.
func (r *outputShareRig) harness() outputsharetest.Harness {
	return outputsharetest.Harness{
		Store:        nil,
		Settings:     r.store.OutputSharingSettings(),
		Attachments:  nil,
		UnitFault:    nil,
		Authority:    nil,
		Profile:      nil,
		Attach:       nil,
		Job:          nil,
		Disable:      nil,
		Calls:        nil,
		StoreRaw:     nil,
		SettingRaw:   nil,
		Checker:      r.checker,
		States:       r.states,
		Outputs:      r.outputs,
		SaveOutputs:  r.saveOutputs,
		DeleteUnit:   r.deleteUnit,
		DeleteOnRead: r.deleteOnRead,
		Namespace:    r.namespace,
		Unit:         r.unit,
		SaveState:    r.saveState,
		Grant:        r.grant,
		Audited:      r.audited,
	}
}

func (r *outputShareRig) namespace(t *testing.T, name string) namespace.Fence {
	t.Helper()

	ns, err := r.svc.Namespaces.Create(t.Context(), namespace.Name(name))
	if err != nil {
		var getErr error

		ns, getErr = r.svc.Namespaces.Get(t.Context(), namespace.Name(name))
		if getErr != nil {
			t.Fatalf("create namespace %s: %v; get: %v", name, err, getErr)
		}
	}

	return ns.Fence()
}

func (r *outputShareRig) grant(_ *testing.T, principal string, action identity.Action, object identity.Object) {
	r.authz.grant(principal, action, object)
}

func (r *outputShareRig) audited(t *testing.T, ns string) []string {
	t.Helper()

	listing, err := r.store.List(t.Context(), namespace.Name(ns), audit.Page{Number: 1, Size: audit.MaxPageSize})
	if err != nil {
		t.Fatalf("list audit entries of %s: %v", ns, err)
	}

	actions := make([]string, 0, len(listing.Entries))
	for i := range slices.Backward(listing.Entries) {
		actions = append(actions, listing.Entries[i].Action)
	}

	return actions
}

// unit makes a unit of the tool. A stack gets the project "web".
func (r *outputShareRig) unit(
	t *testing.T, tool string, ns namespace.Name, name, _ string,
) outputshare.Placement {
	t.Helper()

	var (
		unitName string
		id       string
	)

	switch tool {
	case "opentofu":
		parsed, err := workspace.ParseName(name)
		if err != nil {
			t.Fatalf("workspace name %q: %v", name, err)
		}

		ws, err := r.svc.Workspaces.Create(
			t.Context(), workspace.Ref{Namespace: ns, Name: parsed}, placeWorkspace(t, r.store, ns),
		)
		if err != nil {
			t.Fatalf("create workspace %s: %v", name, err)
		}

		unitName, id = name, ws.ID.String()
	case "pulumi":
		ref, err := stack.ParseRef(ns.String(), "web", name)
		if err != nil {
			t.Fatalf("stack ref %q: %v", name, err)
		}

		st, err := r.svc.Stacks.CreatePlaced(t.Context(), ref, nil, placeStack(t, r.store, ns))
		if err != nil {
			t.Fatalf("create stack %s: %v", name, err)
		}

		unitName, id = "web/"+name, st.ID.String()
	default:
		t.Fatalf("unknown tool %q", tool)
	}

	object, err := identity.UnitObject(tool, ns, unitName)
	if err != nil {
		t.Fatalf("unit object: %v", err)
	}

	return outputshare.Placement{Unit: object.WithUnitID(id), Collection: identity.Object{}}
}

// md5Hex returns the hex MD5 of content, which the declare of a state version
// carries.
func md5Hex(content string) string {
	sum := md5.Sum([]byte(content)) //nolint:gosec // The protocol under test uses MD5 for integrity.

	return hex.EncodeToString(sum[:])
}

// nextSerial returns the next serial of the unit.
func (r *outputShareRig) nextSerial(unit identity.Object) int64 {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.serials[unit.String()]++

	return r.serials[unit.String()]
}

// saveState saves a new latest state of the unit and returns its ID as
// State.ID names it.
//
//nolint:revive // The SaveState hook of the suite has this signature.
func (r *outputShareRig) saveState(t *testing.T, unit outputshare.Placement, encrypted bool) string {
	t.Helper()

	serial := r.nextSerial(unit.Unit)

	switch unit.Unit.Tool() {
	case "opentofu":
		content := plainStateContent(serial)
		if encrypted {
			content = encryptedStateContent(serial)
		}

		return r.saveWorkspaceState(t, unit.Unit, serial, content)
	case "pulumi":
		return r.saveStackCheckpoint(t, unit.Unit, serial)
	default:
		t.Fatalf("unknown tool %q", unit.Unit.Tool())

		return ""
	}
}

// plainStateContent returns the state document of the serial that the server
// reads.
func plainStateContent(serial int64) string {
	return fmt.Sprintf(`{"serial":%d,"lineage":"lineage-1","outputs":{}}`, serial)
}

// encryptedStateContent returns the state document of the serial that the
// client encrypted.
func encryptedStateContent(serial int64) string {
	return fmt.Sprintf(`{"serial":%d,"lineage":"lineage-1","encrypted_data":"aa"}`, serial)
}

func (r *outputShareRig) saveWorkspaceState(t *testing.T, unit identity.Object, serial int64, content string) string {
	t.Helper()

	ctx := t.Context()
	id := workspace.ID(unit.UnitID())

	if _, err := r.svc.Workspaces.Lock(ctx, id, "saver", "apply"); err != nil {
		t.Fatalf("lock %s: %v", unit, err)
	}

	pending, err := r.svc.Workspaces.BeginState(ctx, id, "saver", workspace.NewState{
		Lineage: "lineage-1", MD5: md5Hex(content), Outputs: nil, Serial: serial, Force: false,
	}, workspace.Authority{Allow: namespace.Overrides{Forced: false, Unlocked: false}, Record: nil})
	if err != nil {
		t.Fatalf("declare state of %s: %v", unit, err)
	}

	sv, err := r.svc.Workspaces.Upload(ctx, pending.Version.ID, pending.Upload, strings.NewReader(content))
	if err != nil {
		t.Fatalf("upload state of %s: %v", unit, err)
	}

	if _, err := r.svc.Workspaces.Unlock(ctx, id, "saver"); err != nil {
		t.Fatalf("unlock %s: %v", unit, err)
	}

	return sv.ID.String()
}

func (r *outputShareRig) saveStackCheckpoint(t *testing.T, unit identity.Object, serial int64) string {
	t.Helper()

	return r.saveStackDeployment(t, unit, fmt.Appendf(nil, `{"imported":true,"serial":%d}`, serial))
}

// saveStackDeployment imports deployment as the latest checkpoint of the
// stack of the unit and returns its ID as State.ID names it.
func (r *outputShareRig) saveStackDeployment(t *testing.T, unit identity.Object, deployment []byte) string {
	t.Helper()

	update, err := r.svc.Updates.Import(t.Context(), r.stackRef(t, unit), "saver", stack.CheckpointInput{
		Features: nil, Deployment: deployment, SchemaVersion: 1, Sequence: 0, Invalid: false,
	})
	if err != nil {
		t.Fatalf("import checkpoint of %s: %v", unit, err)
	}

	return stack.StateID(stack.ID(unit.UnitID()), update.Version)
}

// toolOutputs routes the Outputs port to the port of the tool of the unit, as
// the reader of the server does.
type toolOutputs map[string]outputshare.Outputs

// Outputs implements outputshare.Outputs.
func (o toolOutputs) Outputs(ctx context.Context, u identity.Object, stateID string) ([]outputshare.Output, error) {
	port, ok := o[u.Tool()]
	if !ok {
		return nil, fmt.Errorf("%w: no output source for %q", outputshare.ErrInvalid, u)
	}

	out, err := port.Outputs(ctx, u, stateID)
	if err != nil {
		return nil, fmt.Errorf("outputs of %s: %w", u, err)
	}

	return out, nil
}

// hiddenCiphertext is the ciphertext of a secret value in a checkpoint: the
// base64 of the plain text of the suite.
func hiddenCiphertext() string {
	return base64.StdEncoding.EncodeToString([]byte(outputsharetest.OutputHiddenText))
}

// saveOutputs saves a new latest state of the unit with the root outputs of
// the suite, and returns its ID as State.ID names it.
func (r *outputShareRig) saveOutputs(t *testing.T, unit outputshare.Placement) string {
	t.Helper()

	serial := r.nextSerial(unit.Unit)
	plain := fmt.Sprintf("%q", outputsharetest.OutputPlainValue)
	hidden := fmt.Sprintf("%q", outputsharetest.OutputHiddenText)

	switch unit.Unit.Tool() {
	case "opentofu":
		content := fmt.Sprintf(`{"serial":%d,"lineage":"lineage-1","outputs":{`+
			`%q:{"sensitive":false,"value":%s,"type":"string"},`+
			`%q:{"sensitive":true,"value":%s,"type":"string"},`+
			`%q:{"sensitive":true,"value":{"name":"web","inner":%s},"type":["object",{}]}}}`,
			serial, outputsharetest.OutputPlainName, plain, outputsharetest.OutputSecretName, hidden,
			outputsharetest.OutputNestedName, hidden)

		return r.saveWorkspaceState(t, unit.Unit, serial, content)
	case "pulumi":
		sealed := `{"` + stackSigKey + `":"` + stackSecretSig + `","ciphertext":"` + hiddenCiphertext() + `"}`
		outputs := fmt.Sprintf(`{%q:%s,%q:%s,%q:{"name":"web","inner":%s}}`,
			outputsharetest.OutputPlainName, plain, outputsharetest.OutputSecretName, sealed,
			outputsharetest.OutputNestedName, sealed)

		return r.saveStackDeployment(t, unit.Unit, fmt.Appendf(nil,
			`{"manifest":{"time":"2026-01-01T00:00:00Z","magic":"","version":""},`+
				`"secrets_providers":{"type":"service"},"resources":[`+
				`{"urn":"urn:pulumi:prod::web::pulumi:pulumi:Stack::web-prod","type":"pulumi:pulumi:Stack",`+
				`"outputs":%s}],"serial":%d}`, outputs, serial))
	default:
		t.Fatalf("unknown tool %q", unit.Unit.Tool())

		return ""
	}
}

// The members of a secret value in a Pulumi checkpoint: the signature key and
// the signature of a secret.
const (
	stackSigKey    = "4dabf18193072939515e22adb298388d"
	stackSecretSig = "1b47061264138c4ac30d75fd1eb44270"
)

// deleteUnit deletes the unit, also when it has a state.
func (r *outputShareRig) deleteUnit(t *testing.T, unit outputshare.Placement) {
	t.Helper()

	entry := audit.Entry{
		At: time.Time{}, Namespace: unit.Unit.Namespace(), Subject: "admin", IDPSubject: "", TokenID: "",
		Action: "unit:manage", Object: unit.Unit.String(), Version: "", ValuePath: "", ValueCount: nil,
		Changes: nil, SourceIP: "192.0.2.1", UserAgent: "test", Channel: audit.ChannelAPI, Kind: audit.KindChange,
		Outcome: audit.OutcomeSuccess, RequestID: "", ObjectID: "", PrevHash: "", Hash: "", Sequence: 0,
		FenceVersion: 0, Reason: "", PrincipalKind: "", JobID: "",
	}

	switch unit.Unit.Tool() {
	case "opentofu":
		name, err := workspace.ParseName(unit.Unit.UnitName())
		if err != nil {
			t.Fatalf("workspace name of %s: %v", unit.Unit, err)
		}

		ref := workspace.Ref{Namespace: unit.Unit.Namespace(), Name: name}
		if err := r.svc.Workspaces.Delete(t.Context(), ref, entry); err != nil {
			t.Fatalf("delete %s: %v", unit.Unit, err)
		}
	case "pulumi":
		if err := r.svc.Stacks.ForceDelete(t.Context(), r.stackRef(t, unit.Unit), entry); err != nil {
			t.Fatalf("delete %s: %v", unit.Unit, err)
		}
	default:
		t.Fatalf("unknown tool %q", unit.Unit.Tool())
	}
}

// stackRef returns the reference of the stack of the unit.
func (r *outputShareRig) stackRef(t *testing.T, unit identity.Object) stack.Ref {
	t.Helper()

	project, name, _ := strings.Cut(unit.UnitName(), "/")

	ref, err := stack.ParseRef(unit.Namespace().String(), project, name)
	if err != nil {
		t.Fatalf("stack ref of %s: %v", unit, err)
	}

	return ref
}

// TestOutputShareChecker runs the conformance suite of output sharing on the
// ports of the tools over each dialect. The cases of the checker for a read by
// a principal and of the States port run here.
func TestOutputShareChecker(t *testing.T) {
	t.Parallel()

	for _, dialect := range []string{"sqlite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			t.Parallel()

			outputsharetest.Run(t, func(t *testing.T) outputsharetest.Harness {
				t.Helper()

				return newOutputShareRig(t, dialect).harness()
			})
		})
	}
}

// readRace runs a hook before the n-th read of a state through the wrapped
// stores, once, so that a test can delete a unit between the lookup of the
// unit and that read.
type readRace struct {
	mu    sync.Mutex
	armed bool
	left  int
	fired bool
	hook  func()
}

// arm sets hook to run before the read-th read from now.
func (c *readRace) arm(read int, hook func()) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.armed, c.left, c.fired, c.hook = true, read, false, hook
}

// disarm stops the hook and reports whether it ran.
func (c *readRace) disarm() bool {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.armed = false

	return c.fired
}

// read counts one read of a state, and runs the hook when it is the armed one.
func (c *readRace) read() {
	c.mu.Lock()

	if !c.armed {
		c.mu.Unlock()

		return
	}

	c.left--
	if c.left > 0 {
		c.mu.Unlock()

		return
	}

	hook := c.hook
	c.armed, c.fired = false, true
	c.mu.Unlock()

	hook()
}

// raceUpdates counts the reads of an update.
type raceUpdates struct {
	stack.UpdateStore
	stack.JournalStore
	secret.ItemKeyIndex

	race *readRace
}

// GetUpdate implements stack.UpdateStore.
func (u raceUpdates) GetUpdate(ctx context.Context, ref stack.Ref, id stack.UpdateID) (stack.Update, error) {
	u.race.read()

	v, err := u.UpdateStore.GetUpdate(ctx, ref, id)
	if err != nil {
		return stack.Update{}, fmt.Errorf("read a state: %w", err)
	}

	return v, nil
}

// raceCheckpoints counts the reads of a checkpoint.
type raceCheckpoints struct {
	stack.CheckpointStore

	race *readRace
}

// LatestCheckpoint implements stack.CheckpointStore.
func (c raceCheckpoints) LatestCheckpoint(ctx context.Context, ref stack.Ref) (stack.Checkpoint, error) {
	c.race.read()

	v, err := c.CheckpointStore.LatestCheckpoint(ctx, ref)
	if err != nil {
		return stack.Checkpoint{}, fmt.Errorf("read a state: %w", err)
	}

	return v, nil
}

// CheckpointByVersion implements stack.CheckpointStore.
func (c raceCheckpoints) CheckpointByVersion(
	ctx context.Context, ref stack.Ref, version int64,
) (stack.Checkpoint, error) {
	c.race.read()

	v, err := c.CheckpointStore.CheckpointByVersion(ctx, ref, version)
	if err != nil {
		return stack.Checkpoint{}, fmt.Errorf("read a state: %w", err)
	}

	return v, nil
}

// raceStates counts the reads of the current state of a workspace.
type raceStates struct {
	workspace.StateStore

	race *readRace
}

// CurrentStateVersion implements workspace.StateStore.
func (s raceStates) CurrentStateVersion(ctx context.Context, id workspace.ID) (workspace.StateVersion, error) {
	s.race.read()

	v, err := s.StateStore.CurrentStateVersion(ctx, id)
	if err != nil {
		return workspace.StateVersion{}, fmt.Errorf("read a state: %w", err)
	}

	return v, nil
}

// deleteOnRead arms the stores so that the read-th read of a state first
// deletes the unit.
func (r *outputShareRig) deleteOnRead(t *testing.T, unit outputshare.Placement, read int) func() bool {
	t.Helper()

	r.race.arm(read, func() { r.deleteUnit(t, unit) })

	return r.race.disarm
}
