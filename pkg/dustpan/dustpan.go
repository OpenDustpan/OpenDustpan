// Package dustpan is the OpenDustpan server as a library.
//
// The opendustpan binary is a thin main around this package. To swap a
// built-in adapter (persistence, identity, storage) for your own, write your
// own main and pass your implementations as options, in the style of
// Temporal's server SDK:
//
//	srv, err := dustpan.NewServer(
//		dustpan.WithInstallation("eu-west"),
//		dustpan.WithNamespaceStore(myStore),
//		dustpan.WithStackStore(myStore),
//		dustpan.WithUpdateStore(myStore),
//		dustpan.WithCheckpointStore(myStore),
//		dustpan.WithEventStore(myStore),
//		dustpan.WithSecretKeyStore(myStore),
//		dustpan.WithSecretWrapper(myWrapper),
//		dustpan.WithWorkspaceStore(myStore),
//		dustpan.WithStateStore(myStore),
//		dustpan.WithBlobStore(myBlobs),
//		dustpan.WithBackupTarget(myBackupTarget, "dir:/var/backups"),
//		dustpan.WithInventoryReader(myStore),
//		dustpan.WithAuthenticator(myAuthenticator),
//	)
//
// WithSecretWrapper is required: NewServer answers ErrMissingOption without
// it, because the server seals OpenTofu state and Pulumi secret values with
// data keys that the key-encryption key wraps (ADR 41).
//
// Custom stores should pass the store/storetest conformance suite. The SDK is
// pre-1.0: ports may still change between minor versions.
package dustpan

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"opendustpan.org/opendustpan/internal/api/apidesc"
	auditapi "opendustpan.org/opendustpan/internal/api/audit"
	inventoryapi "opendustpan.org/opendustpan/internal/api/inventory"
	contextapi "opendustpan.org/opendustpan/internal/api/objectcontext"
	"opendustpan.org/opendustpan/internal/api/pulumi"
	"opendustpan.org/opendustpan/internal/api/refusal"
	serverapi "opendustpan.org/opendustpan/internal/api/server"
	"opendustpan.org/opendustpan/internal/api/tfe"
	"opendustpan.org/opendustpan/internal/api/tofuhttp"
	"opendustpan.org/opendustpan/internal/authgate"
	"opendustpan.org/opendustpan/internal/baseurl"
	"opendustpan.org/opendustpan/internal/bodygate"
	"opendustpan.org/opendustpan/internal/clientip"
	"opendustpan.org/opendustpan/internal/denylimit"
	"opendustpan.org/opendustpan/internal/devsignin"
	"opendustpan.org/opendustpan/internal/errcode"
	"opendustpan.org/opendustpan/internal/forgelinksvc"
	"opendustpan.org/opendustpan/internal/issuersvc"
	"opendustpan.org/opendustpan/internal/metrics"
	"opendustpan.org/opendustpan/internal/pulumijournal"
	"opendustpan.org/opendustpan/internal/requestid"
	"opendustpan.org/opendustpan/internal/scopeaudit"
	"opendustpan.org/opendustpan/internal/sessionauth"
	"opendustpan.org/opendustpan/internal/storecheck"
	"opendustpan.org/opendustpan/pkg/access"
	"opendustpan.org/opendustpan/pkg/accesstoken"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/backup"
	"opendustpan.org/opendustpan/pkg/backup/blobguard"
	"opendustpan.org/opendustpan/pkg/blob"
	blobcache "opendustpan.org/opendustpan/pkg/blob/cache"
	"opendustpan.org/opendustpan/pkg/collection"
	"opendustpan.org/opendustpan/pkg/drift"
	"opendustpan.org/opendustpan/pkg/egress"
	"opendustpan.org/opendustpan/pkg/forgelink"
	"opendustpan.org/opendustpan/pkg/idempotency"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/installation"
	"opendustpan.org/opendustpan/pkg/inventory"
	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/maintenance"
	"opendustpan.org/opendustpan/pkg/manuallock"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/notify"
	"opendustpan.org/opendustpan/pkg/objectcontext"
	"opendustpan.org/opendustpan/pkg/principal"
	"opendustpan.org/opendustpan/pkg/profile"
	"opendustpan.org/opendustpan/pkg/savedview"
	"opendustpan.org/opendustpan/pkg/secret"
	"opendustpan.org/opendustpan/pkg/session"
	"opendustpan.org/opendustpan/pkg/source"
	"opendustpan.org/opendustpan/pkg/stack"
	"opendustpan.org/opendustpan/pkg/tempadmin"
	"opendustpan.org/opendustpan/pkg/trigger"
	"opendustpan.org/opendustpan/pkg/unitsettings"
	"opendustpan.org/opendustpan/pkg/workspace"
)

// ErrMissingOption reports that a required option was not provided.
var ErrMissingOption = errors.New("missing required option")

// ErrInvalidOption reports an option with a value that the server refuses.
var ErrInvalidOption = errors.New("invalid option")

// MinSoftDeleteRetention is the shortest soft-delete retention that
// WithSoftDeleteRetention accepts (ADR 47, decision 2). There is no maximum.
const MinSoftDeleteRetention = time.Hour

// The secret service seals OpenTofu state blobs and outputs documents behind
// the workspace Sealer port.
var _ workspace.Sealer = (*secret.Service)(nil)

// errMaintenanceNeedsStore reports a namespace store that cannot keep
// the installation state. Every SQL adapter serves it from the same
// database connection; a custom store must serve it too, so every
// instance gates the tool APIs on the same value.
var errMaintenanceNeedsStore = errors.New("namespace store must keep the installation state")

const (
	defaultListenAddr = ":8080"
	readHeaderTimeout = 10 * time.Second
	idleTimeout       = 120 * time.Second
	shutdownTimeout   = 30 * time.Second
	// DefaultDrainPeriod is the drain that the binary sets with
	// WithDrainPeriod: the time between the stop signal and the start of the
	// shutdown. In it, GET /health/ready answers 503 and the server serves
	// all other requests, so a load balancer takes the instance out of its
	// pool before the listener closes. It is fixed: the Helm chart waits in
	// preStop first, and the drain and the shutdown together stay inside
	// maxStopTime.
	DefaultDrainPeriod = 10 * time.Second
	// maxStopTime is the limit of the drain and the shutdown together. The
	// pod grace period of the Helm chart is 60 s, and the preStop wait takes
	// 15 s of it.
	maxStopTime = 45 * time.Second
	// maxHeaderBytes caps the request headers that the server reads. net/http
	// answers 431 past it, so a client cannot grow an audit entry past it
	// through headers.
	maxHeaderBytes = 65536
)

// settings collects every option before NewServer validates them.
type settings struct {
	// denialLimiter is the one limiter of the audit entries of refused requests.
	// Every adapter gets it, so its bound on keys holds for the whole server.
	denialLimiter *denylimit.Limiter
	// devRefusal holds the one recorder of the refused dev sign-ins.
	devRefusal *devRefusalState
	// stackRefusals records the refusals of the stack blob scope. newServices
	// builds it once.
	stackRefusals scopeaudit.Recorder
	// blobGuard tells the blob deleters whether a backup or a restore runs.
	// newUpdateService attaches it.
	blobGuard       blobguard.Watch
	namespaceStore  namespace.Store
	stackStore      stack.Store
	updateStore     stack.UpdateStore
	checkpointStore stack.CheckpointStore
	eventStore      stack.EventStore
	secretKeys      secret.KeyStore
	secretWrapper   secret.Wrapper
	streamStop      *streamStop
	workspaceStore  workspace.Store
	stateStore      workspace.StateStore
	blobStore       blob.Store
	blobCacheBytes  int64
	// workspaceBlobs is the blob store of the workspace service: the blob
	// store behind the in-process cache when WithBlobCache sets a size, else
	// blobStore. Every other user reads blobStore.
	workspaceBlobs  blob.Store
	inventoryReader inventory.Reader
	profiles        profileSettings
	// issuerStore keeps the signing keys and the settings of the OIDC issuer
	// of each namespace, or is nil. issuerBaseURL is the host of the issuer URL,
	// and issuerOutbox receives its notices.
	issuerStore          profile.IssuerStore
	issuerBaseURL        string
	issuerOutbox         notify.Outbox
	sinks                sinkSettings
	jobStore             job.Store
	triggerStore         trigger.Store
	unitSettingsStore    unitsettings.Store
	driftStore           drift.Store
	savedViewStore       savedview.Store
	settingsConfig       settingsConfig
	gitHost              gitHostSettings
	registry             *registrySettings
	idempotencyStore     idempotency.Store
	authenticator        identity.Authenticator
	authorizer           identity.Authorizer
	logger               *slog.Logger
	now                  func() time.Time
	installation         namespace.InstallationName
	listenAddr           string
	listener             net.Listener
	consoleDir           string
	allowedOrigins       []string
	consoleHeaders       ConsoleHeaders
	proxies              proxySettings
	tlsCertFile          string
	tlsKeyFile           string
	publicURL            string
	version              string
	maxCheckpointBytes   int
	maxSecretBatchBytes  int
	maxStateBytes        int64
	softDeleteRetention  time.Duration
	jobRetention         jobRetention
	maxBodyBytesInFlight int64
	// checkpointDeltaCutoff is the checkpoint size in bytes from which the
	// CLI sends delta uploads. Zero hides the delta capability.
	checkpointDeltaCutoff int
	// backup holds the backup target and the dev backup switches. See
	// WithBackupTarget.
	backup       backupSettings
	ipAllowlists *ipAllowlistSettings
	// disablePulumiJournaling turns Pulumi journaling off. See
	// WithDisablePulumiJournaling.
	disablePulumiJournaling bool
	// auditChain is the hash chain setting. See WithAuditChain.
	auditChain audit.ChainSetting
	// drainPeriod is the drain before the shutdown. See WithDrainPeriod.
	drainPeriod time.Duration
	// auditChainInterval is the time between two audit chain checks. See
	// WithAuditChainCheckInterval.
	auditChainInterval time.Duration
	// auditSinks receive a copy of each audit entry. See WithAuditSinks.
	auditSinks []audit.ShipSink
	// oidcProviders are the OpenID Connect providers of the console
	// sign-in. See WithOIDCProvider.
	oidcProviders []OIDCProvider
	// ldapProviders are the LDAP providers of the sign-in. See
	// WithLDAPProvider.
	ldapProviders []LDAPProvider
	// ldapLoopback lets the LDAP sign-in reach a loopback address. See
	// WithLDAPLoopbackForTests.
	ldapLoopback bool
	// signInHTTPClient makes the requests to the sign-in providers of the
	// API. Nil uses the default client of the sign-in.
	signInHTTPClient *http.Client
	// sessionCookies checks the console session cookie on the inventory,
	// audit and Pulumi APIs. newAdapters sets it before it builds them; nil
	// means no console sign-in.
	sessionCookies *sessionauth.Checker
	// instanceID names this server process in the sink leases of its audit
	// shipper. See WithInstanceID.
	instanceID string
	// auditShipper ships to auditSinks. assemble sets it before it mounts
	// the adapters, so the installation API lists it.
	auditShipper *audit.Shipper
	// beforeServe runs after the start-up checks and the listen, before
	// the server serves. See WithBeforeServe.
	beforeServe func(ctx context.Context) error
	// installationKind, kindStore and devCode are the installation kind, the
	// store that keeps it and the one-time sign-in code of opendustpan dev.
	// See WithInstallationKind and WithInstallationKindStore.
	installationKind installation.Kind
	kindStore        installation.KindStore
	devSignIn        *devsignin.Config
	// instanceStore keeps the instance version rows. See WithInstanceStore.
	instanceStore installation.InstanceStore
	// auditSigner signs audit checkpoints. See WithAuditCheckpoints.
	auditSigner *audit.Signer
	// durableStore keeps the durable operations and the job schedules, or
	// is nil without job schedules. See WithDurableStore.
	durableStore DurableStore
	// runners are the stores of the runner protocol, or nil without the
	// protocol. See WithRunners.
	runners RunnerStores
	// sourceFilters keeps the source filter of each namespace, or nil without
	// it. See WithSourceFilters.
	sourceFilters source.Filters
	// trustRules keeps the workload trust rules of each namespace, or nil
	// without them. See WithTrustRules.
	trustRules trustRuleSettings
	// selfService keeps the store of the blueprints of the self-service API.
	// See WithSelfServiceBlueprints.
	selfService selfServiceSettings
	// jobClaimer builds the claimer of the phases of runner jobs for the
	// runners of the protocol, or is nil. See WithJobClaimer.
	jobClaimer JobClaimerFunc
	// jobRecorder records the steps and the log chunks of the launchers of
	// the runner protocol, or is nil. See WithJobRecorder.
	jobRecorder job.CheckedRecorder
	// components are the components this process runs. See WithComponents.
	components Components
	// operationLease is the lease period of a claim on a durable operation,
	// or 0 for the default. See WithOperationLease.
	operationLease time.Duration
	// watch reads the worker state. assemble sets it before it builds the
	// adapters.
	watch *workerWatch
}

// Option configures a Server.
type Option func(*settings)

// WithNamespaceStore sets the namespace persistence port. Required.
//
// The server also uses the store for the audit entries of the scope @server,
// for example the security entry of a stack blob key that a row names outside
// the scope of its stack. So the store must implement audit.ServerStore, as
// the SQLite and PostgreSQL stores do. It must implement audit.Store too, for
// the entries of refusals and overrides. NewServer answers ErrMissingOption
// for a store that lacks either.
func WithNamespaceStore(s namespace.Store) Option {
	return func(o *settings) { o.namespaceStore = s }
}

// WithStackStore sets the stack persistence port. Required.
func WithStackStore(s stack.Store) Option {
	return func(o *settings) { o.stackStore = s }
}

// WithUpdateStore sets the update persistence port. Required.
func WithUpdateStore(s stack.UpdateStore) Option {
	return func(o *settings) { o.updateStore = s }
}

// WithCheckpointStore sets the checkpoint persistence port. Required.
func WithCheckpointStore(s stack.CheckpointStore) Option {
	return func(o *settings) { o.checkpointStore = s }
}

// WithEventStore sets the engine event persistence port. Required.
func WithEventStore(s stack.EventStore) Option {
	return func(o *settings) { o.eventStore = s }
}

// WithSecretKeyStore sets the wrapped-data-key persistence port. Required.
func WithSecretKeyStore(s secret.KeyStore) Option {
	return func(o *settings) { o.secretKeys = s }
}

// WithSecretWrapper sets the key-encryption key holder. Required: the server
// seals OpenTofu state and Pulumi secret values with data keys that this key
// wraps (ADR 41 decisions 10 and 12). NewServer refuses to build without it.
// Run runs secret.CheckWrapper at start, so a key that does not open the
// wrapped keys in the database stops the start (ADR 23).
func WithSecretWrapper(w secret.Wrapper) Option {
	return func(o *settings) { o.secretWrapper = w }
}

// WithWorkspaceStore sets the workspace persistence port. Required.
func WithWorkspaceStore(s workspace.Store) Option {
	return func(o *settings) { o.workspaceStore = s }
}

// WithStateStore sets the state version persistence port. Required. A store
// without WorkspaceStateAt (workspace.StateAtReader) is valid at start, but a
// state-at read fails at request time with workspace.ErrMissingDependency.
func WithStateStore(s workspace.StateStore) Option {
	return func(o *settings) { o.stateStore = s }
}

// WithBlobStore sets the state content persistence port. Required.
func WithBlobStore(s blob.Store) Option {
	return func(o *settings) { o.blobStore = s }
}

// WithBlobCache keeps up to maxBytes of OpenTofu state blobs in memory, so a
// repeat read of a state version does not reach the blob store. Zero, the
// default, turns the cache off. A negative value makes NewServer fail with
// ErrInvalidOption. The cache keeps only key families that are written once,
// so several instances can each keep one over a shared blob store. One
// object takes at most one eighth of maxBytes.
func WithBlobCache(maxBytes int64) Option {
	return func(o *settings) { o.blobCacheBytes = maxBytes }
}

// WithIdempotencyStore sets the store of the replay records of the
// Idempotency-Key header. It is optional: without it a request with the
// header answers 400, and the server does not run the sweep of the records.
func WithIdempotencyStore(st idempotency.Store) Option {
	return func(o *settings) { o.idempotencyStore = st }
}

// WithInventoryReader sets the inventory projection reader. Required.
func WithInventoryReader(r inventory.Reader) Option {
	return func(o *settings) { o.inventoryReader = r }
}

// WithAuthenticator sets how access tokens are resolved to principals. Required.
//
// An authenticator that signs in administrators (principals with Admin set,
// which hold every action with no grant) must also implement
// AdminSubjects() []string and answer their subjects, as
// identity.StaticTokens and identity.Chain do. Otherwise those
// administrators are missing from the access-review export (ADR 52,
// decision 10).
func WithAuthenticator(a identity.Authenticator) Option {
	return func(o *settings) { o.authenticator = a }
}

// WithAuthorizer sets the authorization policy. Defaults to an
// access.Authorizer, which decides with the roles and grants of the store
// (ADR 52), when the namespace store keeps roles, grants and principals,
// as both SQL adapters do. Otherwise it defaults to identity.AdminOnly.
func WithAuthorizer(a identity.Authorizer) Option {
	return func(o *settings) { o.authorizer = a }
}

// WithInstallation sets the name of this installation. Required. It is the
// primary installation of each namespace that this server creates.
func WithInstallation(r namespace.InstallationName) Option {
	return func(o *settings) { o.installation = r }
}

// WithLogger sets the structured logger. Defaults to discarding logs.
func WithLogger(l *slog.Logger) Option {
	return func(o *settings) { o.logger = l }
}

// WithListenAddr sets the address Run listens on. Defaults to ":8080".
// It is ignored when WithListener is set: the passed listener wins.
func WithListenAddr(addr string) Option {
	return func(o *settings) { o.listenAddr = addr }
}

// WithListener sets the listener Run serves on. The listener must already
// be bound, for example on 127.0.0.1:0, and stay open until Run starts.
// Run serves plain HTTP or TLS on it and closes it on shutdown. When set,
// it wins over WithListenAddr: Run never dials or binds another address.
func WithListener(l net.Listener) Option {
	return func(o *settings) { o.listener = l }
}

// WithConsoleDir serves a built web console from dir at "/". The console is a
// separate build; this exists so one process can serve both (omnibus installs).
func WithConsoleDir(dir string) Option {
	return func(o *settings) { o.consoleDir = dir }
}

// WithTLS serves HTTPS with the certificate and key in certFile and keyFile,
// read once at start. The minimum version is TLS 1.2. Both names are
// required: set both or none.
func WithTLS(certFile, keyFile string) Option {
	return func(o *settings) { o.tlsCertFile, o.tlsKeyFile = certFile, keyFile }
}

// WithPublicURL sets the external base URL of the server, for example
// "https://dustpan.acme.internal". Adapters use it for absolute URLs. It
// must be an absolute http or https URL with a host and no query. Empty
// means each request derives it from its own scheme and host.
func WithPublicURL(raw string) Option {
	return func(o *settings) { o.publicURL = raw }
}

// WithAllowedOrigins lists the web console origins allowed to call the API
// cross-origin, for example "https://dustpan.acme.internal".
func WithAllowedOrigins(origins ...string) Option {
	return func(o *settings) { o.allowedOrigins = origins }
}

// WithConsoleHeaders sets the security headers of the web console files. A
// field that is empty keeps the default; ConsoleHeaderOff sends no header.
// Strict-Transport-Security goes out only with WithTLS.
func WithConsoleHeaders(h ConsoleHeaders) Option {
	return func(o *settings) { o.consoleHeaders = h }
}

// WithBeforeServe sets a function that Run calls after the start-up checks
// and the listen, and before the server serves. When it answers an error,
// Run closes the listener and returns the error, so a failed function
// leaves no server running. The binary writes the start audit entry here
// (ADR 63 decision 10), so a start that fails its checks writes no entry.
func WithBeforeServe(fn func(ctx context.Context) error) Option {
	return func(o *settings) { o.beforeServe = fn }
}

// WithClock overrides the time source, for tests.
func WithClock(now func() time.Time) Option {
	return func(o *settings) { o.now = now }
}

// WithVersion sets the server version for dustpan_build_info. Empty means
// dev. The binary passes its stamped version; tests use the default.
func WithVersion(v string) Option {
	return func(o *settings) { o.version = v }
}

// WithMaxCheckpointBytes bounds one checkpoint. Zero means the domain default.
func WithMaxCheckpointBytes(n int) Option {
	return func(o *settings) { o.maxCheckpointBytes = n }
}

// WithMaxSecretBatchBytes bounds one secrets batch. Zero means the domain default.
func WithMaxSecretBatchBytes(n int) Option {
	return func(o *settings) { o.maxSecretBatchBytes = n }
}

// WithSoftDeleteRetention sets how long a deleted stack or workspace stays
// before its final delete (ADR 47, decision 2). Zero means the domain
// default, inventory.DeletedRetention. Any other value below
// MinSoftDeleteRetention makes NewServer fail with ErrInvalidOption.
func WithSoftDeleteRetention(d time.Duration) Option {
	return func(o *settings) { o.softDeleteRetention = d }
}

// WithMaxStateBytes bounds one OpenTofu state version. Zero means the domain default.
func WithMaxStateBytes(n int64) Option {
	return func(o *settings) { o.maxStateBytes = n }
}

// WithMaxBodyBytesInFlight bounds concurrent large request bodies by bytes
// held at once. Zero means the adapter default.
func WithMaxBodyBytesInFlight(n int64) Option {
	return func(o *settings) { o.maxBodyBytesInFlight = n }
}

// WithDisablePulumiJournaling turns Pulumi journaling off when disabled is
// true. The server then answers journalVersion 0 at the start of a Pulumi
// update, so the Pulumi CLI saves checkpoints. Journaling is on by default.
func WithDisablePulumiJournaling(disabled bool) Option {
	return func(o *settings) { o.disablePulumiJournaling = disabled }
}

// WithCheckpointDeltaCutoff sets the checkpoint size in bytes from which
// the CLI sends delta uploads. Zero hides the delta capability, so the CLI
// sends whole checkpoints. A negative value fails NewServer. Without this
// option the server advertises the default cutoff.
func WithCheckpointDeltaCutoff(n int) Option {
	return func(o *settings) { o.checkpointDeltaCutoff = n }
}

// Server is a configured OpenDustpan server.
type Server struct {
	handler http.Handler
	// started records that Start succeeded. The gate in front of handler
	// reads it.
	started *startState
	// ungated is the handler before the component gate. Only the tests read
	// it.
	ungated http.Handler
	// mounted holds the adapters of the server. Only the tests of the API
	// description read it.
	mounted *adapters
	// root is the root mux. Only the tests of the API description read it.
	root          *rootMux
	logger        *slog.Logger
	listenAddr    string
	listener      net.Listener
	secrets       *secret.Service
	secretKeys    secret.KeyStore
	secretWrapper secret.Wrapper
	workspaces    *workspace.Service
	manualLocks   *manuallock.Service // Only the tests read it.
	backups       *backup.Service
	restores      *backup.RestoreService
	storeChecks   *storecheck.Service
	now           func() time.Time
	registry      *metrics.Registry
	tlsCertFile   string
	tlsKeyFile    string
	observed      *metrics.ServerMetrics
	// chain is the audit chain of the store, or nil when the store keeps
	// its own chain.
	chain *auditChain
	// tokens runs the inactivity disable of access tokens and accounts on a
	// schedule (ADR 53), or is nil when the store keeps no tokens.
	tokens      *accesstoken.Service
	tempAdmins  *tempadmin.Service
	shipper     *audit.Shipper
	beforeServe func(ctx context.Context) error
	// flushDenials writes the counts of refused requests that the limiter still
	// holds. Run calls it once at shutdown.
	flushDenials func(ctx context.Context)
	// deleted runs the retention sweep of the deleted units on a schedule.
	deleted *deletedSweeper
	// signInRequests deletes the expired sign-in requests on a schedule, or
	// is nil with no console sign-in. signInPruneEvery is the period of
	// that schedule.
	signInRequests   *session.Sessions
	signInPruneEvery time.Duration
	// idempotency deletes the expired replay records on a schedule, or is nil
	// without a store of them. idempotencyEvery is the period of that
	// schedule.
	idempotency      idempotency.Store
	idempotencyEvery time.Duration
	// ready is the readiness probe. Run makes it answer 503 when the drain
	// starts. drainPeriod is the length of the drain.
	ready       *readiness
	drainPeriod time.Duration
	// auditChainEvery is the period of the audit chain check: the schedule
	// audit-chain-check, or the loop of each instance without a durable
	// store.
	auditChainEvery time.Duration
	// signInProviders binds each provider of the console sign-in to its
	// issuer at the start, or is the zero value with no console sign-in.
	signInProviders providerBinding
	// kind is the installation kind of this server and the store that keeps
	// its claim.
	kind kindState
	// instances is the instance version row of this server and the store that
	// keeps it.
	instances instanceState
	// durable keeps the job schedules, or is nil without them. inactivityEvery
	// and deletedEvery are the periods of those schedules.
	durable      DurableStore
	durableOwner string
	// reconciler does the periodic work of the job model: the schedule
	// job.reconcile. It is nil without a job store or a namespace service.
	reconciler *job.Reconciler
	// artifactSweeper deletes the plan and log blobs of jobs that no row names:
	// the schedule job.artifact-sweep. It is nil without the ports that it
	// needs.
	artifactSweeper *job.ArtifactSweeper
	// nameLocks holds the counts of the lockout by name: the schedule
	// name-lock-prune deletes the counts that can no longer change a lock.
	// It is nil when the store keeps no such counts.
	nameLocks nameLockPrune
	// issuer is the OIDC issuer, or nil. The schedule issuer.rotate calls it.
	issuer          *issuersvc.Service
	inactivityEvery time.Duration
	deletedEvery    time.Duration
	// components are the components this process runs. watch reads the
	// worker state for the readiness probe, the metric and the start gate.
	components Components
	watch      *workerWatch
	// stateWrite is the effective value of the state.write gates, which the
	// start writes to the log, and to the audit log when the gates are off.
	stateWrite stateWriteStart
}

// services holds the domain services that NewServer wires together.
type services struct {
	namespaces *namespace.Service
	stacks     *stack.Service
	updates    *stack.Updates
	secrets    *secret.Service
	workspaces *workspace.Service
	// manualLocks is the manual lock service, or nil when the store holds no
	// manual locks.
	manualLocks *manuallock.Service
	// issuer is the OIDC issuer, or nil when the store keeps none. assemble
	// sets it.
	issuer *issuersvc.Service
	// tokens keeps the access tokens of the principals (ADR 53), or is nil
	// when the store keeps none. NewServer sets it after newServices.
	tokens *accesstoken.Service
	// collections is the collection service (ADR 51), or nil when the store
	// holds no collections. NewServer sets it after newServices.
	collections *collection.Service
	// placements is the placement rule service, or nil when the store holds
	// no placement rules. NewServer sets it after newServices.
	placements *collection.PlacementService
	// gates checks the writes of state against the access limits and the
	// gates of state.write, or is nil when the store keeps no gates.
	gates *gateService
	// stateWrite is the rule of the state.write gates that the tool adapters
	// and the claimer share. Its guard is never nil after
	// addCollectionServices.
	stateWrite stateWriteGates
	// deleted runs the retention sweep. assemble sets it, because
	// it counts on the server metrics.
	deleted *deletedSweeper
	// access keeps the roles and grants, or is nil when the store keeps
	// none. NewServer sets it after newServices. The installation API and
	// the token service share it.
	access *access.Service
	// tempAdmins keeps the temporary administrators, or is nil when the
	// store keeps none. NewServer sets it after newServices. The expiry
	// sweep and the banner flag share it.
	tempAdmins *tempadmin.Service
}

// defaultSettings answers the settings of a server before its options.
//
//nolint:funlen // One literal that names every field of settings (exhaustruct); its length is the field count.
func defaultSettings() *settings {
	return &settings{
		denialLimiter:           defaultDenialLimiter(),
		devRefusal:              new(devRefusalState),
		stackRefusals:           nil,
		settingsConfig:          settingsConfig{store: nil, declarations: nil, static: nil},
		blobGuard:               nil,
		disablePulumiJournaling: false,
		namespaceStore:          nil,
		stackStore:              nil,
		updateStore:             nil,
		checkpointStore:         nil,
		eventStore:              nil,
		secretKeys:              nil,
		secretWrapper:           nil,
		streamStop:              &streamStop{stopped: atomic.Bool{}},
		workspaceStore:          nil,
		stateStore:              nil,
		blobStore:               nil,
		blobCacheBytes:          0,
		workspaceBlobs:          nil,
		inventoryReader:         nil,
		profiles:                defaultProfileSettings(),
		issuerStore:             nil,
		issuerBaseURL:           "",
		issuerOutbox:            nil,
		sinks:                   sinkSettings{store: nil, senders: nil, ports: egress.Ports{}},
		jobStore:                nil,
		triggerStore:            nil,
		unitSettingsStore:       nil,
		driftStore:              nil,
		savedViewStore:          nil,
		gitHost:                 gitHostSettings{store: nil, allow: nil, cas: nil, loopback: false, checkLimits: nil},
		registry:                nil,
		idempotencyStore:        nil,
		authenticator:           nil,
		authorizer:              nil,
		logger:                  slog.New(slog.DiscardHandler),
		now:                     time.Now,
		installation:            "",
		listenAddr:              defaultListenAddr,
		listener:                nil,
		consoleDir:              "",
		allowedOrigins:          nil,
		consoleHeaders:          noConsoleHeaders(),
		proxies:                 defaultProxySettings(),
		tlsCertFile:             "",
		tlsKeyFile:              "",
		publicURL:               "",
		oidcProviders:           nil,
		ldapProviders:           nil,
		ldapLoopback:            false,
		signInHTTPClient:        nil,
		sessionCookies:          nil,
		version:                 "",
		maxCheckpointBytes:      0,
		maxSecretBatchBytes:     0,
		maxStateBytes:           0,
		softDeleteRetention:     0,
		jobRetention:            defaultJobRetention(),
		maxBodyBytesInFlight:    0,
		backup:                  defaultBackupSettings(),
		beforeServe:             nil,
		installationKind:        "",
		kindStore:               nil,
		instanceStore:           nil,
		devSignIn:               nil,
		checkpointDeltaCutoff:   pulumi.DefaultCheckpointDeltaCutoff,
		auditChain:              audit.ChainOn,
		auditChainInterval:      auditChainCheckInterval,
		drainPeriod:             0,
		ipAllowlists:            defaultIPAllowlistSettings(),
		auditSinks:              nil,
		instanceID:              "",
		auditShipper:            nil,
		auditSigner:             nil,
		durableStore:            nil,
		runners:                 nil,
		sourceFilters:           nil,
		trustRules:              trustRuleSettings{store: nil, cas: nil, loopback: false},
		selfService:             selfServiceSettings{store: nil},
		jobClaimer:              nil,
		jobRecorder:             nil,
		components:              AllComponents(),
		operationLease:          0,
		watch:                   nil,
	}
}

// defaultDenialLimiter returns the denial limiter with its default limits.
func defaultDenialLimiter() *denylimit.Limiter {
	return denylimit.New(denylimit.Config{
		Window: 0, MaxKeys: 0, MaxFlush: 0, MaxKeysPerPrincipal: 0, Now: nil,
	})
}

// NewServer validates the options and wires the server together.
func NewServer(opts ...Option) (*Server, error) {
	cfg := defaultSettings()

	for _, opt := range opts {
		opt(cfg)
	}

	base, err := checkSettings(cfg)
	if err != nil {
		return nil, err
	}

	if cacheErr := wrapBlobCache(cfg); cacheErr != nil {
		return nil, cacheErr
	}

	if authzErr := setDefaultAuthorizer(cfg); authzErr != nil {
		return nil, authzErr
	}

	grants, tokens, err := newIdentityServices(cfg)
	if err != nil {
		return nil, err
	}

	// The auth ports need only the token service, so they exist before the
	// services. From the write below, each later reader of cfg.authenticator
	// and cfg.authorizer gets the chain, and no constructor after this point
	// can read the bare configured port. The bare port is visible only to
	// newIdentityServices above (the access service and the token service),
	// which are the named exceptions in bareAuthorizerCallers
	// (authchain_internal_test.go). This is the one write of the chain into
	// cfg: an area constructor takes the ports as arguments instead, and
	// newServices gets the authorizer as an argument for the manual lock
	// service.
	ports, err := newAuthPorts(cfg, tokens)
	if err != nil {
		return nil, err
	}

	cfg.authenticator, cfg.authorizer = ports.authenticator, ports.authorizer

	svc, err := newServices(cfg, tokens, ports.authorizer)
	if err != nil {
		return nil, err
	}

	svc.tokens, svc.access = tokens, grants

	err = svc.addCollectionServices(cfg)
	if err != nil {
		return nil, err
	}

	// The worker watch does not exist yet: prepareBackground makes it in
	// assemble. So the claims of the backup service do not go through the
	// claim recorder of the watch. The restore service and the store checks
	// get the watch.
	backups, err := newBackupService(backupInputs{
		settings: cfg.backup, store: cfg.namespaceStore, blobs: cfg.blobStore, workspaces: svc.workspaces,
		watch: nil, owner: durableOwner(cfg), lease: cfg.operationLease, version: cfg.version,
		now: cfg.now, logger: cfg.logger,
	})
	if err != nil {
		return nil, err
	}

	svc.tempAdmins, err = newTempAdminService(cfg)
	if err != nil {
		return nil, err
	}

	return assemble(cfg, base, svc, earlyParts{backups: backups, gates: ports.gates})
}

// earlyParts holds what NewServer builds before assemble: the installation
// backup service, or nil when no backup target is configured, and the failure
// gates of the console sign-in.
type earlyParts struct {
	backups *backup.Service
	gates   signInGates
}

// authPorts holds what newAuthPorts builds: the authenticator and the
// authorizer of the adapters, and the failure gates of the console sign-in.
type authPorts struct {
	authenticator identity.Authenticator
	authorizer    identity.Authorizer
	gates         signInGates
}

// newAuthPorts builds the authenticator chain and the authorizer chain of the
// adapters from the configured ports. The authenticator chain, from the
// inside out, is:
//
//  1. the configured authenticator, then the stored access tokens behind
//     their failure gate;
//  2. the marks of the temporary administrators;
//  3. the refusal of dev subjects on an installation that is not dev;
//  4. the refusal of the subjects of namespace service principals.
//
// The authorizer chain, from the inside out, is the configured authorizer,
// the self actions, the namespace service rule and the IP allowlist gate.
// Each step is one statement here, in this order.
func newAuthPorts(cfg *settings, tokens *accesstoken.Service) (authPorts, error) {
	var none authPorts

	authn, gates, err := gatedAuthenticator(cfg.authenticator, tokens, cfg.namespaceStore, cfg.logger, cfg.now)
	if err != nil {
		return none, err
	}

	// The namespace service rule decides the principals that the server
	// builds before the self actions and the configured authorizer, so no
	// authorizer of an embedder can widen them. Every authenticated person
	// lists and revokes its own personal tokens with no grant.
	authz := allowlistGate(identity.NamespaceServiceRule{Next: identity.SelfAllowed{
		Next: cfg.authorizer, Actions: []identity.Action{
			identity.ActionTokenManageOwn, identity.ActionCallerManagePreferences, identity.ActionCallerManageSignIn,
			identity.ActionCallerManageGitHostLinks,
		},
	}}, cfg.ipAllowlists)

	marks, marked, err := tempAdminDecorator(authn, cfg.namespaceStore, cfg.now, cfg.logger)
	if err != nil {
		return none, err
	}

	if marked {
		authn = marks
	}

	// Only opendustpan dev makes a person with a dev subject (a subject that
	// starts with principal.DevSubjectPrefix), so one on another
	// installation comes from a restored dev backup.
	if cfg.installationKind != installation.KindDev && authn != nil {
		authn = principal.RefuseDevSubjects(authn).OnRefused(devRefusalAudit(cfg))
	}

	// The server builds the namespace service principals itself, so no
	// authenticator gives one, on every kind of installation.
	if authn != nil {
		authn = principal.RefuseNamespaceServiceSubjects(authn)
	}

	return authPorts{authenticator: authn, authorizer: authz, gates: gates}, nil
}

// tempAdminDecorator marks the temporary administrators among the callers
// that next resolves, with a sign-in entry for each use and an expiry entry
// at the first use after the expiry. The second result is false when the
// store keeps no records or no principals: then next stays as it is. A store
// with records and no audit entries is an error.
func tempAdminDecorator(
	next identity.Authenticator, store namespace.Store, now func() time.Time, logger *slog.Logger,
) (*tempadmin.Decorator, bool, error) {
	records, ok := store.(tempadmin.Store)
	if !ok {
		return nil, false, nil
	}

	principals, ok := store.(tempadmin.Principals)
	if !ok {
		return nil, false, nil
	}

	entries, err := serverAuditOf(store)
	if err != nil {
		return nil, false, err
	}

	dec, err := tempadmin.NewDecorator(tempadmin.DecoratorConfig{
		Next: next, Records: records, Principals: principals, Request: requestOf,
		Expirer: records, Audit: entries, Now: now, Logger: logger,
	})
	if err != nil {
		return nil, false, fmt.Errorf("temporary administrator authenticator: %w", err)
	}

	return dec, true, nil
}

// requestOf answers the client address and the request ID that the server
// stored in ctx, for the audit entries of a temporary administrator.
func requestOf(ctx context.Context) tempadmin.Request {
	req := tempadmin.Request{SourceIP: "", RequestID: requestid.FromContext(ctx)}
	if ip := clientip.FromContext(ctx); ip.IsValid() {
		req.SourceIP = ip.String()
	}

	return req
}

// newTempAdminService builds the temporary administrator service, or nil
// when the store keeps no records: the expiry sweep stays off and the
// routes answer no flag.
func newTempAdminService(cfg *settings) (*tempadmin.Service, error) {
	store, ok := cfg.namespaceStore.(tempadmin.Store)
	if !ok {
		return nil, nil //nolint:nilnil // No record store is a valid setup: expiry never runs.
	}

	svc, err := tempadmin.NewService(tempadmin.Config{Store: store, Reviews: nil, Now: cfg.now, Logger: cfg.logger})
	if err != nil {
		return nil, fmt.Errorf("temporary administrator service: %w", err)
	}

	return svc, nil
}

// newServerMetrics returns the metrics of the server, and gives the recorder of
// the refused dev sign-ins the namespace reader and the failure counter.
func newServerMetrics(cfg *settings, svc services) (*metrics.ServerMetrics, error) {
	observed := metrics.NewServerMetrics(cfg.version, cfg.now())

	err := cfg.devRefusal.bind(cfg, svc.namespaces, func() { observed.IncAuditAppendFailure() })
	if err != nil {
		return nil, err
	}

	return observed, nil
}

// assemble mounts the adapters and routes around the services and the parts
// that NewServer built before it.
func assemble(cfg *settings, base baseurl.Resolver, svc services, early earlyParts) (*Server, error) {
	observed, shipper, err := prepareBackground(cfg, &svc)
	if err != nil {
		return nil, err
	}

	mount, err := newAdapters(svc, cfg, base, early, observed)
	if err != nil {
		return nil, err
	}

	handlers, err := routes(mount, cfg, observed)
	if err != nil {
		return nil, err
	}

	flushDenials, err := denialFlusher(cfg, svc, observed)
	if err != nil {
		return nil, err
	}

	reconciler, artifactSweeper, err := newJobWorkers(svc, cfg)
	if err != nil {
		return nil, err
	}

	started := newStartState()

	srv := &Server{
		flushDenials: flushDenials,
		handler:      withStartGate(handlers.served, started, cfg.logger),
		started:      started,
		ungated:      handlers.ungated, mounted: &mount, root: handlers.root, manualLocks: svc.manualLocks,
		logger:        cfg.logger,
		listenAddr:    cfg.listenAddr,
		listener:      cfg.listener,
		secrets:       svc.secrets,
		secretKeys:    cfg.secretKeys,
		secretWrapper: cfg.secretWrapper,
		workspaces:    svc.workspaces,
		backups:       early.backups,
		restores:      mount.restores,
		storeChecks:   mount.storeChecks,
		now:           cfg.now,
		registry:      observed.Registry(),
		tlsCertFile:   cfg.tlsCertFile,
		tlsKeyFile:    cfg.tlsKeyFile,
		observed:      observed,
		chain:         mount.chain,
		ready:         mount.ready, drainPeriod: cfg.drainPeriod,
		tokens:           svc.tokens,
		tempAdmins:       svc.tempAdmins,
		shipper:          shipper,
		beforeServe:      cfg.beforeServe,
		deleted:          svc.deleted,
		signInRequests:   mount.signInSessions(),
		signInPruneEvery: signInRequestPruneEvery,
		signInProviders:  bindingOf(cfg, mount.signIn),
		kind:             newKindState(cfg), instances: newInstanceState(cfg),
		durable:      cfg.durableStore,
		durableOwner: durableOwner(cfg),
		reconciler:   reconciler, artifactSweeper: artifactSweeper, issuer: svc.issuer,
		inactivityEvery: sweepEvery, deletedEvery: deletedSweepEvery,
		idempotency: cfg.idempotencyStore, idempotencyEvery: idempotencySweepEvery,
		components: cfg.components, nameLocks: nameLocksOf(cfg),
		watch: cfg.watch, auditChainEvery: cfg.auditChainInterval, stateWrite: newStateWriteStart(cfg, svc),
	}

	cfg.watch.attach(srv, observed.Registry())

	return srv, nil
}

// prepareBackground builds the metrics, the worker watch, the sweeper of
// deleted units and the audit shipper, and sets them in cfg and svc, and builds the issuer. One
// registry holds every metric family: the request, runtime and build families,
// and the readiness failure counter of the probe.
func prepareBackground(cfg *settings, svc *services) (*metrics.ServerMetrics, *audit.Shipper, error) {
	observed, err := newServerMetrics(cfg, *svc)
	if err != nil {
		return nil, nil, err
	}

	cfg.watch = newWorkerWatch(cfg)

	registerBlobPurgeMetrics(observed.Registry(), cfg)

	deleted, err := newDeletedSweeper(*svc, cfg, observed)
	if err != nil {
		return nil, nil, err
	}

	svc.deleted = deleted

	shipper, err := newAuditShipper(cfg, observed)
	if err != nil {
		return nil, nil, err
	}

	cfg.auditShipper = shipper

	if svc.issuer, err = newIssuerService(*svc, cfg); err != nil {
		return nil, nil, err
	}

	return observed, shipper, nil
}

// adapters holds the HTTP adapters that routes mounts.
type adapters struct {
	// neutralHandlers are the inventory, audit, context and runner
	// handlers.
	neutralHandlers

	pulumi      http.Handler
	tfe         http.Handler
	tofuHTTP    http.Handler
	server      http.Handler
	ready       *readiness
	maintenance *maintenance.Watcher
	restores    *backup.RestoreService
	storeChecks *storecheck.Service
	chain       *auditChain
	// signIn is the console sign-in, or nil with no provider.
	signIn *serverapi.SignIn
}

// signInSessions returns the session service of the console sign-in, or
// nil with no sign-in.
func (a adapters) signInSessions() *session.Sessions {
	if a.signIn == nil {
		return nil
	}

	return a.signIn.Sessions
}

// newAdapters builds every HTTP adapter from the services and settings.
// observed is the server registry: the metrics route writes it, and the
// readiness probe counts its failures on it.
func newAdapters(
	svc services,
	cfg *settings,
	base baseurl.Resolver,
	early earlyParts,
	observed *metrics.ServerMetrics,
) (adapters, error) {
	chain, err := newAuditChain(cfg, observed)
	if err != nil {
		return adapters{}, err
	}

	maint, watcher, err := newMaintenance(svc, cfg, chain)
	if err != nil {
		return adapters{}, err
	}

	gate := bodygate.New(cfg.maxBodyBytesInFlight)

	sessionPolicies, signIn, err := newConsoleSignIn(cfg, svc.tokens, early.gates)
	if err != nil {
		return adapters{}, err
	}

	api, err := newPulumiHandler(svc, cfg, observed, pulumiDeps{base: base, gate: gate, policies: sessionPolicies})
	if err != nil {
		return adapters{}, err
	}

	neutral, err := newNeutralHandlers(svc, cfg, observed, sessionPolicies)
	if err != nil {
		return adapters{}, err
	}

	tofuHTTPAPI, err := newTofuHTTPHandler(cfg, svc, observed, gate)
	if err != nil {
		return adapters{}, err
	}

	tfeAPI, err := newTFEHandler(cfg, svc, observed, tfeDeps{base: base, gate: gate, policies: sessionPolicies})
	if err != nil {
		return adapters{}, err
	}

	parts, err := newInstallationAPI(cfg, installationDeps{
		services: svc, backups: early.backups, maint: maint, observed: observed, chain: chain,
		policies: sessionPolicies, signIn: signIn,
	})
	if err != nil {
		return adapters{}, err
	}

	mounted := adapters{
		neutralHandlers: neutral,
		pulumi:          api,
		tfe:             tfeAPI,
		tofuHTTP:        tofuHTTPAPI,
		server:          parts.handler,
		ready:           newReadiness(cfg, observed, chain),
		maintenance:     watcher,
		restores:        parts.restores,
		storeChecks:     parts.storeChecks,
		chain:           chain,
		signIn:          signIn,
	}

	// The server refuses to start with a route under the prefix that has no
	// valid name in the description, so it never serves a partial description.
	if _, err = mounted.describeOperations(); err != nil {
		return adapters{}, err
	}

	return mounted, nil
}

// neutralHandlers are the tool-neutral inventory API, audit API and context
// API. context is nil when the store keeps no context.
type neutralHandlers struct {
	inventory http.Handler
	audit     http.Handler
	context   http.Handler
	// runners is the runner protocol and its management API, or nil.
	runners http.Handler
	// profiles is nil when no profile store is set.
	profiles http.Handler
	// issuer is nil when no issuer store is set.
	issuer http.Handler
	// sinks is nil when no sink store is set.
	sinks http.Handler
	// jobs is nil when no job store is set.
	jobs http.Handler
	// triggers is nil when no trigger store is set, or the server lacks the
	// job service that a trigger starts a plan through.
	triggers http.Handler
	// unitSettings is nil when no store of unit settings is set, or the server
	// lacks the Git host store or the runner stores that the settings check.
	unitSettings http.Handler
	// drift is nil when no drift store is set.
	drift http.Handler
	// registry is nil when no registry store is set.
	registry http.Handler
	// sourceFilter is nil when no store of the source filter is set.
	sourceFilter http.Handler
	// trustRules is nil when no trust rule store is set.
	trustRules http.Handler
	// selfService is nil when no blueprint store is set.
	selfService http.Handler
}

// newNeutralHandlers builds the tool-neutral inventory API, audit API and
// context API.
func newNeutralHandlers(
	svc services, cfg *settings, observed *metrics.ServerMetrics, policies *session.Policies,
) (neutralHandlers, error) {
	onFailure := func() { observed.IncAuditAppendFailure() }

	units, err := newInventoryHandler(svc, cfg, onFailure)
	if err != nil {
		return neutralHandlers{}, err
	}

	entries, err := newAuditHandler(svc, cfg, onFailure)
	if err != nil {
		return neutralHandlers{}, err
	}

	contexts, err := newContextHandler(svc, cfg, onFailure)
	if err != nil {
		return neutralHandlers{}, err
	}

	runners, err := newRunnerHandler(cfg, svc, onFailure)
	if err != nil {
		return neutralHandlers{}, err
	}

	profileAPI, err := newProfileAPIs(svc, cfg, onFailure, policies)
	if err != nil {
		return neutralHandlers{}, err
	}

	jobAPIs, err := newJobAndDriftHandlers(svc, cfg, onFailure)
	if err != nil {
		return neutralHandlers{}, err
	}

	registries, err := newRegistryHandler(svc, cfg, onFailure)
	if err != nil {
		return neutralHandlers{}, err
	}

	late, err := newTriggersAndSettingsHandlers(svc, cfg, onFailure)
	if err != nil {
		return neutralHandlers{}, err
	}

	settingAPIs, err := newNamespaceSettingHandlers(svc, cfg, onFailure)
	if err != nil {
		return neutralHandlers{}, err
	}

	return neutralHandlers{
		inventory:    units,
		audit:        entries,
		context:      contexts,
		runners:      runners,
		profiles:     profileAPI.profiles,
		issuer:       profileAPI.issuer,
		sinks:        profileAPI.sinkAPI,
		jobs:         jobAPIs.jobs,
		triggers:     late.triggers,
		unitSettings: late.unitSettings,
		drift:        jobAPIs.drift,
		registry:     registries,

		sourceFilter: settingAPIs.sourceFilter,
		trustRules:   settingAPIs.trustRules,
		selfService:  settingAPIs.selfService,
	}, nil
}

// profileAPIs holds the profiles API, the issuer API and the sinks API.
type profileAPIs struct {
	profiles http.Handler
	issuer   http.Handler
	sinkAPI  http.Handler
}

// newProfileAPIs builds the profiles API, the issuer API and the sinks API.
func newProfileAPIs(
	svc services, cfg *settings, onFailure func(), policies *session.Policies,
) (profileAPIs, error) {
	idem, err := idempotencyMiddleware(cfg)
	if err != nil {
		return profileAPIs{}, err
	}

	profiles, err := newProfileHandler(profileInputs{
		store: cfg.profiles.store, entries: cfg.namespaceStore, namespaces: svc.namespaces, sealer: svc.secrets,
		authenticator: cfg.authenticator, authorizer: cfg.authorizer,
		sessions: cfg.sessionCookies, policies: policies, idempotency: idem,
		denialLimiter: cfg.denialLimiter, onFailure: onFailure, logger: cfg.logger, now: cfg.now,
	})
	if err != nil {
		return profileAPIs{}, err
	}

	issuer, err := newIssuerHandler(svc, cfg, onFailure, policies)
	if err != nil {
		return profileAPIs{}, err
	}

	sinks, err := newSinksHandler(svc, cfg, onFailure)
	if err != nil {
		return profileAPIs{}, err
	}

	return profileAPIs{profiles: profiles, issuer: issuer, sinkAPI: sinks}, nil
}

// namespaceSettingHandlers holds the source filter API and the trust rule
// API. Each is nil when its store is not set.
type namespaceSettingHandlers struct {
	sourceFilter http.Handler
	trustRules   http.Handler
	selfService  http.Handler
}

// newNamespaceSettingHandlers builds the source filter API and the trust rule
// API of the stores that cfg sets.
func newNamespaceSettingHandlers(svc services, cfg *settings, onFailure func()) (namespaceSettingHandlers, error) {
	var (
		out namespaceSettingHandlers
		err error
	)

	if cfg.sourceFilters != nil {
		out.sourceFilter, err = newSourceFilterHandler(svc, cfg, onFailure)
		if err != nil {
			return namespaceSettingHandlers{sourceFilter: nil, trustRules: nil, selfService: nil}, err
		}
	}

	if cfg.trustRules.store != nil {
		out.trustRules, err = newTrustRuleHandler(svc, cfg, onFailure)
		if err != nil {
			return namespaceSettingHandlers{sourceFilter: nil, trustRules: nil, selfService: nil}, err
		}
	}

	if cfg.selfService.store != nil {
		out.selfService, err = newSelfServiceHandler(svc, cfg, onFailure)
		if err != nil {
			return namespaceSettingHandlers{sourceFilter: nil, trustRules: nil, selfService: nil}, err
		}
	}

	return out, nil
}

// jobAPIs holds the jobs API and the drift API, which share one job service.
type jobAPIs struct {
	jobs  http.Handler
	drift http.Handler
}

// newJobAndDriftHandlers builds the jobs API and the drift API. One job service
// starts the jobs of the jobs API and the drift checks.
func newJobAndDriftHandlers(svc services, cfg *settings, onFailure func()) (jobAPIs, error) {
	var starter job.Starter

	if cfg.jobStore != nil {
		service, err := newJobService(svc, cfg)
		if err != nil {
			return jobAPIs{jobs: nil, drift: nil}, err
		}

		starter = service
	}

	jobs, err := newJobsHandler(svc, cfg, onFailure, starter)
	if err != nil {
		return jobAPIs{jobs: nil, drift: nil}, err
	}

	drifts, err := newDriftHandler(svc, cfg, onFailure, starter)
	if err != nil {
		return jobAPIs{jobs: nil, drift: nil}, err
	}

	return jobAPIs{jobs: jobs, drift: drifts}, nil
}

// newReadiness builds the readiness probe: it pings the store and the blob
// store, and counts its failures on observed.
func newReadiness(cfg *settings, observed *metrics.ServerMetrics, chain *auditChain) *readiness {
	var admits func(context.Context) bool
	if chain != nil {
		admits = chain.admits
	}

	// A store without the check leaves minimum nil: the probe skips the check.
	var minimum minimumChecker
	if checker, ok := cfg.namespaceStore.(minimumChecker); ok {
		minimum = checker
	}

	// A store without the write check leaves writable nil: the probe skips it.
	var writable writeChecker
	if checker, ok := cfg.namespaceStore.(writeChecker); ok {
		writable = checker
	}

	return newComponentReadiness(readinessSettings{
		components:  cfg.components,
		streamStop:  cfg.streamStop,
		store:       cfg.namespaceStore,
		blobs:       cfg.blobStore,
		watch:       cfg.watch,
		chainAdmits: admits,
		minimum:     minimum,
		writable:    writable,
		timeout:     readinessTimeout,
		failures:    observed.ReadinessFailures(),
		logger:      cfg.logger,
	})
}

// newTofuHTTPHandler builds the OpenTofu http backend adapter (ADR 49). It
// shares the workspace service with the TFE-compatible adapter; it does not
// call that adapter.
func newTofuHTTPHandler(
	cfg *settings, svc services, observed *metrics.ServerMetrics, gate *bodygate.Gate,
) (*tofuhttp.Handler, error) {
	entries, err := auditStoreOf(cfg)
	if err != nil {
		return nil, err
	}

	handler, err := tofuhttp.NewHandler(tofuhttp.Config{
		Authenticator: cfg.authenticator,
		Authorizer:    cfg.authorizer,
		Logger:        cfg.logger,
		Workspaces:    svc.workspaces,
		Namespaces:    svc.namespaces,
		Audit:         entries,
		Gate:          gate,
		OnAuditFailure: func() {
			observed.IncAuditAppendFailure()
		},
		DenialLimiter: cfg.denialLimiter,
		StateGuard:    svc.stateWrite.guard,
		Placer:        newWorkspacePlacer(svc),
	})
	if err != nil {
		return nil, fmt.Errorf("tofu http backend: %w", err)
	}

	return handler, nil
}

// newMaintenance builds the maintenance service and the watcher that reads
// the maintenance state for every request. A waiting change of the audit
// chain holds maintenance mode.
func newMaintenance(
	svc services, cfg *settings, chain *auditChain,
) (*maintenance.Service, *maintenance.Watcher, error) {
	maintenanceStore, ok := cfg.namespaceStore.(maintenance.Store)
	if !ok {
		return nil, nil, errMaintenanceNeedsStore
	}

	var hold func(ctx context.Context) (bool, error)
	if chain != nil {
		hold = chain.held
	}

	maint, err := maintenance.NewService(maintenance.Config{
		Store:      maintenanceStore,
		Namespaces: svc.namespaces,
		Stacks:     svc.stacks,
		Updates:    svc.updates,
		Workspaces: svc.workspaces,
		Hold:       hold,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("maintenance service: %w", err)
	}

	// The watcher measures its cache limit in wall-clock time, not the
	// injected domain clock: tests freeze the domain clock, and the limit
	// must still expire.
	watcher := maintenance.NewWatcher(maintenanceStore, nil, 0)

	return maint, watcher, nil
}

// tfeDeps are the shared parts that the TFE-compatible API takes besides the
// settings, the services and the metrics: the external base URL, the body gate
// and the session policy service, nil for a store with no session policy.
type tfeDeps struct {
	base     baseurl.Resolver
	gate     *bodygate.Gate
	policies *session.Policies
}

// newTFEHandler returns the TFE-compatible API handler that OpenTofu speaks
// to. base reports the external base URL for absolute URLs. observed counts
// failed audit appends: the reveal call, the state download and the single
// output route fail closed with a 500 when the append fails, so the counter
// is the alert for lost entries.
func newTFEHandler(
	cfg *settings, svc services, observed *metrics.ServerMetrics, deps tfeDeps,
) (*tfe.Handler, error) {
	base, gate, policies := deps.base, deps.gate, deps.policies

	entries, err := auditStoreOf(cfg)
	if err != nil {
		return nil, err
	}

	registryProtocol, err := newRegistryProtocol(cfg, base.String(), func() { observed.IncAuditAppendFailure() })
	if err != nil {
		return nil, err
	}

	// A nil *session.Policies must not become a non-nil interface.
	var sessionPolicy tfe.SessionPolicy
	if policies != nil {
		sessionPolicy = policies
	}

	outputChecker, err := newOutputChecker(cfg, svc)
	if err != nil {
		return nil, err
	}

	handler, err := tfe.NewHandler(tfe.Config{
		Registry:      registryProtocol,
		Authenticator: cfg.authenticator,
		Authorizer:    cfg.authorizer,
		Sessions:      cfg.sessionCookies,
		SessionPolicy: sessionPolicy,
		AddressGate:   allowlistAddressGate(cfg.ipAllowlists),
		Logger:        cfg.logger,
		Namespaces:    svc.namespaces,
		Workspaces:    svc.workspaces,
		OutputChecker: outputChecker,
		BaseURL:       base,
		Audit:         entries,
		Gate:          gate,
		MaxStateBytes: cfg.maxStateBytes,
		OnAuditFailure: func() {
			observed.IncAuditAppendFailure()
		},
		DenialLimiter: cfg.denialLimiter,
		StateGuard:    svc.stateWrite.guard,
		Placer:        newWorkspacePlacer(svc),
	})
	if err != nil {
		return nil, fmt.Errorf("tfe api: %w", err)
	}

	return handler, nil
}

// checkPeriods validates the options that set a length of time.
func checkPeriods(cfg *settings) error {
	if err := checkDrainPeriod(cfg.drainPeriod); err != nil {
		return err
	}

	return checkAuditChainInterval(cfg.auditChainInterval)
}

// checkSettings validates the required options and the TLS and public URL
// settings. It returns the parsed public URL resolver.
func checkSettings(cfg *settings) (baseurl.Resolver, error) {
	if cfg.authenticator == nil {
		return baseurl.Resolver{}, fmt.Errorf("%w: WithAuthenticator", ErrMissingOption)
	}

	if cfg.namespaceStore == nil {
		return baseurl.Resolver{}, fmt.Errorf("%w: WithNamespaceStore", ErrMissingOption)
	}

	if cfg.secretKeys == nil {
		return baseurl.Resolver{}, fmt.Errorf("%w: WithSecretKeyStore", ErrMissingOption)
	}

	if cfg.workspaceStore == nil {
		return baseurl.Resolver{}, fmt.Errorf("%w: WithWorkspaceStore", ErrMissingOption)
	}

	if err := checkStoreOptions(cfg); err != nil {
		return baseurl.Resolver{}, err
	}

	if err := checkPeriods(cfg); err != nil {
		return baseurl.Resolver{}, err
	}

	if cfg.secretWrapper == nil {
		return baseurl.Resolver{}, fmt.Errorf(
			"%w: WithSecretWrapper: the server needs a key-encryption key to seal OpenTofu state and "+
				"Pulumi secrets (ADR 41)",
			ErrMissingOption,
		)
	}

	if err := checkTLSAndAllowlist(cfg); err != nil {
		return baseurl.Resolver{}, err
	}

	if err := checkAllowedOrigins(cfg.allowedOrigins); err != nil {
		return baseurl.Resolver{}, err
	}

	if err := checkConsoleHeaders(cfg.consoleHeaders, cfg.allowedOrigins); err != nil {
		return baseurl.Resolver{}, err
	}

	base, err := baseurl.Parse(cfg.publicURL)
	if err != nil {
		return baseurl.Resolver{}, fmt.Errorf("public URL: %w", err)
	}

	return base, errors.Join(
		checkIssuerBaseURL(cfg.issuerBaseURL),
		checkRetention(cfg.softDeleteRetention),
		checkJobRetention(cfg.jobRetention),
	)
}

// checkIssuerBaseURL refuses an issuer base URL that does not parse. An empty
// value is valid: the issuer then uses the public URL.
func checkIssuerBaseURL(raw string) error {
	if err := profile.CheckIssuerBaseURL(raw); err != nil {
		return fmt.Errorf("issuer base URL: %w", err)
	}

	return nil
}

// checkStoreOptions refuses a missing state store, blob store or inventory
// reader, and a set of components that names none.
func checkStoreOptions(cfg *settings) error {
	switch {
	case len(cfg.components.Names()) == 0:
		return fmt.Errorf("%w: WithComponents names no component", ErrMissingOption)
	case cfg.stateStore == nil:
		return fmt.Errorf("%w: WithStateStore", ErrMissingOption)
	case cfg.blobStore == nil:
		return fmt.Errorf("%w: WithBlobStore", ErrMissingOption)
	case cfg.inventoryReader == nil:
		return fmt.Errorf("%w: WithInventoryReader", ErrMissingOption)
	default:
		return checkOptionalStores(cfg)
	}
}

// checkOptionalStores refuses a namespace store that keeps no audit entries,
// and declarations of settings that are not valid.
func checkOptionalStores(cfg *settings) error {
	if err := checkAuditStores(cfg); err != nil {
		return err
	}

	return checkSettingDeclarations(cfg)
}

// checkAuditStores refuses a namespace store that keeps no audit entries. A
// refusal, a force-unlock override and a state read each write an entry, so
// the server never runs without the stores for them.
//
// Defense in depth: each adapter, each recorder and each builder below checks
// its store too. This check stays so that the server fails at the start, with
// one clear error, before any of them is built.
func checkAuditStores(cfg *settings) error {
	if _, err := auditStoreOf(cfg); err != nil {
		return err
	}

	if _, err := serverAuditOf(cfg.namespaceStore); err != nil {
		return err
	}

	return nil
}

// newWorkspaceService builds the OpenTofu workspace service, which seals
// state with secrets.
func newWorkspaceService(
	cfg *settings, namespaces *namespace.Service, secrets *secret.Service, manual *manuallock.Service,
) (*workspace.Service, error) {
	var locks workspace.ManualLocks
	if manual != nil {
		locks = manual
	}

	// The workspace service reads through the blob cache when
	// wrapBlobCache set one.
	blobs := cfg.workspaceBlobs
	if blobs == nil {
		blobs = cfg.blobStore
	}

	refusals, err := blobRefusals(cfg, workspace.StateBlobFamily())
	if err != nil {
		return nil, fmt.Errorf("workspace service: %w", err)
	}

	workspaces, err := workspace.NewService(workspace.Config{
		Workspaces:         cfg.workspaceStore,
		States:             cfg.stateStore,
		Blobs:              blobs,
		Namespaces:         namespaces,
		OutOfScope:         refusals,
		Sealer:             secrets,
		Logger:             cfg.logger,
		Now:                cfg.now,
		MaxStateBytes:      cfg.maxStateBytes,
		MaxPendingVersions: 0,
		Retention:          cfg.softDeleteRetention,
		ManualLocks:        locks,
		BlobGuard:          cfg.blobGuard,
	})
	if err != nil {
		return nil, fmt.Errorf("workspace service: %w", err)
	}

	return workspaces, nil
}

// clockOf returns the clock of the server settings, defaulting to time.Now
// like the update service does. The journal replayer stamps the manifest of
// each checkpoint it builds with it.
func clockOf(cfg *settings) func() time.Time {
	if cfg.now != nil {
		return cfg.now
	}

	return time.Now
}

// newUpdateService builds the update service. A nil token service leaves the
// token checker nil, so the service fails closed for any update that an
// access token started.
func newUpdateService(
	cfg *settings, namespaces *namespace.Service, journal *stack.JournalEntries, tokens *accesstoken.Service,
	manual *manuallock.Service,
) (*stack.Updates, error) {
	var checker stack.TokenChecker
	if tokens != nil {
		checker = tokens
	}

	var locks stack.ManualLocks
	if manual != nil {
		locks = manual
	}

	attachBlobGuard(cfg)

	sealer, err := newItemSealer(cfg)
	if err != nil {
		return nil, fmt.Errorf("update service: %w", err)
	}

	updates, err := stack.NewUpdates(stack.UpdatesConfig{
		Updates:            cfg.updateStore,
		Checkpoints:        cfg.checkpointStore,
		Events:             cfg.eventStore,
		Namespaces:         namespaces,
		Stacks:             cfg.stackStore,
		Blobs:              cfg.blobStore,
		Refusals:           cfg.stackRefusals,
		Tokens:             checker,
		Now:                cfg.now,
		Logger:             cfg.logger,
		MaxCheckpointBytes: cfg.maxCheckpointBytes,
		Journal:            journal,
		Replayer:           pulumijournal.NewReplayer(clockOf(cfg), ""),
		ManualLocks:        locks,
		BlobGuard:          cfg.blobGuard,
		Sealer:             sealer,
	})
	if err != nil {
		return nil, fmt.Errorf("update service: %w", err)
	}

	return updates, nil
}

// checkTLSAndAllowlist validates the TLS pair, the IP allowlist switch and the
// installation kind of cfg. It keeps checkSettings under the complexity
// limit.
func checkTLSAndAllowlist(cfg *settings) error {
	if (cfg.tlsCertFile == "") != (cfg.tlsKeyFile == "") {
		missing := "WithTLS certificate file"
		if cfg.tlsCertFile != "" {
			missing = "WithTLS key file"
		}

		return fmt.Errorf("%w: %s", ErrMissingOption, missing)
	}

	allow := cfg.ipAllowlists
	if allow != nil && allow.enabled && allow.store == nil {
		return fmt.Errorf(
			"%w: --ip-allowlists needs a store: pass WithIPAllowlistStore",
			ErrMissingOption,
		)
	}

	return checkKindSettings(cfg)
}

// newServices builds the domain services from validated settings. It reads
// no database. Run runs the start-up checks (such as secret.CheckWrapper)
// with its own context.
//
// The authorizer is the wrapped authorizer of the one chain (newAuthPorts).
// The manual lock service gets it, so its lock, unlock and manage checks pass
// the IP allowlist gate and the namespace service rule too.
func newServices(cfg *settings, tokens *accesstoken.Service, authz identity.Authorizer) (services, error) {
	namespaces, err := namespace.NewService(
		namespace.Config{Store: cfg.namespaceStore, Now: cfg.now, Installation: cfg.installation},
	)
	if err != nil {
		return services{}, fmt.Errorf("namespace service: %w", err)
	}

	if refusalsErr := attachStackRefusals(cfg); refusalsErr != nil {
		return services{}, refusalsErr
	}

	journal, err := journalWriter(cfg)
	if err != nil {
		return services{}, fmt.Errorf("journal writer: %w", err)
	}

	manual, err := newManualLockService(cfg.namespaceStore, cfg.jobStore, cfg.now, authz)
	if err != nil {
		return services{}, err
	}

	updates, err := newUpdateService(cfg, namespaces, journal, tokens, manual)
	if err != nil {
		return services{}, err
	}

	var stackLocks stack.ManualLocks
	if manual != nil {
		stackLocks = manual
	}

	stacks, err := stack.NewService(stack.Config{
		Store: cfg.stackStore, Namespaces: namespaces, Now: cfg.now, State: updates, Retention: cfg.softDeleteRetention,
		ManualLocks: stackLocks,
	})
	if err != nil {
		return services{}, fmt.Errorf("stack service: %w", err)
	}

	secrets, err := secret.NewService(secret.Config{
		Keys:          cfg.secretKeys,
		Wrapper:       cfg.secretWrapper,
		Namespaces:    namespaces,
		Now:           cfg.now,
		MaxBatchBytes: cfg.maxSecretBatchBytes,
	})
	if err != nil {
		return services{}, fmt.Errorf("secret service: %w", err)
	}

	workspaces, err := newWorkspaceService(cfg, namespaces, secrets, manual)
	if err != nil {
		return services{}, err
	}

	return services{
		manualLocks: manual,
		namespaces:  namespaces,
		stacks:      stacks,
		updates:     updates,
		secrets:     secrets,
		workspaces:  workspaces,
		tokens:      nil, tempAdmins: nil, deleted: nil, access: nil, collections: nil, placements: nil, gates: nil,
		stateWrite: stateWriteGates{svc: nil, guard: nil}, issuer: nil,
	}, nil
}

// newSessionPolicyService returns the session policy service over the
// namespace store, or nil when the store keeps no session policy. Both SQL
// adapters keep it.
func newSessionPolicyService(cfg *settings) (*session.Policies, error) {
	store, ok := cfg.namespaceStore.(session.PolicyStore)
	if !ok {
		return nil, nil //nolint:nilnil // A store without a session policy is a valid setup: the routes stay unmounted.
	}

	svc, err := session.NewPolicies(session.PolicyConfig{Store: store, Now: cfg.now})
	if err != nil {
		return nil, fmt.Errorf("session policy service: %w", err)
	}

	return svc, nil
}

// Handler returns the server's root HTTP handler, for embedding in your own server.
// Handler serves requests only. It runs no start-up step: an SDK user who
// serves Handler without Run must call Start first. Until Start has
// succeeded, every route answers 503 in the error shape of its adapter,
// except GET /health/live, which answers 204. GET /health/ready answers 503
// in this time. A forgotten Start so fails at the first request, and not
// open. A backup that a request starts waits for a worker. An SDK user who
// serves Handler without Run must call Work on the service that Backups
// returns, or no instance runs the backup.
func (s *Server) Handler() http.Handler {
	return s.handler
}

// Workspaces returns the workspace service the server was wired with.
func (s *Server) Workspaces() *workspace.Service {
	return s.workspaces
}

// Backups returns the backup service the server was wired with, or nil when
// no backup target is configured.
func (s *Server) Backups() *backup.Service {
	return s.backups
}

// Restores returns the restore service the server was wired with, or nil
// when no backup target is configured.
func (s *Server) Restores() *backup.RestoreService {
	return s.restores
}

// Metrics returns the request, build, process, runtime and readiness
// metrics the middleware records. The metrics route of issue #165 reads
// through it.
func (s *Server) Metrics() *metrics.ServerMetrics {
	return s.observed
}

// Run serves HTTP until ctx is done, then shuts down gracefully. With
// WithTLS it serves HTTPS; without it, it warns when the listen address is
// not loopback and serves plain HTTP anyway. With WithListener it serves on
// the passed listener, which wins over WithListenAddr. Without it, Run binds
// WithListenAddr itself. Either way it logs the bound address in the
// listening line, so a server on :0 reports the port it holds. Run first
// runs the start-up check that refuses a key-encryption key that opens less
// than every wrapped key in the database, so a program that embeds
// the server cannot start with the wrong key. It then marks every running
// backup and every running restore failed: the server stopped during them.
// These are the steps of Start. Run calls Start, and does not repeat the steps
// when Start has succeeded before. When ctx
// is done before these start-up steps end, Run returns nil: the caller asked
// it to stop, and an error caused by that stop is not a failure.
func (s *Server) Run(ctx context.Context) error {
	// The row of this process goes at every return of Run, after the workers
	// stopped, also when a start-up step failed after it wrote the row.
	defer s.unregisterInstance(ctx)

	if err := s.Start(ctx); err != nil {
		return startUpResult(ctx, err)
	}

	tlsCfg, err := s.tlsConfig()
	if err != nil {
		return err
	}

	ln, err := s.listen(ctx)
	if err != nil {
		return err
	}

	if s.durable == nil && s.components.Has(ComponentWorker) {
		// The first run of the inactivity disable ends before the hook and
		// before the server serves a request. A slow database delays the start
		// by firstSweepLimit at most: the hourly runs try again.
		firstCtx, stopFirst := context.WithTimeout(ctx, firstSweepLimit)
		s.sweepOnce(firstCtx)
		stopFirst()
	}

	if s.beforeServe != nil {
		if hookErr := s.beforeServe(ctx); hookErr != nil {
			return errors.Join(fmt.Errorf("before serve: %w", hookErr), closeListener(ln))
		}
	}

	srv := s.newHTTPServer(ln, tlsCfg)

	warnPlainHTTP(ctx, s.logger, srv.Addr, tlsCfg)

	// The chain check and the migration loop stop when Run returns, also
	// after a serve error, and Run returns only after they stopped.
	checkCtx, stopCheck := context.WithCancel(ctx)
	waitCheck := s.workerOnly(func() func() { return s.startAuditChain(checkCtx) })

	sweepCtx, stopSweeps := context.WithCancel(ctx)

	var sweeps sync.WaitGroup

	if err := s.startBackground(sweepCtx, &sweeps); err != nil {
		stopCheck()
		waitCheck()
		stopSweeps()

		return errors.Join(err, closeListener(ln))
	}

	// The shipper stops when Run returns, also after a serve error, and Run
	// returns only after it stopped.
	shipCtx, stopShipper := context.WithCancel(ctx)
	waitShipper := s.workerOnly(func() func() { return s.startAuditShipper(shipCtx) })

	// The backup worker releases its claim when Run returns, so another
	// instance, or this one after a restart, continues a backup in progress.
	backupCtx, stopBackups := context.WithCancel(ctx)
	waitBackups := s.workerOnly(func() func() { return s.startBackupWorker(backupCtx) })

	defer func() {
		s.flushDenialCounts(ctx)
		stopBackups()
		waitBackups()
		stopCheck()
		waitCheck()
		stopShipper()
		waitShipper()
		stopSweeps()
		sweeps.Wait()
	}()

	return s.serveUntilDone(ctx, srv, ln)
}

// newHTTPServer returns the HTTP server that serves the handler on ln.
func (s *Server) newHTTPServer(ln net.Listener, tlsCfg *tls.Config) *http.Server {
	return &http.Server{
		Addr:              ln.Addr().String(),
		Handler:           s.handler,
		ReadHeaderTimeout: readHeaderTimeout,
		IdleTimeout:       idleTimeout,
		MaxHeaderBytes:    maxHeaderBytes,
		TLSConfig:         tlsCfg,
	}
}

// startBackupWorker claims and runs backup, restore and store check operations
// until ctx is done. The returned function waits until the workers stopped. Without a
// backup service there is nothing to run.
func (s *Server) startBackupWorker(ctx context.Context) func() {
	if s.backups == nil && s.storeChecks == nil {
		return func() {}
	}

	var wg sync.WaitGroup

	if s.backups != nil {
		wg.Go(func() { s.backups.Work(ctx) })
	}

	if s.restores != nil {
		wg.Go(func() { s.restores.Work(ctx) })
	}

	if s.storeChecks != nil {
		wg.Go(func() { s.storeChecks.Work(ctx) })
	}

	return wg.Wait
}

// flushDenialCounts writes the counts of refused requests that no entry carries
// yet. It uses a fresh context with a timeout, because ctx is done at shutdown.
func (s *Server) flushDenialCounts(ctx context.Context) {
	if s.flushDenials == nil {
		return
	}

	flushCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), flushTimeout)
	defer cancel()

	s.flushDenials(flushCtx)
}

// denialFlusher returns the function that writes the counts that the shared
// limiter still holds. It uses the audit store of the namespace store, like the
// adapters.
func denialFlusher(
	cfg *settings, svc services, observed *metrics.ServerMetrics,
) (func(context.Context), error) {
	entries, err := auditStoreOf(cfg)
	if err != nil {
		return nil, err
	}

	rec, err := refusal.New(nil, entries, svc.namespaces, cfg.logger, func() { observed.IncAuditAppendFailure() })
	if err != nil {
		return nil, fmt.Errorf("denial flusher: %w", err)
	}

	return rec.WithLimiter(cfg.denialLimiter).Flush, nil
}

// flushTimeout bounds the write of the counts of refused requests at shutdown.
const flushTimeout = 5 * time.Second

// workerOnly runs start when the process runs the worker component, and
// answers the function that waits for what start began. Without the worker
// component it starts nothing and answers a function that returns at once.
func (s *Server) workerOnly(start func() func()) func() {
	if !s.components.Has(ComponentWorker) {
		return func() {}
	}

	return start()
}

// startBackground starts the job schedules and the runner for a process
// that runs the worker component. A process with only the api component
// claims nothing. It makes sure that the schedules exist in the store, so
// that a missing worker shows as a schedule that nobody claims.
func (s *Server) startBackground(ctx context.Context, sweeps *sync.WaitGroup) error {
	if s.watch != nil {
		s.watch.begin(ctx)
	}

	if s.components.Has(ComponentWorker) {
		return s.startSweeps(ctx, sweeps)
	}

	if s.durable == nil {
		return nil
	}

	for _, spec := range s.jobSchedules() {
		if _, err := s.durable.EnsureSchedule(ctx, spec); err != nil {
			return fmt.Errorf("ensure schedule %s: %w", spec.Name, err)
		}
	}

	return nil
}

// sweepEvery is the period of the inactivity disable. The inactivity period
// counts in days by default, so one run each hour is ample.
const sweepEvery = time.Hour

// firstSweepLimit bounds the run of the inactivity disable before the
// server serves.
const firstSweepLimit = time.Minute

// sweepInactive runs the inactivity disable once each inactivityEvery,
// until ctx is done. Run makes the first run before it serves.
func (s *Server) sweepInactive(ctx context.Context) {
	if s.tokens == nil {
		return
	}

	every := s.inactivityEvery
	if every <= 0 {
		every = sweepEvery
	}

	ticker := time.NewTicker(every)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.sweepOnce(ctx)
		}
	}
}

// sweepOnce runs the inactivity disable once and the expiry of temporary
// administrators once, and counts each result. A failed run logs no error
// text, which may hold driver detail, and the next run tries again. A run
// that the server stops (ctx canceled) is not a failure. A run past its
// time limit is. While a change of the chain waits, the disable skips one
// run.
func (s *Server) sweepOnce(ctx context.Context) {
	if s.tokens != nil {
		s.sweepTokensOnce(ctx)
	}

	if s.tempAdmins != nil {
		s.sweepTempAdminsOnce(ctx)
	}
}

// sweepTokensOnce runs the inactivity disable once and counts the result.
// While a change of the chain waits, it skips one run.
func (s *Server) sweepTokensOnce(ctx context.Context) {
	if s.chainChangeWaits(ctx) {
		return
	}

	res, err := s.tokens.SweepInactive(ctx, accesstoken.SweepEntry())

	switch {
	case err != nil && !errors.Is(ctx.Err(), context.Canceled):
		s.observed.ObserveSweep(metrics.SweepFailure)
		s.logger.WarnContext(ctx, "inactivity disable failed")
	case err != nil:
	case len(res.Tokens) > 0 || len(res.Principals) > 0:
		s.observed.ObserveSweep(metrics.SweepSuccess)
		s.logger.InfoContext(ctx, "inactivity disable",
			slog.Int("tokens", len(res.Tokens)), slog.Int("principals", len(res.Principals)))
	default:
		s.observed.ObserveSweep(metrics.SweepSuccess)
	}
}

// sweepTempAdminsOnce disables the temporary administrators whose fixed
// time ended, each with its expiry entry, and counts the result.
func (s *Server) sweepTempAdminsOnce(ctx context.Context) {
	done, err := s.tempAdmins.Sweep(ctx, audit.Entry{
		FenceVersion:  0,
		At:            time.Time{},
		Namespace:     "",
		Subject:       "",
		IDPSubject:    "",
		TokenID:       "",
		Action:        "",
		Object:        "",
		Version:       "",
		ValuePath:     "",
		ValueCount:    nil,
		Changes:       nil,
		SourceIP:      "",
		UserAgent:     "",
		Channel:       audit.ChannelAPI,
		Kind:          "",
		Outcome:       "",
		RequestID:     "",
		ObjectID:      "",
		PrevHash:      "",
		Hash:          "",
		Sequence:      0,
		Reason:        "",
		PrincipalKind: "",
		JobID:         "",
	})

	switch {
	case err != nil && !errors.Is(ctx.Err(), context.Canceled):
		s.observed.ObserveSweep(metrics.SweepFailure)
		s.logger.WarnContext(ctx, "temporary administrator expiry failed")
	case err != nil:
	case len(done) > 0:
		s.observed.ObserveSweep(metrics.SweepSuccess)
		s.logger.InfoContext(ctx, "temporary administrator expiry", slog.Int("principals", len(done)))
	default:
		s.observed.ObserveSweep(metrics.SweepSuccess)
	}
}

// startUpResult answers the result of Run when a start-up step fails with
// err: nil when ctx is done, because the caller asked Run to stop and an
// error caused by that stop is not a failure, else err.
func startUpResult(ctx context.Context, err error) error {
	select {
	case <-ctx.Done():
		return nil
	default:
		return err
	}
}

// tlsConfig loads the TLS configuration when a certificate is set, else
// returns nil without an error for plain HTTP.
func (s *Server) tlsConfig() (*tls.Config, error) {
	if s.tlsCertFile == "" {
		//nolint:nilnil // No certificate without an error means plain HTTP.
		return nil, nil
	}

	cert, err := tls.LoadX509KeyPair(s.tlsCertFile, s.tlsKeyFile)
	if err != nil {
		return nil, fmt.Errorf("load TLS certificate: %w", err)
	}

	return &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{cert}}, nil
}

// listen answers the listener Run serves on: the passed listener when
// WithListener is set, else a new binding of the listen address.
func (s *Server) listen(ctx context.Context) (net.Listener, error) {
	if s.listener != nil {
		return s.listener, nil
	}

	//nolint:exhaustruct_v5 // The server listens with defaults; only the network and address matter.
	lc := &net.ListenConfig{}

	ln, err := lc.Listen(ctx, "tcp", s.listenAddr)
	if err != nil {
		return nil, fmt.Errorf("listen: %w", err)
	}

	return ln, nil
}

// warnPlainHTTP logs a warning when addr serves plain HTTP off loopback.
func warnPlainHTTP(ctx context.Context, logger *slog.Logger, addr string, tlsCfg *tls.Config) {
	if tlsCfg != nil || isLoopbackListenAddr(addr) {
		return
	}

	logger.WarnContext(ctx, "serving plain HTTP on a non-loopback address", slog.String("addr", addr))
}

// serveUntilDone serves srv on ln until ctx is done, then shuts down
// gracefully. It logs the bound address, so a server on :0 reports the
// port it holds.
func (s *Server) serveUntilDone(ctx context.Context, srv *http.Server, ln net.Listener) error {
	boundAddr := ln.Addr().String()
	serveErr := make(chan error, 1)

	go func() {
		s.logger.InfoContext(ctx, "listening", slog.String("addr", boundAddr))

		if srv.TLSConfig != nil {
			// ServeTLS, not Serve on a tls listener, so the server keeps
			// HTTP/2. The certificate is already in TLSConfig, so the file
			// arguments stay empty.
			serveErr <- srv.ServeTLS(ln, "", "")
		} else {
			serveErr <- srv.Serve(ln)
		}
	}()

	select {
	case err := <-serveErr:
		return fmt.Errorf("serve: %w", err)
	case <-ctx.Done():
	}

	if err := s.drain(ctx, serveErr); err != nil {
		return err
	}

	return shutdownServer(ctx, srv, serveErr)
}

// drain makes GET /health/ready answer 503, then keeps serving for
// s.drainPeriod, so a load balancer takes this instance out of its pool
// before the listener closes. It ends early with the serve error when the
// server stops serving during the drain.
func (s *Server) drain(ctx context.Context, serveErr <-chan error) error {
	if s.ready != nil {
		s.ready.drain()
	}

	s.logger.InfoContext(ctx, "draining: readiness answers 503", slog.Duration("period", s.drainPeriod))

	if s.drainPeriod <= 0 {
		return nil
	}

	timer := time.NewTimer(s.drainPeriod)
	defer timer.Stop()

	select {
	case err := <-serveErr:
		return fmt.Errorf("serve: %w", err)
	case <-timer.C:
		return nil
	}
}

// closeListener closes ln after a failed start and answers the close
// error, wrapped, or nil.
func closeListener(ln net.Listener) error {
	if err := ln.Close(); err != nil {
		return fmt.Errorf("close listener: %w", err)
	}

	return nil
}

// shutdownServer shuts down srv after ctx is done and reports the serve
// result: a clean close reads ErrServerClosed.
func shutdownServer(ctx context.Context, srv *http.Server, serveErr <-chan error) error {
	shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), shutdownTimeout)
	defer cancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("shutdown: %w", err)
	}

	if err := <-serveErr; !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("serve: %w", err)
	}

	return nil
}

// startUp runs the start-up steps of Run in order: the key check of ADR 23.
// It writes no backup row: a running backup is a durable operation, and the
// instance that holds its claim, or another one after the lease ends, owns
// the row. The store adapters already name the step in their error text, so
// startUp adds no second prefix.
func (s *Server) startUp(ctx context.Context) error {
	if err := s.claimInstallationKind(ctx); err != nil {
		return err
	}

	if err := s.registerInstance(ctx); err != nil {
		return err
	}

	s.warnDevPrincipals(ctx)

	if err := secret.CheckWrapper(ctx, s.secretKeys, s.secretWrapper); err != nil {
		return err
	}

	if keys, ok := s.secretKeys.(secret.InstallationKeyStore); ok {
		if err := secret.CheckInstallationWrapper(ctx, keys, s.secretWrapper); err != nil {
			return err
		}
	}

	return nil
}

// isLoopbackListenAddr reports whether addr binds only the loopback
// interface. An empty host binds every interface, so it is not loopback. A
// name that is not an IP address counts as loopback only for localhost.
func isLoopbackListenAddr(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil || host == "" {
		return false
	}

	if strings.EqualFold(host, "localhost") {
		return true
	}

	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}

	return ip.IsLoopback()
}

// pulumiDeps are the shared parts that the Pulumi CLI API takes besides the
// domain services: the base URL resolver, the body gate and the session
// policy service. The policy service is nil for a store with no sessions.
type pulumiDeps struct {
	base     baseurl.Resolver
	gate     *bodygate.Gate
	policies *session.Policies
}

// newPulumiHandler builds the Pulumi CLI API from the domain services.
// observed counts failed audit appends: the decrypt audit routes still
// answer 204 when the append fails, so the counter is the alert for lost
// entries.
func newPulumiHandler(
	svc services, cfg *settings, observed *metrics.ServerMetrics, deps pulumiDeps,
) (http.Handler, error) {
	base, gate, policies := deps.base, deps.gate, deps.policies

	entries, err := auditStoreOf(cfg)
	if err != nil {
		return nil, err
	}

	// A nil *session.Policies must not become a non-nil interface.
	var sessionPolicy pulumi.SessionPolicy
	if policies != nil {
		sessionPolicy = policies
	}

	handler, err := pulumi.NewHandler(pulumi.Config{
		Stacks:               svc.stacks,
		Updates:              svc.updates,
		Secrets:              svc.secrets,
		Namespaces:           svc.namespaces,
		Authenticator:        cfg.authenticator,
		Authorizer:           cfg.authorizer,
		Sessions:             cfg.sessionCookies,
		SessionPolicy:        sessionPolicy,
		AddressGate:          allowlistAddressGate(cfg.ipAllowlists),
		Logger:               cfg.logger,
		BaseURL:              base,
		MaxBodyBytesInFlight: cfg.maxBodyBytesInFlight,
		Gate:                 gate,
		Audit:                entries,
		OnAuditFailure: func() {
			observed.IncAuditAppendFailure()
		},
		DenialLimiter:           cfg.denialLimiter,
		CheckpointDeltaCutoff:   cfg.checkpointDeltaCutoff,
		DisablePulumiJournaling: cfg.disablePulumiJournaling,
		StateGuard:              svc.stateWrite.guard,
		Placer:                  newStackPlacer(svc),
	})
	if err != nil {
		return nil, fmt.Errorf("api: %w", err)
	}

	return handler, nil
}

// newAuditHandler builds the tool-neutral audit API from the namespace
// service and the audit store. The store comes from the namespace store:
// every SQL adapter serves all ports from one database connection. A custom
// store that keeps no audit entries fails here with a clear error.
func newAuditHandler(svc services, cfg *settings, onFailure func()) (http.Handler, error) {
	entries, ok := cfg.namespaceStore.(audit.Store)
	if !ok {
		return nil, fmt.Errorf("namespace store keeps no audit entries: %w", ErrMissingOption)
	}

	// A store that keeps no EntryReader answers 501 on the read of one
	// entry; the SQL stores keep one.
	reader, ok := cfg.namespaceStore.(audit.EntryReader)
	if !ok {
		reader = nil
	}

	handler, err := auditapi.NewHandler(auditapi.Config{
		Namespaces:    svc.namespaces,
		Entries:       entries,
		EntryReader:   reader,
		Authenticator: cfg.authenticator,
		Authorizer:    cfg.authorizer,
		Sessions:      cfg.sessionCookies,
		Logger:        cfg.logger,

		OnAuditFailure: onFailure,
		DenialLimiter:  cfg.denialLimiter,
	})
	if err != nil {
		return nil, fmt.Errorf("audit api: %w", err)
	}

	return handler, nil
}

// newContextHandler builds the tool-neutral context API on the namespace store
// when that store also keeps context. A custom store without context gets no
// context API: its routes are not served and the rest of the API works as
// before.
func newContextHandler(svc services, cfg *settings, onFailure func()) (http.Handler, error) {
	neutralAudit, err := auditStoreOf(cfg)
	if err != nil {
		return nil, err
	}

	store, ok := cfg.namespaceStore.(objectcontext.Store)
	if !ok {
		return nil, nil //nolint:nilnil // No context API: the store keeps no context.
	}

	deleted, ok := cfg.inventoryReader.(inventory.DeletedReader)
	if !ok {
		return nil, fmt.Errorf("inventory reader reads no deleted units: %w", ErrMissingOption)
	}

	writer, err := objectcontext.NewWriter(objectcontext.WriterConfig{
		Store:      store,
		Namespaces: cfg.namespaceStore,
		Now:        cfg.now,
	})
	if err != nil {
		return nil, fmt.Errorf("context writer: %w", err)
	}

	handler, err := contextapi.NewHandler(contextapi.Config{
		Namespaces:    svc.namespaces,
		Units:         cfg.inventoryReader,
		Deleted:       deleted,
		Collections:   svc.collections,
		Drift:         cfg.driftStore,
		Entries:       store,
		Writer:        writer,
		Authenticator: cfg.authenticator,
		Authorizer:    cfg.authorizer,
		Sessions:      cfg.sessionCookies,
		Logger:        cfg.logger,

		Audit:          neutralAudit,
		OnAuditFailure: onFailure,
		DenialLimiter:  cfg.denialLimiter,
	})
	if err != nil {
		return nil, fmt.Errorf("context api: %w", err)
	}

	return handler, nil
}

// writeOnceFamilies are the directories, inside the scope of one namespace
// ("namespaces/<namespace ID>/"), of the blob key families that the cache
// keeps: a key in them never holds other bytes, also after a delete. OpenTofu
// state versions have such keys. Pulumi checkpoints and journal batches do
// not: a retry or a restore can write a deleted key again. The list never
// holds the namespace directory itself, which would cache them too.
//
//nolint:gochecknoglobals // A fixed list; Go has no constant slices.
var writeOnceFamilies = []string{"tofu"}

// writeOnceKey reports whether key is in a write-once family of a namespace:
// "namespaces/<namespace ID>/<family>/...", with a valid namespace ID. The
// match is on whole segments, so a family name that only starts the same
// ("tofu-x") or a key of no namespace does not match.
func writeOnceKey(key blob.Key) bool {
	parts := strings.SplitN(key.String(), "/", writeOnceKeyParts)
	if len(parts) != writeOnceKeyParts || parts[0] != "namespaces" || parts[3] == "" {
		return false
	}

	if _, err := namespace.ParseID(parts[1]); err != nil {
		return false
	}

	return slices.Contains(writeOnceFamilies, parts[2])
}

// writeOnceKeyParts is the number of parts that writeOnceKey reads: "namespaces",
// the namespace ID, the family, and the rest of the key.
const writeOnceKeyParts = 4

// wrapBlobCache sets the blob store of the workspace service: the in-process
// cache over the blob store when WithBlobCache sets a size, else the blob
// store itself. Backups, restores, Pulumi updates and audit checkpoints read
// the blob store without the cache.
func wrapBlobCache(cfg *settings) error {
	cfg.workspaceBlobs = cfg.blobStore

	switch {
	case cfg.blobCacheBytes < 0:
		return fmt.Errorf("%w: WithBlobCache needs 0 or more bytes, got %d", ErrInvalidOption, cfg.blobCacheBytes)
	case cfg.blobCacheBytes == 0:
		return nil
	default:
	}

	cached, err := blobcache.New(cfg.blobStore, blobcache.Config{
		MaxBytes:       cfg.blobCacheBytes,
		MaxObjectBytes: 0,
		Cacheable:      writeOnceKey,
	})
	if err != nil {
		return fmt.Errorf("blob cache: %w", err)
	}

	cfg.workspaceBlobs = cached

	return nil
}

// checkRetention refuses a soft-delete retention below
// MinSoftDeleteRetention. Zero means the domain default.
func checkRetention(d time.Duration) error {
	if d != 0 && d < MinSoftDeleteRetention {
		return fmt.Errorf("%w: WithSoftDeleteRetention needs at least %s, got %s",
			ErrInvalidOption, MinSoftDeleteRetention, d)
	}

	return nil
}

// newInventoryHandler builds the tool-neutral inventory API from the
// namespace service, the inventory reader and the restore and purge of each
// tool (ADR 47). Every SQL adapter also reads the deleted units through the
// inventory reader. A custom reader that keeps no deleted units fails here
// with a clear error.
func newInventoryHandler(svc services, cfg *settings, onFailure func()) (http.Handler, error) {
	neutralAudit, err := auditStoreOf(cfg)
	if err != nil {
		return nil, err
	}

	deleted, ok := cfg.inventoryReader.(inventory.DeletedReader)
	if !ok {
		return nil, fmt.Errorf("inventory reader reads no deleted units: %w", ErrMissingOption)
	}

	// A reader with no change log still serves the live list; only a request
	// with the at value fails.
	history := historyOf(cfg.inventoryReader)

	stacks, workspaces := unitLifecycles(svc, cfg)

	// The move check is the escalation check of the access service. A store
	// with no roles and grants gives no checker, so a move is refused there.
	grants, err := newAccessService(cfg)
	if err != nil {
		return nil, err
	}

	var moves inventoryapi.MoveChecker
	if grants != nil {
		moves = grants
	}

	views, err := newViewsConfig(cfg)
	if err != nil {
		return nil, err
	}

	outputSharing, outputs, err := newOutputSharingParts(cfg, svc)
	if err != nil {
		return nil, err
	}

	handler, err := inventoryapi.NewHandler(inventoryapi.Config{
		Views:         views,
		OutputSharing: outputSharing,
		Outputs:       outputs,
		Namespaces:    svc.namespaces,
		Units:         cfg.inventoryReader,
		Deleted:       deleted,
		History:       history,
		Lifecycles: map[string]inventory.Lifecycle{
			pulumiTool:   stacks,
			opentofuTool: workspaces,
		},
		ReadScope:     newDeletedReadScope(cfg.authorizer, svc.collections),
		Collections:   svc.collections,
		Placements:    svc.placements,
		Locks:         svc.manualLocks,
		Moves:         moves,
		Authenticator: cfg.authenticator,
		Authorizer:    cfg.authorizer,
		Sessions:      cfg.sessionCookies,
		PublicURL:     strings.TrimSpace(cfg.publicURL),
		Logger:        cfg.logger,
		Now:           cfg.now,

		Audit:          neutralAudit,
		OnAuditFailure: onFailure,
		DenialLimiter:  cfg.denialLimiter,
		StateGuard:     svc.stateWrite.guard,
	})
	if err != nil {
		return nil, fmt.Errorf("inventory api: %w", err)
	}

	return handler, nil
}

// historyOf returns the change log reader of r, or nil when r keeps none.
func historyOf(r inventory.Reader) inventory.HistoryReader { //nolint:ireturn // The optional port of the reader.
	if history, ok := r.(inventory.HistoryReader); ok {
		return history
	}

	return nil
}

// unitLifecycles returns the restore and purge of each tool. The
// inventory API and the retention sweep share them.
func unitLifecycles(svc services, cfg *settings) (pulumiLifecycle, opentofuLifecycle) {
	blobs := blobPurger{logger: cfg.logger}

	return pulumiLifecycle{stacks: svc.stacks, updates: svc.updates, blobs: blobs},
		opentofuLifecycle{workspaces: svc.workspaces, blobs: blobs}
}

// installationDeps carries the services of the installation API.
type installationDeps struct {
	services services
	backups  *backup.Service
	maint    *maintenance.Service
	observed *metrics.ServerMetrics
	chain    *auditChain
	// policies is the session policy service, nil for a store with no
	// session policy. signIn is the console sign-in, nil with no provider.
	policies *session.Policies
	signIn   *serverapi.SignIn
}

// newInstallationAPI builds the restore service, the store check service, the
// parts of the installation API that need the principals, and the
// tool-neutral installation API, in this order. It is assembly: it calls the
// constructors of the backup area and of the installation area.
func newInstallationAPI(cfg *settings, deps installationDeps) (installationParts, error) {
	none := installationParts{handler: nil, restores: nil, storeChecks: nil}

	restores, err := newRestoreService(restoreInputs{
		settings: cfg.backup, store: cfg.namespaceStore, blobs: cfg.blobStore, watch: cfg.watch,
		backups: deps.backups, maint: deps.maint, chain: deps.chain, key: cfg.secretWrapper,
		installation: cfg.installation.String(), owner: durableOwner(cfg), lease: cfg.operationLease,
		now: cfg.now, logger: cfg.logger,
	})
	if err != nil {
		return none, err
	}

	bindAuditChain(deps.chain, deps.maint, restores, cfg)

	checks, _, err := newStoreCheckService(cfg, deps.maint)
	if err != nil {
		return none, err
	}

	principals, err := newPrincipalService(cfg, deps.services.access)
	if err != nil {
		return none, err
	}

	gates, err := newGateConfigService(cfg, deps.services.namespaces, principals)
	if err != nil {
		return none, err
	}

	gitHost, idem, err := newGitHostParts(cfg, deps)
	if err != nil {
		return none, err
	}

	extras, err := newServerExtras(cfg, deps, principals)
	if err != nil {
		return none, err
	}

	reach, err := reachConfig(cfg)
	if err != nil {
		return none, err
	}

	serverAPI, err := newServerHandler(serverHandlerInputs{
		authenticator: cfg.authenticator, authorizer: cfg.authorizer,
		store: cfg.namespaceStore, namespaces: deps.services.namespaces, access: deps.services.access,
		reach:  reach,
		tokens: deps.services.tokens, deleted: deps.services.deleted, stateWrite: deps.services.stateWrite.effective(),
		backups: deps.backups, restores: restores, checks: checks, maintenance: deps.maint,
		observed: deps.observed, chain: deps.chain, policies: deps.policies, signIn: deps.signIn,
		principals: principals, gates: gates, forgeService: gitHost, idempotency: idem, extras: extras,
		shipper: cfg.auditShipper, workers: cfg.watch, instances: cfg.instanceStore,
		installationKind: cfg.installationKind, denialLimiter: cfg.denialLimiter,
		version: cfg.version, logger: cfg.logger, now: cfg.now,
	})
	if err != nil {
		return none, err
	}

	return installationParts{handler: serverAPI, restores: restores, storeChecks: checks}, nil
}

// installationParts holds what newInstallationAPI builds: the installation API
// and the services that the server runs in its worker.
type installationParts struct {
	handler     http.Handler
	restores    *backup.RestoreService
	storeChecks *storecheck.Service
}

// forgeLinkStore is what a SQL store gives for the links from Git host accounts
// to principals.
type forgeLinkStore interface {
	ForgeLinks() forgelink.Store
}

// newForgeLinkService builds the service of the Git host account links from the
// namespace store, or returns nil when the store keeps no links or no
// principals. Both SQL adapters keep both. A nil service makes the routes
// answer 501.
func newForgeLinkService(st namespace.Store, now func() time.Time) *forgelinksvc.Service {
	store, ok := st.(forgeLinkStore)
	if !ok {
		return nil
	}

	people, ok := st.(forgelink.People)
	if !ok {
		return nil
	}

	return forgelinksvc.NewService(store.ForgeLinks(), people, now, nil)
}

// auditStoreOf returns the audit store of the namespace store. It answers
// ErrMissingOption when the store keeps none, so that no adapter and no
// recorder runs without an audit store. Every refusal and every override
// writes an entry, so the server does not start without this store.
//
//nolint:ireturn // The two SQL adapters share no concrete store type, so the caller keeps the port.
func auditStoreOf(cfg *settings) (audit.Store, error) {
	entries, ok := cfg.namespaceStore.(audit.Store)
	if !ok {
		return nil, fmt.Errorf("%w: WithNamespaceStore store must also implement audit.Store, "+
			"for the entries of refusals and overrides", ErrMissingOption)
	}

	return entries, nil
}

// serverAuditOf returns the audit store of the scope @server. It answers
// ErrMissingOption when the namespace store keeps none. Both SQL adapters keep
// it.
//
//nolint:ireturn // The two SQL adapters share no concrete store type, so the caller keeps the port.
func serverAuditOf(store namespace.Store) (audit.ServerStore, error) {
	entries, ok := store.(audit.ServerStore)
	if !ok {
		return nil, fmt.Errorf("%w: WithNamespaceStore store must also implement audit.ServerStore, "+
			"for the entries of the installation scope", ErrMissingOption)
	}

	return entries, nil
}

// adminSubjects is met by an authenticator that signs in bootstrap
// administrators with no grant, as identity.StaticTokens does.
type adminSubjects interface {
	AdminSubjects() []string
}

// adminSubjectsOf returns the subjects of the bootstrap administrators of
// authn, or none when authn does not name them.
func adminSubjectsOf(authn identity.Authenticator) []string {
	if a, ok := authn.(adminSubjects); ok {
		return a.AdminSubjects()
	}

	return nil
}

// newPrincipalService builds the principal and group service (ADR 52) from
// the namespace store, or returns nil when the store keeps no principals.
// Every SQL adapter serves all ports from one database connection, so the
// namespace store is the principal store too. The access service, when the
// store keeps grants, guards the membership changes and the deletes: each
// one gives or takes the grants of a principal or a group.
func newPrincipalService(cfg *settings, grants *access.Service) (*principal.Service, error) {
	store, ok := cfg.namespaceStore.(principal.Store)
	if !ok {
		return nil, nil //nolint:nilnil // No principal store is a valid setup: the routes answer 501.
	}

	// Without grants, only an administrator changes who holds them (none do).
	var guard principal.Guard = principal.AdminOnlyGuard{}

	var tokens principal.Tokens

	var reach principal.GroupReach

	if grants != nil {
		guard = grants
		tokens = grants
		reach = grants
	}

	// Both SQL adapters keep the temporary administrators, so a sign-in
	// through an identity provider cannot name one.
	var temps principal.TempAdmins
	if records, ok := cfg.namespaceStore.(principal.TempAdmins); ok {
		temps = records
	}

	svc, err := principal.NewService(principal.Config{
		TempAdmins: temps,
		Store:      store, Guard: guard, GroupReach: reach, Namespaces: cfg.namespaceStore,
		Tokens: tokens, Now: nil, NewID: nil,
	})
	if err != nil {
		return nil, fmt.Errorf("principal service: %w", err)
	}

	return svc, nil
}

// withRequestCache gives each request its own access cache, so the
// decisions of one request read the subjects of the caller and the custom
// roles once (access.WithRequestCache). The cache ends with the request. It
// wraps the request log, which reads the route pattern from the request
// that the mux serves, so it must see that same request.
func withRequestCache(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(w, r.WithContext(access.WithRequestCache(r.Context())))
	})
}

// withMisuseReporter puts the reporter of the server on the context of each
// request. A handler that reads a name that the API description does not hold
// logs the error through it, so the error goes to the logger of this server,
// also when one process runs more than one server.
func withMisuseReporter(rep *apidesc.Reporter, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(w, r.WithContext(apidesc.WithReporter(r.Context(), rep)))
	})
}

// accessStores is a namespace store that also keeps roles, grants and
// principals. Both SQL adapters are one.
type accessStores interface {
	access.Store
	access.Callers
	access.Units
	principal.Store
}

// setDefaultAuthorizer sets the default authorizer of the store when no
// option sets one.
func setDefaultAuthorizer(cfg *settings) error {
	if cfg.authorizer != nil {
		return nil
	}

	authz, err := defaultAuthorizer(cfg)
	if err != nil {
		return err
	}

	cfg.authorizer = authz

	return nil
}

// defaultAuthorizer returns an access.Authorizer over the store when it
// keeps roles, grants and principals, else identity.AdminOnly. The
// bootstrap admin tokens are administrators, so they hold every action
// under both.
//
//nolint:ireturn // Two policies with no shared concrete type: the caller keeps the port.
func defaultAuthorizer(cfg *settings) (identity.Authorizer, error) {
	stores, ok := cfg.namespaceStore.(accessStores)
	if !ok {
		return identity.AdminOnly{}, nil
	}

	collections, err := unitCollections(cfg)
	if err != nil {
		return nil, err
	}

	authz, err := access.NewAuthorizer(access.AuthorizerConfig{
		Store: stores, Callers: stores, Collections: collections, Units: stores, Principals: stores,
	})
	if err != nil {
		return nil, fmt.Errorf("authorizer: %w", err)
	}

	return authz, nil
}

// newCollectionService builds the collection service on the namespace store
// when that store also holds collections (ADR 51). A custom store without
// collections gets no collection service, so its collection routes are not
// served and the rest of the API works as before.
func newCollectionService(cfg *settings) (*collection.Service, error) {
	store, ok := cfg.namespaceStore.(collection.Store)
	if !ok {
		return nil, nil //nolint:nilnil // No collection service: the store holds no collections.
	}

	svc, err := collection.NewService(collection.Config{
		Store:      store,
		Namespaces: cfg.namespaceStore,
		Now:        cfg.now,
	})
	if err != nil {
		return nil, fmt.Errorf("collection service: %w", err)
	}

	return svc, nil
}

// addCollectionServices sets the collection service, the gate service and the
// placement rule service. A custom store without placement rules gets no
// placement service, so its placement routes are not served and the rest of the
// API works as before.
func (s *services) addCollectionServices(cfg *settings) error {
	var err error

	s.collections, s.stateWrite, err = newCollectionAndGateServices(cfg, s.namespaces)
	if err != nil {
		return err
	}

	s.gates = s.stateWrite.svc

	if store, ok := cfg.namespaceStore.(collection.PlacementStore); ok {
		s.placements, err = newPlacementService(cfg, store)
	}

	return err
}

// newPlacementService builds the placement rule service on the namespace store
// and its placement port.
func newPlacementService(cfg *settings, store collection.PlacementStore) (*collection.PlacementService, error) {
	svc, err := collection.NewPlacementService(collection.PlacementConfig{
		Store:      store,
		Namespaces: cfg.namespaceStore,
		Now:        cfg.now,
	})
	if err != nil {
		return nil, fmt.Errorf("placement service: %w", err)
	}

	return svc, nil
}

// unitCollections returns the collection port of the authorizer: the
// collection service when the store holds collections, else nil. The
// service is returned as an interface only when it is not nil, so that a
// nil service does not become a non-nil port.
//
//nolint:ireturn // The port of the authorizer: the caller keeps it as the interface.
func unitCollections(cfg *settings) (access.UnitCollections, error) {
	svc, err := newCollectionService(cfg)
	if err != nil {
		return nil, err
	}

	if svc == nil {
		return nil, nil //nolint:nilnil // No collections: a grant on a collection is refused as not found.
	}

	return svc, nil
}

// newAccessService builds the role and grant service (ADR 52) from the
// namespace store, or returns nil when the store keeps no roles and grants.
// The service checks escalation with the grant model, so it uses the
// configured authorizer when that is an access.Authorizer, and its own one
// otherwise.
func newAccessService(cfg *settings) (*access.Service, error) {
	stores, ok := cfg.namespaceStore.(accessStores)
	if !ok {
		return nil, nil //nolint:nilnil // No access store is a valid setup: the routes answer 501.
	}

	authz, isAccess := cfg.authorizer.(*access.Authorizer)
	if !isAccess {
		collections, collErr := unitCollections(cfg)
		if collErr != nil {
			return nil, collErr
		}

		var err error

		authz, err = access.NewAuthorizer(access.AuthorizerConfig{
			Store: stores, Callers: stores, Collections: collections, Units: stores, Principals: stores,
		})
		if err != nil {
			return nil, fmt.Errorf("access authorizer: %w", err)
		}
	}

	reach, reachErr := reachConfig(cfg)
	if reachErr != nil {
		return nil, fmt.Errorf("access service: %w", reachErr)
	}

	var limits access.LimitGroups

	if reader := accessLimitGroups(cfg, stores); reader != nil {
		limits = reader
	}

	svc, err := access.NewService(access.ServiceConfig{
		Store: stores, Directory: stores, Namespaces: cfg.namespaceStore, Authorizer: authz,
		Reach: reach, Now: nil, NewID: nil,
		BootstrapAdmins: adminSubjectsOf(cfg.authenticator), LimitGroups: limits,
	})
	if err != nil {
		return nil, fmt.Errorf("access service: %w", err)
	}

	return svc, nil
}

// newIdentityServices builds the role and grant service and the access
// token service. The token service takes the authorizer of the role and
// grant service for principal:manage-tokens. Either is nil when the store
// keeps none.
func newIdentityServices(cfg *settings) (*access.Service, *accesstoken.Service, error) {
	grants, err := newAccessService(cfg)
	if err != nil {
		return nil, nil, err
	}

	tokens, err := newTokenService(cfg, grants)
	if err != nil {
		return nil, nil, err
	}

	return grants, tokens, nil
}

// newTokenService builds the access token service (ADR 53) from the
// namespace store, or returns nil when the store keeps no access tokens or
// no principals. Every SQL adapter keeps both. The escalation check of a
// personal token for another person is the grants of grants, and the
// authorizer of grants decides principal:manage-tokens for a service
// token. With nil grants, a store that keeps none, only an administrator
// passes either. The reach config decides the reach rule of a token change:
// a store that keeps no grants refuses each such change of a caller that is
// not an administrator.
func newTokenService(cfg *settings, grants *access.Service) (*accesstoken.Service, error) {
	store, ok := cfg.namespaceStore.(accesstoken.Store)
	if !ok {
		return nil, nil //nolint:nilnil // No token store is a valid setup: the routes answer 501.
	}

	principals, ok := cfg.namespaceStore.(accesstoken.PrincipalReader)
	if !ok {
		return nil, nil //nolint:nilnil // Tokens need principals; without them the routes answer 501.
	}

	var authz identity.Authorizer
	if grants != nil {
		authz = grants.Authorizer()
	}

	reach, err := reachConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("access token reach: %w", err)
	}

	svc, err := accesstoken.NewService(accesstoken.Config{
		Store: store, Principals: principals, Freshness: nil, Authorizer: authz, Reach: reach,
		Logger: cfg.logger, Now: cfg.now, NewID: nil, TempAdmins: tempReaderOf(cfg.namespaceStore),
	})
	if err != nil {
		return nil, fmt.Errorf("access token service: %w", err)
	}

	return svc, nil
}

// tempReaderOf returns the temporary administrator reader of the namespace
// store, or nil when the store keeps none. Both SQL adapters keep them.
//
//nolint:ireturn // The two SQL adapters share no concrete store type, so the caller keeps the port.
func tempReaderOf(store namespace.Store) tempadmin.Reader {
	records, ok := store.(tempadmin.Reader)
	if !ok {
		return nil
	}

	return records
}

// requestIDHeader carries the request ID in each response. The server mints
// a new ID for each request and ignores any ID the client sends.
const requestIDHeader = requestid.Header

// unmatchedRoute is the route label for a request that matches no pattern.
// It holds no path, so it cannot leak a capability token.
const unmatchedRoute = "unmatched"

// adapterServer is the adapter label for the installation and audit APIs.
const adapterServer = "server"

// RequestID returns the request ID in ctx, or "" when ctx holds none. The
// middleware sets it for each request, so handlers and later issues can log
// it without reading headers.
func RequestID(ctx context.Context) string {
	return requestid.FromContext(ctx)
}

// newRequestID mints one request ID: 128 bits from crypto/rand as hex. It
// falls back to the time when the reader fails, so it never returns "".
func newRequestID() string {
	return requestid.New()
}

// statusWriter records the status and the body size of one response.
type statusWriter struct {
	http.ResponseWriter

	status int
	bytes  int64
}

func (w *statusWriter) WriteHeader(status int) {
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

func (w *statusWriter) Write(p []byte) (int, error) {
	n, err := w.ResponseWriter.Write(p)
	w.bytes += int64(n)

	return n, err //nolint:wrapcheck // Transparent wrapper: callers must see the original error.
}

// adapterForRequest maps a request to its adapter label from the route
// pattern, or from the path when no pattern matched. It returns pulumi,
// opentofu, inventory or server. It never returns the path itself.
func adapterForRequest(pattern, path string) string {
	route := pattern
	if route == "" {
		route = path
	}

	name := route
	if _, rest, ok := strings.Cut(route, " "); ok {
		name = rest
	}

	switch {
	case name == v1Prefix+"capabilities", strings.HasPrefix(name, v1Prefix+"sign-in/"),
		strings.HasPrefix(name, v1Prefix+"me/"):
		return adapterServer
	case strings.HasPrefix(name, "/api/inventory/"), strings.HasPrefix(name, v1Prefix):
		return "inventory"
	case strings.HasPrefix(name, "/api/audit/"):
		return adapterServer
	case strings.HasPrefix(name, "/api/server/"):
		return adapterServer
	case strings.HasPrefix(name, "/api/v2/"), strings.HasPrefix(name, "/.well-known/"),
		strings.HasPrefix(name, "/tofu/"):
		return "opentofu"
	case strings.HasPrefix(name, "/api/"):
		return "pulumi"
	default:
		return adapterServer
	}
}

// debugRoute reports whether pattern logs at debug. Probes and the metrics
// route log at debug. Every other request logs at info.
func debugRoute(pattern string) bool {
	name := pattern
	if _, rest, ok := strings.Cut(pattern, " "); ok {
		name = rest
	}

	switch name {
	case liveProbePath, "/health/ready", "/health/ready/{component}", metricsPath:
		return true
	default:
		return false
	}
}

// warnTokenExpiry sets accesstoken.ExpiryHeader on the answer when the
// stored access token of caller expires within the warning period (ADR 53,
// decision 11). A bootstrap admin token has no expiry and gets no header.
func warnTokenExpiry(w http.ResponseWriter, caller identity.Principal, now time.Time) {
	if caller.TokenID == "" || accesstoken.WarningFor(caller.TokenExpiresAt, now) == accesstoken.WarningNone {
		return
	}

	w.Header().Set(accesstoken.ExpiryHeader, caller.TokenExpiresAt.UTC().Format(time.RFC3339))
}

// unknownGateSource names the shared budget of requests with no client
// address: the authentication gate counts their failures together.
const unknownGateSource = "unknown"

// gateSource returns the source text that the authentication gate counts
// the failures of addr for: the address in text, or unknownGateSource when
// the address is unknown. The gate maps an IPv6 address to its /64 itself.
func gateSource(addr netip.Addr) string {
	if !addr.IsValid() {
		return unknownGateSource
	}

	return addr.String()
}

// callerForRequest returns the signed-in principal, or false when the
// request carries no valid access token. Lease and upload requests carry no
// access token, so they log with no subject.
func callerForRequest(ctx context.Context, authn identity.Authenticator, r *http.Request) (identity.Principal, bool) {
	var none identity.Principal

	header := r.Header.Get("Authorization")

	var raw string

	switch {
	case strings.HasPrefix(header, "token "):
		raw = strings.TrimPrefix(header, "token ")
	case strings.HasPrefix(header, "Bearer "):
		raw = strings.TrimPrefix(header, "Bearer ")
	default:
		return none, false
	}

	if raw == "" {
		return none, false
	}

	caller, err := authn.AuthenticateToken(ctx, raw)
	if err != nil || caller.Subject == "" {
		return none, false
	}

	return caller, true
}

// withRequestLog resolves the client address, mints a request ID, stores
// both in the context, records the request in the metrics registry, and
// writes one log line for each request. The line holds the method, the
// route, the status, the duration, the size, the ID, the adapter, the client
// address when known and, for a signed-in request, the subject. It holds
// nothing else, in particular no header value.
//
// Go's ServeMux stores the matched pattern in Request.Pattern. The root mux
// matches a prefix such as /api/ and forwards the request to an adapter
// mux. The adapter mux overwrites Pattern with its own method and path
// pattern. The middleware reads Pattern after the next handler returns, so
// it sees the inner pattern. When nothing matches, Pattern stays empty and
// the line uses unmatched. The line names the pattern only, never the path,
// so a capability token in the path cannot leak into the log. The metrics
// use the same pattern and adapter, so no label holds a path value.
// requestAddr resolves the client address of each request and rate-limits
// the missing-header warning.
type requestAddr struct {
	resolver *clientip.Resolver
	warns    *warnLimiter
}

func withRequestLog(
	next http.Handler,
	addr requestAddr,
	logger *slog.Logger,
	authn identity.Authenticator,
	observed *metrics.ServerMetrics,
) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resolved := addr.resolver.Resolve(r)
		ctx := clientip.WithAddr(r.Context(), resolved)

		if addr.warns != nil {
			addr.warns.warnMissingHeader(ctx, r, resolved, addr.resolver, logger)
		}

		id := newRequestID()
		// A direct assignment keeps the header name of ADR 32. The key is already
		// in canonical form, so http.Header reads it with Get.
		w.Header()[requestIDHeader] = []string{id}

		ctx = requestid.WithContext(ctx, id)
		ctx = authgate.WithRequest(ctx, gateSource(resolved))
		// The request log and the adapter resolve the token separately. The
		// scope keeps one sign-in entry of a temporary administrator for
		// the request.
		ctx = tempadmin.WithSignInScope(ctx)

		r = r.WithContext(ctx)

		caller, signedIn := callerForRequest(ctx, authn, r)
		if signedIn {
			warnTokenExpiry(w, caller, time.Now())
		}

		// The route pattern is not known until the handler returns, so the
		// in-flight gauge tracks the adapter from the path only. The gauge
		// holds no route label for the same reason.
		startAdapter := adapterForRequest("", r.URL.Path)
		if observed != nil {
			observed.IncInFlight(startAdapter)
			defer observed.DecInFlight(startAdapter)
		}

		start := time.Now()
		wrapped := &statusWriter{ResponseWriter: w, status: http.StatusOK, bytes: 0}

		next.ServeHTTP(wrapped, r)

		elapsed := time.Since(start)
		route := logRoute(r)
		adapter := adapterForRequest(r.Pattern, r.URL.Path)

		if observed != nil {
			observed.ObserveRequest(adapter, route, r.Method, wrapped.status, elapsed)
		}

		attrs := []slog.Attr{
			slog.String("method", r.Method),
			slog.String("route", route),
			slog.Int64("status", int64(wrapped.status)),
			slog.Int64("duration_ms", elapsed.Milliseconds()),
			slog.Int64("bytes", wrapped.bytes),
			slog.String("request_id", id),
			slog.String("adapter", adapter),
		}

		attrs = appendClientIP(attrs, resolved)

		if signedIn {
			attrs = append(attrs, slog.String("subject", caller.Subject))
		}

		level := slog.LevelInfo
		if debugRoute(r.Pattern) {
			level = slog.LevelDebug
		}

		logger.LogAttrs(ctx, level, "request", attrs...)
	})
}

// logRoute answers the route label of the request log line: the matched
// pattern, or unmatched when nothing matched or the adapter found no route.
// A root prefix without a method means the adapter found no route, so it
// logs as unmatched and the line never names a bare prefix.
func logRoute(r *http.Request) string {
	route := r.Pattern
	if route == "" || !strings.Contains(route, " ") {
		return unmatchedRoute
	}

	return route
}

// appendClientIP adds the client address to attrs when it is known. It adds
// the resolved address only, never a header value.
func appendClientIP(attrs []slog.Attr, addr netip.Addr) []slog.Attr {
	if !addr.IsValid() {
		return attrs
	}

	return append(attrs, slog.String("client_ip", addr.String()))
}

// v1Prefix is the prefix of the tool-neutral REST API.
const v1Prefix = "/api/dustpan/v1/"

// patternLister is a handler that lists the ServeMux patterns it serves.
type patternLister interface {
	RegisteredPatterns() []string
}

// rootMux is the root http.ServeMux. It records every pattern that is
// registered on it, so that a test can classify each route the root mux
// serves, not only the routes of the adapters. Register every route through
// Handle, HandleFunc or handleMounted: the mux field is private to this type.
type rootMux struct {
	mux *http.ServeMux
	// patterns holds the patterns that code registered directly.
	patterns []string
	// mounted holds the paths that mountV1 registered from the patterns of an
	// adapter. The adapter patterns already cover them.
	mounted []string
}

// newRootMux returns a rootMux ready to register routes.
func newRootMux() *rootMux {
	return &rootMux{mux: http.NewServeMux(), patterns: nil, mounted: nil}
}

// Handle registers handler for pattern and records the pattern.
func (m *rootMux) Handle(pattern string, handler http.Handler) {
	m.patterns = append(m.patterns, pattern)
	m.mux.Handle(pattern, handler)
}

// HandleFunc registers handler for pattern and records the pattern.
func (m *rootMux) HandleFunc(pattern string, handler func(http.ResponseWriter, *http.Request)) {
	m.patterns = append(m.patterns, pattern)
	m.mux.HandleFunc(pattern, handler)
}

// ServeHTTP implements http.Handler.
func (m *rootMux) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	m.mux.ServeHTTP(w, r)
}

// handleMounted registers handler for the path of a route that an adapter
// lists, and records the path apart from the direct patterns.
func (m *rootMux) handleMounted(path string, handler http.Handler) {
	m.mounted = append(m.mounted, path)
	m.mux.Handle(path, handler)
}

// v1NotFound answers 404 for a path under v1Prefix that no handler serves, in
// the error shape of the tool-neutral API.
func v1NotFound(w http.ResponseWriter, _ *http.Request) {
	writeRouteError(
		w, http.StatusNotFound, "not found: no route at this path", routeAnswer(errcode.ServerRouteNotFound),
	)
}

// mountV1 registers each path of h under v1Prefix on mux, with wrap around
// h. A path serves every method that h registers for it, and h answers the
// methods it does not serve, with the 405 of the tool-neutral API. A nil h or
// a handler that lists no patterns registers nothing.
//
// The pattern check sees only the patterns that the handlers list and the
// patterns of the root mux. A route that a wrapper (the wrap argument or a
// middleware before the mux) answers by itself is outside what the check sees.
func mountV1(mux *rootMux, h http.Handler, wrap func(http.Handler) http.Handler) {
	lister, ok := h.(patternLister)
	if !ok {
		return
	}

	wrapped := wrap(withJSONMethodNotAllowed(h))
	seen := map[string]struct{}{}

	for _, pattern := range lister.RegisteredPatterns() {
		_, path, found := strings.Cut(pattern, " ")
		if !found || !strings.HasPrefix(path, v1Prefix) {
			continue
		}

		if _, dup := seen[path]; dup {
			continue
		}

		seen[path] = struct{}{}

		mux.handleMounted(path, wrapped)
	}
}

// mountAPI mounts the handlers of the api component on mux: the tool APIs,
// the installation API, the runner protocol, service discovery and the
// console files.
func mountAPI(mux *rootMux, a adapters, cfg *settings) error {
	// /api/inventory/, /api/v2/ and /api/server/ are more specific than
	// /api/, so the inventory restore, purge and move routes, the OpenTofu
	// routes and the installation routes reach their own adapters while every
	// other /api/ route reaches Pulumi. In maintenance mode the tool adapters answer 503
	// with Retry-After; the installation API stays available, so the
	// maintenance routes that end the restore keep serving.
	mux.Handle(
		"/api/inventory/",
		withCORS(
			withMaintenance(withJSONMethodNotAllowed(a.inventory), a.maintenance, shapeInventory, cfg.logger),
			cfg.allowedOrigins,
		),
	)
	// The audit API is read-only, so it stays available in maintenance mode
	// like the installation API: an operator reads the log while the
	// installation is down for a restore.
	mux.Handle("/api/audit/", withCORS(withJSONMethodNotAllowed(a.audit), cfg.allowedOrigins))
	// The runner protocol is outside /api/. A runner sends no cookie and no
	// Origin header, so it passes no CORS gate. The management API of the
	// runners is more specific than /api/, so it reaches its own adapter.
	if a.runners != nil {
		mux.Handle("/runner/v1/", withMaintenance(a.runners, a.maintenance, shapeRunner, cfg.logger))
		mux.Handle(
			"/api/runners/",
			withCORS(
				withRunnerMaintenance(withJSONMethodNotAllowed(a.runners), a.maintenance, shapeInventory, cfg.logger),
				cfg.allowedOrigins,
			),
		)
	}
	// The adapters below answer in the shape of the inventory API, with CORS
	// and the maintenance gate.
	wrapV1 := func(h http.Handler) http.Handler {
		return withCORS(withMaintenance(h, a.maintenance, shapeInventory, cfg.logger), cfg.allowedOrigins)
	}
	// The paths of the inventory and the context API under /api/dustpan/v1/
	// reach their own handlers by exact path, so another area can take the
	// rest of the prefix. The tool adapters are refused in maintenance mode.
	for _, h := range []http.Handler{
		a.inventory, a.context, a.profiles, a.issuer, a.sinks, a.registry, a.jobs, a.triggers, a.unitSettings,
		a.drift, a.sourceFilter, a.trustRules, a.selfService,
	} {
		mountV1(mux, h, wrapV1)
	}
	// The capabilities document and the dev sign-in belong to the
	// installation API, so they stay available in maintenance mode like the
	// rest of it.
	mountV1(mux, a.server, func(h http.Handler) http.Handler {
		return withCORS(h, cfg.allowedOrigins)
	})
	// A path under the prefix that no handler serves answers 404 in the shape
	// of the tool-neutral API. Without this row, the console files or the
	// Pulumi adapter would answer for it.
	mux.Handle(v1Prefix, withCORS(http.HandlerFunc(v1NotFound), cfg.allowedOrigins))
	mux.Handle(
		"/api/v2/",
		withCORS(withMaintenance(a.tfe, a.maintenance, shapeTFE, cfg.logger), cfg.allowedOrigins),
	)
	// The OpenTofu http backend (ADR 49) answers tool calls under its own
	// prefix. It is refused in maintenance mode like the other tool adapters.
	mux.Handle("/tofu/state/", withMaintenance(a.tofuHTTP, a.maintenance, shapeHTTPBackend, cfg.logger))
	mux.Handle("/api/server/", withCORS(withJSONMethodNotAllowed(a.server), cfg.allowedOrigins))
	mux.Handle("/api/", withCORS(withMaintenance(a.pulumi, a.maintenance, shapePulumi, cfg.logger), cfg.allowedOrigins))
	// Service discovery lives outside /api/v2/, so the root mux serves it
	// through the TFE adapter. It stays available in maintenance mode: the
	// CLI reads only the service addresses from it, and OpenTofu shows only
	// the discovery status, never the refusal body. The lock, state and
	// version endpoints under /api/v2/ pass the maintenance gate, so the
	// CLI still meets the 503 refusal that names maintenance.
	mux.Handle("GET /.well-known/terraform.json", a.tfe)
	// The protocol routes of the OIDC issuers live outside the versioned API:
	// their path is part of the issuer URL that clouds hold. They answer public
	// data only, and they stay available in maintenance mode like the
	// discovery document of the TFE adapter.
	if a.issuer != nil {
		mux.Handle("/oidc/", withJSONMethodNotAllowed(a.issuer))
	}
	// The provider registry protocol of OpenTofu lives outside /api/, at the
	// path that service discovery names. The handler answers 404 for every path
	// of it when no registry is set. It is refused in maintenance mode like the
	// other tool routes.
	mux.Handle(
		"/v1/providers/",
		withMaintenance(a.tfe, a.maintenance, shapeTFE, cfg.logger),
	)
	// The module registry protocol lives at the path that service discovery
	// names as modules.v1, and follows the same rules.
	mux.Handle(
		"/v1/modules/",
		withMaintenance(a.tfe, a.maintenance, shapeTFE, cfg.logger),
	)

	if cfg.consoleDir != "" {
		headers := cfg.consoleHeaders.resolve(cfg.allowedOrigins)
		if cfg.tlsCertFile != "" {
			headers = append(headers, cfg.consoleHeaders.hsts()...)
		}

		console, err := consoleHandler(cfg.consoleDir, headers)
		if err != nil {
			return err
		}

		mux.Handle("/", console)
	}

	return nil
}

// buildMux builds the root mux: the handlers of the api component, when the
// process runs it, and the probes and the metrics route.
func buildMux(a adapters, cfg *settings) (*rootMux, error) {
	mux := newRootMux()
	// Defense in depth: gateComponents answers 404 for a process without the
	// api component, and this mount is a second layer that does not rely on
	// it. A process without the api component mounts no API handler, so the
	// mux holds only the probes and the metrics route, and a request that gets
	// past the gate, or a gate that a later change removes, still reaches
	// nothing.
	if cfg.components.Has(ComponentAPI) {
		if err := mountAPI(mux, a, cfg); err != nil {
			return nil, err
		}
	} else {
		mux.Handle("GET "+metricsPath, withCORS(a.server, cfg.allowedOrigins))
	}

	// The probes have their own prefix, so that ingress controllers that serve
	// /healthz themselves cannot answer for this server (ADR 34). Liveness
	// checks nothing; readiness pings the store and the blob store. Both stay
	// public.
	mux.HandleFunc("GET /health/live", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	mux.Handle("GET /health/ready", a.ready)
	mux.HandleFunc("GET /health/ready/{component}", a.ready.ServeOne)

	return mux, nil
}

// routeHandlers holds the handler that the server serves, and the same handler
// before the component gate. The tests send hostile requests to the second, to
// show that the mux of a process without the api component holds no API route
// whatever the gate does.
type routeHandlers struct {
	served  http.Handler
	ungated http.Handler
	// root is the root mux, which records its patterns.
	root *rootMux
}

// routes builds the handlers of the server.
func routes(a adapters, cfg *settings, observed *metrics.ServerMetrics) (routeHandlers, error) {
	mux, err := buildMux(a, cfg)
	if err != nil {
		return routeHandlers{}, err
	}

	resolver, err := newClientIPResolver(cfg.proxies)
	if err != nil {
		return routeHandlers{}, err
	}

	addr := requestAddr{resolver: resolver, warns: newWarnLimiter(cfg.now)}

	gated := withAuditChainGate(mux, a.chain, cfg.logger)

	return routeHandlers{
		served: withMisuseReporter(apidesc.NewReporter(cfg.logger), withRequestCache(withRequestLog(
			gateComponents(gated, cfg.components), addr, cfg.logger, cfg.authenticator, observed,
		))),
		ungated: gated,
		root:    mux,
	}, nil
}
