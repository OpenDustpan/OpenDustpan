// Package proxysessionsvc is the service of the job proxy session and of the
// proxy settings of a runner pool. It renders the joined proxy rules of the
// claim of a proxy session, hands the sealed upstream credential of a pool to
// a live proxy session, and reads and saves the proxy settings of a pool. It
// seals the upstream credential with the data key of the namespace, under the
// scope of the stable pool ID.
//
// It checks no grant: the route declares the action of each call. It checks
// the kind of the session and the claim of the session at the point of use.
package proxysessionsvc

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"opendustpan.org/opendustpan/internal/ident"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/durable"
	"opendustpan.org/opendustpan/pkg/egress"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/inventory"
	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/jobproxy"
	"opendustpan.org/opendustpan/pkg/jobproxy/rulestore"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/runner"
	"opendustpan.org/opendustpan/pkg/secret"
)

// Fixed reasons of a refusal. An answer, a log line and an audit entry hold
// these, never the text of a library error, the upstream URL or the
// credential.
const (
	// ReasonWrongSessionKind: a session of another kind called a path of the
	// proxy session.
	ReasonWrongSessionKind = "wrong-session-kind"
	// ReasonRulesNotRenderable: a proxy rule of the unit has a template with
	// no value before the job starts.
	ReasonRulesNotRenderable = "rules-not-renderable"
	// ReasonNoUpstream: the pool names no upstream proxy or no credential.
	ReasonNoUpstream = "no-upstream"
	// ReasonClaimLost: the claim of the session is not live.
	ReasonClaimLost = "claim-lost"
	// ReasonNotJobOfPool: the job of the claim is not a job of the namespace
	// of the pool, or its unit has no stable ID.
	ReasonNotJobOfPool = "not-a-job-of-the-pool"
	// ReasonUnsealFailed: the stored credential does not unseal or fails the
	// check of its text.
	ReasonUnsealFailed = "unseal-failed"
	// ReasonNoProxyOperation: the claim is a sweep, or its job has no proxy
	// operation.
	ReasonNoProxyOperation = "no-proxy-operation"
)

// Sources of the proxy settings of a pool.
const (
	// SourceDefault is the source of a pool with no saved settings.
	SourceDefault = "default"
	// SourceSet is the source of a pool with saved settings.
	SourceSet = "set"
)

// Limits of the upstream credential.
const (
	// MaxUsernameBytes is the longest user name of the upstream credential.
	MaxUsernameBytes = 256
	// MaxPasswordBytes is the longest password of the upstream credential.
	MaxPasswordBytes = 1024
	// maxSealedPlainBytes is the longest sealed form of a credential before
	// the seal: the JSON object with the user name and the password. Both
	// fields at their longest, with each byte escaped in the worst way that
	// the checks allow, stay far below it.
	maxSealedPlainBytes = 8 << 10
)

// maxRetries is the number of times that a save with no If-Match reads the
// version again after a concurrent save. The last write wins.
const maxRetries = 3

// changeEvent is the key of the change that names the event of an entry.
const changeEvent = "event"

var (
	// ErrMissing reports a dependency that the Config of New does not give.
	ErrMissing = errors.New("a dependency is missing")
	// ErrWrongSessionKind reports a session of another kind than proxy.
	ErrWrongSessionKind = errors.New("the session is not a proxy session")
	// ErrRulesNotRenderable reports a proxy rule of the unit that does not
	// render before the job starts. The proxy does not start without rules.
	ErrRulesNotRenderable = errors.New("the proxy rules of the unit cannot be rendered before the job starts")
	// ErrNoUpstream reports a pool with no upstream proxy or no credential.
	ErrNoUpstream = errors.New("the pool has no upstream proxy credential")
	// ErrUnseal reports a credential that does not unseal. The text never
	// holds the credential.
	ErrUnseal = errors.New("the upstream credential cannot be unsealed")
	// ErrSeal reports a credential that the secret service did not seal. The
	// text never holds the credential.
	ErrSeal = errors.New("the upstream credential cannot be sealed")
)

// Sealer seals and unseals values under a scope. *secret.Service meets it.
type Sealer interface {
	Encrypt(ctx context.Context, scope secret.Scope, plaintexts [][]byte) ([][]byte, error)
	Decrypt(ctx context.Context, scope secret.Scope, ciphertexts [][]byte) ([][]byte, error)
}

// Namespaces gives the namespace of a write with its current fence.
// *namespace.Service meets it.
type Namespaces interface {
	// WritableHere returns the namespace called name when this installation
	// may write to it.
	WritableHere(ctx context.Context, name namespace.Name) (namespace.Namespace, error)
}

// Operations reads a durable operation. durable.Store meets it.
type Operations interface {
	// Get returns the operation id, or durable.ErrNotFound.
	Get(ctx context.Context, id durable.ID) (durable.Operation, error)
}

// Claims reads the record of a claim. runner.SessionStore meets it.
type Claims interface {
	// LookupClaim returns the record of the claim c, runner.ErrNotFound or
	// runner.ErrClaimLost.
	LookupClaim(ctx context.Context, c durable.Claim) (runner.ClaimRecord, error)
}

// Jobs reads a job. job.Store meets it.
type Jobs interface {
	// Get returns the job id, or job.ErrNotFound.
	Get(ctx context.Context, id job.ID) (job.Job, error)
}

// Units finds a unit by its stable ID. inventory.Reader meets it.
type Units interface {
	// GetUnit returns the live unit with the stable ID unitID, or
	// inventory.ErrUnitNotFound.
	GetUnit(ctx context.Context, unitID string) (inventory.Entry, error)
}

// Appender appends an audit entry. audit.Store meets it.
type Appender interface {
	Append(ctx context.Context, fence namespace.Fence, e audit.Entry) (audit.Entry, error)
}

// Config holds the dependencies of a Service. Every field except Now is
// required.
type Config struct {
	Settings   runner.PoolProxyStore
	Rules      rulestore.RuleStore
	Secrets    Sealer
	Namespaces Namespaces
	Operations Operations
	Claims     Claims
	Jobs       Jobs
	Units      Units
	Audit      Appender
	// Logger records an audit entry of a refusal that could not be written.
	Logger *slog.Logger
	// Now is the clock. Nil means time.Now.
	Now func() time.Time
}

// Service serves the proxy session and the proxy settings of a pool.
type Service struct {
	settings   runner.PoolProxyStore
	rules      rulestore.RuleStore
	secrets    Sealer
	namespaces Namespaces
	operations Operations
	claims     Claims
	jobs       Jobs
	units      Units
	audit      Appender
	logger     *slog.Logger
	now        func() time.Time
}

// New returns a Service. It returns ErrMissing when cfg lacks a dependency.
func New(cfg Config) (*Service, error) {
	for _, dep := range []struct {
		set  bool
		name string
	}{
		{cfg.Settings != nil, "the pool proxy store"},
		{cfg.Rules != nil, "the rule store"},
		{cfg.Secrets != nil, "the secret service"},
		{cfg.Namespaces != nil, "the namespace service"},
		{cfg.Operations != nil, "the durable operations"},
		{cfg.Claims != nil, "the claim records"},
		{cfg.Jobs != nil, "the job reader"},
		{cfg.Units != nil, "the unit reader"},
		{cfg.Audit != nil, "the audit store"},
		{cfg.Logger != nil, "the logger"},
	} {
		if !dep.set {
			return nil, fmt.Errorf("proxy session service: %w: %s", ErrMissing, dep.name)
		}
	}

	now := cfg.Now
	if now == nil {
		now = time.Now
	}

	return &Service{
		settings: cfg.Settings, rules: cfg.Rules, secrets: cfg.Secrets, namespaces: cfg.Namespaces,
		operations: cfg.Operations, claims: cfg.Claims, jobs: cfg.Jobs, units: cfg.Units, audit: cfg.Audit,
		logger: cfg.Logger, now: now,
	}, nil
}

// Credential is the upstream proxy credential of a pool.
type Credential struct {
	Username string
	Password string
}

// CredentialChange is the change of the credential in a save. With Present
// false, the save keeps the stored credential. With Present true, a nil Value
// removes it and a Value replaces it.
type CredentialChange struct {
	Present bool
	Value   *Credential
}

// Settings is the effective proxy settings of a pool. It never holds the
// credential: CredentialSet says whether the pool has one.
type Settings struct {
	AllowedRanges []string
	UpstreamURL   string
	CredentialSet bool
	// Source is SourceDefault for a pool with no saved settings, or SourceSet.
	Source    string
	Version   int64
	UpdatedBy string
	UpdatedAt time.Time
}

// PutRequest is one save of the proxy settings of a pool.
type PutRequest struct {
	Pool          runner.Pool
	AllowedRanges []string
	UpstreamURL   string
	Credential    CredentialChange
	// IfMatch is the version that the writer read, in decimal. When it is not
	// empty and it is not the stored version, the save fails with
	// runner.ErrConflict and changes nothing. When it is empty, the last write
	// wins.
	IfMatch string
	// Entry is the audit entry of the caller. The service sets the time, the
	// namespace, the action, the kind and the changes.
	Entry audit.Entry
}

// SessionRequest names a proxy session and the pool of its runner. Entry is
// the audit entry of the caller: the service sets the time, the namespace, the
// action, the kind and the changes.
type SessionRequest struct {
	Session runner.Session
	Pool    runner.Pool
	Entry   audit.Entry
}

// AssignmentRequest is the read of the proxy assignment of a proxy session.
type AssignmentRequest struct {
	SessionRequest

	// ServerHost is the host of this server, for the tunnel rule.
	ServerHost string
}

// Rule is one rendered proxy rule with the scope that holds it.
type Rule struct {
	jobproxy.RenderedRule

	// Scope is "namespace" or "unit".
	Scope string
}

// Assignment is the proxy assignment of a proxy session. It holds no secret.
type Assignment struct {
	Namespace     namespace.Name
	Unit          string
	Operation     jobproxy.Operation
	Rules         []Rule
	RulesDigest   string
	ServerHost    string
	AllowedRanges []string
	UpstreamURL   string
}

// Settings returns the effective proxy settings of pool. A pool with no saved
// settings has no range, no upstream proxy and the source SourceDefault.
func (s *Service) Settings(ctx context.Context, pool runner.Pool) (Settings, error) {
	rec, found, err := s.record(ctx, pool.ID)
	if err != nil {
		return Settings{}, err
	}

	source := SourceDefault
	if found {
		source = SourceSet
	}

	return settingsOf(rec, source), nil
}

// PutSettings saves the proxy settings of req.Pool and returns the effective
// settings. It returns runner.ErrInvalid for a range, an upstream URL or a
// credential that the checks refuse, runner.ErrConflict for a stale IfMatch,
// runner.ErrNotFound for a pool that does not exist, and namespace.ErrFenced
// for a stale fence. The store writes the audit entry in the transaction of
// the save, so a save with no entry does not happen.
func (s *Service) PutSettings(ctx context.Context, req *PutRequest) (Settings, error) {
	settings, err := runner.NewProxySettings(req.AllowedRanges, req.UpstreamURL)
	if err != nil {
		return Settings{}, fmt.Errorf("save proxy settings: %w", err)
	}

	sealed, err := s.sealChange(ctx, req.Pool, req.Credential)
	if err != nil {
		return Settings{}, err
	}

	writable, err := s.namespaces.WritableHere(ctx, req.Pool.Namespace)
	if err != nil {
		return Settings{}, fmt.Errorf("save proxy settings: %w", err)
	}

	for range maxRetries {
		stored, putErr := s.saveOnce(ctx, req, writable.Fence(), settings, sealed)

		switch {
		case putErr == nil:
			return settingsOf(stored, SourceSet), nil
		case errors.Is(putErr, runner.ErrConflict) && req.IfMatch == "":
			// Another save came between the read and the write. With no
			// If-Match, the last write wins: read the version again.
			continue
		default:
			return Settings{}, fmt.Errorf("save proxy settings: %w", putErr)
		}
	}

	return Settings{}, fmt.Errorf("save proxy settings: %w", runner.ErrConflict)
}

// Assignment returns the proxy assignment of the proxy session of req: the
// rendered joined proxy rules of the unit of the claim of the session, the
// ranges and the upstream URL of the pool. It reads the claim from the
// session, never from the request. It returns ErrWrongSessionKind for a
// session of another kind, runner.ErrClaimLost for a claim that is not live,
// and ErrRulesNotRenderable for a rule with a template that has no value. A
// refusal and a success each write an audit entry.
func (s *Service) Assignment(ctx context.Context, req *AssignmentRequest) (Assignment, error) {
	claim, err := s.check(ctx, &req.SessionRequest)
	if err != nil {
		return Assignment{}, err
	}

	settings, err := s.Settings(ctx, req.Pool)
	if err != nil {
		return Assignment{}, err
	}

	rendered, rules, err := s.render(ctx, claim, settings.AllowedRanges)
	if err != nil {
		s.refuse(ctx, &req.SessionRequest, claim.job.ID, ReasonRulesNotRenderable)

		return Assignment{}, err
	}

	out := Assignment{
		Namespace: claim.unit.Namespace(), Unit: claim.unit.String(), Operation: claim.operation, Rules: rules,
		RulesDigest: "sha256:" + rendered.Digest(), ServerHost: req.ServerHost, AllowedRanges: settings.AllowedRanges,
		UpstreamURL: settings.UpstreamURL,
	}

	e := s.sessionEntry(&req.SessionRequest, runner.EventProxySessionOpened, claim)
	e.Changes = append(e.Changes,
		change("unitID", claim.unit.UnitID()),
		change("rulesDigest", out.RulesDigest),
		change("ruleCount", strconv.Itoa(len(rules))),
		change("upstreamSet", strconv.FormatBool(settings.UpstreamURL != "")),
	)

	if appendErr := s.append(ctx, req.Pool.Namespace, e); appendErr != nil {
		return Assignment{}, appendErr
	}

	return out, nil
}

// Credential returns the upstream credential of the pool of the proxy
// session of req, once for each call. It returns ErrWrongSessionKind for a
// session of another kind, runner.ErrClaimLost for a claim that is not live,
// ErrNoUpstream for a pool with no upstream proxy or no credential, and
// ErrUnseal for a credential that does not unseal. Each call writes an audit
// entry of the kind reveal, and a refusal writes a denied entry. No error and
// no entry holds the credential or the upstream URL.
func (s *Service) Credential(ctx context.Context, req *SessionRequest) (Credential, error) {
	claim, err := s.check(ctx, req)
	if err != nil {
		return Credential{}, err
	}

	rec, found, err := s.record(ctx, req.Pool.ID)
	if err != nil {
		return Credential{}, err
	}

	if !found || len(rec.Credential) == 0 || rec.Settings.UpstreamURL() == "" {
		s.refuse(ctx, req, claim.job.ID, ReasonNoUpstream)

		return Credential{}, ErrNoUpstream
	}

	cred, err := s.unseal(ctx, req.Pool, rec.Credential)
	if err != nil {
		s.refuse(ctx, req, claim.job.ID, ReasonUnsealFailed)

		return Credential{}, err
	}

	e := s.sessionEntry(req, runner.EventProxyCredentialIssued, claim)
	e.Kind = audit.KindReveal
	e.Changes = append(e.Changes, change("pool", req.Pool.ID), change("session", req.Session.ID))

	// Fail closed: a reveal with no record does not happen.
	if appendErr := s.append(ctx, req.Pool.Namespace, e); appendErr != nil {
		return Credential{}, appendErr
	}

	return cred, nil
}

// saveOnce reads the stored settings of req.Pool, checks req.IfMatch, and
// writes the next record with its audit entry.
func (s *Service) saveOnce(
	ctx context.Context, req *PutRequest, fence namespace.Fence, settings runner.ProxySettings, sealed []byte,
) (runner.ProxySettingsRecord, error) {
	current, _, err := s.record(ctx, req.Pool.ID)
	if err != nil {
		return runner.ProxySettingsRecord{}, err
	}

	if req.IfMatch != "" && req.IfMatch != strconv.FormatInt(current.Version, 10) {
		return runner.ProxySettingsRecord{}, runner.ErrConflict
	}

	next := runner.ProxySettingsRecord{
		Settings: settings, Credential: current.Credential, Version: current.Version,
		UpdatedBy: req.Entry.Subject, UpdatedAt: s.now().UTC().Truncate(time.Millisecond),
	}

	if req.Credential.Present {
		next.Credential = sealed
	}

	// Fail closed: a credential with no upstream proxy has no use, so the save
	// refuses it and keeps the credential out of the row.
	if len(next.Credential) != 0 && settings.UpstreamURL() == "" {
		return runner.ProxySettingsRecord{}, fmt.Errorf("%w: a credential needs an upstream URL", runner.ErrInvalid)
	}

	return s.settings.SetProxySettings(ctx, fence, req.Pool.ID, next, s.settingsEntry(req, &current, &next))
}

// sessionClaim is the claim of a proxy session, as the service read it.
type sessionClaim struct {
	op        durable.Operation
	record    runner.ClaimRecord
	job       job.Job
	unit      identity.Object
	operation jobproxy.Operation
}

// check checks the kind of the session and that its claim is live, and
// returns the claim. A refusal writes a denied entry.
func (s *Service) check(ctx context.Context, req *SessionRequest) (*sessionClaim, error) {
	// Defense in depth: the route wrapper checks the kind of the session too;
	// this check stays so that a new caller cannot give a launcher session the
	// rules or the credential.
	if req.Session.Kind != runner.SessionKindProxy {
		s.refuse(ctx, req, "", ReasonWrongSessionKind)

		return nil, ErrWrongSessionKind
	}

	claim, reason, err := s.claimOf(ctx, req)
	if reason != "" {
		// A refusal of the claim writes a denied entry. A read error of a
		// store has no reason and writes none.
		s.refuse(ctx, req, "", reason)
	}

	return claim, err
}

// claimOf reads the live claim of the session of req, its job and its unit.
// For a refusal it returns the fixed reason of the denied entry too; for a
// read error of a store the reason is empty.
func (s *Service) claimOf(ctx context.Context, req *SessionRequest) (*sessionClaim, string, error) {
	var none *sessionClaim

	sess := &req.Session

	op, err := s.operations.Get(ctx, durable.ID(sess.Operation))
	if errors.Is(err, durable.ErrNotFound) {
		return none, ReasonClaimLost, runner.ErrClaimLost
	}

	if err != nil {
		return none, "", fmt.Errorf("get operation: %w", err)
	}

	// Defense in depth: the session check of the route reads the claim too;
	// this check stays so that a session of an ended claim never gets the rules
	// or the credential.
	if op.State != durable.StateRunning || op.Claim.Owner != sess.Runner || op.Claim.Token != sess.Token {
		return none, ReasonClaimLost, runner.ErrClaimLost
	}

	rec, err := s.claims.LookupClaim(ctx, op.Claim)
	if err != nil {
		return none, "", fmt.Errorf("look up claim: %w", err)
	}

	if rec.Job == "" {
		return none, ReasonNoProxyOperation, fmt.Errorf(
			"%w: the claim of a sweep has no proxy session",
			runner.ErrInvalid,
		)
	}

	j, err := s.jobs.Get(ctx, job.ID(rec.Job))
	if err != nil {
		return none, "", fmt.Errorf("get job: %w", err)
	}

	unit := j.Spec.Unit

	// Fail closed: a job of another namespace than the pool, or with no stable
	// unit ID, gets no rules.
	if unit.Namespace() != req.Pool.Namespace || unit.UnitID() == "" {
		return none, ReasonNotJobOfPool, fmt.Errorf("%w: the job is not a job of the pool", runner.ErrNotFound)
	}

	operation := jobproxy.Operation(j.Spec.Operation)
	if !operation.Valid() {
		return none, ReasonNoProxyOperation, fmt.Errorf("%w: the job has no proxy operation", runner.ErrInvalid)
	}

	return &sessionClaim{op: op, record: rec, job: j, unit: unit, operation: operation}, "", nil
}

// render joins the proxy rule sets of the namespace and of the unit of claim
// and renders them with the variables of the claim. A template with no value
// fails with ErrRulesNotRenderable.
func (s *Service) render(
	ctx context.Context, claim *sessionClaim, ranges []string,
) (jobproxy.Rendered, []Rule, error) {
	allowed, err := egress.ParseAllowedRanges(ranges)
	if err != nil {
		return jobproxy.Rendered{}, nil, fmt.Errorf("%w: the ranges of the pool: %w", runner.ErrInvalid, err)
	}

	ns := claim.unit.Namespace()
	scopes := map[string]string{}

	var sets []jobproxy.RuleSet

	for _, sc := range []struct {
		label string
		scope rulestore.Scope
	}{
		{"namespace", rulestore.Scope{Namespace: ns, UnitID: ""}},
		{"unit", rulestore.Scope{Namespace: ns, UnitID: claim.unit.UnitID()}},
	} {
		set, found, readErr := s.ruleSet(ctx, sc.scope, allowed)
		if readErr != nil {
			return jobproxy.Rendered{}, nil, readErr
		}

		if !found {
			continue
		}

		for i := range set.Rules {
			scopes[set.Rules[i].ID] = sc.label
		}

		sets = append(sets, set)
	}

	joined, err := jobproxy.Join(sets...)
	if err != nil {
		return jobproxy.Rendered{}, nil, fmt.Errorf("%w: %s", ErrRulesNotRenderable, ReasonRulesNotRenderable)
	}

	vars, err := s.varsOf(ctx, claim)
	if err != nil {
		return jobproxy.Rendered{}, nil, err
	}

	rendered, err := jobproxy.Render(joined, vars)
	if err != nil {
		// The error of Render names the rule and the variable. The answer holds
		// the fixed reason only.
		return jobproxy.Rendered{}, nil, fmt.Errorf("%w: %s", ErrRulesNotRenderable, ReasonRulesNotRenderable)
	}

	rules := rendered.Rules()
	out := make([]Rule, 0, len(rules))

	for i := range rules {
		out = append(out, Rule{Scope: scopes[rules[i].ID], RenderedRule: rules[i]})
	}

	return rendered, out, nil
}

// ruleSet returns the parsed proxy rule set of scope, and false for a scope
// with no set.
func (s *Service) ruleSet(
	ctx context.Context, scope rulestore.Scope, allowed []netip.Prefix,
) (jobproxy.RuleSet, bool, error) {
	stored, err := s.rules.Get(ctx, scope)
	if errors.Is(err, rulestore.ErrNotFound) {
		return jobproxy.RuleSet{Rules: nil}, false, nil
	}

	if err != nil {
		return jobproxy.RuleSet{}, false, fmt.Errorf("read proxy rule set: %w", err)
	}

	// Defense in depth: the store parsed the document at the save. This check
	// stays so that a stored document never reaches a proxy unchecked.
	set, err := jobproxy.Parse(stored.Document, allowed...)
	if err != nil {
		return jobproxy.RuleSet{}, false, fmt.Errorf("%w: %s", ErrRulesNotRenderable, ReasonRulesNotRenderable)
	}

	return set, true, nil
}

// varsOf returns the variables of the job of claim before the job starts:
// the unit, its labels and the operation. The inputs and the profiles have no
// value yet, so a template that needs one does not render.
func (s *Service) varsOf(ctx context.Context, claim *sessionClaim) (jobproxy.Vars, error) {
	unit := claim.unit

	entry, err := s.units.GetUnit(ctx, unit.UnitID())
	if err != nil {
		return jobproxy.Vars{}, fmt.Errorf("find unit: %w", err)
	}

	// Defense in depth: the inventory finds a unit by its ID alone. A unit of
	// another namespace gives no labels to this claim.
	if entry.Namespace != unit.Namespace() {
		return jobproxy.Vars{}, fmt.Errorf("find unit: %w", inventory.ErrUnitNotFound)
	}

	return jobproxy.Vars{
		Tool: unit.Tool(), Namespace: unit.Namespace().String(), Name: unit.UnitName(), Labels: entry.Labels,
		Operation: claim.operation, Inputs: nil, Profiles: nil,
	}, nil
}

// record returns the stored settings of the pool poolID, and false for a pool
// with no row.
func (s *Service) record(ctx context.Context, poolID string) (runner.ProxySettingsRecord, bool, error) {
	rec, err := s.settings.ProxySettings(ctx, poolID)
	if errors.Is(err, runner.ErrNotFound) {
		return runner.ProxySettingsRecord{
			Settings: runner.ProxySettings{}, Credential: nil, Version: 0, UpdatedBy: "", UpdatedAt: time.Time{},
		}, false, nil
	}

	if err != nil {
		return runner.ProxySettingsRecord{}, false, fmt.Errorf("read proxy settings: %w", err)
	}

	return rec, true, nil
}

// settingsOf returns the effective settings of rec with the source.
func settingsOf(rec runner.ProxySettingsRecord, source string) Settings {
	ranges := rec.Settings.AllowedRanges()
	if ranges == nil {
		ranges = []string{}
	}

	return Settings{
		AllowedRanges: ranges, UpstreamURL: rec.Settings.UpstreamURL(), CredentialSet: len(rec.Credential) != 0,
		Source: source, Version: rec.Version, UpdatedBy: rec.UpdatedBy, UpdatedAt: rec.UpdatedAt,
	}
}

// scopeOf returns the seal scope of the credential of pool: the namespace of
// the pool and its stable ID. A credential of one pool does not unseal under
// another pool.
func scopeOf(pool runner.Pool) (secret.Scope, error) {
	scope, err := secret.NewScope(pool.Namespace, pool.ID)
	if err != nil {
		return secret.Scope{}, fmt.Errorf("%w: the seal scope of the pool", runner.ErrInvalid)
	}

	return scope, nil
}

// sealChange checks and seals the credential of change, and returns nil for
// no credential.
func (s *Service) sealChange(ctx context.Context, pool runner.Pool, change CredentialChange) ([]byte, error) {
	if !change.Present || change.Value == nil {
		return nil, nil
	}

	if err := checkCredential(change.Value); err != nil {
		return nil, err
	}

	plain, err := json.Marshal(map[string]string{
		credentialUser: change.Value.Username, credentialPass: change.Value.Password,
	})
	if err != nil {
		return nil, fmt.Errorf("%w: the credential", runner.ErrInvalid)
	}

	if sizeErr := CheckSealedSize(plain); sizeErr != nil {
		return nil, sizeErr
	}

	scope, err := scopeOf(pool)
	if err != nil {
		return nil, err
	}

	sealed, err := s.secrets.Encrypt(ctx, scope, [][]byte{plain})
	if err != nil || len(sealed) != 1 {
		// The answer holds a fixed text only.
		return nil, ErrSeal
	}

	return sealed[0], nil
}

// CheckSealedSize is the size check of the sealed form of a credential, before
// the seal. It refuses a sealed form that is longer than maxSealedPlainBytes,
// and so keeps the size of the value that the secret service allocates
// bounded.
//
// Defense in depth: the body limit of the route (16 KiB) and checkCredential
// (user name and password lengths) bound the value too; this check stays so
// that a new caller or a change of those limits cannot skip the bound. It
// does not rely on either of them.
func CheckSealedSize(plain []byte) error {
	if len(plain) > maxSealedPlainBytes {
		return fmt.Errorf("%w: the credential is longer than %d bytes", runner.ErrInvalid, maxSealedPlainBytes)
	}

	return nil
}

// Keys of the sealed form of the credential: a JSON object with the user name
// and the password.
const (
	credentialUser = "username"
	credentialPass = "password"
)

// unseal opens the sealed credential of pool.
func (s *Service) unseal(ctx context.Context, pool runner.Pool, sealed []byte) (Credential, error) {
	scope, err := scopeOf(pool)
	if err != nil {
		return Credential{}, err
	}

	plain, err := s.secrets.Decrypt(ctx, scope, [][]byte{sealed})
	if err != nil || len(plain) != 1 {
		return Credential{}, ErrUnseal
	}

	var fields map[string]string
	if err = json.Unmarshal(plain[0], &fields); err != nil || len(fields) != 2 {
		return Credential{}, ErrUnseal
	}

	cred := Credential{Username: fields[credentialUser], Password: fields[credentialPass]}

	// Defense in depth: the save checks the credential. This check stays so
	// that a row from a restore or another version never reaches a proxy
	// unchecked.
	if err = checkCredential(&cred); err != nil {
		return Credential{}, ErrUnseal
	}

	return cred, nil
}

// checkCredential checks the user name and the password of c. The errors
// never repeat a value.
func checkCredential(c *Credential) error {
	switch {
	case c.Username == "" || len(c.Username) > MaxUsernameBytes:
		return fmt.Errorf("%w: the user name of the credential is 1 to %d bytes", runner.ErrInvalid, MaxUsernameBytes)
	case len(c.Password) > MaxPasswordBytes:
		return fmt.Errorf("%w: the password of the credential is at most %d bytes", runner.ErrInvalid, MaxPasswordBytes)
	case !utf8.ValidString(c.Username) || !utf8.ValidString(c.Password):
		return fmt.Errorf("%w: the credential is not valid text", runner.ErrInvalid)
	case strings.Contains(c.Username, ":"):
		return fmt.Errorf("%w: the user name of the credential holds no colon", runner.ErrInvalid)
	case strings.ContainsFunc(c.Username+c.Password, ident.IsHiddenRune):
		return fmt.Errorf("%w: the credential has a control, format or separator character", runner.ErrInvalid)
	}

	return nil
}

// settingsEntry returns the audit entry of a save. It holds the old and the
// new ranges and versions, the hashes of the old and the new upstream URL and
// whether the credential changed. It never holds the URL or the credential.
func (s *Service) settingsEntry(req *PutRequest, old, next *runner.ProxySettingsRecord) audit.Entry {
	rule := runner.AuditRules()[runner.EventPoolProxySettingsChanged]

	oldRanges := strings.Join(old.Settings.AllowedRanges(), ",")
	oldURL := urlHash(old.Settings.UpstreamURL())
	oldVersion := strconv.FormatInt(old.Version, 10)

	e := req.Entry
	e.At = next.UpdatedAt
	e.Namespace = req.Pool.Namespace
	e.Action = string(rule.Action)
	e.Kind = rule.Kind
	e.ObjectID = req.Pool.ID
	e.Changes = append(slices.Clone(req.Entry.Changes),
		change(changeEvent, string(runner.EventPoolProxySettingsChanged)),
		change("pool", req.Pool.ID),
		audit.Change{
			Key: "allowedRanges", Old: &oldRanges, New: strings.Join(next.Settings.AllowedRanges(), ","),
			Layer: "", TakesEffect: "",
		},
		audit.Change{
			Key: "upstreamURLSHA256", Old: &oldURL, New: urlHash(next.Settings.UpstreamURL()),
			Layer: "", TakesEffect: "",
		},
		change("credentialChanged", strconv.FormatBool(req.Credential.Present)),
		audit.Change{
			Key: "version", Old: &oldVersion, New: strconv.FormatInt(old.Version+1, 10), Layer: "", TakesEffect: "",
		},
	)

	return e
}

// sessionEntry returns the audit entry of event for the session of req and
// its claim.
func (s *Service) sessionEntry(req *SessionRequest, event runner.Event, claim *sessionClaim) audit.Entry {
	rule := runner.AuditRules()[event]

	e := req.Entry
	e.At = s.now().UTC()
	e.Namespace = req.Pool.Namespace
	e.Action = string(rule.Action)
	e.Kind = rule.Kind
	e.Outcome = audit.OutcomeSuccess
	e.JobID = string(claim.job.ID)
	e.Changes = append(slices.Clone(req.Entry.Changes),
		change(changeEvent, string(event)),
		change("phase", strconv.Itoa(claim.record.Phase)),
		change("claim", string(claim.op.ID)),
	)

	return e
}

// refuse writes the denied entry of a refusal of the session of req with the
// fixed reason. It returns nothing: the refusal stays when the append fails.
func (s *Service) refuse(ctx context.Context, req *SessionRequest, jobID job.ID, reason string) {
	rule := runner.AuditRules()[runner.EventHandoffRefused]

	e := req.Entry
	e.At = s.now().UTC()
	e.Namespace = req.Pool.Namespace
	e.Action = string(rule.Action)
	e.Kind = rule.Kind
	e.Outcome = audit.OutcomeDenied
	e.Reason = reason
	e.JobID = string(jobID)
	e.Changes = append(slices.Clone(req.Entry.Changes),
		change(changeEvent, string(runner.EventHandoffRefused)),
		change("reason", reason),
		change("kind", string(req.Session.Kind)),
	)

	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), refusalTimeout)
	defer cancel()

	if err := s.append(ctx, req.Pool.Namespace, e); err != nil {
		// The record holds the fixed reason, never the session ID or a value
		// of the request.
		s.logger.WarnContext(ctx, "audit entry of a proxy session refusal not written",
			slog.String("reason", reason), slog.String("error", err.Error()))
	}
}

// refusalTimeout bounds the append of a denied entry after the request ends.
const refusalTimeout = 5 * time.Second

// append writes e in the namespace ns with its current fence.
func (s *Service) append(ctx context.Context, ns namespace.Name, e audit.Entry) error {
	writable, err := s.namespaces.WritableHere(ctx, ns)
	if err != nil {
		return fmt.Errorf("append audit entry: %w", err)
	}

	if _, err = s.audit.Append(ctx, writable.Fence(), e); err != nil {
		return fmt.Errorf("append audit entry: %w", err)
	}

	return nil
}

// change returns a change with no old value.
func change(key, value string) audit.Change {
	return audit.Change{Key: key, Old: nil, New: value, Layer: "", TakesEffect: ""}
}

// urlHash returns the hex SHA-256 of an upstream URL, or empty for none. An
// audit entry holds the hash, never the URL.
func urlHash(u string) string {
	if u == "" {
		return ""
	}

	sum := sha256.Sum256([]byte(u))

	return hex.EncodeToString(sum[:])
}
