package forge

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strconv"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// ErrMissingDependency reports a Config without a required dependency.
var ErrMissingDependency = errors.New("missing dependency")

// Reasons that the service counts when it refuses to open a connection. The
// wiring maps them to the label of the metric
// git_host_connection_refused_total.
const (
	// ReasonInstallationNotAssigned is a connection whose installation the map
	// does not assign to the namespace of the connection, or a map that the
	// service cannot read.
	ReasonInstallationNotAssigned = "installation-not-assigned"
	// ReasonAddressChanged is a stored credential whose address digest differs
	// from the address and the parameters that the registration holds now.
	ReasonAddressChanged = "address-changed"
	// ReasonNoRegistration is a connection of the installation-level app while
	// the installation has no registration, or one that fails its checks.
	ReasonNoRegistration = "no-registration"
	// ReasonKeyNotOpened is a stored key that does not open with the binding of
	// the address that the registration holds now.
	ReasonKeyNotOpened = "key-not-opened"
	// ReasonAddressRefused is an API address that the outbound address rules
	// refuse.
	ReasonAddressRefused = "address-refused"
)

// Namespaces reads the namespace that a write fences. namespace.Store
// implements it.
type Namespaces interface {
	GetNamespace(ctx context.Context, name namespace.Name) (namespace.Namespace, error)
}

// SecretSealer seals and opens the values of installation scopes. Each value
// is bound to the binding that the caller gives: the AEAD authenticates it as
// associated data, so a value opens only with the binding that sealed it.
// *secret.InstallationSecrets implements it. OpenBound returns an error for a
// value that does not open.
type SecretSealer interface {
	SealBound(ctx context.Context, id string, binding, plaintext []byte) ([]byte, error)
	OpenBound(ctx context.Context, id string, binding, ciphertext []byte) ([]byte, error)
}

// Config holds the dependencies of a Service. Store, Namespaces, Secrets,
// Audit, CheckURL and at least one opener are required.
type Config struct {
	Store      Store
	Namespaces Namespaces
	Secrets    SecretSealer
	// Audit appends the entries that are not part of a write of the store: a
	// refused Open and a Check. Each entry carries the fence of its namespace.
	Audit audit.Store
	// Openers are the adapters of the Git hosts that the server serves. The
	// service picks the adapter by the kind of the connection.
	Openers []Opener
	// CheckURL applies the outbound address rules to an API address when a
	// person sets it, and at each Open. It returns an error for an address
	// that the rules refuse. The adapter checks the resolved address of each
	// connection again.
	CheckURL func(raw string) error
	// OnAuditFailure, when set, runs once for each audit entry that the service
	// appends outside a write and that the store refuses. The wiring counts it
	// on a metric. The entry never changes the answer of the call.
	OnAuditFailure func()
	// OnRefused, when set, runs once for each connection that Open refuses,
	// with one of the Reason constants. The wiring counts it on a metric.
	OnRefused func(reason string)
	// CheckLimits bounds the Check calls. Nil means DefaultCheckLimits.
	CheckLimits *CheckLimits
	// Logger receives the warnings of the service. Nil means a logger that
	// discards.
	Logger *slog.Logger
	// Now is the clock. Nil means time.Now.
	Now func() time.Time
}

// Service implements the use cases of the Git host connections: the
// registration of the installation-level GitHub App and the map of its
// installations (the operator), and the connections of a namespace with their
// Check (a namespace administrator). It seals the key and the event secret of
// the registration, adds the fence, the time, the IDs and the audit entry to a
// write, and opens a connection only after it checked the map and the digest of
// the address.
type Service struct {
	store      Store
	namespaces Namespaces
	secrets    SecretSealer
	audit      audit.Store
	openers    map[Kind]Opener
	checkURL   func(raw string) error
	onRefused  func(reason string)
	onAuditErr func()
	checks     *checkLimiter
	now        func() time.Time
	logger     *slog.Logger
}

// NewService returns a Service. It returns ErrMissingDependency when a
// required dependency is missing.
func NewService(cfg Config) (*Service, error) {
	switch {
	case cfg.Store == nil:
		return nil, fmt.Errorf("%w: Git host store", ErrMissingDependency)
	case cfg.Namespaces == nil:
		return nil, fmt.Errorf("%w: namespaces", ErrMissingDependency)
	case cfg.Secrets == nil:
		return nil, fmt.Errorf("%w: installation secrets", ErrMissingDependency)
	case cfg.Audit == nil:
		return nil, fmt.Errorf("%w: audit store", ErrMissingDependency)
	case cfg.CheckURL == nil:
		return nil, fmt.Errorf("%w: outbound address check", ErrMissingDependency)
	case len(cfg.Openers) == 0:
		return nil, fmt.Errorf("%w: Git host adapter", ErrMissingDependency)
	}

	openers := make(map[Kind]Opener, len(cfg.Openers))
	for _, o := range cfg.Openers {
		openers[o.Kind()] = o
	}

	now := cfg.Now
	if now == nil {
		now = time.Now
	}

	limits := DefaultCheckLimits()
	if cfg.CheckLimits != nil {
		limits = *cfg.CheckLimits
	}

	if err := limits.Validate(); err != nil {
		return nil, fmt.Errorf("check limits: %w", err)
	}

	logger := cfg.Logger
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}

	return &Service{
		checks: newCheckLimiter(limits),
		store:  cfg.Store, namespaces: cfg.Namespaces, secrets: cfg.Secrets, audit: cfg.Audit, openers: openers,
		checkURL: cfg.CheckURL, onRefused: cfg.OnRefused, onAuditErr: cfg.OnAuditFailure, now: now,
		logger: logger,
	}, nil
}

// ConnectionInput is a new connection. Only the installation-level GitHub App
// is supported, so the request names its kind, its auth kind and the
// installation. It holds no key, no event secret, no API address and no app
// ID: they belong to the registration of the operator.
type ConnectionInput struct {
	Kind           Kind
	Auth           AuthKind
	InstallationID string
	Push           bool
}

// ConnectionView is a connection with the values that it takes from the
// registration. AppID, BaseURL and HookID are empty when the installation has
// no registration.
type ConnectionView struct {
	Record  ConnectionRecord
	AppID   string
	BaseURL string
	HookID  string
}

// ConsumerNeeds names the capabilities that a consumer needs and the connection
// does not have.
type ConsumerNeeds struct {
	Consumer Consumer
	Missing  Capabilities
}

// CheckResult is the answer of Check: the account that the credential reaches,
// what the connection can do, and what each consumer misses.
type CheckResult struct {
	Account      Account
	Capabilities Capabilities
	Needs        []ConsumerNeeds
}

// CreateConnection makes a connection in ns for an installation that the
// operator assigned to ns. It returns ErrInvalid, ErrNoRegistration,
// ErrInstallationNotAssigned, ErrConnectionExists, ErrConnectionLimit,
// namespace.ErrNotFound and namespace.ErrFenced. e is the entry of the caller:
// the service completes it.
func (s *Service) CreateConnection(
	ctx context.Context, e audit.Entry, ns namespace.Name, in ConnectionInput,
) (ConnectionView, error) {
	if err := s.validateInput(in); err != nil {
		return ConnectionView{}, err
	}

	n, err := s.namespaces.GetNamespace(ctx, ns)
	if err != nil {
		return ConnectionView{}, fmt.Errorf("read namespace %s: %w", ns, err)
	}

	reg, err := s.store.GetRegistration(ctx, in.Kind)
	if err != nil {
		return ConnectionView{}, fmt.Errorf("get registration: %w", err)
	}

	if assignErr := s.requireAssigned(ctx, in.InstallationID, ns); assignErr != nil {
		return ConnectionView{}, assignErr
	}

	at := s.now().UTC()
	rec := ConnectionRecord{
		CreatedAt: at, UpdatedAt: at, Namespace: ns, ID: NewConnectionID(), Kind: in.Kind, Auth: in.Auth,
		Scope: AppScopeInstallation, InstallationID: in.InstallationID, Version: 1, Push: in.Push, Default: false,
	}

	// Defense in depth: the store checks the record again. This check stays so
	// that a refused connection costs no write, and so that a store that skips
	// its check cannot take an invalid record from this service.
	if validErr := rec.Validate(); validErr != nil {
		return ConnectionView{}, validErr
	}

	entry := connectionEntry(e, at, ns, rec.ID, "create")
	entry.Changes = append(entry.Changes,
		change("kind", string(rec.Kind)), change("auth", string(rec.Auth)),
		change("installationID", rec.InstallationID), change("push", strconv.FormatBool(rec.Push)),
	)

	stored, err := s.store.CreateConnection(ctx, rec, n.Fence(), entry)
	if err != nil {
		return ConnectionView{}, fmt.Errorf("create connection: %w", err)
	}

	return viewOf(stored, &reg), nil
}

// GetConnection returns the connection id of ns, or ErrConnectionNotFound.
func (s *Service) GetConnection(ctx context.Context, ns namespace.Name, id string) (ConnectionView, error) {
	if !ValidConnectionID(id) {
		return ConnectionView{}, ErrConnectionNotFound
	}

	rec, err := s.store.GetConnection(ctx, ns, id)
	if err != nil {
		return ConnectionView{}, fmt.Errorf("get connection: %w", err)
	}

	return s.view(ctx, rec)
}

// ListConnections returns the connections of ns, oldest first.
func (s *Service) ListConnections(ctx context.Context, ns namespace.Name) ([]ConnectionView, error) {
	recs, err := s.store.ListConnections(ctx, ns)
	if err != nil {
		return nil, fmt.Errorf("list connections: %w", err)
	}

	reg, regErr := s.store.GetRegistration(ctx, KindGitHub)
	if regErr != nil && !errors.Is(regErr, ErrNoRegistration) {
		return nil, fmt.Errorf("get registration: %w", regErr)
	}

	out := make([]ConnectionView, 0, len(recs))

	for i := range recs {
		out = append(out, viewOf(recs[i], regPointer(reg, regErr)))
	}

	return out, nil
}

// DeleteConnection removes the connection id of ns. A version above 0 is the
// version that the caller read. It returns ErrConnectionNotFound,
// ErrDefaultConnection, ErrVersionConflict and namespace.ErrFenced.
func (s *Service) DeleteConnection(
	ctx context.Context, e audit.Entry, ns namespace.Name, id string, ifVersion int64,
) error {
	if !ValidConnectionID(id) {
		return ErrConnectionNotFound
	}

	n, err := s.namespaces.GetNamespace(ctx, ns)
	if err != nil {
		return fmt.Errorf("read namespace %s: %w", ns, err)
	}

	entry := connectionEntry(e, s.now().UTC(), ns, id, "delete")

	if delErr := s.store.DeleteConnection(ctx, ns, id, ifVersion, n.Fence(), entry); delErr != nil {
		return fmt.Errorf("delete connection: %w", delErr)
	}

	return nil
}

// MakeDefault makes the connection id the default of ns. A version above 0 is
// the version that the caller read.
func (s *Service) MakeDefault(
	ctx context.Context, e audit.Entry, ns namespace.Name, id string, ifVersion int64,
) (ConnectionView, error) {
	if !ValidConnectionID(id) {
		return ConnectionView{}, ErrConnectionNotFound
	}

	n, err := s.namespaces.GetNamespace(ctx, ns)
	if err != nil {
		return ConnectionView{}, fmt.Errorf("read namespace %s: %w", ns, err)
	}

	entry := connectionEntry(e, s.now().UTC(), ns, id, "make-default")

	rec, err := s.store.MakeDefault(ctx, ns, id, ifVersion, n.Fence(), entry)
	if err != nil {
		return ConnectionView{}, fmt.Errorf("make default: %w", err)
	}

	return s.view(ctx, rec)
}

// Open returns the Forge of the connection id of ns. For a connection of the
// installation-level app it reads the map at each call, and refuses with an
// error that wraps ErrAuth when the map does not assign the installation to
// ns or cannot be read. The refusal is audited and counted, and no older
// credential applies. The API address and the app ID come from the registration
// and never from the connection. e is the entry of the caller for the audit
// of a refusal.
//
//nolint:ireturn // The port is the interface: the adapter type stays hidden.
func (s *Service) Open(ctx context.Context, e audit.Entry, ns namespace.Name, id string) (Forge, error) {
	rec, err := s.connectionRecord(ctx, ns, id)
	if err != nil {
		return nil, err
	}

	// Defense in depth: the store checks a record when it reads it. This check
	// stays so that a store that serves an invalid record cannot make Open send
	// a credential for it.
	if validErr := rec.Validate(); validErr != nil || rec.Namespace != ns || rec.ID != id {
		return nil, fmt.Errorf("%w: the stored connection is not valid", ErrConnectionNotFound)
	}

	if assignErr := s.requireAssigned(ctx, rec.InstallationID, ns); assignErr != nil {
		s.refuse(ctx, e, rec, ReasonInstallationNotAssigned)

		return nil, fmt.Errorf("%w: %w", ErrAuth, ErrInstallationNotAssigned)
	}

	reg, err := s.store.GetRegistration(ctx, rec.Kind)
	if err != nil {
		s.refuse(ctx, e, rec, ReasonNoRegistration)

		return nil, fmt.Errorf("%w: %w", ErrAuth, ErrNoRegistration)
	}

	// Defense in depth: the store checks the digest when it reads the
	// registration. This check stays so that a store that skips it, or a code
	// path that changed the address without a new key, cannot send the key to
	// the new address. The routes of a namespace and the adapter do not rely on
	// it, and it does not rely on them.
	if reg.AddressDigest != AddressDigest(reg.Kind, reg.BaseURL, reg.AppID) {
		s.refuse(ctx, e, rec, ReasonAddressChanged)

		return nil, fmt.Errorf("%w: %w", ErrAuth, ErrAddressChanged)
	}

	return s.openWith(ctx, e, rec, &reg)
}

// Check opens the connection id of ns, calls the Git host and returns the
// account, the capabilities and what each consumer misses. It returns the
// errors of Open and of the adapter: ErrAuth for a credential that the Git host
// refuses, and ErrUnavailable. It returns ErrRateLimited, with no call to the
// Git host, when the connection or its installation was checked too often.
// The adapter refuses a call over the budget of the signed calls of the app
// (see AppBudget) with ErrUnavailable. The result is audited, best effort.
func (s *Service) Check(ctx context.Context, e audit.Entry, ns namespace.Name, id string) (CheckResult, error) {
	rec, err := s.connectionRecord(ctx, ns, id)
	if err != nil {
		return CheckResult{}, err
	}

	if wait, ok := s.checks.admit(ns.String()+"/"+id, string(rec.Kind)+"/"+rec.InstallationID, s.now()); !ok {
		return CheckResult{}, &RateLimitedError{After: wait}
	}

	f, err := s.Open(ctx, e, ns, id)
	if err != nil {
		return CheckResult{}, err
	}

	account, checkErr := f.Check(ctx)
	s.recordCheck(ctx, e, ns, id, checkErr)

	if checkErr != nil {
		s.warnAppBudget(ctx, ns, id, checkErr)

		return CheckResult{}, fmt.Errorf("check connection: %w", checkErr)
	}

	caps := slices.Clone(f.Capabilities())
	needs := make([]ConsumerNeeds, 0, len(Consumers()))

	for _, c := range Consumers() {
		needs = append(needs, ConsumerNeeds{Consumer: c, Missing: caps.Missing(Needs(c))})
	}

	return CheckResult{Account: account, Capabilities: caps, Needs: needs}, nil
}

// warnAppBudget writes one warning when err reports that the app used its
// budget of signed calls. The warning names the namespace, the connection and
// the wait that the error holds.
func (s *Service) warnAppBudget(ctx context.Context, ns namespace.Name, id string, err error) {
	if !errors.Is(err, ErrAppBudget) {
		return
	}

	attrs := []any{slog.String("namespace", ns.String()), slog.String("connection", id)}
	if wait, ok := RetryAfter(err); ok {
		attrs = append(attrs, slog.Duration("wait", wait))
	}

	s.logger.WarnContext(ctx,
		"The GitHub App used its budget of signed calls. The server refused the check of the connection.", attrs...)
}

func (s *Service) validateInput(in ConnectionInput) error {
	if _, ok := s.openers[in.Kind]; !ok || in.Kind != KindGitHub {
		return fmt.Errorf("%w: the connection kind %q is not supported", ErrInvalid, in.Kind)
	}

	if in.Auth != AuthApp {
		return fmt.Errorf("%w: only the auth kind %q is supported now", ErrInvalid, AuthApp)
	}

	if !ValidNumericID(in.InstallationID) {
		return fmt.Errorf("%w: the installation ID is 1 to %d digits", ErrInvalid, maxIDDigits)
	}

	return nil
}

// requireAssigned returns ErrInstallationNotAssigned unless the map assigns the
// installation to ns. It fails closed: an error of the map also refuses.
func (s *Service) requireAssigned(ctx context.Context, installationID string, ns namespace.Name) error {
	a, err := s.store.GetAssignment(ctx, installationID)
	if err != nil {
		if errors.Is(err, ErrAssignmentNotFound) {
			return fmt.Errorf("%w: installation %s has no assignment", ErrInstallationNotAssigned, installationID)
		}

		return fmt.Errorf("read the installation map: %w", err)
	}

	// Defense in depth: the query matched the installation. This check stays so
	// that a store that returns another assignment cannot give an installation
	// of one namespace to another.
	if a.InstallationID != installationID || a.Namespace != ns {
		return fmt.Errorf("%w: installation %s is not assigned to namespace %s",
			ErrInstallationNotAssigned, installationID, ns)
	}

	return nil
}

// view adds the values of the registration to rec.
func (s *Service) view(ctx context.Context, rec ConnectionRecord) (ConnectionView, error) {
	reg, err := s.store.GetRegistration(ctx, rec.Kind)
	if err != nil && !errors.Is(err, ErrNoRegistration) {
		return ConnectionView{}, fmt.Errorf("get registration: %w", err)
	}

	return viewOf(rec, regPointer(reg, err)), nil
}

// connectionRecord reads the stored connection id of ns. It returns
// ErrConnectionNotFound for an ID that is not valid.
func (s *Service) connectionRecord(ctx context.Context, ns namespace.Name, id string) (ConnectionRecord, error) {
	if !ValidConnectionID(id) {
		return ConnectionRecord{}, ErrConnectionNotFound
	}

	rec, err := s.store.GetConnection(ctx, ns, id)
	if err != nil {
		return ConnectionRecord{}, fmt.Errorf("get connection: %w", err)
	}

	return rec, nil
}

// openWith unseals the key of reg and opens the Forge of rec with the address
// and the app ID of reg.
//
//nolint:ireturn // The port is the interface: the adapter type stays hidden.
func (s *Service) openWith(
	ctx context.Context, e audit.Entry, rec ConnectionRecord, reg *Registration,
) (Forge, error) {
	opener, ok := s.openers[rec.Kind]
	if !ok {
		return nil, fmt.Errorf("%w: the connection kind %q is not supported", ErrInvalid, rec.Kind)
	}

	// Defense in depth: the adapter checks the resolved address of each
	// connection again. This check stays so that an address that the rules
	// refuse never reaches an adapter, whatever the adapter does.
	if err := s.checkURL(reg.BaseURL); err != nil {
		s.refuse(ctx, e, rec, ReasonAddressRefused)

		return nil, fmt.Errorf("%w: %w: %w", ErrAuth, ErrAddressRefused, err)
	}

	// The key opens only with the digest of the address that it was sealed for.
	// The digest is computed from the fields of the row, not read from its
	// digest column: a writer of the database who changes the address and the
	// digest column still cannot open the key for the new address.
	key, err := s.secrets.OpenBound(
		ctx, KeyScope(reg.Kind), []byte(AddressDigest(reg.Kind, reg.BaseURL, reg.AppID)), reg.SealedKey,
	)
	if err != nil {
		s.refuse(ctx, e, rec, ReasonKeyNotOpened)

		return nil, fmt.Errorf("%w: %w: %w", ErrAuth, ErrKeyNotOpened, err)
	}

	conn := Connection{
		ID: rec.ID, Kind: rec.Kind, Auth: rec.Auth, BaseURL: reg.BaseURL,
		Params: map[string]string{ParamAppID: reg.AppID, ParamInstallationID: rec.InstallationID}, Push: rec.Push,
	}

	f, err := opener.Open(ctx, conn, Credential{SetID: reg.KeySetID, Secret: key, CreatedAt: reg.KeyCreatedAt})
	if err != nil {
		return nil, fmt.Errorf("open the connection: %w", err)
	}

	return f, nil
}

// refuse counts and audits a refused Open. Neither the count nor the entry
// holds a part of a credential or an address: the reason names the class of the
// refusal only. A failure of the audit append does not change
// the refusal.
func (s *Service) refuse(ctx context.Context, e audit.Entry, rec ConnectionRecord, reason string) {
	if s.onRefused != nil {
		s.onRefused(reason)
	}

	n, err := s.namespaces.GetNamespace(ctx, rec.Namespace)
	if err != nil {
		// Without the namespace there is no fence for the entry. The refusal
		// stands, and the lost entry is counted like a failed append.
		s.auditLost()

		return
	}

	entry := connectionEntry(e, s.now().UTC(), rec.Namespace, rec.ID, "open")
	entry.Kind = audit.KindSecurity
	entry.Outcome = audit.OutcomeDenied
	entry.Reason = "the server refused to open the connection: " + reason

	s.appendBestEffort(ctx, n.Fence(), entry)
}

// recordCheck appends the entry of a Check. It holds the outcome and no value
// that the Git host answered.
func (s *Service) recordCheck(ctx context.Context, e audit.Entry, ns namespace.Name, id string, checkErr error) {
	n, err := s.namespaces.GetNamespace(ctx, ns)
	if err != nil {
		s.auditLost()

		return
	}

	entry := connectionEntry(e, s.now().UTC(), ns, id, "check")
	entry.Kind = audit.KindReadAccess

	if checkErr != nil {
		entry.Outcome = audit.OutcomeFailure
	}

	s.appendBestEffort(ctx, n.Fence(), entry)
}

// appendBestEffort appends an entry that is not part of a write. The answer of
// the call stands whether or not the entry is written, and a failure runs
// OnAuditFailure.
func (s *Service) appendBestEffort(ctx context.Context, fence namespace.Fence, e audit.Entry) {
	if _, err := s.audit.Append(ctx, fence, e); err != nil {
		s.auditLost()
	}
}

// auditLost runs OnAuditFailure for an entry that the service could not write.
func (s *Service) auditLost() {
	if s.onAuditErr != nil {
		s.onAuditErr()
	}
}

func regPointer(reg Registration, err error) *Registration {
	if err != nil {
		return nil
	}

	return &reg
}

func viewOf(rec ConnectionRecord, reg *Registration) ConnectionView {
	v := ConnectionView{Record: rec, AppID: "", BaseURL: "", HookID: ""}
	if reg != nil && reg.Kind == rec.Kind {
		v.AppID, v.BaseURL, v.HookID = reg.AppID, reg.BaseURL, reg.HookID
	}

	return v
}

// connectionEntry completes the entry e of the caller for a change of the
// connection id in ns. The object is the namespace, and the object ID is the
// connection ID.
func connectionEntry(e audit.Entry, at time.Time, ns namespace.Name, id, verb string) audit.Entry {
	e.At = at
	e.Namespace = ns
	e.Action = "git-host-connection:" + verb
	e.Object = identity.NamespaceObject(ns).String()
	e.ObjectID = id
	e.Kind = audit.KindChange
	e.Outcome = audit.OutcomeSuccess
	e.Changes = slices.Clone(e.Changes)

	return e
}

// change records the new value of a field, with no old value. A value is a
// setting of the connection, never a secret.
func change(key, value string) audit.Change {
	return audit.Change{Key: key, Old: nil, New: value, Layer: "", TakesEffect: ""}
}
