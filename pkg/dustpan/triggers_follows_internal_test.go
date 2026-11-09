package dustpan

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/placementtest"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/blob"
	blobfs "opendustpan.org/opendustpan/pkg/blob/fs"
	"opendustpan.org/opendustpan/pkg/graph"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/principal"
	"opendustpan.org/opendustpan/pkg/runner"
	"opendustpan.org/opendustpan/pkg/secret"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
	"opendustpan.org/opendustpan/pkg/store/storetest"
	"opendustpan.org/opendustpan/pkg/trigger"
	"opendustpan.org/opendustpan/pkg/unitsettings"
	"opendustpan.org/opendustpan/pkg/workspace"
)

// The constants of the follows trigger test: the token, the namespace, the
// time of the injected clock, the state of the source workspace and its MD5 as
// the TFE-compatible API names it.
const (
	followsAdminToken   = "follows-token-0123456789abcdef0123456"
	followsNamespace    = namespace.Name("acme")
	followsInstallation = namespace.InstallationName("eu-west")
	followsTriggerID    = trigger.ID("trg-follows")
	followsSaver        = "owen"
	followsSaverID      = "prn-owen"
	followsState        = `{"version":4,"serial":1,"lineage":"lin-vpc","outputs":{}}`
	followsStateMD5     = "63e15e62d49cefe797ddfc5a74fb2ed3"
)

// followsAuthorizer allows every action of every principal. The test checks
// the wiring of the trigger, not the rules of the access model.
type followsAuthorizer struct{}

// Authorize implements identity.Authorizer.
func (followsAuthorizer) Authorize(
	context.Context, identity.Principal, identity.Action, identity.Object,
) error {
	return nil
}

// ListObjects implements identity.Authorizer.
func (followsAuthorizer) ListObjects(
	context.Context, identity.Principal, identity.Action,
) (identity.Scope, error) {
	return identity.Every(), nil
}

// followsUnitPair holds the graph units of the two workspaces of the test.
type followsUnitPair struct {
	source   graph.Unit
	follower graph.Unit
}

// followsDispatch holds the dispatcher that the trigger wiring builds.
type followsDispatch struct {
	dispatcher trigger.Dispatcher
}

// followsStateAnswer is the part of a state version answer that the test
// reads. The attributes keep the wire names of the TFE-compatible API.
type followsStateAnswer struct {
	Data map[string]json.RawMessage `json:"data"`
}

// followsWorld is a real server over one SQLite store, with the job store and
// the real dispatcher of the trigger wiring, and the two workspaces of the
// test.
type followsWorld struct {
	server     *httptest.Server
	jobs       sqlite.JobStore
	dispatcher trigger.Dispatcher
	source     graph.Unit
	follower   graph.Unit
	sourceID   workspace.ID
}

// newFollowsWorld makes the namespace, the two OpenTofu workspaces, a runner
// pool for jobs, the settings of the follower and a follows trigger from the
// follower to the source. It answers the running server and the dispatcher
// that the server builds.
func newFollowsWorld(t *testing.T) followsWorld {
	t.Helper()

	st, blobs := openFollowsStores(t)
	at := followsTime()
	clock := func() time.Time { return at }

	opts := followsOptions(st, blobs, followsWrapper(t), followsAuthenticator(t), clock)

	cfg := defaultSettings()
	for _, opt := range opts {
		opt(cfg)
	}

	svc := newFollowsServices(t, cfg)
	ns := createFollowsNamespace(t, svc)
	fence := ns.Fence()

	seedFollowsSaver(t, st, at)
	seedFollowsPool(t, st, fence, at)

	source := createFollowsWorkspace(t, svc, st, fence, "vpc")
	follower := createFollowsWorkspace(t, svc, st, fence, "network")
	units := followsUnits(source, follower)

	seedFollowsSettings(t, st, follower, fence)
	putFollowsTrigger(t, st, fence, units.source, units.follower)

	dispatch := buildFollowsDispatcher(t, svc, cfg)
	server := startFollowsServer(t, opts)

	return followsWorld{
		server: server, jobs: st.Jobs(), dispatcher: dispatch.dispatcher,
		source: units.source, follower: units.follower, sourceID: source.ID,
	}
}

// followsTime returns the fixed time of the injected clock of the test.
func followsTime() time.Time {
	return time.Date(2026, time.November, 9, 12, 0, 0, 0, time.UTC)
}

// openFollowsStores opens the SQLite store and the file blob store of the
// test, and closes both at its end.
func openFollowsStores(t *testing.T) (*sqlite.Store, *blobfs.Store) {
	t.Helper()

	st, err := sqlite.Open(t.Context(), filepath.Join(t.TempDir(), "dustpan.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	t.Cleanup(func() {
		if closeErr := st.Close(); closeErr != nil {
			t.Errorf("close store: %v", closeErr)
		}
	})

	blobs, err := blobfs.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open blob store: %v", err)
	}

	t.Cleanup(func() {
		if closeErr := blobs.Close(); closeErr != nil {
			t.Errorf("close blob store: %v", closeErr)
		}
	})

	return st, blobs
}

// followsWrapper returns the key wrapper of the state sealing of the test.
func followsWrapper(t *testing.T) *secret.BuiltinWrapper {
	t.Helper()

	wrapper, err := secret.NewBuiltinWrapper([]byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatalf("key wrapper: %v", err)
	}

	return wrapper
}

// followsAuthenticator returns the authenticator that signs in the
// administrator of the test.
func followsAuthenticator(t *testing.T) *identity.StaticToken {
	t.Helper()

	authn, err := identity.NewStaticToken("admin", followsAdminToken)
	if err != nil {
		t.Fatalf("authenticator: %v", err)
	}

	return authn
}

// followsOptions returns the options that build the server and the services
// of the test.
func followsOptions(
	st *sqlite.Store, blobs blob.Store, wrapper secret.Wrapper, authn identity.Authenticator,
	clock func() time.Time,
) []Option {
	return []Option{
		WithInstallation(followsInstallation),
		WithNamespaceStore(st),
		WithStackStore(st),
		WithUpdateStore(st),
		WithCheckpointStore(st),
		WithEventStore(st),
		WithSecretKeyStore(st),
		WithSecretWrapper(wrapper),
		WithWorkspaceStore(st),
		WithStateStore(st),
		WithBlobStore(blobs),
		WithInventoryReader(st),
		WithJobStore(st.Jobs()),
		WithTriggerStore(st.Triggers()),
		WithRunners(st.Runners()),
		WithUnitSettingsStore(st.UnitSettings()),
		WithAuthenticator(authn),
		WithAuthorizer(followsAuthorizer{}),
		WithClock(clock),
	}
}

// newFollowsServices builds the domain services of the test, as the server
// does, with the collection and gate services that a job start reads.
func newFollowsServices(t *testing.T, cfg *settings) services {
	t.Helper()

	svc, err := newServices(cfg, nil, cfg.authorizer)
	if err != nil {
		t.Fatalf("services: %v", err)
	}

	collections, gates, err := newCollectionAndGateServices(cfg, svc.namespaces)
	if err != nil || gates.svc == nil {
		t.Fatalf("gate services: %+v, %v", gates, err)
	}

	svc.collections = collections
	svc.gates = gates.svc
	svc.stateWrite = gates

	return svc
}

// createFollowsNamespace creates the namespace of the test.
func createFollowsNamespace(t *testing.T, svc services) namespace.Namespace {
	t.Helper()

	ns, err := svc.namespaces.Create(t.Context(), followsNamespace)
	if err != nil {
		t.Fatalf("create namespace: %v", err)
	}

	return ns
}

// followsUnits returns the graph units of the two workspaces.
func followsUnits(source, follower workspace.Workspace) followsUnitPair {
	return followsUnitPair{
		source:   graph.Unit{Namespace: followsNamespace, Tool: "opentofu", ID: string(source.ID)},
		follower: graph.Unit{Namespace: followsNamespace, Tool: "opentofu", ID: string(follower.ID)},
	}
}

// putFollowsTrigger stores the follows trigger from the follower to the
// source.
func putFollowsTrigger(
	t *testing.T, st *sqlite.Store, fence namespace.Fence, source, follower graph.Unit,
) {
	t.Helper()

	tr := trigger.Trigger{
		ID: followsTriggerID, Unit: follower, Kind: trigger.KindFollows,
		Frequency: trigger.Frequency{Preset: "", Cron: ""}, Source: source, Enabled: true,
		LastSaver: followsSaver, LastSaverID: followsSaverID,
	}
	if err := st.Triggers().Put(t.Context(), tr, fence, placementtest.Entry(followsNamespace)); err != nil {
		t.Fatalf("put follows trigger: %v", err)
	}
}

// buildFollowsDispatcher builds the trigger manager of the server and answers
// the dispatcher that the manager builds.
func buildFollowsDispatcher(t *testing.T, svc services, cfg *settings) followsDispatch {
	t.Helper()

	ports, ok, err := triggerPortsOf(svc, cfg)
	if err != nil || !ok {
		t.Fatalf("trigger ports: ok = %t, err = %v", ok, err)
	}

	jobs, err := newJobService(svc, cfg)
	if err != nil || jobs == nil {
		t.Fatalf("job service: %v, %v", jobs, err)
	}

	var out followsDispatch

	capture := func(cfg trigger.DispatcherConfig) (trigger.Dispatcher, error) {
		built, buildErr := trigger.NewDispatcher(cfg)
		out.dispatcher = built

		return built, buildErr
	}

	if _, err = newTriggerManager(svc, cfg, &ports, jobs, capture); err != nil {
		t.Fatalf("trigger manager: %v", err)
	}

	return out
}

// startFollowsServer starts the server of the test.
func startFollowsServer(t *testing.T, opts []Option) *httptest.Server {
	t.Helper()

	srv, err := NewServer(opts...)
	if err != nil {
		t.Fatalf("new server: %v", err)
	}

	if err = srv.Start(t.Context()); err != nil {
		t.Fatalf("start server: %v", err)
	}

	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	return ts
}

// seedFollowsSaver stores the principal record of the last saver of the
// trigger. The administrator authorizer allows its actions, and the record
// lets the dispatcher find the saver by its subject.
func seedFollowsSaver(t *testing.T, st *sqlite.Store, at time.Time) {
	t.Helper()

	p := principal.Principal{
		CreatedAt: at, UpdatedAt: at, ID: followsSaverID, Subject: followsSaver, DisplayName: followsSaver,
		Kind: principal.KindPerson, Source: principal.SourceLocal, Namespace: "", GroupCount: 0, BreakGlass: false,
		DisabledAt: time.Time{},
	}
	if err := st.CreatePrincipal(t.Context(), p, storetest.ServerChange(at, "principal:"+followsSaverID)); err != nil {
		t.Fatalf("create principal %s: %v", followsSaver, err)
	}
}

// seedFollowsPool stores the one runner pool for jobs of the namespace, which
// the planner of a plan without a pool takes.
func seedFollowsPool(t *testing.T, st *sqlite.Store, fence namespace.Fence, at time.Time) {
	t.Helper()

	p := runner.Pool{
		ID: "", Namespace: followsNamespace, Name: "ci", Purpose: runner.PurposeJobs, Lease: 0, Concurrency: 1,
		Isolation: nil, GitCredentials: runner.GitCredentials{Mode: "", Helper: ""}, CreatedAt: at, UpdatedAt: at,
	}
	_, err := st.Runners().CreatePool(
		t.Context(), p, fence, []audit.Entry{placementtest.Entry(followsNamespace)},
	)
	if err != nil {
		t.Fatalf("create runner pool: %v", err)
	}
}

// createFollowsWorkspace creates a workspace of the namespace in the
// collection of the test placement.
func createFollowsWorkspace(
	t *testing.T, svc services, st *sqlite.Store, fence namespace.Fence, name string,
) workspace.Workspace {
	t.Helper()

	wsName, err := workspace.ParseName(name)
	if err != nil {
		t.Fatalf("parse workspace name %q: %v", name, err)
	}

	got, err := svc.workspaces.Create(t.Context(), workspace.Ref{Namespace: followsNamespace, Name: wsName},
		placementtest.Workspace(t, st, fence))
	if err != nil {
		t.Fatalf("create workspace %q: %v", name, err)
	}

	return got
}

// seedFollowsSettings stores the code location of the follower. A plan of the
// follower reads it, and the only pool for jobs of the namespace.
func seedFollowsSettings(t *testing.T, st *sqlite.Store, follower workspace.Workspace, fence namespace.Fence) {
	t.Helper()

	obj, err := identity.UnitObject("opentofu", followsNamespace, follower.Ref.Name.String())
	if err != nil {
		t.Fatalf("unit object: %v", err)
	}

	obj = obj.WithUnitID(string(follower.ID))

	s := &unitsettings.Settings{
		Unit: obj, Executor: "", Pool: "",
		CodeLocation: unitsettings.CodeLocation{
			Connection: "", Repository: "acme/network", Ref: "main", Directory: "",
		},
		WriteBack:      unitsettings.WriteBack{Delivery: "", Files: nil},
		ReviewRequests: unitsettings.ReviewRequests{Enabled: false},
		Steps:          nil, Version: 0, UpdatedAt: time.Time{},
	}
	_, err = st.UnitSettings().Put(
		t.Context(), s, 0, fence, placementtest.Entry(followsNamespace),
	)
	if err != nil {
		t.Fatalf("put unit settings: %v", err)
	}
}

// call sends one TFE-compatible request with the administrator token and
// answers its status and body.
func (w followsWorld) call(t *testing.T, method, path, body string) (int, string) {
	t.Helper()

	req, err := http.NewRequestWithContext(t.Context(), method, w.server.URL+path, strings.NewReader(body))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}

	req.Header.Set("Authorization", "Bearer "+followsAdminToken)

	if body != "" {
		req.Header.Set("Content-Type", "application/vnd.api+json")
	}

	resp, err := w.server.Client().Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}

	data, err := io.ReadAll(resp.Body)
	if closeErr := resp.Body.Close(); closeErr != nil {
		t.Fatalf("close body: %v", closeErr)
	}

	if err != nil {
		t.Fatalf("read body: %v", err)
	}

	return resp.StatusCode, string(data)
}

// lock takes the state lock of the source workspace, as an apply does.
func (w followsWorld) lock(t *testing.T) {
	t.Helper()

	code, body := w.call(t, http.MethodPost, "/api/v2/workspaces/"+w.sourceID.String()+"/actions/lock",
		`{"data":{"attributes":{"reason":"apply"}}}`)
	if code != http.StatusOK {
		t.Fatalf("lock = %d %s, want 200", code, body)
	}
}

// unlock releases the state lock of the source workspace, as an apply does.
func (w followsWorld) unlock(t *testing.T) {
	t.Helper()

	code, body := w.call(t, http.MethodPost, "/api/v2/workspaces/"+w.sourceID.String()+"/actions/unlock", "")
	if code != http.StatusOK {
		t.Fatalf("unlock = %d %s, want 200", code, body)
	}
}

// writeState declares and uploads one state version through the
// TFE-compatible API, and answers the ID of the version.
func (w followsWorld) writeState(t *testing.T) string {
	t.Helper()

	body := `{"data":{"type":"state-versions","attributes":{"serial":1,"lineage":"lin-vpc","md5":"` +
		followsStateMD5 + `"}}}`

	code, answer := w.call(t, http.MethodPost, "/api/v2/workspaces/"+w.sourceID.String()+"/state-versions", body)
	if code != http.StatusCreated {
		t.Fatalf("declare state = %d %s, want 201", code, answer)
	}

	var doc followsStateAnswer
	if err := json.Unmarshal([]byte(answer), &doc); err != nil {
		t.Fatalf("decode state version %s: %v", answer, err)
	}

	var versionID string
	if err := json.Unmarshal(doc.Data["id"], &versionID); err != nil {
		t.Fatalf("decode state version ID %s: %v", answer, err)
	}

	var attributes map[string]string
	if err := json.Unmarshal(doc.Data["attributes"], &attributes); err != nil {
		t.Fatalf("decode state version attributes %s: %v", answer, err)
	}

	uploadURL := attributes["hosted-state-upload-url"]
	if versionID == "" || uploadURL == "" {
		t.Fatalf("state version answer %s holds no ID or upload URL", answer)
	}

	req, err := http.NewRequestWithContext(t.Context(), http.MethodPut, uploadURL,
		strings.NewReader(followsState))
	if err != nil {
		t.Fatalf("new upload request: %v", err)
	}

	resp, err := w.server.Client().Do(req)
	if err != nil {
		t.Fatalf("upload state: %v", err)
	}

	if closeErr := resp.Body.Close(); closeErr != nil {
		t.Fatalf("close upload body: %v", closeErr)
	}

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("upload state = %d, want 200", resp.StatusCode)
	}

	return versionID
}

// finalVersion returns the last final state version of the unit, and fails the
// test when the store holds none.
func (w followsWorld) finalVersion(t *testing.T, unitID string) job.FinalVersion {
	t.Helper()

	got, ok, err := w.jobs.FinalVersion(t.Context(), followsNamespace, unitID)
	if err != nil {
		t.Fatalf("final version of %s: %v", unitID, err)
	}

	if !ok {
		t.Fatalf("unit %s has no final state version", unitID)
	}

	return got
}

// startsPlan sends the event of an apply of the source to the dispatcher and
// returns the job of the follower that it started.
func (w followsWorld) startsPlan(t *testing.T, version string) job.ID {
	t.Helper()

	out, err := w.dispatcher.Upstream(t.Context(), trigger.Applied{Unit: w.source, Version: version})
	if err != nil {
		t.Fatalf("upstream of %s: %v", version, err)
	}

	if len(out) != 1 {
		t.Fatalf("upstream of %s answered %d outcomes, want 1: %+v", version, len(out), out)
	}

	if out[0].Err != nil || out[0].Job == "" || out[0].Trigger != followsTriggerID {
		t.Fatalf("upstream of %s = %+v, want a plan of %s", version, out[0], followsTriggerID)
	}

	return out[0].Job
}

// TestFollowsTriggerStartsPlanAfterAFinalStateWrite proves, over a real
// server, that the final version that the dispatcher reads is the version of
// the last final state write: a final state write of the source through the
// TFE-compatible API, given to the real dispatcher, starts a plan of the
// follower, and a lock and an unlock with no write make no new final version
// and start no new plan. The test gives the version to Dispatcher.Upstream
// itself: the server does not call it after a state write.
func TestFollowsTriggerStartsPlanAfterAFinalStateWrite(t *testing.T) {
	t.Parallel()

	w := newFollowsWorld(t)

	// An apply locks the state, writes a version and releases the lock.
	w.lock(t)
	written := w.writeState(t)
	w.unlock(t)

	final := w.finalVersion(t, w.source.ID)
	if final.VersionID != written {
		t.Fatalf("final version = %q, want the written version %q", final.VersionID, written)
	}

	first := w.startsPlan(t, final.VersionID)

	j, err := w.jobs.Get(t.Context(), first)
	if err != nil {
		t.Fatalf("read job %s: %v", first, err)
	}

	if j.Spec.Operation != job.OperationPlan || j.Spec.Unit.UnitID() != w.follower.ID {
		t.Fatalf("job %s = %s of %s, want a plan of %s", first, j.Spec.Operation,
			j.Spec.Unit.UnitID(), w.follower.ID)
	}

	if j.Trigger == nil || j.Trigger.ID != string(followsTriggerID) {
		t.Fatalf("job %s trigger = %+v, want %s", first, j.Trigger, followsTriggerID)
	}

	// A plan locks and unlocks the state and writes nothing. It publishes no
	// new final version, so it starts no new plan.
	w.lock(t)
	w.unlock(t)

	after := w.finalVersion(t, w.source.ID)
	if after != final {
		t.Fatalf("final version after a lock and an unlock with no write = %+v, want %+v", after, final)
	}

	again := w.startsPlan(t, after.VersionID)
	if again != first {
		t.Fatalf("a lock and an unlock with no write started job %s, want no new plan after %s", again, first)
	}
}
