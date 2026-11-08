package dustpan_test

import (
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"opendustpan.org/opendustpan/internal/pgtest"
	"opendustpan.org/opendustpan/pkg/access"
	"opendustpan.org/opendustpan/pkg/accesstoken"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/backup/blobguard/blobguardtest"
	"opendustpan.org/opendustpan/pkg/blob/fs"
	"opendustpan.org/opendustpan/pkg/dustpan"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/inventory"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/principal"
	"opendustpan.org/opendustpan/pkg/secret"
	"opendustpan.org/opendustpan/pkg/secret/secrettest"
	"opendustpan.org/opendustpan/pkg/stack"
	"opendustpan.org/opendustpan/pkg/stack/stacktest"
	"opendustpan.org/opendustpan/pkg/store/postgres"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
	"opendustpan.org/opendustpan/pkg/workspace"
)

// leaseBenchURLEnv names the variable with the PostgreSQL URL for the
// PostgreSQL case of BenchmarkLeaseCheck. The benchmark skips without it.
const leaseBenchURLEnv = "DUSTPAN_TEST_POSTGRES_URL"

// leaseStores holds the store of each port the server and the benchmark use.
// A SQL adapter serves all of them from one database.
type leaseStores struct {
	namespaces  namespace.Store
	stacks      stack.Store
	updates     stack.UpdateStore
	checkpoints stack.CheckpointStore
	events      stack.EventStore
	keys        secret.KeyStore
	workspaces  workspace.Store
	state       workspace.StateStore
	inventory   inventory.Reader
	principals  principal.Store
	tokens      accesstoken.Store
}

// BenchmarkLeaseCheck measures one checkpoint post on an update lease, with
// the access token check of ADR 53 and without it. The check is the only
// difference: the token-started update runs the check on each request, and
// the bootstrap-started update has no token ID, so it skips the check. Each
// post goes through the whole server handler over a real store.
func BenchmarkLeaseCheck(b *testing.B) {
	b.Run("sqlite", func(b *testing.B) {
		st, err := sqlite.Open(b.Context(), filepath.Join(b.TempDir(), "dustpan.db"))
		if err != nil {
			b.Fatalf("open store: %v", err)
		}

		b.Cleanup(func() {
			if closeErr := st.Close(); closeErr != nil {
				b.Errorf("close store: %v", closeErr)
			}
		})

		benchLeaseCheck(b, leaseStores{
			namespaces: st, stacks: st, updates: st, checkpoints: st, events: st, keys: st,
			workspaces: st, state: st, inventory: st, principals: st, tokens: st,
		})
	})

	b.Run("postgres", func(b *testing.B) {
		base := os.Getenv(leaseBenchURLEnv)
		if base == "" {
			b.Skipf("set %s to run the PostgreSQL case", leaseBenchURLEnv)
		}

		st, err := postgres.Open(b.Context(), leaseBenchSchemaURL(b, base))
		if err != nil {
			b.Fatalf("open store: %v", err)
		}

		b.Cleanup(st.Close)

		benchLeaseCheck(b, leaseStores{
			namespaces: st, stacks: st, updates: st, checkpoints: st, events: st, keys: st,
			workspaces: st, state: st, inventory: st, principals: st, tokens: st,
		})
	})
}

// benchLease is one update that the benchmark posts checkpoints to.
type benchLease struct {
	path  string
	token string
}

// benchLeaseCheck builds a server over s, starts one update per mode, and runs
// the checkpoint post of each mode as a sub-benchmark.
func benchLeaseCheck(b *testing.B, s leaseStores) {
	b.Helper()

	ctx := b.Context()
	now := time.Now

	nsSvc, err := namespace.NewService(namespace.Config{Store: s.namespaces, Now: now, Installation: installationName})
	if err != nil {
		b.Fatalf("namespace service: %v", err)
	}

	if _, createErr := nsSvc.Create(ctx, "acme"); createErr != nil {
		b.Fatalf("create namespace: %v", createErr)
	}

	stacks, err := stack.NewService(
		stack.Config{Store: s.stacks, Namespaces: nsSvc, Now: now, State: nil, Retention: 0, ManualLocks: nil},
	)
	if err != nil {
		b.Fatalf("stack service: %v", err)
	}

	blobs := openBenchBlobs(b)

	updates, err := stack.NewUpdates(stack.UpdatesConfig{
		Sealer:      secrettest.NewMemorySealer(),
		ManualLocks: nil, BlobGuard: blobguardtest.None{},
		Updates: s.updates, Checkpoints: s.checkpoints, Events: s.events, Namespaces: nsSvc,
		Stacks: stacks, Blobs: blobs, Tokens: nil, Now: now, Logger: nil, MaxCheckpointBytes: 0, Journal: nil,
		Replayer: nil, Refusals: stacktest.NewRefusals(),
	})
	if err != nil {
		b.Fatalf("updates service: %v", err)
	}

	tokenID := benchTokenID(b, s, now)

	// The token check of each mode runs on its own stack: a stack allows
	// one running mutating update, and a checkpoint needs a mutating one.
	modes := []struct {
		name    string
		stack   string
		tokenID string
	}{
		{name: "with-check", stack: "prod", tokenID: tokenID},
		{name: "without-check", stack: "dev", tokenID: ""},
	}

	leases := make([]benchLease, len(modes))

	for i, m := range modes {
		createBenchStack(b, stacks, s.stacks, m.stack)
		leases[i] = startBenchLease(b, updates, m.stack, m.tokenID)
	}

	handler := leaseBenchHandler(b, s, blobs)

	const body = `{"version":3,"deployment":{"resources":[]}}`

	for i, m := range modes {
		b.Run(m.name, func(b *testing.B) {
			b.ReportAllocs()
			b.ResetTimer()

			for range b.N {
				req := httptest.NewRequestWithContext(ctx, http.MethodPatch, leases[i].path, strings.NewReader(body))
				req.Header.Set("Authorization", "update-token "+leases[i].token)

				rec := httptest.NewRecorder()
				handler.ServeHTTP(rec, req)

				if rec.Code != http.StatusOK {
					b.Fatalf("checkpoint answers %d, want %d (%s)", rec.Code, http.StatusOK, rec.Body.String())
				}
			}
		})
	}
}

// openBenchBlobs opens a blob store in a temporary directory that the
// benchmark closes.
func openBenchBlobs(b *testing.B) *fs.Store {
	b.Helper()

	blobs, err := fs.Open(b.TempDir())
	if err != nil {
		b.Fatalf("open blob store: %v", err)
	}

	b.Cleanup(func() {
		if closeErr := blobs.Close(); closeErr != nil {
			b.Errorf("close blob store: %v", closeErr)
		}
	})

	return blobs
}

// benchTokenID creates an access token for a new service principal and returns
// its ID.
func benchTokenID(b *testing.B, s leaseStores, now func() time.Time) string {
	b.Helper()

	ctx := b.Context()

	principals, err := principal.NewService(
		principal.Config{
			TempAdmins: nil,
			Store:      s.principals,
			Guard:      principal.AdminOnlyGuard{},
			Namespaces: nil,
			Tokens:     nil,
			GroupReach: nil,
			Now:        now,
			NewID:      nil,
		},
	)
	if err != nil {
		b.Fatalf("principal service: %v", err)
	}

	tokens, err := accesstoken.NewService(accesstoken.Config{
		TempAdmins: nil,
		Store:      s.tokens, Principals: s.principals, Freshness: nil, Authorizer: nil,
		Reach: access.ReachConfig{
			Grants: nil, Roles: nil, Callers: nil, Principals: nil, Collections: nil, Units: nil,
		},
		Logger: nil, Now: now,
		NewID: nil,
	})
	if err != nil {
		b.Fatalf("token service: %v", err)
	}

	owner, err := principals.CreatePrincipal(ctx, benchCaller(),
		principal.Input{Subject: "ci-bench", DisplayName: "", Kind: principal.KindService})
	if err != nil {
		b.Fatalf("create principal: %v", err)
	}

	tok, _, err := tokens.Create(ctx, benchAdmin(), benchCaller(), accesstoken.ScopeService, accesstoken.Input{
		ExpiresAt: now().Add(24 * time.Hour), PrincipalID: owner.ID, Name: "bench",
	})
	if err != nil {
		b.Fatalf("create token: %v", err)
	}

	return tok.ID
}

// startBenchLease creates and starts a mutating update on the stack named
// stackName, started by the access token tokenID, or by no token when tokenID
// is empty. It returns the checkpoint path and the lease token.
func startBenchLease(b *testing.B, updates *stack.Updates, stackName, tokenID string) benchLease {
	b.Helper()

	ctx := b.Context()

	ref, err := stack.ParseRef("acme", "web", stackName)
	if err != nil {
		b.Fatalf("parse ref: %v", err)
	}

	u, err := updates.Create(ctx, ref, stack.Plan{
		Kind: stack.KindUpdate, DryRun: false, CreatedBy: "", RestoredFrom: 0,
	})
	if err != nil {
		b.Fatalf("create update: %v", err)
	}

	started, err := updates.StartBy(ctx, ref, u.ID, stack.StartedBy{TokenID: tokenID, JobID: ""}, 0)
	if err != nil {
		b.Fatalf("start update: %v", err)
	}

	return benchLease{
		path:  "/api/stacks/acme/web/" + stackName + "/update/" + u.ID.String() + "/checkpoint",
		token: started.Token.Reveal(),
	}
}

// leaseBenchHandler builds the server handler over the stores of s. The
// server reads the access tokens through its own services, as in production.
func leaseBenchHandler(b *testing.B, s leaseStores, blobs *fs.Store) http.Handler {
	b.Helper()

	authn, err := identity.NewStaticToken("admin", adminToken)
	if err != nil {
		b.Fatalf("authenticator: %v", err)
	}

	wrapper, err := secret.NewBuiltinWrapper([]byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		b.Fatalf("key wrapper: %v", err)
	}

	srv, err := dustpan.NewServer(
		dustpan.WithInstallation(installationName),
		dustpan.WithNamespaceStore(s.namespaces),
		dustpan.WithStackStore(s.stacks),
		dustpan.WithUpdateStore(s.updates),
		dustpan.WithCheckpointStore(s.checkpoints),
		dustpan.WithEventStore(s.events),
		dustpan.WithSecretKeyStore(s.keys),
		dustpan.WithSecretWrapper(wrapper),
		dustpan.WithWorkspaceStore(s.workspaces),
		dustpan.WithStateStore(s.state),
		dustpan.WithBlobStore(blobs),
		dustpan.WithInventoryReader(s.inventory),
		dustpan.WithAuthenticator(authn),
		dustpan.WithAllowedOrigins(origin),
	)
	if err != nil {
		b.Fatalf("new server: %v", err)
	}

	return startedHandler(b, srv)
}

// benchAdmin is the bootstrap administrator that creates the benchmark token.
func benchAdmin() identity.Principal {
	return identity.Principal{
		TemporaryAdministrator: false, TemporaryExpiresAt: time.Time{},
		Subject:        "admin",
		DisplayName:    "admin",
		Admin:          true,
		TokenID:        "",
		TokenExpiresAt: time.Time{},
		Kind:           "",
		Credential:     "",
		JobID:          "",
	}
}

// benchCaller is the audit entry of the benchmark setup.
func benchCaller() audit.Entry {
	return audit.Entry{
		At: time.Time{}, Namespace: "", Subject: "admin", IDPSubject: "", TokenID: "",
		Action: string(identity.ActionServerManageTokens), Object: "", Version: "", ValuePath: "",
		ValueCount: nil, SourceIP: "192.0.2.1", UserAgent: "bench", Channel: audit.ChannelAPI, Kind: "",
		Outcome: "", RequestID: "", PrevHash: "", Hash: "", Sequence: 0, Changes: nil, ObjectID: "",
		FenceVersion:  0,
		Reason:        "",
		PrincipalKind: "",
		JobID:         "",
	}
}

// leaseBenchSchemaURL returns base with a schema of its own, so the benchmark
// does not share tables with other PostgreSQL tests.
func leaseBenchSchemaURL(b *testing.B, base string) string {
	b.Helper()

	suffix := make([]byte, 8)
	if _, err := rand.Read(suffix); err != nil {
		b.Fatalf("random schema name: %v", err)
	}

	name := "bench_" + hex.EncodeToString(suffix)

	conn, err := pgx.Connect(b.Context(), base)
	if err != nil {
		b.Fatalf("connect: %v", err)
	}

	defer func() {
		if closeErr := conn.Close(b.Context()); closeErr != nil {
			b.Errorf("close: %v", closeErr)
		}
	}()

	if _, execErr := conn.Exec(b.Context(), "CREATE SCHEMA "+pgx.Identifier{name}.Sanitize()); execErr != nil {
		b.Fatalf("create schema: %v", execErr)
	}

	pgtest.DropSchemaAtCleanup(b, base, name)

	u, err := url.Parse(base)
	if err != nil {
		b.Fatalf("parse %s: %v", leaseBenchURLEnv, err)
	}

	q := u.Query()
	q.Set("search_path", name)
	u.RawQuery = q.Encode()

	return u.String()
}

// createBenchStack makes the stack acme/web/stackName in a collection.
func createBenchStack(b *testing.B, stacks *stack.Service, store any, stackName string) {
	b.Helper()

	ref, err := stack.ParseRef("acme", "web", stackName)
	if err != nil {
		b.Fatalf("parse ref: %v", err)
	}

	if _, createErr := stacks.CreatePlaced(b.Context(), ref, nil, placeStack(b, store, "acme")); createErr != nil {
		b.Fatalf("create stack %s: %v", stackName, createErr)
	}
}
