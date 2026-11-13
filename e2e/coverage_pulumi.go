//go:build e2e

package e2e

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	pulumiapi "opendustpan.org/opendustpan/internal/api/pulumi"
	"opendustpan.org/opendustpan/internal/api/stategate"
	"opendustpan.org/opendustpan/internal/baseurl"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/blob"
	coreinventory "opendustpan.org/opendustpan/pkg/inventory"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/secret"
	"opendustpan.org/opendustpan/pkg/secret/secrettest"
	"opendustpan.org/opendustpan/pkg/stack"
	"opendustpan.org/opendustpan/pkg/stack/stacktest"
)

// coveragePulumiRoutes returns the Pulumi CLI adapter's route table.
func coveragePulumiRoutes(tb testing.TB, namespaces *namespace.Service) []RouteRef {
	tb.Helper()

	stacks, err := stack.NewService(stack.Config{
		Store: coverageStackStore{}, Namespaces: namespaces, Now: nil, State: nil, Retention: 0, ManualLocks: nil,
	})
	if err != nil {
		tb.Fatalf("stack service: %v", err)
	}

	updates, err := stack.NewUpdates(stack.UpdatesConfig{
		Sealer:      secrettest.NewMemorySealer(),
		ManualLocks: nil,
		// The updates service never deletes a blob here, so it has no guard.
		BlobGuard: nil,
		// The store never serves a request: only ActiveUpdate runs, and it
		// answers not found. The embedded stores stay nil on purpose.
		Updates:            coverageUpdateStore{UpdateStore: nil, CheckpointStore: nil, EventStore: nil},
		Tokens:             nil,
		Checkpoints:        coverageUpdateStore{UpdateStore: nil, CheckpointStore: nil, EventStore: nil},
		Events:             coverageUpdateStore{UpdateStore: nil, CheckpointStore: nil, EventStore: nil},
		Namespaces:         namespaces,
		Stacks:             coverageStackStore{},
		Blobs:              coverageBlobs{},
		Refusals:           stacktest.NewRefusals(),
		Now:                nil,
		Logger:             nil,
		MaxCheckpointBytes: 0,
		Journal:            nil,
		Replayer:           nil,
	})
	if err != nil {
		tb.Fatalf("updates service: %v", err)
	}

	secrets, err := secret.NewService(secret.Config{
		Keys: coverageKeyStore{}, Wrapper: nil, Namespaces: namespaces, Now: nil, MaxBatchBytes: 0,
	})
	if err != nil {
		tb.Fatalf("secret service: %v", err)
	}

	handler, err := pulumiapi.NewHandler(pulumiapi.Config{
		Placer:                  nil,
		SessionPolicy:           nil,
		DisablePulumiJournaling: false,
		Stacks:                  stacks,
		Updates:                 updates,
		Secrets:                 secrets,
		Namespaces:              namespaces,
		Authenticator:           coverageAuthenticator{},
		Authorizer:              coverageAuthorizer{},
		Sessions:                nil,
		AddressGate:             nil,
		Logger:                  slog.New(slog.DiscardHandler),
		BaseURL:                 baseurl.Resolver{},
		CheckpointDeltaCutoff:   0,
		// The gate is irrelevant here: no request is ever served.
		MaxBodyBytesInFlight: 0,
		Gate:                 nil,
		Audit:                coverageAuditStore{},
		OnAuditFailure:       nil,
		DenialLimiter:        nil,
		StateGuard:           stategate.Off("the test keeps no gates"),
	})
	if err != nil {
		tb.Fatalf("pulumi handler: %v", err)
	}

	return pulumiRefs(handler.Routes())
}

// coverageStackStore answers no stack. It exists only to build a Handler
// for its route table; no request is ever served.
type coverageStackStore struct{}

func (coverageStackStore) CreateStack(_ context.Context, _ stack.Stack, _ namespace.Fence) error {
	return nil
}

func (coverageStackStore) GetStack(_ context.Context, _ stack.Ref) (stack.Stack, error) {
	var none stack.Stack

	return none, stack.ErrNotFound
}

func (coverageStackStore) DeleteStack(
	_ context.Context,
	_ stack.Ref,
	_ namespace.Fence,
	_ stack.DeleteGuard,
	_ coreinventory.Deletion,
) error {
	return stack.ErrNotFound
}

func (coverageStackStore) ForceDeleteStack(
	_ context.Context,
	_ stack.Ref,
	_ namespace.Fence,
	_ coreinventory.Deletion,
) error {
	return stack.ErrNotFound
}

// RestoreStack implements stack.Store; this fake holds no deleted stack.
func (coverageStackStore) RestoreStack(
	_ context.Context, _ stack.ID, _ stack.Name, _ namespace.Fence, _ audit.Entry,
) (stack.Ref, error) {
	return stack.Ref{}, stack.ErrNotFound
}

// PurgeStack implements stack.Store; this fake holds no deleted stack.
func (coverageStackStore) PurgeStack(
	_ context.Context,
	_ stack.ID,
	_ namespace.Fence,
	_ time.Time,
	_ audit.Entry,
) error {
	return stack.ErrNotFound
}

func (coverageStackStore) ListStacks(_ context.Context, _ stack.Filter) ([]stack.Stack, error) {
	return nil, nil
}

// QueryStacks implements stack.Store over the lists of ListStacks.
func (coverageStackStore) QueryStacks(ctx context.Context, q stack.PageQuery) ([]stack.Stack, error) {
	return stack.ListPage(ctx, coverageStackStore{}.ListStacks, q)
}

func (coverageStackStore) RenameStack(
	_ context.Context, _ stack.Ref, _ stack.Name, _ namespace.Fence, _ audit.Entry,
) error {
	return stack.ErrNotFound
}

func (coverageStackStore) UpdateStackTags(
	_ context.Context, _ stack.Ref, _ map[string]string, _ time.Time, _ namespace.Fence,
) error {
	return nil
}

// coverageBlobs answers no object. It exists only to build an Updates
// service for the route tables; no request is ever served.
type coverageBlobs struct{}

func (coverageBlobs) Put(_ context.Context, _ blob.Key, _ io.Reader) error {
	return blob.ErrExists
}

func (coverageBlobs) Get(_ context.Context, _ blob.Key) (io.ReadCloser, error) {
	return nil, blob.ErrNotFound
}

func (coverageBlobs) Delete(_ context.Context, _ blob.Key) error {
	return nil
}

// ListPage implements blob.Store: it cuts the page that req selects from the
// objects of list.
func (x coverageBlobs) ListPage(ctx context.Context, req blob.PageRequest) (blob.Page, error) {
	if err := req.Validate(); err != nil {
		return blob.Page{}, err
	}

	objects, err := x.list(ctx, req.Prefix)
	if err != nil {
		return blob.Page{}, err
	}

	slices.SortFunc(objects, func(a, b blob.Object) int { return strings.Compare(a.Key.String(), b.Key.String()) })

	return blob.PageOf(objects, req), nil
}

func (coverageBlobs) Ping(_ context.Context) error {
	return nil
}

func (coverageBlobs) list(_ context.Context, _ string) ([]blob.Object, error) {
	return nil, nil
}

// coverageUpdateStore answers no update and no state. It exists only to
// build a Handler for its route table; no request is ever served.
type coverageUpdateStore struct {
	stack.UpdateStore
	stack.CheckpointStore
	stack.EventStore
}

func (coverageUpdateStore) ActiveUpdate(_ context.Context, _ stack.Ref) (stack.Update, error) {
	return stack.Update{}, stack.ErrUpdateNotFound
}

// coverageKeyStore answers no key. It exists only to build a Handler for
// its route table; no request is ever served.
type coverageKeyStore struct{}

func (coverageKeyStore) CreateKey(
	_ context.Context, _ secret.WrappedKey, _ namespace.Fence,
) (secret.WrappedKey, error) {
	var none secret.WrappedKey

	return none, secret.ErrKeyNotFound
}

func (coverageKeyStore) GetKey(_ context.Context, _ secret.Scope) (secret.WrappedKey, error) {
	var none secret.WrappedKey

	return none, secret.ErrKeyNotFound
}

func (coverageKeyStore) ListWrapperIDs(_ context.Context) ([]string, error) {
	return nil, nil
}

// routeRefs formats one adapter's table as method and path pairs. Each
// adapter declares its own Route type with Method and Path fields, so each
// table has its own small mapper.
func pulumiRefs(routes []pulumiapi.Route) []RouteRef {
	out := make([]RouteRef, 0, len(routes))

	for _, route := range routes {
		out = append(out, RouteRef{Method: route.Method, Path: route.Path})
	}

	return out
}

// journalAllowEntries returns the allowlist row for the journal entry route.
// The release build answers journal version 0, so no CLI reaches the route
// there. The recording build (journalrecording tag) reaches it through
// make e2e-journal.
func journalAllowEntries() []AllowEntry {
	return []AllowEntry{
		{
			Method: http.MethodPatch, Path: "/api/stacks/{namespace}/{project}/{stack}/update/{update}/journalentries",
			CoveringTest: "TestPulumiJournalingOn (make e2e-journal, journalrecording build)",
			Reason: "The release build answers journal version 0, so no CLI sends journal batches. " +
				"The recording build answers version 1, and its test proves the route.",
		},
	}
}

// stateCoveringTest names the test that proves the masked state routes
// instead of a CLI: no CLI version in the matrix calls them.
const stateCoveringTest = "TestCLIs (pulumiMaskedStateScenario)"

// stateAllowEntries returns the console state routes that no CLI calls. The
// harness steps of stateCoveringTest drive them directly after two ups.
func stateAllowEntries() []AllowEntry {
	return []AllowEntry{
		{
			Method: http.MethodGet, Path: "/api/stacks/{namespace}/{project}/{stack}/state/{version}/resources",
			CoveringTest: stateCoveringTest,
			Reason: "Console route (ADR 41): the CLI never lists masked resources. " +
				"The harness step reads the list after two ups and checks masking and changed markers.",
		},
		{
			Method: http.MethodGet, Path: "/api/stacks/{namespace}/{project}/{stack}/state/{version}/resources/{urn}",
			CoveringTest: stateCoveringTest,
			Reason: "Console route (ADR 41): the CLI never reads one masked resource. " +
				"The harness step reads it after two ups and checks masking.",
		},
		{
			Method: http.MethodGet, Path: "/api/stacks/{namespace}/{project}/{stack}/state/{version}/outputs",
			CoveringTest: stateCoveringTest,
			Reason: "Console route (ADR 41): the CLI never reads masked outputs. " +
				"The harness step reads them after two ups and checks masking.",
		},
		{
			Method: http.MethodGet, Path: "/api/stacks/{namespace}/{project}/{stack}/state-at/{at}/resources",
			CoveringTest: "TestCLIs (checkMaskedStateAt)",
			Reason: "Console route: the CLI never reads the state at a time. " +
				"The harness step reads the list between two ups and before the first, " +
				"checks the 404 text, and reads it while a held update runs.",
		},
		{
			Method: http.MethodPost, Path: "/api/stacks/{namespace}/{project}/{stack}/state/{version}/reveal",
			CoveringTest: "TestCLIs (checkMaskedReveal)",
			Reason: "Console route (ADR 41): the CLI never reveals a secret value. " +
				"The harness step reveals one masked output after two ups, checks the value, " +
				"no-store and the reveal audit entry, and checks that a path outside the " +
				"masked set answers 404.",
		},
		{
			Method: http.MethodGet, Path: "/api/stacks/{namespace}/{project}/{stack}/state/diff",
			CoveringTest: "TestCLIs (pulumiDiffScenario)",
			Reason: "Console route (ADR 41): the CLI never diffs state. " +
				"The harness step diffs two ups with an added, a removed and a changed " +
				"resource and a changed secret, and checks that no secret value appears.",
		},
		{
			Method: http.MethodGet, Path: "/api/v2/state-versions/{state_version_id}/resources",
			CoveringTest: "TestCLIs (tofuMaskedStateScenario)",
			Reason: "Console route (ADR 41): the CLI never lists masked resources. " +
				"The harness step reads the list after two applies and checks masking and changed markers.",
		},
		{
			Method: http.MethodGet, Path: "/api/v2/state-versions/{state_version_id}/resources/{address}",
			CoveringTest: "TestCLIs (tofuMaskedStateScenario)",
			Reason: "Console route (ADR 41): the CLI never reads one masked resource. " +
				"The harness step reads it after two applies and checks masking.",
		},
		{
			Method: http.MethodPost, Path: "/api/v2/state-versions/{state_version_id}/actions/reveal",
			CoveringTest: "TestCLIs (maskedRevealCheck)",
			Reason: "Console route (ADR 41): the CLI never reveals a secret value. " +
				"The harness step reveals the sensitive input after two applies, checks the value, " +
				"no-store and the reveal audit entry, and checks that a path outside the " +
				"masked set answers 404.",
		},
		{
			Method: http.MethodGet, Path: "/api/v2/state-versions/{state_version_id}/diff",
			CoveringTest: "TestCLIs (tofuDiffScenario)",
			Reason: "Console route (ADR 41): the CLI never diffs state. " +
				"The harness step diffs two applies with an added, a removed and a changed " +
				"resource and a changed secret, and checks that no secret value appears.",
		},
	}
}
